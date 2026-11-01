package interp

import (
	"testing"
	"testing/fstest"
)

// TestBoot evaluates boot.jl in Core, then checks what post_boot_hooks
// (jltypes.c:3909-3968) establishes: the integer types under boot.jl's
// abstract types, and the runtime's exceptions being Core's.
func TestBoot(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	for src, want := range map[string]string{
		"(Bool.super, UInt8.super, UInt16.super, UInt32.super, UInt64.super)":            "(Integer, Unsigned, Unsigned, Unsigned, Unsigned)",
		"(Int8.super, Int16.super, Int32.super, Int64.super, Char.super)":                "(Signed, Signed, Signed, Signed, AbstractChar)",
		"(Float16.super, Float32.super, Float64.super, Signed.super)":                    "(AbstractFloat, AbstractFloat, AbstractFloat, Integer)",
		"try; getfield((1,), 2); catch e; e isa BoundsError; end":                        "true",
		"try; throw(ErrorException(\"m\")); catch e; e; end":                             "ErrorException(\"m\")",
		"try; Core.Intrinsics.checked_sdiv_int(1, 0); catch e; e === DivideError(); end": "true",
	} {
		v, err := s.Eval(src)
		check(t, src, v, err, want)
	}
	// after post_boot_hooks, an error in an included file is a LoadError
	vm.Sources = fstest.MapFS{"t.jl": {Data: []byte("x = 1\nthrow(x)\n")}}
	v, err := s.Eval("try; include(Core, \"t.jl\"); catch e; e; end")
	check(t, "LoadError", v, err, "LoadError(\"t.jl\", 2, 1)")
}

// getfield of the type-system values reads the layouts jltypes.c gives
// them.
func TestKindFields(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	for src, want := range map[string]string{
		"(Int64.name.name, Int64.parameters, Int64.types, Any.super)":  "(:Int64, svec(), svec(), Any)",
		"(Tuple{Int8}.parameters, Tuple{Int8}.types)":                  "(svec(Int8), svec(Int8))",
		"(Array{Int8, 1}.name.wrapper, Expr.name.names, Number.types)": "(Array, svec(:head, :args), svec())",
		"Array.name": "!type UnionAll has no field name",
		"(Array.var.name, Array.var.ub, Array.body.var.lb)": "(:T, Any, Union{})",
		"(Union{Int8, Int16}.a, Union{Int8, Int16}.b)":      "(Int16, Int8)",
		"(Vararg{Int8}.T, getfield(Array, 1))":              "(Int8, T)",
		"Vararg{Int8}.N":                                    "!UndefRefError()",
		"Int64.layout":                                      "!not ported yet: the field layout of a DataType",
		"Int64.name.cache":                                  "!not ported yet: the field cache of a TypeName",
		"Int64.nope":                                        "!type DataType has no field nope",
		"getfield(Array.var, 4)":                            "!BoundsError",
	} {
		v, err := s.Eval(src)
		check(t, src, v, err, want)
	}
}
