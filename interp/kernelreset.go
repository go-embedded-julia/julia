package interp

import (
	"bytes"
	"errors"

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
}

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
// What it does not undo, by design:
//   - state the Go side of the VM holds that Julia code can reach only through
//     a ccall: open files and sockets, the environment, the working directory,
//     Stdout and Stderr and what was written to them, the clock, tasks and
//     timers a cell started (cancel its ctx and wait before Reset), libraries
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
	if l.overflow {
		return k.reload()
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
	return ResetInfo{Undone: n, Tables: nt}, nil
}

// resetSession gives Main the lowering state of a session never used.
func (k *Kernel) resetSession() {
	k.sess.low = lower.NewState()
	k.sess.file, k.sess.line = "", 0
}

// reload loads the image again into a new VM, in place of the one whose log
// overflowed.
func (k *Kernel) reload() (ResetInfo, error) {
	if k.img == nil {
		return ResetInfo{}, ErrResetOverflow
	}
	old := k.vm
	nvm, err := LoadImage(bytes.NewReader(k.img), k.lo)
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
