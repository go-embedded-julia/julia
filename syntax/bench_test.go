package syntax

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

func benchParse(b *testing.B, src []byte) {
	b.SetBytes(int64(len(src)))
	for b.Loop() {
		ParseAll(src, "bench")
	}
}

// BenchmarkManyLines and BenchmarkLongChain measure the parser on large
// regular inputs; BenchmarkSnippets on the golden snippets.
func BenchmarkManyLines(b *testing.B) {
	benchParse(b, []byte(strings.Repeat("x = f(y, 2.5) + z[1]\n", 20000)))
}

func BenchmarkLongChain(b *testing.B) {
	benchParse(b, []byte(strings.Repeat("a+", 100000)+"a"))
}

// throughputRounds is the number of interleaved julia/Go rounds per input.
const throughputRounds = 9

// TestThroughputAgainstJulia (JLGO_ORACLE=1) times Meta.parseall in julia
// and ParseAll in Go on the same generated inputs, the best of nine rounds
// that alternate the two, and reports the ratio. It fails only when Go is more
// than twice as slow.
func TestThroughputAgainstJulia(t *testing.T) {
	if !live {
		t.Skip("needs JLGO_ORACLE=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for name, gen := range map[string]string{
		"many lines": `repeat("x = f(y, 2.5) + z[1]\n", 20000)`,
		"long chain": `repeat("a+", 100000) * "a"`,
		"functions":  `repeat("function f(x::T; k=1) where {T}\n    [i^2 for i in 1:x if isodd(i)]\nend\n", 5000)`,
	} {
		// the input is built and julia's parser warmed once; then the rounds
		// alternate, one julia parse and one Go parse each, so that both
		// sides meet the same machine state (a shared runner's load changes
		// from one second to the next: best-of-five of one side followed by
		// best-of-six of the other measured two different machines)
		out, err := p.Call("eval", "s = "+gen+"; Core.eval(Main, :(JLGO_S = $s)); Meta.parseall(s); print(s)")
		if err != nil {
			t.Fatal(err)
		}
		src := out[:strings.LastIndex(out, "\n=> ")]
		ParseAll([]byte(src), "none")
		jl, goBest := math.Inf(1), math.Inf(1)
		for i := 0; i < throughputRounds; i++ {
			out, err := p.Call("eval", `print(@elapsed(Meta.parseall(Main.JLGO_S)))`)
			if err != nil {
				t.Fatal(err)
			}
			v, err := strconv.ParseFloat(out[:strings.LastIndex(out, "\n=> ")], 64)
			if err != nil {
				t.Fatalf("julia answered %q", out)
			}
			jl = min(jl, v)
			t0 := time.Now()
			ParseAll([]byte(src), "none")
			goBest = min(goBest, time.Since(t0).Seconds())
		}
		best := time.Duration(goBest * float64(time.Second))
		ratio := best.Seconds() / jl
		t.Logf("%s (%d bytes): julia %.1f ms, go %.1f ms, go/julia %.2f", name, len(src), jl*1e3, best.Seconds()*1e3, ratio)
		if ratio > 2 {
			t.Errorf("%s: Go is %.2fx slower than julia", name, ratio)
		}
	}
}

// A chain of 100 000 terms is one node of 200 000 children: parsing it must
// not allocate in proportion to the growth of its arrays (#46: 53.8 MB when
// append grew each of them by a quarter and the children were copied out of
// the stack; 16.5 MB now). Allocation is exact under load, unlike a time.
func TestLongChainAllocation(t *testing.T) {
	src := []byte(strings.Repeat("a+", 100000) + "a")
	ParseAll(src, "warm")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	ParseAll(src, "chain")
	runtime.ReadMemStats(&after)
	const limit = 24 << 20
	if got := after.TotalAlloc - before.TotalAlloc; got > limit {
		t.Errorf("a 100 000-term chain allocated %d bytes, want at most %d", got, limit)
	}
}

func BenchmarkSnippets(b *testing.B) {
	var srcs [][]byte
	total := 0
	for _, sn := range readGolden[gen.Snippet](b, "snippets.jsonl") {
		srcs = append(srcs, []byte(sn.Source()))
		total += len(sn.Source())
	}
	b.SetBytes(int64(total))
	for b.Loop() {
		for _, s := range srcs {
			ParseAll(s, "none")
		}
	}
}
