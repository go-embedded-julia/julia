// Ported from JuliaSyntax src/parse_stream.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/go-embedded-julia/julia/expr"
)

// RawFlags hold auxiliary information about tokens and nonterminals which
// the Kind doesn't capture.
type RawFlags uint16

// Flag bits (parse_stream.jl). Several are head-specific and overlap.
const (
	emptyFlags   RawFlags = 0
	triviaFlag   RawFlags = 1 << 0
	dotopFlag    RawFlags = 1 << 1
	suffixedFlag RawFlags = 1 << 2

	prefixCallFlag RawFlags = 0 << 3
	infixFlag      RawFlags = 1 << 3
	prefixOpFlag   RawFlags = 2 << 3
	postfixOpFlag  RawFlags = 3 << 3

	tripleStringFlag      RawFlags = 1 << 5
	rawStringFlag         RawFlags = 1 << 6
	parensFlag            RawFlags = 1 << 5
	trailingCommaFlag     RawFlags = 1 << 6
	colonQuote            RawFlags = 1 << 5
	toplevelSemicolonFlag RawFlags = 1 << 5
	shortFormFunctionFlag RawFlags = 1 << 5
	mutableFlag           RawFlags = 1 << 5
	bareModuleFlag        RawFlags = 1 << 5

	numericFlags RawFlags = 0xff << 8
)

// setNumericFlags stores a small integer (the dimension of nrow/ncat) in
// the flags. JuliaSyntax raises an error when n does not fit; the parser
// never asks for more than the number of semicolons it has counted, which
// the lexer bounds, but an attacker-sized input could: the dimension is then
// clamped, and the input is already an error for Julia.
func setNumericFlags(n int) RawFlags {
	if n < 0 || n > 0xff {
		panic(parseLimitError{fmt.Sprintf("Numeric flags unable to hold large integer %d", n)})
	}
	return RawFlags(n<<8) & numericFlags
}

func (f RawFlags) has(t RawFlags) bool { return f&t != 0 }

// SyntaxHead combines the Kind of a syntactic construct with its flags.
type SyntaxHead struct {
	Kind  Kind
	Flags RawFlags
}

func (h SyntaxHead) isTrivia() bool    { return h.Flags.has(triviaFlag) }
func (h SyntaxHead) isDotted() bool    { return h.Flags.has(dotopFlag) }
func (h SyntaxHead) isSuffixed() bool  { return h.Flags.has(suffixedFlag) }
func (h SyntaxHead) isDecorated() bool { return h.isDotted() || h.isSuffixed() }

// syntaxToken is a token covering a contiguous byte range; only the index
// of the byte after it (1-based) is stored.
type syntaxToken struct {
	head                SyntaxHead
	origKind            Kind
	precedingWhitespace bool
	nextByte            int
}

// flags is flags(tok): the head flags without the numeric part.
func (t syntaxToken) flags() RawFlags { return t.head.Flags &^ numericFlags }

// taggedRange is a range of output tokens which will become a node.
type taggedRange struct {
	head       SyntaxHead
	firstToken int
	lastToken  int
}

// streamPosition is ParseStreamPosition.
type streamPosition struct {
	tokenIndex int // index of last token in output
	rangeIndex int
}

var noPosition = streamPosition{0, 0}

// parseStream is ParseStream: it lexes the input into tokens, manages
// insignificant whitespace on behalf of the parser, and stores output tokens
// and tree nodes in two arrays. Indices of tokens and ranges are 1-based as
// in Julia: element 0 of each slice is unused.
type parseStream struct {
	textbuf        []byte
	lexer          *Lexer
	lookahead      []syntaxToken
	lookaheadIndex int // 0-based into lookahead
	tokens         []syntaxToken
	ranges         []taggedRange
	diagnostics    []Diagnostic
	peekCount      int
	version        [2]int
	depth          int
	limits         Limits
}

