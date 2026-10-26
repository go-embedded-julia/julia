// Ported from JuliaSyntax src/parser.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): call chains and reserved-word constructs.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

// parseCallChain parses a chain of suffixes at function call precedence,
// leftmost binding tightest: bracketed calls a() b[] c{}, field access a.b.c
// and dotted forms, adjoint a', string macros a"str".
// f(a).g(b) ==> (call (. (call f a) g) b)
// flisp: parse-call-chain, parse-call-with-initial-ex
func (ps parseState) parseCallChain(mark streamPosition, isMacrocall bool) {
	if isNumber(ps.peekBehind().kind) && ps.peek(1) == kLparen {
		// juxtaposition with numbers is multiply, not call
		// 2(x) ==> (* 2 x)
		return
	}
	// source range of the @-prefixed part of a macro
	var atnameFrom, atnameTo streamPosition
	hasAtname := false
	// $A.@x  ==>  (macrocall (. ($ A) @x))
	maybeStrmac := true
	// We record the last component of chains of dot-separated identifiers so
	// we know which identifier was the macro name.
	macroNamePosition := ps.position() // points to same output span as peek_behind
	for {
		maybeStrmac1 := false
		t := ps.peekToken(1)
		k := t.head.Kind
		switch {
		case !isMacrocall && ps.spaceSensitive && t.precedingWhitespace &&
			(k == kLparen || k == kLsquare || k == kLbrace || k == kDquote || k == kTripleDquote ||
				k == kBacktick || k == kTripleBacktick):
			// [f (x)]  ==>  (hcat f (parens x))
			return
		case isMacrocall && (t.precedingWhitespace ||
			!(k == kLparen || k == kLsquare || k == kLbrace || k == kPrime || k == kDot)):
			// Macro calls with space-separated arguments
			// @foo a b    ==> (macrocall @foo a b)
			ps.fixMacroNameKind(macroNamePosition, kNone)
			sps := ps.withSpaceSensitive()
			// Space separated macro arguments
			nArgs := sps.parseSpaceSeparatedExprs()
			isDocMacro := sps.peekBehindAt(macroNamePosition).origKind == kDoc
			if isDocMacro && nArgs == 1 {
				// Parse extended @doc args on next line
				// @doc x\ny      ==>  (macrocall @doc x y)
				k2 := sps.peek(2)
				if sps.peek(1) == kNewlineWs && !sps.isClosingToken(k2) && k2 != kNewlineWs {
					sps.bump(emptyFlags) // newline
					sps.parseEq()
				}
			}
			sps.emit(mark, kMacrocall, emptyFlags)
			return
		case k == kLparen:
			// f(a,b)  ==>  (call f a b)
			// f (a)    ==>  (call f (error-t) a)
			ps.bumpDisallowedSpace()
			ps.bump(triviaFlag)
			opts := ps.parseCallArglist(kRparen)
			if ps.peek(1) == kDo {
				// f(x) do y body end  ==>  (call f x (do (tuple y) (block body)))
				ps.parseDo()
			}
			if isMacrocall {
				ps.emit(mark, kMacrocall, parensFlag|opts.delimFlags)
				// @x(a, b)   ==>  (macrocall-p @x a b)
				ps.fixMacroNameKind(macroNamePosition, kNone)
				isMacrocall = false
				hasAtname = false
			} else {
				ps.emit(mark, kCall, opts.delimFlags)
			}
		case k == kLsquare:
			m := ps.position()
			// a [i]  ==>  (ref a (error-t) i)
			ps.bumpDisallowedSpace()
			ps.bump(triviaFlag)
			inner := ps
			inner.endSymbol = true
			ckind, cflags := inner.parseCat(kRsquare, ps.endSymbol)
			if isMacrocall {
				// @S[a,b]  ==>  (macrocall @S (vect a b))
				ps.fixMacroNameKind(macroNamePosition, kNone)
				ps.emit(m, ckind, cflags)
				ps.checkNcatCompat(m, ckind)
				ps.emit(mark, kMacrocall, emptyFlags)
				isMacrocall = false
				hasAtname = false
			} else {
				// a[i]    ==>  (ref a i)
				var outk Kind
				switch ckind {
				case kVect:
					outk = kRef
				case kHcat:
					outk = kTypedHcat
				case kVcat:
					outk = kTypedVcat
				case kComprehension:
					outk = kTypedComprehension
				default: // kNcat; parse_cat returns no other kind
					outk = kTypedNcat
				}
				ps.emit(mark, outk, cflags)
				ps.checkNcatCompat(mark, ckind)
			}
		case k == kDot:
			// x .y  ==>  (. x (error-t) y)
			ps.bumpDisallowedSpace()
			emark := ps.position()
			if hasAtname {
				// Allow `@` in macrocall only in first and last position
				// A.@B.x  ==>  (macrocall (. (. A B (error-t)) @x))
				ps.stream.diagMarks(atnameFrom, atnameTo, levelError,
					"`@` must appear on first or last macro name component")
				ps.bumpErr(triviaFlag, "Unexpected `.` after macro name")
			} else {
				ps.bump(triviaFlag)
			}
			k = ps.peek(1)
			switch k {
			case kLparen:
				if isMacrocall {
					// @M.(x)  ==> (macrocall (dotcall @M (error-t) x))
					ps.bumpInvisible(kError, triviaFlag, "")
					ps.stream.diagMark(mark, true, levelError, "dot call syntax not supported for macros")
				}
				// f.(a,b)   ==>  (dotcall f a b)
				ps.bumpDisallowedSpace()
				ps.bump(triviaFlag)
				opts := ps.parseCallArglist(kRparen)
				ps.emit(mark, kDotcall, opts.delimFlags)
			case kColon:
				// A.:+  ==>  (. A (quote-: +))
				m := ps.position()
				ps.bump(triviaFlag)
				ps.bumpDisallowedSpace()
				ps.parseAtom(false, false)
				ps.emit(m, kQuote, colonQuote)
				ps.emit(mark, kDot, emptyFlags)
			case kDollar:
				// f.$x      ==>  (. f ($ x))
				m := ps.position()
				ps.bump(triviaFlag)
				ps.parseAtom(true, false)
				ps.emit(m, kDollar, emptyFlags)
				macroNamePosition = ps.position()
				ps.emit(mark, kDot, emptyFlags)
			case kAt:
				// A macro call after some prefix A has been consumed
				// A.@x    ==>  (macrocall (. A @x))
				m := ps.position()
				if isMacrocall {
					// @A.B.@x a ==> (macrocall (. (. A B) (error-t) @x) a)
					ps.bumpErr(triviaFlag, "repeated `@` in macro module path")
				} else {
					ps.bump(triviaFlag)
					isMacrocall = true
				}
				ps.parseMacroName()
				macroNamePosition = ps.position()
				atnameFrom, atnameTo, hasAtname = m, ps.position(), true
				ps.emit(mark, kDot, emptyFlags)
			case kPrime:
				// f.'  =>  f (error-t (. '))
				ps.bumpDotsplit(emptyFlags, false, kIdentifier)
				ps.emitErr(emark, kError, emptyFlags, "the .' operator for transpose is discontinued")
				ps.emit(mark, kDotcall, postfixOpFlag)
			default:
				// Field/property syntax
				// f.x.y ==> (. (. f x) y)
				ps.parseAtom(false, false)
				macroNamePosition = ps.position()
				maybeStrmac1 = true
				ps.emit(mark, kDot, emptyFlags)
			}
		case k == kPrime && !t.precedingWhitespace:
			// f'  ==> (call-post f ')
			ps.bumpRemap(emptyFlags, kIdentifier)
			ps.emit(mark, kCall, postfixOpFlag)
		case k == kLbrace:
			// Type parameter curlies and macro calls
			m := ps.position()
			// S {a} ==> (curly S (error-t) a)
			ps.bumpDisallowedSpace()
			ps.bump(triviaFlag)
			opts := ps.parseCallArglist(kRbrace)
			if isMacrocall {
				// @S{a,b} ==> (macrocall S (braces a b))
				ps.fixMacroNameKind(macroNamePosition, kNone)
				ps.emit(m, kBraces, opts.delimFlags)
				ps.emit(mark, kMacrocall, emptyFlags)
				ps.minSupportedVersion(1, 6, mark, "macro call without space before `{}`")
				isMacrocall = false
				hasAtname = false
			} else {
				// S{a,b} ==> (curly S a b)
				ps.emit(mark, kCurly, opts.delimFlags)
			}
		case (k == kDquote || k == kTripleDquote || k == kBacktick || k == kTripleBacktick) &&
			!t.precedingWhitespace && maybeStrmac && ps.strmacOrigKindOK(macroNamePosition):
			// Custom string and command literals
			// x"str" ==> (macrocall @x_str (string-r "str"))
			// x`str` ==> (macrocall @x_cmd (cmdstring-r "str"))
			outk := kCmdMacroName
			if isStringDelim(k) {
				outk = kStringMacroName
			}
			ps.fixMacroNameKind(macroNamePosition, outk)
			ps.parseString(true)
			t = ps.peekToken(1)
			k = t.head.Kind
			if !t.precedingWhitespace && isStringMacroSuffix(k) {
				// Macro suffixes can include keywords and numbers
				// x"s"y    ==> (macrocall @x_str (string-r "s") "y")
				suffixKind := k
				if k == kIdentifier || isKeyword(k) || isWordOperator(k) {
					suffixKind = kString
				}
				ps.bumpRemap(emptyFlags, suffixKind)
			}
			ps.emit(mark, kMacrocall, emptyFlags)
		default:
			return
		}
		maybeStrmac = maybeStrmac1
	}
}

