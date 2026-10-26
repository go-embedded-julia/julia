package syntax

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

func TestOldSyntaxVersionExpr(t *testing.T) {
	saved := SyntaxVersion
	defer func() { SyntaxVersion = saved }()
	SyntaxVersion = [2]int{1, 5}
	ex := expr.Serialize(ParseAll([]byte("import A as B"), "none"))
	if !strings.Contains(ex, "`import ... as` not supported in Julia version 1.5 < 1.6") {
		t.Fatalf("version error missing: %s", ex)
	}
	SyntaxVersion = [2]int{1, 10}
	ex = expr.Serialize(ParseAll([]byte("public = 1"), "none"))
	if ex != `(:toplevel {L 1 :none} (:"=" :public 1))` {
		t.Fatalf("public as an identifier before 1.11: %s", ex)
	}
}

func TestFirstErrorAndIncompleteTag(t *testing.T) {
	errLeaf := func(pos int) *syntaxNode {
		return &syntaxNode{leaf: true, head: SyntaxHead{kError, emptyFlags}, position: pos, span: 0}
	}
	if i, c := firstError(errLeaf(1)); i != 0 || c == nil {
		t.Fatal("firstError of an error root")
	}
	node := func(k Kind, children ...*syntaxNode) *syntaxNode {
		n := &syntaxNode{head: SyntaxHead{k, emptyFlags}, children: children, position: 1, span: 10}
		for _, c := range children {
			c.parent = n
		}
		return n
	}
	ident := func() *syntaxNode {
		return &syntaxNode{leaf: true, head: SyntaxHead{kIdentifier, emptyFlags}, position: 1, span: 1}
	}
	errNode := func(children ...*syntaxNode) *syntaxNode {
		n := node(kError, children...)
		n.position, n.span = 10, 1
		return n
	}
	for _, tc := range []struct {
		tree *syntaxNode
		want string
	}{
		{node(kFor, errLeaf(10)), "other"},
		{node(kFor, ident(), errLeaf(10)), "block"},
		{node(kDo, ident(), errLeaf(10)), "other"},
		{node(kDo, ident(), ident(), errLeaf(10)), "block"},
		{node(kCmdstring, errLeaf(10)), "cmd"},
		{node(kQuote, errLeaf(10)), "block"},
		{node(kCall, errLeaf(10)), "other"},
		{errLeaf(10), "other"},
		{node(kToplevel, errNode(errLeaf(10))), "other"},
		{node(kToplevel, errNode(ident())), "other"},
	} {
		if got := incompleteTag(tc.tree, 5); got != tc.want {
			t.Errorf("%v: incompleteTag = %s, want %s", tc.tree.head.Kind, got, tc.want)
		}
	}
	if got := incompleteTag(node(kToplevel, ident()), 5); got != "none" {
		t.Errorf("no error: %s", got)
	}
}

func TestDiagnosticDisplayHelpers(t *testing.T) {
	if isSpaceChar(expr.Char(0xff000000)) || !isSpaceChar(expr.Char(' '<<24)) {
		t.Fatal("isSpaceChar")
	}
	var b strings.Builder
	printMarkerLine(&b, "", "\n", false, false, "", false)
	if b.String() != "┌" {
		t.Fatalf("zero-width marker: %q", b.String())
	}
}
