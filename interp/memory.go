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

// Memory is a GenericMemory{kind, T, addrspace} instance.
type Memory struct {
	Type  *core.DataType
	Elems []any
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
		m := &Memory{Type: mt}
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
	m := &Memory{Type: mt, Elems: make([]any, n)}
	zero := vm.zeroElem(eltype)
	for i := range m.Elems {
		m.Elems[i] = zero
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
	if v, ok := bitsValue(d, 0); ok {
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
	// size_t arithmetic: an index below 1 wraps to a huge one
	if i < 1 || uint64(r.Off)+uint64(i-1) >= uint64(len(r.Mem.Elems)) {
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
	if len(r.Mem.Elems) == 0 {
		return nil, vm.exception(vm.tBounds, r.Mem, int64(1))
	}
	v := r.Mem.Elems[r.Off]
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
	if len(r.Mem.Elems) == 0 {
		return nil, vm.exception(vm.tBounds, r.Mem, int64(1))
	}
	eltype := r.Mem.Type.Parameters[1]
	if eltype != any(core.AnyType) && !vm.isa(args[1], eltype) {
		return nil, vm.typeError("memoryrefset!", "", eltype, args[1])
	}
	r.Mem.Elems[r.Off] = args[1]
	return args[1], nil
}

// biMemoryrefIsassigned is jl_f_memoryref_isassigned (builtins.c:1832-1856).
func biMemoryrefIsassigned(vm *VM, args []any) (any, error) {
	r, err := vm.memoryAccess("memoryref_isassigned", args, 3, 1, "accessed", true, false)
	if err != nil {
		return nil, err
	}
	if len(r.Mem.Elems) == 0 {
		return false, nil
	}
	return r.Mem.Elems[r.Off] != undef, nil
}
