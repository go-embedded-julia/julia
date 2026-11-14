package interp

import "testing"

// DataType.instance, each want julia 1.12.6's (testdata/instance.jl), the
// session's types shown under Main.L.
func TestTypeInstance(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("struct Z end; struct ZZ; a::Nothing; b::Z; end; struct NZ; x::Int; end; mutable struct MZ end; f() = 1"); err != nil {
		t.Fatal(err)
	}
	for ty, want := range map[string]string{
		"Nothing": "nothing", "Core.TypeofBottom": "Union{}", "Tuple{}": "()",
		"Z": "Main.L.Z()", "ZZ": "Main.L.ZZ(nothing, Main.L.Z())", "NZ": "!UndefRefError()",
		"MZ": "!UndefRefError()", "Int64": "!UndefRefError()", "Any": "!UndefRefError()",
		"Integer": "!UndefRefError()",
		"Core.GenericMemory{:not_atomic, Int, Core.CPU}": "Int64[]",
		"Core.GenericMemory{:atomic, Int, Core.CPU}":     "Int64[]",
		"Tuple{Nothing}":         "(nothing,)",
		"Core.IntrinsicFunction": "!UndefRefError()",
		"Tuple{Int64}":           "!UndefRefError()",
		"Core.GenericMemory{:not_atomic, Int, :elsewhere}": "!UndefRefError()",
	} {
		v, err := s.Eval("getfield(" + ty + ", :instance)")
		check(t, ty, v, err, want)
	}
	for src, want := range map[string]string{
		"Z() === getfield(Z, :instance)":                         "true",
		"NZ(1) === NZ(1)":                                        "true",
		"Z() === Z()":                                            "true",
		"getfield(typeof(f), :instance) === f":                   "true",
		"getfield(typeof(Core.tuple), :instance) === Core.tuple": "true",
		"getfield(Core.GenericMemory{:not_atomic, Int, Core.CPU}, :instance) === getfield(Core.GenericMemory{:not_atomic, Int, Core.CPU}, :instance)": "true",
		"getfield(Core.TypeName, :instance)": "!UndefRefError()",
	} {
		v, err := s.Eval(src)
		check(t, src, v, err, want)
	}
}
