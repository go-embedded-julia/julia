package interp

import "github.com/go-embedded-julia/julia/core"

// getBacktrace is jl_get_backtrace (stackwalk.c:345-361): the backtrace of the
// current task's current exception as a Core.svec of two vectors, bt a
// Vector{Ptr{Cvoid}} (decode_backtrace, :324) and bt2 a Vector{Any} (:330).
//
// The interpreter keeps no native stack to walk and no exception stack of
// bt_data, so it answers what julia answers when no backtrace was recorded
// (the `s && s->top` test at :351 failing, bt_size 0): two empty vectors.
// Base's catch_backtrace (base/error.jl:133-136) reformats them to an empty
// Vector{Union{Ptr{Cvoid}, Base.InterpreterIP}}. Naming julia-level frames is
// not attempted (go-embedded-julia/julia#107).
func (vm *VM) getBacktrace() (any, error) {
	bt := vm.newVector(vm.ptrType(core.NothingType), nil)
	bt2 := vm.newVector(core.AnyType, nil)
	return SVec{bt, bt2}, nil
}

// getExcstack is jl_get_excstack (stackwalk.c:367-398), which Base's
// current_exceptions calls (base/error.jl:161) and so does the printing of a
// failed task (show_task_exception, base/task.jl:89-98): a Vector{Any} of the
// task's exceptions, with each one's bt and bt2 when asked for. The
// interpreter keeps no exception stack (as for jl_get_backtrace), so the
// vector is empty, the case show_task_exception handles ("exception stack
// buffer not available") by printing t.result with showerror. The task
// argument is checked as JL_TYPECHK does (:369).
func (vm *VM) getExcstack(a []any) (any, error) {
	if t, ok := a[0].(*Struct); !ok || t.Type != vm.coreType("Task") {
		return nil, vm.typeError("current_exceptions", "", vm.coreType("Task"), a[0])
	}
	return vm.newVector(core.AnyType, nil), nil
}
