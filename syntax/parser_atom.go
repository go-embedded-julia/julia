// Ported from JuliaSyntax src/parser.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): brackets, arrays, strings and atoms.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import "math"

// parseVect parses the suffix of comma-separated array expressions such as
// [x, suffix]. Consumes `closer`, but does not emit the AST node for the
// surrounding brackets. flisp: parse-vect
func (ps parseState) parseVect(closer Kind, prefixTrailingComma bool) (Kind, RawFlags) {
	// [x, y]        ==>  (vect x y)
	opts := ps.parseBrackets(closer, true, func(_, _ bool, _, numSubexprs int) bracketOpts {
		return bracketOpts{needsParameters: true, numSubexprs: numSubexprs}
	})
	delimFlags := opts.delimFlags
	if opts.numSubexprs == 0 && prefixTrailingComma {
		delimFlags |= trailingCommaFlag
	}
	return kVect, delimFlags
}

// parseGenerator parses generators.
// (x for a in as for b in bs) ==> (parens (generator x (iteration (in a as)) (iteration (in b bs))))
// flisp: parse-generator
func (ps parseState) parseGenerator(mark streamPosition) {
	for t := ps.peekToken(1); t.head.Kind == kFor; t = ps.peekToken(1) {
		if !t.precedingWhitespace {
			// ((x)for x in xs)  ==>  (parens (generator (parens x) (error) (iteration (in x xs))))
			ps.bumpInvisible(kError, triviaFlag, "Expected space before `for` in generator")
		}
		ps.bump(triviaFlag)
		iterMark := ps.position()
		ps.parseIterationSpecs()
		if ps.peek(1) == kIf {
			// (x for a in as if z) ==> (parens (generator x (filter (iteration (in a as)) z)))
			ps.bump(triviaFlag)
			ps.parseCond()
			ps.emit(iterMark, kFilter, emptyFlags)
		}
	}
	ps.emit(mark, kGenerator, emptyFlags)
}

// parseComprehension: flisp parse-comprehension
func (ps parseState) parseComprehension(mark streamPosition, closer Kind) (Kind, RawFlags) {
	// [x for a in as] ==> (comprehension (generator x (iteration (in a as))))
	ps.whitespaceNewline = true
	ps.spaceSensitive = false
	ps.endSymbol = false
	ps.parseGenerator(mark)
	ps.bumpClosingToken(closer, "")
	return kComprehension, emptyFlags
}

const typeminInt = math.MinInt

type arrayOrder int

const (
	orderUnknown arrayOrder = iota
	orderRowMajor
	orderColumnMajor
)

// parseArray parses array concatenation syntax with multiple semicolons.
// flisp: parse-array
func (ps parseState) parseArray(mark streamPosition, closer Kind, endIsSymbol bool) (Kind, RawFlags) {
	ps.endSymbol = endIsSymbol
	order := orderUnknown
	// Outer array parsing loop - parse chain of separators with descending
	// precedence such as
	//v1.7: [a ; b ;; c ;;; d ;;;; e] ==> (ncat-4 (ncat-3 (ncat-2 (ncat-1 a b) c) d) e)
	dim, bindingPower := ps.parseArraySeparator(&order)
	if bindingPower == typeminInt {
		// [x@y  ==>  (hcat x (error-t ✘ y))
		ps.bumpClosingToken(closer, "")
		return kHcat, emptyFlags
	}
	for {
		nextDim, nextBP := ps.parseArrayInner(bindingPower, &order)
		if nextBP == typeminInt {
			break
		}
		if bindingPower == 0 {
			ps.emit(mark, kRow, emptyFlags)
		} else {
			ps.emit(mark, kNrow, setNumericFlags(dim))
		}
		dim = nextDim
		bindingPower = nextBP
	}
	ps.bumpClosingToken(closer, "")
	switch bindingPower {
	case -1:
		return kVcat, emptyFlags
	case 0:
		return kHcat, emptyFlags
	}
	return kNcat, setNumericFlags(dim)
}

