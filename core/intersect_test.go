package core

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/oracle"
	"github.com/go-embedded-julia/julia/syntax"
)

// The intersection questions of testdata/intersect-cases.jl and the product
// families below, answered by julia once (JLGO_ORACLE=1 JLGO_UPDATE_CORE=1)
// into intersect-cases.golden, as for subtyping (cases_test.go).

const (
	intersectCasesPath  = "testdata/intersect-cases.jl"
	intersectGoldenPath = "testdata/intersect-cases.golden"
)

// intersectProductLines cross productAtoms over the shapes that drive the
// intersection paths: unions against variables (intersect_union, merge_env),
// diagonal and invariant variables (intersect_var's constraint kinds),
// Varargs of a bound length (intersect_varargs), Type{} against kinds.
func intersectProductLines() []string {
	var out []string
	for _, a := range productAtoms {
		for _, b := range productAtoms {
			out = append(out,
				fmt.Sprintf("it(%s, %s)", a, b),
				fmt.Sprintf("it(Tuple{%s,%s}, Tuple{T,T} where T)", a, b),
				fmt.Sprintf("it(Ref{%s}, Ref{T} where T<:%s)", a, b),
				fmt.Sprintf("it(Tuple{Ref{T},T} where T<:%s, Tuple{Ref{%s},Any})", a, b),
				fmt.Sprintf("it(Tuple{%s,Vararg{%s}}, Tuple{Vararg{T}} where T)", a, b),
				fmt.Sprintf("it(Tuple{%s,Vararg{%s}}, Tuple{Vararg{T,N}} where N where T)", a, b),
				fmt.Sprintf("it(Pair{%s,%s}, Pair{T,T} where T)", a, b),
				fmt.Sprintf("it(Union{%s,%s}, Union{T,Ref{T}} where T)", a, b),
				fmt.Sprintf("it(Tuple{Type{%s},%s}, Tuple{Type{T},T} where T)", a, b),
				fmt.Sprintf("it(Vector{T} where T<:%s, Vector{S} where S>:%s)", a, b),
				fmt.Sprintf("tie(Tuple{%s,%s}, Tuple{T,S} where S<:T where T)", a, b),
				fmt.Sprintf("tie(Tuple{%s,Ref{%s}}, Tuple{T,Ref{T}} where T)", a, b),
				fmt.Sprintf("emp(Tuple{%s,%s}, Tuple{T,T} where T)", a, b))
		}
	}
	return out
}

