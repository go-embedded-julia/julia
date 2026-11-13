package interp

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The libuv loop, in Go (issue #35, step 4): the handles julia's scheduler
// needs, an async handle (uv_async_send wakes the loop from any goroutine) and
// a timer (a monotonic clock, in milliseconds as libuv keeps it, with a
// repeat), and the loop that runs them. A handle is the memory julia code
// allocated for it (Libc.malloc(_sizeof_uv_timer)); its state is a record
// here, found by that memory. The callbacks are julia functions, run on the
// task that runs the loop, as in julia: the cfunction pointers julia code
// passed (cfunction.go).
//
// The shape of one pass of uv_run follows libuv's (unix/core.c uv_run):
// timers are run, the loop waits for a timer or an async send, the pending
// asyncs are run, the closing handles are closed, the timers run again.

// ErrHandleLimit is the error of a libuv handle past VM.MaxHandles.
var ErrHandleLimit = errors.New("interp: libuv handle limit exceeded")

// DefaultMaxHandles is the number of libuv handles a new VM allows.
const DefaultMaxHandles = 1 << 16

// errLoopForever is what a blocking pass of the loop answers instead of
// waiting when no timer is armed and nothing is pending: libuv would wait for
// a signal that nothing in the VM can send.
var errLoopForever = errors.New("interp: libuv would wait forever")

type uvKind int8

const (
	uvUnknown uvKind = iota
	uvAsync
	uvTimer
)

// uvHandle is a handle's state.
type uvHandle struct {
	mem  *Memory
	ptr  *Pointer // what julia code holds, handed back to its callback
	kind uvKind
	// active (uv__is_active), refd (UV_HANDLE_REF: only active referenced
	// handles keep the loop alive) and closing (uv_close was called)
	active, refd, closing bool
	data                  any // handle->data, a julia object, nil for NULL
	cb                    any // the cfunction pointer of the callback
	due                   uint64
	repeat                uint64
	startID               uint64
	pending               bool // async: sent, not yet run; guarded by uvLoop.mu
}

// uvLoop is the loop.
type uvLoop struct {
	// mu guards the pending flags of the async handles, which any goroutine
	// may set; wake holds a token for a loop that waits.
	mu      sync.Mutex
	wake    chan struct{}
	handles map[*Memory]*uvHandle
	order   []*uvHandle // the handles in the order they were made
	closing []*uvHandle // uv_close was called, the close callback has not run
	now     uint64      // the loop time in milliseconds (uv_now)
	timerID uint64      // loop->timer_counter
	running bool
}

// uv is the VM's loop, made on first use (jl_global_event_loop).
func (vm *VM) uv() *uvLoop {
	if vm.loop == nil {
		vm.loop = &uvLoop{wake: make(chan struct{}, 1), handles: map[*Memory]*uvHandle{}}
		vm.loop.updateTime()
	}
	return vm.loop
}

// updateTime is uv_update_time: the loop time is the monotonic clock in
// milliseconds.
func (l *uvLoop) updateTime() { l.now = uint64(time.Since(hrtimeOrigin) / time.Millisecond) }

// handle is the record of the handle a Ptr{Cvoid} points at: the memory
// julia allocated for it. create makes the record the first time, within
// VM.MaxHandles.
func (vm *VM) uvHandle(fname string, v any, create bool) (*uvHandle, error) {
	p, ok := v.(*Pointer)
	if ok {
		if m, isMem := p.Target.(*Memory); isMem && p.Off == 0 {
			l := vm.uv()
			if h := l.handles[m]; h != nil {
				return h, nil
			}
			if create {
				limit := vm.MaxHandles
				if limit <= 0 {
					limit = DefaultMaxHandles
				}
				if len(l.order) >= limit {
					return nil, fmt.Errorf("%w: %d handles (VM.MaxHandles is %d)", ErrHandleLimit, len(l.order), limit)
				}
				h := &uvHandle{mem: m, ptr: p, refd: true}
				l.handles[m] = h
				l.order = append(l.order, h)
				return h, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %s of the handle %s", ErrUnsupported, fname, showValue(v))
}

// uvForget drops the record of a handle whose memory was freed.
func (vm *VM) uvForget(m *Memory) {
	l := vm.loop
	if l == nil || l.handles[m] == nil {
		return
	}
	delete(l.handles, m)
	for i, h := range l.order {
		if h.mem == m {
			l.order = append(l.order[:i], l.order[i+1:]...)
			break
		}
	}
}

// alive is uv__loop_alive: a handle that is active and referenced, or a
// handle waiting for its close callback.
func (l *uvLoop) alive() bool {
	if len(l.closing) > 0 {
		return true
	}
	for _, h := range l.order {
		if h.active && h.refd {
			return true
		}
	}
	return false
}

// asyncSend is uv_async_send (unix/async.c): the handle's callback runs in
// the next pass of the loop, and a loop that waits is woken. It is safe to
// call from any goroutine.
func (l *uvLoop) asyncSend(h *uvHandle) {
	l.mu.Lock()
	h.pending = true
	l.mu.Unlock()
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// takePending is the async handles with a pending send, in the order they
// were made, their flags cleared; a token left in wake is dropped.
func (l *uvLoop) takePending() []*uvHandle {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []*uvHandle
	for _, h := range l.order {
		if h.pending {
			h.pending = false
			if h.kind == uvAsync && !h.closing {
				out = append(out, h)
			}
		}
	}
	select {
	case <-l.wake:
	default:
	}
	return out
}

func (l *uvLoop) anyPending() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, h := range l.order {
		if h.pending {
			return true
		}
	}
	return false
}

// nextTimeout is uv__next_timeout: the milliseconds until the first armed
// timer, whether or not it is referenced, -1 when none is armed.
func (l *uvLoop) nextTimeout() int64 {
	next := int64(-1)
	for _, h := range l.order {
		if h.kind == uvTimer && h.active {
			if d := int64(h.due) - int64(l.now); next < 0 || d < next {
				next = max(d, 0)
			}
		}
	}
	return next
}

// backendTimeout is uv_backend_timeout: how long a pass may wait, 0 not at
// all (nothing keeps the loop alive, or handles are closing), -1 until
// something happens.
func (l *uvLoop) backendTimeout() int64 {
	if len(l.closing) > 0 {
		return 0
	}
	for _, h := range l.order {
		if h.active && h.refd {
			return l.nextTimeout()
		}
	}
	return 0
}

// runTimers is uv__run_timers: the timers due at the time this pass began,
// first due first, the older start first among equals. A repeating timer is
// armed again before its callback runs, a one-shot one stops. A timer that a
// callback stopped or started again is not run for its old arming.
func (vm *VM) runTimers(l *uvLoop) error {
	var due []*uvHandle
	var ids []uint64
	for _, h := range l.order {
		if h.kind == uvTimer && h.active && h.due <= l.now {
			i := len(due)
			for i > 0 && (due[i-1].due > h.due || due[i-1].due == h.due && due[i-1].startID > h.startID) {
				i--
			}
			due = append(due[:i], append([]*uvHandle{h}, due[i:]...)...)
			ids = append(ids[:i], append([]uint64{h.startID}, ids[i:]...)...)
		}
	}
	for i, h := range due {
		if !h.active || h.startID != ids[i] {
			continue
		}
		if h.repeat > 0 {
			h.due, h.startID = l.now+h.repeat, l.timerID
			l.timerID++
		} else {
			h.active = false
		}
		if err := vm.uvCallback(h.cb, h.ptr); err != nil {
			return err
		}
	}
	return nil
}

// runClosing is uv__run_closing_handles: the close callback of each handle
// closed since the last pass (jl_uv_closeHandle, jl_uv.c:172-198): julia's
// _uv_hook_close of the object the handle belongs to, which frees the handle;
// a handle without an object is freed.
func (vm *VM) runClosing(l *uvLoop) error {
	closing := l.closing
	l.closing = nil
	for _, h := range closing {
		if h.data == nil {
			vm.uvForget(h.mem)
			continue
		}
		if err := vm.uvHookClose(h.data); err != nil {
			return err
		}
	}
	return nil
}

// uvHookClose calls topmod(typeof(data))._uv_hook_close(data) in the newest
// world.
func (vm *VM) uvHookClose(data any) error {
	var m *Module
	if dt, ok := vm.typeOf(data).(*core.DataType); ok {
		m, _ = dt.Name.ModuleRef.(*Module)
	}
	if m == nil {
		return fmt.Errorf("%w: _uv_hook_close of a %s", ErrUnsupported, showValue(vm.typeOf(data)))
	}
	last := vm.taskWorld
	vm.taskWorld = vm.World()
	defer func() { vm.taskWorld = last }()
	f, err := vm.globalValue(vm.baseRelativeTo(m), "_uv_hook_close")
	if err != nil {
		return err
	}
	_, err = vm.apply(f, []any{data})
	return err
}

// uvIterate is one pass of uv_run. When block is set the pass waits for a
// timer or an async send, as uv_run(ONCE) does, and answers errLoopForever
// where that would be for ever.
func (vm *VM) uvIterate(block bool) error {
	l := vm.uv()
	if l.running {
		return vm.errorf("uv_run: the event loop is already running")
	}
	l.running = true
	defer func() { l.running = false }()
	l.updateTime()
	if err := vm.runTimers(l); err != nil {
		return err
	}
	if block {
		if timeout := l.backendTimeout(); timeout != 0 && !l.anyPending() {
			if timeout < 0 {
				return errLoopForever
			}
			select {
			case <-l.wake:
			case <-time.After(time.Duration(timeout) * time.Millisecond):
			}
		}
	}
	for _, h := range l.takePending() {
		if err := vm.uvCallback(h.cb, h.ptr); err != nil {
			return err
		}
	}
	if err := vm.runClosing(l); err != nil {
		return err
	}
	l.updateTime()
	return vm.runTimers(l)
}

// uvRun is uv_run(loop, UV_RUN_ONCE): one pass, which waits when something is
// active; whether handles remain that keep the loop alive.
func (vm *VM) uvRun() (bool, error) {
	l := vm.uv()
	if !l.alive() {
		l.updateTime()
		return false, nil
	}
	if err := vm.uvIterate(true); err != nil {
		return false, err
	}
	return l.alive(), nil
}

// uvCallback runs a handle's callback, a cfunction pointer, on the current
// task in the newest world, as a callback julia is called back from C runs.
func (vm *VM) uvCallback(cb any, args ...any) error {
	last := vm.taskWorld
	vm.taskWorld = vm.World()
	defer func() { vm.taskWorld = last }()
	_, err := vm.callCFunction(cb, args...)
	return err
}

// bootUVLoop registers the C functions of the loop and its handles.
func (vm *VM) bootUVLoop() {
	// jl_global_event_loop (jl_uv.c): the loop julia's tasks share
	vm.shims["jl_global_event_loop"] = func(vm *VM, a []any) (any, error) { return vm.voidPtr(vm.uv()), nil }
	vm.shims["uv_update_time"] = func(vm *VM, a []any) (any, error) {
		vm.uv().updateTime()
		return nil, nil
	}
	// uv_async_init and uv_timer_init: a new handle is referenced, an async
	// one is active from the start (uv__handle_start), a timer waits for
	// uv_timer_start
	vm.shims["uv_async_init"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_async_init", a[1], true)
		if err != nil {
			return nil, err
		}
		h.kind, h.cb, h.active = uvAsync, a[2], true
		return int32(0), nil
	}
	vm.shims["uv_timer_init"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_timer_init", a[1], true)
		if err != nil {
			return nil, err
		}
		h.kind = uvTimer
		return int32(0), nil
	}
	vm.shims["uv_async_send"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_async_send", a[0], false)
		if err != nil {
			return nil, err
		}
		vm.uv().asyncSend(h)
		return int32(0), nil
	}
	// uv_timer_start (unix/timer.c:56-86): UV_EINVAL for a closing handle or
	// a NULL callback, an armed timer is stopped first
	vm.shims["uv_timer_start"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_timer_start", a[0], false)
		if err != nil {
			return nil, err
		}
		timeout, ok1 := a[2].(uint64)
		repeat, ok2 := a[3].(uint64)
		if !ok1 || !ok2 {
			return nil, vm.typeError("ccall", "", core.UInt64Type, vm.newTuple(a[2:]))
		}
		if p, isPtr := a[1].(*Pointer); h.closing || !isPtr || p.Target == nil {
			return uvEINVAL, nil
		}
		l := vm.uv()
		h.cb, h.repeat, h.active = a[1], repeat, true
		h.due = l.now + min(timeout, ^uint64(0)-l.now)
		h.startID = l.timerID
		l.timerID++
		return int32(0), nil
	}
	vm.shims["uv_timer_stop"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_timer_stop", a[0], false)
		if err != nil {
			return nil, err
		}
		h.active = false
		return int32(0), nil
	}
	vm.shims["uv_timer_get_repeat"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_timer_get_repeat", a[0], false)
		if err != nil {
			return nil, err
		}
		return h.repeat, nil
	}
	// uv_ref, uv_unref and uv_is_active (uv-common.c, unix/internal.h)
	for name, set := range map[expr.Symbol]bool{"uv_ref": true, "uv_unref": false} {
		vm.shims[name] = func(vm *VM, a []any) (any, error) {
			h, err := vm.uvHandle(string(name), a[0], false)
			if err != nil {
				return nil, err
			}
			h.refd = set
			return nil, nil
		}
	}
	vm.shims["uv_is_active"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_is_active", a[0], false)
		if err != nil {
			return nil, err
		}
		return cbool(h.active), nil
	}
	// handle->data: uv_handle_get_data and uv_handle_set_data, and julia's
	// jl_uv_associate_julia_struct and jl_uv_disassociate_julia_struct
	// (jl_uv.c:464-484), which set it from a julia object. The record exists
	// before the handle is initialised: Timer associates first.
	vm.shims["uv_handle_get_data"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_handle_get_data", a[0], false)
		if err != nil {
			return nil, err
		}
		return vm.voidPtr(h.data), nil
	}
	vm.shims["uv_handle_set_data"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("uv_handle_set_data", a[0], false)
		if err != nil {
			return nil, err
		}
		if p, ok := a[1].(*Pointer); ok {
			h.data = p.Target
		}
		return nil, nil
	}
	vm.shims["jl_uv_associate_julia_struct"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("jl_uv_associate_julia_struct", a[0], true)
		if err != nil {
			return nil, err
		}
		h.data = a[1]
		return nil, nil
	}
	vm.shims["jl_uv_disassociate_julia_struct"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("jl_uv_disassociate_julia_struct", a[0], true)
		if err != nil {
			return nil, err
		}
		h.data = nil
		return nil, nil
	}
	// jl_close_uv (jl_uv.c:415-446) for these handles: uv_close, which stops
	// the handle and leaves its close callback for the next pass; a handle
	// already closing is left alone
	vm.shims["jl_close_uv"] = func(vm *VM, a []any) (any, error) {
		h, err := vm.uvHandle("jl_close_uv", a[0], false)
		if err != nil {
			return nil, err
		}
		if !h.closing {
			l := vm.uv()
			h.closing, h.active = true, false
			l.closing = append(l.closing, h)
		}
		return nil, nil
	}
	// jl_process_events (jl_uv.c:384-403): a pass that does not wait, on the
	// thread that runs the loop (the loop is forced alive, so it answers 1)
	vm.shims["jl_process_events"] = func(vm *VM, a []any) (any, error) {
		vm.currentTask()
		if vm.curRec.tid != 0 {
			return int32(0), nil
		}
		if err := vm.uvIterate(false); err != nil {
			return nil, err
		}
		return int32(1), nil
	}
}
