package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// @cfunction's form evaluates to a Ptr{Cvoid} holding the callable and its
// signature; the function is the quoted expression, evaluated in the module
// of the code, or a value; what does not fit is refused.
func TestCfunctionForm(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`double(x::Int) = Core.Intrinsics.mul_int(2, x)`); err != nil {
		t.Fatal(err)
	}
	pv := vm.ptrType(core.NothingType)
	q := func(v any) expr.QuoteNode { return expr.QuoteNode{Value: v} }
	cc := q(expr.Symbol("ccall"))
	form := func(typ, fn, rt, at any) []any {
		return []any{expr.New("cfunction", typ, fn, rt, at, cc), ret(expr.SSAValue(1))}
	}
	argt := q(SVec{core.Int64Type})
	bad := call(gr("Core", "throw"), int64(1))
	for _, c := range []struct {
		name string
		code []any
		want string
	}{
		{"a function by its name", form(q(pv), q(expr.Symbol("double")), q(core.Int64Type), argt), "Ptr{Nothing}()"},
		{"a function by an expression", form(q(pv), q(expr.New("call", gr("Core", "tuple"))), q(core.Int64Type), argt), "Ptr{Nothing}()"},
		{"a function value", form(q(pv), q(&Builtin{Name: "f"}), q(core.Int64Type), argt), "Ptr{Nothing}()"},
		{"a name that is not defined", form(q(pv), q(expr.Symbol("nope")), q(core.Int64Type), argt), "!UndefVarError(:nope"},
		{"not Ptr{Cvoid}", form(q(core.Int64Type), q(expr.Symbol("double")), q(core.Int64Type), argt), "!not ported yet: a cfunction of type Int64"},
		{"argument types that are no svec", form(q(pv), q(expr.Symbol("double")), q(core.Int64Type), q(int64(1))), "!cfunction: argument types must be a tuple of types"},
		{"a type that raises", form(bad, q(expr.Symbol("double")), q(core.Int64Type), argt), "!julia exception: 1"},
		{"a function that raises", form(q(pv), bad, q(core.Int64Type), argt), "!julia exception: 1"},
		{"a return type that raises", form(q(pv), q(expr.Symbol("double")), bad, argt), "!julia exception: 1"},
		{"argument types that raise", form(q(pv), q(expr.Symbol("double")), q(core.Int64Type), bad), "!julia exception: 1"},
		{"too few operands", []any{expr.New("cfunction", q(pv)), ret(nil)}, "!Invalid IR: malformed :cfunction"},
		{"another calling convention", []any{expr.New("cfunction", q(pv), q(expr.Symbol("double")), q(core.Int64Type), argt, q(expr.Symbol("stdcall"))), ret(nil)},
			"!not ported yet: a :cfunction with a calling convention other than :ccall"},
		{"a calling convention that is no QuoteNode", []any{expr.New("cfunction", q(pv), q(expr.Symbol("double")), q(core.Int64Type), argt, int64(1)), ret(nil)},
			"!not ported yet: a :cfunction with a calling convention other than :ccall"},
	} {
		v, err := runIR(s, codeInfo(c.code), false)
		check(t, c.name, v, err, c.want)
	}
	// the pointer calls the function, and checks the arguments against the signature
	pv1, err := runIR(s, codeInfo(form(q(pv), q(expr.Symbol("double")), q(core.Int64Type), argt)), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		p    any
		args []any
		want string
	}{
		{"a call", pv1, []any{int64(21)}, "42"},
		{"too many arguments", pv1, []any{int64(1), int64(2)}, "!2 arguments for a signature of 1"},
		{"an argument of another type", pv1, []any{"x"}, `!TypeError(:cfunction, "", Int64, "x")`},
		{"an offset pointer", &Pointer{Type: pv, Target: pv1.(*Pointer).Target, Off: 1}, []any{int64(1)}, "!not a cfunction pointer"},
		{"a pointer to something else", vm.voidPtr("x"), nil, "!not a cfunction pointer"},
		{"no pointer", int64(1), nil, "!not a cfunction pointer"},
	} {
		v, err := vm.callCFunction(c.p, c.args...)
		check(t, c.name, v, err, c.want)
	}
}
