package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The builtins Base's first files call, each want julia 1.12.6's answer to
// the same call.
func TestBuiltins2(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval("f(x::Integer) = 1; f(x::Int) = 2; g(x::T) where {T} = T; struct P1; a::Int; end"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"Core.compilerbarrier(:type, 3)", "3"},
		{"Core._svec_len(Core.svec(1, 2))", "2"},
		{"Core._svec_ref(Core.svec(1, 2), 2)", "2"},
		{"isdefined(Core, :f)", "true"},
		{"isdefined(Core, :nope)", "false"},
		{"isdefined((1, 2), 2)", "true"},
		{"isdefined((1, 2), 3)", "false"},
		{"invoke(f, Tuple{Integer}, 1)", "1"},
		{"invoke(g, Tuple{Any}, 1)", "Int64"},
		{"applicable(f, 1)", "true"},
		{"applicable(f, 1.0)", "false"},
		{"applicable(tuple, 1)", "true"},
		{"(applicable(P1, 1), applicable(P1, :a), applicable(P1))", "(true, true, false)"},
		{"Core.current_scope()", "nothing"},
		{"try Core.compilerbarrier(:x, 1) catch e; e end", "ErrorException(\"The first argument of `compilerbarrier` must be either of `:type`, `:const` or `:conditional`.\")"},
		{"try Core._svec_ref(Core.svec(1), 2) catch e; e end", "BoundsError(svec(1), 2)"},
		{"try invoke(f, Tuple{Integer}, 1.0) catch e; e end", "!argument type error"},
		{"try invoke(f, Int, 1) catch e; e end", `TypeError(:invoke, "", Type{T} where T<:Tuple, Int64)`},
		{"try invoke(f, Tuple{AbstractFloat}, 1.0) catch e; e end", "!MethodError(f, Tuple{AbstractFloat}"},
		{"try isdefined(Core, :f, :not_atomic) catch e; e end", `ConcurrencyViolationError("isdefined: module binding cannot be accessed non-atomically")`},
		{"try isdefined((1,), 1, :monotonic) catch e; e end", `ConcurrencyViolationError("isdefined: non-atomic field cannot be accessed atomically")`},
		{"try Core.throw_methoderror(f, 1.0) catch e; e end", "!MethodError(f, (1.0,)"},
	} {
		v, err := s.Eval(c.src)
		if len(c.want) > 0 && c.want[0] == '!' {
			// julia's answer contains the world, which differs: compare a prefix
			check(t, c.src, v, err, "")
			if got := Repr(v); err == nil && !strings.Contains(got, c.want[1:]) {
				t.Errorf("%s: got %s, want it to contain %s", c.src, got, c.want[1:])
			}
			continue
		}
		check(t, c.src, v, err, c.want)
	}

	// the refusals and the cases julia reaches only through other paths
	st, _ := vm.newStruct(vm.tBounds, []any{int64(1)})
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"compilerbarrier arity", biCompilerbarrier, nil, "!compilerbarrier: too few arguments"},
		{"compilerbarrier of a non-symbol", biCompilerbarrier, []any{int64(1), int64(1)}, "!TypeError(:compilerbarrier"},
		{"_svec_len arity", biSvecLen, nil, "!_svec_len: too few arguments"},
		{"_svec_len of a non-svec", biSvecLen, []any{int64(1)}, "!TypeError(:_svec_len"},
		{"_svec_ref arity", biSvecRef, nil, "!_svec_ref: too few arguments"},
		{"_svec_ref of a non-svec", biSvecRef, []any{int64(1), int64(1)}, "!TypeError(:_svec_ref"},
		{"_svec_ref of a non-Int", biSvecRef, []any{SVec{}, "1"}, "!TypeError(:_svec_ref"},
		{"isdefined arity", biIsdefined, nil, "!isdefined: too few arguments"},
		{"isdefined of a non-symbol order", biIsdefined, []any{st, int64(1), int64(1)}, "!TypeError(:isdefined"},
		{"isdefined of a bad order", biIsdefined, []any{st, int64(1), expr.Symbol("bogus")}, "!invalid atomic ordering"},
		{"isdefined of a module's non-symbol", biIsdefined, []any{vm.Core, int64(1)}, "!TypeError(:isdefined"},
		{"isdefined of a set field", biIsdefined, []any{st, expr.Symbol("a")}, "true"},
		{"isdefined of an unset field", biIsdefined, []any{st, int64(2)}, "false"},
		{"isdefined of no field, ordered", biIsdefined, []any{st, int64(9), expr.Symbol("monotonic")}, "!atomic ordering cannot be specified for nonexistent field"},
		{"isdefined of a field by a bad key", biIsdefined, []any{st, 1.5}, "!TypeError(:isdefined"},
		{"isdefined of an AST field", biIsdefined, []any{expr.QuoteNode{Value: int64(1)}, expr.Symbol("value")}, "true"},
		{"isdefined of a field not_atomic", biIsdefined, []any{st, int64(1), expr.Symbol("not_atomic")}, "true"},
		{"throw_methoderror arity", biThrowMethoderror, nil, "!throw_methoderror: too few arguments"},
		{"current_scope arity", biCurrentScope, []any{int64(1)}, "!current_scope: too many arguments"},
		{"finalizer", biFinalizer, []any{int64(1), int64(2)}, "nothing"},
		{"finalizer arity", biFinalizer, nil, "!finalizer: too few arguments"},
		{"donotdelete", biDonotdelete, []any{int64(1)}, "nothing"},
		{"_call_in_world_total", biCallInWorldTotal, []any{uint64(1), coreValue(t, vm, "tuple"), int64(1)}, "(1,)"},
		{"_call_in_world_total arity", biCallInWorldTotal, nil, "!_call_in_world_total: too few arguments"},
		{"_call_in_world_total of a non-UInt", biCallInWorldTotal, []any{int64(1), int64(1)}, "!TypeError(:_call_in_world_total"},
		{"invoke arity", biInvoke, nil, "!invoke: too few arguments"},
		{"invoke of a non-function", biInvoke, []any{int64(1), core.EmptyTupleType}, "!not ported yet: invoke of a Int64"},
		{"applicable arity", biApplicable, nil, "!applicable: too few arguments"},
		{"applicable of a type", biApplicable, []any{vm.tBounds, int64(1)}, ""},
		{"applicable of an Int", biApplicable, []any{int64(1)}, "false"},
		{"applicable of an intrinsic", biApplicable, []any{intrinsic(t, vm, "add_int")}, "true"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}

	// a dispatch past the subtype work bound is a Go error, never an answer:
	// swept so that some bound stops the lookup and some the static
	// parameters of the actual arguments, deeper than the declared tuple
	g := coreValue(t, vm, "g")
	anyT := must(core.Apply(core.AnyTupleType, core.AnyType))
	for n := 1; n < 40; n++ {
		old := core.SetMaxSubtypeSteps(n)
		_, err1 := biInvoke(vm, []any{g, anyT, int64(1)})
		_, err2 := biApplicable(vm, []any{g, int64(1)})
		core.SetMaxSubtypeSteps(old)
		var thrown *Thrown
		for _, e := range []error{err1, err2} {
			if e != nil && errors.As(e, &thrown) {
				t.Fatalf("bound %d: a Julia exception: %v", n, e)
			}
		}
	}
	// the static parameters of the actual arguments, past the bound
	m := g.(*Function).Table.Methods(vm.World())[0]
	old := core.SetMaxSubtypeSteps(1)
	_, err := vm.invokeMethod(m, g, []any{int64(1)})
	core.SetMaxSubtypeSteps(old)
	if err == nil {
		t.Error("invokeMethod past the subtype work bound")
	}
}
