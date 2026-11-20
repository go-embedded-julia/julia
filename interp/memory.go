package interp

import (
	"fmt"
	"math"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// GenericMemory and GenericMemoryRef, the storage under Array since julia
// 1.11 (genericmemory.c; the builtins of builtins.c:1711-1960). Elements are
// held boxed whatever their type: a slot never assigned holds undef, where
// julia's pointer slots hold NULL; a slot of a primitive or singleton
// element type starts at its zero value, as julia's zero-initialised
// memory reads.

// Memory is a GenericMemory{kind, T, addrspace} instance. Its elements are
// reached through Len, Get, Set, Slice and All, and made by newMemory: nothing
// but this file names the field that holds them, so that the representation
// (today a []any of boxed values) can change one element type at a time
// (#81). TestMemoryStoreIsPrivate keeps it so.
type Memory struct {
	Type  *core.DataType
	elems []any
	// u8 holds the elements of a memory whose kind is kindU8, and elems is
	// then nil
	u8 []uint8
	// floats holds those of a memory whose kind is kindF64
	floats []float64
	kind   memKind
	id     uint64 // its object id (objectid.go)
	// undo is the log of the VM that loaded this memory from an image, nil for
	// a memory a cell made (undo.go).
	undo *undoLog
}

// memKind is how a Memory stores its elements: boxed, or in a slice of its
// own element type (#81). A typed kind has no undef slot: a primitive element
// starts at zero, as julia's zero-initialised memory reads.
type memKind uint8

const (
	kindAny memKind = iota // []any, the boxed values
	kindU8                 // []uint8
	kindF64                // []float64
)

// typedKinds is the table of element types that have a typed store. Adding a
// type is one line here, and the sites that move bytes in bulk (Bytes) then
// reach it; every other site goes through Get and Set and needs no change. It
// is the one place that says which kinds are typed (TestTypedKinds).
var typedKinds = map[any]memKind{
	core.UInt8Type:   kindU8,
	core.Float64Type: kindF64,
}

// memKindOf is the kind of the memory type t.
func memKindOf(t *core.DataType) memKind {
	if t != nil && len(t.Parameters) == 3 {
		return typedKinds[t.Parameters[1]]
	}
	return kindAny
}

// stores says which stores a memory holds: the boxed one, the one of bytes, the
// one of floats. Exactly one of them is, and only the tests ask
// (TestTypedKinds).
func (m *Memory) stores() (boxed, bytes, floats bool) {
	return m.elems != nil, m.u8 != nil, m.floats != nil
}

// newMemory is a Memory of type t holding elems, which it takes over: a typed
// kind copies them into its own slice (a slot that does not hold the element
// type, undef, reads as zero).
func newMemory(t *core.DataType, elems []any) *Memory {
	m := &Memory{Type: t, kind: memKindOf(t)}
	switch m.kind {
	case kindU8:
		m.u8 = make([]uint8, len(elems))
		for i, e := range elems {
			m.u8[i], _ = e.(uint8)
		}
	case kindF64:
		m.floats = make([]float64, len(elems))
		for i, e := range elems {
			m.floats[i], _ = e.(float64)
		}
	default:
		m.elems = elems
	}
	return m
}

// newMemoryN is a Memory of type t and n elements, each the zero of the
// element type for a typed kind and nil otherwise: the caller fills the boxed
// kinds.
func newMemoryN(t *core.DataType, n int) *Memory {
	switch k := memKindOf(t); k {
	case kindU8:
		return &Memory{Type: t, kind: k, u8: make([]uint8, n)}
	case kindF64:
		return &Memory{Type: t, kind: k, floats: make([]float64, n)}
	}
	return &Memory{Type: t, elems: make([]any, n)}
}

// Len is the number of elements.
func (m *Memory) Len() int {
	switch m.kind {
	case kindU8:
		return len(m.u8)
	case kindF64:
		return len(m.floats)
	}
	return len(m.elems)
}

// Get is element i (0-based), as it is stored: undef for a boxed slot never
// assigned. A typed element is boxed to be returned (a Float64 allocates unless
// it is small; a UInt8 does not).
func (m *Memory) Get(i int) any {
	switch m.kind {
	case kindU8:
		return m.u8[i]
	case kindF64:
		return m.floats[i]
	}
	return m.elems[i]
}

// Set stores v as element i.
func (m *Memory) Set(i int, v any) {
	if l := m.undo; l != nil && l.on {
		l.recordElem(m, i)
	}
	switch m.kind {
	case kindU8:
		m.u8[i] = v.(uint8)
	case kindF64:
		m.floats[i] = v.(float64)
	default:
		m.elems[i] = v
	}
}

// Slice is elements lo to hi (exclusive), to read. A boxed memory answers a
// slice that shares the store, so that the code that searches or moves runs of
// elements (the tables of IdDict and IdSet) works in place on a memory it made;
// a typed memory answers a copy, and a write through it is lost: write through
// Set, Written or Bytes.
func (m *Memory) Slice(lo, hi int) []any {
	switch m.kind {
	case kindU8:
		out := make([]any, hi-lo)
		for i, b := range m.u8[lo:hi] {
			out[i] = b
		}
		return out
	case kindF64:
		out := make([]any, hi-lo)
		for i, f := range m.floats[lo:hi] {
			out[i] = f
		}
		return out
	}
	return m.elems[lo:hi]
}

// All is every element, as Slice(0, Len()).
func (m *Memory) All() []any { return m.Slice(0, m.Len()) }

// Bytes is the elements lo to hi of a memory of UInt8 as a slice that shares
// the store, to read, and false for any other kind. A write through it is not
// logged: write through WrittenBytes.
func (m *Memory) Bytes(lo, hi int) ([]byte, bool) {
	if m.kind != kindU8 {
		return nil, false
	}
	return m.u8[lo:hi], true
}

// WrittenBytes is Bytes for a caller that is about to write the range: the old
// values are recorded first, when the memory is one the image holds (undo.go).
func (m *Memory) WrittenBytes(lo, hi int) ([]byte, bool) {
	if m.kind != kindU8 {
		return nil, false
	}
	if l := m.undo; l != nil && l.on {
		l.recordRange(m, lo, hi)
	}
	return m.u8[lo:hi], true
}

// Float64s is the elements lo to hi of a memory of Float64 as a slice that
// shares the store, to read, and false for any other kind: what the BLAS and the
// Kernel's value table read, with no boxing. A write through it is not logged:
// write through WrittenFloat64s.
func (m *Memory) Float64s(lo, hi int) ([]float64, bool) {
	if m.kind != kindF64 {
		return nil, false
	}
	return m.floats[lo:hi], true
}

// WrittenFloat64s is Float64s for a caller that is about to write the range:
// the old values are recorded first, when the memory is one the image holds
// (undo.go).
func (m *Memory) WrittenFloat64s(lo, hi int) ([]float64, bool) {
	if m.kind != kindF64 {
		return nil, false
	}
	if l := m.undo; l != nil && l.on {
		l.recordRange(m, lo, hi)
	}
	return m.floats[lo:hi], true
}

// Written is Slice for a caller that is about to write the range of a boxed
// memory: the old values are recorded first, when the memory is one the image
// holds (undo.go). A typed memory has none: see CopyFrom and WrittenBytes.
func (m *Memory) Written(lo, hi int) []any {
	if m.kind != kindAny {
		panic("interp: Written of a typed Memory (use WrittenBytes, WrittenFloat64s or CopyFrom)")
	}
	if l := m.undo; l != nil && l.on {
		l.recordRange(m, lo, hi)
	}
	return m.elems[lo:hi]
}

// CopyFrom copies n elements of src from si to m from di, as memmove does: the
// two memories have one type, and may be one memory.
func (m *Memory) CopyFrom(di int, src *Memory, si, n int) {
	switch m.kind {
	case kindU8:
		d, _ := m.WrittenBytes(di, di+n)
		s, _ := src.Bytes(si, si+n)
		copy(d, s)
	case kindF64:
		d, _ := m.WrittenFloat64s(di, di+n)
		s, _ := src.Float64s(si, si+n)
		copy(d, s)
	default:
		copy(m.Written(di, di+n), src.Slice(si, si+n))
	}
}

// SetAll replaces the elements by es, which a boxed memory takes over and a
// typed one copies.
func (m *Memory) SetAll(es []any) {
	if l := m.undo; l != nil && l.on {
		l.recordAll(m)
	}
	switch m.kind {
	case kindU8:
		m.u8 = make([]uint8, len(es))
		for i, e := range es {
			m.u8[i], _ = e.(uint8)
		}
	case kindF64:
		m.floats = make([]float64, len(es))
		for i, e := range es {
			m.floats[i], _ = e.(float64)
		}
	default:
		m.elems = es
	}
}

// MemoryRef is a GenericMemoryRef: a memory and the 0-based index of an
// element in it (ptr_or_offset, kept as an index whatever the layout).
type MemoryRef struct {
	Type *core.DataType
	Mem  *Memory
	Off  int
}

// DefaultMaxMemoryElems is the largest memory a new VM allocates.
const DefaultMaxMemoryElems = 1 << 24

func (vm *VM) bootMemory() {
	for name, fn := range map[expr.Symbol]func(*VM, []any) (any, error){
		"memorynew":            biMemorynew,
		"memoryrefnew":         biMemoryrefnew,
		"memoryrefoffset":      biMemoryrefoffset,
		"memoryrefget":         biMemoryrefget,
		"memoryrefset!":        biMemoryrefset,
		"memoryref_isassigned": biMemoryrefIsassigned,
	} {
		t := mustType("Core", "#"+string(name), vm.builtinType, false, nil, nil)
		vm.setCore(name, &Builtin{Name: name, Type: t, Fn: fn})
	}
	// the atomic read-modify-write builtins: atomic memory is not here yet
	for _, name := range []expr.Symbol{"memoryrefswap!", "memoryrefmodify!", "memoryrefreplace!", "memoryrefsetonce!"} {
		t := mustType("Core", "#"+string(name), vm.builtinType, false, nil, nil)
		vm.setCore(name, &Builtin{Name: name, Type: t, Fn: func(*VM, []any) (any, error) {
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, name)
		}})
	}
}

