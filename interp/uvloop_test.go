package interp

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/core"
)

func newHandle(vm *VM) *Pointer {
	return vm.voidPtr(&Memory{Type: memUInt8Type, Elems: make([]any, 64)})
}

// callback is a cfunction pointer whose function is f.
func callback(vm *VM, f func(args []any) error) *Pointer {
	b := &Builtin{Name: "callback", Fn: func(vm *VM, args []any) (any, error) { return nil, f(args) }}
	return vm.voidPtr(&cfunction{fn: b, rt: core.NothingType, argt: SVec{vm.ptrType(core.NothingType)}})
}

func u64(n uint64) uint64 { return n }

// timer makes a handle, initialised as a timer started with cb.
func timer(t *testing.T, vm *VM, cb *Pointer, timeout, repeat uint64) *Pointer {
	t.Helper()
	h := newHandle(vm)
	if r, err := shim1(vm, "uv_timer_init", vm.voidPtr(vm.uv()), h); err != nil || r != int32(0) {
		t.Fatalf("uv_timer_init: %v %v", r, err)
	}
	if r, err := shim1(vm, "uv_timer_start", h, cb, timeout, repeat); err != nil || r != int32(0) {
		t.Fatalf("uv_timer_start: %v %v", r, err)
	}
	return h
}

func async(t *testing.T, vm *VM, cb *Pointer) *Pointer {
	t.Helper()
	h := newHandle(vm)
	if r, err := shim1(vm, "uv_async_init", vm.voidPtr(vm.uv()), h, cb); err != nil || r != int32(0) {
		t.Fatalf("uv_async_init: %v %v", r, err)
	}
	return h
}

func rec(vm *VM, h *Pointer) *uvHandle { return vm.uv().handles[h.Target.(*Memory)] }

// The timers due are run first due first and, when due together, the one
// started first; a repeating timer is armed again before its callback runs; a
// callback that stops a timer, or starts it again, takes it out of this pass.
func TestUVRunTimers(t *testing.T) {
	vm := New()
	l := vm.uv()
	var log []string
	mk := func(name string, due, id uint64, repeat uint64, extra func()) *Pointer {
		h := timer(t, vm, callback(vm, func([]any) error {
			log = append(log, name)
			if extra != nil {
				extra()
			}
			return nil
		}), 1, repeat)
		r := rec(vm, h)
		r.due, r.startID = due, id
		return h
	}
	l.now = 100
	var b, d, e *Pointer
	mk("a", 100, 5, 7, nil)
	b = mk("b", 100, 2, 0, func() { shim1(vm, "uv_timer_stop", d) })
	mk("c", 90, 9, 0, nil)
	d = mk("d", 100, 3, 0, nil)
	e = mk("e", 100, 4, 0, func() { shim1(vm, "uv_timer_start", d, callback(vm, func([]any) error { return nil }), u64(1), u64(0)) })
	_ = b
	mk("f", 101, 1, 0, nil) // not due
	if err := vm.runTimers(l); err != nil {
		t.Fatal(err)
	}
	// c is older, then b (id 2), d (id 3) which b stopped, e (id 4), a (id 5)
	if got := strings.Join(log, ""); got != "cbea" {
		t.Errorf("order %q, want cbea (d was stopped by b, f is not due)", got)
	}
	a := l.order[0]
	if !a.active || a.due != 107 || a.startID != l.timerID-1 {
		t.Errorf("the repeating timer: active %v due %d start %d (timerID %d)", a.active, a.due, a.startID, l.timerID)
	}
	if rec(vm, e).active {
		t.Error("a one-shot timer is still active after its callback")
	}
	// d was started again by e's callback: its old arming is not run, but the
	// new one is, in the next pass
	if !rec(vm, d).active {
		t.Error("the timer started by a callback is not armed")
	}
	// a callback that fails stops the pass with its error
	boom := errors.New("boom")
	timer(t, vm, callback(vm, func([]any) error { return boom }), 0, 0)
	l.updateTime()
	l.now += 1000
	if err := vm.runTimers(l); !errors.Is(err, boom) {
		t.Errorf("a failing callback: %v", err)
	}
}

