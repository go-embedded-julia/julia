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
bench(PS); nothing
`)
	if _, err := p.Call("eval", prog.String()); err != nil {
		t.Fatal(err)
	}
	const rounds = 7
	bestJ, bestG := time.Duration(1<<62), time.Duration(1<<62)
	var nJ, nG int
	for r := 0; r < rounds; r++ {
		out, err := p.Call("eval", "bench(PS)")
		if err != nil {
			t.Fatal(err)
		}
		_, v, _ := strings.Cut(out, "=> ")
		v = strings.Trim(v, "()\n")
		a, b, _ := strings.Cut(v, ", ")
		ns, err := strconv.ParseInt(strings.TrimSuffix(a, "0x"), 10, 64)
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
