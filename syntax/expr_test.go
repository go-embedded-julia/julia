package syntax

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
)

func init() {
	levels = append(levels, level{
		name:   "Expr trees",
		run:    func(_ testing.TB, src []byte, filename string) string { return expr.Serialize(ParseAll(src, filename)) },
		golden: func(s gen.Snippet) string { return s.Expr },
		digest: func(e gen.CorpusEntry) (string, bool) { return e.ExprSHA256, e.Parity },
	})
}

// TestPrograms replays the parse column of the program goldens (the lower
// and eval columns are for the milestones that lower and run code).
func TestPrograms(t *testing.T) {
	progs := readGolden[gen.Program](t, "programs.jsonl")
	if len(progs) == 0 {
		t.Fatal("no programs")
	}
	for _, p := range progs {
		src, err := os.ReadFile(filepath.Join("..", "testdata", "programs", p.Name))
		if err != nil {
			t.Fatal(err)
		}
		if got := expr.Serialize(ParseAll(src, p.Name)); got != p.Parse {
			t.Errorf("%s:\n%s", p.Name, firstDiff(got, p.Parse))
		}
	}
}

func TestParseLimits(t *testing.T) {
	if _, err := ParseAllLimits([]byte("x"), "f", Limits{MaxBytes: 0, MaxDepth: 10}); !errors.Is(err, ErrLimit) {
		t.Fatalf("MaxBytes: %v", err)
	}
	deep := []byte(strings.Repeat("(", 5000) + strings.Repeat(")", 5000))
	if _, err := ParseAllLimits(deep, "f", Limits{MaxBytes: 1 << 20, MaxDepth: 100}); !errors.Is(err, ErrLimit) ||
		!strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("MaxDepth: %v", err)
	}
	ex := ParseAll(deep, "f")
	if !expr.Is(ex, "error") || !strings.Contains(ex.(*expr.Expr).Args[0].(*expr.ParseError).Msg, "depth") {
		t.Fatalf("ParseAll of deep input: %s", expr.Serialize(ex))
	}
}

func TestWrapperExpr(t *testing.T) {
	s := newParseStream([]byte("a b"), 1, SyntaxVersion, DefaultLimits)
	ps := newParseState(s)
	ps.parseAtom(true, false)
	ps.parseAtom(true, false)
	if got := expr.Serialize(s.buildExpr("f", 1)); got != "(:block :a :b)" {
		t.Fatalf("wrapper Expr: %s", got)
	}
}

func TestInternalNodeOddities(t *testing.T) {
	src := NewSourceFile("x", "f", 1, 1)
	h := func(k Kind) SyntaxHead { return SyntaxHead{k, emptyFlags} }
	if got := internalNodeToExpr(src, 1, 1, h(kInert), nil, nil, []any{expr.Symbol("x")}); got != (expr.QuoteNode{Value: expr.Symbol("x")}) {
		t.Fatalf("inert: %v", got)
	}
	if got := expr.Serialize(internalNodeToExpr(src, 1, 1, h(kWrapper), nil, nil, []any{expr.Symbol("x")})); got != "(:block :x)" {
		t.Fatalf("wrapper: %s", got)
	}
	if symbolString("s") != "s" || symbolString(int64(1)) != "1" {
		t.Fatal("symbolString")
	}
}

func TestParseJuliaLiteralErrors(t *testing.T) {
	lit := func(k Kind, f RawFlags, s string) any {
		return parseJuliaLiteral([]byte(s), SyntaxHead{k, f}, 1, len(s))
	}
	for _, tc := range []struct {
		k    Kind
		text string
	}{
		{kFloat, "1e999"}, {kFloat32, "1e99"}, {kChar, `\q`}, {kChar, "ab"}, {kString, `\q`},
		{kError, "x"}, {kComma, ","},
	} {
		if v := lit(tc.k, emptyFlags, tc.text); v != (errorVal{}) {
			t.Errorf("%v %q: %v", tc.k, tc.text, v)
		}
	}
	if v := parseJuliaLiteral([]byte("+"), SyntaxHead{kPlus, emptyFlags}, 1, 0); v != expr.Symbol("+") {
		t.Errorf("synthetic operator: %v", v)
	}
	if v := lit(kCall, emptyFlags, "x"); v != nil {
		t.Errorf("syntax kind leaf: %v", v)
	}
	if v := lit(kEnd, emptyFlags, "end"); v != expr.Symbol("end") {
		t.Errorf("keyword leaf: %v", v)
	}
}

// FuzzParse checks that ParseAll terminates on any input, without
// panicking, and returns a tree or a limit error.
func FuzzParse(f *testing.F) {
	for _, sn := range readGolden[gen.Snippet](f, "snippets.jsonl") {
		f.Add([]byte(sn.Source()))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		ex, err := ParseAllLimits(src, "fuzz", fuzzLimits)
		if err != nil {
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("non-limit error %v", err)
			}
			return
		}
		if !expr.Is(ex, "toplevel") {
			t.Fatalf("not a toplevel Expr: %s", expr.Serialize(ex))
		}
	})
}

// TestHostileInputs feeds inputs shaped to exhaust a naive parser and
// requires each to finish quickly within the default limits.
func TestHostileInputs(t *testing.T) {
	for name, src := range map[string]string{
		"deep parens":     strings.Repeat("(", 100000),
		"deep brackets":   strings.Repeat("[", 100000),
		"deep unary":      strings.Repeat("-", 100000) + "x",
		"deep interp":     strings.Repeat("\"$(", 20000),
		"deep quote":      strings.Repeat(":(", 50000),
		"long chain":      strings.Repeat("a+", 200000) + "a",
		"long power":      strings.Repeat("a^", 50000) + "a",
		"long assignment": strings.Repeat("a=", 50000) + "a",
		"many semicolons": "[a " + strings.Repeat(";", 100000) + " b]",
		"nested comments": strings.Repeat("#=", 100000),
		"long string":     "\"" + strings.Repeat("a", 1<<20),
		"many lines":      strings.Repeat("x\n", 200000),
		"elseif chain":    "if a\n" + strings.Repeat("elseif a\n", 50000) + "end",
		"ternary chain":   strings.Repeat("a ? b : ", 50000) + "c",
	} {
		bound := 5 * time.Second
		if raceEnabled {
			bound = 120 * time.Second
		}
		t0 := time.Now()
		ex := ParseAll([]byte(src), "hostile")
		if d := time.Since(t0); d > bound {
			t.Errorf("%s: %v", name, d)
		}
		if ex == nil {
			t.Errorf("%s: nil", name)
		}
	}
}

// TestGoldenWithinFuzzLimits makes sure the golden snippets parse within the
// fuzz limits too, so that the fuzz seeds exercise the parser, not the
// limits.
func TestGoldenWithinFuzzLimits(t *testing.T) {
	for _, sn := range readGolden[gen.Snippet](t, "snippets.jsonl") {
		if _, err := ParseAllLimits([]byte(sn.Source()), "none", fuzzLimits); err != nil {
			t.Errorf("%q: %v", sn.Source(), err)
		}
	}
}
