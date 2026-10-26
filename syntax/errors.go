// Ported from JuliaSyntax src/hooks.jl, src/syntax_tree.jl,
// src/parser_api.jl and src/parse_stream.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): the error path of the Core parser hook.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// syntaxNode is SyntaxNode: the green tree without trivia (error trivia
// excepted), with positions and parents.
type syntaxNode struct {
	parent   *syntaxNode
	children []*syntaxNode // nil for leaves
	leaf     bool
	head     SyntaxHead
	position int
	span     int
}

func (n *syntaxNode) firstByte() int { return n.position }
func (n *syntaxNode) lastByte() int  { return n.position + n.span - 1 }

// toSyntaxNode is _to_SyntaxNode with keep_parens=false.
func toSyntaxNode(txtbuf []byte, raw *greenNode, position int) *syntaxNode {
	if raw.leaf {
		return &syntaxNode{leaf: true, head: raw.head, position: position, span: raw.span}
	}
	cs := []*syntaxNode{}
	pos := position
	for _, rc := range raw.children {
		// FIXME: Allowing trivia is_error nodes here corrupts the tree layout.
		if !rc.head.isTrivia() || isError(rc.head.Kind) {
			cs = append(cs, toSyntaxNode(txtbuf, rc, pos))
		}
		pos += rc.span
	}
	if (raw.head.Kind == kParens || raw.head.Kind == kWrapper) && len(cs) == 1 {
		return cs[0]
	}
	node := &syntaxNode{children: cs, head: raw.head, position: position, span: raw.span}
	for _, c := range cs {
		c.parent = node
	}
	return node
}

// syntaxNodeToExpr is _to_expr(node).
func syntaxNodeToExpr(source *SourceFile, txtbuf []byte, n *syntaxNode) any {
	if n.leaf {
		// node.val is parse_julia_literal of the same bytes: reparsing
		// gives the same value.
		return leafToExpr(source, txtbuf, n.head, n.firstByte(), n.lastByte())
	}
	args := make([]any, len(n.children))
	ranges := make([][2]int, len(n.children))
	heads := make([]SyntaxHead, len(n.children))
	for i, c := range n.children {
		args[i] = syntaxNodeToExpr(source, txtbuf, c)
		ranges[i] = [2]int{c.firstByte(), c.lastByte()}
		heads[i] = c.head
	}
	return internalNodeToExpr(source, n.firstByte(), n.lastByte(), n.head, ranges, heads, args)
}

// firstError is _first_error.
func firstError(t *syntaxNode) (int, *syntaxNode) {
	if isError(t.head.Kind) {
		return 0, t
	}
	if !t.leaf {
		for i, c := range t.children {
			if isError(c.head.Kind) {
				return i + 1, c
			}
			if j, x := firstError(c); x != nil {
				return j, x
			}
		}
	}
	return 0, nil
}

// incompleteTag is _incomplete_tag: which construct an incomplete input
// was in the middle of.
func incompleteTag(n *syntaxNode, codelen int) string {
	i, c := firstError(n)
	if c == nil || c.lastByte() < codelen || codelen == 0 {
		if c != nil && c.head.Kind == kErrorEofMultiComment {
			// This is the one weird case where the token itself is an
			// incomplete error
			return "comment"
		}
		return "none"
	} else if c.firstByte() <= codelen && c.head.Kind != kErrorInvalidEscapeSequence {
		// "ErrorInvalidEscapeSequence" may be incomplete, so we don't
		// include it here as a hard error.
		return "none"
	}
	if c.head.Kind == kError && len(c.children) > 0 {
		for _, cc := range c.children {
			if cc.head.Kind == kError {
				return "other"
			}
		}
	}
	if c.parent == nil {
		return "other"
	}
	switch c.parent.head.Kind {
	case kStringNode, kVar:
		return "string"
	case kCmdstring:
		return "cmd"
	case kCharNode:
		return "char"
	case kBlock, kQuote, kLet, kTry:
		return "block"
	case kFor, kWhile, kFunction, kIf, kModule, kStruct:
		if i == 1 {
			return "other"
		}
		return "block"
	case kDo:
		if i < 3 {
			return "other"
		}
		return "block"
	}
	return "other"
}

func hasNestedError(v any) bool {
	switch x := v.(type) {
	case *expr.Expr:
		if x.Head == "error" {
			return true
		}
		for _, a := range x.Args {
			if hasNestedError(a) {
				return true
			}
		}
	case expr.QuoteNode:
		return hasNestedError(x.Value)
	}
	return false
}

// lastNonWhitespaceByte returns the last non-whitespace byte parsed.
func (s *parseStream) lastNonWhitespaceByte() int {
	// The loop stops at the latest on the sentinel token (index 1, a
	// TOMBSTONE), whose next byte is the first byte: first_byte - 1 is then
	// returned, as parse_stream.jl's fallback does.
	i := len(s.tokens) - 1
	for ; i > 1; i-- {
		switch s.tokens[i].head.Kind {
		case kComment, kWhitespace, kNewlineWs, kErrorEofMultiComment:
			continue
		}
		break
	}
	return s.tokens[i].nextByte - 1
}

// parseErrorMessage is sprint(showerror, ParseError(stream; filename,
// first_line)) for a stream with at least one error: the diagnostics up to
// the first error. (The " some warnings detected:" form of showerror is for
// warnings-only ParseErrors, which the Core hook never builds.)
func (s *parseStream) parseErrorMessage(filename string, firstLine int) string {
	source := s.sourceFile(filename, firstLine)
	var b strings.Builder
	n := 0
	for !s.diagnostics[n].IsError() {
		n++
	}
	b.WriteString("ParseError:\n")
	showDiagnostics(&b, s.diagnostics[:n+1], source)
	return b.String()
}

// errorExpr is the error branch of core_parser_hook for options === :all.
func (s *parseStream) errorExpr(filename string, firstLine int) any {
	posBeforeComments := s.lastNonWhitespaceByte()
	source := s.sourceFile(filename, firstLine)
	tree := toSyntaxNode(s.textbuf, s.buildGreen(), s.firstByte())
	tag := incompleteTag(tree, posBeforeComments)
	head := expr.Symbol("incomplete")
	if tag == "none" {
		head = "error"
	}
	errorEx := expr.New(head, &expr.ParseError{Msg: s.parseErrorMessage(filename, firstLine)})
	args := []any{syntaxNodeToExpr(source, s.textbuf, tree)}
	fixupExprChildren(SyntaxHead{kNone, emptyFlags}, args)
	topex := args[0].(*expr.Expr) // the toplevel node parse_toplevel always emits
	// When encountering a toplevel error, the reference parser
	// * truncates the top level expression arg list before that error
	// * includes the last line number
	// * appends the error message
	i := len(topex.Args)
	for j, a := range topex.Args {
		if hasNestedError(a) {
			i = j
			break
		}
	}
	if i > 0 {
		if _, ok := topex.Args[i-1].(expr.LineNumberNode); ok {
			i--
		}
	}
	topex.Args = topex.Args[:i]
	line := firstLine
	if _, errort := firstError(tree); errort != nil {
		line = source.Line(errort.firstByte())
	}
	// LineNumberNode(line, filename::String) converts the name to a Symbol.
	topex.Args = append(topex.Args, expr.LineNumberNode{Line: line, File: expr.Symbol(filename)}, errorEx)
	return topex
}