// isMemoryType is jl_is_genericmemory_type.
func isMemoryType(t any) (*core.DataType, bool) {
	d, ok := t.(*core.DataType)
	return d, ok && d.Name == core.GenericMemoryType.Body.(*core.UnionAll).Body.(*core.UnionAll).Body.(*core.DataType).Name
}

// biMemorynew is jl_f_memorynew (builtins.c:1711-1719) over
// jl_alloc_genericmemory (genericmemory.c:55-102).
func biMemorynew(vm *VM, args []any) (any, error) {
	if err := vm.nargs("memorynew", args, 2, 2); err != nil {
		return nil, err
	}
	mt, ok := isMemoryType(args[0])
	if err := vm.typechk("memorynew", core.DataTypeType, args[0], ok); err != nil {
		return nil, err
	}
	n, ok := args[1].(int64)
	if err := vm.typechk("memorynew", core.Int64Type, args[1], ok); err != nil {
		return nil, err
	}
	kind, eltype := mt.Parameters[0], mt.Parameters[1]
	if kind != any(expr.Symbol("not_atomic")) && kind != any(expr.Symbol("atomic")) {
		return nil, vm.errorf("GenericMemory kind must be :not_atomic or :atomic")
	}
	if mt.Parameters[2] != any(core.CPU) {
		return nil, vm.errorf("GenericMemory addrspace must be Core.CPU")
	}
	// a memory type without a layout: one with a free element type
	// (genericmemory.c:87-88)
	if !mt.IsConcrete {
		return nil, vm.typeError("GenericMemory", "element type", core.TypeType, eltype)
	}
	if kind == any(expr.Symbol("atomic")) {
		return nil, fmt.Errorf("%w: atomic memory", ErrUnsupported)
	}
	if n == 0 {
		// the type's instance (genericmemory.c:57-58, 92-93)
		if m := vm.emptyMemory[mt]; m != nil {
			return m, nil
		}
		m := newMemory(mt, nil)
		vm.emptyMemory[mt] = m
		return m, nil
	}
	// a size that wraps size_t or overflows the byte count
	// (genericmemory.c:59-66): every element is boxed here, 8 bytes in C
	if n < 0 || n >= math.MaxInt64/8-1 {
		return nil, vm.exception(vm.tArgError, "invalid GenericMemory size: the number of elements is either negative or too large for system address width")
	}
	if n > int64(vm.MaxMemoryElems) {
		return nil, fmt.Errorf("%w: GenericMemory of %d elements (VM.MaxMemoryElems is %d)", ErrMemoryLimit, n, vm.MaxMemoryElems)
	}
	if vm.allocMax > 0 {
		if vm.allocs += n; vm.allocs > vm.allocMax {
			return nil, fmt.Errorf("%w: the cell allocated more than %d elements", ErrMemoryLimit, vm.allocMax)
		}
	}
	m := newMemoryN(mt, int(n))
	if m.kind == kindAny {
		zero := vm.zeroElem(eltype)
		for i := range m.elems {
			m.elems[i] = zero
		}
	}
	return m, nil
}