// A pass that waits wakes for the first of a timer and an async send, which
// may come from another goroutine.
func TestUVAsyncWakesTheLoop(t *testing.T) {
	vm := New()
	var log []string
	timer(t, vm, callback(vm, func([]any) error { log = append(log, "timer"); return nil }), 5000, 0)
	h := async(t, vm, callback(vm, func(args []any) error { log = append(log, "async"); return nil }))
	go func() {
		time.Sleep(20 * time.Millisecond)
		vm.uv().asyncSend(rec(vm, h))
	}()
	start := time.Now()
	alive, err := vm.uvRun()
	if err != nil || !alive {
		t.Fatalf("uvRun: %v %v", alive, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the pass waited %v for the timer", d)
	}
	if strings.Join(log, ",") != "async" {
		t.Errorf("log %v", log)
	}
	// a send before the pass is run by it, without waiting; a send to a handle
	// that is closing is dropped
	vm.uv().asyncSend(rec(vm, h))
	start = time.Now()
	if err := vm.uvIterate(true); err != nil || time.Since(start) > time.Second {
		t.Errorf("a pending send: %v after %v", err, time.Since(start))
	}
	rec(vm, h).closing = true
	vm.uv().asyncSend(rec(vm, h))
	if got := vm.uv().takePending(); len(got) != 0 {
		t.Errorf("a closing handle is pending: %v", got)
	}
	if strings.Join(log, ",") != "async,async" {
		t.Errorf("log %v", log)
	}
}

// uv_run does not return, and does not wait, when nothing can end the wait: an
// async handle alone cannot (no goroutine of the VM sends), an unreferenced
// handle does not keep the loop alive, an unreferenced timer ends a wait that
// a referenced handle keeps.
func TestUVRunAliveAndForever(t *testing.T) {
	vm := New()
	if alive, err := vm.uvRun(); alive || err != nil {
		t.Errorf("an empty loop: %v %v", alive, err)
	}
	h := async(t, vm, callback(vm, func([]any) error { return nil }))
	if alive, err := vm.uvRun(); alive || !errors.Is(err, errLoopForever) {
		t.Errorf("a referenced async handle alone: %v %v", alive, err)
	}
	// unreferenced: the loop is not alive
	if _, err := shim1(vm, "uv_unref", h); err != nil {
		t.Fatal(err)
	}
	if alive, err := vm.uvRun(); alive || err != nil {
		t.Errorf("an unreferenced handle: %v %v", alive, err)
	}
	// referenced again, with an unreferenced timer: the pass waits for the timer
	shim1(vm, "uv_ref", h)
	fired := false
	tm := timer(t, vm, callback(vm, func([]any) error { fired = true; return nil }), 10, 0)
	shim1(vm, "uv_unref", tm)
	if alive, err := vm.uvRun(); err != nil || !fired || !alive {
		t.Errorf("a timer under a referenced handle: alive %v, %v, fired %v", alive, err, fired)
	}
	// a pass that does not wait, with nothing due
	if err := vm.uvIterate(false); err != nil {
		t.Error(err)
	}
	// a handle that waits for its close callback is alive and does not make
	// the pass wait, though a timer is armed
	timer(t, vm, callback(vm, func([]any) error { return nil }), 60000, 0)
	shim1(vm, "jl_close_uv", h)
	start := time.Now()
	if alive, err := vm.uvRun(); err != nil || !alive || time.Since(start) > 5*time.Second {
		t.Errorf("a pass with a closing handle: %v %v after %v", alive, err, time.Since(start))
	}
}

// The loop is not reentrant, as libuv's is not.
func TestUVRunReentrant(t *testing.T) {
	vm := New()
	var inner error
	timer(t, vm, callback(vm, func([]any) error { inner = vm.uvIterate(false); return nil }), 0, 0)
	if _, err := vm.uvRun(); err != nil {
		t.Fatal(err)
	}
	if inner == nil || !strings.Contains(inner.Error(), "already running") {
		t.Errorf("a pass inside a callback: %v", inner)
	}
}

// What the C functions of handles refuse and answer.
func TestUVShims(t *testing.T) {
	vm := New()
	loop := vm.voidPtr(vm.uv())
	notHandle := []any{int64(1)}
	offset := &Pointer{Type: vm.ptrType(core.NothingType), Target: &Memory{Type: memUInt8Type, Elems: make([]any, 8)}, Off: 4}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"uv_async_init", []any{loop, notHandle[0], nil}},
		{"uv_timer_init", []any{loop, offset}},
		{"uv_async_send", []any{notHandle[0]}},
		{"uv_timer_start", []any{notHandle[0], nil, u64(1), u64(0)}},
		{"uv_timer_stop", []any{notHandle[0]}},
		{"uv_timer_get_repeat", []any{notHandle[0]}},
		{"uv_ref", []any{notHandle[0]}},
		{"uv_unref", []any{notHandle[0]}},
		{"uv_is_active", []any{notHandle[0]}},
		{"uv_handle_get_data", []any{notHandle[0]}},
		{"uv_handle_set_data", []any{notHandle[0], nil}},
		{"jl_uv_associate_julia_struct", []any{notHandle[0], nil}},
		{"jl_uv_disassociate_julia_struct", []any{notHandle[0]}},
		{"jl_close_uv", []any{notHandle[0]}},
		// a handle nothing initialised is not known to the loop
		{"uv_is_active", []any{newHandle(vm)}},
	} {
		_, err := shim1(vm, c.name, c.args...)
		check(t, c.name, nil, err, "!not ported yet: "+c.name+" of the handle")
	}
	h := newHandle(vm)
	if _, err := shim1(vm, "uv_timer_init", loop, h); err != nil {
		t.Fatal(err)
	}
	cb := callback(vm, func([]any) error { return nil })
	// uv_timer_start: the numbers must be UInt64s; a NULL or missing callback
	// and a closing handle are UV_EINVAL
	if _, err := shim1(vm, "uv_timer_start", h, cb, int64(1), u64(0)); err == nil {
		t.Error("uv_timer_start with an Int timeout")
	}
	for _, c := range []struct {
		name string
		cb   any
	}{{"a NULL callback", vm.voidPtr(nil)}, {"no pointer", int64(1)}} {
		if r, err := shim1(vm, "uv_timer_start", h, c.cb, u64(1), u64(0)); err != nil || r != uvEINVAL {
			t.Errorf("%s: %v %v", c.name, r, err)
		}
	}
	if r, _ := shim1(vm, "uv_timer_start", h, cb, u64(10), u64(4)); r != int32(0) {
		t.Errorf("uv_timer_start: %v", r)
	}
	if r, _ := shim1(vm, "uv_timer_get_repeat", h); r != u64(4) {
		t.Errorf("uv_timer_get_repeat: %v", r)
	}
	if r, _ := shim1(vm, "uv_is_active", h); r != int32(1) {
		t.Errorf("uv_is_active: %v", r)
	}
	if r, _ := shim1(vm, "uv_timer_stop", h); r != int32(0) {
		t.Errorf("uv_timer_stop: %v", r)
	}
	if r, _ := shim1(vm, "uv_is_active", h); r != int32(0) {
		t.Errorf("uv_is_active after a stop: %v", r)
	}
	// handle->data: NULL, then a julia object, which uv_handle_get_data hands
	// back as a pointer to it; uv_handle_set_data takes a pointer, and
	// anything else leaves it
	if r, _ := shim1(vm, "uv_handle_get_data", h); r.(*Pointer).Target != nil {
		t.Errorf("data of a new handle: %v", r)
	}
	shim1(vm, "jl_uv_associate_julia_struct", h, "obj")
	if r, _ := shim1(vm, "uv_handle_get_data", h); r.(*Pointer).Target != "obj" {
		t.Errorf("data after associate: %v", r)
	}
	shim1(vm, "uv_handle_set_data", h, vm.voidPtr("other"))
	shim1(vm, "uv_handle_set_data", h, int64(5))
	if r, _ := shim1(vm, "uv_handle_get_data", h); r.(*Pointer).Target != "other" {
		t.Errorf("data after set_data: %v", r)
	}
	shim1(vm, "jl_uv_disassociate_julia_struct", h)
	if rec(vm, h).data != nil {
		t.Error("data after disassociate")
	}
	// a closing handle cannot start a timer, and closes once
	shim1(vm, "jl_close_uv", h)
	shim1(vm, "jl_close_uv", h)
	if got := len(vm.uv().closing); got != 1 {
		t.Errorf("%d closing handles after two closes", got)
	}
	if r, _ := shim1(vm, "uv_timer_start", h, cb, u64(1), u64(0)); r != uvEINVAL {
		t.Errorf("uv_timer_start of a closing handle: %v", r)
	}
	if !vm.uv().alive() {
		t.Error("a handle waiting for its close callback keeps the loop alive")
	}
	// the clock
	vm.uv().now = 0
	shim1(vm, "uv_update_time", loop)
	if vm.uv().now == 0 && time.Since(hrtimeOrigin) > time.Millisecond {
		t.Error("uv_update_time left the time at 0")
	}
}

