package syntax

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// fragments are spliced into snippets by mutate: the punctuation and
// keywords around which JuliaSyntax's own failures were found.
var fragments = []string{
	".", "..", "...", " ", "\n", ":", "::", "<", "<:", "(", ")", "[", "]", "{", "}",
	",", ";", "=", "'", "\"", "`", "\\", "$", "@", "#", "#=", "=#", "import", "using",
	"end", "begin", "function", "do", "for", "if", "else", "try", "catch", "where",
	"0", "1.5", "x", "->", "|>", "&&", "?", "!", "\xff",
}

// mutate derives a hostile input from src: a few random insertions,
// deletions and splices of fragments.
func mutate(r *rand.Rand, src string) string {
	b := []byte(src)
	for n := 1 + r.IntN(4); n > 0; n-- {
		i := 0
		if len(b) > 0 {
			i = r.IntN(len(b) + 1)
		}
		switch r.IntN(3) {
		case 0: // insert
			f := fragments[r.IntN(len(fragments))]
			b = append(b[:i:i], append([]byte(f), b[i:]...)...)
		case 1: // delete
			if i < len(b) {
				j := min(len(b), i+1+r.IntN(4))
				b = append(b[:i:i], b[j:]...)
			}
		default: // truncate
			b = b[:i]
		}
	}
	return string(b)
}

// fuzzCorpus reads the inputs `go test -fuzz` committed for target.
func fuzzCorpus(t *testing.T, target string) []string {
	t.Helper()
	dir := filepath.Join("testdata", "fuzz", target)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		// go test fuzz v1\n[]byte("...")\n
		_, lit, ok := strings.Cut(strings.TrimSpace(string(data)), "[]byte(")
		if !ok {
			t.Fatalf("%s: not a []byte corpus entry", e.Name())
		}
		s, err := strconv.Unquote(strings.TrimSuffix(lit, ")"))
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		out = append(out, s)
	}
	return out
}

// TestParserFailuresAgreeWithJulia (JLGO_ORACLE=1) checks the claim behind
// ErrParserFailed: the Go parser reports it exactly where JuliaSyntax itself
// throws (and julia falls back to flisp). The inputs are the committed fuzz
// corpora and JLGO_MUTATIONS (default 3000) seeded mutations of the golden
// snippets; every one is checked in both directions.
func TestParserFailuresAgreeWithJulia(t *testing.T) {
	if !live {
		t.Skip("needs JLGO_ORACLE=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	inputs := append(fuzzCorpus(t, "FuzzParse"), fuzzCorpus(t, "FuzzGreen")...)
	snippets := readGolden[gen.Snippet](t, "snippets.jsonl")
	n := 3000
	if v, err := strconv.Atoi(os.Getenv("JLGO_MUTATIONS")); err == nil {
		n = v
	}
	r := rand.New(rand.NewPCG(1, 2))
	for range n {
		inputs = append(inputs, mutate(r, snippets[r.IntN(len(snippets))].Source()))
	}
	failed, differ := 0, 0
	for _, src := range inputs {
		ex, goErr := ParseAllLimits([]byte(src), "fuzz", DefaultLimits)
		if goErr != nil && !errors.Is(goErr, ErrParserFailed) {
			t.Fatalf("%q: %v", src, goErr)
		}
		j, err := p.Call("jsfails", src, "fuzz")
		if err != nil {
			t.Fatal(err)
		}
		goFails := goErr != nil
		if goFails {
			failed++
		}
		if goFails != strings.HasPrefix(j, "throws") {
			t.Errorf("%q: go %v, julia %s", src, goErr, j)
			continue
		}
		if goFails {
			continue
		}
		// Where both succeed, the trees must be the same: a differential
		// fuzz of the whole parser, error Expr included.
		want, err := p.ParseAll(src, "fuzz")
		if err != nil {
			t.Fatal(err)
		}
		if got := expr.Serialize(ex); got != want {
			differ++
			if differ <= 20 {
				t.Errorf("%q: Expr differs\n%s", src, firstDiff(got, want))
			}
		}
	}
	t.Logf("%d inputs, %d where JuliaSyntax and the Go parser both fail, %d Expr differences",
		len(inputs), failed, differ)
}

func TestMutate(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	seen := map[string]bool{}
	for range 200 {
		seen[mutate(r, "import A")] = true
		seen[mutate(r, "")] = true
	}
	if len(seen) < 50 {
		t.Fatalf("only %d distinct mutations", len(seen))
	}
}