// strmacOrigKindOK must mirror the logic in lex_quote() for consistency.
func (ps parseState) strmacOrigKindOK(pos streamPosition) bool {
	origk := ps.peekBehindAt(pos).origKind
	return origk == kIdentifier || isContextualKeyword(origk) || isWordOperator(origk)
}

// parseSubtypeSpec parses the `A<:B` part of type definitions like
// `struct A<:B end`. flisp: parse-subtype-spec
func (ps parseState) parseSubtypeSpec() {
	// Wart: why isn't the flisp parser more strict here?
	// <: is the only operator which isn't a syntax error, but
	// parse_comparison allows all sorts of things.
	ps.parseComparison(true)
}

// parseStructField: flisp parse-struct-field
func (ps parseState) parseStructField() {
	mark := ps.position()
	constField := ps.peek(1) == kConst
	if constField {
		ps.bump(triviaFlag)
	}
	ps.parseEq()
	if constField {
		// Const fields https://github.com/JuliaLang/julia/pull/43305
		ps.emit(mark, kConst, emptyFlags)
		ps.minSupportedVersion(1, 8, mark, "`const` struct field")
	}
}

// parseResword parses expressions or blocks introduced by syntactic
// reserved words. flisp: parse-resword
func (ps parseState) parseResword() {
	defer ps.enter().leave()
	// In normal_context
	// begin f() where T = x end  ==>  (block (= (where (call f) T) x))
	ps = ps.normalContext()
	ps.bumpTrivia()
	mark := ps.position()
	word := ps.peek(1)
	switch word {
	case kBegin, kQuote:
		// begin end         ==>  (block)
		ps.bump(triviaFlag)
		ps.parseBlockInner(func(p parseState) { p.parseDocstring(parseState.parseEq) })
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kBlock, emptyFlags)
		if word == kQuote {
			// quote body end  ==>  (quote (block body))
			ps.emit(mark, kQuote, emptyFlags)
		}
	case kWhile:
		// while cond body end  ==>  (while cond (block body))
		ps.bump(triviaFlag)
		ps.parseCond()
		ps.parseBlockDefault()
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kWhile, emptyFlags)
	case kFor:
		// for x in xs end  ==>  (for (iteration (in x xs)) (block))
		ps.bump(triviaFlag)
		ps.parseIterationSpecs()
		ps.parseBlockDefault()
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kFor, emptyFlags)
	case kLet:
		ps.bump(triviaFlag)
		m := ps.position()
		if k := ps.peek(1); k != kNewlineWs && k != kSemicolon {
			// let x=1\n end     ==>  (let (block (= x 1)) (block))
			ps.parseCommaSeparated(parseState.parseEqStar)
		}
		ps.emit(m, kBlock, emptyFlags)
		k := ps.peek(1)
		if k == kNewlineWs || k == kSemicolon {
			ps.bump(triviaFlag)
		} else if k != kEnd {
			ps.recover(isCloserOrNewline, triviaFlag, ps.position(), "let variables should end in `;` or newline")
		}
		// let\na\nb\nend    ==>  (let (block) (block a b))
		ps.parseBlockDefault()
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kLet, emptyFlags)
	case kIf:
		ps.parseIfElseif(false, false)
	case kGlobal, kLocal:
		// global x   ==>  (global x)
		ps.bump(triviaFlag)
		var constMark streamPosition
		hasConst := false
		if ps.peek(1) == kConst {
			constMark, hasConst = ps.position(), true
			ps.bump(triviaFlag)
		}
		hadAssignment := ps.parseGlobalLocalConstVars()
		if hasConst {
			// global const x = 1  ==>  (global (const (= x 1)))
			ps.emit(constMark, kConst, emptyFlags)
			if !hadAssignment {
				// global const x  ==>  (global (error (const x)))
				ps.emitErr(mark, kError, emptyFlags, "expected assignment after `const`")
			}
		}
		ps.emit(mark, word, emptyFlags)
	case kConst:
		// const x = 1  ==>  (const (= x 1))
		ps.bump(triviaFlag)
		var scopeMark streamPosition
		hasScope := false
		scopeK := ps.peek(1)
		if scopeK == kLocal || scopeK == kGlobal {
			scopeMark, hasScope = ps.position(), true
			ps.bump(triviaFlag)
		}
		hadAssignment := ps.parseGlobalLocalConstVars()
		if hasScope {
			// const global x = 1  ==>  (const (global (= x 1)))
			ps.emit(scopeMark, scopeK, emptyFlags)
		}
		ps.emit(mark, kConst, emptyFlags)
		if !hadAssignment {
			// const x .= 1  ==>  (error (const (.= x 1)))
			ps.emitErr(mark, kError, emptyFlags, "expected assignment after `const`")
		}
	case kFunction, kMacro:
		ps.bump(triviaFlag)
		ps.bumpTrivia()
		hasBody := ps.parseFunctionSignature(word == kFunction)
		if hasBody {
			// function f() \n a \n b end  ==> (function (call f) (block a b))
			ps.parseBlockDefault()
			ps.bumpClosingToken(kEnd, "")
			ps.emit(mark, word, emptyFlags)
		} else {
			// Function/macro definition with no methods
			// function f end       ==> (function f)
			ps.bumpNL(triviaFlag, true)
			ps.emit(mark, word, emptyFlags)
		}
	case kAbstract:
		// abstract type A end             ==>  (abstract A)
		ps.bump(triviaFlag)
		ps.bump(triviaFlag) // type; peek_initial_reserved_words checked it
		ps.parseSubtypeSpec()
		ps.bumpSemicolonTrivia()
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kAbstract, emptyFlags)
	case kStruct, kMutable:
		// struct A <: B \n a::X \n end  ==>  (struct (<: A B) (block (::-i a X)))
		isMut := word == kMutable
		if isMut {
			// mutable struct A end  ==>  (struct-mut A (block))
			ps.bump(triviaFlag)
		}
		ps.bump(triviaFlag) // struct
		ps.parseSubtypeSpec()
		ps.parseBlock(func(p parseState) { p.parseDocstring(parseState.parseStructField) }, ps.position())
		ps.bumpClosingToken(kEnd, "")
		f := emptyFlags
		if isMut {
			f = mutableFlag
		}
		ps.emit(mark, kStruct, f)
	case kPrimitive:
		// primitive type A 32 end             ==> (primitive A 32)
		ps.bump(triviaFlag)
		ps.bump(triviaFlag) // type
		sps := ps.withSpaceSensitive()
		sps.parseSubtypeSpec()
		sps.parseCond()
		ps.bumpSemicolonTrivia()
		ps.bumpClosingToken(kEnd, "")
		ps.emit(mark, kPrimitive, emptyFlags)
	case kTry:
		ps.parseTry()
	case kReturn:
		ps.bump(triviaFlag)
		k := ps.peek(1)
		if !(k == kNewlineWs || ps.isClosingToken(k)) {
			// return x    ==>  (return x)
			ps.parseEq()
		}
		ps.emit(mark, kReturn, emptyFlags)
	case kBreak, kContinue:
		// break     ==>  (break)
		ps.bump(triviaFlag)
		ps.emit(mark, word, emptyFlags)
		k := ps.peek(1)
		ok := k == kNewlineWs || k == kSemicolon || k == kRparen || k == kColon || k == kEndMarker ||
			(k == kEnd && !ps.endSymbol)
		if !ok {
			ps.recover(isCloserOrNewline, triviaFlag, ps.position(), "unexpected token after "+word.String())
		}
	case kModule, kBaremodule:
		// module A end  ==> (module A (block))
		ps.bump(triviaFlag)
		if isReservedWord(ps.peek(1)) {
			// module do \n end  ==>  (module (error do) (block))
			ps.bumpErr(emptyFlags, "Invalid module name")
		} else {
			// module $A end  ==>  (module ($ A) (block))
			ps.parseUnaryPrefix(false)
		}
		// module A \n a \n b \n end  ==>  (module A (block a b))
		ps.parseBlock(parseState.parsePublic, ps.position())
		ps.bumpClosingToken(kEnd, "")
		f := emptyFlags
		if word == kBaremodule {
			f = bareModuleFlag
		}
		ps.emit(mark, kModule, f)
	case kExport, kPublic:
		// export a         ==>  (export a)
		ps.bump(triviaFlag)
		ps.parseCommaSeparated(func(p parseState) { p.parseAtsym(false) })
		ps.emit(mark, word, emptyFlags)
	case kImport, kUsing:
		ps.parseImports()
	default: // kDo: peek_initial_reserved_words admits no other word
		ps.bumpErr(triviaFlag, "invalid `do` syntax")
	}
}

