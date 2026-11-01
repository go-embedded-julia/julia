package interp

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The branches of jl_expand_macros the differential programs do not reach:
// malformed forms, escapes, hygienic scopes given as input, do-blocks after
// a macro call, qualified macro names, and the builtins macros build with.
func TestMacroExpansion(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"@undefined_macro x", "!UndefVarError(:@undefined_macro,"},
		{"macro m(x)\n    x\nend\n@m 2", "2"},
		{"macro m()\n    :(@n())\nend\n@m()", "!UndefVarError(:@n,"},
		{"macro m(f, x)\n    x\nend\n@m(3) do y\n    y\nend", "3"},
		{"macro m()\n    Expr(:escape, :zz)\nend\nzz = 4\n@m()", "4"},
		{"macro m()\n    Expr(:escape, 5)\nend\n@m()", "5"},
		{"macro m()\n    :(Expr(:escape, 6))\nend\ntypeof(@m())", "Expr"},
		{"macro m()\n    Expr(:escape, :(ww = 8))\nend\n@m()\nww", "8"},
		{"macro m()\n    1\nend\nCore.@m()", "!UndefVarError(:@m, "},
		{"macro m(x)\n    x\nend\n@m(:(1 + $2))", ""},
		{"import Core.Intrinsics\nmacro m()\n    7\nend\nIntrinsics.@m()", "!UndefVarError(:@m,"},
		{"macro m()\n    1\nend\nm = 3\nL = 0\nnothing", "nothing"},
		{"macro inner()\n    Expr(:escape, Expr(:escape, :zz))\nend\nmacro outer()\n    :(@inner())\nend\nzz = 9\n@outer()", "9"},
		{"typeof(LineNumberNode(1, \"f\"))", "LineNumberNode"},
		{"import Core: _apply_iterate\n_apply_iterate(tuple)", "!_apply_iterate: too few arguments (expected 2)"},
		{"macro r()\n    :(@r())\nend\n@r()", "!StackOverflowError()"},
		{"tuple((1, 2)...)", "(1, 2)"},
		{"import Core: svec\ntuple(svec(1, 2)...)", "(1, 2)"},
		{"tuple(:abc...)", "!not ported yet: splatting a Symbol"},
		{"iterate((1,))", "!MethodError(iterate, ((1,),)"},
		{"try\n    LineNumberNode(:x)\ncatch e\n    typeof(e)\nend", "MethodError"},
		{"try\n    Expr(1)\ncatch e\n    e\nend", "TypeError(:Expr, \"\", Symbol, 1)"},
		{"try\n    Expr()\ncatch e\n    e\nend", ""},
	} {
		v, err := New().NewSession().Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	s := New().NewSession()
	for _, c := range []struct {
		name string
		e    any
		want string
	}{
		{"inert", expr.New("inert", expr.Symbol("x")), ""},
		{"hygienic scope of a module", expr.New("hygienic-scope", expr.New("call", expr.Symbol("f")), expr.Module{Path: []expr.Symbol{"Main", "L"}}), ""},
		{"hygienic scope of an escape", expr.New("hygienic-scope", expr.New("escape", expr.Symbol("x")), expr.Module{Path: []expr.Symbol{"Main", "L"}}), ""},
		{"hygienic scope of a constant", expr.New("hygienic-scope", int64(3), expr.Module{Path: []expr.Symbol{"Main", "L"}}), "3"},
		{"hygienic scope of an escaped constant", expr.New("hygienic-scope", expr.New("escape", int64(3)), expr.Module{Path: []expr.Symbol{"Main", "L"}}), "3"},
		{"hygienic scope of a non-module", expr.New("hygienic-scope", int64(3), int64(1)), "!TypeError(:hygienic-scope"},
		{"hygienic scope with an error inside", expr.New("hygienic-scope", expr.New("macrocall", expr.Symbol("@nope"), nil), expr.Module{Path: []expr.Symbol{"Main", "L"}}), "!UndefVarError(:@nope,"},
		{"macrocall without a location", expr.New("macrocall"), "!macrocall: too few arguments (expected 3)"},
		{"macrocall with a bad location", expr.New("macrocall", expr.Symbol("@nope"), int64(1)), "!UndefVarError(:@nope,"},
		{"escape outside a macro", expr.New("escape", expr.Symbol("x")), ""},
		{"quote with an error", expr.New("quote", expr.New("$", expr.New("macrocall", expr.Symbol("@nope"), nil))), "!UndefVarError(:@nope,"},
		{"argument error", expr.New("call", expr.New("macrocall", expr.Symbol("@nope"), nil)), "!UndefVarError(:@nope,"},
		{"do without a macro", expr.New("do", expr.New("call", expr.Symbol("f")), int64(1)), ""},
		{"qualified name of a non-module", expr.New("macrocall", expr.New(".", int64(1), expr.QuoteNode{Value: expr.Symbol("@m")}), nil), "!TypeError(:getglobal"},
		{"qualified name with an error", expr.New("macrocall", expr.New(".", expr.Symbol("Nope"), expr.QuoteNode{Value: expr.Symbol("@m")}), nil), "!UndefVarError(:Nope,"},
	} {
		v, err := s.expandMacros(c.e, nil)
		check(t, c.name, v, err, c.want)
	}

	// a macro whose method lookup fails for a reason other than no method
	// is a Go error, not a MethodError
	if _, err := s.Eval("macro w(x)\n    x\nend"); err != nil {
		t.Fatal(err)
	}
	fv := s.Mod.own("@w").Value.(*Function)
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	sig := must(core.Apply(core.AnyTupleType, s.VM.tLineNumberNode, core.ModuleType, T))
	if _, err := fv.Table.Define(core.TypeUnionAll(T, sig), s.VM.newWorld(), nil); err != nil {
		t.Fatal(err)
	}
	old := core.SetMaxSubtypeSteps(1)
	_, err := s.Eval("@w(1)")
	core.SetMaxSubtypeSteps(old)
	var thrown *Thrown
	if err == nil || errors.As(err, &thrown) {
		t.Errorf("macro lookup beyond the work bound: %v", err)
	}
	deep := New().NewSession()
	deep.VM.MaxDepth = 0
	_, err = deep.expandMacros(expr.New("macrocall", expr.Symbol("@m"), nil), nil)
	check(t, "expansion past the depth bound", nil, err, "!StackOverflowError()")
	if needEsc(int64(1)) || !needEsc(expr.Symbol("x")) || needEsc(expr.New("line", int64(1))) {
		t.Error("needEsc")
	}
}
