package interp

import (
	"errors"
	"sort"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
)

// Kernel.Reset (#93): put the kernel back in the state NewKernel left it in
// (the initializers run, the helpers defined, Main holding nothing), without
// loading the image again.
//
// What a cell can change, and what undoes it:
//   - a field of a Struct or an element of a Memory that the image holds: the
//     undo log (undo.go), replayed in reverse;
//   - the methods of a function: Table.Revert to the world of the cut, for the
//     tables a cell defined into or deleted from (VM.touch);
//   - the bindings of every module, the usings and options of every module,
//     the modules a cell made, the functions and constructors it made: the
//     snapshot NewKernel took (kernelSnap), put back.
//
// What it does not undo is listed in the doc of Reset.

var (
	// ErrResetOverflow is a Reset of a kernel whose cells changed more than
	// the undo log holds (DefaultMaxUndo entries), and that has no image to
	// load again (NewKernel was given a VM, not an image).
	ErrResetOverflow = errors.New("interp: the cells changed more than the undo log holds, and the kernel has no image to reload")
)

// ResetInfo says how a Reset was made.
type ResetInfo struct {
	// Reloaded is true when the undo log overflowed and the image was loaded
	// again into a new VM: the kernel's VM is another one then (Kernel.VM),
	// with the embedder's Stdout, Stderr, MaxSteps, MaxDepth and
	// MaxMemoryElems carried over.
	Reloaded bool
	// Undone is the writes the log held and replayed (0 when Reloaded).
	Undone int
	// Tables is the method tables reverted to the world of the cut.
	Tables int
	// Tasks is the tasks cells started that Reset stopped, and Timers the
	// active libuv timers it closed (see Reset: nothing of theirs runs again).
	// A reload counts them too, as the old VM closes.
	Tasks, Timers int
}

// resetStopsTasks is whether Reset stops the tasks and timers cells started
// before it rolls back. It is true: the tests turn it off to show that each of
// them fails when stopping is not done (#104).
var resetStopsTasks = true

// kernelSnap is what NewKernel records for Reset: state that no write of the
// log covers, because Go code, not a Julia write, changes it.
type kernelSnap struct {
	cut       uint64 // the world of the state: nothing was defined in it
	mods      []modSnap
	binds     []*Binding // every binding of every module...
	saved     []Binding  // ...as it was
	initOrder []*Module
	modules   map[expr.Symbol]*Module
	sessions  map[*Module]*Session
	funcs     map[*core.DataType]*Function
	ctors     map[*core.TypeName]*dispatch.Table
	moduleCtr uint64
	// the tasks made and the libuv handles open at the cut: Base's own, which
	// Reset leaves alone (a task or a handle after them is a cell's)
	tasks   map[*taskRec]struct{}
	handles map[*uvHandle]struct{}
}

type modSnap struct {
	m        *Module
	bindings map[expr.Symbol]*Binding
	usings   []*Module
	istopmod bool
	opts     moduleOpts
	file     expr.Symbol
	line     int32
}

// modules lists every module reachable from the roots: the root modules, Main,
// the modules bound in a module and the ones it uses.
func (vm *VM) allModules() []*Module {
	seen := map[*Module]bool{}
	var out []*Module
	var visit func(m *Module)
	visit = func(m *Module) {
		if m == nil || seen[m] {
			return
		}
		seen[m] = true
		out = append(out, m)
		m.mu.RLock()
		var next []*Module
		next = append(next, m.usings...)
		for _, b := range m.bindings {
			if sub, ok := b.Value.(*Module); ok {
				next = append(next, sub)
			}
		}
		m.mu.RUnlock()
		for _, n := range next {
			visit(n)
		}
	}
	visit(vm.Core)
	visit(vm.Base)
	visit(vm.Main)
	for _, m := range vm.modules {
		visit(m)
	}
	return out
}

