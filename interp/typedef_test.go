package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The type definition builtins check their arguments as builtins.c does.
func TestTypedefBuiltins(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	m := s.Mod
	sym := func(s string) expr.Symbol { return expr.Symbol(s) }
	abs := core.NewIncompleteType("L", "Abs", nil, true, false, nil)
	abs.SetSuper(core.AnyType)
	st := core.NewIncompleteType("L", "St", nil, false, false, []expr.Symbol{"x"})
	st.SetSuper(core.AnyType)
	st.SetFieldTypes([]any{core.Int64Type})
	lnn := expr.LineNumberNode{Line: 1, File: sym("none")}
	cases := []struct {
		name string
		f    expr.Symbol
		args []any
		want string
	}{
		{"_abstracttype arity", "_abstracttype", nil, "!_abstracttype: too few arguments"},
		{"_abstracttype module", "_abstracttype", []any{int64(1), sym("A"), SVec{}}, "!TypeError(:_abstracttype, \"\", Module, 1)"},
		{"_abstracttype name", "_abstracttype", []any{m, int64(1), SVec{}}, "!TypeError(:_abstracttype, \"\", Symbol, 1)"},
		{"_abstracttype params", "_abstracttype", []any{m, sym("A"), int64(1)}, "!TypeError(:_abstracttype, \"\", Core.SimpleVector, 1)"},
		{"_abstracttype with a parameter that is not a TypeVar", "_abstracttype", []any{m, sym("A"), SVec{int64(1)}}, "!TypeError(:_abstracttype, \"\", TypeVar, 1)"},
		{"_structtype arity", "_structtype", []any{m}, "!_structtype: too few arguments"},
		{"_structtype header error", "_structtype", []any{int64(1), sym("S"), SVec{}, SVec{}, SVec{}, false, int64(0)}, "!TypeError(:_structtype"},
		{"_structtype names", "_structtype", []any{m, sym("S"), SVec{}, int64(1), SVec{}, false, int64(0)}, "!TypeError(:_structtype, \"\", Core.SimpleVector, 1)"},
		{"_structtype attrs", "_structtype", []any{m, sym("S"), SVec{}, SVec{}, int64(1), false, int64(0)}, "!TypeError(:_structtype, \"\", Core.SimpleVector, 1)"},
		{"_structtype mutable", "_structtype", []any{m, sym("S"), SVec{}, SVec{}, SVec{}, int64(1), int64(0)}, "!TypeError(:_structtype, \"\", Bool, 1)"},
		{"_structtype ninit", "_structtype", []any{m, sym("S"), SVec{}, SVec{}, SVec{}, false, true}, "!TypeError(:_structtype, \"\", Int64, true)"},
		{"_structtype attributes", "_structtype", []any{m, sym("S"), SVec{}, SVec{sym("x")}, SVec{int64(1), sym("const")}, true, int64(1)}, "!not ported yet: field attributes"},
		{"_structtype field name", "_structtype", []any{m, sym("S"), SVec{}, SVec{int64(1)}, SVec{}, false, int64(1)}, "!TypeError(:_structtype, \"\", Symbol, 1)"},
		{"_setsuper! arity", "_setsuper!", nil, "!_setsuper!: too few arguments"},
		{"_setsuper! of a non-type", "_setsuper!", []any{int64(1), core.AnyType}, "!TypeError(:_setsuper!, \"\", DataType, 1)"},
		{"_setsuper! to Builtin", "_setsuper!", []any{core.NewIncompleteType("L", "B", nil, false, false, nil), vm.builtinType}, "!cannot add subtypes to Core.Builtin."},
		{"_setsuper! to a concrete type", "_setsuper!", []any{core.NewIncompleteType("L", "B", nil, false, false, nil), core.Int64Type}, "!can only subtype abstract types."},
		{"_typebody! arity", "_typebody!", nil, "!_typebody!: too few arguments"},
		{"_typebody! of a non-type", "_typebody!", []any{false, int64(1)}, "!TypeError(:_typebody!, \"\", DataType, 1)"},
		{"_typebody! without fields", "_typebody!", []any{false, abs}, "Main.L.Abs"},
		{"_typebody! fields not an svec", "_typebody!", []any{false, st, int64(1)}, "!TypeError(:_typebody!, \"\", Core.SimpleVector, 1)"},
		{"_typebody! field not a type", "_typebody!", []any{false, st, SVec{int64(1)}}, "!TypeError(:St, \"type definition\", Type, 1)"},
		{"_typebody! of an abstract type", "_typebody!", []any{false, abs, SVec{}}, "!abstract type has no fields"},
		{"_typebody! same fields as before", "_typebody!", []any{st, core.NewIncompleteType("L", "St", nil, false, false, []expr.Symbol{"x"}), SVec{core.Int64Type}}, "Main.L.St"},
		{"_equiv_typedef arity", "_equiv_typedef", nil, "!_equiv_typedef: too few arguments"},
		{"_equiv_typedef of a non-type", "_equiv_typedef", []any{int64(1), st}, "false"},
		{"_equiv_typedef of other kinds", "_equiv_typedef", []any{abs, st}, "false"},
		{"_equiv_typedef of other field names", "_equiv_typedef", []any{st, newSt("L", "St", "y")}, "false"},
		{"_equiv_typedef of the same", "_equiv_typedef", []any{st, newSt("L", "St", "x")}, "true"},
		{"_defaultctors arity", "_defaultctors", nil, "!_defaultctors: too few arguments"},
		{"_defaultctors of a non-type", "_defaultctors", []any{int64(1), lnn}, "!TypeError(:ctor_def, \"\", DataType, 1)"},
		{"_defaultctors without a line", "_defaultctors", []any{st, int64(1)}, "!TypeError(:ctor_def, \"\", LineNumberNode, 1)"},
		{"fieldtype arity", "fieldtype", nil, "!fieldtype: too few arguments"},
		{"fieldtype boundscheck", "fieldtype", []any{st, int64(1), int64(1)}, "!TypeError(:fieldtype, \"\", Bool, 1)"},
		{"fieldtype with boundscheck", "fieldtype", []any{st, int64(1), true}, "Int64"},
		{"fieldtype of a UnionAll", "fieldtype", []any{core.ArrayType, int64(1)}, "!not ported"},
		{"fieldtype of a value", "fieldtype", []any{int64(1), int64(1)}, "!TypeError(:fieldtype, \"\", DataType, 1)"},
		{"_typevar arity", "_typevar", []any{sym("T")}, "!TypeVar: too few arguments (expected 3)"},
		{"_typevar of a non-symbol", "_typevar", []any{int64(1), core.Bottom, core.AnyType}, "!TypeError(:TypeVar, \"\", Symbol, 1)"},
		{"_typebody! with other field types", "_typebody!", []any{st, newSt("L", "St", "x"), SVec{core.BoolType}}, ""},
		{"_typebody! with more fields", "_typebody!", []any{st, core.NewIncompleteType("L", "St", nil, false, false, []expr.Symbol{"x", "y"}), SVec{core.Int64Type, core.Int64Type}}, ""},
		{"fieldtype of an unknown name", "fieldtype", []any{st, sym("nope")}, "!type St has no field nope"},
		{"fieldtype of a bad index", "fieldtype", []any{st, 1.5}, "!TypeError(:fieldtype, \"\", Symbol, 1.5)"},
	}
	for _, c := range cases {
		v, err := vm.apply(coreValue(t, vm, c.f), c.args)
		check(t, c.name, v, err, c.want)
	}

	// calling a type without constructors, and defining one for a type of
	// another VM, are refused
	_, err := vm.apply(core.Int64Type, []any{int64(1)})
	check(t, "call of a type without constructors", nil, err, "!MethodError(Int64, (1,)")
	other := New()
	if _, err := other.apply(coreValue(t, other, "_defaultctors"), []any{newSt("Nope", "Q", "x"), lnn}); err != nil {
		t.Errorf("constructors of a type of an unknown module: %v", err)
	}
	for _, c := range []struct{ src, want string }{
		{"global gg\ngg = 1\ngg", "1"},
		{"isdefinedglobal(Core, :Int, 1)", "!TypeError(:isdefined, \"\", Bool, 1)"},
		{"isdefinedglobal(Core, :Int, false)", "true"},
	} {
		v, err := New().NewSession().Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	for _, c := range []struct {
		name string
		e    any
		want string
	}{
		{"global of a GlobalRef", expr.New("global", gr("L", "h")), "nothing"},
		{"global of a GlobalRef to an unknown module", expr.New("global", gr("Nope", "h")), "!not ported"},
		{"global of a non-name", expr.New("global", int64(1)), "!not ported"},
	} {
		v, err := vm.toplevelEval(m, c.e)
		check(t, c.name, v, err, c.want)
	}
	// a constructor method for a type whose constructors are not defined here
	body := codeInfo([]any{ret(int64(1))}, "#self#")
	tyInt, _ := core.Apply(core.TypeType, core.Int64Type)
	_, err = runIR(s, codeInfo([]any{
		call(gr("Core", "svec"), call(gr("Core", "svec"), expr.QuoteNode{Value: tyInt}), call(gr("Core", "svec"))),
		expr.New("method", gr("Core", "Int"), expr.SSAValue(1), body),
		ret(nil),
	}), true)
	check(t, "a constructor of Int", nil, err, "!not ported")
	// a TypeVar shown with its bounds, as base/show.jl shows it
	vec1 := core.TypeUnionAll(core.ArrayType.Var, core.ArrayType.Body.(*core.UnionAll).Body)
	for tv, want := range map[*core.TypeVar]string{
		{Name: "T", LB: core.Int64Type, UB: core.AnyType}:     "T>:Int64",
		{Name: "T", LB: core.Int64Type, UB: core.IntegerType}: "Int64<:T<:Integer",
		{Name: "T", LB: core.Bottom, UB: core.ArrayType}:      "T<:Array",
		{Name: "T", LB: core.Bottom, UB: vec1}:                "T<:(",
	} {
		if got := Repr(tv); !strings.HasPrefix(got, want) {
			t.Errorf("Repr(%s) = %s, want %s…", tv.Name, got, want)
		}
	}
	_, err = vm.apply(core.UnionAllType, []any{&core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}, core.VarargType})
	check(t, "UnionAll over a bare Vararg", nil, err, "!not ported")
	if ctorTypeName(must(core.Apply(core.TypeType, core.TypeUnion(core.Int64Type, core.BoolType)))) != nil {
		t.Error("ctorTypeName of Type{Union{…}} named a type")
	}
	if !panics(func() { must(0, errors.New("x")) }) {
		t.Error("must of an error did not panic")
	}
}

func newSt(mod, name, field string) *core.DataType {
	d := core.NewIncompleteType(mod, name, nil, false, false, []expr.Symbol{expr.Symbol(field)})
	d.SetSuper(core.AnyType)
	d.SetFieldTypes([]any{core.Int64Type})
	return d
}
