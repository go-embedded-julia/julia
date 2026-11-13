package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// shim runs the Go side of the C function name.
func shim1(vm *VM, name string, args ...any) (any, error) {
	return vm.shims[expr.Symbol(name)](vm, args)
}

// A shim of tasks refuses what is not a task, or a value of the wrong type.
func TestTaskShimArguments(t *testing.T) {
	vm := New()
	task, err := shim1(vm, "jl_new_task", int64(1), nil, int64(0))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"jl_new_task", []any{nil, nil, "x"}},
		{"jl_set_next_task", []any{int64(1)}},
		{"jl_is_task_started", []any{int64(1)}},
		{"jl_get_task_tid", []any{int64(1)}},
		{"jl_get_task_threadpoolid", []any{int64(1)}},
		{"jl_set_task_tid", []any{int64(1), int32(0)}},
		{"jl_set_task_tid", []any{task, int64(0)}},
		{"jl_set_task_threadpoolid", []any{int64(1), int8(0)}},
		{"jl_set_task_threadpoolid", []any{task, int64(0)}},
		{"jl_threadpoolid", []any{int64(0)}},
		{"jl_set_ptls_rng", []any{int64(0)}},
		{"jl_set_peek_cond", []any{int64(0)}},
		// a Task that jl_new_task did not make has no record
		{"jl_is_task_started", []any{&Struct{Type: vm.tTask, Fields: newTaskFields(nil, nil, nil, [5]uint64{})}}},
	} {
		_, err := shim1(vm, c.name, c.args...)
		check(t, c.name, nil, err, "!TypeError(:ccall")
	}
	if _, err := shim1(vm, "jl_threadpoolid", int16(1)); err == nil || !strings.Contains(err.Error(), "invalid tid") {
		t.Errorf("jl_threadpoolid(1): %v, want invalid tid", err)
	}
	if _, err := shim1(vm, "jl_threadpoolid", int16(-1)); err == nil || !strings.Contains(err.Error(), "invalid tid") {
		t.Errorf("jl_threadpoolid(-1): %v, want invalid tid", err)
	}
}

// julia -t 1 answers the thread questions Base asks: one thread, in the
// default pool, no interactive thread (`julia -t 1`, 1.12.6: nthreads() 1,
// nthreads(:interactive) 0, jl_n_threadpools 2, jl_n_threads_per_pool [0, 1],
// jl_threadpoolid(0) 1, the root task's tid 0 and threadpoolid 1,
// jl_in_threaded_region 0).
func TestThreadFacts(t *testing.T) {
	vm := taskVM(t)
	s := vm.NewSession()
	load := `
nthreads() = Core.Intrinsics.atomic_pointerref(Core.Intrinsics.cglobal(:jl_n_threads, Int32), :acquire)
npools() = Core.Intrinsics.pointerref(Core.Intrinsics.cglobal(:jl_n_threadpools, Int32), 1, 1)
perpool(i) = Core.Intrinsics.pointerref(Core.Intrinsics.pointerref(Core.Intrinsics.cglobal(:jl_n_threads_per_pool, Ptr{Int32}), 1, 1), i, 1)
`
	if _, err := s.Eval(load); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{`(nthreads(), npools(), perpool(1), perpool(2))`, "(1, 2, 0, 1)"},
		{`ccall(:jl_threadpoolid, Int8, (Int16,), Core.Intrinsics.trunc_int(Int16, 0))`, "1"},
		{`ccall(:jl_threadid, Int16, ())`, "0"},
		{`let t = ccall(:jl_get_current_task, Ref{Core.Task}, ())
            (ccall(:jl_get_task_tid, Int16, (Any,), t), ccall(:jl_get_task_threadpoolid, Int8, (Any,), t)) end`, "(0, 1)"},
		{`ccall(:jl_in_threaded_region, Int32, ())`, "0"},
		// the region nests, and leaving the last one wakes the loop
		{`(ccall(:jl_enter_threaded_region, Cvoid, ()); ccall(:jl_enter_threaded_region, Cvoid, ());
          a = ccall(:jl_in_threaded_region, Int32, ()); ccall(:jl_exit_threaded_region, Cvoid, ());
          b = ccall(:jl_in_threaded_region, Int32, ()); ccall(:jl_exit_threaded_region, Cvoid, ());
          (a, b, ccall(:jl_in_threaded_region, Int32, ())))`, "(1, 1, 0)"},
		// the threadpool of a task: -1 up to the last pool, nothing past it
		{`let t = ccall(:jl_new_task, Ref{Core.Task}, (Any, Any, Int), nothing, nothing, 0)
            set(i) = ccall(:jl_set_task_threadpoolid, Int32, (Any, Int8), t, Core.Intrinsics.trunc_int(Int8, i))
            r = (set(-2), set(-1), set(0), set(1), set(2), set(5))
            (r, ccall(:jl_get_task_threadpoolid, Int8, (Any,), t)) end`, "((0, 1, 1, 1, 0, 0), 1)"},
		{`(ccall(:jl_set_ptls_rng, Cvoid, (UInt64,), Core.Intrinsics.bitcast(UInt64, 5)), ccall(:jl_get_ptls_rng, UInt64, ()))`, "(nothing, 0x0000000000000005)"},
		{`ccall(:jl_gc_safepoint, Cvoid, ())`, "nothing"},
		{`ccall(:jl_cpu_suspend, Cvoid, ())`, "nothing"},
		{`ccall(:jl_set_peek_cond, Cvoid, (Ptr{Cvoid},), ccall(:malloc, Ptr{Cvoid}, (UInt,), Core.Intrinsics.bitcast(UInt64, 8)))`, "nothing"},
		{`ccall(:jl_wakeup_thread, Cvoid, (Int16,), Core.Intrinsics.trunc_int(Int16, 0))`, "nothing"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}

// Sys.__init__'s questions, answered as julia 1.12.6 does on darwin/arm64.
func TestSysinfoShims(t *testing.T) {
	vm := New()
	for name, want := range map[string]any{"jl_SC_CLK_TCK": int64(100), "jl_get_cpu_name": "apple-m1",
		"jl_get_JIT": "ORCJIT", "jl_getpagesize": int64(16384)} {
		if got, err := shim1(vm, name); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", name, got, err, want)
		}
	}
	if got, err := shim1(vm, "jl_cpu_threads"); err != nil || got.(int32) < 1 {
		t.Errorf("jl_cpu_threads: %v %v", got, err)
	}
}

