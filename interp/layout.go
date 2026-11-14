package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// dataLayout is the size and alignment julia gives an instance of t in
// memory, as jl_compute_field_offsets (datatype.c:629-856) lays a struct out
// on the 64-bit platforms julia builds for.
func dataLayout(t *core.DataType) (size, align int, err error) {
	l, err := structLayout(t)
	return l.size, l.align, err
}

// layout is what jl_compute_field_offsets records of a type: its size and
// alignment, each field's offset and whether it is a pointer, where every
// pointer lies, whether fields leave undefined bytes between them, and
// whether its egality is that of its bits (julia.h:571-592).
type layout struct {
	size, align int
	offsets     []int
	sizes       []int
	// pointers is per field: stored as a pointer, not inline
	pointers []bool
	// ptrs are the word offsets of every pointer slot, those of inline fields
	// included (jl_ptr_offset), in field order
	ptrs       []int
	haspadding bool
	isbitsegal bool
	zeroinit   bool
}

const (
	maxAlign       = 16 // MAX_ALIGN, julia_internal.h:111-125 (64-bit)
	maxAtomicSize  = 16 // MAX_ATOMIC_SIZE, julia.h:41
	mutexSize      = 16 // sizeof(jl_mutex_t), julia_threads.h:122-125
	maxInlineKinds = 127
)

// nextPow2 is next_power_of_two (support/dtypes.h:216-227), 0 for 0.
func nextPow2(v uint32) uint32 {
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	return v + 1
}

// alignUp is LLT_ALIGN (support/dtypes.h:229) for a power of two.
func alignUp(x, a int) int { return (x + a - 1) &^ (a - 1) }

func structLayout(t *core.DataType) (layout, error) {
	if t.Name.Bits > 0 {
		// jl_new_primitivetype (datatype.c:972-995)
		size := (t.Name.Bits + 7) / 8
		return layout{size: size, align: min(int(nextPow2(uint32(size))), maxAlign), isbitsegal: true}, nil
	}
	if t.Name.Abstract {
		return layout{}, fmt.Errorf("%w: the layout of the abstract type %s", ErrUnsupported, t.Name.Name)
	}
	ft, ok := t.FieldTypes()
	if !ok {
		return layout{}, fmt.Errorf("%w: the layout of %s", ErrUnsupported, showValue(t))
	}
	if len(ft) == 0 {
		// the opaque layouts and the singleton one (datatype.c:665-682)
		switch t {
		case core.SimpleVectorType, core.ModuleType:
			return layout{align: 8, isbitsegal: true}, nil
		}
		return layout{align: 1, isbitsegal: true}, nil
	}
	l := layout{align: 1, isbitsegal: true}
	sub := make([]layout, len(ft)) // the layouts of the inline fields of a datatype
	needlock, homogeneous := false, true
	sz := 0
	for i, fld := range ft {
		atomic := fieldMarked(t.Name.AtomicFields, i)
		fsz, al := 0, 1
		n, acc, err := islayoutInline(fld)
		if err != nil {
			return layout{}, err
		}
		_, isDT := fld.(*core.DataType)
		if n > 0 && (!atomic || isDT) { // datatype.c:729
			l.pointers = append(l.pointers, false)
			fsz, al = acc.size, acc.align
			if _, isUnion := fld.(*core.Union); isUnion {
				fsz++ // selector byte
				l.zeroinit = true
				l.isbitsegal = false // some unions could be bits comparable
			} else {
				fl := acc.dt
				sub[i] = fl
				if fsz > fl.size {
					// padded to the integer size class
					l.isbitsegal, l.haspadding = false, true
				}
				l.haspadding = l.haspadding || fl.haspadding
				l.isbitsegal = l.isbitsegal && fl.isbitsegal
				if i >= len(ft)-int(t.Name.NUninitialized) && len(fl.ptrs) > 0 && len(fl.ptrs)*8 != fsz {
					// the first pointer is the #undef marker of the inline
					// struct, whose other bytes may be arbitrary
					// (datatype.c:752-765)
					l.isbitsegal = false
				}
				l.zeroinit = l.zeroinit || fl.zeroinit
			}
		} else {
			fsz, al = 8, min(8, maxAlign)
			l.pointers = append(l.pointers, true)
			l.zeroinit = true
			if !pointerEgal(fld) {
				l.isbitsegal = false
			}
		}
		if atomic && fsz > maxAtomicSize {
			needlock = true
		}
		if atomic && fsz <= maxAtomicSize {
			al = int(nextPow2(uint32(fsz)))
			fsz = al
		}
		if al != 0 {
			alsz := alignUp(sz, al)
			if alsz != sz {
				l.haspadding = true
			}
			sz = alsz
			l.align = max(l.align, al)
		}
		homogeneous = homogeneous && ft[0] == fld
		l.offsets = append(l.offsets, sz)
		l.sizes = append(l.sizes, fsz)
		sz += fsz
	}
	if needlock {
		off := alignUp(mutexSize, l.align)
		for i := range l.offsets {
			l.offsets[i] += off
		}
		sz += off
		l.haspadding = true
	}
	if homogeneous && t.Name == core.TupleTypeName {
		// some tuples become LLVM vectors (datatype.c:812-818)
		l.align = max(l.align, specialVectorAlignment(len(ft), ft[0]))
	}
	if alignUp(sz, l.align) > sz {
		l.haspadding = true
		sz = alignUp(sz, l.align)
	}
	l.size = sz
	for i, off := range l.offsets {
		if l.pointers[i] {
			l.ptrs = append(l.ptrs, off/8)
			continue
		}
		for _, p := range sub[i].ptrs {
			l.ptrs = append(l.ptrs, off/8+p)
		}
	}
	return l, nil
}

