package interp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// swapglobal!, modifyglobal!, replaceglobal!, setglobalonce! and
// setglobal!'s orders, each on fresh globals of a new module, each want
// julia 1.12.6's (testdata/globalops.jl): the result as (its type, its
// fields...) for a struct, and the global after.
func TestGlobalOps(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	plus, _ := s.Eval("Core.Intrinsics.add_int")
	float, _ := s.Eval("(a, b) -> 1.5")
	sym := func(n string) expr.Symbol { return expr.Symbol(n) }
	sc, mono, na := sym("sequentially_consistent"), sym("monotonic"), sym("not_atomic")
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
	noGlobal := func(name string) string {
		return "!Global GO%d." + name + " does not exist and cannot be assigned."
	}
	cv := func(msg string) string { return "!ConcurrencyViolationError(\"" + msg + "\")" }
	te := func(f, name string) string {
		return fmt.Sprintf("!TypeError(:%s, :(Main.GO%%d.%s), Int64, 1.5)", f, name)
	}
	for i, c := range []struct {
		f     string
		args  []any
		want  string
		after string
	}{
		{"swapglobal!", []any{sym("g"), int64(10)}, "1", "10"},
		{"swapglobal!", []any{sym("t"), 1.5}, te("swapglobal!", "t"), ""},
		{"swapglobal!", []any{sym("t"), int64(5)}, "2", "5"},
		{"swapglobal!", []any{sym("c"), int64(1)}, "!invalid assignment to constant GO%d.c. This redefinition may be permitted using the `const` keyword.", ""},
		{"swapglobal!", []any{sym("s"), int64(1)}, "!cannot assign a value to imported variable Intrinsics.s from module GO%d", ""},
		{"swapglobal!", []any{sym("nope"), int64(1)}, noGlobal("nope"), ""},
		{"swapglobal!", []any{sym("u"), int64(7)}, "!UndefVarError(:u,", "7"},
		{"swapglobal!", []any{sym("g"), int64(1), na}, cv("swapglobal!: module binding cannot be written non-atomically"), ""},
		{"swapglobal!", []any{sym("g"), int64(1), sym("unordered")}, cv("invalid atomic ordering"), ""},
		{"swapglobal!", []any{sym("g"), int64(1), sym("acquire")}, "1", "1"},
		{"swapglobal!", []any{sym("g"), int64(1), int64(2)}, "!TypeError(:swapglobal!, \"\", Symbol, 2)", ""},
		{"swapglobal!", []any{sym("g")}, "!swapglobal!: too few arguments (expected 3)", ""},
		{"modifyglobal!", []any{sym("g"), plus, int64(1)}, "(Pair{Any, Any}, 1, 2)", "2"},
		{"modifyglobal!", []any{sym("t"), plus, int64(1)}, "(Pair{Int64, Int64}, 2, 3)", "3"},
		{"modifyglobal!", []any{sym("t"), float, int64(1)}, te("modifyglobal!", "t"), ""},
		{"modifyglobal!", []any{sym("t"), plus, 1.5}, "!", ""},
		{"modifyglobal!", []any{sym("u"), plus, int64(1)}, "!UndefVarError(:u,", ""},
		{"modifyglobal!", []any{sym("c"), plus, int64(1)}, "!invalid assignment to constant GO%d.c.", ""},
		{"modifyglobal!", []any{sym("g"), plus, int64(1), na}, cv("modifyglobal!: module binding cannot be written non-atomically"), ""},
		{"modifyglobal!", []any{sym("g"), plus}, "!modifyglobal!: too few arguments (expected 4)", ""},
		{"replaceglobal!", []any{sym("g"), int64(1), int64(2)}, "(@NamedTuple{old, success::Bool}, 1, true)", "2"},
		{"replaceglobal!", []any{sym("g"), int64(5), int64(2)}, "(@NamedTuple{old, success::Bool}, 1, false)", "1"},
		{"replaceglobal!", []any{sym("t"), int64(2), 1.5}, te("replaceglobal!", "t"), ""},
		{"replaceglobal!", []any{sym("t"), int64(2), int64(3)}, "(@NamedTuple{old::Int64, success::Bool}, 2, true)", "3"},
		{"replaceglobal!", []any{sym("u"), int64(1), int64(2)}, "!UndefVarError(:u,", ""},
		{"replaceglobal!", []any{sym("c"), int64(1), int64(2)}, "!invalid assignment to constant GO%d.c.", ""},
		{"replaceglobal!", []any{sym("g"), int64(1), int64(2), mono, sc}, cv("invalid atomic ordering"), ""},
		{"replaceglobal!", []any{sym("g"), int64(1), int64(2), sc, na}, cv("replaceglobal!: module binding cannot be accessed non-atomically"), ""},
		{"replaceglobal!", []any{sym("g"), int64(1), int64(2), na}, cv("replaceglobal!: module binding cannot be written non-atomically"), ""},
		{"replaceglobal!", []any{sym("g"), int64(1), int64(2), sc, int64(1)}, "!TypeError(:replaceglobal!, \"\", Symbol, 1)", ""},
		{"replaceglobal!", []any{sym("g"), int64(1)}, "!replaceglobal!: too few arguments (expected 4)", ""},
		{"setglobalonce!", []any{sym("u"), int64(1)}, "true", "1"},
		{"setglobalonce!", []any{sym("g"), int64(1)}, "false", "1"},
		{"setglobalonce!", []any{sym("u2"), 1.5}, te("setglobalonce!", "u2"), ""},
		{"setglobalonce!", []any{sym("u2"), int64(1)}, "true", "1"},
		{"setglobalonce!", []any{sym("c"), int64(1)}, "!invalid assignment to constant GO%d.c.", ""},
		{"setglobalonce!", []any{sym("u"), int64(1), sc, na}, cv("setglobalonce!: module binding cannot be accessed non-atomically"), ""},
		{"setglobalonce!", []any{sym("u")}, "!setglobalonce!: too few arguments (expected 3)", ""},
		{"setglobal!", []any{sym("g"), int64(1), na}, cv("setglobal!: module binding cannot be written non-atomically"), ""},
		{"setglobal!", []any{sym("g"), int64(9), sym("release")}, "9", "9"},
		{"setglobal!", []any{sym("g"), int64(9), sym("acquire")}, cv("invalid atomic ordering"), ""},
	} {
		ms := vm.sessionFor(vm.NewModule(expr.Symbol(fmt.Sprintf("GO%d", i))))
		if _, err := ms.Eval("g = 1; t::Int = 2; const c = 3; global u; global u2::Int; import Core.Intrinsics: add_int as s"); err != nil {
			t.Fatal(err)
		}
		want := c.want
		if strings.Contains(want, "%d") {
			want = fmt.Sprintf(want, i)
		}
		name := c.f + showValue(vm.newTuple(c.args))
		v, err := coreValue(t, vm, expr.Symbol(c.f)).(*Builtin).Fn(vm, append([]any{ms.Mod}, c.args...))
		if err != nil || strings.HasPrefix(want, "!") {
			check(t, name, v, err, want)
		} else if got := parts(v); got != want {
			t.Errorf("%s: got %s, want %s", name, got, want)
		}
		if c.after != "" {
			b := ms.Mod.own(c.args[0].(expr.Symbol))
			if got := showValue(b.Value); got != c.after {
				t.Errorf("%s: after %s, want %s", name, got, c.after)
			}
		}
	}
	// how julia prints a GlobalRef
	for ref, want := range map[expr.GlobalRef]string{
		{Mod: "Main", Name: "x"}: ":(Main.x)", {Mod: "Core", Name: "Int"}: ":(Core.Int)",
		{Mod: "GO1", Name: "t"}: ":(Main.GO1.t)", {Mod: "Main", Name: "a b"}: ":(Main.:(var\"a b\"))",
	} {
		if got := showValue(ref); got != want {
			t.Errorf("%v: %s, want %s", ref, got, want)
		}
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{int64(1), sym("x"), int64(1)}, "!TypeError(:swapglobal!, \"\", Module, 1)"},
		{[]any{vm.Main, int64(1), int64(1)}, "!TypeError(:swapglobal!, \"\", Symbol, 1)"},
	} {
		v, err := biSwapglobal(vm, c.args)
		check(t, "swapglobal!", v, err, c.want)
	}
}