// zeroElem is what a zero-initialised slot of element type t reads: the
// zero of a primitive type, the instance of a singleton, undef for a slot
// that holds a reference.
func (vm *VM) zeroElem(t any) any {
	d, ok := t.(*core.DataType)
	if !ok || !d.IsConcrete {
		return undef
	}
	if v, ok := bitsValue(d, u128{}); ok {
		return v
	}
	if d == core.NothingType {
		return nil
	}
	if d.Name.Singleton {
		if v, err := vm.newStruct(d, nil); err == nil {
			return v
		}
	}
	return undef
}

// biMemoryrefnew is jl_f_memoryrefnew (builtins.c:1721-1760): a reference
// to a memory's first element, or one i-1 elements past a reference.
func biMemoryrefnew(vm *VM, args []any) (any, error) {
	if err := vm.nargs("memoryrefnew", args, 1, 3); err != nil {
		return nil, err
	}
	if len(args) == 1 {
		m, ok := args[0].(*Memory)
		if err := vm.typechk("memoryrefnew", core.GenericMemoryType, args[0], ok); err != nil {
			return nil, err
		}
		rt := must(core.Apply(core.GenericMemoryRefType, m.Type.Parameters...)).(*core.DataType)
		return &MemoryRef{Type: rt, Mem: m}, nil
	}
	r, ok := args[0].(*MemoryRef)
	if err := vm.typechk("memoryrefnew", core.GenericMemoryRefType, args[0], ok); err != nil {
		return nil, err
	}
	i, ok := args[1].(int64)
	if err := vm.typechk("memoryrefnew", core.Int64Type, args[1], ok); err != nil {
		return nil, err
	}
	if len(args) == 3 {
		_, ok := args[2].(bool)
		if err := vm.typechk("memoryrefnew", core.BoolType, args[2], ok); err != nil {
			return nil, err
		}
	}
	// size_t arithmetic, offset + (i - 1): an index below 1 steps back from
	// the offset, and is out of bounds only once it wraps below the memory's
	// start (builtins.c:1740-1745); _growbeg! steps back so
	if uint64(r.Off)+uint64(i-1) >= uint64(r.Mem.Len()) {
		return nil, vm.exception(vm.tBounds, r, args[1])
	}
	return &MemoryRef{Type: r.Type, Mem: r.Mem, Off: r.Off + int(i-1)}, nil
}

