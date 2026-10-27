package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/lower"
)

// loweringSide is the Go lowering, for the lowering manifests.
var loweringSide = gen.LoweringSide{
	Canonical: lower.CanonicalString,
	Lower:     lowerForms,
}

// lowerForms lowers the "F" forms of one file in order, in one State, and
// reports which come out as julia's canonical output.
func lowerForms(forms []gen.LoweringForm) []bool {
	st := lower.NewState()
	out := make([]bool, len(forms))
	for i, f := range forms {
		in, err := expr.Parse(f.Input)
		if err != nil {
			continue
		}
		got, err := st.Lower(in)
		if err != nil {
			continue
		}
		want, err := lower.CanonicalString(f.Output)
		out[i] = err == nil && expr.Serialize(lower.Canonical(got)) == want
	}
	return out
}

// writeLoweringSnippets writes testdata/golden/lowering-snippets.jsonl:
// julia's expansion and lowering of each snippet of
// testdata/lowering-snippets.jl, gensyms renumbered so that the file is the
// same whatever julia session wrote it.
func writeLoweringSnippets(o gen.Oracle, root string, out io.Writer) error {
	src, err := os.ReadFile(filepath.Join(root, "testdata", "lowering-snippets.jl"))
	if err != nil {
		return err
	}
	data, err := gen.LoweringSnippets(o, gen.SplitExtra(string(src)), lower.CanonicalString)
	if err != nil {
		return err
	}
	return write(root, "testdata/golden/lowering-snippets.jsonl", data, out)
}

// writeLoweringCorpus writes testdata/golden/lowering.jsonl, the lowering
// manifest of the corpus, and prints the parity of the Go lowering.
func writeLoweringCorpus(o gen.Oracle, root string, out io.Writer) error {
	data, err := gen.LoweringCorpus(o, loweringSide)
	if err != nil {
		return err
	}
	entries, _ := gen.ReadJSONLines[gen.LoweringCorpusEntry](data)
	var forms, same, files, filesSame, unexpanded, lowerErrors int
	for _, e := range entries {
		files++
		forms += e.Forms
		same += e.Identical
		unexpanded += e.Unexpanded
		lowerErrors += e.LowerErrors
		if e.Identical == e.Forms {
			filesSame++
		}
	}
	fmt.Fprintf(out, "lowering parity: %d/%d files, %d/%d forms (%d forms not expandable, %d lowering errors in julia)\n",
		filesSame, files, same, forms, unexpanded, lowerErrors)
	return write(root, "testdata/golden/lowering.jsonl", data, out)
}
