package core

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// widenSer prints a type as testdata/widen.jl's ser does: variables by order
// of first appearance with their bounds, so that two types print alike when
// they are alike up to the names of their variables.
func widenSer(x any, seen map[*TypeVar]int) string {
	switch v := x.(type) {
	case *TypeVar:
		if i, ok := seen[v]; ok {
			return fmt.Sprintf("v%d", i)
		}
		i := len(seen)
		seen[v] = i
		return fmt.Sprintf("V%d[%s,%s]", i, widenSer(v.LB, seen), widenSer(v.UB, seen))
	case *UnionAll:
		return fmt.Sprintf("U(%s,%s)", widenSer(v.Var, seen), widenSer(v.Body, seen))
	case *Union:
		return fmt.Sprintf("Un(%s,%s)", widenSer(v.A, seen), widenSer(v.B, seen))
	case *Vararg:
		t, n := "-", "-"
		if v.T != nil {
			t = widenSer(v.T, seen)
		}
		if v.N != nil {
			n = widenSer(v.N, seen)
		}
		return fmt.Sprintf("Va(%s,%s)", t, n)
	case *bottomType:
		return "Bot"
	case *DataType:
		ps := make([]string, len(v.Parameters))
		for i, p := range v.Parameters {
			ps[i] = widenSer(p, seen)
		}
		return v.Name.Name + "{" + strings.Join(ps, ",") + "}"
	}
	return fmt.Sprint(x)
}

// WidenDiagonal against julia 1.12.6's jl_widen_diagonal (testdata/widen.jl),
// over the UnionAlls of testdata/widen-cases.jl and types no source writes.
func TestWidenDiagonalCases(t *testing.T) {
	golden, err := os.ReadFile("testdata/widen.golden")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	var order []string
	for _, l := range strings.Split(strings.TrimRight(string(golden), "\n"), "\n") {
		k, v, _ := strings.Cut(l, "\t")
		want[k] = v
		order = append(order, k)
	}
	widen := func(ua any) string {
		u, ok := ua.(*UnionAll)
		if !ok {
			// an unused variable is no UnionAll: julia answers the type itself
			return widenSer(ua, map[*TypeVar]int{})
		}
		return widenSer(WidenDiagonal(UnwrapUnionAll(u), u), map[*TypeVar]int{})
	}
	got := map[string]string{}
	cases, err := os.ReadFile("testdata/widen-cases.jl")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(string(cases), "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		n++
		top, ok := syntax.ParseAll([]byte(line), "case").(*expr.Expr)
		if !ok || len(top.Args) != 2 {
			got[line] = "E"
			continue
		}
		ev := &typeEval{names: names(), vars: map[expr.Symbol]*TypeVar{}}
		ua, err := ev.eval(top.Args[1])
		if err != nil {
			got[line] = "E"
			continue
		}
		got[line] = widen(ua)
	}
	if n+4 != len(order) {
		t.Fatalf("%d cases, %d golden lines: regenerate widen.golden", n, len(order))
	}
	// types no source writes
	T, S := tv("T"), tv("S")
	F, err := NewTypeVar("F", Bottom, T)
	if err != nil {
		t.Fatal(err)
	}
	ref := func(x any) any { return mustApply(RefType, x) }
	tup := func(xs ...any) any { return mustApply(AnyTupleType, xs...) }
	// julia's apply_type renames a variable bound again in a parameter, so
	// these tuples are made by hand, and answer as the renamed ones do (the
	// golden's): check_diagonal skips a variable while in its own UnionAll
	hand := func(ps ...any) any {
		return &DataType{Name: TupleTypeName, Parameters: ps, HasFreeTypeVars: true}
	}
	// troot's own variable is not skipped (subtype.c:4731-4741 starts at
	// troot->prev): the inner Ref{T} counts as an invariant occurrence, so T
	// stays diagonal-free and the type is as it was
	if g, w := widen(TypeUnionAll(T, hand(T, TypeUnionAll(T, ref(T)), T))), "Tuple{V0[Bot,Any{}],U(v0,Ref{v0}),v0}"; g != w {
		t.Errorf("#shadow: %s, want %s", g, w)
	}
	R := tv("R")
	got["#shadow2"] = widen(TypeUnionAll(S, TypeUnionAll(T, hand(T, TypeUnionAll(S, ref(S)), T, S))))
	got["#shadow3"] = widen(TypeUnionAll(R, TypeUnionAll(S, TypeUnionAll(T, hand(T, TypeUnionAll(R, ref(R)), T, R, S)))))
	// a UnionAll of a variable its body does not use (julia's UnionAll drops
	// it, so the golden is the dropped form): widening the body leaves it out
	unusedBody := tup(T, T)
	got["#unused"] = widen(TypeUnionAll(T, hand(T, T, &UnionAll{Var: S, Body: unusedBody})))
	// a Union whose first member has the occurrences, and no second one has:
	// the larger count of the two stays (subtype.c:4722-4727)
	un := &Union{A: tup(T, T), B: Int64Type}
	if g := widen(TypeUnionAll(T, hand(un, T))); !strings.HasPrefix(g, "U(") || !strings.Contains(g, "Un(Int64{},Tuple{") {
		t.Errorf("a Union of a tuple of T and an Int, with T: %s", g)
	}
	got["#free"] = widen(TypeUnionAll(T, tup(T, T, F)))
	for _, k := range order {
		if got[k] != want[k] {
			t.Errorf("%s:\n go    %s\n julia %s", k, got[k], want[k])
		}
	}
}