// biMemoryrefoffset is jl_f_memoryrefoffset (builtins.c:1762-1779).
func biMemoryrefoffset(vm *VM, args []any) (any, error) {
	if err := vm.nargs("memoryrefoffset", args, 1, 1); err != nil {
		return nil, err
	}
	r, ok := args[0].(*MemoryRef)
	// julia's check names the builtin memoryrefoffest
	if err := vm.typechk("memoryrefoffest", core.GenericMemoryRefType, args[0], ok); err != nil {
		return nil, err
	}
	return int64(r.Off + 1), nil
}

// memoryAccess is the argument and order checks the memoryref builtins
// share (builtins.c:1781-1856): ref, order and boundscheck at the given
// positions, the order being :not_atomic on non-atomic memory.
func (vm *VM) memoryAccess(name expr.Symbol, args []any, n, iorder int, what string, loading, storing bool) (*MemoryRef, error) {
	if err := vm.nargs(string(name), args, n, n); err != nil {
		return nil, err
	}
	r, ok := args[0].(*MemoryRef)
	if err := vm.typechk(name, core.GenericMemoryRefType, args[0], ok); err != nil {
		return nil, err
	}
	order, ok := args[iorder].(expr.Symbol)
	if err := vm.typechk(name, core.SymbolType, args[iorder], ok); err != nil {
		return nil, err
	}
	_, ok = args[iorder+1].(bool)
	if err := vm.typechk(name, core.BoolType, args[iorder+1], ok); err != nil {
		return nil, err
	}
	if order != "not_atomic" {
		if !validAtomicOrder(order, loading, storing) {
			return nil, vm.atomicError("invalid atomic ordering")
		}
		return nil, vm.atomicError(fmt.Sprintf("%s: non-atomic memory cannot be %s atomically", name, what))
	}
	return r, nil
}