// jl_process_events runs a pass that does not wait, on the thread that runs the
// loop only.
func TestUVProcessEvents(t *testing.T) {
	vm := New()
	n := 0
	timer(t, vm, callback(vm, func([]any) error { n++; return nil }), 0, 0)
	if r, err := shim1(vm, "jl_process_events"); err != nil || r != int32(1) || n != 1 {
		t.Errorf("jl_process_events: %v %v, %d callbacks", r, err, n)
	}
	vm.curRec.tid = -1
	if r, err := shim1(vm, "jl_process_events"); err != nil || r != int32(0) {
		t.Errorf("jl_process_events off the loop's thread: %v %v", r, err)
	}
	vm.curRec.tid = 0
	boom := errors.New("boom")
	timer(t, vm, callback(vm, func([]any) error { return boom }), 0, 0)
	if _, err := shim1(vm, "jl_process_events"); !errors.Is(err, boom) {
		t.Errorf("a failing callback: %v", err)
	}
}

// A callback that fails stops the pass, wherever it runs from: a pending async
// send, a timer before the wait, and a close callback.
func TestUVCallbackErrors(t *testing.T) {
	boom := errors.New("boom")
	vm := New()
	h := async(t, vm, callback(vm, func([]any) error { return boom }))
	vm.uv().asyncSend(rec(vm, h))
	if err := vm.uvIterate(false); !errors.Is(err, boom) {
		t.Errorf("an async callback: %v", err)
	}
	vm = New()
	timer(t, vm, callback(vm, func([]any) error { return boom }), 0, 0)
	if _, err := vm.uvRun(); !errors.Is(err, boom) {
		t.Errorf("a timer callback in uv_run: %v", err)
	}
	// the timers after the wait
	vm = New()
	timer(t, vm, callback(vm, func([]any) error { return boom }), 5, 0)
	if _, err := vm.uvRun(); !errors.Is(err, boom) {
		t.Errorf("a timer that fires during the wait: %v", err)
	}
	// a close callback: a handle with an object whose type has no module
	vm = New()
	h2 := newHandle(vm)
	shim1(vm, "jl_uv_associate_julia_struct", h2, int64(1))
	shim1(vm, "jl_close_uv", h2)
	if err := vm.uvIterate(false); err == nil || !strings.Contains(err.Error(), "_uv_hook_close of a Int64") {
		t.Errorf("a close callback of an object with no module: %v", err)
	}
}

