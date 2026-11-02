package interp

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// swapfield!, modifyfield!, replacefield! and setfieldonce!, each case on a
// fresh F, each want julia 1.12.6's (testdata/fieldops.jl): the result as
// (its type, its fields...) for a struct, and the field after.
func TestFieldOps(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	sym := func(n string) expr.Symbol { return expr.Symbol(n) }
	// mutable struct F; const c::Int; @atomic a::Int; x::Int; y; u::String; F() = new(1, 2, 3, 4); end
	def := expr.New("struct", true, sym("F"), expr.New("block",
		expr.New("const", expr.New("::", sym("c"), sym("Int"))),
		expr.New("atomic", expr.New("::", sym("a"), sym("Int"))),
		expr.New("::", sym("x"), sym("Int")),
		sym("y"),
		expr.New("::", sym("u"), sym("String")),
		expr.New("=", expr.New("call", sym("F")), expr.New("call", sym("new"), int64(1), int64(2), int64(3), int64(4)))))
	if _, err := s.evalForm(def); err != nil {
		t.Fatal(err)
	}
	plus, _ := s.Eval("Core.Intrinsics.add_int")
	mul, _ := s.Eval("(a, b) -> Core.tuple(a, b)")
	float, _ := s.Eval("(a, b) -> 1.5")
	fail, _ := s.Eval("(a, b) -> Core.throw(Core.ErrorException(\"op\"))")
	parts := func(v any) string {
		if st, ok := v.(*Struct); ok {
			fs := []string{showValue(st.Type)}
			for _, f := range st.Fields {
				fs = append(fs, showValue(f))
			}
			return "(" + strings.Join(fs, ", ") + ")"
		}
		return showValue(v)
	}
	sc, mono := sym("sequentially_consistent"), sym("monotonic")
	for _, c := range []struct {
		f     string
		args  []any
		want  string
		after string
	}{
		{"swapfield!", []any{sym("x"), int64(10)}, "3", "10"},
		{"swapfield!", []any{sym("x"), 1.5}, "!TypeError(:swapfield!, \"\", Int64, 1.5)", ""},
		{"swapfield!", []any{sym("c"), int64(1)}, "!swapfield!: const field .c of type F cannot be changed", ""},
		{"swapfield!", []any{sym("a"), int64(5)}, "!ConcurrencyViolationError(\"swapfield!: atomic field cannot be written non-atomically\")", ""},
		{"swapfield!", []any{sym("a"), int64(5), sc}, "2", "5"},
		{"swapfield!", []any{sym("x"), int64(5), sym("acquire")}, "!ConcurrencyViolationError(\"swapfield!: non-atomic field cannot be written atomically\")", ""},
		{"swapfield!", []any{sym("x"), int64(5), sym("unordered")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", ""},
		{"swapfield!", []any{sym("u"), "s"}, "!UndefRefError()", "\"s\""},
		{"swapfield!", []any{sym("zz"), int64(1)}, "!FieldError(Main.L.F, :zz)", ""},
		{"swapfield!", []any{int64(9), int64(1)}, "!BoundsError(", ""},
		{"swapfield!", []any{sym("x"), int64(1), int64(2)}, "!TypeError(:swapfield!, \"\", Symbol, 2)", ""},
		{"swapfield!", []any{sym("x")}, "!swapfield!: too few arguments (expected 3)", ""},
		{"swapfield!", []any{1.0, int64(1)}, "!TypeError(:swapfield!, \"\", Union{Int64, Symbol}, 1.0)", ""},
		{"modifyfield!", []any{sym("x"), plus, int64(1)}, "(Pair{Int64, Int64}, 3, 4)", "4"},
		{"modifyfield!", []any{sym("x"), float, int64(1)}, "!TypeError(:modifyfield!, \"\", Int64, 1.5)", ""},
		{"modifyfield!", []any{sym("y"), mul, int64(1)}, "(Pair{Any, Any}, 4, (4, 1))", "(4, 1)"},
		{"modifyfield!", []any{sym("u"), mul, "s"}, "!UndefRefError()", ""},
		{"modifyfield!", []any{sym("a"), plus, int64(1), mono}, "(Pair{Int64, Int64}, 2, 3)", "3"},
		{"modifyfield!", []any{sym("a"), plus, int64(1)}, "!ConcurrencyViolationError(\"modifyfield!: atomic field cannot be written non-atomically\")", ""},
		{"modifyfield!", []any{sym("x"), fail, int64(1)}, "!op", "3"},
		{"modifyfield!", []any{sym("x"), plus}, "!modifyfield!: too few arguments (expected 4)", ""},
		{"replacefield!", []any{sym("x"), int64(3), int64(7)}, "(@NamedTuple{old::Int64, success::Bool}, 3, true)", "7"},
		{"replacefield!", []any{sym("x"), int64(4), int64(7)}, "(@NamedTuple{old::Int64, success::Bool}, 3, false)", "3"},
		{"replacefield!", []any{sym("x"), int64(3), 1.5}, "!TypeError(:replacefield!, \"\", Int64, 1.5)", ""},
		{"replacefield!", []any{sym("a"), int64(2), int64(3), sc, mono}, "(@NamedTuple{old::Int64, success::Bool}, 2, true)", "3"},
		{"replacefield!", []any{sym("a"), int64(2), int64(3), mono, sc}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", ""},
		{"replacefield!", []any{sym("x"), int64(3), int64(7), sym("not_atomic"), sym("acquire")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", ""},
		{"replacefield!", []any{sym("a"), int64(2), int64(3), sc, sym("not_atomic")}, "!ConcurrencyViolationError(\"replacefield!: atomic field cannot be accessed non-atomically\")", ""},
		{"replacefield!", []any{sym("a"), int64(2), int64(3), sym("release"), sym("release")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", ""},
		{"replacefield!", []any{sym("a"), int64(2), int64(3), sc}, "(@NamedTuple{old::Int64, success::Bool}, 2, true)", "3"},
		{"replacefield!", []any{sym("u"), "a", "b"}, "!UndefRefError()", ""},
		{"replacefield!", []any{sym("y"), int64(4), int64(5)}, "(@NamedTuple{old, success::Bool}, 4, true)", "5"},
		{"replacefield!", []any{sym("y"), 4.0, int64(5)}, "(@NamedTuple{old, success::Bool}, 4, false)", "4"},
		{"replacefield!", []any{sym("x"), int64(3)}, "!replacefield!: too few arguments (expected 4)", ""},
		{"setfieldonce!", []any{sym("u"), "s"}, "true", "\"s\""},
		{"setfieldonce!", []any{sym("x"), int64(1)}, "false", "3"},
		{"setfieldonce!", []any{sym("u"), int64(1)}, "!TypeError(:setfieldonce!, \"\", String, 1)", ""},
		{"setfieldonce!", []any{sym("a"), int64(1), sc}, "false", "2"},
		{"setfieldonce!", []any{sym("a"), int64(1)}, "!ConcurrencyViolationError(\"setfieldonce!: atomic field cannot be written non-atomically\")", ""},
		{"setfieldonce!", []any{sym("u"), "s", sym("not_atomic"), sym("acquire")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", ""},
		{"setfieldonce!", []any{sym("u")}, "!setfieldonce!: too few arguments (expected 3)", ""},
	} {
		o, err := s.Eval("F()")
		if err != nil {
			t.Fatal(err)
		}
		name := c.f + showValue(vm.newTuple(c.args))
		v, err := coreValue(t, vm, expr.Symbol(c.f)).(*Builtin).Fn(vm, append([]any{o}, c.args...))
		if err != nil {
			check(t, name, v, err, c.want)
		} else if got := parts(v); got != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
		if c.after != "" {
			if got := showValue(o.(*Struct).Fields[map[expr.Symbol]int{"a": 1, "x": 2, "y": 3, "u": 4}[c.args[0].(expr.Symbol)]]); got != c.after {
				t.Errorf("%s: the field after is %s, want %s", name, got, c.after)
			}
		}
	}

	// the value itself: immutable, a module, a value without fields
	o, _ := s.Eval("F()")
	ex, _ := s.Eval(":(a + b)")
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"swapfield!", []any{vm.newTuple([]any{int64(1), int64(2)}), int64(1), int64(3)}, "!swapfield!: immutable struct of type Tuple cannot be changed"},
		{"swapfield!", []any{vm.Main, sym("x"), int64(1)}, "!cannot assign variables in other modules"},
		{"modifyfield!", []any{sym("sym"), int64(1), plus, int64(1)}, "!BoundsError(:sym, 1)"},
		// an Expr's fields are Go's: written by setfield! only
		{"swapfield!", []any{ex, sym("head"), sym("call")}, "!not ported yet: swapfield! of a Expr's head"},
		{"setfieldonce!", []any{o, sym("u"), "s"}, "true"},
		{"setfieldonce!", []any{o, sym("u"), "t"}, "false"},
	} {
		v, err := coreValue(t, vm, expr.Symbol(c.f)).(*Builtin).Fn(vm, c.args)
		check(t, c.f, v, err, c.want)
	}

	// before boot.jl there is no Pair to return (julia's jl_pair_type is
	// NULL then)
	pre := New()
	ps := pre.NewSession()
	p, err := ps.Eval("mutable struct P; x; end; P(1)")
	if err != nil {
		t.Fatal(err)
	}
	v, err := biModifyfield(pre, []any{p, sym("x"), coreValue(t, pre, "tuple"), int64(2)})
	check(t, "modifyfield! before boot", v, err, "!modifyfield!: Core.Pair is not defined before boot.jl")
}
