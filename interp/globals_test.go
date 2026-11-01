package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Typed globals as jl_declare_global (toplevel.c:270-331) and
// jl_checked_assignment (module.c:1886-1907) treat them, and the bindings
// export makes, which resolve through usings until declared.
func TestTypedGlobals(t *testing.T) {
	vm := New()
	m := vm.NewModule("G")
	ref := func(n string) expr.GlobalRef { return expr.GlobalRef{Mod: "G", Name: expr.Symbol(n)} }
	if err := vm.declareGlobal(ref("i"), core.Int64Type); err != nil {
		t.Fatal(err)
	}
	if v, _ := biGetBindingType(vm, []any{m, expr.Symbol("i")}); v != any(core.Int64Type) {
		t.Errorf("get_binding_type: %v", v)
	}
	check(t, "the same type again", nil, vm.declareGlobal(ref("i"), core.Int64Type), "")
	check(t, "another type", nil, vm.declareGlobal(ref("i"), core.StringType),
		"!cannot set type for global G.i. It already has a value or is already set to a different type.")
	vm.taskWorld = vm.World() // the declaration's world, as :latestworld makes it
	check(t, "an assignment of the type", nil, vm.assignGlobal(m, "i", int64(1)), "")
	check(t, "an assignment of another type", nil, vm.assignGlobal(m, "i", "s"), "!TypeError(:setglobal!")
	check(t, "Any after a value", nil, vm.declareGlobal(ref("i"), core.AnyType), "")
	m.declare("v", 0).Value = int64(1)
	check(t, "a type for a global with a value", nil, vm.declareGlobal(ref("v"), core.Int64Type),
		"!cannot set type for global G.v.")
	c := m.declare("c", 0)
	c.Value, c.Const = int64(1), true
	check(t, "a type for a constant", nil, vm.declareGlobal(ref("c"), core.Int64Type), "!cannot set type for constant G.c.")
	m.declare("a", 0).Alias = vm.Core.own("Int")
	check(t, "a type for an import", nil, vm.declareGlobal(ref("a"), core.Int64Type), "!cannot set type for imported binding G.a.")

	// export of a name a module uses leaves it resolving there
	s := vm.NewSession()
	v, err := s.Eval("export tuple\ntuple(1)")
	check(t, "an exported name used from Core", v, err, "(1,)")
	v, err = s.Eval("export zz\nglobal zz\nzz")
	check(t, "an exported name declared", v, err, "!UndefVarError(:zz,")
	// a method body's global is declared when the method is defined
	v, err = s.Eval("function setw(x)\n    global w\n    w = x\nend\nsetw(3)\nw")
	check(t, "a global in a method body", v, err, "3")
	_, err = vm.definitionEffects(m, &expr.CodeInfo{Code: []any{expr.New("global", int64(1))}})
	check(t, "a malformed global in a body", nil, err, "!syntax: malformed")
	_, err = vm.definitionEffects(m, &expr.CodeInfo{Code: []any{expr.New("globaldecl", gr("Nope", "x"))}})
	check(t, "a global of an unknown module in a body", nil, err, "!not ported")
	// and a method whose body has one is not defined
	svec := func(a ...any) *expr.Expr { return call(append([]any{gr("Core", "svec")}, a...)...) }
	v, err = runIR(New().NewSession(), codeInfo([]any{
		expr.New("method", gr("L", "f")),
		expr.New("latestworld"),
		call(gr("Core", "Typeof"), expr.SSAValue(1)),
		svec(svec(expr.SSAValue(3)), svec()),
		expr.New("method", gr("L", "f"), expr.SSAValue(4), codeInfo([]any{expr.New("global", int64(1)), ret(nil)}, "#self#")),
		ret(nil),
	}), true)
	check(t, "a method with a malformed global", v, err, "!syntax: malformed")
}