// specialVectorAlignment is jl_special_vector_alignment (datatype.c:322-344):
// the alignment of a homogeneous tuple of VecElements, 0 for any other.
func specialVectorAlignment(nfields int, t any) int {
	d, ok := t.(*core.DataType)
	if !ok || d.Name.Name != "VecElement" || d.Name.Module != "Core" {
		return 0
	}
	ft, _ := d.FieldTypes()
	ty, ok := ft[0].(*core.DataType)
	if !ok || ty.Name.Bits == 0 {
		return 0 // LLVM requires a primitive type element
	}
	elsz := (ty.Name.Bits + 7) / 8
	if int(nextPow2(uint32(elsz))) != elsz {
		return 0
	}
	return int(nextPow2(uint32(nfields * elsz)))
}

// fielddescType is the smallest field descriptor that holds l's sizes and
// offsets (datatype.c:200-223): 0 for jl_fielddesc8_t (a 7-bit size, an
// 8-bit offset), 1 for 16 (15 and 16 bits), 2 for 32 (julia.h:553-572).
func fielddescType(l layout) int {
	maxSize, maxOffset := 0, 0
	if n := len(l.offsets); n > 0 {
		maxOffset = l.offsets[n-1]
	}
	for _, s := range l.sizes {
		maxSize = max(maxSize, s)
	}
	if n := len(l.ptrs); n > 0 && l.ptrs[n-1] > maxOffset {
		maxOffset = l.ptrs[n-1]
	}
	switch {
	case maxSize < 1<<7 && maxOffset < 1<<8:
		return 0
	case maxSize < 1<<15 && maxOffset < 1<<16:
		return 1
	}
	return 2
}

// islayoutInline is jl_islayout_inline (datatype.c:424-428): how many types
// a field or element of type t stores inline (a Union's members), 0 when it
// is a pointer, with the size and alignment of the largest.
func islayoutInline(t any) (n int, acc inlineAcc, err error) {
	n, err = unionInlinable(t, false, &acc, true)
	if n >= maxInlineKinds {
		n = 0
	}
	return n, acc, err
}

// inlineAcc is what union_isinlinable accumulates: the size and alignment of
// the largest inline type, and the layout of the last one (the type itself
// when t is not a Union).
type inlineAcc struct {
	size, align int
	dt          layout
}

// unionInlinable is union_isinlinable (datatype.c:392-416): the number of
// inline types of t, raising acc to the largest. Members of a Union must be
// free of pointers.
func unionInlinable(t any, pointerfree bool, acc *inlineAcc, asfield bool) (int, error) {
	switch x := t.(type) {
	case *core.Union:
		na, err := unionInlinable(x.A, true, acc, asfield)
		if na == 0 || err != nil {
			return 0, err
		}
		nb, err := unionInlinable(x.B, true, acc, asfield)
		if nb == 0 || err != nil {
			return 0, err
		}
		return na + nb, nil
	case *core.DataType:
		l, ok, err := datatypeIsInlineAlloc(x, pointerfree)
		if !ok || err != nil {
			return 0, err
		}
		sz := l.size
		// primitive types in struct slots need their sizes aligned (#37974)
		if asfield && x.Name.Bits > 0 {
			sz = alignUp(sz, l.align)
		}
		acc.size, acc.align, acc.dt = max(acc.size, sz), max(acc.align, l.align), l
		return 1, nil
	}
	return 0, nil
}

// inlineType is the type whose layout an inline field of type t has:
// Type{Union{}} is laid out as TypeofBottom (datatype.c:376-377).
func inlineType(t *core.DataType) *core.DataType {
	if t == core.TypeofBottomType.Super {
		return core.TypeofBottomType
	}
	return t
}

// datatypeIsInlineAlloc is jl_datatype_isinlinealloc (datatype.c:374-390):
// whether instances of ty are stored inline, which they are when the type
// may be (mayinlinealloc) and has a layout, and, when that layout holds
// pointers, they may be left undefined by `new` nowhere, the type is not
// wanted pointer-free, and the GC can walk the layout (descriptors of at
// most 16 bits).
func datatypeIsInlineAlloc(ty *core.DataType, pointerfree bool) (layout, bool, error) {
	ty = inlineType(ty)
	if !ty.Name.MayInlineAlloc || !core.HasFixedLayout(ty) {
		return layout{}, false, nil
	}
	l, err := structLayout(ty)
	if err != nil {
		return l, false, err
	}
	if len(l.ptrs) > 0 {
		return l, !pointerfree && ty.Name.NUninitialized == 0 && fielddescType(l) <= 1, nil
	}
	return l, true, nil
}