// A task that raises something julia code cannot catch (here a ccall that is
// not ported) ends the computation: the error comes out of the switch of the
// root task, and the VM still works.
func TestTaskFatalError(t *testing.T) {
	vm := taskVM(t)
	s := vm.NewSession()
	_, err := s.Eval(taskHeader + `run(newtask(() -> ccall(:no_such_function, Cvoid, ())))`)
	check(t, "a task that stops", nil, err, "!ccall(:no_such_function)")
	if vm.cur != vm.task || vm.liveTasks != 0 || len(vm.running) != 0 {
		t.Errorf("after the error: current %p root %p, %d live tasks, %d running", vm.cur, vm.task, vm.liveTasks, len(vm.running))
	}
	v, err := s.Eval(`t = newtask(() -> 5); run(t); result(t)`)
	check(t, "the VM after it", v, err, "5")
}

// The number of started tasks is bounded by VM.MaxTasks; a task past it is
// refused, and Close ends the ones that wait.
func TestTaskLimitAndClose(t *testing.T) {
	vm := taskVM(t)
	vm.MaxTasks = 1
	s := vm.NewSession()
	if _, err := s.Eval(taskHeader + `
t1 = newtask(() -> (setnext(root); doswitch(); note(:never)))
t2 = newtask(() -> 1)
setnext(t1); doswitch()
`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Eval(`setnext(t2); doswitch()`)
	if !errors.Is(err, ErrTaskLimit) || !strings.Contains(err.Error(), "VM.MaxTasks is 1") {
		t.Errorf("a second started task: %v, want ErrTaskLimit", err)
	}
	if vm.liveTasks != 1 || len(vm.running) != 1 {
		t.Errorf("live tasks %d, running %d", vm.liveTasks, len(vm.running))
	}
	vm.Close()
	vm.Close()
	if vm.liveTasks != 0 || len(vm.running) != 0 {
		t.Errorf("after Close: live tasks %d, running %d", vm.liveTasks, len(vm.running))
	}
	v, err := s.Eval(`(getfield(LOG, :v), started(t1), (try; setnext(t1); doswitch(); catch e; e; end))`)
	check(t, "a task that was closed", v, err, `((), 1, ErrorException("attempt to switch to exited task"))`)
	// the limit is the default one when MaxTasks is not set
	vm.MaxTasks = 0
	if vm.maxTasks() != DefaultMaxTasks {
		t.Errorf("maxTasks() = %d", vm.maxTasks())
	}
}

// What jl_finish_task needs from Base: its task_done_hook, which switches
// away; a task that finds none, or a hook that throws or returns, ends the
// computation with an error that says so.
func TestTaskFinishErrors(t *testing.T) {
	for _, c := range []struct {
		name, hook, want string
	}{
		{"no hook", "", "Base.task_done_hook is not defined"},
		{"a hook that throws", "task_done_hook(t) = throw(ErrorException(\"hook\"))",
			"fatal: error thrown and no exception handler available.\nErrorException(\"hook\")"},
		{"a hook that returns", "task_done_hook(t) = nothing", "Base.task_done_hook returned"},
	} {
		vm := New()
		if err := vm.Boot(); err != nil {
			t.Fatal(err)
		}
		vm.baseModule = vm.Base
		if c.hook != "" {
			if _, err := vm.sessionFor(vm.Base).Eval(c.hook); err != nil {
				t.Fatal(err)
			}
		}
		_, err := vm.NewSession().Eval(taskHeader + `t = newtask(() -> 1); setnext(t); doswitch()`)
		check(t, c.name, nil, err, "!"+c.want)
		vm.Close()
	}
	// a VM whose Base is not made yet has no hook either
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	if vm.taskDoneHook() != nil {
		t.Error("a hook before Base")
	}
}

// A new task copies the scope of the task that makes it, and :enter's scope
// is the task's until the try block is left by any way: normally, by an
// exception, or by the depth limit.
func TestTaskScope(t *testing.T) {
	vm := New()
	vm.currentTask().Fields[5] = "S0"
	task, _ := shim1(vm, "jl_new_task", nil, nil, int64(0))
	if got := task.(*Struct).Fields[5]; got != "S0" {
		t.Errorf("the scope of a new task: %v", got)
	}
	vm.currentTask().Fields[5] = nil
	s := vm.NewSession()
	scope := func() any { return vm.currentTask().Fields[5] }
	cs := call(gr("Core", "current_scope"))
	for _, c := range []struct {
		name string
		code []any
		want string
	}{
		{"left normally", []any{expr.EnterNode{Catch: 7, Scope: "S", HasScope: true}, cs, expr.New("leave", expr.SSAValue(1)), cs,
			call(gr("Core", "tuple"), expr.SSAValue(2), expr.SSAValue(4)), ret(expr.SSAValue(5)), ret(nil)}, `("S", nothing)`},
		{"left by an exception", []any{expr.EnterNode{Catch: 5, Scope: "S", HasScope: true}, call(gr("Core", "throw"), int64(1)),
			ret(nil), ret(nil), cs, ret(expr.SSAValue(5))}, "nothing"},
		{"nested", []any{expr.EnterNode{Catch: 9, Scope: "A", HasScope: true}, expr.EnterNode{Catch: 9, Scope: "B", HasScope: true}, cs,
			expr.New("leave", expr.SSAValue(2)), cs, expr.New("leave", expr.SSAValue(1)), call(gr("Core", "tuple"), expr.SSAValue(3), expr.SSAValue(5)),
			ret(expr.SSAValue(7)), ret(nil), ret(nil)}, `("B", "A")`},
		{"a scope that cannot be evaluated", []any{expr.EnterNode{Catch: 3, Scope: call(gr("Core", "throw"), int64(2)), HasScope: true}, ret(nil), ret(nil)},
			"!julia exception: 2"},
	} {
		v, err := runIR(s, codeInfo(c.code), false)
		check(t, c.name, v, err, c.want)
		if scope() != nil {
			t.Errorf("%s: the scope stays %v", c.name, scope())
		}
	}
	// the depth limit refuses a try block, and the scope it set is undone
	vm.MaxDepth = 0
	_, err := runIR(s, codeInfo([]any{expr.EnterNode{Catch: 3, Scope: "S", HasScope: true}, ret(nil), ret(nil)}), false)
	check(t, "depth", nil, err, "!StackOverflowError")
	if scope() != nil {
		t.Errorf("depth: the scope stays %v", scope())
	}
	if v, err := biCurrentScope(vm, []any{int64(1)}); err == nil {
		t.Errorf("current_scope(1): %v", v)
	}
	_ = core.AnyType
}