func (vm *VM) takeSnap() *kernelSnap {
	s := &kernelSnap{
		cut:       vm.World(),
		initOrder: append([]*Module(nil), vm.moduleInitOrder...),
		modules:   copyMap(vm.modules),
		sessions:  copyMap(vm.sessions),
		funcs:     copyMap(vm.funcs),
		ctors:     copyMap(vm.ctors),
		moduleCtr: vm.moduleCtr,
		tasks:     map[*taskRec]struct{}{},
		handles:   map[*uvHandle]struct{}{},
	}
	// every task made by the cut, started or not: Base makes some that start
	// on the first yield (the profile listeners), and they are Base's
	vm.taskMu.Lock()
	for _, rec := range vm.taskRecs {
		s.tasks[rec] = struct{}{}
	}
	vm.taskMu.Unlock()
	if vm.loop != nil {
		for _, h := range vm.loop.order {
			s.handles[h] = struct{}{}
		}
	}
	for _, m := range vm.allModules() {
		m.mu.RLock()
		s.mods = append(s.mods, modSnap{m: m, bindings: copyMap(m.bindings), usings: append([]*Module(nil), m.usings...),
			istopmod: m.istopmod, opts: m.opts, file: m.file, line: m.line})
		for _, b := range m.bindings {
			s.binds = append(s.binds, b)
			s.saved = append(s.saved, *b)
		}
		m.mu.RUnlock()
	}
	return s
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func replaceMap[K comparable, V any](dst *map[K]V, from map[K]V) {
	m := *dst
	for k := range m {
		if _, ok := from[k]; !ok {
			delete(m, k)
		}
	}
	for k, v := range from {
		m[k] = v
	}
}

// restore puts back what the snapshot holds.
func (vm *VM) restoreSnap(s *kernelSnap) {
	for i, b := range s.binds {
		*b = s.saved[i]
	}
	for i := range s.mods {
		ms := &s.mods[i]
		m := ms.m
		m.mu.Lock()
		m.bindings = copyMap(ms.bindings)
		m.usings = append([]*Module(nil), ms.usings...)
		m.istopmod, m.opts, m.file, m.line = ms.istopmod, ms.opts, ms.file, ms.line
		m.mu.Unlock()
	}
	vm.moduleInitOrder = append([]*Module(nil), s.initOrder...)
	replaceMap(&vm.modules, s.modules)
	replaceMap(&vm.sessions, s.sessions)
	replaceMap(&vm.funcs, s.funcs)
	replaceMap(&vm.ctors, s.ctors)
	vm.moduleCtr = s.moduleCtr
}

// touch notes that a cell changed the methods of tab, so that Reset reverts it.
func (vm *VM) touch(tab *dispatch.Table) {
	if l := vm.undo; l.on {
		if l.tables == nil {
			l.tables = map[*dispatch.Table]struct{}{}
		}
		l.tables[tab] = struct{}{}
	}
}

// Reset puts the kernel back in the state NewKernel returned it in: what the
// cells defined, assigned, imported or mutated is gone, and the next cell runs
// as the first of a kernel just made. It costs what the cells changed, not the
// size of the image; past DefaultMaxUndo writes it loads the image again
// instead (ResetInfo.Reloaded), or, for a kernel made by NewKernel from a VM,
// not from an image, fails with ErrResetOverflow and changes nothing. Like
// Eval, it is refused with ErrKernelBusy while a cell runs.
//
// Before it rolls back, Reset stops every task a cell started that has not
// finished, and closes every libuv timer (and other handle) a cell opened, and
// ResetInfo counts them. A stopped task never runs again, and NONE OF ITS
// finally OR catch BLOCKS RUNS: julia cannot kill a task, and running one
// (schedule(t, InterruptException(); error=true)) would run user code during
// the Reset, which can print, block or write the state the Reset restores. The
// rollback restores the lists such a task waited in, so they are restored
// without it. Tasks that existed when the kernel was made, Base's own (it
// makes the profile listeners then, and they start on the first yield), are
// left alone. A task that never ran holds nothing, and is not counted.
//
// What it does not undo, by design:
//   - state the Go side of the VM holds that Julia code can reach only through
//     a ccall: open files and sockets, the environment, the working directory,
//     Stdout and Stderr and what was written to them, the clock, libraries
//     opened with dlopen, the state of libgmp and libmpfr;
//   - the caches: the types a cell made stay in the type caches of the image's
//     types until the collector drops them (they are weak), the method
//     specializations and compiled bodies stay, and a limit an embedder set
//     through Kernel.VM() is the one in force;
//   - the world counter and the object-id counters, which only move forward:
//     Base.get_world_counter() and objectid of a new object differ from a
//     fresh kernel's;
//   - a write to a field of the image from Go code (the interpreter's own
//     bookkeeping of method instances, code instances and tasks), which is not
//     a write a cell makes: a cell's own writes go through setfield!,
//     swapfield!, modifyfield!, replacefield!, setfield-once, memory stores
//     and copies, and are all logged.
func (k *Kernel) Reset() (ResetInfo, error) {
	if err := k.enter(); err != nil {
		return ResetInfo{}, err
	}
	defer k.busy.Store(false)
	vm := k.vm
	l := vm.undo
	// the tasks and timers go first: a list a task waits in is restored by the
	// replay below to what it held before the cell, and a task that was still
	// in it would be put back with it
	var info ResetInfo
	if resetStopsTasks {
		info.Tasks, info.Timers = k.counts()
	}
	if l.overflow {
		// the old VM closes: its Close ends every task that has not finished
		rinfo, err := k.reload()
		rinfo.Tasks, rinfo.Timers = info.Tasks, info.Timers
		return rinfo, err
	}
	if resetStopsTasks {
		k.stopCellTasks()
	}
	if resetTrace != nil {
		resetTrace("replay")
	}
	l.on = false
	n := len(l.entries)
	for i := n - 1; i >= 0; i-- {
		l.entries[i].undo()
		l.entries[i] = undoEntry{}
	}
	l.entries = l.entries[:0]
	if cap(l.entries) > 1<<12 {
		l.entries = nil
	}
	nt := len(l.tables)
	for t := range l.tables {
		t.Revert(k.snap.cut)
	}
	clear(l.tables)
	vm.restoreSnap(k.snap)
	vm.newWorld() // past every world a cell used; the undone methods are gone, not ended
	vm.taskWorld = vm.World()
	vm.excStack = nil
	k.resetSession()
	l.on = true
	info.Undone, info.Tables = n, nt
	return info, nil
}

// resetTrace, when a test sets it, is told the step Reset is at ("replay": the
// tasks and timers are stopped, the log is about to be replayed).
var resetTrace func(step string)

// cellTasks is the tasks a cell started that have not finished, oldest first:
// the started tasks that did not exist at the cut.
func (vm *VM) cellTasks(snap *kernelSnap) []*taskRec {
	var recs []*taskRec
	for rec := range vm.running {
		if _, base := snap.tasks[rec]; !base {
			recs = append(recs, rec)
		}
	}
	sort.Slice(recs, func(i, j int) bool { return recs[i].id < recs[j].id })
	return recs
}

// cellTimers is the libuv timers a cell started that are armed.
func (vm *VM) cellTimers(snap *kernelSnap) int {
	n := 0
	if vm.loop != nil {
		for _, h := range vm.loop.order {
			if _, base := snap.handles[h]; !base && h.kind == uvTimer && h.active {
				n++
			}
		}
	}
	return n
}

// counts is the live tasks and armed timers the cells started.
func (k *Kernel) counts() (tasks, timers int) {
	return len(k.vm.cellTasks(k.snap)), k.vm.cellTimers(k.snap)
}

// Tasks is the tasks and the timers the cells started that are alive: the tasks
// that have run and not finished (a task that never ran holds nothing), and the
// armed libuv timers, `sleep`'s included. Base's own, the ones that were alive
// when the kernel was made, are not counted. Reset stops them; this is for an
// embedder that wants to decide for itself. Like Has, it is refused with
// ErrKernelBusy while a cell runs.
func (k *Kernel) Tasks() (tasks, timers int, err error) {
	if err := k.enter(); err != nil {
		return 0, 0, err
	}
	defer k.busy.Store(false)
	tasks, timers = k.counts()
	return tasks, timers, nil
}

// stopCellTasks ends the tasks and closes the timers the cells started, as
// Kernel.Reset needs before it rolls back. A task is taken out of every queue
// and wait list by never running again: its goroutine, parked in a switch, is
// told to unwind (the kill Close gives every task of a closing VM), which no
// try block catches, so NO finally and no catch block of the task runs: user
// code during a Reset would write the state the Reset restores. The task's
// object is marked done, so that istaskdone and wait answer as for a task that
// ended. A timer is dropped as close(t) would, without its close callback
// (Base's _uv_hook_close wakes the tasks that wait on it, which are gone), and
// so is every other libuv handle a cell opened.
func (k *Kernel) stopCellTasks() {
	vm := k.vm
	recs := vm.cellTasks(k.snap)
	if len(recs) > 0 {
		// unwinding the goroutines touches the per-task fields: put back
		// the root task's
		state := vm.saveTaskState()
		for _, rec := range recs {
			if t := rec.task.Value(); t != nil {
				t.Fields[7] = taskStateDone
			}
			rec.baton <- baton{kill: true}
			<-rec.done
		}
		vm.loadTaskState(state)
	}
	if l := vm.loop; l != nil {
		l.dropExcept(k.snap.handles)
	}
}

// dropExcept forgets every handle but keep's: it will not fire, and nothing
// waits for its close callback.
func (l *uvLoop) dropExcept(keep map[*uvHandle]struct{}) {
	kept := l.order[:0]
	for _, h := range l.order {
		if _, ok := keep[h]; ok {
			kept = append(kept, h)
			continue
		}
		h.active, h.pending = false, false
		delete(l.handles, h.mem)
	}
	clear(l.order[len(kept):])
	l.order = kept
	closing := l.closing[:0]
	for _, h := range l.closing {
		if _, ok := keep[h]; ok {
			closing = append(closing, h)
		}
	}
	clear(l.closing[len(closing):])
	l.closing = closing
}

// resetSession gives Main the lowering state of a session never used.
func (k *Kernel) resetSession() {
	vm := k.vm
	k.sess.low = lower.NewStateIn(exprModule(k.sess.Mod), vm.baseRelativeTo(k.sess.Mod).Name)
	k.sess.file, k.sess.line = "", 0
}

// reload loads the image again into a new VM, in place of the one whose log
// overflowed.
func (k *Kernel) reload() (ResetInfo, error) {
	if k.load == nil {
		return ResetInfo{}, ErrResetOverflow
	}
	old := k.vm
	nvm, err := k.load()
	if err != nil {
		return ResetInfo{}, err
	}
	nvm.Stdout, nvm.Stderr = old.Stdout, old.Stderr
	nvm.MaxSteps, nvm.MaxDepth, nvm.MaxMemoryElems = old.MaxSteps, old.MaxDepth, old.MaxMemoryElems
	nk, err := newKernelOn(nvm, k.ko)
	if err != nil {
		nvm.Close()
		return ResetInfo{}, err
	}
	k.vm, k.sess, k.snap = nk.vm, nk.sess, nk.snap
	k.show, k.errText, k.schedule = nk.show, nk.errText, nk.schedule
	nvm.kernel = k
	old.Close()
	old.kernel = nil
	return ResetInfo{Reloaded: true}, nil
}
