package syntax

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
)

// live enables the tests that need the reference julia: JLGO_ORACLE=1.
var live = os.Getenv("JLGO_ORACLE") != ""

// A level is one of the outputs the parser is compared with julia at: the
// raw lexer's tokens, the green tree with its diagnostics, the Expr tree.
// Each is registered by the test file of the step that implements it.
type level struct {
	name string
	// run computes the Go side's output for a source file.
	run func(t testing.TB, src []byte, filename string) string
	// golden is julia's output for a snippet.
	golden func(gen.Snippet) string
	// digest is the digest of julia's output for a corpus file, and whether
	// the Go side was recorded as identical.
	digest func(gen.CorpusEntry) (string, bool)
}

var levels []level

func readGolden[T any](t testing.TB, name string) []T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "testdata", "golden", name))
	if err != nil {
		t.Fatal(err)
	}
	v, err := gen.ReadJSONLines[T](data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// firstDiff locates the first difference of two outputs for the failure
// message.
func firstDiff(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	lo := max(0, i-60)
	return "at byte " + itoa(i) + ":\n got …" + got[lo:min(len(got), i+60)] + "\nwant …" + want[lo:min(len(want), i+60)]
}

// TestSnippets replays the golden records of JuliaSyntax's own test inputs
// (and testdata/snippets-extra.jl, and the binary snippets of jloracle)
// recorded from the reference julia, at every level implemented.
func TestSnippets(t *testing.T) {
	snippets := readGolden[gen.Snippet](t, "snippets.jsonl")
	if len(snippets) < 900 {
		t.Fatalf("only %d snippets in the golden file", len(snippets))
	}
	var failures []string
	for _, lv := range levels {
		n := 0
		for _, sn := range snippets {
			src := sn.Source()
			if got, want := lv.run(t, []byte(src), gen.SnippetFilename), lv.golden(sn); got == want {
				n++
			} else {
				failures = append(failures, lv.name+" of "+strconv.Quote(src)+"\n"+firstDiff(got, want))
			}
		}
		t.Logf("snippets: %s identical to julia for %d of %d", lv.name, n, len(snippets))
	}
	for i, f := range failures {
		if i >= 40 {
			t.Errorf("… and %d more", len(failures)-40)
			break
		}
		t.Error(f)
	}
}
