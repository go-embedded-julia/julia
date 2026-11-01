package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Struct sizes as julia 1.12.6 lays them out (its answers: 24, 32, 16, 16,
// 8), jl_options' defaults, and :new of the AST and IR node types giving
// package expr's values.
func TestLayoutAndNodes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval(`struct S1; a::Int8; b::Int64; c::Int16; end
struct S2; x::S1; y::Int8; end
struct S3; a::Int8; b::Any; end
struct U1; a::Union{Int8, Int16}; end
struct O1; banner::Int8; opt_level::Int8; project::Ptr{UInt8}; heap::UInt64; end
struct O2; a::Int8; b::Int128; end
abstract type A1 end`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"(sizeof(S1), sizeof(S2), sizeof(S3), sizeof(Tuple{Int8, Int64}), sizeof(Ptr{UInt8}))", "(24, 32, 16, 16, 8)"},
		{"sizeof(U1)", "!not ported yet: the layout of an isbits Union field of U1"},
		{"sizeof(A1)", "!Abstract type A1 does not have a definite size."},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	if _, _, err := dataLayout(core.NewIncompleteType("Main", "I1", nil, false, false, []expr.Symbol{"x"})); err == nil {
		t.Error("the layout of a type without field types")
	}
	if _, _, err := dataLayout(core.NumberType); err == nil {
		t.Error("the layout of an abstract type")
	}
	// an isbits struct whose own layout cannot be computed, as a field
	inner := core.NewIncompleteType("Main", "I3", nil, false, false, []expr.Symbol{"u"})
	must(0, inner.SetSuper(core.AnyType))
	must(0, inner.SetFieldTypes([]any{core.TypeUnion(core.Int8Type, core.Int16Type)}))
	inner.Name.Isbits = true // as julia has it: a Union of isbits types is stored inline
	outer := core.NewIncompleteType("Main", "I4", nil, false, false, []expr.Symbol{"x"})
	must(0, outer.SetSuper(core.AnyType))
	must(0, outer.SetFieldTypes([]any{inner}))
	if _, _, err := dataLayout(outer); err == nil {
		t.Error("the layout of a struct holding an unlaid struct")
	}
	var p any
	_, err := biCglobal(New(), []any{expr.Symbol("jl_options"), core.Bottom})
	check(t, "jl_options as Union{}", nil, err, "!not ported yet: jl_options as Union{}")

	// jl_options for a mirror of the C struct
	o1 := coreValue(t, vm, "O1").(*core.DataType)
	opts, err := vm.defaultOptions(o1)
	check(t, "jl_options", opts, err, "O1(-1, 2, Ptr{UInt8}(0x0000000000000000), 0x0000000000000000)")
	if _, err := vm.defaultOptions(core.NewIncompleteType("Main", "I2", nil, false, false, []expr.Symbol{"x"})); err == nil {
		t.Error("jl_options of a type without field types")
	}
	p, err = biCglobal(vm, []any{expr.Symbol("jl_options"), o1})
	check(t, "cglobal(:jl_options)", p, err, "")
	v, err := biPointerref(vm, []any{p, int64(1), int64(1)})
	check(t, "the options through the pointer", v, err, "O1(-1, 2, Ptr{UInt8}(0x0000000000000000), 0x0000000000000000)")
	for _, c := range []struct {
		p    *Pointer
		want string
	}{
		{p.(*Pointer), "Ptr{O1}(cglobal(:jl_options) + 0)"},
		{&Pointer{Type: vm.ptrType(core.UInt8Type), Target: "ab", Off: 1}, `Ptr{UInt8}("ab" + 1)`},
		{vm.cglobals["jl_uv_stdout"].val.(*Pointer), "Ptr{Nothing}(stdout + 0)"},
	} {
		if got := Repr(c.p); got != c.want {
			t.Errorf("show of a pointer: %s, want %s", got, c.want)
		}
	}
	_, err = biCglobal(New(), []any{expr.Symbol("jl_options"), core.Int64Type})
	check(t, "jl_options as Int64", nil, err, "!not ported yet")
	_, err = biCglobal(New(), []any{expr.Symbol("jl_options"), int64(1)})
	check(t, "jl_options as a non-type", nil, err, "!TypeError(:cglobal")
	_, err = biCglobal(New(), []any{expr.Symbol("jl_options"), free(t)})
	check(t, "jl_options as a free type", nil, err, "!cglobal: type argument not concrete")
	_, err = biCglobal(New(), []any{expr.Symbol("jl_options"), coreValue(t, vm, "O2")})
	check(t, "jl_options as a struct with an Int128", nil, err, "!not ported yet: the jl_options field b")
	_, err = vm.shims["jl_sizeof_jl_options"](vm, nil)
	check(t, "jl_sizeof_jl_options without Base.JLOptions", nil, err, "!Base.JLOptions is not defined")
	b := vm.Base.declare("JLOptions", 0)
	b.Value = o1
	v, err = vm.shims["jl_sizeof_jl_options"](vm, nil)
	check(t, "jl_sizeof_jl_options", v, err, "24")
	for name, args := range map[string][]any{"jl_module_name": {int64(1)}, "jl_module_globalref": {int64(1), int64(1)}} {
		_, err := vm.shims[expr.Symbol(name)](vm, args)
		check(t, name+" of a non-module", nil, err, "!TypeError(")
	}
	v, err = vm.shims["jl_module_name"](vm, []any{vm.Core})
	check(t, "jl_module_name", v, err, ":Core")

	// :new of the node types is expr's value
	for src, want := range map[string]any{
		"Core.SSAValue(3)":                    expr.SSAValue(3),
		"Core.SlotNumber(2)":                  expr.SlotNumber(2),
		"Core.Argument(2)":                    expr.Argument(2),
		"Core.GotoNode(4)":                    expr.GotoNode(4),
		"Core.GotoIfNot(true, 3)":             expr.GotoIfNot{Cond: true, Dest: 3},
		"Core.ReturnNode(1)":                  expr.ReturnNode{Val: int64(1)},
		"Core.ReturnNode()":                   expr.Unreachable{},
		"Core.NewvarNode(Core.SlotNumber(1))": expr.NewvarNode{Slot: expr.SlotNumber(1)},
		"Core.EnterNode(5)":                   expr.EnterNode{Catch: 5},
		"Core.EnterNode(5, 1)":                expr.EnterNode{Catch: 5, Scope: int64(1), HasScope: true},
		"LineNumberNode(3, :f)":               expr.LineNumberNode{Line: 3, File: expr.Symbol("f")},
		"QuoteNode(:x)":                       expr.QuoteNode{Value: expr.Symbol("x")},
		"GlobalRef(Core, :Int)":               expr.GlobalRef{Mod: "Core", Name: "Int"},
	} {
		v, err := s.Eval(src)
		if err != nil || v != want {
			t.Errorf("%s: %#v, %v", src, v, err)
		}
	}
}

func free(t *testing.T) any {
	t.Helper()
	return must(core.Apply(core.ArrayType, &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}, int64(1)))
}