// validAtomicOrder is jl_get_atomic_order (builtins.c:967-984) answering
// whether order is valid at all.
func validAtomicOrder(order expr.Symbol, loading, storing bool) bool {
	switch order {
	case "unordered":
		return loading != storing
	case "monotonic", "sequentially_consistent":
		return true
	case "acquire":
		return loading
	case "release":
		return storing
	case "acquire_release":
		return loading && storing
	}
	return false
}

// atomicError is jl_atomic_error (rtutils.c:181-186): a
// ConcurrencyViolationError once post_boot_hooks has found it.
func (vm *VM) atomicError(msg string) error {
	if vm.tAtomicError == nil {
		return vm.errorf("%s", msg)
	}
	return vm.exception(vm.tAtomicError, msg)
}

// biMemoryrefget is jl_f_memoryrefget (builtins.c:1781-1804) over
// jl_memoryrefget (genericmemory.c:379-423).
func biMemoryrefget(vm *VM, args []any) (any, error) {
	r, err := vm.memoryAccess("memoryrefget", args, 3, 1, "accessed", true, false)
	if err != nil {
		return nil, err
	}
	if r.Mem.Len() == 0 {
		return nil, vm.exception(vm.tBounds, r.Mem, int64(1))
	}
	v := r.Mem.Get(r.Off)
	if v == undef {
		return nil, vm.exception(vm.tUndefRef)
	}
	return v, nil
}

// biMemoryrefset is jl_f_memoryrefset (builtins.c:1806-1830) over
// jl_memoryrefset (genericmemory.c:445-).
func biMemoryrefset(vm *VM, args []any) (any, error) {
	r, err := vm.memoryAccess("memoryrefset!", args, 4, 2, "written", false, true)
	if err != nil {
		return nil, err
	}
	if r.Mem.Len() == 0 {
		return nil, vm.exception(vm.tBounds, r.Mem, int64(1))
	}
	eltype := r.Mem.Type.Parameters[1]
	if eltype != any(core.AnyType) && !vm.isa(args[1], eltype) {
		return nil, vm.typeError("memoryrefset!", "", eltype, args[1])
	}
	r.Mem.Set(r.Off, args[1])
	return args[1], nil
}

// biMemoryrefIsassigned is jl_f_memoryref_isassigned (builtins.c:1832-1856).
func biMemoryrefIsassigned(vm *VM, args []any) (any, error) {
	r, err := vm.memoryAccess("memoryref_isassigned", args, 3, 1, "accessed", true, false)
	if err != nil {
		return nil, err
	}
	if r.Mem.Len() == 0 {
		return false, nil
	}
	return r.Mem.Get(r.Off) != undef, nil
}
