package syntax

import (
	"math"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
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
// the stack; 16.5 MB after the first pass; 11.6 MB with the token array
// and the stack allocated once at their final size; 10.0 MB now, with a stack
// entry of 24 bytes instead of 40). Allocation is exact under load, unlike a
// time.
func TestLongChainAllocation(t *testing.T) {
	src := []byte(strings.Repeat("a+", 100000) + "a")
	ParseAll(src, "warm")
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	ParseAll(src, "chain")
	runtime.ReadMemStats(&after)
	const limit = 10<<20 + 512<<10
	if got := after.TotalAlloc - before.TotalAlloc; got > limit {
		t.Errorf("a 100 000-term chain allocated %d bytes, want at most %d", got, limit)
	}
}

// stackPeak is the deepest buildTree's stack gets, to the entry: the stack of
// nodes is allocated at that size and never grows. It is compared with the
// depth the callback sees, for a flat node (the chain), a stream of statements,
// nested nodes, trivia that is dropped and trivia that is kept (the green tree:
// drops is nil).
func TestStackPeakIsTheDepthOfTheBuild(t *testing.T) {
	for _, src := range []string{
		strings.Repeat("a+", 1000) + "a",
		strings.Repeat("x = f(y, 2.5) + z[1]\n", 200),
		strings.Repeat("f(", 50) + "1" + strings.Repeat(")", 50),
		"begin\n  a; b\n  # note\nend\n",
		"",
		"a b\n(c",
	} {
		for _, drops := range []func(SyntaxHead) bool{dropsTrivia, nil} {
			s := newParseStream([]byte(src), 1, SyntaxVersion, DefaultLimits)
			newParseState(s).parseToplevel()
			s.validateTokens()
			_, peak := s.stackPeak(drops)
			depth, seen := 0, 0
			buildTree(s, drops, func(h SyntaxHead, first, last int, children []struct{}, _ []int, leaf bool) (struct{}, bool) {
				if drops != nil && drops(h) {
					return struct{}{}, false
				}
				if !leaf {
					depth -= len(children)
				}
				depth++
				seen = max(seen, depth)
				return struct{}{}, true
			})
			if seen != peak {
				t.Errorf("%.30q (drops %v): the stack got %d deep, stackPeak says %d", src, drops != nil, seen, peak)
			}
		}
	}
}

// The token array is sized from the text: a chain `a+a+...` and a text of
// statements fit it without growing (cap unchanged), and a text that has more
// tokens than runs (`1x` is two tokens in one run of word bytes) grows it.
func TestTokenArrayIsSizedFromTheText(t *testing.T) {
	for _, tc := range []struct {
		src  string
		grow bool
	}{
		{strings.Repeat("a+", 5000) + "a", false},
		{strings.Repeat("x = f(y, 2.5) + z[1]\n", 500), false},
		{strings.Repeat("1x,", 500), true},
	} {
		s := newParseStream([]byte(tc.src), 1, SyntaxVersion, DefaultLimits)
		initial := cap(s.tokens)
		newParseState(s).parseToplevel()
		if grew := cap(s.tokens) != initial; grew != tc.grow {
			t.Errorf("%.20q: the token array grew: %v (cap %d, then %d, %d tokens), want %v", tc.src, grew, initial, cap(s.tokens), len(s.tokens), tc.grow)
		}
	}
}

// The ranges array starts at one per six bytes and doubles when full: an input
// of one range per two bytes (`a.b.c.…`, a field access each) fills it, and the
// tree that comes out is the same as a small input's.
func TestManyRangesGrowTheArray(t *testing.T) {
	src := strings.Repeat("a.", 3000) + "a"
	// 16+len/6 = 1017 ranges at the start, 3000 needed
	got := expr.Serialize(ParseAll([]byte(src), "ranges"))
	if !strings.Contains(got, `(:"." (:"." (:"." `) || !strings.HasSuffix(got, " {Q :a}) {Q :a}))") || strings.Count(got, `(:"."`) != 3000 {
		t.Fatalf("the tree of a.a.a…: %.60s … %s", got, got[max(0, len(got)-40):])
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