// parseArrayInner parses equal and ascending precedence chains of array
// concatenation operators - semicolons, newlines and whitespace.
func (ps parseState) parseArrayInner(bindingPower int, order *arrayOrder) (int, int) {
	defer ps.enter().leave()
	mark := noPosition
	dim := -1
	bp := bindingPower
	for {
		if bp < bindingPower {
			return dim, bp
		}
		// Allow trailing separators
		// [a ;] ==> (vcat a)
		if ps.isClosingToken(ps.peek(1)) {
			return typeminInt, typeminInt
		}
		var nextDim, nextBP int
		if bp == bindingPower {
			// Parse one expression
			mark = ps.position()
			ps.parseEqStar()
			nextDim, nextBP = ps.parseArraySeparator(order)
		} else { // bp > binding_power
			// Recurse to parse a separator with greater binding power.
			nextDim, nextBP = ps.parseArrayInner(bp, order)
			if bp == 0 {
				ps.emit(mark, kRow, emptyFlags)
			} else {
				ps.emit(mark, kNrow, setNumericFlags(dim))
			}
		}
		dim, bp = nextDim, nextBP
	}
}

// parseArraySeparator parses a separator in an array concatenation,
// returning (dim, binding_power).
func (ps parseState) parseArraySeparator(order *arrayOrder) (int, int) {
	const sepMismatchErr = "cannot mix space and ;; separators in an array expression, except to wrap a line"
	mark := ps.position()
	t := ps.peekTokenNL(1, true)
	if t.head.Kind == kSemicolon {
		// Newlines before semicolons are not significant
		// [a \n ;]     ==> (vcat a)
		ps.bumpTrivia()
		nSemis := 1
		for {
			ps.bump(triviaFlag)
			t = ps.peekToken(1)
			if t.head.Kind != kSemicolon {
				break
			}
			if t.precedingWhitespace {
				ps.bumpDisallowedSpace()
			}
			nSemis++
		}
		hadNewline := ps.peek(1) == kNewlineWs
		// Newlines after semicolons are not significant
		ps.bumpTrivia()
		if nSemis == 2 {
			if *order == orderRowMajor {
				if hadNewline {
					// In hcat with spaces as separators, `;;` is a line
					// continuation character
					//v1.7: [a b ;; \n c]  ==>  (hcat a b c)
					return 2, 0
				}
				// Can't mix spaces and multiple ;;
				//v1.7:  [a b ;; c]  ==>  (ncat-2 (row a b (error-t)) c)
				ps.emitErr(mark, kError, triviaFlag, sepMismatchErr)
			} else {
				*order = orderColumnMajor
			}
		}
		return nSemis, -nSemis
	}
	t = ps.peekToken(1)
	k := t.head.Kind
	if k == kNewlineWs {
		ps.bumpTrivia()
		if ps.peek(1) == kRsquare {
			// Linebreaks not significant before closing `]`
			// [a b\n\n]  ==>  (hcat a b)
			return typeminInt, typeminInt
		}
		// Treat a linebreak prior to a value as a semicolon (ie, separator
		// for the first dimension) if no previous semicolons observed
		// [a \n b]  ==> (vcat a b)
		return 1, -1
	} else if k == kComma {
		// Treat `,` as semicolon for the purposes of recovery
		// [a; b, c] ==> (vcat a b (error-t) c)
		ps.bumpErr(triviaFlag, "unexpected comma in array expression")
		return 1, -1
	}
	if t.precedingWhitespace && !ps.isClosingToken(k) {
		if *order == orderColumnMajor {
			// Can't mix multiple ;'s and spaces
			//v1.7:  [a ;; b c]  ==>  (ncat-2 a (row b (error-t) c))
			ps.bumpTriviaErr(triviaFlag, true, sepMismatchErr)
		} else {
			*order = orderRowMajor
		}
		return 2, 0
	}
	// Something else; use typemin to exit array parsing
	return typeminInt, typeminInt
}

