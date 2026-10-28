package core

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The differential test of subtyping: pairs drawn by typeGen from a fixed
// seed, answered by julia 1.12.6 once (JLGO_ORACLE=1 JLGO_UPDATE_CORE=1)
// and replayed from core/testdata/subtype.golden by every run.

const (
	goldenSeed  = 1
	goldenPairs = 20000
	goldenPath  = "testdata/subtype.golden"
)

type goldenCase struct{ x, y, want string }

func drawPairs(n int) ([]any, []any) {
	g := newTypeGen(goldenSeed)
	xs, ys := make([]any, n), make([]any, n)
	for i := range xs {
		xs[i], ys[i] = g.pair()
	}
	return xs, ys
}

// gshow is Show written in Julia: julia prints the type it built from the
// text Show gave it, in Show's format, so that a type julia normalises
// differently (union order, tuple and wrapper normalisation) shows up as a
// different line, not only a different answer.
const gshow = `
const QUALIFIED = (:TypeofBottom, :TypeofVararg, :TypeName, :SimpleVector, :BFloat16)
gs(t) = sprint(gs, t)
function gs(io::IO, t)
    if t === Union{}
        print(io, "Union{}")
    elseif t isa DataType
        n = t.name
        n.module === Core && n.name in QUALIFIED && print(io, "Core.")
        print(io, n.name)
        ps = t.parameters
        if n === Tuple.name && isempty(ps)
            print(io, "{}")
        elseif !isempty(ps) && t !== Tuple
            print(io, '{'); join(io, map(gs, ps), ", "); print(io, '}')
        end
    elseif t isa Union
        print(io, "Union{"); join(io, map(gs, Base.uniontypes(t)), ", "); print(io, '}')
    elseif t isa UnionAll
        v = t.var
        print(io, '('); gs(io, t.body); print(io, " where {")
        hl, hu = v.lb !== Union{}, v.ub !== Any
        hl && hu && (gs(io, v.lb); print(io, "<:"))
        print(io, v.name)
        if hu
            print(io, "<:"); gs(io, v.ub)
        elseif hl
            print(io, ">:"); gs(io, v.lb)
        end
        print(io, "})")
    elseif t isa TypeVar
        print(io, t.name)
    elseif t isa Core.TypeofVararg
        print(io, "Vararg")
        if isdefined(t, :T)
            print(io, '{'); gs(io, t.T)
            isdefined(t, :N) && (print(io, ", "); gs(io, t.N))
            print(io, '}')
        end
    else
        print(io, t)
    end
end
`

// askJulia answers, for every pair, x <: y ("1", "0", or "E" when julia
// raised), and julia's own rendering of x and y.
func askJulia(t *testing.T, xs, ys []any) []goldenCase {
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var prog strings.Builder
	prog.WriteString(gshow)
	for i := range xs {
		fmt.Fprintf(&prog, "let x = %s, y = %s; print(gs(x), '\\t', gs(y), '\\t', try Int(x <: y) catch; \"E\" end, ' ', try Int(Base.morespecific(x, y)) catch; \"E\" end, '\\n') end\n",
			Show(xs[i]), Show(ys[i]))
	}
	prog.WriteString("nothing\n")
	out, err := p.Call("eval", prog.String())
	if err != nil {
		t.Fatal(err)
	}
	printed, _, _ := strings.Cut(out, "\n=> ")
	lines := strings.Split(strings.TrimRight(printed, "\n"), "\n")
	if len(lines) != len(xs) {
		t.Fatalf("julia answered %d lines for %d pairs", len(lines), len(xs))
	}
	cs := make([]goldenCase, len(lines))
	for i, l := range lines {
		parts := strings.Split(l, "\t")
		if len(parts) != 3 {
			t.Fatalf("julia's line %d is malformed: %q", i, l)
		}
		cs[i] = goldenCase{parts[0], parts[1], parts[2]}
	}
	return cs
}

