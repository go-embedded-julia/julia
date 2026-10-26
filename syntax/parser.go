// Ported from JuliaSyntax src/parser.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.
//
// The parse_* functions keep the names, order and comments of parser.jl
// (themselves mirroring the flisp parser), so that the two can be read side
// by side. Comments of the form `a ==> (tree)` are JuliaSyntax's own test
// notation for the tree each branch builds.

package syntax

// parseState wraps the stream to carry parser context as we recursively
// descend into the parse tree. It is passed by value, as Julia's immutable
// ParseState is.
type parseState struct {
	stream *parseStream
	// Disable range colon for parsing ternary conditional operator
	rangeColonEnabled bool
	// In space-sensitive mode "x -y" is 2 expressions, not a subtraction
	spaceSensitive bool
	// Seeing `for` stops parsing macro arguments and makes a generator
	forGenerator bool
	// Treat 'end' like a normal symbol instead of a reserved word
	endSymbol bool
	// Treat newline like ordinary whitespace instead of as a potential separator
	whitespaceNewline bool
	// Enable parsing `where` with high precedence
	whereEnabled bool
}

func newParseState(s *parseStream) parseState {
	return parseState{stream: s, rangeColonEnabled: true, whereEnabled: true}
}

func (ps parseState) normalContext() parseState {
	ps.rangeColonEnabled = true
	ps.spaceSensitive = false
	ps.whereEnabled = true
	ps.forGenerator = false
	ps.endSymbol = false
	ps.whitespaceNewline = false
	return ps
}

func (ps parseState) withSpaceSensitive() parseState {
	ps.spaceSensitive = true
	ps.whitespaceNewline = false
	return ps
}

// enter accounts one level of recursion and returns the stream, whose leave
// method the caller defers. Exceeding Limits.MaxDepth aborts the parse.
func (ps parseState) enter() *parseStream {
	s := ps.stream
	s.depth++
	if s.depth > s.limits.MaxDepth {
		panic(parseLimitError{"expression nesting exceeds the parser's depth limit"})
	}
	return s
}

func (s *parseStream) leave() { s.depth-- }

// Convenient wrappers for ParseStream

func (ps parseState) peek(n int) Kind { return ps.stream.peek(n, ps.whitespaceNewline) }
func (ps parseState) peekNL(n int, skipNewlines bool) Kind {
	return ps.stream.peek(n, skipNewlines)
}
func (ps parseState) peekToken(n int) syntaxToken {
	return ps.stream.peekToken(n, ps.whitespaceNewline)
}
func (ps parseState) peekTokenNL(n int, skipNewlines bool) syntaxToken {
	return ps.stream.peekToken(n, skipNewlines)
}
func (ps parseState) peekFullToken() fullToken {
	return ps.stream.peekFullToken(1, ps.whitespaceNewline)
}
func (ps parseState) peekBehind() behind { return ps.stream.peekBehind(true, true) }
func (ps parseState) peekBehindNoParens() behind {
	return ps.stream.peekBehind(true, false)
}
func (ps parseState) peekBehindAt(pos streamPosition) behind { return ps.stream.peekBehindAt(pos) }
func (ps parseState) peekBehindPos() streamPosition          { return ps.stream.peekBehindPos(true, true) }

func (ps parseState) bump(flags RawFlags) streamPosition {
	return ps.stream.bump(flags, ps.whitespaceNewline, "", kNone)
}
func (ps parseState) bumpNL(flags RawFlags, skipNewlines bool) streamPosition {
	return ps.stream.bump(flags, skipNewlines, "", kNone)
}
func (ps parseState) bumpRemap(flags RawFlags, remap Kind) streamPosition {
	return ps.stream.bump(flags, ps.whitespaceNewline, "", remap)
}
func (ps parseState) bumpErr(flags RawFlags, msg string) streamPosition {
	return ps.stream.bump(flags, ps.whitespaceNewline, msg, kNone)
}

// bumpTrivia is bump_trivia(ps): skips newlines by default.
func (ps parseState) bumpTrivia() streamPosition {
	return ps.stream.bumpTrivia(emptyFlags, true, "")
}
func (ps parseState) bumpTriviaNL(skipNewlines bool) streamPosition {
	return ps.stream.bumpTrivia(emptyFlags, skipNewlines, "")
}
func (ps parseState) bumpTriviaErr(flags RawFlags, skipNewlines bool, msg string) streamPosition {
	return ps.stream.bumpTrivia(flags, skipNewlines, msg)
}
func (ps parseState) bumpInvisible(k Kind, flags RawFlags, msg string) streamPosition {
	return ps.stream.bumpInvisible(k, flags, msg)
}
func (ps parseState) bumpGlue(k Kind, flags RawFlags) streamPosition {
	return ps.stream.bumpGlue(k, flags)
}
func (ps parseState) bumpSplit(specs ...splitSpec) streamPosition {
	return ps.stream.bumpSplit(specs...)
}
func (ps parseState) resetKind(pos streamPosition, k Kind) { ps.stream.resetNode(pos, &k, nil) }
func (ps parseState) resetFlags(pos streamPosition, f RawFlags) {
	ps.stream.resetNode(pos, nil, &f)
}
func (ps parseState) resetBoth(pos streamPosition, k Kind, f RawFlags) {
	ps.stream.resetNode(pos, &k, &f)
}
func (ps parseState) position() streamPosition { return ps.stream.position() }
func (ps parseState) emit(mark streamPosition, k Kind, flags RawFlags) streamPosition {
	return ps.stream.emit(mark, k, flags, "")
}
func (ps parseState) emitErr(mark streamPosition, k Kind, flags RawFlags, msg string) streamPosition {
	return ps.stream.emit(mark, k, flags, msg)
}

//------------------------------------------------------------------------------
// Parser Utils

// bumpClosingToken bumps an expected closing token. If not found, discard
// unexpected tokens until we find it or another closing token.
func (ps parseState) bumpClosingToken(closingKind Kind, hint string) {
	if ps.peek(1) == closingKind {
		ps.bumpTrivia()
		ps.bump(triviaFlag)
		return
	}
	errmsg := "Expected `" + closingKind.String() + "`" + hint
	// We didn't find the closing token. Read ahead in the stream
	mark := ps.position()
	ps.stream.diagMarks(mark, mark, levelError, errmsg)
	for {
		k := ps.peek(1)
		if ps.isClosingToken(k) && k != kComma && k != kSemicolon {
			break
		}
		ps.bump(emptyFlags)
	}
	// mark as trivia => ignore in AST.
	ps.emit(mark, kError, triviaFlag)
	if ps.peek(1) == closingKind {
		ps.bump(triviaFlag)
	}
}

