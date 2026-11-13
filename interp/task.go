package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Tasks. Julia code starts on the root task, which jl_get_current_task
// returns until a switch (taskswitch.go): Base reads and writes its fields
// (its task-local storage, the scope, the owner of a lock).

// bootTaskType defines Core.Task as jl_init_types does (jltypes.c:
// 3751-3822): its 27 fields, the first a Union{Task, Nothing} once Task
// exists, metrics_enabled const, _state and the metric counters atomic.
func (vm *VM) bootTaskType() {
	u8, u64 := core.UInt8Type, core.UInt64Type
	names := []expr.Symbol{"next", "queue", "storage", "donenotify", "result", "scope", "code", "_state",
		"sticky", "priority", "_isexception", "pad00", "pad01", "pad02", "rngState0", "rngState1",
		"rngState2", "rngState3", "rngState4", "metrics_enabled", "pad10", "pad11", "pad12",
		"first_enqueued_at", "last_started_running_at", "running_time_ns", "finished_at"}
	a := core.AnyType
	types := []any{a, a, a, a, a, a, a, u8, core.BoolType, core.UInt16Type, core.BoolType,
		u8, u8, u8, u64, u64, u64, u64, u64, core.BoolType, u8, u8, u8, u64, u64, u64, u64}
	t := must(core.NewType("Core", "Task", core.AnyType, false, true, names, types))
	types[0] = core.TypeUnion(t, core.NothingType)
	must(0, t.SetFieldTypes(types))
	mask := func(bits ...int) []bool {
		m := make([]bool, len(names))
		for _, b := range bits {
			m[b] = true
		}
		return m
	}
	t.Name.ConstFields = mask(19)
	t.Name.AtomicFields = mask(7, 23, 24, 25, 26)
	vm.tTask = t
	vm.setCore("Task", t)
}

// rootTask is the task julia code runs on, made at the first
// jl_get_current_task: runnable (_state 0), sticky, with nothing in its
// object fields. julia seeds a root task's rngState from the process's
// random seed (jl_init_root_task); here it is fixed, so that a program's
// default RNG stream is the same from run to run.
func (vm *VM) rootTask() *Struct {
	if vm.task != nil {
		return vm.task
	}
	f := newTaskFields(nil, nil, nil,
		[5]uint64{0x7e1df2e2a3e6c1d5, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1, 0x510e527fade682d1, 0x9b05688c2b3e6c1f})
	vm.task = &Struct{Type: vm.tTask, Fields: f}
	// the root task runs on thread 0, in the default pool, and has started
	// (jl_init_root_task, task.c:1526+)
	rec := vm.newTaskRec(0, 1)
	rec.started = true
	vm.addTaskRec(vm.task, rec)
	vm.cur, vm.curRec, vm.rootRec = vm.task, rec, rec
	return vm.task
}

// newTaskFields are the 27 fields of a runnable, sticky task with nothing in
// its result and its queue links (jl_new_task, task.c:1103-1132): start is
// its code, done its donenotify, scope its scope, rng its random state.
func newTaskFields(start, done, scope any, rng [5]uint64) []any {
	f := make([]any, 27)
	f[2], f[3], f[5], f[6] = nil, done, scope, start
	f[7], f[8], f[9], f[10] = uint8(0), true, uint16(0), false
	f[11], f[12], f[13] = uint8(0), uint8(0), uint8(0)
	for i, s := range rng {
		f[14+i] = s
	}
	f[19], f[20], f[21], f[22] = false, uint8(0), uint8(0), uint8(0)
	for i := 23; i < 27; i++ {
		f[i] = uint64(0)
	}
	return f
}
