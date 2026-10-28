package lower

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// exprGoldens are the programs that build their inputs in julia: forms
// still wrapped in hygienic scopes, and expressions lowering must handle
// that the parser never makes (error paths, rare heads). Each program
// prints `F\tinput\toutput` lines; the golden is named after it.
var exprGoldens = []string{"lowering-hygiene", "lowering-exprs"}

// TestExprsGolden (JLGO_ORACLE=1 JLGO_REGEN=1) runs the programs in the
// oracle and records their forms with julia's Meta.lower of each.
func TestExprsGolden(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_REGEN") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_REGEN=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, name := range exprGoldens {
		src, err := os.ReadFile(filepath.Join("..", "testdata", name+".jl"))
		if err != nil {
			t.Fatal(err)
		}
		ans, err := p.Call("eval", string(src))
		if err != nil {
			t.Fatal(err)
		}
		if i := strings.LastIndex(ans, "\n=> "); i >= 0 {
			ans = ans[:i]
		}
		var forms []gen.LoweringForm
		for _, line := range strings.Split(ans, "\n") {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "\t")
			if len(parts) != 3 || parts[0] != "F" {
				t.Fatalf("bad record %q", line)
			}
			in, err1 := base64.StdEncoding.DecodeString(parts[1])
			out, err2 := base64.StdEncoding.DecodeString(parts[2])
			if err1 != nil || err2 != nil {
				t.Fatalf("bad record %q", line)
			}
			forms = append(forms, gen.LoweringForm{Tag: "F", Input: string(in), Output: canonicalOrFatal(t, string(out))})
		}
		writeJSONLines(t, filepath.Join("..", "testdata", "golden", name+".jsonl"), forms)
		t.Logf("%s: %d forms", name, len(forms))
	}
}

// TestLoweringExprs lowers the recorded forms of each program in one
// State, in order, as julia did in one module: every one must be julia's.
func TestLoweringExprs(t *testing.T) {
	for _, name := range exprGoldens {
		forms := readGolden[gen.LoweringForm](t, name+".jsonl")
		if len(forms) == 0 {
			t.Fatalf("%s: no forms", name)
		}
		st := NewState()
		for _, f := range forms {
			got, err := st.Lower(mustParse(t, f.Input))
			if g := serializeCanonical(got); err != nil || g != f.Output {
				k := 0
				for k < len(g) && k < len(f.Output) && g[k] == f.Output[k] {
					k++
				}
				t.Errorf("%s: %s\n  got  …%s %v\n  want …%s", name, f.Input, g[max(0, k-150):min(len(g), k+150)], err,
					f.Output[max(0, k-150):min(len(f.Output), k+150)])
			}
		}
	}
}
