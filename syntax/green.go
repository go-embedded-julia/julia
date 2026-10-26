// Ported from JuliaSyntax src/green_tree.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// greenNode is GreenNode: a lossless node covering span bytes, trivia
// included, without position or parent.
type greenNode struct {
	head     SyntaxHead
	span     int
	children []*greenNode // nil for leaves
	leaf     bool
}

// buildGreen is build_tree(GreenNode, stream).
func (s *parseStream) buildGreen() *greenNode {
	return buildTree(s, func(h SyntaxHead, first, last int, children []*greenNode, leaf bool) (*greenNode, bool) {
		if leaf {
			return &greenNode{head: h, span: last - first + 1, leaf: true}, true
		}
		return &greenNode{head: h, span: last - first + 1, children: append([]*greenNode{}, children...)}, true
	})
}

// GreenDump parses src as a file (parse! with rule=:all) and prints the
// lossless green tree — kind, flags and span of every node, trivia included
// — then the diagnostics, in the oracle's `green` format.
func GreenDump(src []byte) (string, error) {
	var out string
	err := guarded(func() {
		s := newParseStream(src, 1, SyntaxVersion, DefaultLimits)
		newParseState(s).parseToplevel()
		s.validateTokens()
		var b strings.Builder
		writeGreen(&b, s.buildGreen())
		for _, d := range s.diagnostics {
			b.WriteString("\n" + itoa(d.FirstByte) + " " + itoa(d.LastByte) + " " + d.Level + " ")
			b.WriteString(expr.Quote(d.Message))
		}
		out = b.String()
	})
	return out, err
}

func writeGreen(b *strings.Builder, g *greenNode) {
	b.WriteString("(" + g.head.Kind.String() + " " + strconv.Itoa(int(g.head.Flags)) + " " + itoa(g.span))
	for _, c := range g.children {
		b.WriteByte(' ')
		writeGreen(b, c)
	}
	b.WriteByte(')')
}
