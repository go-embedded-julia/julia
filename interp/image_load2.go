package interp

import (
	"fmt"
	"reflect"
	"sync"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// weak reads a weak pointer: a reference to its target, or nil. Only the two
// weak types the VM holds exist (an Expr's args vector keeps its Expr, the
// address table its Memory), made again with weak.Make.
func (d *imgDec) weak(v reflect.Value) {
	x := d.uv()
	if x == 0 {
		v.SetZero()
		return
	}
	if x == 1 || x-2 >= uint64(len(d.objs)) {
		d.failf(ErrImageCorrupt, "a weak pointer to object %d of %d", x-2, len(d.objs))
	}
	o := d.objs[x-2]
	switch v.Type() {
	case weakExprType:
		if p, ok := o.Interface().(*expr.Expr); ok {
			v.Set(reflect.ValueOf(weak.Make(p)))
			return
		}
	case weakMemoryType:
		if p, ok := o.Interface().(*Memory); ok {
			v.Set(reflect.ValueOf(weak.Make(p)))
			return
		}
	}
	d.failf(ErrImageCorrupt, "a weak %s to a %s", v.Type(), o.Type())
}

// roots decodes the saved fields of the VM by name.
func (d *imgDec) roots() {
	d.cur, d.depth = d.fr.roots, 0
	n := d.uv()
	if n > uint64(len(d.cur)) {
		d.failf(ErrImageCorrupt, "%d roots in %d bytes", n, len(d.cur))
	}
	rv := reflect.ValueOf(d.vm).Elem()
	seen := map[string]bool{}
	for i := uint64(0); i < n; i++ {
		s := d.uv()
		if s >= uint64(len(d.fr.strs)) {
			d.failf(ErrImageCorrupt, "string %d of %d", s, len(d.fr.strs))
		}
		name := d.fr.strs[s]
		if saved, ok := d.spec.roots[name]; !ok || !saved || seen[name] {
			d.failf(ErrImageCorrupt, "the root %q is not a saved field of the VM, or is there twice", name)
		}
		seen[name] = true
		f := rv.FieldByName(name)
		if !f.IsValid() {
			d.failf(ErrImageCorrupt, "the VM has no field %q", name)
		}
		d.value(rw(f))
	}
	if len(d.cur) != 0 {
		d.failf(ErrImageCorrupt, "%d bytes after the roots", len(d.cur))
	}
}

// ----- interning -----

// intern puts the types that were in a cache when the image was taken back
// in the caches, parameters first (jl_cache_type_, the post-order julia uses
// for uniquing, staticdata.c:1122-1124). A type the process already holds is
// that one: every reference to the one the image made is redirected to it,
// which the second pass of records does (the objects that hold it are decoded
// again).
func (d *imgDec) intern() {
	d.cur = d.fr.interned
	n := d.uv()
	if n > uint64(len(d.cur)) {
		d.failf(ErrImageCorrupt, "%d interned types in %d bytes", n, len(d.cur))
	}
	ids := make([]uint32, n)
	st := &internState{done: map[uint32]bool{}, set: map[uint32]bool{}, id: map[*core.DataType]uint32{},
		redirect: map[*core.DataType]*core.DataType{}}
	for i := range ids {
		x := d.uv()
		if x >= uint64(len(d.objs)) || d.fr.kinds[x].tier != tierFull || d.types[d.fr.kinds[x].ti] != dataTypePtrType {
			d.failf(ErrImageCorrupt, "interned object %d is not a type of the image", x)
		}
		ids[i] = uint32(x)
		st.set[uint32(x)] = true
		st.id[d.objs[x].Interface().(*core.DataType)] = uint32(x)
	}
	if len(d.cur) != 0 {
		d.failf(ErrImageCorrupt, "%d bytes after the interned types", len(d.cur))
	}
	for _, id := range ids {
		d.internOne(st, id)
	}
}

type internState struct {
	done, set map[uint32]bool
	id        map[*core.DataType]uint32
	redirect  map[*core.DataType]*core.DataType
}

func (d *imgDec) internOne(st *internState, id uint32) {
	if st.done[id] {
		return
	}
	st.done[id] = true
	dt := d.objs[id].Interface().(*core.DataType)
	if dt.Name == nil {
		d.failf(ErrImageCorrupt, "interned type %d has no name", id)
	}
	seen := map[any]bool{}
	for i, p := range dt.Parameters {
		dt.Parameters[i] = d.canon(st, p, seen)
	}
	// the interner writes nothing to the shared caches: what it holds is put
	// there by commit, when the whole image has been decoded
	if c, _ := d.in.Resolve(dt); c != dt {
		d.objs[id] = reflect.ValueOf(c)
		d.redir[id] = true
		st.redirect[dt] = c
		d.replaced = append(d.replaced, [2]*core.DataType{dt, c})
	}
}

// instanceXfer is the cached instance of a type of the image, to give to the
// type that replaced it.
type instanceXfer struct {
	to *core.DataType
	v  any
}

// instanceTransfers is what a type the process holds takes from the image's
// type it replaced: the instance of a type is state the VMs of a process
// share, as its hints are, and a process that has not run the program has not
// made it. Like a hint, an instance is set when the type has none, left when
// the type has an equal one, and refused (ErrImageConflict) when it has
// another. It writes nothing.
func (d *imgDec) instanceTransfers() []instanceXfer {
	var xfers []instanceXfer
	pending := map[*core.DataType]any{}
	for _, p := range d.replaced {
		v, ok := p[0].CachedInstance()
		if !ok {
			continue
		}
		cur, has := p[1].CachedInstance()
		if !has {
			cur, has = pending[p[1]]
		}
		if !has {
			pending[p[1]] = v
			xfers = append(xfers, instanceXfer{p[1], v})
		} else if !d.vm.egal(cur, v) {
			d.failf(ErrImageConflict, "the instance of %s is another value here than in the image: another image was loaded in this process", p[1].Name.Name)
		}
	}
	return xfers
}

// canon is the type the process holds for x, in the parts of x that are types
// of the image: a data type interned before it is used, and the types inside
// unions, wrappers, bounds and tuples rewritten in place.
func (d *imgDec) canon(st *internState, x any, seen map[any]bool) any {
	switch t := x.(type) {
	case *core.DataType:
		if t == nil {
			return x
		}
		if c, ok := st.redirect[t]; ok {
			return c
		}
		if id, ok := st.id[t]; ok && !st.done[id] {
			d.internOne(st, id)
			if c, ok := st.redirect[t]; ok {
				return c
			}
			return t
		}
		if seen[t] {
			return t
		}
		seen[t] = true
		for i, p := range t.Parameters {
			t.Parameters[i] = d.canon(st, p, seen)
		}
	case *core.UnionAll:
		if t == nil || seen[t] {
			return x
		}
		seen[t] = true
		t.Var, _ = d.canon(st, t.Var, seen).(*core.TypeVar)
		t.Body = d.canon(st, t.Body, seen)
	case *core.Union:
		if t == nil || seen[t] {
			return x
		}
		seen[t] = true
		t.A, t.B = d.canon(st, t.A, seen), d.canon(st, t.B, seen)
	case *core.TypeVar:
		if t == nil || seen[t] {
			return x
		}
		seen[t] = true
		t.LB, t.UB = d.canon(st, t.LB, seen), d.canon(st, t.UB, seen)
	case *core.Vararg:
		if t == nil || seen[t] {
			return x
		}
		seen[t] = true
		t.T, t.N = d.canon(st, t.T, seen), d.canon(st, t.N, seen)
	case *core.Tuple:
		if t == nil || seen[t] {
			return x
		}
		seen[t] = true
		if c, ok := d.canon(st, t.Type, seen).(*core.DataType); ok {
			t.Type = c
		}
		for i, e := range t.Elems {
			t.Elems[i] = d.canon(st, e, seen)
		}
	}
	return x
}

// ----- hints on core's shared types -----

type loadHint struct {
	tn    *core.TypeName
	field string
	val   int64 // the value, or the object of a cached type
}

// parseHints reads what Base wrote on core's type names and refuses, before
// anything is changed, a numeric hint the process already holds with another
// value (another image was loaded in this process).
func (d *imgDec) parseHints() []loadHint {
	d.cur = d.fr.hints
	n := d.uv()
	if n > uint64(len(d.cur)) {
		d.failf(ErrImageCorrupt, "%d hints in %d bytes", n, len(d.cur))
	}
	statics := core.StaticObjects()
	hints := make([]loadHint, 0, n)
	for i := uint64(0); i < n; i++ {
		idx, s, val := d.uv(), d.uv(), d.zz()
		if idx >= uint64(len(statics)) || s >= uint64(len(d.fr.strs)) {
			d.failf(ErrImageCorrupt, "a hint of static object %d, string %d", idx, s)
		}
		tn, ok := statics[idx].(*core.TypeName)
		field := d.fr.strs[s]
		known := false
		for _, f := range imageHintFields {
			known = known || f == field
		}
		if !ok || !known {
			d.failf(ErrImageCorrupt, "a hint %q of static object %d", field, idx)
		}
		h := loadHint{tn, field, val}
		if field == "typeofWrapper" {
			if val < 0 || val >= int64(len(d.fr.kinds)) {
				d.failf(ErrImageCorrupt, "the Type{Wrapper} of %s is object %d of %d", tn.Name, val, len(d.fr.kinds))
			}
		} else {
			d.hintConflict(h) // a numeric conflict is refused before anything is decoded
		}
		hints = append(hints, h)
	}
	if len(d.cur) != 0 {
		d.failf(ErrImageCorrupt, "%d bytes after the hints", len(d.cur))
	}
	return hints
}

// hintConflict refuses (ErrImageConflict) a hint the process holds with another
// value: a number that differs, or a cached Type{Wrapper} that is another type
// than the image's. It writes nothing.
func (d *imgDec) hintConflict(h loadHint) {
	if h.field == "typeofWrapper" {
		dt, ok := d.objs[h.val].Interface().(*core.DataType)
		if !ok {
			d.failf(ErrImageCorrupt, "the Type{Wrapper} of %s is not a type", h.tn.Name)
		}
		if cur := h.tn.TypeofWrapper(); cur != nil && cur != dt {
			d.failf(ErrImageConflict, "%s.typeofWrapper is another type here than in the image", h.tn.Name)
		}
	} else if cur, _, set := hintValue(h.tn, h.field); set && cur != h.val {
		d.failf(ErrImageConflict, "%s.%s is %d here and %d in the image: another image was loaded in this process",
			h.tn.Name, h.field, cur, h.val)
	}
}

// imageCommitMu serializes the commits of loads, so that the check of what a
// load would write and the write are one step among them.
var imageCommitMu sync.Mutex

// commit is the only place a load writes to what the VMs of the process
// share: the types it interned, the cached instances it gave and the hints it
// set. The image has been decoded whole against structures of its own. Every
// conflict is looked for first, and the first write is made when there is none
// (a cache another goroutine grew since the interner read it is reported as
// core.IsInternChanged, and the caller decodes again).
func (d *imgDec) commit(hints []loadHint) {
	imageCommitMu.Lock()
	defer imageCommitMu.Unlock()
	for _, h := range hints {
		d.hintConflict(h)
	}
	xfers := d.instanceTransfers()
	if err := d.in.Commit(); err != nil {
		panic(imgFail{fmt.Errorf("%w: %w", ErrImageConflict, err)})
	}
	for _, x := range xfers {
		x.to.SetCachedInstance(x.v)
	}
	d.applyHints(hints)
}

// applyHints sets the hints that are unset; one already set to the same value
// is left. commit has checked them all (hintConflict) before it writes any.
func (d *imgDec) applyHints(hints []loadHint) {
	for _, h := range hints {
		switch h.field {
		case "MaxArgs":
			h.tn.MaxArgs.CompareAndSwap(0, int32(h.val))
		case "CacheEntryCount":
			h.tn.CacheEntryCount.CompareAndSwap(0, uint32(h.val))
		case "MaxMethods":
			h.tn.MaxMethods.CompareAndSwap(0, uint32(h.val))
		case "ConstpropHeuristic":
			h.tn.ConstpropHeuristic.CompareAndSwap(0, uint32(h.val))
		default:
			h.tn.SetTypeofWrapper(d.objs[h.val].Interface().(*core.DataType))
		}
	}
}

// ----- the root task and what is derived -----

// finishTask makes the placeholder the root task's references point at the
// loading VM's root task, with the task-local storage the image holds.
func (d *imgDec) finishTask() {
	for i, k := range d.fr.kinds {
		if k.tier != tierTask {
			continue
		}
		t := d.objs[i].Interface().(*Struct)
		if d.vm.task != t || d.vm.tTask == nil {
			d.failf(ErrImageCorrupt, "the root task object is not the VM's task")
		}
		d.vm.initRootTask(t)
		for j, n := range d.vm.tTask.Name.FieldNames {
			if n == "storage" {
				d.cur, d.depth = d.fr.recs[i], 0
				d.value(reflect.ValueOf(&t.Fields[j]).Elem())
				if len(d.cur) != 0 {
					d.failf(ErrImageCorrupt, "%d bytes after the root task", len(d.cur))
				}
				return
			}
		}
		d.failf(ErrImageCorrupt, "Core.Task has no storage field")
	}
}

// rebuild sets what an image leaves to the loader: the method tables answer
// from their methods, the sessions hold their VM, the Go bodies are made
// again from their names, reflectedOf is reflected by the method object.
func (d *imgDec) rebuild() {
	for i, k := range d.fr.kinds {
		if k.tier == tierStatic || d.redir[uint32(i)] {
			continue
		}
		switch d.types[k.ti] {
		case tablePtrType:
			d.objs[i].Interface().(*dispatch.Table).ResetAfterLoad()
		case sessionPtrType:
			d.objs[i].Interface().(*Session).VM = d.vm
		case goBodyPtrType:
			b := d.objs[i].Interface().(*goBody)
			nb, err := newGoBody(b.Name, b.Capture)
			if err != nil {
				d.failf(ErrImageCorrupt, "%v", err)
			}
			b.fn = nb.fn
		}
	}
	d.vm.reflectedOf = make(map[*Struct]*reflected, len(d.vm.reflected))
	for _, r := range d.vm.reflected {
		d.vm.reflectedOf[r.method] = r
	}
	d.vm.taskWorld = d.vm.World()
}
