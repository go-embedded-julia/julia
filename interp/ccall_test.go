package interp

import (
	"testing"
	"testing/fstest"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A :foreigncall reaches the Go shim of the function it names, with its
// arguments checked against the declared types.
func TestForeignCall(t *testing.T) {
	fc := func(name string, rt, atypes any, args ...any) []any {
		return []any{expr.New("foreigncall", append([]any{expr.QuoteNode{Value: expr.Symbol(name)}, rt, atypes,
			int64(0), expr.QuoteNode{Value: expr.Symbol("ccall")}}, args...)...), ret(expr.SSAValue(1))}
	}
	anyT, at := expr.QuoteNode{Value: core.AnyType}, func(ts ...any) any { return expr.QuoteNode{Value: SVec(ts)} }
	for _, c := range []struct {
		name string
		code []any
		want string
	}{
		{"a shim", fc("jl_has_free_typevars", anyT, at(core.AnyType), core.Int64Type), "false"},
		{"an argument by value", fc("jl_has_free_typevars", anyT, at(core.BoolType), true), "false"},
		{"an argument of another type", fc("jl_has_free_typevars", anyT, at(core.Int64Type), "s"), "!not ported yet: a ccall argument of type Int64"},
		{"an unknown function", fc("nope", anyT, at()), "!not ported yet: ccall(:nope)"},
		{"argtypes not an svec", fc("nope", anyT, expr.QuoteNode{Value: int64(1)}), "!Invalid IR: malformed :foreigncall"},
		{"more argtypes than arguments", fc("nope", anyT, at(core.AnyType)), "!Invalid IR: malformed :foreigncall"},
		{"a return type error", fc("nope", gr("Nope", "x"), at()), "!not ported"},
		{"an argtypes error", fc("nope", anyT, gr("Nope", "x")), "!not ported"},
		{"an argument error", fc("nope", anyT, at(core.AnyType), gr("Nope", "x")), "!not ported"},
		{"a computed callee", []any{expr.New("foreigncall", int64(1), anyT, at(), int64(0), int64(0)), ret(nil)}, "!not ported yet: a ccall to a computed"},
	} {
		v, err := runIR(New().NewSession(), codeInfo(c.code), true)
		check(t, c.name, v, err, c.want)
	}

	vm := New()
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"jl_toplevel_eval_in", []any{vm.Core, expr.New("call", gr("Core", "tuple"), int64(1))}, "(1,)"},
		{"jl_toplevel_eval_in", []any{int64(1), int64(1)}, "!TypeError(:eval, \"\", Module, 1)"},
		{"jl_type_unionall", []any{int64(1), core.AnyType}, "!TypeError(:typeassert, \"\", TypeVar, 1)"},
		{"jl_has_free_typevars", []any{&core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}}, "true"},
		{"jl_get_tls_world_age", nil, ""},
		{"jl_set_istopmod", []any{vm.Core, true}, "nothing"},
	} {
		v, err := vm.shims[expr.Symbol(c.name)](vm, c.args)
		check(t, c.name, v, err, c.want)
	}
}

// Boot reports what stops it: a missing boot.jl, a form that raises, and a
// Core that lacks what post_boot_hooks takes from it.
func TestBootFailures(t *testing.T) {
	for _, c := range []struct {
		name, src, want string
	}{
		{"a form that raises", "x = 1\nthrow(x)\n", "boot.jl:2:"},
		{"no LoadError", "x = 1\n", "post_boot_hooks: Core.LoadError is not defined"},
		{"a LoadError that is not a type", "LoadError = 1\n", "post_boot_hooks: Core.LoadError is not a DataType"},
	} {
		vm := New()
		vm.Sources = fstest.MapFS{"boot.jl": {Data: []byte(c.src)}}
		check(t, c.name, nil, vm.Boot(), "!"+c.want)
	}
	vm := New()
	vm.Sources = fstest.MapFS{}
	if err := vm.Boot(); err == nil {
		t.Error("Boot without boot.jl")
	}

	// a Go error from an included file is not wrapped in a LoadError
	vm = New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vm.Sources = fstest.MapFS{"u.jl": {Data: []byte("import Pkg\n")}}
	_, err := vm.jlLoad([]any{vm.Core, "u.jl"})
	check(t, "a Go error in an included file", nil, err, "!not ported yet: loading the package Pkg")

	// a struct's field types before _typebody! are undefined
	st := core.NewIncompleteType("Main", "S", nil, false, false, []expr.Symbol{"x"})
	_, err = vm.kindField(st, expr.Symbol("types"))
	check(t, "types before _typebody!", nil, err, "!UndefRefError()")

	// an unprintable character beyond the BMP (julia: '\Ue0001')
	if got := Repr(expr.Char(0xf3a08081)); got != `'\Ue0001'` {
		t.Errorf("U+E0001: %s", got)
	}
}