// parseIfElseif parses if-elseif-else-end expressions.
func (ps parseState) parseIfElseif(isElseif, isElseifWhitespaceErr bool) {
	defer ps.enter().leave()
	mark := ps.position()
	word := ps.peek(1)
	if isElseifWhitespaceErr {
		// Only get here on recovery from error case - pretend we're parsing
		// elseif.
		word = kElseif
	} else {
		ps.bump(triviaFlag)
	}
	if k := ps.peek(1); k == kNewlineWs || k == kEnd {
		// if end      ==>  (if (error) (block))
		ps.bumpTriviaErr(emptyFlags, true, "missing condition in `"+word.String()+"`")
	} else {
		// if a end      ==>  (if a (block))
		ps.parseCond()
	}
	// if a \n\n xx \n\n end   ==>  (if a (block xx))
	ps.parseBlockDefault()
	ps.bumpTrivia()
	k := ps.peek(1)
	if k == kElseif {
		// if a xx elseif b yy end   ==>  (if a (block xx) (elseif b (block yy)))
		ps.parseIfElseif(true, false)
	} else if k == kElse {
		emark := ps.position()
		ps.bump(triviaFlag)
		if ps.peek(1) == kIf {
			// Recovery: User wrote `else if` by mistake ?
			ps.bump(triviaFlag)
			ps.emitErr(emark, kError, triviaFlag, "use `elseif` instead of `else if`")
			ps.parseIfElseif(true, true)
		} else {
			// if a xx else yy end   ==>  (if a (block xx) (block yy))
			ps.parseBlockDefault()
		}
	}
	if !isElseif {
		ps.bumpClosingToken(kEnd, "")
	}
	ps.emit(mark, word, emptyFlags)
}

