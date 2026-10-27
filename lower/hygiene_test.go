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

const hygieneGolden = "lowering-hygiene.jsonl"

// TestHygieneGolden (JLGO_ORACLE=1 JLGO_REGEN=1) runs
// testdata/lowering-hygiene.jl in the oracle and records its forms, still
// wrapped in hygienic scopes, with julia's Meta.lower of each.
func TestHygieneGolden(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_REGEN") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_REGEN=1")
	}
	src, err := os.ReadFile(filepath.Join("..", "testdata", "lowering-hygiene.jl"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
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
	writeJSONLines(t, filepath.Join("..", "testdata", "golden", hygieneGolden), forms)
	t.Logf("%d forms", len(forms))
}

// TestLoweringHygiene lowers the recorded hygienic-scope forms in one
// State, in order, as julia did in one module: every one must be julia's.
func TestLoweringHygiene(t *testing.T) {
	forms := readGolden[gen.LoweringForm](t, hygieneGolden)
	if len(forms) == 0 {
		t.Fatal("no forms")
	}
	st := NewState()
	for _, f := range forms {
		got, err := st.Lower(mustParse(t, f.Input))
		if g := serializeCanonical(got); err != nil || g != f.Output {
			k := 0
			for k < len(g) && k < len(f.Output) && g[k] == f.Output[k] {
				k++
			}
			t.Errorf("%s\n  got  …%s %v\n  want …%s", f.Input, g[max(0, k-150):min(len(g), k+150)], err,
				f.Output[max(0, k-150):min(len(f.Output), k+150)])
		}
	}
}
