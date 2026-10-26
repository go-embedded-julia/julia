// Ported from JuliaSyntax src/parse_stream.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): the head and stream accessors the Expr conversion uses.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

func (f RawFlags) callType() RawFlags { return f & 0b11000 }
func (f RawFlags) numeric() int       { return int((f >> 8) & 0xff) }

func (h SyntaxHead) isPrefixCall() bool  { return h.Flags.callType() == prefixCallFlag }
func (h SyntaxHead) isInfixOpCall() bool { return h.Flags.callType() == infixFlag }
func (h SyntaxHead) isPostfixOpCall() bool {
	return h.Flags.callType() == postfixOpFlag
}

// headName is untokenize(head; include_flag_suff=false), the name of the
// Expr head of an internal node: the kind's text, dotted if the head is.
// Internal nodes always have kinds of unique text, or error kinds.
func (h SyntaxHead) headName() string {
	str := h.Kind.String()
	if h.isDotted() {
		str = "." + str
	}
	return str
}

func (s *parseStream) firstByte() int { return s.tokens[1].nextByte }
func (s *parseStream) lastByte() int  { return s.nextByte() - 1 }
