package interp

import (
	"encoding/binary"
	"fmt"
	"slices"

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
	if d, ok := t.(*core.DataType); ok {
		if n, err := storedInline(d); err == nil && n > 0 {
			if size, align, err := dataLayout(d); err == nil {
				return size, align, false, false
			}
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

// storedInline is jl_stored_inline (datatype.c:374-434), Base's
// allocatedinline: whether a memory of element type t holds its elements
// inline, as the number of inline types (a Union's isbits members), 0 when
// boxed. An immutable concrete type holding references is inline unless it
// may reference itself or leaves fields uninitialized (mayinlinealloc,
// builtins.c:2254-2269; n_uninitialized): known here for tuples and named
// tuples, which can do neither, and refused for other structs, whose
// TypeName does not record either.
func storedInline(t any) (int, error) {
	switch x := t.(type) {
	case *core.Union:
		// union_isinlinable with pointerfree: every member isbits
		n := 0
		for _, m := range unionMembers(x, nil) {
			if !unionIsbits(m) {
				return 0, nil
			}
			n++
		}
		if n >= 127 {
			return 0, nil
		}
		return n, nil
	case *core.DataType:
		switch {
		case x.Name.Abstract || x.Name.Mutable:
			return 0, nil
		case x.IsBits():
			return 1, nil
		case !x.IsConcrete:
			// a layout only when fixed (jl_has_fixed_layout,
			// jltypes.c:293-319): never a tuple's
			if x.Name == core.TupleTypeName {
				return 0, nil
			}
		case x.Name == core.TupleTypeName || x.Name == core.NamedTupleTypeName:
			return 1, nil
		}
		return 0, fmt.Errorf("%w: jl_stored_inline of %s, an immutable struct holding references", ErrUnsupported, showValue(x))
	}
	return 0, nil // Union{}, a UnionAll, a TypeVar: no datatype to inline
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

// ptrArgs is the n values a C function's jl_value_t **args, n names: the
// elements of a Memory{Any} from where the pointer points (a Vector{Any}
// passed as Ptr{Any}), at least one.
func (vm *VM) ptrArgs(fname string, p, count any) ([]any, error) {
	ptr, ok1 := p.(*Pointer)
	n, ok2 := count.(int32)
	if ok1 && ok2 && n > 0 {
		if m, ok := ptr.Target.(*Memory); ok && m.Type == memAnyType && ptr.Off%8 == 0 && ptr.Off >= 0 {
			if i := ptr.Off / 8; i <= int64(len(m.Elems)) && int64(n) <= int64(len(m.Elems))-i {
				args := m.Elems[i : i+int64(n)]
				for _, x := range args {
					if x == undef {
						return nil, vm.exception(vm.tUndefRef)
					}
				}
				return args, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %s of %s", ErrUnsupported, fname, showValue(vm.newTuple([]any{p, count})))
}

// instantiateTypeInEnv is jl_instantiate_type_in_env(t, env, vals)
// (jltypes.c:2772-2784), as the Compiler calls it (abstractinterpretation.jl:
// 2927): vals a Ptr{Any} to the data of an svec (unsafe_convert(Ptr{Any},
// sv), essentials.jl:960) or of a Memory{Any}, one value per UnionAll of
// env.
func (vm *VM) instantiateTypeInEnv(a []any) (any, error) {
	n := 0
	for u, ok := a[1].(*core.UnionAll); ok; u, ok = u.Body.(*core.UnionAll) {
		n++
	}
	if n == 0 {
		// no variable to set: the type as it is, whatever vals is (C_NULL
		// among them), as jl_instantiate_type_in_env reads none
		return core.InstantiateTypeInEnv(a[0], a[1], nil), nil
	}
	p, ok := a[2].(*Pointer)
	if ok {
		switch t := p.Target.(type) {
		case SVec:
			// the svec's length word first, then its elements
			if i := (p.Off - 8) / 8; p.Off >= 8 && p.Off%8 == 0 && i+int64(n) <= int64(len(t)) {
				return core.InstantiateTypeInEnv(a[0], a[1], t[i:i+int64(n)]), nil
			}
		case *Memory:
			if vals, err := vm.ptrArgs("jl_instantiate_type_in_env", p, int32(n)); err == nil {
				return core.InstantiateTypeInEnv(a[0], a[1], vals), nil
			}
		}
	}
	return nil, fmt.Errorf("%w: jl_instantiate_type_in_env of %s", ErrUnsupported, showValue(vm.newTuple(a)))
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
	return p, vm.ptrStore("pointerset", p, a[1], i)
}

// ptrStore is the store of jl_pointerset and jl_atomic_pointerset, named
// as the intrinsic fname: x as element i (1-based) of the memory p points
// into, of p's element type.
func (vm *VM) ptrStore(fname string, p *Pointer, x any, i int64) error {
	if slot, ok := p.Target.(*ptrSlot); ok {
		// a C global variable cglobal names, of the pointer's element type
		if i != 1 || p.Off != 0 {
			return vm.errorf("%s: invalid pointer", fname)
		}
		if et := p.Type.Parameters[0]; et != any(core.AnyType) && vm.typeOf(x) != et {
			return vm.typeError(expr.Symbol(fname), "", et, x)
		}
		slot.val = x
		return nil
	}
	if s, ok := p.Target.(string); ok {
		// a byte of a String allocString made (strbuf.go), as _string_n's
		// callers fill it (substring.jl:199)
		b, writable := vm.writableBytes(s)
		at := p.Off + i - 1
		if !writable || p.Type.Parameters[0] != any(core.UInt8Type) || at < 0 || at >= int64(len(b)) {
			return vm.errorf("%s: invalid pointer", fname)
		}
		c, isByte := x.(uint8)
		if !isByte {
			return vm.typeError(expr.Symbol(fname), "", core.UInt8Type, x)
		}
		b[at] = c
		return nil
	}
	if st, ok := p.Target.(*Struct); ok {
		if !vm.firstFieldAt(p, st, i) {
			return vm.errorf("%s: invalid pointer", fname)
		}
		if vm.typeOf(x) != p.Type.Parameters[0] {
			return vm.typeError(expr.Symbol(fname), "", p.Type.Parameters[0], x)
		}
		st.Fields[0] = x
		return nil
	}
	m, ok := p.Target.(*Memory)
	if !ok {
		return vm.errorf("%s: invalid pointer", fname)
	}
	ety := p.Type.Parameters[0]
	if at, ok := reinterpreted(p, m); ok {
		if vm.typeOf(x) != any(at) {
			return vm.typeError(expr.Symbol(fname), "", at, x)
		}
		return vm.memStoreBytes(fname, m, p.Off+(i-1)*int64(at.Name.Bits/8), x)
	}
	if k, ok := vm.unsetThrough(p, m, x, i); ok {
		m.Elems[k] = undef
		return nil
	}
	if ety != any(core.AnyType) && vm.typeOf(x) != ety {
		return vm.typeError(expr.Symbol(fname), "", ety, x)
	}
	k, err := vm.memIndex(fname, p, m, ety, i)
	if err != nil {
		return err
	}
	m.Elems[k] = x
	return nil
}

// firstFieldAt is whether p, element i, addresses the first field of the
// mutable struct st, pointer_from_objref(st) (pointer.jl:299-303): the
// field a C function writes through a Ref (unsafe_convert of a RefValue,
// refvalue.jl:38-54), of the pointer's element type, at offset 0. Other
// bytes of a struct have no address here.
func (vm *VM) firstFieldAt(p *Pointer, st *Struct, i int64) bool {
	if i != 1 || p.Off != 0 || !st.Type.Name.Mutable || len(st.Fields) == 0 {
		return false
	}
	ft, _ := st.Type.FieldTypes()
	return ft[0] == p.Type.Parameters[0]
}

// reinterpreted is the access type of a pointer that reads a Memory of
// primitive values as another primitive type (a UInt8 view of UInt32s, as
// io.jl's byte-order test makes): both primitive, of different types.
func reinterpreted(p *Pointer, m *Memory) (*core.DataType, bool) {
	at, ok1 := p.Type.Parameters[0].(*core.DataType)
	et, ok2 := m.Type.Parameters[1].(*core.DataType)
	if !ok1 || !ok2 || at == et || at.Name.Bits == 0 || et.Name.Bits == 0 || at.Name.Bits%8 != 0 || et.Name.Bits%8 != 0 {
		return nil, false
	}
	return at, true
}

// memByteRange checks n bytes at byte offset off of a primitive memory and
// returns its element size.
func (vm *VM) memByteRange(fname string, m *Memory, off int64, n int) (int, error) {
	es := m.Type.Parameters[1].(*core.DataType).Name.Bits / 8
	if off < 0 || off+int64(n) > int64(es)*int64(len(m.Elems)) {
		return 0, vm.errorf("%s: invalid pointer", fname)
	}
	return es, nil
}

// elemBytes and setElemBytes are element k's little-endian bytes, as
// julia's memory holds them.
func (vm *VM) elemBytes(m *Memory, k, es int) ([]byte, error) {
	if m.Elems[k] == undef {
		return make([]byte, es), nil // uninitialized bits
	}
	bits, _, ok := primBits(m.Elems[k])
	if !ok {
		return nil, fmt.Errorf("%w: the bits of a %s", ErrUnsupported, showValue(vm.typeOf(m.Elems[k])))
	}
	b := binary.LittleEndian.AppendUint64(nil, bits.lo)
	return binary.LittleEndian.AppendUint64(b, bits.hi)[:es], nil
}

func bytesValue(t *core.DataType, b []byte) (any, bool) {
	var buf [16]byte
	copy(buf[:], b)
	return bitsValue(t, u128{binary.LittleEndian.Uint64(buf[8:]), binary.LittleEndian.Uint64(buf[:8])})
}

// memLoadBytes is a load of type at from byte offset off of a primitive
// memory.
func (vm *VM) memLoadBytes(fname string, m *Memory, off int64, at *core.DataType) (any, error) {
	n := at.Name.Bits / 8
	es, err := vm.memByteRange(fname, m, off, n)
	if err != nil {
		return nil, err
	}
	var b []byte
	for k := int(off) / es; len(b) < int(off)%es+n; k++ {
		eb, err := vm.elemBytes(m, k, es)
		if err != nil {
			return nil, err
		}
		b = append(b, eb...)
	}
	v, ok := bytesValue(at, b[int(off)%es:int(off)%es+n])
	if !ok {
		return nil, fmt.Errorf("%w: %s as %s", ErrUnsupported, fname, showValue(at))
	}
	return v, nil
}

// memStoreBytes is a store of x at byte offset off of a primitive memory,
// the elements it overlaps rewritten.
func (vm *VM) memStoreBytes(fname string, m *Memory, off int64, x any) error {
	bits, w, ok := primBits(x)
	if !ok {
		return fmt.Errorf("%w: %s of a %s", ErrUnsupported, fname, showValue(vm.typeOf(x)))
	}
	n := int(w / 8)
	es, err := vm.memByteRange(fname, m, off, n)
	if err != nil {
		return err
	}
	src := binary.LittleEndian.AppendUint64(nil, bits.lo)
	src = binary.LittleEndian.AppendUint64(src, bits.hi)[:n]
	et := m.Type.Parameters[1].(*core.DataType)
	for k := int(off) / es; k*es < int(off)+n; k++ {
		eb, err := vm.elemBytes(m, k, es)
		if err != nil {
			return err
		}
		for j := range eb {
			if at := k*es + j; at >= int(off) && at < int(off)+n {
				eb[j] = src[at-int(off)]
			}
		}
		v, ok := bytesValue(et, eb)
		if !ok {
			return fmt.Errorf("%w: %s into a Memory{%s}", ErrUnsupported, fname, showValue(et))
		}
		m.Elems[k] = v
	}
	return nil
}

// unsetThrough is _unsetindex! (genericmemory.jl:78-101): C_NULL stored
// through a Ptr{Ptr{Cvoid}} over a boxed slot, or over any word of an
// inline element with pointers, leaves the element #undef. It returns the
// element, and whether the store is one.
func (vm *VM) unsetThrough(p *Pointer, m *Memory, x any, i int64) (int, bool) {
	np, ok := x.(*Pointer)
	eltype := m.Type.Parameters[1]
	voidp := any(vm.ptrType(core.NothingType))
	if !ok || np.Target != nil || np.Off != 0 || p.Type.Parameters[0] != voidp || eltype == voidp {
		return 0, false
	}
	size, _, boxed, union := memElem(eltype)
	if union || size == 0 {
		return 0, false
	}
	if !boxed {
		// memElem stores inline only DataTypes and isbits unions
		l, err := structLayout(eltype.(*core.DataType))
		if err != nil || !slices.Contains(l.pointers, true) {
			return 0, false
		}
	}
	off := p.Off + (i-1)*8
	if off < 0 || off/int64(size) >= int64(len(m.Elems)) {
		return 0, false
	}
	return int(off / int64(size)), true
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