// parseCat parses array concatenation/construction/indexing syntax inside
// of `[]` or `{}`. The opening bracket has been consumed. flisp: parse-cat
func (ps parseState) parseCat(closer Kind, endIsSymbol bool) (Kind, RawFlags) {
	ps.rangeColonEnabled = true
	ps.spaceSensitive = true
	ps.whereEnabled = true
	ps.whitespaceNewline = false
	ps.forGenerator = true
	k := ps.peekNL(1, true)
	mark := ps.position()
	if k == closer {
		// []  ==>  (vect)
		return ps.parseVect(closer, false)
	} else if k == kSemicolon {
		//v1.8: [;]           ==>  (ncat-1)
		ps.bumpTrivia()
		order := orderUnknown
		dim, _ := ps.parseArraySeparator(&order)
		ps.minSupportedVersion(1, 8, mark, "empty multidimensional array syntax")
		ps.bumpClosingToken(closer, "")
		return kNcat, setNumericFlags(dim)
	}
	ps.parseEqStar()
	k = ps.peekNL(1, true)
	if k == kComma || (ps.isClosingToken(k) && k != kSemicolon) {
		prefixTrailingComma := k == kComma
		if prefixTrailingComma {
			// [x,]  ==>  (vect x)
			ps.bumpNL(triviaFlag, true)
		}
		// [x]      ==>  (vect x)
		return ps.parseVect(closer, prefixTrailingComma)
	} else if k == kFor {
		// [x for a in as]  ==>  (comprehension (generator x (iteration (in a as))))
		return ps.parseComprehension(mark, closer)
	}
	// [x y]  ==>  (hcat x y)
	return ps.parseArray(mark, closer, endIsSymbol)
}

func (ps parseState) checkNcatCompat(mark streamPosition, k Kind) {
	// https://github.com/JuliaLang/julia/pull/33697
	if k == kNcat {
		ps.minSupportedVersion(1, 7, mark, "multidimensional array syntax")
	}
}

// parseParen parses un-prefixed parenthesized syntax. This is hard because
// parentheses are *very* overloaded! flisp: parse-paren / parse-paren-
func (ps parseState) parseParen(checkIdentifiers, hasUnaryPrefix bool) {
	ps.rangeColonEnabled = true
	ps.spaceSensitive = false
	ps.whereEnabled = true
	ps.whitespaceNewline = true
	mark := ps.position()
	ps.bump(triviaFlag) // K"("
	k := ps.peek(1)
	if k == kRparen {
		// ()  ==>  (tuple-p)
		ps.bump(triviaFlag)
		ps.emit(mark, kTuple, parensFlag)
	} else if isSyntacticOperator(k) {
		// allow :(=) etc in unchecked contexts, eg quotes
		// :(=)  ==>  (quote-: (parens =))
		ps.parseAtom(checkIdentifiers, false)
		ps.bumpClosingToken(kRparen, "")
		ps.emit(mark, kParens, emptyFlags)
	} else if !checkIdentifiers && k == kDecl && ps.peekNL(2, true) == kRparen {
		// allow :(::) as a special case
		// :(::)  ==>  (quote-: (parens ::))
		ps.bump(emptyFlags)
		ps.bumpNL(triviaFlag, true)
		ps.emit(mark, kParens, emptyFlags)
	} else {
		// Deal with all other cases of tuple or block syntax via the generic
		// parse_brackets
		initialSemi := ps.peek(1) == kSemicolon
		opts := ps.parseBrackets(kRparen, true, func(hadCommas, hadSplat bool, numSemis, numSubexprs int) bracketOpts {
			isTuple := hadCommas || (hadSplat && numSemis >= 1) ||
				(initialSemi && (numSemis == 1 || numSubexprs > 0)) ||
				(ps.peek(2) == kArrow && (ps.peekBehind().kind != kWhere && !hasUnaryPrefix))
			return bracketOpts{needsParameters: isTuple, isCall: isTuple, isBlock: numSemis > 0}
		})
		if opts.isCall {
			// Tuple syntax with commas
			// (x,)        ==>  (tuple-p x)
			ps.emit(mark, kTuple, parensFlag|opts.delimFlags)
		} else if opts.isBlock {
			// Blocks
			// (a=1;)      ==>  (block-p (= a 1))
			ps.emit(mark, kBlock, parensFlag)
		} else {
			// Parentheses used for grouping
			// (x)         ==>  (parens x)
			ps.emit(mark, kParens, emptyFlags)
		}
	}
}

