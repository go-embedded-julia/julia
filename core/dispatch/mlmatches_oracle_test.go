package dispatch

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The differential test of ml_matches: random method tables (with a few
// written by hand) and queries, answered by julia 1.12.6's
// Base._methods_by_ftype once (JLGO_ORACLE=1 JLGO_UPDATE_CORE=1) into
// testdata/mlmatches.golden and replayed. A match is "line:fully_covers",
// its list is followed by "|" and the has_ambig flag.
//
// julia's typemap caches are hash tables keyed by type name, and a type
// name's hash includes its module's build_id, random for a module made at
// run time (datatype.c:84, module.c:512): the order of two methods that
// sit in different slots of one cache is not reproducible, here or in
// another julia. The generator therefore draws its types from a module it
// makes anew for each of mlReplicas runs, and a case whose answers differ
// between runs is marked unstable in the golden and not replayed; the
// Core types it draws are one per cache (Int64 in arg1, Integer in name1),
// whose hashes are fixed.

const (
	mlSeed     = 11
	mlCases    = 700
	mlReplicas = 12
	mlGolden   = "testdata/mlmatches.golden"
	mlPrelude  = `
abstract type A end
abstract type B <: A end
abstract type C <: A end
abstract type D <: B end
struct S1 <: B end
struct S2 <: C end
struct S3 <: D end
struct S4 <: A end
function f end
`
)

// mlVocab is the types of the prelude.
type mlVocab struct {
	head                       *core.DataType
	a, b, c, d, s1, s2, s3, s4 *core.DataType
}

var (
	mlVocabOnce sync.Once
	mlV         *mlVocab
)

func mlTypes(t testing.TB) *mlVocab {
	mlVocabOnce.Do(func() {
		mk := func(name string, super *core.DataType, abstract bool) *core.DataType {
			d, err := core.NewType("Main", name, super, abstract, false, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			return d
		}
		v := &mlVocab{}
		v.head = mk("typeof(f)", core.FunctionType, false)
		v.a = mk("A", nil, true)
		v.b = mk("B", v.a, true)
		v.c = mk("C", v.a, true)
		v.d = mk("D", v.b, true)
		v.s1 = mk("S1", v.b, false)
		v.s2 = mk("S2", v.c, false)
		v.s3 = mk("S3", v.d, false)
		v.s4 = mk("S4", v.a, false)
		mlV = v
	})
	return mlV
}

type mlQuery struct {
	tt   any
	lim  int
	incl bool
}

type mlCase struct {
	sigs    []any
	queries []mlQuery
}

type mlGen struct {
	r *rand.Rand
	t testing.TB
	v *mlVocab
}

func (g *mlGen) tuple(ps ...any) any { return mustApply(g.t, core.AnyTupleType, ps...) }

func (g *mlGen) typeOf(x any) any { return mustApply(g.t, core.TypeType, x) }

// arg draws a signature's argument; vars are the static parameters in scope.
func (g *mlGen) arg(vars []*core.TypeVar) any {
	v := g.v
	atoms := []any{v.a, v.b, v.c, v.d, v.s1, v.s2, v.s3, v.s4, core.Int64Type, core.IntegerType, core.AnyType,
		core.TypeUnion(v.s1, v.s2), core.TypeUnion(v.b, core.Int64Type), core.TypeUnion(v.s3, v.s4),
		g.typeOf(v.s1), g.typeOf(v.a), g.typeOf(v.b), g.typeOf(core.Int64Type), core.TypeType, g.typeOf(v.s4)}
	switch k := g.r.IntN(12); {
	case k < 7 || len(vars) == 0:
		return atoms[g.r.IntN(len(atoms))]
	case k < 9:
		return vars[g.r.IntN(len(vars))]
	case k < 11:
		return g.typeOf(vars[g.r.IntN(len(vars))])
	default:
		return core.TypeUnion(vars[g.r.IntN(len(vars))], core.Int64Type)
	}
}

func (g *mlGen) sig() any {
	v := g.v
	var vars []*core.TypeVar
	for i := 0; i < g.r.IntN(3); i++ {
		tv := &core.TypeVar{Name: fmt.Sprintf("T%d", i), LB: core.Bottom, UB: core.AnyType}
		if g.r.IntN(2) == 0 {
			tv.UB = []any{v.a, v.b, v.c, core.IntegerType}[g.r.IntN(4)]
		}
		vars = append(vars, tv)
	}
	n := 1 + g.r.IntN(3)
	ps := []any{}
	for i := 0; i < n; i++ {
		ps = append(ps, g.arg(vars))
	}
	if g.r.IntN(5) == 0 {
		ps = append(ps, &core.Vararg{T: g.arg(vars)})
	}
	t, err := core.Apply(core.AnyTupleType, ps...)
	if err != nil {
		return nil
	}
	for i := len(vars) - 1; i >= 0; i-- {
		t = core.TypeUnionAll(vars[i], t)
	}
	return t
}

// query draws a call or an abstract query (typeof(f) first).
func (g *mlGen) query() mlQuery {
	v := g.v
	conc := []any{v.s1, v.s2, v.s3, v.s4, core.Int64Type}
	abst := []any{v.a, v.b, v.c, v.d, core.IntegerType, core.AnyType, g.typeOf(v.s1), g.typeOf(v.a), g.typeOf(v.b), core.TypeType, core.TypeUnion(v.s1, v.s2)}
	n := g.r.IntN(4)
	ps := []any{v.head}
	if g.r.IntN(3) == 0 {
		for i := 0; i < n; i++ {
			ps = append(ps, conc[g.r.IntN(len(conc))])
		}
	} else {
		for i := 0; i < n; i++ {
			ps = append(ps, abst[g.r.IntN(len(abst))])
		}
		if g.r.IntN(3) == 0 {
			ps = append(ps, &core.Vararg{T: abst[g.r.IntN(len(abst))]})
		}
	}
	return mlQuery{tt: g.tuple(ps...), lim: []int{-1, -1, -1, -1, 1, 2, 3}[g.r.IntN(7)], incl: g.r.IntN(3) == 0}
}

// handCases are tables written by hand, over Core types whose caches hold
// one key per slot, or the order of whose methods does not come from a hash.
func (g *mlGen) handCases() []mlCase {
	v := g.v
	I, Int, Any := core.IntegerType, core.Int64Type, core.AnyType
	sym, mod, str := core.SymbolType, core.ModuleType, core.StringType
	tvT := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	typeT := core.TypeUnionAll(tvT, g.tuple(g.typeOf(tvT), sym))
	// f(::Module, ::Symbol), f(::Type{T} where T, ::Symbol): disjoint, one in
	// arg1 and one in tname
	probe := []any{g.tuple(mod, sym), typeT, g.tuple(Any, sym)}
	va := func(t any) *core.Vararg { return &core.Vararg{T: t} }
	q := func(lim int, incl bool, ps ...any) mlQuery {
		return mlQuery{tt: g.tuple(append([]any{v.head}, ps...)...), lim: lim, incl: incl}
	}
	return []mlCase{
		{sigs: probe, queries: []mlQuery{q(-1, false, va(Any)), q(-1, false, Any, sym), q(-1, false, mod, sym), q(-1, true, Any, sym),
			q(-1, false, g.typeOf(Int), sym), q(-1, false, core.TypeType, sym), q(-1, false, core.DataTypeType, sym)}},
		{sigs: []any{g.tuple(mod), g.tuple(g.typeOf(v.s1)), g.tuple(core.TypeType), g.tuple(Any)},
			queries: []mlQuery{q(-1, false, va(Any)), q(-1, false, Any), q(-1, true, Any), q(-1, false, g.typeOf(v.s1)), q(-1, false, core.DataTypeType)}},
		{sigs: []any{g.tuple(Int, va(Int)), g.tuple(I, va(Any)), g.tuple(va(Any)), g.tuple(Int, I), g.tuple(Any, Int)},
			queries: []mlQuery{q(-1, false, va(Any)), q(-1, true, va(Any)), q(-1, false, Int, Int), q(-1, false, Int, Int, Int), q(-1, false), q(2, false, va(Any)), q(-1, false, I, Int)}},
		{sigs: []any{g.tuple(core.TypeUnion(Int, str)), g.tuple(core.TypeUnion(Int, sym)), g.tuple(Int), g.tuple(Any)},
			queries: []mlQuery{q(-1, false, va(Any)), q(-1, false, Int), q(-1, false, str), q(-1, true, Int), q(-1, false, core.TypeUnion(Int, str))}},
		{sigs: []any{g.tuple(Int, Any), g.tuple(Any, Int)},
			queries: []mlQuery{q(-1, false, Int, Int), q(-1, true, Int, Int), q(-1, false, va(Any)), q(1, true, Int, Int), q(-1, false, Int, Any)}},
		{sigs: []any{g.tuple(v.s1, Any), g.tuple(Any, v.s1), g.tuple(v.s1, v.s1), g.tuple(v.s2, Any), g.tuple(Any, v.s2), g.tuple(Any, Any)},
			queries: []mlQuery{q(-1, false, va(Any)), q(-1, false, v.s1, v.s1), q(-1, true, v.s1, v.s1), q(-1, false, v.s2, v.s2), q(-1, false, v.b, v.b), q(-1, true, va(Any))}},
		{sigs: []any{g.tuple(g.typeOf(v.s1), Int), g.tuple(g.typeOf(v.a), Int), typeT, g.tuple(core.TypeType, Any), g.tuple(Any, Any)},
			queries: []mlQuery{q(-1, false, va(Any)), q(-1, false, g.typeOf(v.s1), Int), q(-1, false, g.typeOf(v.s2), Int), q(-1, false, g.typeOf(core.Bottom), sym), q(-1, false, core.TypeType, Int)}},
	}
}

// mlHasEqual reports a type-equal signature among sigs. A method that
// replaces another leaves the replaced entry in julia's typemap, where it
// still counts towards the list-to-cache conversion (typemap.c:1370) and,
// measured against julia 1.12.6, puts the replacer where the replaced
// method stood in ml_matches' order once the list is converted: a rule
// this port does not reproduce, so the random tables avoid replacements.
func mlHasEqual(sigs []any, s any) bool {
	for _, o := range sigs {
		if core.TypesEqual(o, s) {
			return true
		}
	}
	return false
}

func drawML(t testing.TB) []mlCase {
	g := &mlGen{r: rand.New(rand.NewPCG(mlSeed, 1)), t: t, v: mlTypes(t)}
	cs := g.handCases()
	for len(cs) < mlCases {
		var c mlCase
		for n := 2 + g.r.IntN(8); len(c.sigs) < n; {
			if s := g.sig(); s != nil && !mlHasEqual(c.sigs, s) {
				c.sigs = append(c.sigs, s)
			}
		}
		for j := 0; j < 5; j++ {
			c.queries = append(c.queries, g.query())
		}
		cs = append(cs, c)
	}
	return cs
}

// goMLAnswers answers each query of a case as julia's text does.
func goMLAnswers(t testing.TB, c mlCase) string {
	v := mlTypes(t)
	tab := &Table{Name: "f", Head: v.head}
	for _, s := range c.sigs {
		if _, err := tab.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	var out []string
	for _, q := range c.queries {
		res, err := tab.MlMatches(q.tt, q.lim, q.incl, tab.World(), 0, ^uint64(0))
		switch {
		case err != nil:
			out = append(out, "E")
		case res.Nothing:
			out = append(out, "nothing")
		default:
			var ms []string
			for _, m := range res.List {
				fc := 0
				if m.FullyCovers {
					fc = 1
				}
				ms = append(ms, fmt.Sprintf("%d:%d", m.Method.Index+1, fc))
			}
			amb := 0
			if res.Ambig {
				amb = 1
			}
			out = append(out, strings.Join(ms, ",")+"|"+fmt.Sprint(amb))
		}
	}
	return strings.Join(out, answerSep)
}

// juliaMLProgram answers each case mlReplicas times, each in a module of its
// own.
func juliaMLProgram(cs []mlCase) string {
	var b strings.Builder
	fmt.Fprintf(&b, "const PRELUDE = %q\n", mlPrelude)
	b.WriteString(`
function answer(m, qs)
    out = String[]
    for (T, lim, incl) in qs
        try
            sig = Core.eval(m, T)
            ha = Ref{Int32}(0)
            ms = Base._methods_by_ftype(sig, nothing, lim, Base.get_world_counter(), incl, Ref(UInt(0)), Ref(typemax(UInt)), ha)
            push!(out, ms === nothing ? "nothing" :
                join((string(x.method.line, ":", x.fully_covers ? 1 : 0) for x in ms), ",") * "|" * string(ha[]))
        catch e
            push!(out, "E")
        end
    end
    join(out, " | ")
end
function run(i, src, qs)
    for r in 1:`)
	fmt.Fprintf(&b, "%d\n", mlReplicas)
	b.WriteString(`
        m = Module(Symbol("D", i, "_", r))
        Base.include_string(m, PRELUDE, "prelude.jl")
        redirect_stderr(devnull) do
            Base.include_string(m, src, "m.jl")
        end
        println(i, "\t", r, "\t", answer(m, qs))
    end
end
`)
	for i, c := range cs {
		var src strings.Builder
		for _, s := range c.sigs {
			src.WriteString(methodSource(s))
			src.WriteByte('\n')
		}
		var qs []string
		for _, q := range c.queries {
			qs = append(qs, fmt.Sprintf("(Meta.parse(%q), %d, %v)", core.Show(q.tt), q.lim, q.incl))
		}
		fmt.Fprintf(&b, "run(%d, %q, Any[%s])\n", i, src.String(), strings.Join(qs, ", "))
	}
	b.WriteString("nothing\n")
	return b.String()
}

func TestMlMatchesAgainstJulia(t *testing.T) {
	cs := drawML(t)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Call("eval", juliaMLProgram(cs))
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		answers := make([][]string, len(cs))
		for _, line := range strings.Split(strings.TrimRight(printed, "\n"), "\n") {
			f := strings.SplitN(line, "\t", 3)
			if len(f) != 3 {
				t.Fatalf("a line of the oracle's: %q", line)
			}
			var i int
			fmt.Sscan(f[0], &i)
			answers[i] = append(answers[i], f[2])
		}
		var b strings.Builder
		for i, c := range cs {
			for _, s := range c.sigs {
				b.WriteString(core.Show(s))
				b.WriteByte('\t')
			}
			want := answers[i][0]
			for _, a := range answers[i] {
				if a != want {
					want = "unstable"
				}
			}
			b.WriteString("=>\t" + want + "\n")
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mlGolden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gb, err := os.ReadFile(mlGolden)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(string(gb), "\n"), "\n")
	if len(golden) != len(cs) {
		t.Fatalf("golden has %d tables, the generator %d: regenerate it", len(golden), len(cs))
	}
	bad, unstable, queries, matches := 0, 0, 0, 0
	for i, c := range cs {
		_, want, _ := strings.Cut(golden[i], "=>\t")
		if want == "unstable" {
			unstable++
			continue
		}
		queries += len(c.queries)
		matches += strings.Count(want, ":")
		if got := goMLAnswers(t, c); got != want {
			bad++
			if bad <= 12 {
				var src []string
				for _, s := range c.sigs {
					src = append(src, methodSource(s))
				}
				var qs []string
				for _, q := range c.queries {
					qs = append(qs, fmt.Sprintf("%s lim=%d incl=%v", core.Show(q.tt), q.lim, q.incl))
				}
				t.Errorf("table %d:\n  %s\nqueries %s\ngo    %s\njulia %s", i, strings.Join(src, "\n  "), strings.Join(qs, "\n        "), got, want)
			}
		}
	}
	t.Logf("%d tables (%d unstable across %d runs, not replayed), %d queries, %d matches", len(cs), unstable, mlReplicas, queries, matches)
	if bad > 0 {
		t.Errorf("%d tables differ from julia", bad)
	}
}