// parseGlobalLocalConstVars is like parse_assignment, but specialized so
// that we can omit the tuple when there's commas but no assignment.
func (ps parseState) parseGlobalLocalConstVars() bool {
	mark := ps.position()
	nCommas := ps.parseComma(false)
	t := ps.peekToken(1)
	if isPrecAssignment(t.head.Kind) {
		if nCommas >= 1 {
			// const x,y = 1,2  ==>  (const (= (tuple x y) (tuple 1 2)))
			ps.emit(mark, kTuple, emptyFlags)
		}
		// const x = 1   ==>  (const (= x 1))
		ps.parseAssignmentWithInitialEx(mark, parseCommaEmit)
	}
	return t.head.Kind == kEq && !t.head.isDotted()
}

// parseFunctionSignature parses function and macro definitions; it returns
// whether a body follows.
func (ps parseState) parseFunctionSignature(isFunction bool) bool {
	isAnonFunc := false
	needsParseCall := true

	mark := ps.position()
	if !isFunction {
		// Parse macro name
		ps.parseUnaryPrefix(false)
		kb := ps.peekBehind().origKind
		if ps.isInitialReservedWord(kb) {
			// macro while(ex) end  ==> (macro (call (error while) ex) (block))
			ps.emitErr(mark, kError, emptyFlags, "invalid macro name")
		}
	} else {
		if ps.peek(1) != kLparen {
			// function f() end  ==> (function (call f))
			ps.parseUnaryPrefix(false)
		} else {
			// When an initial parenthesis is present, we need to distinguish
			// between
			// * The function name in parens, followed by (args...)
			// * An anonymous function argument list in parens
			// * The whole function declaration, in parens
			ps.bump(triviaFlag)
			isEmptyTuple := ps.peekNL(1, true) == kRparen
			opts := ps.parseBrackets(kRparen, true, func(hadCommas, hadSplat bool, numSemis, numSubexprs int) bracketOpts {
				parsedCall := ps.wasEventuallyCall()
				k2 := ps.peek(2)
				needsCall := k2 == kLparen || k2 == kDot
				anon := (!needsCall && !parsedCall) || hadCommas
				return bracketOpts{needsParameters: anon, isAnonFunc: anon, parsedCall: parsedCall,
					needsParseCall:      needsCall,
					maybeGroupingParens: !hadCommas && !hadSplat && numSemis == 0 && numSubexprs == 1}
			})
			isAnonFunc = opts.isAnonFunc
			needsParseCall = opts.needsParseCall
			if isAnonFunc {
				// function (x) body end ==>  (function (tuple-p x) (block body))
				kb := ps.peekBehind().kind
				ambiguousParens := opts.maybeGroupingParens && (kb == kMacrocall || kb == kDollar)
				ps.emit(mark, kTuple, parensFlag|opts.delimFlags)
				if ambiguousParens {
					// Got something like `(@f(x))`. Is it anon `(@f(x),)` or
					// named sig `@f(x)` ??
					ps.emitErr(mark, kError, emptyFlags, "Ambiguous signature. Add a trailing comma if this is a 1-argument anonymous function; remove parentheses if this is a macro call acting as function signature.")
				}
			} else if isEmptyTuple {
				// Weird case which is consistent with parse_paren but will
				// be rejected in lowering
				// function ()(x) end  ==> (function (call (tuple-p) x) (block))
				ps.emit(mark, kTuple, parensFlag)
			} else {
				// function (A).f() end  ==> (function (call (. (parens A) f)) (block))
				ps.emit(mark, kParens, parensFlag)
			}
		}
		if !isAnonFunc {
			kb := ps.peekBehind().origKind
			if isReservedWord(kb) {
				// function begin() end  ==>  (function (call (error begin)) (block))
				ps.emitErr(mark, kError, emptyFlags, "invalid function name")
			}
		}
	}
	if needsParseCall {
		// Parse function argument list
		// function f(x,y)  end    ==>  (function (call f x y) (block))
		ps.parseCallChain(mark, false)
		sigKind := ps.peekBehind().kind
		if (sigKind == kIdentifier || sigKind == kVar || sigKind == kDollar) && ps.peekNL(1, true) == kEnd {
			// function f end ==> (function f)
			return false
		} else if sigKind == kMacrocall {
			ps.minSupportedVersion(1, 12, mark, "macro call as function signature")
		} else if sigKind != kCall {
			// function f body end  ==>  (function (error f) (block body))
			what := "macro"
			if isFunction {
				what = "function"
			}
			ps.emitErr(mark, kError, emptyFlags, "Invalid signature in "+what+" definition")
		}
	}
	if isFunction && ps.peek(1) == kDecl {
		// Function return type
		// function f()::T    end   ==>  (function (::-i (call f) T) (block))
		ps.bump(triviaFlag)
		ps.parseCall()
		ps.emit(mark, kDecl, infixFlag)
	}
	if ps.peek(1) == kWhere {
		// Function signature where syntax
		// function f() where {T} end   ==>  (function (where (call f) (braces T)) (block))
		ps.parseWhereChain(mark)
	}
	return true
}

