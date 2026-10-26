package syntax

import (
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

// TestThroughputAgainstJulia (JLGO_ORACLE=1) times Meta.parseall in julia
// and ParseAll in Go on the same generated inputs, best of five each, and
// reports the ratio. It fails only when Go is more than twice as slow.
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
		out, err := p.Call("eval", "s = "+gen+"; Meta.parseall(s); t = minimum(@elapsed(Meta.parseall(s)) for _ in 1:5); print(t, \" \", s)")
		if err != nil {
			t.Fatal(err)
		}
		tStr, src, _ := strings.Cut(out, " ")
		src = src[:strings.LastIndex(src, "\n=> ")]
		jl, _ := strconv.ParseFloat(tStr, 64)
		best := time.Duration(1<<63 - 1)
		for i := 0; i < 6; i++ {
			t0 := time.Now()
			ParseAll([]byte(src), "none")
			if d := time.Since(t0); i > 0 && d < best {
				best = d
			}
		}
		ratio := best.Seconds() / jl
		t.Logf("%s (%d bytes): julia %.1f ms, go %.1f ms, go/julia %.2f", name, len(src), jl*1e3, best.Seconds()*1e3, ratio)
		if ratio > 2 {
			t.Errorf("%s: Go is %.2fx slower than julia", name, ratio)
		}
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