// parseBrackets handles bracketed syntax inside any of () [] or {} where
// there's a mixture of commas and semicolon delimiters. afterParse
// disambiguates the cases from the counts.
// flisp: parts of parse-paren- and parse-arglist
func (ps parseState) parseBrackets(closingKind Kind, generatorIsLast bool,
	afterParse func(hadCommas, hadSplat bool, numSemis, numSubexprs int) bracketOpts) bracketOpts {
	ps.rangeColonEnabled = true
	ps.spaceSensitive = false
	ps.whereEnabled = true
	ps.whitespaceNewline = true
	var paramsPositions []streamPosition
	numSubexprs := 0
	numSemis := 0
	hadCommas := false
	hadSplat := false
	var paramStart streamPosition
	hasParamStart := false
	trailingComma := false
	for {
		k := ps.peek(1)
		if k == closingKind {
			break
		} else if k == kSemicolon {
			// Start of parameters list
			// a, b; c d  ==>  a b (parameters c d)
			if hasParamStart {
				paramsPositions = append(paramsPositions, ps.emit(paramStart, kTombstone, emptyFlags))
			}
			numSemis++
			paramStart, hasParamStart = ps.position(), true
			ps.bump(triviaFlag)
			ps.bumpTrivia()
		} else if ps.isClosingToken(k) {
			trailingComma = false
			// Error; handled below in bump_closing_token
			break
		} else {
			mark := ps.position()
			ps.parseEqStar()
			trailingComma = false
			numSubexprs++
			if numSubexprs == 1 {
				hadSplat = ps.peekBehind().kind == kDotDotDot
			}
			k = ps.peekNL(1, true)
			if k == kFor {
				// Generator syntax
				// (x for a in as)  ==>  (parens (generator x (iteration (in a as))))
				ps.parseGenerator(mark)
				if generatorIsLast {
					break
				}
				k = ps.peekNL(1, true)
			}
			if k == kComma {
				hadCommas = true
				ps.bump(triviaFlag)
				trailingComma = true
			} else if k == kSemicolon || k == closingKind {
				// Handled above
				continue
			} else {
				// Error - recovery done when consuming closing_kind
				break
			}
		}
	}
	if hasParamStart && ps.position() != paramStart {
		paramsPositions = append(paramsPositions, ps.emit(paramStart, kTombstone, emptyFlags))
	}
	opts := afterParse(hadCommas, hadSplat, numSemis, numSubexprs)
	// Emit nested parameter nodes if necessary
	if opts.needsParameters {
		for _, pos := range paramsPositions {
			ps.resetKind(pos, kParameters)
		}
	}
	ps.bumpClosingToken(closingKind, " or `,`")
	opts.delimFlags = emptyFlags
	if trailingComma {
		opts.delimFlags = trailingCommaFlag
	}
	return opts
}

func isIndentation(b byte) bool { return b == ' ' || b == '\t' }