// recover reads tokens until we find an expected closing token, bumping the
// pile of resulting tokens as a single nontrivia error token.
func (ps parseState) recover(isCloser func(parseState, Kind) bool, flags RawFlags, mark streamPosition, msg string) {
	for {
		k := ps.peek(1)
		if k == kEndMarker {
			ps.bumpInvisible(kError, triviaFlag, "premature end of input")
			break
		} else if isCloser(ps, k) {
			break
		}
		ps.bump(emptyFlags)
	}
	ps.emitErr(mark, kError, flags, msg)
}

// minSupportedVersion emits an error if the version is less than min.
func (ps parseState) minSupportedVersion(major, minor int, mark streamPosition, message string) {
	v := ps.stream.version
	if v[0] < major || (v[0] == major && v[1] < minor) {
		msg := message + " not supported in Julia version " + itoa(v[0]) + "." + itoa(v[1]) +
			" < " + itoa(major) + "." + itoa(minor)
		ps.emitErr(mark, kError, emptyFlags, msg)
	}
}

// bumpDisallowedSpace is flisp's disallow-space.
func (ps parseState) bumpDisallowedSpace() {
	if ps.peekToken(1).precedingWhitespace {
		ps.bumpTriviaErr(triviaFlag, false, "whitespace is not allowed here")
	}
}

func (ps parseState) bumpSemicolonTrivia() {
	for k := ps.peek(1); k == kSemicolon || k == kNewlineWs; k = ps.peek(1) {
		ps.bump(triviaFlag)
	}
}

//------------------------------------------------------------------------------
// Parsing-specific predicates on tokens/kinds

func isPlainEquals(t syntaxToken) bool { return t.head.Kind == kEq && !t.head.isDecorated() }

func (ps parseState) isClosingToken(k Kind) bool {
	switch k {
	case kElse, kElseif, kCatch, kFinally, kComma, kRparen, kRsquare, kRbrace, kSemicolon, kEndMarker:
		return true
	}
	return k == kEnd && !ps.endSymbol
}

func (ps parseState) isBlockContinuationKeyword(k Kind) bool {
	return isBlockContinuationKeyword(k) && !(ps.endSymbol && k == kEnd)
}

func isCloserOrNewline(ps parseState, k Kind) bool {
	return ps.isClosingToken(k) || k == kNewlineWs
}

func (ps parseState) isInitialReservedWord(k Kind) bool {
	switch k {
	case kBegin:
		// `begin` means firstindex(a) inside a[...]
		return !ps.endSymbol
	case kWhile, kIf, kFor, kTry, kReturn, kBreak, kContinue, kFunction, kMacro, kQuote,
		kLet, kLocal, kGlobal, kConst, kDo, kStruct, kModule, kBaremodule, kUsing, kImport, kExport:
		return true
	}
	return false
}

func isReservedWord(k Kind) bool { return isKeyword(k) && !isContextualKeyword(k) }

// peekInitialReservedWords reports whether the next word (or word pair) is
// reserved, introducing a syntactic structure.
func (ps parseState) peekInitialReservedWords() bool {
	k := ps.peek(1)
	if ps.isInitialReservedWord(k) {
		return true
	} else if isContextualKeyword(k) {
		k2 := ps.peekNL(2, false)
		return (k == kMutable && k2 == kStruct) ||
			(k == kPrimitive && k2 == kType) ||
			(k == kAbstract && k2 == kType)
	}
	return false
}

func isBlockForm(k Kind) bool {
	switch k {
	case kBlock, kQuote, kIf, kFor, kWhile, kLet, kFunction, kMacro, kAbstract, kPrimitive, kStruct, kTry, kModule:
		return true
	}
	return false
}

func isSyntacticUnaryOp(k Kind) bool { return k == kDollar || k == kAmp || k == kDecl }

func isTypeOperator(h SyntaxHead) bool {
	return (h.Kind == kSubtype || h.Kind == kSupertype) && !h.isDotted()
}

func isUnaryOp(h SyntaxHead) bool {
	k := h.Kind
	if h.isSuffixed() {
		return false
	}
	if (k == kSubtype || k == kSupertype) && !h.isDotted() {
		return true
	}
	switch k {
	case kPlus, kMinus, kNot, kTilde, kNotSign, kSqrt, kCbrt, kFourthRoot, kStarOp, kPlusMinus, kMinusPlus:
		return true
	}
	return false
}

// isBothUnaryAndBinary: operators that are both unary and binary.
func isBothUnaryAndBinary(h SyntaxHead) bool {
	k := h.Kind
	if h.isSuffixed() {
		return false
	}
	switch k {
	case kPlus, kMinus, kStarOp, kPlusMinus, kMinusPlus:
		return true
	case kDollar, kAmp, kTilde:
		return !h.isDotted()
	}
	return false
}

func isStringMacroSuffix(k Kind) bool {
	return k == kIdentifier || isKeyword(k) || isWordOperator(k) || isNumber(k)
}

// isValidIdentifier is flisp's invalid-identifier? negated.
func isValidIdentifier(k Kind) bool {
	return !(isSyntacticOperator(k) || k == kQuestion || k == kDotPrime)
}

// wasEventuallyCall: the expression is a call after stripping `where` and `::`.
func (ps parseState) wasEventuallyCall() bool {
	stream := ps.stream
	p := ps.peekBehindPos()
	for {
		b := stream.peekBehindAt(p)
		if b.kind == kCall {
			return true
		} else if b.kind == kWhere || b.kind == kParens || (b.kind == kDecl && b.flags.has(infixFlag)) {
			// parser.jl also returns false here for an infix `::` whose
			// last_child_position equals p. That cannot happen: p names a
			// range, and last_child_position returns either a token
			// (range_index 0) or an earlier range, so the test is omitted.
			p = stream.firstChildPosition(p)
		} else {
			return false
		}
	}
}