// The handles are bounded by VM.MaxHandles.
func TestUVHandleLimit(t *testing.T) {
	vm := New()
	vm.MaxHandles = 1
	loop := vm.voidPtr(vm.uv())
	if _, err := shim1(vm, "uv_timer_init", loop, newHandle(vm)); err != nil {
		t.Fatal(err)
	}
	_, err := shim1(vm, "uv_timer_init", loop, newHandle(vm))
	if !errors.Is(err, ErrHandleLimit) || !strings.Contains(err.Error(), "VM.MaxHandles is 1") {
		t.Errorf("a second handle: %v", err)
	}
	// freeing a handle makes room, and an unknown one is ignored
	first := vm.uv().order[0].mem
	vm.uvForget(&Memory{})
	vm.uvForget(first)
	if len(vm.uv().order) != 0 {
		t.Error("a freed handle is still known")
	}
	if _, err := shim1(vm, "uv_timer_init", loop, newHandle(vm)); err != nil {
		t.Error(err)
	}
	// the loop is not even made by forgetting
	New().uvForget(first)
}

// free forgets the handle that its memory stood for.
func TestFreeForgetsHandle(t *testing.T) {
	vm := New()
	h := newHandle(vm)
	shim1(vm, "uv_timer_init", vm.voidPtr(vm.uv()), h)
	if _, err := shim1(vm, "free", h); err != nil {
		t.Fatal(err)
	}
	if _, err := shim1(vm, "uv_is_active", h); err == nil {
		t.Error("a freed handle is still known")
	}
}