func newParseStream(text []byte, index int, version [2]int, limits Limits) *parseStream {
	sentinel := syntaxToken{head: SyntaxHead{kTombstone, emptyFlags}, origKind: kTombstone, nextByte: index}
	// Julia source runs at about one token per three bytes and one range
	// per two tokens: sizing the output arrays up front saves their growth.
	tokens := make([]syntaxToken, 2, 16+len(text)/2)
	tokens[1] = sentinel
	ranges := make([]taggedRange, 1, 16+len(text)/6)
	return &parseStream{
		textbuf:   text,
		lexer:     NewLexer(text, index-1),
		lookahead: make([]syntaxToken, 0, 256),
		tokens:    tokens,
		ranges:    ranges,
		version:   version,
		limits:    limits,
	}
}

func (s *parseStream) lastToken() int { return len(s.tokens) - 1 }
func (s *parseStream) lastRange() int { return len(s.ranges) - 1 }

// tokenIsLast reports whether a token was emitted last at position pos.
func (s *parseStream) tokenIsLast(pos streamPosition) bool {
	return pos.rangeIndex == 0 || pos.tokenIndex > s.ranges[pos.rangeIndex].lastToken
}

func (s *parseStream) tokenFirstByte(i int) int { return s.tokens[i-1].nextByte }
func (s *parseStream) tokenLastByte(i int) int  { return s.tokens[i].nextByte - 1 }

// lookaheadTokenFirstByte takes a 0-based lookahead index.
func (s *parseStream) lookaheadTokenFirstByte(i int) int {
	if i == 0 {
		return s.nextByte()
	}
	return s.lookahead[i-1].nextByte
}

func (s *parseStream) lookaheadTokenLastByte(i int) int { return s.lookahead[i].nextByte - 1 }

// bufferLookaheadTokens lexes several tokens ahead.
func (s *parseStream) bufferLookaheadTokens() {
	hadWhitespace := false
	count := 0
	for {
		raw := s.lexer.NextToken()
		k := raw.Kind
		wasWhitespace := isWhitespace(k)
		hadWhitespace = hadWhitespace || wasWhitespace
		f := emptyFlags
		if raw.DotOp {
			f |= dotopFlag
		}
		if raw.Suffix {
			f |= suffixedFlag
		}
		s.lookahead = append(s.lookahead, syntaxToken{SyntaxHead{k, f}, k, hadWhitespace, raw.EndByte + 2})
		count++
		if k == kEndMarker {
			break
		}
		if !wasWhitespace {
			// Buffer tokens in batches for lookahead.
			hadWhitespace = false
			if count > 100 {
				break
			}
		}
	}
}

func (s *parseStream) nextByte() int { return s.tokens[len(s.tokens)-1].nextByte }

// lookaheadIndex finds the 0-based lookahead index of the nth next
// non-trivia token.
func (s *parseStream) lookaheadIndexOf(n int, skipNewlines bool) int {
	i := s.lookaheadIndex
	// Fast path, as parse_stream.jl's: the next token is usually not
	// trivia, or follows a single trivia token.
	if n == 1 && i+2 <= len(s.lookahead) {
		k := s.lookahead[i].head.Kind
		if !(k == kWhitespace || k == kComment || (k == kNewlineWs && skipNewlines)) {
			return i
		}
		k = s.lookahead[i+1].head.Kind
		if !(k == kWhitespace || k == kComment || (k == kNewlineWs && skipNewlines)) {
			return i + 1
		}
	}
	for {
		if i+1 > len(s.lookahead) {
			nDelete := s.lookaheadIndex
			if float64(nDelete) > 0.9*float64(len(s.lookahead)) {
				s.lookahead = append(s.lookahead[:0], s.lookahead[nDelete:]...)
				i -= nDelete
				s.lookaheadIndex = 0
			}
			s.bufferLookaheadTokens()
			continue
		}
		k := s.lookahead[i].head.Kind
		if !((k == kWhitespace || k == kComment) || (k == kNewlineWs && skipNewlines)) {
			if n == 1 {
				return i
			}
			n--
		}
		i++
	}
}

