package syntax

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

func TestSourceIndexing(t *testing.T) {
	s := NewSourceFile("aé\xffb", "", 1, 1)
	if s.nextind(0) != 1 || s.nextind(10) != 11 || s.nextind(2) != 4 || s.nextind(3) != 4 {
		t.Fatal("nextind")
	}
	if s.thisindRaw(0) != 0 || s.thisindRaw(3) != 2 || s.thisindRaw(4) != 4 {
		t.Fatal("thisind")
	}
	s2 := NewSourceFile("\x80\x80\x80\x80\x80", "", 1, 1)
	if s2.thisindRaw(5) != 5 {
		t.Fatal("thisind among continuation bytes")
	}
	if s3 := NewSourceFile("a\x80", "", 1, 1); s3.thisindRaw(2) != 2 || s3.nextind(2) != 3 {
		t.Fatal("stray continuation byte")
	}
	if s.prevind(1) != 0 || s.prevind(4) != 2 {
		t.Fatal("prevind")
	}
	if s.slice(3, 2) != "" || s.slice(2, 2) != "é" {
		t.Fatal("slice")
	}
	lines := NewSourceFile("a\nb\n", "f", 1, 1)
	if lines.Line(1) != 1 || lines.Line(3) != 2 || lines.Line(5) != 2 || lines.Line(99) != 2 {
		t.Fatal("Line")
	}
}

// TestBuildExprOfErrors converts trees holding error nodes directly (the
// Core hook replaces them by julia's error Expr, but the conversion of
// error leaves and clauses is expr.jl's).
func TestBuildExprOfErrors(t *testing.T) {
	for src, want := range map[string]string{
		"(a b)":     "(:block :a (:error :b))",
		"x\xff":     "(:error \"invalid UTF-8 sequence: `\\xff`\")",
		"f(":        "(:call :f (:error))",
		"try x end": "(:try (:block {L 1 :f} :x) false false (:block (:error)))",
	} {
		s := newParseStream([]byte(src), 1, SyntaxVersion, DefaultLimits)
		newParseState(s).parseToplevel()
		s.validateTokens()
		if got := expr.Serialize(s.buildExpr("f", 1)); !strings.Contains(got, want) {
			t.Errorf("%q: %s does not contain %s", src, got, want)
		}
	}
}