// A handle closed with no object is freed by its close callback; one with an
// object gets julia's _uv_hook_close of the module that defines the object's
// type, which frees it.
func TestUVClose(t *testing.T) {
	vm := taskVM(t)
	// before Base is made the top module is Core, which is where
	// jl_base_relative_to looks for _uv_hook_close; what julia code defines
	// for the objects handles belong to (asyncevent.jl:259-271)
	_, err := vm.sessionFor(vm.Core).Eval(`
function _uv_hook_close(x)
    setfield!(getfield(x, :log), :v, (getfield(getfield(x, :log), :v)..., :closed))
    ccall(:free, Cvoid, (Ptr{Cvoid},), getfield(x, :h))
end
`)
	if err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	_, err = s.Eval(taskHeader + `
mutable struct Obj; h::Ptr{Cvoid}; log::Log; end
function mk()
    h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
    ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
    loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
    ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
    h
end
active(h) = ccall(:uv_is_active, Int32, (Ptr{Cvoid},), h)
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, src, want string }{
		{"no object", `let h = mk()
            ccall(:jl_close_uv, Cvoid, (Ptr{Cvoid},), h); ccall(:jl_process_events, Int32, ())
            getfield(LOG, :v) end`, "()"},
		{"an object", `let h = mk()
            ccall(:jl_uv_associate_julia_struct, Cvoid, (Ptr{Cvoid}, Any), h, Obj(h, LOG))
            ccall(:jl_close_uv, Cvoid, (Ptr{Cvoid},), h); ccall(:jl_process_events, Int32, ())
            getfield(LOG, :v) end`, "(:closed,)"},
		{"the handle is freed", `let h = mk()
            ccall(:jl_close_uv, Cvoid, (Ptr{Cvoid},), h); ccall(:jl_process_events, Int32, ())
            active(h) end`, "!not ported yet: uv_is_active of the handle"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.name, v, err, c.want)
	}
	// without _uv_hook_close the close of an object's handle raises what
	// julia raises: it is not defined
	bare := New()
	if err := bare.Boot(); err != nil {
		t.Fatal(err)
	}
	bs := bare.NewSession()
	v, err := bs.Eval(`struct S end; S()`)
	if err != nil {
		t.Fatal(err)
	}
	h := newHandle(bare)
	shim1(bare, "jl_uv_associate_julia_struct", h, v)
	shim1(bare, "jl_close_uv", h)
	if err := bare.uvIterate(false); err == nil || !strings.Contains(err.Error(), "UndefVarError(:_uv_hook_close") {
		t.Errorf("no _uv_hook_close: %v", err)
	}
}

// jl_task_get_next with the loop: a timer's callback ends the wait, a
// callback that wakes a thread ends it with nothing left alive, one that
// queued a task without a wakeup is found by the last look, and what cannot be
// ended is refused.
func TestTaskGetNextWithLoop(t *testing.T) {
	vm := taskVM(t)
	s := vm.NewSession()
	if _, err := s.Eval(taskHeader + `
mutable struct St; n::Int; flag::Bool; end
const ST = St(0, false)
t = newtask(() -> 1)
function setflag(h::Ptr{Cvoid})
    setfield!(ST, :flag, true)
    nothing
end
function setflag_wake(h::Ptr{Cvoid})
    setfield!(ST, :flag, true)
    ccall(:jl_wakeup_thread, Cvoid, (Int16,), i16(0))
    nothing
end
function raise(h::Ptr{Cvoid})
    throw(ErrorException("callback"))
end
function mktimer(cb, ms)
    h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
    ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
    loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
    ccall(:uv_timer_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}), loop, h)
    ccall(:uv_timer_start, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, UInt64, UInt64), h, cb, u64(ms), u64(0))
    h
