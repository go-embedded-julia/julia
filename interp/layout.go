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
	l, err := structLayout(t)
	return l.size, l.align, err
}

// layout is what jl_compute_field_offsets records of a type: its size and
// alignment, each field's offset and whether it is a pointer, whether
// fields leave undefined bytes between them, and whether its egality is
// that of its bits (julia.h:571-592).
type layout struct {
	size, align int
	offsets     []int
	sizes       []int
	pointers    []bool
	haspadding  bool
	isbitsegal  bool
}

func structLayout(t *core.DataType) (layout, error) {
	if t.Name.Bits > 0 {
		size := t.Name.Bits / 8
		return layout{size: size, align: min(size, 16), isbitsegal: true}, nil
	}
	if t.Name.Abstract {
		return layout{}, fmt.Errorf("%w: the layout of the abstract type %s", ErrUnsupported, t.Name.Name)
	}
	ft, ok := t.FieldTypes()
	if !ok {
		return layout{}, fmt.Errorf("%w: the layout of %s", ErrUnsupported, t.Name.Name)
	}
	l := layout{align: 1, isbitsegal: true}
	used := 0
	for _, f := range ft {
		fs, fa := 8, 8 // a pointer to a boxed value
		isptr := true
		// an isbits field is inline, a parametric one too (a NamedTuple's)
		if d, ok := f.(*core.DataType); ok && d.IsBits() {
			fl, err := structLayout(d)
			if err != nil {
				return layout{}, err
			}
			fs, fa, isptr = fl.size, fl.align, false
			l.isbitsegal = l.isbitsegal && fl.isbitsegal
			l.haspadding = l.haspadding || fl.haspadding
		} else if _, isUnion := f.(*core.Union); isUnion && unionIsbits(f) {
			return layout{}, fmt.Errorf("%w: the layout of an isbits Union field of %s", ErrUnsupported, t.Name.Name)
		}
		if fa > 0 {
			l.size = (l.size + fa - 1) / fa * fa
		}
		l.offsets = append(l.offsets, l.size)
		l.sizes = append(l.sizes, fs)
		l.pointers = append(l.pointers, isptr)
		l.size += fs
		used += fs
		l.align = max(l.align, fa)
		if isptr {
			l.isbitsegal = false
		}
	}
	l.size = (l.size + l.align - 1) / l.align * l.align
	l.haspadding = l.haspadding || used < l.size
	return l, nil
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