func (s *parseStream) stuckCheck() {
	s.peekCount++
	if s.peekCount > 100_000 {
		panic(parseLimitError{fmt.Sprintf("The parser seems stuck at byte %d", s.nextByte())})
	}
}

// peekToken is peek_token(stream, n; skip_newlines, skip_whitespace=true).
func (s *parseStream) peekToken(n int, skipNewlines bool) syntaxToken {
	s.stuckCheck()
	return s.lookahead[s.lookaheadIndexOf(n, skipNewlines)]
}

func (s *parseStream) peek(n int, skipNewlines bool) Kind {
	return s.peekToken(n, skipNewlines).head.Kind
}

type fullToken struct {
	head      SyntaxHead
	firstByte int
	lastByte  int
}

func (t fullToken) span() int { return 1 + t.lastByte - t.firstByte }

func (s *parseStream) peekFullToken(n int, skipNewlines bool) fullToken {
	s.stuckCheck()
	i := s.lookaheadIndexOf(n, skipNewlines)
	t := s.lookahead[i]
	return fullToken{t.head, s.lookaheadTokenFirstByte(i), s.lookaheadTokenLastByte(i)}
}

// behind is the result of peek_behind.
type behind struct {
	kind     Kind
	flags    RawFlags
	origKind Kind
	isLeaf   bool
}

func (s *parseStream) peekBehindAt(pos streamPosition) behind {
	if s.tokenIsLast(pos) && pos.tokenIndex > 0 {
		t := s.tokens[pos.tokenIndex]
		return behind{t.head.Kind, t.flags(), t.origKind, true}
	} else if s.lastRange() > 0 && pos.rangeIndex > 0 {
		r := s.ranges[pos.rangeIndex]
		return behind{r.head.Kind, r.head.Flags, kNone, false}
	}
	return behind{kNone, emptyFlags, kNone, true}
}

func (s *parseStream) firstChildPosition(pos streamPosition) streamPosition {
	ranges := s.ranges
	parent := ranges[pos.rangeIndex]
	// Find the first nontrivia range which is a child of this range but not
	// a child of the child
	c := 0
	for i := pos.rangeIndex - 1; i >= 1; i-- {
		if ranges[i].firstToken < parent.firstToken {
			break
		}
		if (c == 0 || ranges[i].firstToken < ranges[c].firstToken) && !ranges[i].head.isTrivia() {
			c = i
		}
	}
	// Find first nontrivia token
	t := 0
	for i := parent.firstToken; i <= parent.lastToken; i++ {
		if !s.tokens[i].head.isTrivia() {
			t = i
			break
		}
	}
	if c == 0 || (t != 0 && ranges[c].firstToken > t) {
		return streamPosition{t, 0}
	}
	return streamPosition{ranges[c].lastToken, c}
}

// peekBehindPos gets the last position in the stream "of interest",
// skipping parens nodes, deleted tokens and (if skipTrivia) whitespace.
func (s *parseStream) peekBehindPos(skipTrivia, skipParens bool) streamPosition {
	tokenIndex := s.lastToken()
	rangeIndex := s.lastRange()
	if skipParens {
		for rangeIndex >= 1 && s.ranges[rangeIndex].head.Kind == kParens {
			rangeIndex--
		}
	}
	lastTokenInNonterminal := 0
	if rangeIndex != 0 {
		lastTokenInNonterminal = s.ranges[rangeIndex].lastToken
	}
	for tokenIndex > lastTokenInNonterminal {
		t := s.tokens[tokenIndex]
		if t.head.Kind != kTombstone && (!skipTrivia || !t.head.isTrivia()) {
			break
		}
		tokenIndex--
	}
	return streamPosition{tokenIndex, rangeIndex}
}

func (s *parseStream) peekBehind(skipTrivia, skipParens bool) behind {
	return s.peekBehindAt(s.peekBehindPos(skipTrivia, skipParens))
}