// bumpDotsplit bumps an operator token, splitting a dotted operator into
// `.` and the operator.
func (ps parseState) bumpDotsplit(flags RawFlags, emitDotNode bool, remap Kind) streamPosition {
	t := ps.peekToken(1)
	if t.head.isDotted() {
		ps.bumpTrivia()
		mark := ps.position()
		k := t.head.Kind
		if remap != kNone {
			k = remap
		}
		pos := ps.bumpSplit(splitSpec{1, kDot, triviaFlag}, splitSpec{-1, k, flags})
		if emitDotNode {
			pos = ps.emit(mark, kDot, emptyFlags)
		}
		return pos
	}
	if remap != kNone {
		return ps.bumpRemap(emptyFlags, remap)
	}
	return ps.bump(emptyFlags)
}

func callKind(h SyntaxHead) Kind {
	if h.isDotted() {
		return kDotcall
	}
	return kCall
}

//------------------------------------------------------------------------------
// Parser
//
// The `parse_*` functions are listed here roughly in order of increasing
// precedence (lowest to highest binding power).

// parseLtoR parses a left-to-right binary operator, producing structures
// like (+ (+ (+ 2 3) 4) 5). flisp: parse-LtoR
func (ps parseState) parseLtoR(down func(parseState), isOp func(Kind) bool) {
	mark := ps.position()
	down(ps)
	for isOp(ps.peek(1)) {
		t := ps.peekToken(1)
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		down(ps)
		ps.emit(mark, callKind(t.head), infixFlag)
	}
}

// parseRtoL parses a right-to-left binary operator, producing structures
// like (=> a (=> b (=> c d))). flisp: parse-RtoL
func (ps parseState) parseRtoL(down func(parseState), isOp func(Kind) bool, self func(parseState)) {
	defer ps.enter().leave()
	mark := ps.position()
	down(ps)
	t := ps.peekToken(1)
	if isOp(t.head.Kind) {
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		self(ps)
		ps.emit(mark, callKind(t.head), infixFlag)
	}
}

// parseNary parses block-like structures. It returns true if the block was
// nontrivial and a node needs to be emitted by the caller. flisp: parse-Nary
func (ps parseState) parseNary(down func(parseState), delimiters, closing func(Kind) bool) bool {
	ps.bumpTrivia()
	k := ps.peek(1)
	if closing(k) {
		return true
	}
	nDelims := 0
	if !delimiters(k) {
		// a ; b  ==>  (block a b)
		down(ps)
	}
	for delimiters(ps.peek(1)) {
		ps.bump(triviaFlag)
		nDelims++
		k = ps.peek(1)
		if k == kEndMarker || closing(k) {
			break
		} else if delimiters(k) {
			// ignore empty delimited sections
			continue
		}
		down(ps)
	}
	return nDelims != 0
}

// parseToplevel parses a sequence of top level statements separated by
// newlines, all wrapped in a toplevel node.
func (ps parseState) parseToplevel() {
	mark := ps.position()
	for {
		if ps.peekNL(1, true) == kEndMarker {
			// Allow end of input if there is nothing left but whitespace
			ps.bumpTrivia()
			break
		}
		ps.bumpTrivia()
		ps.parseStmts()
	}
	ps.emit(mark, kToplevel, emptyFlags)
}

func isNewlineOrSemicolon(k Kind) bool { return k == kNewlineWs || k == kSemicolon }
func isBlockCloser(k Kind) bool {
	return k == kEnd || k == kElse || k == kElseif || k == kCatch || k == kFinally
}

// parseBlock parses a newline or semicolon-delimited list of expressions.
// flisp: parse-block
func (ps parseState) parseBlock(down func(parseState), mark streamPosition) {
	ps.parseBlockInner(down)
	ps.emit(mark, kBlock, emptyFlags)
}

func (ps parseState) parseBlockDefault() {
	ps.parseBlock(parseState.parseEq, ps.position())
}

// parseBlockInner parses a block, but leaves emitting the block up to the
// caller.
func (ps parseState) parseBlockInner(down func(parseState)) bool {
	return ps.parseNary(down, isNewlineOrSemicolon, isBlockCloser)
}

// parseStmts: ";" at the top level produces a sequence of top level
// expressions. flisp: parse-stmts
func (ps parseState) parseStmts() {
	mark := ps.position()
	doEmit := ps.parseNary(parseState.parsePublic,
		func(k Kind) bool { return k == kSemicolon },
		func(k Kind) bool { return k == kNewlineWs })
	// check for unparsed junk after an expression
	junkMark := ps.position()
	for k := ps.peek(1); k != kEndMarker && k != kNewlineWs; k = ps.peek(1) {
		// Error recovery
		ps.bump(emptyFlags)
	}
	if junkMark != ps.position() {
		// x y  ==>  x (error-t y)
		ps.emitErr(junkMark, kError, triviaFlag, "extra tokens after end of expression")
	}
	if doEmit {
		ps.emit(mark, kToplevel, toplevelSemicolonFlag)
	}
}

// parsePublic parses `public foo, bar`, only from toplevel contexts.
func (ps parseState) parsePublic() {
	v := ps.stream.version
	if (v[0] > 1 || (v[0] == 1 && v[1] >= 11)) && ps.peek(1) == kPublic {
		switch ps.peek(2) {
		case kLparen, kEq, kLsquare:
			// this branch is for compatibility with use of public as a
			// non-keyword.
			ps.stream.diagNext(false, levelWarning, "using public as an identifier is deprecated")
		default:
			ps.parseResword()
			return
		}
	}
	ps.parseDocstring(parseState.parseEq)
}

// parseDocstring parses docstrings attached by a space or single newline.
// flisp: parse-docstring
func (ps parseState) parseDocstring(down func(parseState)) {
	mark := ps.position()
	down(ps)
	if ps.peekBehind().kind == kStringNode {
		isDoc := true
		k := ps.peek(1)
		if ps.isClosingToken(k) {
			// "notdoc" ] ==> (string "notdoc")
			isDoc = false
		} else if k == kNewlineWs {
			k2 := ps.peek(2)
			if ps.isClosingToken(k2) || k2 == kNewlineWs {
				// "notdoc" \n]      ==> (string "notdoc")
				// "notdoc" \n\n foo ==> (string "notdoc")
				isDoc = false
			} else {
				// Allow a single newline
				// "doc" \n foo ==> (doc (string "doc") foo)
				ps.bump(triviaFlag) // NewlineWs
			}
		}
		if isDoc {
			down(ps)
			ps.emit(mark, kDoc, emptyFlags)
		}
	}
}

