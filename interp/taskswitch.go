package interp

// Tasks and switching (issue #35). This file holds the state a task owns and,
// from the next commit on, the switch itself.
//
// # Inventory: what the VM keeps for "the current computation"
//
// Every field of VM, and every place that reads or writes one, as of
// m3-base 6d1819e (file:line of that commit). A field is per task when julia
// keeps it in the task (jl_task_t, julia.h) or in a structure a task switch
// saves and restores (jl_switch, task.c:664-718); it is shared when julia
// keeps it per process or per thread, and the VM is both.
//
// Per task, saved at every switch and restored when the task runs again
// (taskState):
//
//   - taskWorld, ct->world_age (vm.go:55-58). Written by :latestworld
//     (eval.go:169), invokelatest and invoke_in_world (builtins.go:197-199,
//     213-215; builtins2.go:279-281), the typeinf world (ccall.go:428-430),
//     module initializers (modinit.go:61-63), macro expansion (macro.go:203)
//     and the top level (toplevel.go:99, 193, 349); read by every binding and
//     method lookup (bpart.go:281-313, builtins2.go:109-262, eval.go:195-766,
//     globalops.go:82-137, toplevel.go:272-549) and by jl_get_tls_world_age
//     (ccall.go:351). A new task starts in its creator's world
//     (task.c:1126, t->world_age = ct->world_age).
//   - depth, the nesting of calls and of try blocks that MaxDepth bounds
//     (vm.go:46-47; eval.go:88-93, 779-783; load.go:42-46; macro.go:153-157).
//     It counts the frames of one Go stack, so each task, which has a stack
//     of its own, counts from 0.
//   - excStack, ct->excstack, julia's exception stack (vm.go:58-59). An
//     EnterNode records its length (eval.go:79), a catch pushes (eval.go:109),
//     :pop_exception truncates (eval.go:130-133), the_exception reads the top
//     (eval.go:270-271), jl_rethrow and jl_rethrow_other read and replace it
//     (ccall.go:356-367). A new task's is empty (task.c:1120).
//   - typeinfTiming, ct->reentrant_timing bit 0 (vm.go:143-144;
//     ccall.go:402-413). A new task's is 0 (task.c:1127).
//   - finalizersInhibited, ptls->finalizers_inhibited (vm.go:149-150;
//     ccall.go:271-272). jl_switch stores it on the switching stack, zeroes
//     it for the next task and puts it back when the task resumes
//     (task.c:684-709), which is what a per-task value does.
//
// Per task, but already in the task's own object, so a switch moves nothing:
//
//   - scope, ct->scope, field 6 of Core.Task (task.go:18, Fields[5]). The
//     :enter form that carries a scope sets it and every way out of the try
//     block puts the old one back (interpreter.c:541-547, jl_eh_restore_state
//     restoring eh->scope); Core.current_scope reads it (builtins.c:619-623).
//     Today the interpreter evaluates the :enter scope and drops it
//     (eval.go:80-84) and current_scope answers nothing (builtins2.go:182-187);
//     both use the field from this commit on, and a new task copies its
//     creator's (task.c:1112).
//   - the task's result, _state, _isexception, sticky, priority and
//     task-local storage: fields of the same object.
//
// Per task, with no VM field because it is the Go stack itself:
//
//   - the exception handlers. An EnterNode runs its try block as a nested
//     evalBody call (eval.go:76-112), the handler stack of julia's ct->eh is
//     the chain of those Go frames, and a goroutine has its own.
//   - the interpreter's frames (locals, static parameters, program counter).
//
// Shared by every task, not saved at a switch:
//
//   - steps and MaxSteps (vm.go:52-53; eval.go:53): the VM's budget, so that
//     a program cannot escape it by spreading its steps over tasks.
//   - iolock (vm.go:108-109; ios.go:427-435), jl_uv_mutex: one lock for the
//     process, recursive, and julia does not release it at a switch either.
//   - errno (vm.go:111) and mpfr (vm.go:89): per thread in C, and there is
//     one thread.
//   - gsCtr (vm.go:141), typeinfFunc and typeinfWorld (vm.go:136-139):
//     globals of the runtime (symbol.c:125, gf.c:954-961).
//   - openModules and moduleInitOrder (vm.go:86-87): jl_current_modules and
//     jl_module_init_order, globals under the world lock.
//   - the world counter, the modules, method tables, sessions, ios, reqs,
//     cglobals, strBufs, argVectors, singletons, reflected: the runtime's
//     global tables.
//   - task, the root task (vm.go:147-148; task.go:46): the one object that
//     is not made by jl_new_task. The current task is a VM field of its own
//     from the next commit on.
//
// The rest of this file is the part of the inventory that moves.

// taskState is what a task owns while it is not running: the per-task fields
// of the inventory that live in the VM, saved when the task stops and loaded
// when it runs again.
type taskState struct {
	world               uint64
	depth               int
	excStack            []any
	typeinfTiming       bool
	finalizersInhibited int
}

// saveTaskState is the state of the task that is running now.
func (vm *VM) saveTaskState() taskState {
	return taskState{world: vm.taskWorld, depth: vm.depth, excStack: vm.excStack,
		typeinfTiming: vm.typeinfTiming, finalizersInhibited: vm.finalizersInhibited}
}

// loadTaskState makes s the state of the task that runs.
func (vm *VM) loadTaskState(s taskState) {
	vm.taskWorld, vm.depth, vm.excStack = s.world, s.depth, s.excStack
	vm.typeinfTiming, vm.finalizersInhibited = s.typeinfTiming, s.finalizersInhibited
}
