package interp

import (
	"errors"
	"fmt"
)

// taskGetNext is jl_task_get_next (scheduler.c:365-552) for one thread: the
// next runnable task, popped by julia's trypoptask. When none is runnable
// and the queue looks empty, the libuv loop runs once (uv_run(ONCE),
// scheduler.c:457) and the search starts again. Where julia would sleep
// forever, because no task is runnable, no handle fired and nothing the loop
// holds can wake it, the VM refuses with ErrDeadlock naming the waiting task:
// an embedded interpreter must not hang its host. That is a stated
// divergence.
func (vm *VM) taskGetNext(a []any) (any, error) {
	trypop, q, checkempty := a[0], a[1], a[2]
	for {
		t, empty, err := vm.pollTask(trypop, q, checkempty)
		if err != nil {
			return nil, err
		}
		if t != nil {
			return t, nil
		}
		if !empty {
			continue
		}
		vm.wakeup = false
		alive, err := vm.uvRun()
		if err != nil && !errors.Is(err, errLoopForever) {
			return nil, err
		}
		if err == nil && (alive || vm.wakeup) {
			continue
		}
		// a last look, for work that a callback queued without a wakeup
		t, empty, err = vm.pollTask(trypop, q, checkempty)
		if err != nil {
			return nil, err
		}
		if t != nil {
			return t, nil
		}
		if !empty {
			continue
		}
		return nil, fmt.Errorf("%w: %s waits forever: no task is runnable and no referenced libuv handle can wake the event loop",
			ErrDeadlock, vm.describeTask(vm.currentTask()))
	}
}

// pollTask is get_next_task (scheduler.c:306-316) and, when no task comes,
// check_empty (scheduler.c:318-321): the task trypoptask popped, now this
// thread's, or whether the queue looks empty.
func (vm *VM) pollTask(trypop, q, checkempty any) (*Struct, bool, error) {
	v, err := vm.apply(trypop, []any{q})
	if err != nil {
		return nil, false, err
	}
	if t, ok := v.(*Struct); ok && t.Type == vm.tTask {
		if rec := vm.recOf(t); rec != nil {
			rec.setTid(vm.curRec.tid)
			return t, false, nil
		}
	}
	e, err := vm.apply(checkempty, nil)
	return nil, e == any(true), err
}

// bootTaskGetNext registers jl_task_get_next.
func (vm *VM) bootTaskGetNext() {
	vm.shims["jl_task_get_next"] = func(vm *VM, a []any) (any, error) {
		return vm.taskGetNext(a)
	}
}
