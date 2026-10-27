package lower

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

func TestGensymID(t *testing.T) {
	for s, want := range map[string]string{
		"#s12": "#s12|", "#15#name": "#15|#name", "#2#3": "#2|#3", "#1": "#1|",
		"#self#": "", "##x": "", "#": "", "#s": "", "x#1": "", "#12x": "", "#s1x": "",
	} {
		id, rest, ok := gensymID(s)
		got := ""
		if ok {
			got = id + "|" + rest
		}
		if got != want {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
}

func TestRuntimeGensym(t *testing.T) {
	for s, want := range map[string]string{
		"##x#12": "##x#", "##7": "##", "##a#b#3": "##a#b#",
		"##x": "", "##": "", "#s1": "", "##x#": "", "##x1": "",
	} {
		got, ok := runtimeGensym(s)
		if !ok {
			got = ""
		}
		if got != want {
			t.Errorf("%q: %q, want %q", s, got, want)
		}
	}
	got := expr.Serialize(Canonical(expr.New("block", expr.Symbol("##x#12"), expr.Symbol("#s3"), expr.Symbol("##x#12"), expr.Symbol("##9"))))
	if got != `(:block :"##x#r1" :"#g2" :"##x#r1" :"##r3")` {
		t.Fatal(got)
	}
}

func TestCanonical(t *testing.T) {
	g := func(s string) expr.Symbol { return expr.Symbol(s) }
	in := expr.New("block",
		g("#s9"), g("#7#x"), g("#s9"), g("plain"),
		expr.Vector{g("#s4")},
		expr.QuoteNode{Value: g("#7#y")},
		expr.GlobalRef{Mod: "Main", Name: g("#5#6")},
		expr.LineNumberNode{Line: 1, File: g("f.jl")},
		expr.GotoIfNot{Cond: g("#s4"), Dest: 3},
		expr.ReturnNode{Val: g("#s4")},
		expr.NewvarNode{Slot: g("#s9")},
		expr.EnterNode{Catch: 2, Scope: g("#s1"), HasScope: true},
		&expr.CodeInfo{Code: expr.Vector{g("#s1")}, SlotNames: expr.Vector{g("#s9")}},
		&expr.DebugInfo{Def: g("#s2"), Edges: expr.Vector{g("#s2")}, Codelocs: "#s2"},
		"#s9", int64(1))
	want := `(:block :"#g1" :"#g2#x" :"#g1" :plain [:"#g3"] {Q :"#g2#y"} {G :Main :"#g4#6"} {L 1 :"f.jl"} {GotoIfNot :"#g3" 3} {Return :"#g3"} {Newvar :"#g1"} {Enter 2 :"#g5"} ` +
		`{CodeInfo [:"#g5"] nothing nothing [] [:"#g1"] [] nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing nothing} {DI :"#g6" nothing [:"#g6"] "#s2"} "#s9" 1)`
	if got := expr.Serialize(Canonical(in)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if _, err := CanonicalString("(bad"); err == nil {
		t.Fatal("bad serialisation accepted")
	}
	if s, err := CanonicalString(":\"#s3\""); err != nil || s != `:"#g1"` {
		t.Fatalf("%s %v", s, err)
	}
}

func TestLowerToplevel(t *testing.T) {
	st := NewState()
	for _, v := range []any{
		expr.Symbol("x"), int64(1), "s", 1.5,
		expr.New("toplevel", int64(1)), expr.New("module"), expr.New("export", expr.Symbol("f")),
		expr.New("global", expr.Symbol("a"), expr.Symbol("b")), expr.New("const", expr.Symbol("c")),
	} {
		got, err := st.Lower(v)
		if err != nil || expr.Serialize(got) != expr.Serialize(v) {
			t.Errorf("%s: %v %v", expr.Serialize(v), got, err)
		}
	}
	got, _ := st.Lower(expr.Symbol("__"))
	if !expr.Is(got, "error") {
		t.Errorf("underscore: %s", expr.Serialize(got))
	}
}

var _ = errors.Is

// minSnippetsIdentical is the number of lowering snippets' forms the Go
// lowering reproduces; every step of the port raises it.
const minSnippetsIdentical = 5

func readGolden[T any](t *testing.T, name string) []T {
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

// lowerForm lowers one recorded form in st and reports whether the result
// is julia's canonical output.
func lowerForm(t *testing.T, st *State, f gen.LoweringForm) bool {
	t.Helper()
	in, err := expr.Parse(f.Input)
	if err != nil {
		t.Fatalf("%s: %v", f.Input, err)
	}
	want, err := CanonicalString(f.Output)
	if err != nil {
		t.Fatalf("%s: %v", f.Output, err)
	}
	got, err := st.Lower(in)
	if err != nil {
		if os.Getenv("JLGO_SHOWDIFF") != "" {
			t.Logf("%s:\n  error %v", f.Input, err)
		}
		return false
	}
	g := expr.Serialize(Canonical(got))
	if g != want && os.Getenv("JLGO_SHOWDIFF") != "" {
		k := 0
		for k < len(g) && k < len(want) && g[k] == want[k] {
			k++
		}
		t.Logf("%s:\n  got  …%s\n  want …%s", f.Input, g[max(0, k-200):min(len(g), k+300)], want[max(0, k-200):min(len(want), k+300)])
	}
	return g == want
}

// TestLoweringSnippets lowers the forms of testdata/lowering-snippets.jl that
// julia expanded, one State per snippet, and compares with julia's output.
func TestLoweringSnippets(t *testing.T) {
	snippets := readGolden[gen.LoweringSnippet](t, "lowering-snippets.jsonl")
	n, same := 0, 0
	for _, sn := range snippets {
		st := NewState()
		for _, f := range sn.Forms {
			if f.Tag != "F" {
				continue
			}
			n++
			if lowerForm(t, st, f) {
				same++
			}
		}
	}
	t.Logf("lowering snippets: %d of %d forms identical to julia", same, n)
	if same < minSnippetsIdentical {
		t.Fatalf("%d forms identical, %d before", same, minSnippetsIdentical)
	}
}

// TestLoweringCorpus (JLGO_ORACLE=1) lowers the whole corpus with julia and
// with the Go lowering, and the Go lowering must reproduce at least as many
// forms as the manifest testdata/golden/lowering.jsonl records. Files whose
// julia output is not the manifest's are counted, not failed: macros such as
// @static Sys.isapple() expand differently on another platform.
func TestLoweringCorpus(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" {
		t.Skip("needs JLGO_ORACLE=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	side := gen.LoweringSide{Canonical: CanonicalString, Lower: func(forms []gen.LoweringForm) []bool {
		st := NewState()
		out := make([]bool, len(forms))
		for i, f := range forms {
			out[i] = lowerForm(t, st, f)
		}
		return out
	}}
	var files, filesSame, forms, same, recorded, drift int
	for _, want := range readGolden[gen.LoweringCorpusEntry](t, "lowering.jsonl") {
		got, err := gen.LoweringFile(p, want.Path)
		if err != nil {
			t.Fatal(err)
		}
		e, err := gen.LoweringEntry(want.Path, got, side)
		if err != nil {
			t.Fatal(err)
		}
		if e.Digests != want.Digests || e.Unexpanded != want.Unexpanded {
			drift++
		}
		files++
		forms += e.Forms
		same += e.Identical
		recorded += want.Identical
		if e.Identical == e.Forms {
			filesSame++
		}
	}
	t.Logf("lowering corpus: %d/%d files, %d/%d forms identical to julia (%d files lower differently here than in the manifest)",
		filesSame, files, same, forms, drift)
	if same < recorded {
		t.Fatalf("%d forms identical, the manifest records %d", same, recorded)
	}
}
