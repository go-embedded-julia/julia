package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// DataType.flags and TypeName.flags, each want julia 1.12.6's repr of the
// same field.
func TestTypeFlags(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval(`struct F1; a::Int; end
mutable struct F2; a::Int; end
struct F3; a::Any; end
struct F4; s::String; end
struct F5; m::F2; end
struct F6; u::Union{Int8, String}; end
struct F7{T}; x::T; end`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"(Int64.flags, F1.flags, F2.flags, F3.flags, F4.flags, F5.flags)", "(0x43ea, 0x036a, 0x0062, 0x0072, 0x0372, 0x0072)"},
		{"(String.flags, Symbol.flags, DataType.flags, Module.flags)", "(0x2b62, 0x1f62, 0x0972, 0x2362)"},
		{"(Tuple{Int64}.flags, Tuple{Any}.flags, Array{Int64,1}.flags)", "(0x036e, 0x0060, 0x0072)"},
		{"(Nothing.flags, Core.SimpleVector.flags, Bool.flags, UInt8.flags, Char.flags, Float64.flags, Int128.flags)", "(0x036a, 0x2662, 0x33ea, 0x57ea, 0x37ea, 0x03ea, 0x03ea)"},
		{"(Int64.name.flags, F2.name.flags, String.name.flags, Number.name.flags)", "(0x04, 0x02, 0x02, 0x01)"},
		{"(Type{Int64}.flags, Memory{Int64}.flags, Core.GenericMemoryRef{:not_atomic, Int64, Core.CPU}.flags)", "(0x0160, 0x0062, 0x0072)"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// a Union field, a parameter, a struct that reaches itself (julia:
	// 0x0372, 0x036a, 0x0072, 0x0072)
	if _, err := s.Eval("struct R1; r::Union{Nothing, R1}; end"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Eval("(F6.flags, F7{Int64}.flags, F7{F2}.flags, R1.flags)")
	check(t, "Union, parameter and recursive fields", v, err, "(0x0372, 0x036a, 0x0072, 0x0072)")
	// a free type variable is neither (is_type_mutationfree's last case)
	tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	if mutationFree(tv, map[*core.DataType]bool{}) || identityFree(tv, map[*core.DataType]bool{}) {
		t.Error("a type variable is mutation- or identity-free")
	}
	if zeroinit(core.NewIncompleteType("Main", "Z1", nil, false, false, []expr.Symbol{"x"})) {
		t.Error("zeroinit of a type without field types")
	}
	if !identityFree(core.TypeUnion(core.Int8Type, core.StringType), map[*core.DataType]bool{}) {
		t.Error("identityFree of a Union of identity-free types")
	}
}