// parseEq parses assignments with comma separated lists on each side.
// flisp: parse-eq
func (ps parseState) parseEq() {
	ps.parseAssignment(parseCommaEmit)
}

func parseCommaEmit(ps parseState) { ps.parseComma(true) }

// parseEqStar is used where commas are special, for example in an argument
// list. flisp: parse-eq*
func (ps parseState) parseEqStar() {
	k := ps.peek(1)
	k2 := ps.peek(2)
	if (isLiteral(k) || k == kIdentifier) && (k2 == kComma || k2 == kRparen || k2 == kRbrace || k2 == kRsquare) {
		// optimization: skip checking the whole precedence stack if we have
		// a simple token followed by a common closing token
		ps.bump(emptyFlags)
	} else {
		ps.parseAssignment(parseState.parsePair)
	}
}

// parseAssignment: a = b  ==>  (= a b). flisp: parse-assignment
func (ps parseState) parseAssignment(down func(parseState)) {
	defer ps.enter().leave()
	mark := ps.position()
	down(ps)
	ps.parseAssignmentWithInitialEx(mark, down)
}

func (ps parseState) parseAssignmentWithInitialEx(mark streamPosition, down func(parseState)) {
	t := ps.peekToken(1)
	k := t.head.Kind
	if !isPrecAssignment(k) {
		return
	}
	if k == kTilde {
		if ps.spaceSensitive && t.precedingWhitespace && !ps.peekToken(2).precedingWhitespace {
			// Unary ~ in space sensitive context is not assignment precedence
			// [a ~b]  ==>  (hcat a (call-pre ~ b))
			return
		}
		// ~ is currently the only assignment-precedence operator which is
		// parsed as a call.
		// a ~ b      ==>  (call-i a ~ b)
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		ps.bumpTrivia()
		ps.parseAssignment(down)
		ps.emit(mark, callKind(t.head), infixFlag)
		return
	}
	// f() = 1  ==>  (function-= (call f) 1)
	isShortFormFunc := k == kEq && !t.head.isDotted() && ps.wasEventuallyCall()
	if k == kOpEq {
		// x += y   ==>  (op= x + y)
		ps.bumpTrivia()
		if t.head.isDotted() {
			ps.bumpSplit(splitSpec{1, kDot, triviaFlag},
				splitSpec{-2, kIdentifier, emptyFlags}, // op
				splitSpec{1, kEq, triviaFlag})
		} else {
			ps.bumpSplit(splitSpec{-1, kIdentifier, emptyFlags}, // op
				splitSpec{1, kEq, triviaFlag})
		}
	} else {
		ps.bump(triviaFlag)
	}
	ps.bumpTrivia()
	// Syntax Edition TODO: We'd like to call `down` here when
	// is_short_form_func is true, to prevent `f() = 1 = 2` from parsing.
	ps.parseAssignment(down)
	if isShortFormFunc {
		ps.emit(mark, kFunction, shortFormFunctionFlag)
	} else {
		ps.emit(mark, k, t.flags())
	}
}

// parseComma: commas outside parens, for example a = b,c. flisp: parse-comma
func (ps parseState) parseComma(doEmit bool) int {
	mark := ps.position()
	nCommas := 0
	ps.parsePair()
	for {
		if ps.peek(1) != kComma {
			if doEmit && nCommas >= 1 {
				ps.emit(mark, kTuple, emptyFlags)
			}
			return nCommas
		}
		ps.bump(triviaFlag)
		nCommas++
		if isPlainEquals(ps.peekToken(1)) {
			// Allow trailing comma before `=`
			// x, = xs  ==>  (tuple x)
			continue
		}
		ps.parsePair()
	}
}

// parsePair: a => b  ==>  (call-i a => b). flisp: parse-pair
func (ps parseState) parsePair() {
	ps.parseRtoL(parseState.parseCond, isPrecPair, parseState.parsePair)
}

// parseCond parses short form conditional expressions:
// a ? b : c ==> (? a b c). flisp: parse-cond
func (ps parseState) parseCond() {
	defer ps.enter().leave()
	mark := ps.position()
	ps.parseArrow()
	t := ps.peekToken(1)
	if t.head.Kind != kQuestion {
		return
	}
	if !t.precedingWhitespace {
		// a? b : c  => (? a (error-t) b c)
		ps.bumpInvisible(kError, triviaFlag, "space required before `?` operator")
	}
	ps.bump(triviaFlag) // ?
	t = ps.peekToken(1)
	if !t.precedingWhitespace {
		// a ?b : c  ==>  (? a (error-t) b c)
		ps.bumpInvisible(kError, triviaFlag, "space required after `?` operator")
	}
	inner := ps
	inner.rangeColonEnabled = false
	inner.parseEqStar()
	t = ps.peekToken(1)
	if !t.precedingWhitespace {
		// a ? b: c  ==>  (? a b (error-t) c)
		ps.bumpInvisible(kError, triviaFlag, "space required before `:` in `?` expression")
	}
	if t.head.Kind == kColon {
		ps.bump(triviaFlag)
	} else {
		// a ? b c  ==>  (? a b (error-t) c)
		ps.bumpInvisible(kError, triviaFlag, "`:` expected in `?` expression")
	}
	t = ps.peekTokenNL(1, true)
	if !t.precedingWhitespace {
		// a ? b :c  ==>  (? a b (error-t) c)
		ps.bumpInvisible(kError, triviaFlag, "space required after `:` in `?` expression")
	}
	// FIXME: This is a very specific case. Error recovery should be handled
	// more generally elsewhere.
	if ps.isBlockContinuationKeyword(t.head.Kind) {
		// a "continuation keyword" is likely to belong to the surrounding
		// code, so we abort early
		ps.bumpInvisible(kError, triviaFlag, "unexpected `"+t.head.Kind.String()+"`")
		ps.emit(mark, kIf, emptyFlags)
		return
	}
	ps.parseEqStar()
	ps.emit(mark, kQuestion, emptyFlags)
}