// bumpUntilN copies lookahead tokens up to the 0-based index n to the
// output; flags and remap are applied to non-trivia tokens.
func (s *parseStream) bumpUntilN(n int, flags RawFlags, remap Kind) {
	if n < s.lookaheadIndex {
		return
	}
	for i := s.lookaheadIndex; i <= n; i++ {
		tok := s.lookahead[i]
		k := tok.head.Kind
		if k == kEndMarker {
			break
		}
		f := flags | tok.flags()
		trivia := isWhitespace(k)
		if trivia {
			f |= triviaFlag
		}
		outk := remap
		if trivia || remap == kNone {
			outk = k
		}
		s.tokens = append(s.tokens, syntaxToken{SyntaxHead{outk, f}, k, tok.precedingWhitespace, tok.nextByte})
	}
	s.lookaheadIndex = n + 1
	// Defuse the time bomb
	s.peekCount = 0
}

func (s *parseStream) position() streamPosition {
	return streamPosition{s.lastToken(), s.lastRange()}
}

// bump copies the current token from input to output.
func (s *parseStream) bump(flags RawFlags, skipNewlines bool, errMsg string, remap Kind) streamPosition {
	emark := s.position()
	s.bumpUntilN(s.lookaheadIndexOf(1, skipNewlines), flags, remap)
	if errMsg != "" {
		s.emit(emark, kError, flags, errMsg)
	}
	return s.position()
}

// bumpTrivia bumps comments and whitespace tokens preceding the next token.
func (s *parseStream) bumpTrivia(flags RawFlags, skipNewlines bool, errMsg string) streamPosition {
	emark := s.position()
	s.bumpUntilN(s.lookaheadIndexOf(1, skipNewlines)-1, emptyFlags, kNone)
	if errMsg != "" {
		s.emit(emark, kError, flags, errMsg)
	}
	return s.position()
}

// bumpInvisible bumps an invisible zero-width token into the output.
func (s *parseStream) bumpInvisible(k Kind, flags RawFlags, errMsg string) streamPosition {
	b := s.nextByte()
	s.tokens = append(s.tokens, syntaxToken{SyntaxHead{k, flags}, k, false, b})
	if errMsg != "" {
		s.diagRange(b, b-1, levelError, errMsg)
	}
	s.peekCount = 0
	return s.position()
}

// bumpGlue bumps two tokens, gluing them together into a single token.
func (s *parseStream) bumpGlue(k Kind, flags RawFlags) streamPosition {
	i := s.lookaheadIndex
	s.tokens = append(s.tokens, syntaxToken{SyntaxHead{k, flags}, k, false, s.lookahead[i+1].nextByte})
	s.lookaheadIndex += 2
	s.peekCount = 0
	return s.position()
}

type splitSpec struct {
	nbyte int
	kind  Kind
	flags RawFlags
}

// bumpSplit bumps the next token, splitting it into several pieces.
func (s *parseStream) bumpSplit(specs ...splitSpec) streamPosition {
	tok := s.lookahead[s.lookaheadIndex]
	s.lookaheadIndex++
	b := s.nextByte()
	toklen := tok.nextByte - b
	for _, sp := range specs {
		if sp.nbyte < 0 {
			b += toklen + sp.nbyte
		} else {
			b += sp.nbyte
		}
		orig := tok.head.Kind
		if sp.kind == kDot {
			orig = kDot
		}
		s.tokens = append(s.tokens, syntaxToken{SyntaxHead{sp.kind, sp.flags}, orig, false, b})
	}
	s.peekCount = 0
	return s.position()
}

// resetNode resets the kind and/or flags of an existing node in the output.
func (s *parseStream) resetNode(pos streamPosition, kind *Kind, flags *RawFlags) {
	if s.tokenIsLast(pos) {
		t := &s.tokens[pos.tokenIndex]
		if kind != nil {
			t.head.Kind = *kind
		}
		if flags != nil {
			t.head.Flags = *flags
		} else {
			t.head.Flags = t.flags()
		}
	} else {
		r := &s.ranges[pos.rangeIndex]
		if kind != nil {
			r.head.Kind = *kind
		}
		if flags != nil {
			r.head.Flags = *flags
		}
	}
}