// parseTry parses a try block.
func (ps parseState) parseTry() {
	mark := ps.position()
	ps.bump(triviaFlag)
	ps.parseBlockDefault()
	hasCatch := false
	hasFinally := false
	ps.bumpTrivia()
	if ps.peek(1) == kCatch {
		hasCatch = true
		ps.parseCatch()
	}
	ps.bumpTrivia()
	if ps.peek(1) == kElse {
		// catch-else syntax: https://github.com/JuliaLang/julia/pull/42211
		elseMark := ps.position()
		ps.bump(triviaFlag)
		ps.parseBlockDefault()
		if !hasCatch {
			//v1.8: try else x finally y end ==> (try (block) (else (error (block x))) (finally (block y)))
			ps.emitErr(elseMark, kError, emptyFlags, "Expected `catch` before `else`")
		}
		ps.minSupportedVersion(1, 8, elseMark, "`else` after `catch`")
		ps.emit(elseMark, kElse, emptyFlags)
	}
	ps.bumpTrivia()
	if ps.peek(1) == kFinally {
		finallyMark := ps.position()
		// try x finally y end  ==>  (try (block x) (finally (block y)))
		hasFinally = true
		ps.bump(triviaFlag)
		ps.parseBlockDefault()
		ps.emit(finallyMark, kFinally, emptyFlags)
	}
	// Wart: the flisp parser allows finally before catch, the *opposite*
	// order in which these blocks execute.
	ps.bumpTrivia()
	if !hasCatch && ps.peek(1) == kCatch {
		// try x finally y catch e z end  ==>  (try (block x) (finally (block y)) (catch e (block z)))
		m := ps.position()
		ps.parseCatch()
		ps.stream.diagMark(m, true, levelWarning, "`catch` after `finally` will execute out of order")
	}
	missingRecovery := !hasCatch && !hasFinally
	if missingRecovery {
		// try x end  ==>  (try (block x) (error-t))
		ps.bumpInvisible(kError, triviaFlag, "")
	}
	ps.bumpClosingToken(kEnd, "")
	ps.emit(mark, kTry, emptyFlags)
	if missingRecovery {
		ps.stream.diagMark(mark, true, levelError, "try without catch or finally")
	}
}

