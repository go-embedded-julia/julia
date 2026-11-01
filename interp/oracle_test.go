package interp

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The programs of testdata/snippets.jl, answered once by julia 1.12.6
// (JLGO_ORACLE=1 JLGO_UPDATE_INTERP=1) into testdata/eval.golden and
// replayed: each program's last value, as julia shows it, must be what the
// Go interpreter computes.

const (
	snippetsPath = "testdata/snippets.jl"
	evalGolden   = "testdata/eval.golden"
)

type evalCase struct {
	Src  string `json:"src"`
	Want string `json:"want"`
}

func loadSnippets(t testing.TB) []string {
	t.Helper()
	b, err := os.ReadFile(snippetsPath)
	if err != nil {
		t.Fatal(err)
	}
	// the first part is the header comment
	return strings.Split(string(b), "#---\n")[1:]
}

func TestEvalAgainstJulia(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_INTERP") != "" {
		updateEvalGolden(t, loadSnippets(t))
	}
	f, err := os.Open(evalGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		var c evalCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		n++
		v, err := New().NewSession().Eval(c.Src)
		if err != nil {
			t.Errorf("program %d:\n%s\nGo error: %v\njulia: %s", n, c.Src, err, c.Want)
			continue
		}
		// the oracle evaluates in its own scratch module, OracleEval; the
		// session's is L: the name is the harness's, not the program's
		want := strings.ReplaceAll(c.Want, "Main.OracleEval", "Main.L")
		if got := Repr(v); got != want {
			t.Errorf("program %d:\n%s\nGo:    %s\njulia: %s", n, c.Src, got, want)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if want := len(loadSnippets(t)); n != want {
		t.Fatalf("%d golden answers for %d programs: regenerate with JLGO_ORACLE=1 JLGO_UPDATE_INTERP=1", n, want)
	}
}

func updateEvalGolden(t *testing.T, progs []string) {
	t.Helper()
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, err := os.Create(evalGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for i, src := range progs {
		ans, err := p.Call("eval", src)
		if err != nil {
			t.Fatalf("julia rejects program %d (a program must catch its own errors):\n%s\n%v", i+1, src, err)
		}
		_, shown, ok := strings.Cut(ans, "\n=> ")
		if !ok {
			t.Fatalf("program %d: julia answered %q", i+1, ans)
		}
		if err := enc.Encode(evalCase{Src: src, Want: shown}); err != nil {
			t.Fatal(err)
		}
	}
}