var (
	whereVar = regexp.MustCompile(`where \{(?:[^{}<>]*<:)?([A-Za-z_][A-Za-z0-9_]*)`)
	ident    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// alphaRename renames the variables bound by where clauses to V1, V2 … in
// order of first appearance. julia's Type{T} cache returns an equal type
// built earlier by Base when one exists (typekey_eq, jltypes.c:960-968), so
// `Type{Array{T9,1} where T9}` comes back as Base's `Type{Vector}`, whose
// variable is named T: equal, and named differently.
func alphaRename(s string) string {
	bound := map[string]bool{}
	for _, m := range whereVar.FindAllStringSubmatch(s, -1) {
		bound[m[1]] = true
	}
	names := map[string]string{}
	return ident.ReplaceAllStringFunc(s, func(w string) string {
		if !bound[w] {
			return w
		}
		if n, ok := names[w]; ok {
			return n
		}
		n := fmt.Sprintf("V%d", len(names)+1)
		names[w] = n
		return n
	})
}

func readGolden(t *testing.T) []goldenCase {
	f, err := os.Open(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cs []goldenCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		parts := strings.Split(sc.Text(), "\t")
		if len(parts) != 3 {
			t.Fatalf("malformed golden line %q", sc.Text())
		}
		cs = append(cs, goldenCase{parts[0], parts[1], parts[2]})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cs
}

// goMoreSpecific answers Base.morespecific(x, y) as the golden records it.
func goMoreSpecific(x, y any) string {
	more, err := MoreSpecific(x, y)
	switch {
	case err != nil:
		return "E"
	case more:
		return "1"
	}
	return "0"
}

func goAnswer(x, y any) string {
	sub, err := SubtypeChecked(x, y)
	switch {
	case err != nil:
		return "E"
	case sub:
		return "1"
	}
	return "0"
}

func TestSubtypeAgainstJulia(t *testing.T) {
	xs, ys := drawPairs(goldenPairs)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		var b strings.Builder
		for _, c := range askJulia(t, xs, ys) {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", c.x, c.y, c.want)
		}
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cs := readGolden(t)
	if len(cs) != len(xs) {
		t.Fatalf("golden has %d cases, the generator %d: regenerate it", len(cs), len(xs))
	}
	var bad []string
	counts := map[string]int{}
	for i, c := range cs {
		// a drift is a difference too: building a union calls subtyping
		// (jl_type_union, jltypes.c:643-690), so a subtyping defect shows up
		// first as a different type built from the same draws
		if sx, sy := Show(xs[i]), Show(ys[i]); (alphaRename(sx) != alphaRename(c.x) || alphaRename(sy) != alphaRename(c.y)) &&
			!knownDivergence[c.x+"\t"+c.y] {
			bad = append(bad, fmt.Sprintf("%d: built %s <: %s, julia's golden has %s <: %s", i, sx, sy, c.x, c.y))
			continue
		}
		counts[c.want[:1]]++
		if got := goAnswer(xs[i], ys[i]) + " " + goMoreSpecific(xs[i], ys[i]); got != c.want && !knownDivergence[c.x+"\t"+c.y] {
			bad = append(bad, fmt.Sprintf("%d: %s <: %s: go %s, julia %s", i, c.x, c.y, got, c.want))
		}
	}
	t.Logf("%d pairs: julia true %d, false %d, error %d; %d known divergences",
		len(cs), counts["1"], counts["0"], counts["E"], len(knownDivergence))
	for i, b := range bad {
		if i == 30 {
			t.Errorf("... and %d more", len(bad)-30)
			break
		}
		t.Error(b)
	}
	if len(bad) > 0 {
		t.Errorf("%d / %d pairs differ from julia", len(bad), len(cs))
	}
}

// knownDivergence lists the pairs where Go is known to differ from julia,
// each with its cause.
var knownDivergence = map[string]bool{
	// Type-cache history, not subtyping: a non-concrete parameter is matched
	// in the cache by type equality (typekey_eq, jltypes.c:980-983), so
	// Ref{Tuple{Vararg{Nothing}}} returns whichever equal Ref was built
	// first. julia had built Ref{Tuple{Vararg{Nothing,N}} where N} earlier in
	// its session; this Go process built the pairs in another order. The
	// answer to <: is the same.
	"Tuple{Ref{(Tuple{Vararg{Nothing, T3961}} where {T3961})}, Tuple{Vararg{Nothing}}}\t(Tuple{Ref{S16520}, S16520} where {S16520})": true,
}
