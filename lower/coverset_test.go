package lower

import (
	"bytes"
	"cmp"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// coverForm is one corpus form the Go lowering reproduces, with the state
// of its file's module just before it, so that it can be lowered again on
// its own: testdata/golden/lowering-cover.jsonl is the smallest forms of
// the corpus that, together, reach every statement the corpus reaches.
type coverForm struct {
	Path     string   `json:"path"`
	Counter  int      `json:"counter,omitempty"`
	Bindings []string `json:"bindings,omitempty"`
	Pool     []string `json:"pool,omitempty"`
	Current  int      `json:"current,omitempty"`
	GCounter int      `json:"gcounter,omitempty"`
	Input    string   `json:"input"`
	Output   string   `json:"output"`
}

func snapshot(st *State, path string) coverForm {
	c := coverForm{Path: path, Counter: st.moduleCounter, Current: st.gensyms.current, GCounter: st.gensyms.counter}
	for b := range st.bindings {
		c.Bindings = append(c.Bindings, b)
	}
	slices.Sort(c.Bindings)
	for _, s := range st.gensyms.pool {
		c.Pool = append(c.Pool, string(s))
	}
	return c
}

func (c coverForm) state() *State {
	st := NewState()
	st.moduleCounter = c.Counter
	for _, b := range c.Bindings {
		st.bindings[b] = true
	}
	for _, s := range c.Pool {
		st.gensyms.pool = append(st.gensyms.pool, sym(s))
	}
	st.gensyms.current, st.gensyms.counter = c.Current, c.GCounter
	return st
}

// lowersAlike lowers c in its recorded state and compares with julia.
func (c coverForm) lowersAlike(t *testing.T) bool {
	t.Helper()
	return lowerForm(t, c.state(), gen.LoweringForm{Tag: "F", Input: c.Input, Output: c.Output})
}

func writeJSONLines[T any](t *testing.T, path string, v []T) {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, x := range v {
		if err := enc.Encode(x); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCoverCandidates (JLGO_ORACLE=1 JLGO_COVER_CANDIDATES=<file>) writes
// every corpus form the Go lowering reproduces, with its state, as the
// candidates of TestCoverSelect. It runs without -cover: the selection
// must measure only what the candidates themselves reach.
func TestCoverCandidates(t *testing.T) {
	out := os.Getenv("JLGO_COVER_CANDIDATES")
	if out == "" || os.Getenv("JLGO_ORACLE") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_COVER_CANDIDATES")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var cands []coverForm
	for _, e := range readGolden[gen.LoweringCorpusEntry](t, "lowering.jsonl") {
		forms, err := gen.LoweringFile(p, e.Path)
		if err != nil {
			t.Fatal(err)
		}
		st := NewState()
		for _, f := range forms {
			if f.Tag != "F" {
				continue
			}
			c := snapshot(st, e.Path)
			if lowerForm(t, st, f) {
				// the input as julia lowered it: renumbering it would
				// change the names lowering derives from it (a slot
				// named `sync#41` after the runtime gensym `##sync#41`)
				c.Input, c.Output = f.Input, canonicalOrFatal(t, f.Output)
				cands = append(cands, c)
			}
		}
	}
	writeJSONLines(t, out, cands)
	t.Logf("%d candidates", len(cands))
}

func canonicalOrFatal(t *testing.T, s string) string {
	t.Helper()
	c, err := CanonicalString(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// TestCoverSelect (go test -cover, JLGO_COVER_CANDIDATES=<file>
// JLGO_COVER_OUT=<golden>) keeps, smallest first, each candidate that
// raises the coverage of the package.
func TestCoverSelect(t *testing.T) {
	in, out := os.Getenv("JLGO_COVER_CANDIDATES"), os.Getenv("JLGO_COVER_OUT")
	if in == "" || out == "" || testing.CoverMode() == "" {
		t.Skip("needs -cover, JLGO_COVER_CANDIDATES and JLGO_COVER_OUT")
	}
	data, err := os.ReadFile(in)
	if err != nil {
		t.Fatal(err)
	}
	cands, err := gen.ReadJSONLines[coverForm](data)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortStableFunc(cands, func(a, b coverForm) int { return cmp.Compare(len(a.Input), len(b.Input)) })
	var keep []coverForm
	level, alone := testing.Coverage(), 0
	for _, c := range cands {
		if !c.lowersAlike(t) {
			// the canonical names hash differently from julia's
			alone++
			continue
		}
		if l := testing.Coverage(); l > level {
			level = l
			keep = append(keep, c)
		}
	}
	writeJSONLines(t, out, keep)
	t.Logf("%d of %d candidates kept (%d differ on their own), coverage %.2f%%", len(keep), len(cands), alone, 100*level)
}

// TestLoweringCoverSet lowers the forms of lowering-cover.jsonl, each in its
// recorded state: every one must be julia's output.
func TestLoweringCoverSet(t *testing.T) {
	forms := readGolden[coverForm](t, "lowering-cover.jsonl")
	for _, c := range forms {
		if !c.lowersAlike(t) {
			got, err := c.state().Lower(mustParse(t, c.Input))
			t.Errorf("%s: %s\n  got %s %v\n  want %s", c.Path, c.Input, serializeCanonical(got), err, c.Output)
		}
	}
}

func mustParse(t *testing.T, s string) any {
	t.Helper()
	v, err := expr.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func serializeCanonical(v any) string {
	if v == nil {
		return ""
	}
	return expr.Serialize(Canonical(v))
}
