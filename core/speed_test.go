package core

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/internal/oracle"
)

// BenchmarkSubtypePairs times x <: y over the golden pairs, for profiling
// without julia.
func BenchmarkSubtypePairs(b *testing.B) {
	xs, ys := drawPairs(goldenPairs)
	b.ResetTimer()
	for b.Loop() {
		for i := range xs {
			Subtype(xs[i], ys[i])
		}
	}
}

// TestSubtypeSpeedVsJulia times x <: y over the golden pairs, Go against
// julia's jl_subtype, in interleaved rounds on one core. It runs with
// JLGO_ORACLE=1 JLGO_BENCH=1; it measures, it does not gate (a machine
// under load makes the ratio meaningless).
func TestSubtypeSpeedVsJulia(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_BENCH") == "" {
		t.Skip("set JLGO_ORACLE=1 JLGO_BENCH=1")
	}
	xs, ys := drawPairs(goldenPairs)
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var prog strings.Builder
	prog.WriteString("global const PS = Tuple{Any,Any}[\n")
	for i := range xs {
		fmt.Fprintf(&prog, "(%s, %s),\n", Show(xs[i]), Show(ys[i]))
	}
	prog.WriteString(`]
function bench(ps)
    n = 0
    t0 = time_ns()
    for (x, y) in ps
        n += (x <: y)
    end
    return (time_ns() - t0, n)
end
bench(PS)
let (ns, n) = bench(PS); println(ns, " ", n) end
nothing
`)
	// the oracle's eval runs each request in a fresh module, so every round
	// rebuilds the pairs (untimed) and times them, warm, in the same request
	const rounds = 7
	bestJ, bestG := time.Duration(1<<62), time.Duration(1<<62)
	var nJ, nG int
	for r := 0; r < rounds; r++ {
		out, err := p.Call("eval", prog.String())
		if err != nil {
			t.Fatal(err)
		}
		printed, _, _ := strings.Cut(out, "\n=> ")
		a, b, _ := strings.Cut(strings.TrimSpace(printed), " ")
		ns, err := strconv.ParseInt(a, 10, 64)
		if err != nil {
			t.Fatalf("julia answered %q", out)
		}
		nJ, _ = strconv.Atoi(b)
		bestJ = min(bestJ, time.Duration(ns))

		t0 := time.Now()
		n := 0
		for i := range xs {
			if Subtype(xs[i], ys[i]) {
				n++
			}
		}
		bestG = min(bestG, time.Since(t0))
		nG = n
	}
	if nJ != nG {
		t.Fatalf("julia counted %d true, Go %d", nJ, nG)
	}
	t.Logf("%d pairs, best of %d: julia %v, Go %v, go/julia = %.2f",
		len(xs), rounds, bestJ, bestG, float64(bestG)/float64(bestJ))
}
