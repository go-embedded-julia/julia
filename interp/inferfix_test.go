package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// ccall(…, Bool, …) of a C function returning uint8_t: julia keeps the low
// bit as the Bool, so jl_ir_flag_has_fcall's 0x00 is false (the inliner's
// `!may_have_fcalls(m)` raised MethodError(!, (0x00,)) before).
func TestForeignResultBool(t *testing.T) {
	vm := New()
	ret := func(v any) shim { return func(*VM, []any) (any, error) { return v, nil } }
	for _, c := range []struct {
		v    any
		want any
	}{
		{uint8(0), false}, {uint8(1), true}, {uint8(2), false}, {int8(-1), true},
		{uint16(1), true}, {int16(2), false}, {uint32(1), true}, {int32(0), false},
		{uint64(3), true}, {int64(0), false},
		{"x", "x"}, {nil, nil},
	} {
		v, err := vm.foreignResult(core.BoolType, ret(c.v), nil)
		if err != nil || v != c.want {
			t.Errorf("Bool of %#v: %#v %v, want %#v", c.v, v, err, c.want)
		}
	}
	// another return type keeps the integer
	if v, _ := vm.foreignResult(core.UInt8Type, ret(uint8(2)), nil); v != uint8(2) {
		t.Errorf("UInt8 of 0x02: %#v", v)
	}
}

// isdefined of a Vararg's fields: Vararg{T} leaves N NULL (jl_vararg_t), so
// julia answers true, false for Tuple{Int,Vararg{Int}}.parameters[2]
// (isdefined(v, :T), isdefined(v, :N)); Compiler's constrains_param relies on
// it before reading vararg.N.
func TestIsdefinedVararg(t *testing.T) {
	vm := New()
	for _, c := range []struct {
		name string
		v    *core.Vararg
		idx  any
		want bool
	}{
		{"Vararg{Int} :T", &core.Vararg{T: core.Int64Type}, expr.Symbol("T"), true},
		{"Vararg{Int} :N", &core.Vararg{T: core.Int64Type}, expr.Symbol("N"), false},
		{"Vararg{Int} 1", &core.Vararg{T: core.Int64Type}, int64(1), true},
		{"Vararg{Int} 2", &core.Vararg{T: core.Int64Type}, int64(2), false},
		{"Vararg{Int,2} :N", &core.Vararg{T: core.Int64Type, N: int64(2)}, expr.Symbol("N"), true},
		{"Vararg :T", &core.Vararg{}, expr.Symbol("T"), false},
	} {
		v, err := biIsdefined(vm, []any{c.v, c.idx})
		if err != nil || v != c.want {
			t.Errorf("isdefined(%s): %v %v, julia %v", c.name, v, err, c.want)
		}
	}
}

// jl_promote_cis_to_current (gf.c:1790-1804): each CodeInstance of the
// Ptr{CodeInstance} array validated in the current world, with its edges.
func TestPromoteCIsToCurrent(t *testing.T) {
	f := newCodeinstFixture(t)
	vm := f.vm
	w := vm.World()
	inner := f.newCI(w, nil)
	a := f.newCI(w, SVec{inner})
	b := f.newCI(w, nil)
	skipped := f.newCI(w, nil)
	memT := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), vm.tCodeInstance, core.CPU)).(*core.DataType)
	mem := &Memory{Type: memT, Elems: []any{skipped, a, b}}
	ptr := func(off int64) *Pointer { return &Pointer{Type: vm.ptrType(vm.tCodeInstance), Target: mem, Off: off} }
	// another world: nothing changes
	if _, err := f.sh("jl_promote_cis_to_current", ptr(8), uint64(2), w+1); err != nil || a.Fields[ciMaxWorld] != any(w) {
		t.Errorf("another world: %v %v", err, a.Fields[ciMaxWorld])
	}
	if _, err := f.sh("jl_promote_cis_to_current", ptr(8), uint64(2), w); err != nil {
		t.Fatal(err)
	}
	for name, ci := range map[string]*Struct{"a": a, "b": b, "inner": inner} {
		if ci.Fields[ciMaxWorld] != any(^uint64(0)) {
			t.Errorf("%s not promoted: %v", name, ci.Fields[ciMaxWorld])
		}
	}
	if skipped.Fields[ciMaxWorld] != any(w) {
		t.Errorf("an element before the pointer: %v", skipped.Fields[ciMaxWorld])
	}
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"no pointer", []any{int64(1), uint64(1), w}, "!not ported yet: jl_promote_cis_to_current of"},
		{"no count", []any{ptr(0), int64(1), w}, "!not ported yet: jl_promote_cis_to_current of"},
		{"misaligned", []any{ptr(4), uint64(1), w}, "!not ported yet: jl_promote_cis_to_current of"},
		{"past the memory", []any{ptr(8), uint64(3), w}, "!not ported yet: jl_promote_cis_to_current past the memory"},
		{"offset past the memory", []any{ptr(32), uint64(1), w}, "!not ported yet: jl_promote_cis_to_current past the memory"},
		{"not a CodeInstance", []any{&Pointer{Type: vm.ptrType(vm.tCodeInstance), Target: &Memory{Type: memT, Elems: []any{int64(1)}}}, uint64(1), w}, "!TypeError(:ccall"},
	} {
		v, err := f.sh("jl_promote_cis_to_current", c.args...)
		check(t, c.name, v, err, c.want)
	}
}
