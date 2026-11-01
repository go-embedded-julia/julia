package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// dataLayout is the size and alignment julia gives an instance of t in
// memory, as jl_compute_field_offsets (datatype.c) lays a struct out on the
// 64-bit platforms julia builds for: a primitive type by its bits, aligned
// to its size up to 16; a field of a non-isbits type as a pointer; each
// field at the next multiple of its alignment; the whole rounded up to the
// largest alignment. An isbits Union field (inline with a selector byte) is
// not laid out here.
func dataLayout(t *core.DataType) (size, align int, err error) {
	if t.Name.Bits > 0 {
		size = t.Name.Bits / 8
		return size, min(size, 16), nil
	}
	if t.Name.Abstract {
		return 0, 0, fmt.Errorf("%w: the layout of the abstract type %s", ErrUnsupported, t.Name.Name)
	}
	ft, ok := t.FieldTypes()
	if !ok {
		return 0, 0, fmt.Errorf("%w: the layout of %s", ErrUnsupported, t.Name.Name)
	}
	align = 1
	for _, f := range ft {
		fs, fa := 8, 8 // a pointer to a boxed value
		if d, ok := f.(*core.DataType); ok && d.Name.Isbits && d.IsConcrete {
			var err error
			if fs, fa, err = dataLayout(d); err != nil {
				return 0, 0, err
			}
		} else if _, isUnion := f.(*core.Union); isUnion && unionIsbits(f) {
			return 0, 0, fmt.Errorf("%w: the layout of an isbits Union field of %s", ErrUnsupported, t.Name.Name)
		}
		if fa > 0 {
			size = (size + fa - 1) / fa * fa
		}
		size += fs
		align = max(align, fa)
	}
	return (size + align - 1) / align * align, align, nil
}

// unionIsbits reports that every member of a Union is an isbits type, the
// case julia stores inline.
func unionIsbits(u any) bool {
	if x, ok := u.(*core.Union); ok {
		return unionIsbits(x.A) && unionIsbits(x.B)
	}
	d, ok := u.(*core.DataType)
	return ok && d.Name.Isbits && d.IsConcrete
}

// defaultOptions is jl_options as jl_init_options sets it
// (jloptions.c:87-160), as an instance of Base.JLOptions, which mirrors the
// C struct field for field: the options a julia started without flags has.
func (vm *VM) defaultOptions(t *core.DataType) (any, error) {
	ft, ok := t.FieldTypes()
	if !ok {
		return nil, fmt.Errorf("%w: jl_options as %s", ErrUnsupported, showValue(t))
	}
	set := map[expr.Symbol]int64{
		"banner": -1, "historyfile": 1, "compile_enabled": 1, "opt_level": 2, "debug_level": 1,
		"can_inline": 1, "polly": 1, "handle_signals": 1, "use_sysimage_native_code": 1,
		"use_compiled_modules": 1, "use_pkgimages": 1, "warn_scope": 1, "timeout_for_safepoint_straggler_s": -1,
	}
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