func (ps parseState) parseCatch() {
	mark := ps.position()
	ps.bump(triviaFlag)
	k := ps.peek(1)
	if k == kNewlineWs || k == kSemicolon || ps.isClosingToken(k) {
		// try x catch end      ==>  (try (block x) (catch □ (block)))
		ps.bumpInvisible(kPlaceholder, emptyFlags, "")
	} else {
		// try x catch e y end   ==>  (try (block x) (catch e (block y)))
		m := ps.position()
		ps.parseEqStar()
		if kb := ps.peekBehind().kind; !(kb == kIdentifier || kb == kVar || kb == kDollar) {
			// try x catch e+3 y end  ==>  (try (block x) (catch (error (call-i e + 3)) (block y)))
			ps.emitErr(m, kError, emptyFlags, "a variable name is expected after `catch`")
		}
	}
	ps.parseBlockDefault()
	ps.emit(mark, kCatch, emptyFlags)
}

// parseDo: flisp parse-do
func (ps parseState) parseDo() {
	mark := ps.position()
	ps.bump(triviaFlag) // do
	ps = ps.normalContext()
	m := ps.position()
	if k := ps.peek(1); k == kNewlineWs || k == kSemicolon {
		// f() do\nend        ==>  (call f (do (tuple) (block)))
		// this trivia needs to go into the tuple due to the way position()
		// works.
		ps.bump(triviaFlag)
	} else {
		// f() do x, y\n body end  ==>  (call f (do (tuple x y) (block body)))
		ps.parseCommaSeparated(parseState.parseRange)
	}
	ps.emit(m, kTuple, emptyFlags)
	ps.parseBlockDefault()
	ps.bumpClosingToken(kEnd, "")
	ps.emit(mark, kDo, emptyFlags)
}

func isValidMacroName(b behind) bool {
	return !isError(b.kind) && (b.isLeaf || b.kind == kVar)
}

func (ps parseState) fixMacroNameKind(pos streamPosition, nameKind Kind) {
	k := ps.peekBehindAt(pos).kind
	switch k {
	case kVar:
		pos = ps.stream.firstChildPosition(pos)
	case kParens:
		// @(A) x  ==>  (macrocall (parens @A) x)
		pos = ps.stream.firstChildPosition(pos)
		if pos == noPosition {
			return
		}
	case kError:
		// Error already reported in parse_macro_name
		return
	}
	if nameKind == kNone {
		nameKind = kMacroName
		if !isValidMacroName(ps.peekBehindAt(pos)) {
			nameKind = kError
			// TODO: This isn't quite accurate
			ps.stream.diagMarks(pos, pos, levelError, "invalid macro name")
		}
	}
	ps.resetKind(pos, nameKind)
}

// parseMacroName: flisp parse-macro-name
func (ps parseState) parseMacroName() {
	// @! x   ==>  (macrocall @! x)
	ps.bumpDisallowedSpace()
	mark := ps.position()
	ps.parseAtom(false, false)
	b := ps.peekBehindNoParens()
	if b.kind == kParens {
		ps.stream.diagMark(mark, true, levelWarning, "parenthesizing macro names is unnecessary")
	} else if !isValidMacroName(b) {
		// @[x] y z  ==>  (macrocall (error (vect x)) y z)
		ps.emitErr(mark, kError, emptyFlags, "invalid macro name")
	}
}

