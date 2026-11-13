package interp

import (
	"errors"
	"fmt"
	"runtime"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

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
//     is not made by jl_new_task. The current task is a VM field of its own,
//     cur, which currentTask makes the root task on first use (rootTask).
//
// The rest of this file is the part of the inventory that moves, and the
// switch.

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

// A task is a goroutine, and one runs at a time. The evaluator is recursive
// Go code, so a julia task can stop in the middle of a call only if it has a
// stack of its own: each started task gets a goroutine, and a baton (a
// channel per task) says whose turn it is. jl_switch makes next_task the
// current task, hands it the baton and waits for its own; the first switch to
// a task starts its goroutine, which runs julia's _start_task and, when the
// body returns or throws, jl_finish_task (task.c:1208-1266, 308-349). Because
// exactly one goroutine runs julia code at any time, and every hand-over is a
// channel send, the evaluator needs no locks and the race detector sees
// ordered accesses.

// ErrTaskLimit is the error of a switch that would start a task past
// VM.MaxTasks.
var ErrTaskLimit = errors.New("interp: task limit exceeded")

// ErrDeadlock is the error of a task that waits when no task is runnable and
// nothing can wake the event loop: julia sleeps forever there, the VM refuses.
var ErrDeadlock = errors.New("interp: deadlock")

var (
	// errTaskExit unwinds the goroutine of a finished task that switched
	// away for the last time; errTaskKilled that of a parked task whose VM
	// is closed. Neither is a julia exception: no try block catches them.
	errTaskExit   = errors.New("interp: task exited")
	errTaskKilled = errors.New("interp: task killed")
)

// DefaultMaxTasks is the number of started tasks a new VM allows.
const DefaultMaxTasks = 10000

// taskRec is what the VM knows of a task that its object does not hold: the
// jl_task_t fields Core.Task does not show (tid, threadpoolid, ctx.started)
// and the goroutine.
type taskRec struct {
	id      int
	tid     int16
	poolid  int8
	started bool // ctx.started
	exited  bool // the goroutine ended: julia's ctx.stkbuf == NULL
	state   taskState
	baton   chan baton
	done    chan struct{} // closed when a killed task's goroutine ends
}

// baton is the hand-over: the task that hands it (its tid is reset when it
// is not sticky, task.c:692-700), and a kill or a fatal error.
type baton struct {
	prev *Struct
	kill bool
	err  error
}

func (vm *VM) newTaskRec(tid int16, pool int8) *taskRec {
	vm.taskSeq++
	return &taskRec{id: vm.taskSeq, tid: tid, poolid: pool, baton: make(chan baton, 1), done: make(chan struct{})}
}

// addTaskRec records rec as t's, until nothing refers to t.
func (vm *VM) addTaskRec(t *Struct, rec *taskRec) {
	key := weak.Make(t)
	vm.taskMu.Lock()
	if vm.taskRecs == nil {
		vm.taskRecs = map[weak.Pointer[Struct]]*taskRec{}
	}
	vm.taskRecs[key] = rec
	vm.taskMu.Unlock()
	runtime.AddCleanup(t, func(k weak.Pointer[Struct]) {
		vm.taskMu.Lock()
		delete(vm.taskRecs, k)
		vm.taskMu.Unlock()
	}, key)
}

// recOf is t's record: every Task made by jl_new_task, and the root task,
// has one.
func (vm *VM) recOf(t *Struct) *taskRec {
	vm.taskMu.Lock()
	defer vm.taskMu.Unlock()
	return vm.taskRecs[weak.Make(t)]
}

// currentTask is ct, the task that runs: the root task until a switch.
func (vm *VM) currentTask() *Struct {
	if vm.cur == nil {
		vm.rootTask()
	}
	return vm.cur
}

// taskArg is argument i of a shim as a task and its record.
func (vm *VM) taskArg(a []any, i int) (*Struct, *taskRec, error) {
	if t, ok := a[i].(*Struct); ok && t.Type == vm.tTask {
		if rec := vm.recOf(t); rec != nil {
			return t, rec, nil
		}
	}
	return nil, nil, vm.typeError("ccall", "", vm.tTask, a[i])
}

// The task states of julia.h (JL_TASK_STATE_*), the values of Task._state.
const (
	taskStateRunnable = uint8(0)
	taskStateDone     = uint8(1)
	taskStateFailed   = uint8(2)
)

// rngSplit is jl_rng_split (task.c:1040-1072): the child's random state from
// its parent's, which advances the parent's linear congruential counter.
func rngSplit(src []any) [5]uint64 {
	var dst [5]uint64
	x := src[4].(uint64)
	src[4] = x*0xd1342543de82ef95 + 1
	dst[4] = src[4].(uint64)
	a := [4]uint64{0x214c146c88e47cb7, 0xa66d8cc21285aafa, 0x68c7ef2d7b1a54d4, 0xb053a7d7aa238c61}
	m := [4]uint64{0xaef17502108ef2d9, 0xf34026eeb86766af, 0x38fd70ad58dd9fbb, 0x6677f9b93ab0c04d}
	for i := range 4 {
		c := src[i].(uint64)
		w := x ^ a[i]
		c += w * (2*c + 1)
		c ^= c >> ((c >> 59) + 5)
		c *= m[i]
		c ^= c >> 43
		dst[i] = c
	}
	return dst
}

// gcInitFinalizerRNG is jl_gc_init_finalizer_rng_state (gc-common.c:282-
// 285), which Random.__init__ calls: the state finalizers would draw from,
// split from the current task's, whose counter it advances as julia's
// does. The VM runs no julia finalizer, so nothing draws from it.
func (vm *VM) gcInitFinalizerRNG() {
	vm.finalizerRNG = rngSplit(vm.currentTask().Fields[14:19])
}

// jlNewTask is jl_new_task (task.c:1074-1146): a runnable task for start,
// sticky, not yet started, in no thread, with the creator's scope, world and
// threadpool and a random state split from the creator's. The stack size is
// not observable here: a task's stack is its goroutine's, which grows as
// the call depth needs and is bounded by VM.MaxDepth.
func (vm *VM) jlNewTask(a []any) (any, error) {
	if _, ok := a[2].(int64); !ok {
		return nil, vm.typeError("ccall", "", core.Int64Type, a[2])
	}
	ct, crec := vm.currentTask(), vm.curRec
	rng := rngSplit(ct.Fields[14:19])
	t := &Struct{Type: vm.tTask, Fields: newTaskFields(a[0], a[1], ct.Fields[5], rng)}
	rec := vm.newTaskRec(-1, crec.poolid)
	rec.state = taskState{world: vm.taskWorld}
	vm.addTaskRec(t, rec)
	return t, nil
}

// setTaskTid is jl_set_task_tid (scheduler.c:64-73): a task belongs to the
// thread that first claims it.
func (rec *taskRec) setTid(tid int16) bool {
	if rec.tid == tid {
		return true
	}
	if rec.tid == -1 {
		rec.tid = tid
		return true
	}
	return false
}

// handoff gives the baton to t: the current task from now on, started on a
// goroutine of its own the first time.
func (vm *VM) handoff(t *Struct, rec *taskRec, b baton) {
	vm.cur, vm.curRec = t, rec
	if rec.started {
		rec.baton <- b
		return
	}
	rec.started = true
	vm.liveTasks++
	if vm.running == nil {
		vm.running = map[*taskRec]struct{}{}
	}
	vm.running[rec] = struct{}{}
	go vm.runTask(t, rec, b)
}

// afterSwitch is what the task that resumes does for the one that left: a
// task that is not sticky is free to move to another thread
// (task.c:692-700).
func (vm *VM) afterSwitch(prev *Struct) {
	if prev == nil || prev.Fields[8] == any(true) {
		return
	}
	if rec := vm.recOf(prev); rec != nil {
		rec.tid = -1
	}
}

// jlSwitch is jl_switch (task.c:664-718): the current task stops, next_task
// runs. The call returns when something switches back to this task, with the
// fatal error of a task that raised one, or errTaskKilled when the VM is
// closed. A task that is not runnable any more (done or failed) does not wait:
// it ends, and its goroutine hands over as its last act (runTask).
func (vm *VM) jlSwitch() (any, error) {
	ct, crec := vm.currentTask(), vm.curRec
	t := vm.nextTask
	if t == nil || t == ct {
		return nil, nil
	}
	trec := vm.recOf(t)
	if trec.started && trec.exited {
		return nil, vm.errorf("attempt to switch to exited task")
	}
	if !trec.setTid(crec.tid) {
		return nil, vm.errorf("cannot switch to task running on another thread")
	}
	if !trec.started && vm.liveTasks >= vm.maxTasks() {
		return nil, fmt.Errorf("%w: %d tasks are started (VM.MaxTasks is %d)", ErrTaskLimit, vm.liveTasks, vm.maxTasks())
	}
	vm.nextTask = nil
	if ct.Fields[7] != any(taskStateRunnable) {
		vm.exitTo = t
		return nil, errTaskExit
	}
	crec.state = vm.saveTaskState()
	vm.handoff(t, trec, baton{prev: ct})
	b := <-crec.baton
	vm.loadTaskState(crec.state)
	vm.afterSwitch(b.prev)
	switch {
	case b.kill:
		return nil, errTaskKilled
	case b.err != nil:
		return nil, b.err
	}
	return nil, nil
}

func (vm *VM) maxTasks() int {
	if vm.MaxTasks > 0 {
		return vm.MaxTasks
	}
	return DefaultMaxTasks
}

// runTask is the goroutine of a started task: _start_task, then the task's
// last hand-over. After the hand-over it touches nothing of the VM.
func (vm *VM) runTask(t *Struct, rec *taskRec, from baton) {
	vm.loadTaskState(rec.state)
	vm.afterSwitch(from.prev)
	err := vm.startTask(t)
	vm.liveTasks--
	rec.exited = true
	delete(vm.running, rec)
	switch {
	case errors.Is(err, errTaskExit):
		next := vm.exitTo
		vm.exitTo = nil
		vm.handoff(next, vm.recOf(next), baton{prev: t})
	case errors.Is(err, errTaskKilled):
		close(rec.done)
	default:
		// no julia code catches this: the root task, parked in its own
		// jl_switch, returns it to the embedder
		vm.cur, vm.curRec = vm.task, vm.rootRec
		vm.rootRec.baton <- baton{prev: t, err: err}
	}
}

// startTask is _start_task (task.c:1208-1266): the task's body, whose
// exception becomes its result, then jl_finish_task. It returns what ended the
// goroutine, never nil.
func (vm *VM) startTask(t *Struct) error {
	var res any
	if t.Fields[10] == any(true) {
		// throwto reached the task before it started: it starts with that
		// exception, which is its result
		res = t.Fields[4]
		vm.excStack = append(vm.excStack, res)
	} else {
		v, err := vm.apply(t.Fields[6], nil)
		var th *Thrown
		switch {
		case errors.As(err, &th):
			res = th.Value
			t.Fields[10] = true
			if !th.rethrown {
				vm.excStack = append(vm.excStack, res)
			}
		case err != nil:
			return err
		default:
			res = v
		}
	}
	t.Fields[4] = res
	return vm.finishTask(t)
}

// finishTask is jl_finish_task (task.c:308-349): the state becomes done or
// failed, and Base.task_done_hook picks what runs next. The hook switches
// away and the task never runs again, so it returns an error whatever it
// does.
func (vm *VM) finishTask(t *Struct) error {
	if t.Fields[10] == any(true) {
		t.Fields[7] = taskStateFailed
	} else {
		t.Fields[7] = taskStateDone
	}
	vm.taskWorld = vm.World()
	hook := vm.taskDoneHook()
	if hook == nil {
		return fmt.Errorf("interp: a task finished and Base.task_done_hook is not defined, so nothing can run next")
	}
	_, err := vm.apply(hook, []any{t})
	var th *Thrown
	switch {
	case errors.As(err, &th):
		// jl_no_exc_handler (task.c:726-740)
		return fmt.Errorf("fatal: error thrown and no exception handler available.\n%s", showValue(th.Value))
	case err != nil:
		return err
	}
	return fmt.Errorf("interp: Base.task_done_hook returned, and it must switch to another task")
}

// taskDoneHook is Base.task_done_hook in the newest world, nil when Base has
// not defined it (task.c:332-337).
func (vm *VM) taskDoneHook() any {
	if vm.baseModule == nil {
		return nil
	}
	if b := vm.baseModule.lookup("task_done_hook", vm.taskWorld); b != nil && b.Value != undef {
		return b.Value
	}
	return nil
}

// Close ends the goroutines of the tasks that were started and have not
// finished, which wait for a switch that will not come. Call it when the VM
// is done; the root task keeps working, a switch to a task that was ended
// raises julia's "attempt to switch to exited task".
func (vm *VM) Close() {
	if len(vm.running) == 0 {
		return
	}
	// what ends the goroutines unwinds their stacks, which touches the
	// per-task fields: put the root task's back
	state := vm.saveTaskState()
	recs := make([]*taskRec, 0, len(vm.running))
	for rec := range vm.running {
		recs = append(recs, rec)
	}
	for _, rec := range recs {
		rec.baton <- baton{kill: true}
		<-rec.done
	}
	vm.loadTaskState(state)
}

// describeTask names a task in an error: the root task, or its number and
// its code.
func (vm *VM) describeTask(t *Struct) string {
	if t == vm.task {
		return "the root task"
	}
	code := showValue(t.Fields[6])
	if len(code) > 60 {
		code = code[:60] + "…"
	}
	return fmt.Sprintf("task #%d (%s)", vm.recOf(t).id, code)
}

// bootTaskShims registers the C functions of tasks, scheduling and threads:
// julia -t 1's answers for the thread calls (one thread, in the default
// pool, no interactive thread).
func (vm *VM) bootTaskShims() {
	ret := func(name expr.Symbol, f func(*VM, []any) (any, error)) { vm.shims[name] = f }
	ret("jl_new_task", func(vm *VM, a []any) (any, error) { return vm.jlNewTask(a) })
	ret("jl_switch", func(vm *VM, a []any) (any, error) { return vm.jlSwitch() })
	// jl_set_next_task and jl_get_next_task (task.c:409-420)
	ret("jl_set_next_task", func(vm *VM, a []any) (any, error) {
		t, _, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		vm.nextTask = t
		return nil, nil
	})
	ret("jl_get_next_task", func(vm *VM, a []any) (any, error) {
		if vm.nextTask != nil {
			return vm.nextTask, nil
		}
		return vm.currentTask(), nil
	})
	// jl_is_task_started, jl_get_task_tid, jl_get_task_threadpoolid
	// (task.c:1645-1658)
	ret("jl_is_task_started", func(vm *VM, a []any) (any, error) {
		_, rec, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		return cbool(rec.started), nil
	})
	ret("jl_get_task_tid", func(vm *VM, a []any) (any, error) {
		_, rec, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		return rec.tid, nil
	})
	ret("jl_get_task_threadpoolid", func(vm *VM, a []any) (any, error) {
		_, rec, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		return rec.poolid, nil
	})
	// jl_set_task_tid and jl_set_task_threadpoolid (scheduler.c:64-81); the
	// thread is not checked against the thread count, as in julia
	ret("jl_set_task_tid", func(vm *VM, a []any) (any, error) {
		_, rec, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		tid, ok := a[1].(int32)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int32Type, a[1])
		}
		return cbool(rec.setTid(int16(tid))), nil
	})
	ret("jl_set_task_threadpoolid", func(vm *VM, a []any) (any, error) {
		_, rec, err := vm.taskArg(a, 0)
		if err != nil {
			return nil, err
		}
		tpid, ok := a[1].(int8)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int8Type, a[1])
		}
		if tpid < -1 || tpid >= nThreadPools {
			return int32(0), nil
		}
		rec.poolid = tpid
		return int32(1), nil
	})
	// jl_threadid (threading.c:287-290) is the current task's tid
	ret("jl_threadid", func(vm *VM, a []any) (any, error) { vm.currentTask(); return vm.curRec.tid, nil })
	// jl_threadpoolid (threading.c:292-304)
	ret("jl_threadpoolid", func(vm *VM, a []any) (any, error) {
		tid, ok := a[0].(int16)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int16Type, a[0])
		}
		if tid < 0 || tid >= nThreads {
			return nil, vm.errorf("invalid tid")
		}
		// the first pool whose running total of threads passes tid; the
		// total of all pools is nThreads, so one does
		pool := int8(0)
		for n := threadsPerPool[0]; int32(tid) >= n; n += threadsPerPool[pool] {
			pool++
		}
		return pool, nil
	})
	// jl_wakeup_thread (scheduler.c:299-303): the only thread is the caller;
	// a wakeup is what tells jl_task_get_next that the event loop did something
	ret("jl_wakeup_thread", func(vm *VM, a []any) (any, error) { vm.wakeup = true; return nil, nil })
	// the threaded region (threading.c:885-907)
	ret("jl_enter_threaded_region", func(vm *VM, a []any) (any, error) { vm.threaded++; return nil, nil })
	ret("jl_exit_threaded_region", func(vm *VM, a []any) (any, error) {
		if vm.threaded--; vm.threaded == 0 {
			vm.wakeup = true
		}
		return nil, nil
	})
	ret("jl_in_threaded_region", func(vm *VM, a []any) (any, error) { return cbool(vm.threaded != 0), nil })
	// jl_get_ptls_rng and jl_set_ptls_rng (threading.c:307-318)
	ret("jl_get_ptls_rng", func(vm *VM, a []any) (any, error) { return vm.ptlsRng, nil })
	ret("jl_set_ptls_rng", func(vm *VM, a []any) (any, error) {
		seed, ok := a[0].(uint64)
		if !ok {
			return nil, vm.typeError("ccall", "", core.UInt64Type, a[0])
		}
		vm.ptlsRng = seed
		return nil, nil
	})
	// jl_gc_safepoint and jl_cpu_suspend (locks-mt.jl:46-48): there is no
	// collector to stop for, and the one thread is the one that spins
	ret("jl_gc_safepoint", func(vm *VM, a []any) (any, error) { return nil, nil })
	ret("jl_cpu_suspend", func(vm *VM, a []any) (any, error) { return nil, nil })
	// jl_set_peek_cond (signals-unix.c): the async handle a SIGINFO or SIGUSR1
	// would send on, which Base.start_profile_listener registers; there is
	// no signal here, so the handle is only kept
	ret("jl_set_peek_cond", func(vm *VM, a []any) (any, error) {
		if _, ok := a[0].(*Pointer); !ok {
			return nil, vm.typeError("ccall", "", vm.ptrType(core.NothingType), a[0])
		}
		vm.peekCond = a[0]
		return nil, nil
	})
	// the thread counts Base reads with cglobal (threadingconstructs.jl:47-124)
	perPool := &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int32Type, core.CPU)).(*core.DataType),
		Elems: []any{threadsPerPool[0], threadsPerPool[1]}}
	vm.cglobals["jl_n_threads"] = &ptrSlot{name: "jl_n_threads", val: int32(nThreads)}
	vm.cglobals["jl_n_threadpools"] = &ptrSlot{name: "jl_n_threadpools", val: int32(nThreadPools)}
	vm.cglobals["jl_n_threads_per_pool"] = &ptrSlot{name: "jl_n_threads_per_pool",
		val: &Pointer{Type: vm.ptrType(core.Int32Type), Target: perPool}}
	vm.ptlsRng = 0x9e3779b97f4a7c15
}

// julia -t 1: one thread, the interactive pool (0) with none and the default
// pool (1) with it.
const (
	nThreads     = 1
	nThreadPools = 2
)

var threadsPerPool = [2]int32{0, 1}