// parseString parses a string, embedded interpolations and deindents triple
// quoted strings by marking indentation characters as whitespace trivia.
// flisp: parse-string-literal-, parse-interpolate
func (ps parseState) parseString(raw bool) {
	mark := ps.position()
	delimK := ps.peek(1)
	triplestr := delimK == kTripleDquote || delimK == kTripleBacktick
	stringChunkKind := kCmdString
	if delimK == kDquote || delimK == kTripleDquote {
		stringChunkKind = kString
	}
	indentRefI := 0
	indentRefLen := math.MaxInt
	var indentChunks []streamPosition
	txtbuf := ps.stream.textbuf
	at := func(i int) byte { return txtbuf[i-1] }
	chunkFlags := emptyFlags
	if raw {
		chunkFlags = rawStringFlag
	}
	ps.bump(triviaFlag)
	firstChunk := true
	nNontriviaChunks := 0
	prevChunkNewline := false
	for {
		t := ps.peekFullToken()
		k := t.head.Kind
		if k == kDollar {
			if raw {
				// FIXME: This case is actually a tokenization error:
				// The `K"$"` token should not occur when a raw string is
				// being parsed, but this would require the lexer to know
				// about the parse state. (see also parse_atom)
				break
			}
			if prevChunkNewline {
				// """\n$x\n a"""  ==>  (string-s x "\n" " a")
				indentRefI = t.firstByte
				indentRefLen = 0
			}
			ps.bump(triviaFlag)
			k = ps.peek(1)
			if k == kLparen {
				// "a $(x + y) b"  ==>  (string "a " (parens (call-i x + y)) " b")
				m := ps.position()
				ps.bump(triviaFlag)
				opts := ps.parseBrackets(kRparen, true, func(hadCommas, _ bool, numSemis, numSubexprs int) bracketOpts {
					return bracketOpts{simpleInterp: !hadCommas && numSemis == 0 && numSubexprs == 1}
				})
				if !opts.simpleInterp || ps.peekBehindNoParens().kind == kGenerator {
					// "$(x,y)" ==> (string (parens (error x y)))
					ps.emitErr(m, kError, emptyFlags, "invalid interpolation syntax")
				}
				ps.emit(m, kParens, emptyFlags)
			} else if k == kVar {
				// var identifiers disabled in strings
				// "$var"  ==>  (string var)
				ps.bumpRemap(emptyFlags, kIdentifier)
			} else if k == kIdentifier || isKeyword(k) || isWordOperator(k) {
				// "a $foo b"  ==> (string "a " foo " b")
				ps.parseAtom(true, false)
			} else {
				ps.bumpInvisible(kError, emptyFlags, "identifier or parenthesized expression expected after $ in string")
			}
			firstChunk = false
			nNontriviaChunks++
			prevChunkNewline = false
		} else if k == stringChunkKind {
			s := t.span()
			isNewlineOnly := false
			if triplestr && firstChunk && s <= 2 {
				b := at(t.lastByte)
				// Test whether the string is a single logical newline
				isNewlineOnly = (s == 1 && (b == '\n' || b == '\r')) ||
					(s == 2 && (at(t.firstByte) == '\r' && b == '\n'))
			}
			if isNewlineOnly {
				// First line of triple string is a newline only: mark as
				// trivia.
				// """\nx"""    ==> (string-s "x")
				ps.bump(triviaFlag)
				firstChunk = false
				prevChunkNewline = true
			} else {
				if triplestr {
					// Triple-quoted dedenting:
					// """\n x\n y"""      ==> (string-s "x\n" "y")
					if b := at(t.firstByte); prevChunkNewline && b != '\n' && b != '\r' {
						// Compute length of longest common prefix of mixed
						// spaces and tabs, in bytes
						//
						// Initial whitespace is never regarded as
						// indentation in any triple quoted string chunk, as
						// it's always preceded in the source code by a
						// visible token of some kind; either a """ delimiter
						// or $() interpolation.
						if indentRefI == 0 {
							// No indentation found yet. Find indentation we'll
							// use as a reference
							i := t.firstByte - 1
							for i < t.lastByte && isIndentation(at(i+1)) {
								i++
							}
							indentRefI = t.firstByte
							indentRefLen = i - t.firstByte + 1
						} else {
							// Matching the current indentation with reference,
							// shortening length if necessary.
							j := 0
							for j < t.span() && j < indentRefLen {
								if at(j+t.firstByte) != at(j+indentRefI) {
									break
								}
								j++
							}
							indentRefLen = min(indentRefLen, j)
						}
						// Prepare a place for indentiation trivia, if
						// necessary
						indentChunks = append(indentChunks, ps.bumpInvisible(kTombstone, emptyFlags, ""))
					}
					b := at(t.lastByte)
					prevChunkNewline = b == '\n' || b == '\r'
				}
				ps.bump(chunkFlags)
				firstChunk = false
				nNontriviaChunks++
			}
		} else if k == kErrorInvalidInterpolationTerminator || k == kErrorBidiFormatting || k == kErrorInvalidUTF8 {
			// Treat these errors as string chunks
			ps.bump(emptyFlags)
			nNontriviaChunks++
		} else {
			break
		}
	}
	hadEndDelim := ps.peek(1) == delimK
	if triplestr && prevChunkNewline && hadEndDelim {
		// Newline at end of string
		// """\n x\n y\n"""    ==> (string-s " x\n" " y\n")
		indentRefLen = 0
	}
	if triplestr && indentRefLen > 0 {
		for _, pos := range indentChunks {
			ps.resetBoth(pos, kWhitespace, triviaFlag)
			if ps.stream.stealTokenBytes(pos, indentRefLen) {
				// Empty chunks after dedent are removed
				// """\n \n """        ==> (string-s "\n")
				nNontriviaChunks--
			}
		}
	}
	if hadEndDelim {
		if nNontriviaChunks == 0 {
			// Empty strings, or empty after triple quoted processing
			// "" ==> (string "")
			ps.bumpInvisible(stringChunkKind, chunkFlags, "")
		}
		ps.bump(triviaFlag)
	} else {
		// Missing delimiter recovery
		// "str   ==> (string "str" (error-t))
		ps.bumpInvisible(kError, triviaFlag, "unterminated string literal")
	}
	// String interpolations
	// "$x$y$z"  ==> (string x y z)
	stringKind := kCmdstring
	if delimK == kDquote || delimK == kTripleDquote {
		stringKind = kStringNode
	}
	strFlags := emptyFlags
	if triplestr {
		strFlags |= tripleStringFlag
	}
	if raw {
		strFlags |= rawStringFlag
	}
	ps.emit(mark, stringKind, strFlags)
}