// parseArrow parses arrows. Like parseRtoL, but specialized for the -->
// syntactic operator. flisp: parse-arrow
func (ps parseState) parseArrow() {
	defer ps.enter().leave()
	mark := ps.position()
	ps.parseOr()
	t := ps.peekToken(1)
	k := t.head.Kind
	if isPrecArrow(k) {
		if k == kLongArrow && !t.head.isDecorated() {
			// x --> y   ==>  (--> x y)           # The only syntactic arrow
			ps.bump(triviaFlag)
			ps.parseArrow()
			ps.emit(mark, k, t.flags())
		} else {
			// x → y     ==>  (call-i x → y)
			ps.bumpDotsplit(emptyFlags, false, kIdentifier)
			ps.parseArrow()
			ps.emit(mark, callKind(t.head), infixFlag)
		}
	}
}

// parseLazyCond is like parseRtoL, but specialized for the version test of
// dotted operators.
func (ps parseState) parseLazyCond(down func(parseState), isOp func(Kind) bool, self func(parseState)) {
	defer ps.enter().leave()
	mark := ps.position()
	down(ps)
	t := ps.peekToken(1)
	k := t.head.Kind
	if isOp(k) {
		ps.bump(triviaFlag)
		self(ps)
		ps.emit(mark, k, t.flags())
		if t.head.isDotted() {
			ps.minSupportedVersion(1, 7, mark, "dotted operators `.||` and `.&&`")
		}
	}
}

// parseOr: x || y || z   ==>   (|| x (|| y z)). flisp: parse-or
func (ps parseState) parseOr() {
	ps.parseLazyCond(parseState.parseAnd, isPrecLazyOr, parseState.parseOr)
}

// parseAnd: x && y && z   ==>   (&& x (&& y z)). flisp: parse-and
func (ps parseState) parseAnd() {
	ps.parseLazyCond(parseComparisonPlain, isPrecLazyAnd, parseState.parseAnd)
}

func parseComparisonPlain(ps parseState) { ps.parseComparison(false) }

// parseComparison parses binary comparisons and comparison chains.
// flisp: parse-comparison
func (ps parseState) parseComparison(subtypeComparison bool) {
	mark := ps.position()
	if subtypeComparison && isReservedWord(ps.peek(1)) {
		// Recovery
		// struct try end  ==>  (struct (error (try)) (block))
		name := ps.peek(1).String()
		ps.bump(emptyFlags)
		ps.emitErr(mark, kError, emptyFlags, "Invalid type name `"+name+"`")
	} else {
		ps.parsePipeLt()
	}
	nComparisons := 0
	opPos := noPosition
	opDotted := false
	initialTok := ps.peekToken(1)
	for t := ps.peekToken(1); isPrecComparison(t.head.Kind); t = ps.peekToken(1) {
		nComparisons++
		opDotted = t.head.isDotted()
		opPos = ps.bumpDotsplit(emptyFlags, true, kIdentifier)
		ps.parsePipeLt()
	}
	if nComparisons == 1 {
		if isTypeOperator(initialTok.head) {
			// Type comparisons are syntactic
			// x <: y  ==>  (<: x y)
			ps.resetFlags(opPos, triviaFlag)
			ps.emit(mark, initialTok.head.Kind, emptyFlags)
		} else {
			// Normal binary comparisons
			// x < y    ==>  (call-i x < y)
			if opDotted {
				// x .<: y  ==>  (dotcall-i x <: y)
				ps.resetBoth(opPos, kTombstone, triviaFlag)
			}
			ps.emit(mark, callKind(initialTok.head), infixFlag)
		}
	} else if nComparisons > 1 {
		// Comparison chains
		// x < y < z    ==> (comparison x < y < z)
		ps.emit(mark, kComparison, emptyFlags)
	}
}

// parsePipeLt: x <| y <| z  ==>  (call-i x <| (call-i y <| z))
func (ps parseState) parsePipeLt() {
	ps.parseRtoL(parseState.parsePipeGt, isPrecPipeLt, parseState.parsePipeLt)
}

// parsePipeGt: x |> y |> z  ==>  (call-i (call-i x |> y) |> z)
func (ps parseState) parsePipeGt() {
	ps.parseLtoR(parseState.parseRange, isPrecPipeGt)
}

// parseRange parses ranges and postfix ...; colon is strange: 3 arguments
// with 2 colons yields one call. flisp: parse-range
func (ps parseState) parseRange() {
	mark := ps.position()
	ps.parseInvalidOps()
	initialTok := ps.peekToken(1)
	initialKind := initialTok.head.Kind
	if initialKind != kColon && isPrecColon(initialKind) {
		// a..b     ==>   (call-i a .. b)
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		ps.parseInvalidOps()
		ps.emit(mark, callKind(initialTok.head), infixFlag)
	} else if initialKind == kColon && ps.rangeColonEnabled {
		// a ? b : c:d   ==>   (? a b (call-i c : d))
		nColons := 0
		for ps.peek(1) == kColon {
			if ps.spaceSensitive && ps.peekToken(1).precedingWhitespace && !ps.peekToken(2).precedingWhitespace {
				// Tricky cases in space sensitive mode
				// [1 :a]      ==>  (hcat 1 (quote-: a))
				break
			}
			t2 := ps.peekToken(2)
			if (t2.head.Kind == kLess || t2.head.Kind == kGreater) && !t2.precedingWhitespace {
				// Error heuristic: we found `:>` or `:<` which are invalid
				// lookalikes for `<:` and `>:`.
				// a :> b   ==>  (call-i a (error : >) b)
				ps.bumpTriviaNL(false)
				emark := ps.position()
				ps.bumpRemap(emptyFlags, kIdentifier) // K":"
				ks := ps.peek(1).String()
				ps.bumpRemap(emptyFlags, kIdentifier) // K"<" or K">"
				ps.emitErr(emark, kError, emptyFlags, "Invalid `:"+ks+"` found, maybe replace with `"+ks+":`")
				ps.parseInvalidOps()
				ps.emit(mark, kCall, infixFlag)
				break
			}
			nColons++
			if nColons == 1 {
				ps.bumpRemap(emptyFlags, kIdentifier)
			} else {
				ps.bumpRemap(triviaFlag, kIdentifier)
			}
			hadNewline := ps.peek(1) == kNewlineWs
			t := ps.peekToken(1)
			if ps.isClosingToken(t.head.Kind) {
				// 1: }    ==>  (call-i 1 : (error))
				ps.bumpInvisible(kError, emptyFlags, "missing last argument in range expression")
				ps.emit(mark, kCall, infixFlag)
				ps.stream.diagNext(false, levelError, "found unexpected closing token")
				return
			}
			if hadNewline {
				// Error message for people coming from python
				// 1:\n2   ==> (call-i 1 : (error))
				ps.stream.diagNext(true, levelError, "line break after `:` in range expression")
				ps.bumpInvisible(kError, emptyFlags, "")
				ps.emit(mark, kCall, infixFlag)
				return
			}
			ps.parseInvalidOps()
			if nColons == 2 {
				ps.emit(mark, kCall, infixFlag)
				nColons = 0
			}
		}
		if nColons > 0 {
			ps.emit(mark, kCall, infixFlag)
		}
	}
	// x...     ==>  (... x)
	if ps.peek(1) == kDotDotDot {
		ps.bump(triviaFlag)
		ps.emit(mark, kDotDotDot, emptyFlags)
	}
}