func intersectCaseLines(t *testing.T) []string {
	b, err := os.ReadFile(intersectCasesPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return append(out, intersectProductLines()...)
}

// intersectPrelude defines the question forms on the julia side, printing
// each type with gs (oracle_test.go).
const intersectPrelude = `
ti(a, b) = gs(typeintersect(a, b))
it(a, b) = gs(ccall(:jl_intersect_types, Any, (Any, Any), a, b))
function tie(a, b)
    ti, env = ccall(:jl_type_intersection_with_env, Any, (Any, Any), a, b)
    string(gs(ti), join((" " * gs(v) for v in env)))
end
emp(a, b) = string(Int(ccall(:jl_has_empty_intersection, Cint, (Any, Any), a, b)))
`

// goIntersectCase answers one line as julia's prelude prints it, "E" when
// the line raises.
func goIntersectCase(line string) string {
	top, ok := syntax.ParseAll([]byte(line), "case").(*expr.Expr)
	if !ok || len(top.Args) != 2 {
		return "E"
	}
	q, ok := top.Args[1].(*expr.Expr)
	if !ok || q.Head != "call" || len(q.Args) != 3 {
		return "E"
	}
	ev := &typeEval{names: names(), vars: map[expr.Symbol]*TypeVar{}}
	a, err := ev.eval(q.Args[1])
	if err != nil {
		return "E"
	}
	b, err := ev.eval(q.Args[2])
	if err != nil {
		return "E"
	}
	switch q.Args[0] {
	case any(expr.Symbol("ti")):
		r, err := TypeIntersection(a, b)
		if err != nil {
			return "E"
		}
		return Show(r)
	case any(expr.Symbol("it")):
		r, err := IntersectTypes(a, b)
		if err != nil {
			return "E"
		}
		return Show(r)
	case any(expr.Symbol("tie")):
		r, env, _, err := TypeIntersectionEnv(a, b)
		if err != nil {
			return "E"
		}
		var s strings.Builder
		s.WriteString(Show(r))
		for _, v := range env {
			s.WriteString(" " + Show(v))
		}
		return s.String()
	case any(expr.Symbol("emp")):
		empty, err := HasEmptyIntersection(a, b)
		if err != nil {
			return "E"
		}
		if empty {
			return "1"
		}
		return "0"
	}
	return "E"
}

// The differential test of intersection: pairs drawn by typeGen from its
// own seed, answered by julia once into intersect.golden as x, y, then
// "ti | it | env" (tie and it of the prelude).
const (
	intersectSeed       = 2
	intersectPairs      = 20000
	intersectRandomPath = "testdata/intersect.golden"
)

func goIntersectAnswer(x, y any) string {
	ti, env, _, err := TypeIntersectionEnv(x, y)
	if err != nil {
		return "E"
	}
	it, err := IntersectTypes(x, y)
	if err != nil {
		return "E"
	}
	var s strings.Builder
	s.WriteString(Show(ti) + " | " + Show(it) + " |")
	for _, v := range env {
		s.WriteString(" " + Show(v))
	}
	return s.String()
}

func TestIntersectAgainstJulia(t *testing.T) {
	xs, ys := intersectXs, intersectYs
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		var prog strings.Builder
		prog.WriteString(gshow)
		// the subtyping pairs first, built as this process built them
		for i := range subtypeXs {
			fmt.Fprintf(&prog, "%s; %s\n", Show(subtypeXs[i]), Show(subtypeYs[i]))
		}
		prog.WriteString(intersectPrelude)
		prog.WriteString(`function both(x, y)
    ti, env = ccall(:jl_type_intersection_with_env, Any, (Any, Any), x, y)
    string(gs(ti), " | ", it(x, y), " |", join((" " * gs(v) for v in env)))
end
`)
		for i := range xs {
			fmt.Fprintf(&prog, "let x = %s, y = %s; print(gs(x), '\\t', gs(y), '\\t', try both(x, y) catch; \"E\" end, '\\n') end\n",
				Show(xs[i]), Show(ys[i]))
		}
		prog.WriteString("nothing\n")
		out, err := p.Call("eval", prog.String())
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		if err := os.WriteFile(intersectRandomPath, []byte(printed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gb, err := os.ReadFile(intersectRandomPath)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(string(gb), "\n"), "\n")
	if len(golden) != len(xs) {
		t.Fatalf("golden has %d pairs, the generator %d: regenerate it", len(golden), len(xs))
	}
	var bad []string
	for i, l := range golden {
		parts := strings.Split(l, "\t")
		if len(parts) != 3 {
			t.Fatalf("malformed golden line %q", l)
		}
		if knownIntersectDivergence[parts[0]+"\t"+parts[1]] {
			continue
		}
		if sx, sy := Show(xs[i]), Show(ys[i]); alphaRename(sx) != alphaRename(parts[0]) || alphaRename(sy) != alphaRename(parts[1]) {
			bad = append(bad, fmt.Sprintf("%d: built %s ∩ %s, julia's golden has %s ∩ %s", i, sx, sy, parts[0], parts[1]))
			continue
		}
		if got := goIntersectAnswer(xs[i], ys[i]); alphaRename(got) != alphaRename(parts[2]) {
			bad = append(bad, fmt.Sprintf("%d: %s ∩ %s: go %s, julia %s", i, parts[0], parts[1], got, parts[2]))
		}
	}
	for i, b := range bad {
		if i == 30 {
			t.Errorf("... and %d more", len(bad)-30)
			break
		}
		t.Error(b)
	}
	if len(bad) > 0 {
		t.Errorf("%d / %d pairs differ from julia", len(bad), len(golden))
	}
}

// knownIntersectDivergence lists the pairs Go builds differently from
// julia: the type-cache history of knownDivergence (oracle_test.go), julia
// having built Ref{Tuple{Vararg{Nothing,N}} where N} in the subtyping pairs
// before these.
var knownIntersectDivergence = map[string]bool{
	"Tuple{Array{Float64, 1}, Vararg{Ref{(Tuple{Vararg{Nothing, T3961}} where {T3961})}}}\tNumber":                           true,
	"Tuple{Ref{(Tuple{Vararg{Nothing, T3961}} where {T3961})}, Number}\t(Tuple{Ref{S16628}, S16628} where {S16628>:Signed})": true,
}

func TestIntersectCasesAgainstJulia(t *testing.T) {
	lines := intersectCaseLines(t)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		var prog strings.Builder
		prog.WriteString(gshow)
		prog.WriteString(intersectPrelude)
		for _, l := range lines {
			fmt.Fprintf(&prog, "print(try %s catch; \"E\" end, '\\n')\n", l)
		}
		prog.WriteString("nothing\n")
		out, err := p.Call("eval", prog.String())
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		ans := strings.Split(strings.TrimRight(printed, "\n"), "\n")
		if len(ans) != len(lines) {
			t.Fatalf("julia answered %d lines for %d cases", len(ans), len(lines))
		}
		var b strings.Builder
		for i, l := range lines {
			fmt.Fprintf(&b, "%s\t%s\n", l, ans[i])
		}
		if err := os.WriteFile(intersectGoldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gb, err := os.ReadFile(intersectGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(string(gb), "\n"), "\n")
	if len(golden) != len(lines) {
		t.Fatalf("golden has %d cases, the case file %d: regenerate it", len(golden), len(lines))
	}
	bad := 0
	for i, g := range golden {
		l, want, _ := strings.Cut(g, "\t")
		if l != lines[i] {
			t.Fatalf("case %d changed (%q, golden %q): regenerate it", i, lines[i], l)
		}
		if got := goIntersectCase(l); alphaRename(got) != alphaRename(want) {
			bad++
			t.Errorf("%s: go %s, julia %s", l, got, want)
		}
	}
	if bad > 0 {
		t.Errorf("%d / %d cases differ from julia", bad, len(lines))
	}
}