func (ps parseState) emitBraces(mark streamPosition, ckind Kind, cflags RawFlags) {
	if ckind == kHcat {
		// {x y}  ==>  (bracescat (row x y))
		ps.emit(mark, kRow, cflags&^trailingCommaFlag)
	} else if ckind == kNcat {
		// {x ;;; y}  ==>  (bracescat (nrow-3 x y))
		ps.emit(mark, kNrow, cflags&^trailingCommaFlag)
	}
	ps.checkNcatCompat(mark, ckind)
	if ckind == kVect || ckind == kComprehension {
		ps.emit(mark, kBraces, cflags&trailingCommaFlag)
	} else {
		ps.emit(mark, kBracescat, emptyFlags)
	}
}

// parseAtom parses numbers, identifiers, parenthesized expressions, lists,
// vectors, etc. If checkIdentifiers is true, identifiers are disallowed from
// being one of the syntactic operators or closing tokens. flisp: parse-atom
func (ps parseState) parseAtom(checkIdentifiers, hasUnaryPrefix bool) {
	defer ps.enter().leave()
	ps.bumpTrivia()
	mark := ps.position()
	leadingKind := ps.peek(1)
	switch {
	case isError(leadingKind):
		// Errors for bad tokens are emitted in validate_tokens() rather
		// than here.
		ps.bump(emptyFlags)
	case leadingKind == kPrime:
		// char literal
		ps.bump(triviaFlag)
		k := ps.peek(1)
		if k == kPrime {
			// ''  ==>  (char (error))
			ps.bumpInvisible(kError, emptyFlags, "empty character literal")
			ps.bump(triviaFlag)
		} else if k == kEndMarker {
			// '   ==>  (char (error))
			ps.bumpInvisible(kError, emptyFlags, "unterminated character literal")
		} else {
			if k == kChar || isError(k) {
				ps.bump(emptyFlags)
			} else {
				// FIXME: This case is actually a tokenization error.
				// x in'c'  ==>  (call-i x in (char 'c'))
				ps.bumpRemap(emptyFlags, kChar)
			}
			if ps.peek(1) == kPrime {
				// 'a'         ==>  (char 'a')
				ps.bump(triviaFlag)
			} else {
				// 'a  ==>  (char 'a' (error-t))
				ps.bumpInvisible(kError, triviaFlag, "unterminated character literal")
			}
		}
		ps.emit(mark, kCharNode, emptyFlags)
	case leadingKind == kChar:
		// FIXME: This is a tokenization error and should be preceded with
		// K"'". However this workaround is better than emitting a bare Char.
		ps.bumpRemap(emptyFlags, kIdentifier)
	case leadingKind == kColon:
		// symbol/expression quote
		// :foo  ==>  (quote-: foo)
		t := ps.peekToken(2)
		k := t.head.Kind
		if ps.isClosingToken(k) && (!isKeyword(k) || t.precedingWhitespace) {
			// : is a literal colon in some circumstances
			// :)     ==>  :
			ps.bump(emptyFlags) // K":"
			return
		}
		ps.bump(triviaFlag) // K":"
		if t.precedingWhitespace {
			// : foo   ==>  (quote-: (error-t) foo)
			ps.bumpTriviaErr(triviaFlag, true, "whitespace not allowed after `:` used for quoting")
		}
		// Being inside quote makes keywords into identifiers at the first
		// level of nesting
		// :end ==> (quote-: end)
		inner := ps
		inner.endSymbol = false
		inner.parseAtom(false, false)
		ps.emit(mark, kQuote, colonQuote)
	case checkIdentifiers && leadingKind == kEq && isPlainEquals(ps.peekToken(1)):
		// =   ==> (error =)
		ps.bumpErr(emptyFlags, "unexpected `=`")
	case leadingKind == kIdentifier:
		// xx  ==>  xx
		ps.bump(emptyFlags)
	case isWordOperator(leadingKind):
		// where=1 ==> (= where 1)
		ps.bumpRemap(emptyFlags, kIdentifier)
	case isOperator(leadingKind):
		// +     ==>  +
		// .+    ==>  (. +)
		remap := kIdentifier
		if isSyntacticOperator(leadingKind) {
			remap = leadingKind
		}
		ps.bumpDotsplit(emptyFlags, true, remap)
		if checkIdentifiers && !isValidIdentifier(leadingKind) {
			// +=   ==>  (error (op= +))
			ps.emitErr(mark, kError, emptyFlags, "invalid identifier")
		}
	case isKeyword(leadingKind):
		if t := ps.peekToken(2); leadingKind == kVar && t.head.Kind == kDquote && !t.precedingWhitespace {
			// var"x"     ==> (var x)
			// Raw mode unescaping
			ps.bump(triviaFlag)
			ps.bump(triviaFlag)
			if ps.peek(1) == kString {
				ps.bumpRemap(rawStringFlag, kIdentifier)
			} else {
				ps.bumpInvisible(kIdentifier, rawStringFlag, "")
			}
			if ps.peek(1) == kDquote {
				ps.bump(triviaFlag)
			} else {
				ps.bumpInvisible(kError, triviaFlag, "unterminated `var\"\"` identifier")
			}
			t = ps.peekToken(1)
			k := t.head.Kind
			switch {
			case t.precedingWhitespace || isOperator(k) || k == kLparen || k == kRparen ||
				k == kLsquare || k == kRsquare || k == kLbrace || k == kRbrace || k == kComma ||
				k == kSemicolon || k == kAt || k == kEndMarker:
				// var"x"+  ==>  x
			case isStringMacroSuffix(k):
				// var"x"end  ==>  (var x (error-t))
				ps.bumpErr(triviaFlag, "suffix not allowed after `var\"...\"` syntax")
			case k == kBacktick || k == kDquote || k == kTripleDquote || k == kTripleBacktick:
				// Disallow `var"#""str". To allow this we'd need to fix `raw`
				// detection in lex_quote to be consistent with the parser.
				ps.bumpInvisible(kError, triviaFlag, "`var\"...\"` syntax not supported as string macro name")
			}
			ps.emit(mark, kVar, emptyFlags)
		} else if checkIdentifiers && ps.isClosingToken(leadingKind) {
			// :(end)  ==>  (quote-: (error end))
			ps.bumpErr(emptyFlags, "invalid identifier")
		} else {
			// Remap keywords to identifiers.
			// :end  ==>  (quote-: end)
			ps.bumpRemap(emptyFlags, kIdentifier)
		}
	case leadingKind == kLparen: // parens or tuple
		ps.parseParen(checkIdentifiers, hasUnaryPrefix)
	case leadingKind == kLsquare: // cat expression
		ps.bump(triviaFlag)
		ckind, cflags := ps.parseCat(kRsquare, ps.endSymbol)
		ps.emit(mark, ckind, cflags)
		ps.checkNcatCompat(mark, ckind)
	case leadingKind == kLbrace: // cat expression
		ps.bump(triviaFlag)
		ckind, cflags := ps.parseCat(kRbrace, ps.endSymbol)
		ps.emitBraces(mark, ckind, cflags)
	case leadingKind == kAt: // macro call
		// Macro names can be keywords
		// @end x  ==> (macrocall @end x)
		ps.bump(triviaFlag)
		ps.parseMacroName()
		ps.parseCallChain(mark, true)
	case isStringDelim(leadingKind):
		ps.parseString(false)
	case leadingKind == kBacktick || leadingKind == kTripleBacktick:
		// `cmd`       ==>  (cmdstring-r "cmd")
		ps.parseString(true)
	case isLiteral(leadingKind):
		// 42   ==>  42
		ps.bump(emptyFlags)
	default:
		// The kinds left are the closing tokens (parser.jl tests
		// is_closing_token here and has a final "invalid syntax atom" branch,
		// which no token the lexer emits can reach: whitespace has been
		// bumped and every other kind is matched above).
		//
		// Leave closing token in place for other productions to recover
		// with
		// )  ==>  error
		msg := "premature end of input"
		if leadingKind != kEndMarker {
			msg = "unexpected `" + leadingKind.String() + "`"
		}
		ps.stream.diagNext(false, levelError, msg)
		ps.bumpInvisible(kError, emptyFlags, "")
	}
}