// pointerEgal is jl_pointer_egal (datatype.c:437-473): whether instances of
// t compare by pointer under `===`.
func pointerEgal(t any) bool {
	switch {
	case t == any(core.AnyType):
		return false
	case t == any(core.SymbolType), t == any(core.BoolType):
		return true
	}
	if d, ok := core.UnwrapUnionAll(t).(*core.DataType); ok && d.Name.Mutable &&
		d != core.StringType && d != core.SimpleVectorType && !isKind(t) {
		return true
	}
	d, isDT := t.(*core.DataType)
	if isDT && (d.IsSingleton() || d == core.TypeofBottomType.Super) {
		return true
	}
	if isDT && d.Name == core.TypeTypeName {
		// Type{T} of a datatype: concrete types have unique pointers, and so
		// does a type with no parameters
		if dt, ok := d.Parameters[0].(*core.DataType); ok && dt != core.TypeofBottomType &&
			(dt.IsConcrete || len(dt.Parameters) == 0) {
			return true
		}
	}
	if u, ok := t.(*core.Union); ok {
		return pointerEgal(u.A) && pointerEgal(u.B)
	}
	return false
}

// isKind is jl_is_kind (julia.h:1708): DataType, UnionAll, Union or
// TypeofBottom.
func isKind(t any) bool {
	return t == any(core.DataTypeType) || t == any(core.UnionAllType) || t == any(core.UnionType) ||
		t == any(core.TypeofBottomType)
}

// tryLayout is jl_struct_try_layout (datatype.c:362-372): whether d has a
// layout, which a memory type has whatever its parameters (its wrapper's is
// the struct layout of its two fields, made before the type was named).
func tryLayout(d *core.DataType) bool {
	return d.Name == typeNameOf(core.GenericMemoryType) || core.HasFixedLayout(d)
}

// fieldOffset is jl_get_field_offset (datatype.c:2313-2318), fieldoffset:
// the byte offset of field i (from 1) of a type with a layout
// (jl_struct_try_layout, datatype.c:362-372), a BoundsError otherwise.
func (vm *VM) fieldOffset(a []any) (any, error) {
	d, ok := a[0].(*core.DataType)
	if !ok {
		return nil, vm.typeError("ccall", "", core.DataTypeType, a[0])
	}
	i, ok := a[1].(int32)
	if !ok {
		return nil, vm.typeError("ccall", "", core.Int32Type, a[1])
	}
	ft, hasFields := d.FieldTypes()
	if !hasFields || !tryLayout(d) || i < 1 || int(i) > len(ft) {
		return nil, vm.exception(vm.tBounds, d, int64(i))
	}
	l, err := structLayout(d)
	if err != nil {
		return nil, err
	}
	return uint64(l.offsets[i-1]), nil
}

// jlOptionDefaults are the jl_options fields jl_init_options sets to other
// than 0 (jloptions.c:87-160): a julia started without flags.
var jlOptionDefaults = map[expr.Symbol]int64{
	"banner": -1, "historyfile": 1, "compile_enabled": 1, "opt_level": 2, "debug_level": 1,
	"can_inline": 1, "polly": 1, "handle_signals": 1, "use_sysimage_native_code": 1,
	"use_compiled_modules": 1, "use_pkgimages": 1, "warn_scope": 1, "timeout_for_safepoint_straggler_s": -1,
}

// cacheFlags is jl_cache_flags (staticdata_utils.c:430-443) of those
// options: OOICCDDP, julia 1.12.6 answers 163 started without flags.
func cacheFlags() uint8 {
	o := jlOptionDefaults
	return uint8(o["use_pkgimages"]&1 | (o["debug_level"]&3)<<1 | (o["check_bounds"]&3)<<3 |
		(o["can_inline"]&1)<<5 | (o["opt_level"]&3)<<6)
}

// defaultOptions is jl_options as jl_init_options sets it
// (jloptions.c:87-160), as an instance of Base.JLOptions, which mirrors the
// C struct field for field: the options a julia started without flags has.
func (vm *VM) defaultOptions(t *core.DataType) (any, error) {
	ft, ok := t.FieldTypes()
	if !ok {
		return nil, fmt.Errorf("%w: jl_options as %s", ErrUnsupported, showValue(t))
	}
	set := jlOptionDefaults
	fields := make([]any, len(ft))
	for i, f := range ft {
		d, _ := f.(*core.DataType)
		if d != nil && d.Name == core.PtrType.Body.(*core.DataType).Name {
			fields[i] = &Pointer{Type: d} // NULL
			continue
		}
		v, ok := bitsValue(d, from64(uint64(set[t.Name.FieldNames[i]])).sext(64))
		if !ok {
			return nil, fmt.Errorf("%w: the jl_options field %s", ErrUnsupported, t.Name.FieldNames[i])
		}
		fields[i] = v
	}
	return vm.newStruct(t, fields)
}
