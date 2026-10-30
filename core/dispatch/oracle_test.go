package dispatch

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The differential test of dispatch: random method tables and random
// concrete calls, answered by julia 1.12.6's `which` once (JLGO_ORACLE=1
// JLGO_UPDATE_CORE=1) into testdata/dispatch.golden and replayed.

const (
	dispatchSeed   = 7
	dispatchTables = 1500
	dispatchCalls  = 6
	dispatchGolden = "testdata/dispatch.golden"
)

func mustApply(t testing.TB, tc any, ps ...any) any {
	v, err := core.Apply(tc, ps...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// sigGen draws method signatures over a small vocabulary.
type sigGen struct {
	r *rand.Rand
	t testing.TB
}

func (g *sigGen) vector(p any) any { return mustApply(g.t, core.ArrayType, p, int64(1)) }

// arg draws one argument type; vars are the static parameters in scope.
func (g *sigGen) arg(vars []*core.TypeVar) any {
	atoms := []any{core.Int64Type, core.IntegerType, core.RealType, core.NumberType, core.AnyType,
		core.StringType, core.AbstractStringType, core.NothingType, core.SignedType,
		core.TypeUnion(core.Int64Type, core.StringType), core.TypeUnion(core.NothingType, core.Int64Type)}
	switch k := g.r.IntN(10); {
	case k < 5 || len(vars) == 0:
		return atoms[g.r.IntN(len(atoms))]
	case k < 7:
		return vars[g.r.IntN(len(vars))]
	case k < 8:
		return g.vector(vars[g.r.IntN(len(vars))])
	case k < 9:
		return g.vector(atoms[g.r.IntN(len(atoms))])
	default:
		return core.TypeUnion(vars[g.r.IntN(len(vars))], core.NothingType)
	}
}

func (g *sigGen) sig() any {
	var vars []*core.TypeVar
	for i := 0; i < g.r.IntN(3); i++ {
		v := &core.TypeVar{Name: fmt.Sprintf("T%d", i), LB: core.Bottom, UB: core.AnyType}
		switch g.r.IntN(3) {
		case 1:
			v.UB = []any{core.IntegerType, core.RealType, core.NumberType}[g.r.IntN(3)]
		case 2:
			v.LB = []any{core.Int64Type, core.SignedType}[g.r.IntN(2)]
		}
		vars = append(vars, v)
	}
	n := 1 + g.r.IntN(3)
	ps := make([]any, 0, n+1)
	for i := 0; i < n; i++ {
		ps = append(ps, g.arg(vars))
	}
	if g.r.IntN(4) == 0 {
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

// call draws a concrete argument tuple; three times in four it aims at one
// of sigs (a tuple of the right length whose arguments are drawn until it is
// a subtype of that signature), so that methods match and overlap.
func (g *sigGen) call(sigs []any) any {
	atoms := []any{core.Int64Type, core.Int8Type, core.StringType, core.NothingType, core.Float64Type,
		core.BoolType, g.vector(core.Int64Type), g.vector(core.StringType), g.vector(core.NothingType)}
	draw := func(n int) any {
		ps := make([]any, n)
		for i := range ps {
			ps[i] = atoms[g.r.IntN(len(atoms))]
		}
		return mustApply(g.t, core.AnyTupleType, ps...)
	}
	if len(sigs) > 0 && g.r.IntN(4) != 0 {
		sig := sigs[g.r.IntN(len(sigs))]
		body := core.UnwrapUnionAll(sig).(*core.DataType)
		n := len(body.Parameters)
		if core.IsVaTuple(body) {
			n += g.r.IntN(3) - 1
		}
		for try := 0; try < 40 && n > 0; try++ {
			if c := draw(n); core.Subtype(c, sig) {
				return c
			}
		}
	}
	return draw(1 + g.r.IntN(4))
}

// methodSource writes a signature as a julia method definition:
// f(::A, ::B, ::C...) where {T<:U} = nothing.
func methodSource(sig any) string {
	var wheres []string
	body := sig
	for {
		u, ok := body.(*core.UnionAll)
		if !ok {
			break
		}
		w := u.Var.Name
		hasLB, hasUB := u.Var.LB != any(core.Bottom), u.Var.UB != any(core.AnyType)
		switch {
		case hasLB && hasUB:
			w = core.Show(u.Var.LB) + "<:" + w + "<:" + core.Show(u.Var.UB)
		case hasUB:
			w += "<:" + core.Show(u.Var.UB)
		case hasLB:
			w += ">:" + core.Show(u.Var.LB)
		}
		wheres = append(wheres, w)
		body = u.Body
	}
	var args []string
	for _, p := range body.(*core.DataType).Parameters {
		if va, ok := p.(*core.Vararg); ok {
			args = append(args, "::"+core.Show(va.T)+"...")
			continue
		}
		args = append(args, "::"+core.Show(p))
	}
	s := "f(" + strings.Join(args, ", ") + ")"
	if len(wheres) > 0 {
		s += " where {" + strings.Join(wheres, ", ") + "}"
	}
	return s + " = nothing"
}

type dispatchCase struct {
	sigs  []any
	calls []any
}

func drawDispatch(t testing.TB) []dispatchCase {
	g := &sigGen{r: rand.New(rand.NewPCG(dispatchSeed, 1)), t: t}
	cs := make([]dispatchCase, dispatchTables)
	for i := range cs {
		for len(cs[i].sigs) < 1+g.r.IntN(5) {
			if s := g.sig(); s != nil {
				cs[i].sigs = append(cs[i].sigs, s)
			}
		}
		for j := 0; j < dispatchCalls; j++ {
			cs[i].calls = append(cs[i].calls, g.call(cs[i].sigs))
		}
	}
	return cs
}

// goAnswers answers each call of a case: the 1-based index of the method,
// "A" (ambiguous) or "N" (no method).
func goAnswers(t testing.TB, c dispatchCase) string {
	tab := &Table{Name: "f"}
	for _, s := range c.sigs {
		if _, err := tab.Add(s); err != nil {
			t.Fatal(err)
		}
	}
	var out []string
	for _, call := range c.calls {
		m, _, err := tab.Lookup(call, tab.World())
		switch {
		case err == nil:
			out = append(out, fmt.Sprint(m.Index+1))
		case strings.Contains(err.Error(), ErrAmbiguous.Error()):
			out = append(out, "A")
		case strings.Contains(err.Error(), ErrNoMethod.Error()):
			out = append(out, "N")
		default:
			out = append(out, "E")
		}
	}
	return strings.Join(out, " ")
}

// juliaProgram defines each case's methods in a fresh module, one method
// per line, so that which(f, T).line names the method julia chose.
func juliaProgram(cs []dispatchCase) string {
	var b strings.Builder
	b.WriteString(`
function answer(mod, calls)
    out = String[]
    for T in calls
        try
            push!(out, string(Base.invokelatest(which, getglobal(mod, :f), T).line))
        catch e
            # julia 1.12's which wraps the MethodError in an ErrorException
            msg = sprint(showerror, e)
            push!(out, occursin("ambiguous", msg) ? "A" : occursin("no method matching", msg) ? "N" : "E")
        end
    end
    println(join(out, ' '))
end
`)
	for i, c := range cs {
		var src strings.Builder
		for _, s := range c.sigs {
			src.WriteString(methodSource(s))
			src.WriteByte('\n')
		}
		var calls []string
		for _, call := range c.calls {
			calls = append(calls, core.Show(call))
		}
		fmt.Fprintf(&b, "let m = Module(:D%d); Core.eval(m, :(function f end)); redirect_stderr(devnull) do; Base.include_string(m, %q, \"m.jl\"); end; answer(m, Any[%s]) end\n",
			i, src.String(), strings.Join(calls, ", "))
	}
	b.WriteString("nothing\n")
	return b.String()
}

func TestDispatchAgainstJulia(t *testing.T) {
	cs := drawDispatch(t)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_CORE") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Call("eval", juliaProgram(cs))
		p.Close()
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		lines := strings.Split(strings.TrimRight(printed, "\n"), "\n")
		if len(lines) != len(cs) {
			t.Fatalf("julia answered %d lines for %d tables", len(lines), len(cs))
		}
		var b strings.Builder
		for i, c := range cs {
			for _, s := range c.sigs {
				b.WriteString(core.Show(s))
				b.WriteByte('\t')
			}
			b.WriteString("=>\t" + lines[i] + "\n")
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dispatchGolden, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gb, err := os.ReadFile(dispatchGolden)
	if err != nil {
		t.Fatal(err)
	}
	golden := strings.Split(strings.TrimRight(string(gb), "\n"), "\n")
	if len(golden) != len(cs) {
		t.Fatalf("golden has %d tables, the generator %d: regenerate it", len(golden), len(cs))
	}
	bad, calls := 0, 0
	tally := map[string]int{}
	for i, c := range cs {
		_, want, _ := strings.Cut(golden[i], "=>\t")
		for _, a := range strings.Fields(want) {
			switch a {
			case "A", "N", "E":
				tally[a]++
			default:
				tally["method"]++
			}
		}
		got := goAnswers(t, c)
		calls += len(c.calls)
		if got != want {
			bad++
			if bad <= 20 {
				var src []string
				for _, s := range c.sigs {
					src = append(src, methodSource(s))
				}
				var cl []string
				for _, call := range c.calls {
					cl = append(cl, core.Show(call))
				}
				t.Errorf("table %d:\n  %s\ncalls %s\ngo    %s\njulia %s", i, strings.Join(src, "\n  "), strings.Join(cl, " "), got, want)
			}
		}
	}
	t.Logf("%d tables, %d calls: julia chose a method %d times, ambiguous %d, no method %d, error %d",
		len(cs), calls, tally["method"], tally["A"], tally["N"], tally["E"])
	if bad > 0 {
		t.Errorf("%d / %d tables differ from julia", bad, len(cs))
	}
}
