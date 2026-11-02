package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Pointers into a Memory and to a type's layout (vptr.go has the model: a
// pointer names what it points into and a byte offset). Base reaches a
// memory's elements through Ptr values when it hands an Array to C
// (pointer.jl:63-88), and reads an element's size and kind through
// DataType.layout (runtime_internals.jl:523-688).

// layoutOf is what DataType.layout points at: the type's layout.
type layoutOf struct{ t *core.DataType }

// memElem is how a memory of element type t stores an element
// (jl_islayout_inline, datatype.c:511-600): its size and alignment inline,
// or a boxed pointer, or an inline isbits Union (with a selector byte).
func memElem(t any) (size, align int, boxed, union bool) {
	if d, ok := t.(*core.DataType); ok && d.IsBits() {
		size, align, err := dataLayout(d)
		if err == nil {
			return size, align, false, false
		}
	}
	if _, ok := t.(*core.Union); ok && unionIsbits(t) {
		for _, m := range unionMembers(t, nil) {
			s, a, _ := dataLayout(m.(*core.DataType))
			size, align = max(size, s), max(align, a)
		}
		return size, max(align, 1), false, true
	}
	return 8, 8, true, false
}

// memPtr is getfield(m, :ptr): a pointer to the first element.
func (vm *VM) memPtr(m *Memory) *Pointer {
	return &Pointer{Type: vm.ptrType(core.NothingType), Target: m}
}

// refPtrOrOffset is getfield(r, :ptr_or_offset) (genericmemory.c): the
// address of the element, or its index when the elements are an isbits
// Union or of size 0, which julia stores in the same field.
func (vm *VM) refPtrOrOffset(r *MemoryRef) *Pointer {
	size, _, _, union := memElem(r.Mem.Type.Parameters[1])
	if union || size == 0 {
		return &Pointer{Type: vm.ptrType(core.NothingType), Off: int64(r.Off)}
	}
	return &Pointer{Type: vm.ptrType(core.NothingType), Target: r.Mem, Off: int64(r.Off * size)}
}

// memoryCopyto is jl_genericmemory_copyto(dest, destdata, src, srcdata, n)
// (genericmemory.c:225-300): n elements moved, overlap allowed, between
// memories of one type, each pointer one refPtrOrOffset made. What C leaves
// undefined (a pointer outside its memory, a range past its end) is
// refused.
func (vm *VM) memoryCopyto(a []any) error {
	dest, ok1 := a[0].(*Memory)
	src, ok2 := a[2].(*Memory)
	dp, ok3 := a[1].(*Pointer)
	sp, ok4 := a[3].(*Pointer)
	n, ok5 := a[4].(int64)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return fmt.Errorf("%w: jl_genericmemory_copyto of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	if dest.Type != src.Type {
		return vm.exception(vm.tArgError, "jl_genericmemory_copyto requires source and dest to have same type")
	}
	di, ok1 := memIndexOf(dest, dp)
	si, ok2 := memIndexOf(src, sp)
	if !ok1 || !ok2 || n < 0 || di+n > int64(len(dest.Elems)) || si+n > int64(len(src.Elems)) {
		return fmt.Errorf("%w: jl_genericmemory_copyto of %d elements outside its memories (undefined in C)", ErrUnsupported, n)
	}
	copy(dest.Elems[di:di+n], src.Elems[si:si+n])
	return nil
}

// memIndexOf is the element index of a pointer refPtrOrOffset made into m.
func memIndexOf(m *Memory, p *Pointer) (int64, bool) {
	size, _, _, union := memElem(m.Type.Parameters[1])
	if union || size == 0 {
		return p.Off, p.Target == nil && p.Off >= 0
	}
	return p.Off / int64(size), p.Target == any(m) && p.Off >= 0 && p.Off%int64(size) == 0
}

// typeLayoutPtr is getfield(d, :layout): C_NULL for a type without a
// layout (abstract, or with free parameters), else a pointer to it.
func (vm *VM) typeLayoutPtr(d *core.DataType) *Pointer {
	p := &Pointer{Type: vm.ptrType(core.NothingType)}
	if d.IsConcrete {
		p.Target = layoutOf{d}
	}
	return p
}