// stealTokenBytes moves numbytes from the token at pos+1 to the token at
// pos; it reports whether the donor became empty (and dead).
func (s *parseStream) stealTokenBytes(pos streamPosition, numbytes int) bool {
	i := pos.tokenIndex
	t1 := &s.tokens[i]
	t2 := &s.tokens[i+1]
	t1.nextByte += numbytes
	empty := t1.nextByte == t2.nextByte
	if empty {
		t2.head = SyntaxHead{kTombstone, emptyFlags}
	}
	return empty
}

// emit emits a new node covering source bytes from mark to the end of the
// most recently bumped token.
func (s *parseStream) emit(mark streamPosition, k Kind, flags RawFlags, errMsg string) streamPosition {
	firstToken := mark.tokenIndex + 1
	r := taggedRange{SyntaxHead{k, flags}, firstToken, s.lastToken()}
	if errMsg != "" {
		fbyte := s.tokenFirstByte(firstToken)
		lbyte := s.tokenLastByte(s.lastToken())
		s.diagRange(fbyte, lbyte, levelError, errMsg)
	}
	s.ranges = append(s.ranges, r)
	return s.position()
}

func (s *parseStream) diagRange(first, last int, level, msg string) {
	s.diagnostics = append(s.diagnostics, newDiag(first, last, level, msg))
}

// diagNext emits a diagnostic at the position of the next token, or of the
// whitespace before it.
func (s *parseStream) diagNext(whitespace bool, level, msg string) {
	i := s.lookaheadIndexOf(1, true)
	begin, end := i, i
	if whitespace {
		// It's the whitespace which is the error. parse_stream.jl ends the
		// range at i when lookahead[i] is whitespace, which it never is: i
		// indexes a token found by skipping whitespace and newlines.
		begin = s.lookaheadIndex
		end = max(s.lookaheadIndex, i-1)
	}
	s.diagRange(s.lookaheadTokenFirstByte(begin), s.lookaheadTokenLastByte(end), level, msg)
}

// diagMark emits a diagnostic covering the output since mark.
func (s *parseStream) diagMark(mark streamPosition, trimWhitespace bool, level, msg string) {
	i := mark.tokenIndex
	j := s.lastToken()
	if trimWhitespace {
		for i < j && isWhitespace(s.tokens[j].head.Kind) {
			j--
		}
		for i+1 < j && isWhitespace(s.tokens[i+1].head.Kind) {
			i++
		}
	}
	s.diagRange(s.tokens[i].nextByte, s.tokens[j].nextByte-1, level, msg)
}

func (s *parseStream) diagMarks(mark, endMark streamPosition, level, msg string) {
	s.diagRange(s.tokens[mark.tokenIndex].nextByte, s.tokens[endMark.tokenIndex].nextByte-1, level, msg)
}