// parseInvalidOps parses invalid binary operators, for better error trees.
// a--b  ==>  (call-i a (error) b)
func (ps parseState) parseInvalidOps() {
	mark := ps.position()
	ps.parseExpr()
	for t := ps.peekToken(1); t.head.Kind == kErrorInvalidOperator || t.head.Kind == kErrorStarStar; t = ps.peekToken(1) {
		ps.bumpTrivia()
		ps.bumpDotsplit(emptyFlags, false, kNone)
		ps.parseExpr()
		ps.emit(mark, callKind(t.head), infixFlag)
	}
}

// parseExpr: a - b - c  ==>  (call-i (call-i a - b) - c); a + b + c  ==>
// (call-i a + b c). flisp: parse-expr
func (ps parseState) parseExpr() {
	ps.parseWithChains(parseState.parseTerm, isPrecPlus, func(k Kind) bool { return k == kPlus || k == kPlusPlus })
}

// parseTerm: a * b * c  ==>  (call-i a * b c). flisp: parse-term
func (ps parseState) parseTerm() {
	ps.parseWithChains(parseState.parseRational, isPrecTimes, func(k Kind) bool { return k == kStar })
}

// parseWithChains parses left to right, combining any of chainOps into one
// call. flisp: parse-with-chains
func (ps parseState) parseWithChains(down func(parseState), isOp func(Kind) bool, chainOps func(Kind) bool) {
	mark := ps.position()
	down(ps)
	for t := ps.peekToken(1); isOp(t.head.Kind); t = ps.peekToken(1) {
		if ps.spaceSensitive && t.precedingWhitespace && isBothUnaryAndBinary(t.head) &&
			!ps.peekToken(2).precedingWhitespace {
			// The following is two elements of a hcat
			// [x +y]     ==>  (hcat x (call-pre + y))
			break
		}
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		down(ps)
		if chainOps(t.head.Kind) && !t.head.isDecorated() {
			// a + b + c    ==>  (call-i a + b c)
			ps.parseChain(down, t.head.Kind)
		}
		ps.emit(mark, callKind(t.head), infixFlag)
	}
}

// parseChain parses left to right chains of a given binary operator.
// flisp: parse-chain
func (ps parseState) parseChain(down func(parseState), opKind Kind) {
	for t := ps.peekToken(1); t.head.Kind == opKind && !t.head.isDecorated(); t = ps.peekToken(1) {
		if ps.spaceSensitive && t.precedingWhitespace && isBothUnaryAndBinary(t.head) &&
			!ps.peekToken(2).precedingWhitespace {
			// [x +y]  ==>  (hcat x (call-pre + y))
			break
		}
		ps.bump(triviaFlag)
		down(ps)
	}
}

// parseRational: x // y // z  ==>  (call-i (call-i x // y) // z)
func (ps parseState) parseRational() {
	ps.parseLtoR(parseState.parseShift, isPrecRational)
}

// parseShift: x >> y >> z  ==>  (call-i (call-i x >> y) >> z)
func (ps parseState) parseShift() {
	ps.parseLtoR(parseState.parseUnarySubtype, isPrecBitshift)
}

// parseUnarySubtype parses `<: A where B` as `<: (A where B)` (issue
// #21545). flisp: parse-unary-subtype
func (ps parseState) parseUnarySubtype() {
	defer ps.enter().leave()
	t := ps.peekToken(1)
	if isTypeOperator(t.head) {
		k2 := ps.peek(2)
		if ps.isClosingToken(k2) || k2 == kNewlineWs || k2 == kEq {
			// return operator by itself
			// <: )  ==>  <:
			ps.bump(emptyFlags)
		} else if k2 == kLbrace || k2 == kLparen {
			// parse <:{T}(x::T) or <:(x::T) like other unary operators
			ps.parseWhere(parseState.parseJuxtapose)
		} else {
			// <: x          ==>  (<:-pre x)
			mark := ps.position()
			ps.bump(triviaFlag)
			ps.parseUnarySubtype()
			ps.emit(mark, t.head.Kind, prefixOpFlag)
		}
	} else {
		ps.parseWhere(parseState.parseJuxtapose)
	}
}

// parseWhereChain: flisp parse-where-chain
func (ps parseState) parseWhereChain(mark streamPosition) {
	ps.whereEnabled = false
	for ps.peek(1) == kWhere {
		ps.bump(triviaFlag) // where
		ps.bumpTrivia()
		k := ps.peek(1)
		if k == kLbrace {
			// x where {T,S}  ==>  (where x (braces T S))
			m := ps.position()
			ps.bump(triviaFlag)
			ckind, cflags := ps.parseCat(kRbrace, ps.endSymbol)
			ps.emitBraces(m, ckind, cflags)
			ps.emit(mark, kWhere, emptyFlags)
		} else {
			// x where T     ==>  (where x T)
			ps.parseComparison(false)
			ps.emit(mark, kWhere, emptyFlags)
		}
	}
}

// parseWhere: flisp parse-where
func (ps parseState) parseWhere(down func(parseState)) {
	// `where` needs to be below unary for the following to work
	// +(x::T,y::T) where {T} = x
	mark := ps.position()
	down(ps)
	if ps.whereEnabled && ps.peek(1) == kWhere {
		ps.parseWhereChain(mark)
	}
}