// layoutStruct is *(jl_datatype_layout_t*)d->layout as a value of Base's
// DataTypeLayout (st): size, nfields, npointers, firstptr, alignment, flags
// (julia.h:571-592). A memory type's is its element's (datatype.c:511-
// 600); any other's its struct layout.
func (vm *VM) layoutStruct(d, st *core.DataType) (any, error) {
	var size, align, nfields, npointers int
	firstptr := int32(-1)
	var flags uint16
	if d.Name == memoryName(core.GenericMemoryType) {
		// haspadding, fielddesc_type 2, then arrayelem's isboxed and
		// isunion (datatype.c:533-620)
		elsz, al, boxed, union := memElem(d.Parameters[1])
		size, align, npointers = elsz, al, 1
		flags = 1 | 2<<1
		if boxed {
			flags |= 1 << 3
		}
		if union {
			flags |= 1 << 4
		}
	} else {
		l, err := structLayout(d)
		if err != nil {
			return nil, err
		}
		size, align, nfields = l.size, l.align, len(l.offsets)
		for i, isptr := range l.pointers {
			if isptr {
				if firstptr < 0 {
					firstptr = int32(l.offsets[i] / 8) // in pointer-sized words
				}
				npointers++
			}
		}
		if l.haspadding {
			flags |= 1
		}
		if l.isbitsegal {
			flags |= 1 << 7
		}
	}
	return vm.newStruct(st, []any{uint32(size), uint32(nfields), uint32(npointers), firstptr, uint16(align), flags})
}

// memIndex is the element a pointer into a memory addresses, for an access
// of type t: the offset must fall on an element of exactly that type.
func (vm *VM) memIndex(name string, p *Pointer, m *Memory, t any, i int64) (int, error) {
	eltype := m.Type.Parameters[1]
	size, _, boxed, union := memElem(eltype)
	if union || size == 0 {
		return 0, fmt.Errorf("%w: %s into a Memory{%s}", ErrUnsupported, name, showValue(eltype))
	}
	if !boxed && t != eltype {
		return 0, fmt.Errorf("%w: %s of a %s pointer into a Memory{%s}", ErrUnsupported, name, showValue(t), showValue(eltype))
	}
	off := p.Off + (i-1)*int64(size)
	if off < 0 || off%int64(size) != 0 || off/int64(size) >= int64(len(m.Elems)) {
		return 0, vm.errorf("%s: invalid pointer", name)
	}
	return int(off / int64(size)), nil
}

// biPointerset is jl_pointerset (runtime_intrinsics.c:432-455) for a
// pointer into a memory.
func biPointerset(vm *VM, a []any) (any, error) {
	p, ok := a[0].(*Pointer)
	if err := vm.typechk("pointerset", core.PtrType, a[0], ok); err != nil {
		return nil, err
	}
	i, ok := a[2].(int64)
	if err := vm.typechk("pointerset", core.Int64Type, a[2], ok); err != nil {
		return nil, err
	}
	if _, ok := a[3].(int64); !ok {
		return nil, vm.typeError("pointerset", "", core.Int64Type, a[3])
	}
	m, ok := p.Target.(*Memory)
	if !ok {
		return nil, vm.errorf("pointerset: invalid pointer")
	}
	ety := p.Type.Parameters[0]
	if ety != any(core.AnyType) && vm.typeOf(a[1]) != ety {
		return nil, vm.typeError("pointerset", "", ety, a[1])
	}
	k, err := vm.memIndex("pointerset", p, m, ety, i)
	if err != nil {
		return nil, err
	}
	m.Elems[k] = a[1]
	return p, nil
}

// ptrArith is add_ptr and sub_ptr (runtime_intrinsics.c:1742-1758): a
// pointer moved by a UInt number of bytes.
func ptrArith(name expr.Symbol, sign int64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		p, ok := a[0].(*Pointer)
		if err := vm.typechk(name, core.PtrType, a[0], ok); err != nil {
			return nil, err
		}
		n, ok := a[1].(uint64)
		if err := vm.typechk(name, core.UInt64Type, a[1], ok); err != nil {
			return nil, err
		}
		return &Pointer{Type: p.Type, Target: p.Target, Off: p.Off + sign*int64(n)}, nil
	}
}

// fenvConsts is jl_get_fenv_consts (jl_uv.c): the floating-point
// environment's flags and rounding modes, as julia reports them on arm64
// (Base.Rounding.JL_FE_*), written to nine Int32s.
func (vm *VM) fenvConsts(a []any) (any, error) {
	p, ok := a[0].(*Pointer)
	if !ok {
		return nil, vm.typeError("ccall", "", core.PtrType, a[0])
	}
	m, ok := p.Target.(*Memory)
	if !ok {
		return nil, vm.errorf("jl_get_fenv_consts: invalid pointer")
	}
	for i, v := range []int32{0x10, 0x08, 0x04, 0x02, 0x01, 0, 0x400000, 0x800000, 0xc00000} {
		k, err := vm.memIndex("jl_get_fenv_consts", p, m, core.Int32Type, int64(i+1))
		if err != nil {
			return nil, err
		}
		m.Elems[k] = v
	}
	return nil, nil
}
