package lower

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// benchRounds is the number of timed rounds, after a warm-up round.
const benchRounds = 3

// TestBenchLowering (JLGO_ORACLE=1 JLGO_BENCH=1) times Meta.lower in julia
// and Lower in Go over every macro-expanded form of the corpus, one core
// each (GOMAXPROCS=1; julia lowers on one thread), best of benchRounds.
// Each file's forms are lowered in a fresh module, as the goldens are.
func TestBenchLowering(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_BENCH") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_BENCH=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var files [][]any
	n := 0
	for _, e := range readGolden[gen.LoweringCorpusEntry](t, "lowering.jsonl") {
		forms, err := gen.LoweringFile(p, e.Path)
		if err != nil {
			t.Fatal(err)
		}
		var ins []any
		for _, f := range forms {
			if f.Tag == "F" {
				ins = append(ins, mustParse(t, f.Input))
			}
		}
		n += len(ins)
		files = append(files, ins)
	}
	ans, err := p.Call("benchlower", strconv.Itoa(benchRounds))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(ans)
	jlNS, _ := strconv.ParseFloat(f[0], 64)
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	best := time.Duration(1<<63 - 1)
	for r := 0; r <= benchRounds; r++ {
		runtime.GC()
		t0 := time.Now()
		for _, ins := range files {
			st := NewState()
			for _, in := range ins {
				st.Lower(in)
			}
		}
		if dt := time.Since(t0); r > 0 && dt < best {
			best = dt
		}
	}
	jl := time.Duration(jlNS)
	t.Logf("corpus: %d forms in Go, %s in julia", n, f[1])
	t.Logf("julia %s Meta.lower: %v", oracle.Version, jl)
	t.Logf("go Lower:              %v", best)
	t.Logf("ratio go/julia: %.2f", best.Seconds()/jl.Seconds())
}