// validateTokens is validate_tokens: the post-processing pass which checks
// literals and attaches messages to error tokens.
func (s *parseStream) validateTokens() {
	txtbuf := s.textbuf
	toks := s.tokens
	for i := 2; i < len(toks); i++ {
		t := toks[i]
		k := t.head.Kind
		fbyte := toks[i-1].nextByte
		nbyte := t.nextByte
		errorKind := kNone
		switch {
		case k == kInteger || k == kBinInt || k == kOctInt || k == kHexInt:
		case k == kFloat || k == kFloat32:
			bits := 64
			if k == kFloat32 {
				bits = 32
			}
			x, code := parseFloatLiteral(bits, txtbuf, fbyte, nbyte)
			if code == floatOverflow {
				s.diagRange(fbyte, nbyte-1, levelError, "overflow in floating point literal")
				errorKind = kErrorNumericOverflow
			} else if code == floatUnderflow && x == 0 {
				s.diagRange(fbyte, nbyte-1, levelWarning, "underflow to zero in floating point literal")
			}
		case k == kChar:
			var b bytes.Buffer
			if unescapeJuliaString(&b, txtbuf, fbyte, nbyte, &s.diagnostics) {
				errorKind = kErrorInvalidEscapeSequence
			} else if _, n := expr.DecodeChar(b.Bytes()); n != b.Len() {
				errorKind = kErrorOverLongCharacter
				s.diagRange(fbyte, nbyte-1, levelError, "character literal contains multiple characters")
			}
		case k == kString && !t.head.Flags.has(rawStringFlag):
			var b bytes.Buffer
			if unescapeJuliaString(&b, txtbuf, fbyte, nbyte, &s.diagnostics) {
				errorKind = kErrorInvalidEscapeSequence
			}
		case isError(k) && k != kError:
			// Emit messages for non-generic token errors
			tokstr := string(txtbuf[fbyte-1 : nbyte-1])
			var msg string
			switch k {
			case kErrorInvisibleChar, kErrorUnknownCharacter, kErrorIdentifierStart:
				msg = tokenErrorDescriptions[k] + " " + reprChar(juliaChars(tokstr)[0])
			case kErrorInvalidUTF8, kErrorBidiFormatting:
				msg = tokenErrorDescriptions[k] + " " + reprString(tokstr)
			default:
				msg = tokenErrorDescriptions[k]
			}
			s.diagRange(fbyte, nbyte-1, levelError, msg)
		}
		if errorKind != kNone {
			toks[i].head = SyntaxHead{errorKind, emptyFlags}
		}
	}
	sort.SliceStable(s.diagnostics, func(a, b int) bool {
		return s.diagnostics[a].FirstByte < s.diagnostics[b].FirstByte
	})
}

// buildNode is the make_node callback of build_tree: children is nil for
// leaves.
type buildEntry[T any] struct {
	firstToken int
	node       T
}

// buildTree is build_tree(make_node, stream): a depth-first postorder walk of
// the output ranges. makeNode returns ok=false to drop a node. The children
// slice it receives is reused from call to call: a node that keeps its
// children must copy them.
func buildTree[T any](s *parseStream, makeNode func(h SyntaxHead, first, last int, children []T, leaf bool) (T, bool)) T {
	// The stack holds at most one entry per token: sized from the token
	// count, it never grows by copying.
	stack := make([]buildEntry[T], 0, len(s.tokens)/2+16)
	children := make([]T, 0, 64)
	tokens := s.tokens
	ranges := s.ranges
	i := 1
	j := 1
	for {
		lastToken := len(tokens) - 1
		if j <= len(ranges)-1 {
			lastToken = ranges[j].lastToken
		}
		// Process tokens to leaf nodes for all tokens used by the next
		// internal node
		for i <= lastToken {
			t := tokens[i]
			if t.head.Kind == kTombstone {
				i++
				continue // Ignore removed tokens
			}
			if node, ok := makeNode(t.head, tokens[i-1].nextByte, tokens[i].nextByte-1, nil, true); ok {
				stack = append(stack, buildEntry[T]{i, node})
			}
			i++
		}
		if j > len(ranges)-1 {
			break
		}
		// Process internal nodes which end at the current position
		for j <= len(ranges)-1 {
			r := ranges[j]
			if r.lastToken != lastToken {
				break
			}
			if r.head.Kind == kTombstone {
				j++
				continue
			}
			// Collect children from the stack for this internal node
			k := len(stack)
			for k > 0 && r.firstToken <= stack[k-1].firstToken {
				k--
			}
			children = children[:0]
			for _, e := range stack[k:] {
				children = append(children, e.node)
			}
			node, ok := makeNode(r.head, tokens[r.firstToken-1].nextByte, tokens[r.lastToken].nextByte-1, children, false)
			stack = stack[:k]
			if ok {
				stack = append(stack, buildEntry[T]{r.firstToken, node})
			}
			j++
		}
	}
	if len(stack) == 1 {
		return stack[0].node
	}
	children = children[:0]
	for _, e := range stack {
		children = append(children, e.node)
	}
	node, _ := makeNode(SyntaxHead{kWrapper, emptyFlags}, tokens[1].nextByte, tokens[len(tokens)-1].nextByte-1, children, false)
	return node
}