// parseJuxtapose: juxtaposition, as in units and Field identities like `im`.
// flisp: parse-juxtapose
func (ps parseState) parseJuxtapose() {
	mark := ps.position()
	ps.parseUnary()
	nTerms := 1
	for {
		t := ps.peekToken(1)
		k := t.head.Kind
		prevK := ps.peekBehind().kind
		isJuxtapose := false
		if !t.precedingWhitespace &&
			(isNumber(prevK) ||
				(!isNumber(k) && // disallow "x.3" and "f(2)2"
					k != kAt && // disallow "x@y"
					!(isBlockForm(prevK) || isSyntacticUnaryOp(prevK) || ps.isInitialReservedWord(prevK)))) &&
			(!isOperator(k) || isRadicalOp(k)) &&
			!ps.isClosingToken(k) {
			if prevK == kStringNode || isStringDelim(t.head.Kind) {
				ps.bumpInvisible(kError, triviaFlag, "cannot juxtapose string literal")
				// JuliaLang/julia#20575
				// Error, but assume juxtapose for recovery
				isJuxtapose = true
			} else if !ps.isInitialReservedWord(k) {
				// 2x       ==>  (juxtapose 2 x)
				isJuxtapose = true
			}
		}
		if !isJuxtapose {
			// x.3       ==>  x
			break
		}
		if isRadicalOp(t.head.Kind) {
			ps.parseUnary()
		} else {
			ps.parseFactor()
		}
		nTerms++
	}
	if nTerms > 1 {
		ps.emit(mark, kJuxtapose, emptyFlags)
	}
}

// bracketOpts is the named tuple an after_parse callback of parse_brackets
// returns, plus the delim_flags parse_brackets adds. Each caller uses its
// own subset of the fields.
type bracketOpts struct {
	needsParameters     bool
	isCall              bool // is_paren_call, is_tuple
	isBlock             bool
	isAnonFunc          bool
	parsedCall          bool
	needsParseCall      bool
	maybeGroupingParens bool
	numSubexprs         int
	simpleInterp        bool
	delimFlags          RawFlags
}

// parseUnary parses numeric literal prefixes, calls to unary operators and
// prefix calls involving arbitrary operators with bracketed arglists.
// flisp: parse-unary, parse-unary-call
func (ps parseState) parseUnary() {
	defer ps.enter().leave()
	mark := ps.position()
	ps.bumpTrivia()
	opT := ps.peekToken(1)
	opK := opT.head.Kind
	if !isOperator(opK) || isWordOperator(opK) || opK == kColon || opK == kPrime || opK == kDotPrime ||
		(isSyntacticUnaryOp(opK) && !opT.head.isDotted()) || isSyntacticOperator(opK) {
		// `op_t` is not an initial operator
		// :T      ==>  (quote-: T)
		ps.parseFactor()
		return
	}
	t2 := ps.peekToken(2)
	k2 := t2.head.Kind
	if (opK == kMinus || opK == kPlus) && !opT.head.isDecorated() {
		if !t2.precedingWhitespace && (k2 == kInteger || k2 == kFloat || k2 == kFloat32 ||
			(opK == kPlus && (k2 == kBinInt || k2 == kHexInt || k2 == kOctInt))) {
			k3 := ps.peek(3)
			if isPrecPower(k3) || k3 == kLsquare || k3 == kLbrace {
				// `[`, `{` (issue #18851) and `^` have higher precedence than
				// unary negation
				// -2^x      ==>  (call-pre - (call-i 2 ^ x))
				ps.bumpRemap(emptyFlags, kIdentifier)
				ps.parseFactor()
				ps.emit(mark, kCall, prefixOpFlag)
			} else {
				// We have a signed numeric literal. Glue the operator to the
				// next token to create a signed literal:
				// -2      ==>  -2
				ps.bumpGlue(k2, emptyFlags)
				ps.parseFactorWithInitialEx(mark)
			}
			return
		}
	}
	if ps.isClosingToken(k2) || k2 == kNewlineWs || k2 == kEq {
		// Standalone operators parsed as `op` or `(. op)`
		// +)   ==>  +
		ps.parseAtom(true, false)
	} else if k2 == kLbrace || (!isUnaryOp(opT.head) && k2 == kLparen) {
		// Call with type parameters or non-unary prefix call
		// +{T}(x::T)  ==>  (call (curly + T) (:: x T))
		ps.parseFactor()
	} else if k2 == kLparen {
		// Cases like +(a;b) are ambiguous: are they prefix calls to + with b
		// as a keyword argument, or is `a;b` a block?  We resolve this with
		// a simple heuristic: if there were any commas (or an initial splat),
		// it was a function call.
		opPos := ps.bumpDotsplit(emptyFlags, true, kIdentifier)

		spaceBeforeParen := t2.precedingWhitespace
		var wsMark, wsErrorPos, wsMarkEnd streamPosition
		if spaceBeforeParen {
			// Setup possible whitespace error between operator and (
			wsMark = ps.position()
			ps.bumpTrivia()
			wsErrorPos = ps.emit(wsMark, kTombstone, emptyFlags)
			wsMarkEnd = ps.position()
		}

		markBeforeParen := ps.position()
		ps.bump(triviaFlag) // (
		initialSemi := ps.peekNL(1, true) == kSemicolon
		opts := ps.parseBrackets(kRparen, true, func(hadCommas, hadSplat bool, numSemis, numSubexprs int) bracketOpts {
			isParenCall := hadCommas || hadSplat ||
				(initialSemi && numSubexprs > 0) ||
				(initialSemi && numSemis == 1) ||
				(numSemis == 0 && numSubexprs == 0)
			return bracketOpts{needsParameters: isParenCall, isCall: isParenCall,
				isBlock: !isParenCall && numSemis > 0}
		})

		// The precedence between unary + and any following infix ^ depends
		// on whether the parens are a function call or not
		if opts.isCall {
			if spaceBeforeParen {
				// Whitespace not allowed before prefix function call bracket
				// + (a,b)   ==> (call + (error) a b)
				ps.resetKind(wsErrorPos, kError)
				ps.stream.diagMarks(wsMark, wsMarkEnd, levelError,
					"whitespace not allowed between prefix function call and argument list")
			}
			// Prefix function calls for operators which are both binary and
			// unary
			// +(a,b)    ==>  (call + a b)
			if isTypeOperator(opT.head) {
				// <:(a,)  ==>  (<: a)
				ps.emit(mark, opK, opts.delimFlags)
				ps.resetBoth(opPos, opK, triviaFlag)
			} else {
				ps.emit(mark, kCall, opts.delimFlags)
			}
			ps.parseCallChain(mark, false)
			ps.parseFactorWithInitialEx(mark)
		} else {
			// Unary function calls with brackets as grouping, not an arglist
			// .+(a)    ==>  (dotcall-pre + (parens a))
			if opts.isBlock {
				// +(a;b)   ==>  (call-pre + (block-p a b))
				ps.emit(markBeforeParen, kBlock, parensFlag)
			} else {
				ps.emit(markBeforeParen, kParens, emptyFlags)
			}
			// Not a prefix operator call but a block; `=` is not `kw`
			// +(a=1)  ==>  (call-pre + (parens (= a 1)))
			// Unary operators have lower precedence than ^
			// +(a)^2  ==>  (call-pre + (call-i (parens a) ^ 2))
			ps.parseCallChain(markBeforeParen, false)
			ps.parseFactorWithInitialEx(markBeforeParen)
			if isTypeOperator(opT.head) {
				// <:(a)  ==>  (<:-pre (parens a))
				ps.emit(mark, opK, prefixOpFlag)
				ps.resetBoth(opPos, opK, triviaFlag)
			} else if opT.head.isDotted() {
				ps.emit(mark, kDotcall, prefixOpFlag)
				ps.resetKind(opPos, kTombstone)
			} else {
				ps.emit(mark, kCall, prefixOpFlag)
			}
		}
	} else {
		var opPos streamPosition
		if isUnaryOp(opT.head) {
			// Normal unary calls
			// +x  ==>  (call-pre + x)
			opPos = ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		} else {
			// /x     ==>  (call-pre (error /) x)
			ps.bumpDotsplit(emptyFlags, true, kIdentifier)
			opPos = ps.emitErr(mark, kError, emptyFlags, "not a unary operator")
		}
		ps.parseUnary()
		if isTypeOperator(opT.head) {
			ps.resetFlags(opPos, triviaFlag)
			ps.emit(mark, opK, prefixOpFlag)
		} else {
			ps.emit(mark, callKind(opT.head), prefixOpFlag)
		}
	}
}