end
function mkasync()
    h = ccall(:malloc, Ptr{Cvoid}, (UInt,), u64(512))
    ccall(:jl_uv_disassociate_julia_struct, Cvoid, (Ptr{Cvoid},), h)
    loop = ccall(:jl_global_event_loop, Ptr{Cvoid}, ())
    ccall(:uv_async_init, Int32, (Ptr{Cvoid}, Ptr{Cvoid}, Ptr{Cvoid}), loop, h, Base.@cfunction(setflag, Cvoid, (Ptr{Cvoid},)))
    h
end
trypop_flag(q) = getfield(ST, :flag) ? t : nothing
trypop_none(q) = nothing
trypop_count(q) = (setfield!(ST, :n, Core.Intrinsics.add_int(getfield(ST, :n), 1)); getfield(ST, :n) === 3 ? t : nothing)
trypop_throw2(q) = (setfield!(ST, :n, Core.Intrinsics.add_int(getfield(ST, :n), 1)); getfield(ST, :n) === 2 ? throw(ErrorException("second")) : nothing)
empty_true() = true
empty_not2() = getfield(ST, :n) === 2 ? false : true
next(f, e) = ccall(:jl_task_get_next, Ref{Core.Task}, (Any, Any, Any), f, nothing, e)
reset() = (setfield!(ST, :n, 0); setfield!(ST, :flag, false))
`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, src, want string }{
		{"a timer ends the wait", `reset(); mktimer(Base.@cfunction(setflag, Cvoid, (Ptr{Cvoid},)), 3); next(trypop_flag, empty_true) === t`, "true"},
		{"a wakeup ends it", `reset(); mktimer(Base.@cfunction(setflag_wake, Cvoid, (Ptr{Cvoid},)), 3); next(trypop_flag, empty_true) === t`, "true"},
		{"the queue looks non-empty once", `reset(); next(trypop_count, empty_not2) === t`, "true"},
		{"trypoptask raises in the last look", `reset(); next(trypop_throw2, empty_true)`, "!ErrorException(\"second\")"},
		{"a callback raises", `reset(); mktimer(Base.@cfunction(raise, Cvoid, (Ptr{Cvoid},)), 1); next(trypop_none, empty_true)`, "!ErrorException(\"callback\")"},
		// a referenced async handle alone: nothing in the VM can send
		{"an async handle alone", `reset(); mkasync(); next(trypop_none, empty_true)`, "!the root task waits forever"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.name, v, err, c.want)
	}
}