// parseAtsym parses an identifier, interpolation or @-prefixed symbol.
// flisp: parse-atsym
func (ps parseState) parseAtsym(allowQuotes bool) {
	ps.bumpTrivia()
	if ps.peek(1) == kAt {
		// export @a       ==>  (export @a)
		ps.bump(triviaFlag)
		ps.parseMacroName()
		ps.fixMacroNameKind(ps.position(), kNone)
		return
	}
	// export a  ==>  (export a)
	mark := ps.position()
	if allowQuotes && ps.peek(1) == kColon && !ps.isClosingToken(ps.peek(2)) {
		// import A.:+  ==>  (import (importpath A (quote-: +)))
		ps.stream.diagNext(false, levelWarning, "quoting with `:` is not required here")
	}
	ps.parseUnaryPrefix(false)
	pos := ps.position()
	warnParens := false
	if ps.peekBehindAt(pos).kind == kParens {
		// import A.(:+)  ==>  (import (importpath A (parens (quote-: +))))
		pos = ps.stream.firstChildPosition(pos)
		warnParens = true
	}
	if allowQuotes && ps.peekBehindAt(pos).kind == kQuote {
		pos = ps.stream.firstChildPosition(pos)
		if ps.peekBehindAt(pos).kind == kParens {
			// import A.:(+)  ==>  (import (importpath A (quote-: (parens +))))
			pos = ps.stream.firstChildPosition(pos)
			warnParens = true
		}
	}
	b := ps.peekBehindAt(pos)
	if warnParens && b.origKind != kDotDot {
		ps.stream.diagMark(mark, true, levelWarning, "parentheses are not required here")
	}
	ok := (b.isLeaf && (b.kind == kIdentifier || isOperator(b.kind))) ||
		(!b.isLeaf && (b.kind == kDollar || b.kind == kVar))
	if !ok {
		ps.emitErr(mark, kError, emptyFlags, "expected identifier")
	}
}

// parseImports parses import and using syntax. flisp: parse-imports
func (ps parseState) parseImports() {
	mark := ps.position()
	word := ps.peek(1)
	ps.bump(triviaFlag)
	emark := ps.position()
	initialAs := ps.parseImport(word, false)
	t := ps.peekToken(1)
	k := t.head.Kind
	hasImportPrefix := false // true if we have `prefix:` in `import prefix: stuff`
	hasComma := false
	if k == kColon && !t.precedingWhitespace {
		ps.bump(triviaFlag)
		hasImportPrefix = true
		if initialAs {
			// import A as B: x  ==>  (import (: (error (as (importpath A) B)) (importpath x)))
			ps.emitErr(emark, kError, emptyFlags, "`as` before `:` in import/using")
		}
	} else if k == kComma {
		ps.bump(triviaFlag)
		hasComma = true
	}
	if hasImportPrefix || hasComma {
		// import A, y      ==>  (import (importpath A) (importpath y))
		down := func(p parseState) { p.parseImport(word, hasImportPrefix) }
		ps.parseCommaSeparated(down)
		if ps.peek(1) == kColon {
			// Error recovery
			emark := ps.position()
			ps.bump(triviaFlag)
			ps.parseCommaSeparated(down)
			ps.emitErr(emark, kError, triviaFlag,
				"`:` can only be used when importing a single module. Split imports into multiple lines")
		}
	}
	if hasImportPrefix {
		// import A: x  ==>  (import (: (importpath A) (importpath x)))
		ps.emit(mark, kColon, emptyFlags)
	}
	// using  A  ==>  (using (importpath A))
	ps.emit(mark, word, emptyFlags)
}

// parseImport parses an individual module path and renaming with `as`.
// flisp: parse-import
func (ps parseState) parseImport(word Kind, hasImportPrefix bool) bool {
	mark := ps.position()
	ps.parseImportPath()
	// import A: x, y   ==>  (import (: (importpath A) (importpath x) (importpath y)))
	if ps.peek(1) != kAs {
		return false
	}
	// import A as B     ==>  (import (as (importpath A) B))
	ps.bump(triviaFlag)
	ps.parseAtsym(false)
	ps.emit(mark, kAs, emptyFlags)
	if word == kUsing && !hasImportPrefix {
		// using A as B     ==>  (using (error (as (importpath A) B)))
		ps.emitErr(mark, kError, emptyFlags, "`using` with `as` renaming requires a `:` and context module")
	}
	//v1.5: import A as B     ==>  (import (error (as (importpath A) B)))
	ps.minSupportedVersion(1, 6, mark, "`import ... as`")
	return true
}