// parseFactor handles ^ and .^. flisp: parse-factor
func (ps parseState) parseFactor() {
	mark := ps.position()
	ps.parseCall()
	ps.parseFactorWithInitialEx(mark)
}

// parseFactorWithInitialEx: flisp parse-factor-with-initial-ex
func (ps parseState) parseFactorWithInitialEx(mark streamPosition) {
	ps.parseDeclWithInitialEx(mark)
	if t := ps.peekToken(1); isPrecPower(t.head.Kind) {
		ps.bumpDotsplit(emptyFlags, false, kIdentifier)
		ps.parseFactorAfter()
		ps.emit(mark, callKind(t.head), infixFlag)
	}
}

// parseFactorAfter: flisp parse-factor-after
func (ps parseState) parseFactorAfter() {
	ps.parseRtoL(parseState.parseJuxtapose, isPrecPower, parseState.parseFactorAfter)
}

// parseDeclWithInitialEx parses type declarations and lambda syntax:
// a::b ==> (::-i a b), a->b ==> (-> a b).
// flisp: parse-decl-with-initial-ex
func (ps parseState) parseDeclWithInitialEx(mark streamPosition) {
	for ps.peek(1) == kDecl {
		// a::b::c   ==>   (::-i (::-i a b) c)
		ps.bump(triviaFlag)
		ps.parseWhere(parseState.parseCall)
		ps.emit(mark, kDecl, infixFlag)
	}
	if ps.peek(1) == kArrow {
		kb := ps.peekBehind().kind
		if kb == kTuple {
			// (x,y) -> z
		} else if kb == kWhere {
			// `where` and `->` have the "wrong" precedence when writing anon
			// functions. So ignore this case to allow use of grouping
			// brackets with `where`.
			// (x where T) -> y  ==>  (-> (x where T) y)
		} else {
			// x -> y    ==>  (-> (tuple x) y)
			ps.emit(mark, kTuple, emptyFlags)
		}
		ps.bump(triviaFlag)
		// -> is unusual: it binds tightly on the left and loosely on the
		// right.
		ps.parseEqStar()
		ps.emit(mark, kArrow, emptyFlags)
	}
}

// parseCall parses function call, indexing, dot, and transpose expressions;
// it also handles looking for syntactic reserved words. flisp: parse-call
func (ps parseState) parseCall() {
	if ps.peekInitialReservedWords() {
		ps.parseResword()
	} else {
		mark := ps.position()
		// f(x)   ==>  (call f x)
		ps.parseUnaryPrefix(false)
		ps.parseCallChain(mark, false)
	}
}

// parseUnaryPrefix parses syntactic unary operators: &a, ::a, $a.
// flisp: parse-unary-prefix
func (ps parseState) parseUnaryPrefix(hasUnaryPrefix bool) {
	defer ps.enter().leave()
	mark := ps.position()
	t := ps.peekToken(1)
	k := t.head.Kind
	if isSyntacticUnaryOp(k) && !t.head.isDotted() {
		k2 := ps.peek(2)
		if (k == kAmp || k == kDollar) && (ps.isClosingToken(k2) || k2 == kNewlineWs) {
			// &)   ==>  &
			ps.bump(emptyFlags)
		} else {
			ps.bump(triviaFlag)
			if k == kAmp || k == kDecl {
				// &a   ==>  (& a)
				ps.parseWhere(parseState.parseCall)
			} else {
				// $a   ==>  ($ a)
				ps.parseUnaryPrefix(true)
			}
			// Only need PREFIX_OP_FLAG for ::
			f := emptyFlags
			if k == kDecl {
				f = prefixOpFlag
			}
			ps.emit(mark, k, f)
		}
	} else {
		// .&(x,y)  ==>  (call .& x y)
		ps.parseAtom(true, hasUnaryPrefix)
	}
}