// parseImportPath: flisp parse-import-path
func (ps parseState) parseImportPath() {
	mark := ps.position()
	ps.bumpTrivia()
	// The tokenizer produces conjoined dotted tokens .. and ...
	// When parsing import we must split these into single dots
	// import .A     ==> (import (importpath . A))
	firstDot := true
	for {
		t := ps.peekToken(1)
		k := t.head.Kind
		if !firstDot && t.precedingWhitespace {
			ps.stream.diagNext(true, levelWarning, "space between dots in import path")
		}
		if k == kDot {
			ps.bump(emptyFlags)
		} else if k == kDotDot {
			ps.bumpSplit(splitSpec{1, kDot, emptyFlags}, splitSpec{1, kDot, emptyFlags})
		} else if k == kDotDotDot {
			ps.bumpSplit(splitSpec{1, kDot, emptyFlags}, splitSpec{1, kDot, emptyFlags}, splitSpec{1, kDot, emptyFlags})
		} else {
			break
		}
		firstDot = false
	}
	if ps.peekToken(1).head.isDotted() {
		// Modules with operator symbol names
		// import .⋆  ==>  (import (importpath . ⋆))
		ps.bumpTrivia()
		ps.bumpSplit(splitSpec{1, kDot, emptyFlags}, splitSpec{-1, ps.peek(1), emptyFlags})
	} else {
		// import @x     ==>  (import (importpath @x))
		ps.parseAtsym(false)
	}
	for {
		t := ps.peekToken(1)
		k := t.head.Kind
		if k == kDot {
			// import A.B    ==>  (import (importpath A B))
			ps.bumpDisallowedSpace()
			ps.bump(triviaFlag)
			ps.parseAtsym(true)
		} else if t.head.isDotted() {
			// Resolve tokenization ambiguity: In imports, dots are part of
			// the path, not operators
			// import A.==   ==>  (import (importpath A ==))
			if t.precedingWhitespace {
				// Whitespace in import path allowed but discouraged
				ps.stream.diagNext(true, levelWarning, "space between dots in import path")
			}
			ps.bumpTrivia()
			m := ps.position()
			ps.bumpSplit(splitSpec{1, kDot, triviaFlag}, splitSpec{-1, k, emptyFlags})
			if isSyntacticOperator(k) {
				// import A.=  ==>  (import (importpath A (error =)))
				ps.emitErr(m, kError, emptyFlags, "syntactic operators not allowed in import")
			}
		} else if k == kDotDotDot {
			// Import the .. operator
			// import A...  ==>  (import (importpath A ..))
			ps.bumpSplit(splitSpec{1, kDot, triviaFlag}, splitSpec{2, kDotDot, emptyFlags})
		} else {
			// import A; B  ==>  (import (importpath A))
			break
		}
	}
	ps.emit(mark, kImportpath, emptyFlags)
}

// parseCommaSeparated parses comma-separated assignments, like
// "i=1:n,j=1:m,...". flisp: parse-comma-separated
func (ps parseState) parseCommaSeparated(down func(parseState)) int {
	n := 0
	for {
		down(ps)
		n++
		if ps.peek(1) != kComma {
			return n
		}
		ps.bump(triviaFlag)
	}
}

// peekSkipNewlineInGen is parser.jl's peek_skip_newline_in_gen, which skips
// a newline before =/in/∈ in generator expressions (issue #37393) when
// for_generator is set. Iteration specs are only ever parsed by `for` loops
// (normal context, for_generator false) and by parse_generator, whose
// callers (parse_brackets, parse_comprehension) set whitespace_newline: peek
// then never returns NewlineWs. It is a plain peek.
func (ps parseState) peekSkipNewlineInGen(n int) Kind {
	return ps.peek(n)
}

func isIterationAssign(k Kind) bool { return k == kEq || k == kIn || k == kElementOf }

// parseIterationSpec parses a comma-separated "assignment" but allowing
// `in` and `∈` as assignment operators. flisp: parse-iteration-spec
func (ps parseState) parseIterationSpec() {
	mark := ps.position()
	// Handle `outer` contextual keyword
	ps.withSpaceSensitive().parsePipeLt()
	if ps.peekBehind().origKind == kOuter {
		if !isIterationAssign(ps.peekSkipNewlineInGen(1)) {
			// outer i = rhs      ==>  (iteration (in (outer i) rhs))
			ps.resetBoth(ps.position(), kOuter, triviaFlag)
			ps.parsePipeLt()
			ps.emit(mark, kOuter, emptyFlags)
		}
		// otherwise not the outer keyword: outer = rhs ==> (iteration (in outer rhs))
	}
	if isIterationAssign(ps.peekSkipNewlineInGen(1)) {
		ps.bump(triviaFlag)
		ps.parsePipeLt()
	} else {
		// Recovery heuristic
		ps.recover(func(p parseState, k Kind) bool {
			return k == kComma || k == kNewlineWs || p.isClosingToken(k)
		}, emptyFlags, ps.position(), "invalid iteration spec: expected one of `=` `in` or `∈`")
	}
	ps.emit(mark, kIn, emptyFlags)
}

// parseIterationSpecs parses an iteration spec, or a comma separate list of
// such for for loops and generators.
func (ps parseState) parseIterationSpecs() {
	mark := ps.position()
	ps.parseCommaSeparated(parseState.parseIterationSpec)
	ps.emit(mark, kIteration, emptyFlags)
}

// parseSpaceSeparatedExprs: flisp parse-space-separated-exprs
func (ps parseState) parseSpaceSeparatedExprs() int {
	ps = ps.withSpaceSensitive()
	n := 0
	for {
		k := ps.peek(1)
		if ps.isClosingToken(k) || k == kNewlineWs || (ps.forGenerator && k == kFor) {
			return n
		}
		ps.parseEq()
		n++
	}
}

// parseCallArglist is like parse-arglist, but with `for` parsed as a
// generator. flisp: parse-call-arglist
func (ps parseState) parseCallArglist(closer Kind) bracketOpts {
	ps.forGenerator = true
	return ps.parseBrackets(closer, false, func(bool, bool, int, int) bracketOpts {
		return bracketOpts{needsParameters: true}
	})
}
