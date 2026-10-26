// Ported from JuliaSyntax src/tokenize.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1), itself derived from Tokenize.jl.
// Copyright (c) 2021 Julia Computing and contributors; Copyright (c) 2016
// Kristoffer Carlsson. MIT "Expat" License; see NOTICE.

package syntax

import (
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// The lexer works on characters as Go runes: a valid character is its code
// point, eofChar marks the end of input and badChar any character Julia's
// isvalid rejects. Byte positions are tracked separately, and characters are
// cut out of the input exactly as Julia's read(io, Char) cuts them, so that a
// malformed sequence covers the same bytes it covers in julia.
const (
	eofChar rune = -1
	badChar rune = -2
)

func isIdentifierChar(c rune) bool {
	if c < 0 {
		return false
	}
	if c < 0x80 {
		return asciiIsIdentifierChar[c]
	}
	return unitab.IsIDChar(uint32(c))
}

func isIdentifierStartChar(c rune) bool {
	if c >= 0 && c < 0x80 {
		return asciiIsIdentifierStart[c]
	}
	if c < 0 || c == '🢲' { // first divergence from Base.is_id_start_char
		return false
	}
	return unitab.IsIDStart(uint32(c))
}

func isInvisibleChar(c rune) bool {
	switch c {
	case '­', '​', '‌', '‍', '‎', '‏', '⁠', '⁡':
		return true
	}
	return false
}

// isNeverIDChar: chars that we will never allow to be part of a valid
// non-operator identifier.
func isNeverIDChar(ch rune) bool {
	if ch < 0 {
		return true
	}
	cat := unitab.Category(uint32(ch))
	c := uint32(ch)
	return (cat >= unitab.CatZs && cat <= unitab.CatCs) ||
		(c < 0xff && cat >= unitab.CatPd && cat <= unitab.CatPo) ||
		c == '`' ||
		(c >= 0x27e6 && c <= 0x27ef) ||
		(c >= 0x3008 && c <= 0x3011) ||
		(c >= 0x3014 && c <= 0x301b) ||
		c == 0xff08 || c == 0xff09 ||
		c == 0xff3b || c == 0xff3d
}

// opsWithUnicodeAliases are the operator characters the tokenizer normalizes
// into another kind.
var opsWithUnicodeAliases = []struct {
	c rune
	k Kind
}{
	{'−', kMinus},
	{'·', kCdot},
	{'·', kCdot},
}

func nondotSymbolicOperatorKinds() []Kind {
	excluded := map[Kind]bool{
		kErrorInvalidOperator: true, kErrorStarStar: true, kDotDotDot: true, kDot: true,
		kWhere: true, kIsa: true, kIn: true, kDotPrime: true, kOpEq: true,
	}
	var out []Kind
	for k := kBeginOps; k <= kEndOps; k++ {
		if !excluded[k] {
			out = append(out, k)
		}
	}
	return out
}

var (
	operatorStartChars = map[rune]bool{}
	unicodeOps         = map[rune]Kind{}
	opSuffixChars      = map[rune]bool{}
)

func init() {
	for _, k := range nondotSymbolicOperatorKinds() {
		s := []rune(k.String())
		operatorStartChars[s[0]] = true
		if len(s) == 1 && s[0] >= 0x80 {
			unicodeOps[s[0]] = k
		}
	}
	for _, a := range opsWithUnicodeAliases {
		operatorStartChars[a.c] = true
		unicodeOps[a.c] = a.k
	}
	for _, c := range "²³¹ʰʲʳʷʸˡˢˣᴬᴮᴰᴱᴳᴴᴵᴶᴷᴸᴹᴺᴼᴾᴿᵀᵁᵂᵃᵇᵈᵉᵍᵏᵐᵒᵖᵗᵘᵛᵝᵞᵟᵠᵡᵢᵣᵤᵥᵦᵧᵨᵩᵪᶜᶠᶥᶦᶫᶰᶸᶻᶿ′″‴‵‶‷⁗⁰ⁱ⁴⁵⁶⁷⁸⁹⁺⁻⁼⁽⁾ⁿ₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎ₐₑₒₓₕₖₗₘₙₚₛₜⱼⱽꜛꜜꜝ" {
		opSuffixChars[c] = true
	}
	for b := 0; b < 0x80; b++ {
		asciiIsIdentifierChar[b] = unitab.IsIDChar(uint32(b))
		asciiIsIdentifierStart[b] = unitab.IsIDStart(uint32(b))
		asciiIsOperatorStart[b] = operatorStartChars[rune(b)]
	}
}

// ASCII fast paths of the lexer's character classes.
var asciiIsIdentifierChar, asciiIsIdentifierStart, asciiIsOperatorStart [0x80]bool

func isOperatorStartChar(c rune) bool {
	if c >= 0 && c < 0x80 {
		return asciiIsOperatorStart[c]
	}
	return c >= 0 && operatorStartChars[c]
}

// isDottableOperatorStartChar checks whether a char is an operator which can
// be prefixed with a dot `.`
func isDottableOperatorStartChar(c rune) bool {
	return c != '?' && c != '$' && c != ':' && c != '\'' && isOperatorStartChar(c)
}

func isOpSuffix(c rune) bool {
	if c < 0xa1 || c > 0x10ffff {
		return false // also eofChar and badChar
	}
	switch unitab.Category(uint32(c)) {
	case unitab.CatMn, unitab.CatMc, unitab.CatMe:
		return true
	}
	return opSuffixChars[c]
}

func opTakesSuffix(k Kind) bool {
	if !isOperator(k) {
		return false
	}
	switch k {
	case kDotDotDot, kQuestion, kSubtype, kSupertype, kAndAnd, kOrOr, kIn, kIsa,
		kColonEqUnicode, kAssignUnicode2, kColon, kDotDot, kDollar, kDecl, kWhere,
		kDot, kNot, kDotPrime, kArrow:
		return false
	}
	return !isPrecAssignment(k) && !between(k, kNotSign, kFourthRoot)
}

// RawToken is a token of the lexer: a kind and the 0-based inclusive byte
// range of its text.
type RawToken struct {
	Kind      Kind
	StartByte int
	EndByte   int
	DotOp     bool
	Suffix    bool
}

func isDigit(c rune) bool  { return c >= '0' && c <= '9' }
func isHex(c rune) bool    { return isDigit(c) || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') }
func isBinary(c rune) bool { return c == '0' || c == '1' }
func isOctal(c rune) bool  { return c >= '0' && c <= '7' }

func isWhitespaceChar(c rune) bool {
	if c >= 0 && c < 0x80 {
		return c == ' ' || (c >= '\t' && c <= '\r') // Base.isspace on ASCII
	}
	return (c >= 0 && unitab.IsSpace(uint32(c))) || c == 0xfeff
}

type stringState struct {
	triplestr  bool
	raw        bool
	delim      rune
	parenDepth int
}

// Lexer reads Julia source and emits one token each time NextToken is
// called. As in JuliaSyntax it is mildly stateful, to disambiguate x' from
// 'x' and to lex code inside string interpolations.
type Lexer struct {
	buf    []byte
	ioPos  int // position of the next byte to decode
	start  int // start of the current token
	last   Kind
	states []stringState
	chars  [4]rune
	pos    [4]int
	dotop  bool
}

// NewLexer returns a lexer over buf, starting at byte offset start.
func NewLexer(buf []byte, start int) *Lexer {
	l := &Lexer{buf: buf, ioPos: start, last: kError}
	l.chars[0], l.pos[0] = ' ', start
	for i := 1; i < 4; i++ {
		l.chars[i] = l.readIO()
		l.pos[i] = l.ioPos
	}
	l.start = l.ioPos
	return l
}

func (l *Lexer) readIO() rune {
	if l.ioPos >= len(l.buf) {
		return eofChar
	}
	if b := l.buf[l.ioPos]; b < 0x80 {
		l.ioPos++
		return rune(b)
	}
	c, n := expr.DecodeChar(l.buf[l.ioPos:min(len(l.buf), l.ioPos+4)])
	l.ioPos += n
	if !c.IsValid() {
		return badChar
	}
	return c.Rune()
}

func (l *Lexer) peekchar() rune          { return l.chars[1] }
func (l *Lexer) dpeekchar() (rune, rune) { return l.chars[1], l.chars[2] }
func (l *Lexer) peekchar3() (rune, rune, rune) {
	return l.chars[1], l.chars[2], l.chars[3]
}
func (l *Lexer) position() int { return l.pos[0] }
func (l *Lexer) startToken()   { l.start = l.pos[0] }

func (l *Lexer) readchar() rune {
	c := l.readIO()
	l.chars = [4]rune{l.chars[1], l.chars[2], l.chars[3], c}
	l.pos = [4]int{l.pos[1], l.pos[2], l.pos[3], l.ioPos}
	return l.chars[0]
}

func (l *Lexer) accept(c rune) bool {
	if l.peekchar() == c {
		l.readchar()
		return true
	}
	return false
}

func (l *Lexer) acceptAny(set string) bool {
	pc := l.peekchar()
	for _, c := range set {
		if c == pc {
			l.readchar()
			return true
		}
	}
	return false
}

func (l *Lexer) acceptFunc(f func(rune) bool) bool {
	if f(l.peekchar()) {
		l.readchar()
		return true
	}
	return false
}

func (l *Lexer) acceptBatch(f func(rune) bool) bool {
	ok := false
	for l.acceptFunc(f) {
		ok = true
	}
	return ok
}

func (l *Lexer) emit(k Kind) RawToken { return l.emitOp(k, true) }

func (l *Lexer) emitOp(k Kind, maybeOp bool) RawToken {
	suffix := false
	if maybeOp && opTakesSuffix(k) {
		for isOpSuffix(l.peekchar()) {
			l.readchar()
			suffix = true
		}
	}
	tok := RawToken{Kind: k, StartByte: l.start, EndByte: l.position() - 1, DotOp: l.dotop, Suffix: suffix}
	l.dotop = false
	l.last = k
	return tok
}

// NextToken returns the next token; at the end of input it returns
// EndMarker tokens.
func (l *Lexer) NextToken() RawToken {
	l.startToken()
	if len(l.states) > 0 {
		return l.lexStringChunk()
	}
	return l.nextToken(l.readchar())
}

func (l *Lexer) nextToken(c rune) RawToken {
	switch {
	case c == eofChar:
		return l.emit(kEndMarker)
	case isWhitespaceChar(c):
		return l.lexWhitespace(c)
	}
	switch c {
	case '[':
		return l.emit(kLsquare)
	case ']':
		return l.emit(kRsquare)
	case '{':
		return l.emit(kLbrace)
	case ';':
		return l.emit(kSemicolon)
	case '}':
		return l.emit(kRbrace)
	case '(':
		return l.emit(kLparen)
	case ')':
		return l.emit(kRparen)
	case ',':
		return l.emit(kComma)
	case '*':
		return l.lexStar()
	case '^':
		return l.lexCircumflex()
	case '@':
		return l.emit(kAt)
	case '?':
		return l.emit(kQuestion)
	case '$':
		return l.lexDollar()
	case '⊻':
		return l.lexXor()
	case '~':
		return l.emit(kTilde)
	case '#':
		return l.lexComment()
	case '=':
		return l.lexEqual()
	case '!':
		return l.lexExclaim()
	case '>':
		return l.lexGreater()
	case '<':
		return l.lexLess()
	case ':':
		return l.lexColon()
	case '|':
		return l.lexBar()
	case '&':
		return l.lexAmper()
	case '\'':
		return l.lexPrime()
	case '÷':
		return l.lexDivision()
	case '"':
		return l.lexQuote()
	case '%':
		return l.lexPercent()
	case '/':
		return l.lexForwardslash()
	case '\\':
		return l.lexBackslash()
	case '.':
		return l.lexDot()
	case '+':
		return l.lexPlus()
	case '-':
		return l.lexMinus()
	case '−': // \minus '−' treated as hyphen '-'
		if l.accept('=') {
			return l.emit(kOpEq)
		}
		return l.emit(kMinus)
	case '`':
		return l.lexBacktick()
	}
	switch {
	case isIdentifierStartChar(c):
		return l.lexIdentifier(c)
	case isDigit(c):
		return l.lexDigit(kInteger)
	}
	if k, ok := unicodeOps[c]; ok {
		return l.emit(k)
	}
	switch {
	case c == badChar:
		return l.emit(kErrorInvalidUTF8)
	case isInvisibleChar(c):
		return l.emit(kErrorInvisibleChar)
	case isIdentifierChar(c):
		return l.emit(kErrorIdentifierStart)
	}
	return l.emit(kErrorUnknownCharacter)
}

// bidiState is a very partial implementation of UAX #9, just enough to check
// correct nesting in strings and multiline comments.
type bidiState struct{ embedding, isolate int }

func (s bidiState) update(c rune) bidiState {
	switch c {
	case '\n':
		return bidiState{}
	case '‪', '‫', '‭', '‮': // LRE RLE LRO RLO
		s.embedding++
	case '‬': // PDF
		s.embedding--
	case '⁦', '⁧', '⁨': // LRI RLI FSI
		s.isolate++
	case '⁩': // PDI
		s.isolate--
	}
	return s
}

// lexStringChunk: we're inside a string; possibly reading the string
// characters, or maybe in Julia code within an interpolation.
func (l *Lexer) lexStringChunk() RawToken {
	state := l.states[len(l.states)-1]
	if state.parenDepth > 0 {
		// Read normal Julia code inside an interpolation but track nesting of
		// parentheses.
		c := l.readchar()
		switch c {
		case '(':
			l.states[len(l.states)-1].parenDepth++
			return l.emit(kLparen)
		case ')':
			l.states[len(l.states)-1].parenDepth--
			return l.emit(kRparen)
		}
		return l.nextToken(c)
	}
	pc := l.peekchar()
	if l.last == kDollar {
		// Interpolated symbol or expression
		if pc == '(' {
			l.readchar()
			l.states[len(l.states)-1].parenDepth++
			return l.emit(kLparen)
		} else if isIdentifierStartChar(pc) {
			return l.lexIdentifier(l.readchar())
		}
		// Getting here is a syntax error - fall through to reading string
		// characters and let the parser deal with it.
	} else if l.last == kIdentifier && !(pc == eofChar || isOperatorStartChar(pc) || isNeverIDChar(pc)) {
		// Only allow certain characters after interpolated vars
		// https://github.com/JuliaLang/julia/pull/25234
		l.readchar()
		return l.emit(kErrorInvalidInterpolationTerminator)
	}
	if pc == eofChar {
		return l.emit(kEndMarker)
	} else if !state.raw && pc == '$' {
		// Start interpolation
		l.readchar()
		return l.emit(kDollar)
	} else if _, pc2, _ := l.peekchar3(); !state.raw && pc == '\\' && (pc2 == '\r' || pc2 == '\n') {
		// Process escaped newline as whitespace
		l.readchar()
		l.readchar()
		if pc2 == '\r' && l.peekchar() == '\n' {
			l.readchar()
		}
		for pc := l.peekchar(); pc == ' ' || pc == '\t'; pc = l.peekchar() {
			l.readchar()
		}
		return l.emit(kWhitespace)
	} else if pc == state.delim && l.stringTerminates(state.delim, state.triplestr) {
		if _, pc2 := l.dpeekchar(); state.delim == '\'' && l.last == kPrime && pc2 == '\'' {
			// Handle '''
			l.readchar()
			return l.emit(kChar)
		}
		// Terminate string
		l.states = l.states[:len(l.states)-1]
		l.readchar()
		if state.triplestr {
			l.readchar()
			l.readchar()
			if state.delim == '"' {
				return l.emit(kTripleDquote)
			}
			return l.emit(kTripleBacktick)
		}
		switch state.delim {
		case '"':
			return l.emitOp(kDquote, false)
		case '`':
			return l.emitOp(kBacktick, false)
		}
		return l.emitOp(kPrime, false)
	}
	// Read a chunk of string characters
	var bidi bidiState
	valid := true
	if state.raw {
		// Raw strings treat all characters as literals with the exception
		// that the closing quotes can be escaped with an odd number of \
		// characters.
		for {
			pc := l.peekchar()
			if l.stringTerminates(state.delim, state.triplestr) || pc == eofChar {
				break
			} else if state.triplestr && (pc == '\n' || pc == '\r') {
				// triple quoted newline splitting
				l.readchar()
				if pc == '\n' {
					bidi = bidiState{}
				} else if pc == '\r' && l.peekchar() == '\n' {
					bidi = bidiState{}
					l.readchar()
				}
				break
			}
			c := l.readchar()
			if c == '\\' {
				n := 1
				for l.peekchar() == '\\' {
					l.readchar()
					n++
				}
				if l.peekchar() == state.delim && n%2 != 0 {
					l.readchar()
				}
			}
			bidi = bidi.update(c)
			valid = valid && c != badChar
		}
	} else {
		for {
			pc := l.peekchar()
			if pc == '$' || pc == eofChar {
				break
			} else if state.triplestr && (pc == '\n' || pc == '\r') {
				// triple quoted newline splitting
				l.readchar()
				if pc == '\n' {
					bidi = bidiState{}
				} else if pc == '\r' && l.peekchar() == '\n' {
					l.readchar()
					bidi = bidiState{}
				}
				break
			} else if pc == state.delim && l.stringTerminates(state.delim, state.triplestr) {
				break
			} else if pc == '\\' {
				// Escaped newline
				_, pc2, pc3 := l.peekchar3()
				if pc2 == '\r' || pc2 == '\n' {
					if pc2 == '\n' || pc3 == '\n' {
						bidi = bidiState{}
					}
					break
				}
			}
			c := l.readchar()
			if c == '\\' {
				c = l.readchar()
				if c == eofChar {
					break
				}
			}
			bidi = bidi.update(c)
			valid = valid && c != badChar
		}
	}
	var outk Kind
	switch {
	case !valid:
		outk = kErrorInvalidUTF8
	case state.delim == '\'':
		outk = kChar
	case bidi != (bidiState{}):
		outk = kErrorBidiFormatting
	case state.delim == '"':
		outk = kString
	default: // '`'
		outk = kCmdString
	}
	return l.emit(outk)
}

// lexWhitespace: a whitespace char c has been consumed.
func (l *Lexer) lexWhitespace(c rune) RawToken {
	k := kWhitespace
	for {
		if c == '\n' {
			k = kNewlineWs
		}
		pc, ppc := l.dpeekchar()
		// stop on non whitespace and limit to a single newline in a token
		if !isWhitespaceChar(pc) || (k == kNewlineWs && (pc == '\n' || (pc == '\r' && ppc == '\n'))) {
			break
		}
		c = l.readchar()
	}
	return l.emit(k)
}

func (l *Lexer) lexComment() RawToken {
	if l.peekchar() != '=' {
		valid := true
		for {
			pc, ppc := l.dpeekchar()
			if pc == '\n' || (pc == '\r' && ppc == '\n') || pc == eofChar {
				if valid {
					return l.emit(kComment)
				}
				return l.emit(kErrorInvalidUTF8)
			}
			valid = valid && pc != badChar
			l.readchar()
		}
	}
	c := l.readchar() // consume the '='
	var bidi bidiState
	skip := true // true => c was part of the prev comment marker pair
	nesting := 1
	valid := true
	for {
		if c == eofChar {
			return l.emit(kErrorEofMultiComment)
		}
		nc := l.readchar()
		bidi = bidi.update(nc)
		valid = valid && nc != badChar
		if skip {
			skip = false
		} else if c == '#' && nc == '=' {
			nesting++
			skip = true
		} else if c == '=' && nc == '#' {
			nesting--
			skip = true
			if nesting == 0 {
				switch {
				case !valid:
					return l.emit(kErrorInvalidUTF8)
				case bidi != (bidiState{}):
					return l.emit(kErrorBidiFormatting)
				}
				return l.emit(kComment)
			}
		}
		c = nc
	}
}

// lexGreater: a '>' has been consumed.
func (l *Lexer) lexGreater() RawToken {
	switch {
	case l.accept('>'):
		if l.accept('>') {
			if l.accept('=') {
				return l.emit(kOpEq)
			}
			return l.emit(kUshr)
		} else if l.accept('=') {
			return l.emit(kOpEq)
		}
		return l.emit(kShr)
	case l.accept('='):
		return l.emit(kGreaterEq)
	case l.accept(':'):
		return l.emit(kSupertype)
	}
	return l.emit(kGreater)
}

// lexLess: a '<' has been consumed.
func (l *Lexer) lexLess() RawToken {
	switch {
	case l.accept('<'):
		if l.accept('=') {
			return l.emit(kOpEq)
		}
		return l.emit(kShl)
	case l.accept('='):
		return l.emit(kLessEq)
	case l.accept(':'):
		return l.emit(kSubtype)
	case l.accept('|'):
		return l.emit(kPipeLt)
	}
	if pc, ppc := l.dpeekchar(); pc == '-' && ppc == '-' {
		l.readchar()
		l.readchar()
		if l.accept('-') {
			return l.emit(kErrorInvalidOperator)
		}
		// tokenize.jl tests accept('-') once more after accept('>'): a
		// third '-' has been taken just above, so that branch is dead.
		if l.accept('>') {
			return l.emit(kBothLongArrow)
		}
		return l.emit(kLeftLongArrow)
	}
	return l.emit(kLess)
}

// lexEqual lexes all tokens that start with an = character.
func (l *Lexer) lexEqual() RawToken {
	if l.accept('=') {
		if l.accept('=') {
			return l.emit(kEqEqEq)
		}
		return l.emit(kEqEq)
	} else if l.accept('>') {
		return l.emit(kPairArrow)
	}
	return l.emit(kEq)
}

func (l *Lexer) lexColon() RawToken {
	if l.accept(':') {
		return l.emit(kDecl)
	} else if l.accept('=') {
		return l.emit(kColonEq)
	}
	return l.emit(kColon)
}

func (l *Lexer) lexExclaim() RawToken {
	if l.accept('=') {
		if l.accept('=') {
			return l.emit(kNotEqEq)
		}
		return l.emit(kNotEq)
	}
	return l.emit(kNot)
}

func (l *Lexer) lexPercent() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kPercent)
}

func (l *Lexer) lexBar() RawToken {
	switch {
	case l.accept('='):
		return l.emit(kOpEq)
	case l.accept('>'):
		return l.emit(kPipeGt)
	case l.accept('|'):
		return l.emit(kOrOr)
	}
	return l.emit(kBar)
}

func (l *Lexer) lexPlus() RawToken {
	if l.accept('+') {
		return l.emit(kPlusPlus)
	} else if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kPlus)
}

func (l *Lexer) lexMinus() RawToken {
	if l.accept('-') {
		if l.accept('>') {
			return l.emit(kLongArrow)
		}
		return l.emit(kErrorInvalidOperator) // "--" is an invalid operator
	} else if !l.dotop && l.accept('>') {
		return l.emit(kArrow)
	} else if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kMinus)
}

func (l *Lexer) lexStar() RawToken {
	if l.accept('*') {
		return l.emit(kErrorStarStar) // "**" is an invalid operator use ^
	} else if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kStar)
}

func (l *Lexer) lexCircumflex() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kCaret)
}

func (l *Lexer) lexDivision() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kDivide)
}

func (l *Lexer) lexDollar() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kDollar)
}

func (l *Lexer) lexXor() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kXor)
}

func (l *Lexer) acceptNumber(f func(rune) bool) bool {
	lexed := false
	for {
		pc, ppc := l.dpeekchar()
		if pc == '_' && !f(ppc) {
			return lexed
		} else if f(pc) || pc == '_' {
			l.readchar()
		} else {
			return lexed
		}
		lexed = true
	}
}

func isExpChar(pc, ppc rune) bool {
	return (pc == 'e' || pc == 'E' || pc == 'f') && (isDigit(ppc) || ppc == '+' || ppc == '-' || ppc == '−')
}

// lexDigit: a digit has been consumed.
func (l *Lexer) lexDigit(kind Kind) RawToken {
	l.acceptNumber(isDigit)
	pc, ppc := l.dpeekchar()
	if pc == '.' {
		if ppc == '.' {
			// Number followed by K".." or K"..."
			return l.emit(kind)
		} else if kind == kFloat {
			// If we enter the function with kind == K"Float" then a '.' has
			// been parsed.
			l.readchar()
			return l.emit(kErrorInvalidNumericConstant)
		} else if isDottableOperatorStartChar(ppc) {
			l.readchar()
			return l.emit(kErrorAmbiguousNumericConstant) // `1.+`
		}
		l.readchar()

		kind = kFloat
		if l.accept('_') {
			return l.emit(kErrorInvalidNumericConstant) // `1._`
		}
		hadFractionDigits := l.acceptNumber(isDigit)
		pc, ppc = l.dpeekchar()
		if isExpChar(pc, ppc) {
			kind = kFloat
			if pc == 'f' {
				kind = kFloat32
			}
			l.readchar()
			l.acceptAny("+-−")
			if l.acceptBatch(isDigit) {
				pc, ppc = l.dpeekchar()
				if pc == '.' && !isDottableOperatorStartChar(ppc) {
					l.readchar()
					return l.emit(kErrorInvalidNumericConstant) // `1.e1.`
				}
			} else {
				return l.emit(kErrorInvalidNumericConstant) // `1.e`
			}
		} else if pc == '.' && ppc != '.' && !isDottableOperatorStartChar(ppc) {
			l.readchar()
			return l.emit(kErrorInvalidNumericConstant) // `1.1.`
		} else if !hadFractionDigits && (isIdentifierStartChar(pc) ||
			pc == '(' || pc == '[' || pc == '{' || pc == '@' || pc == '`' || pc == '"') {
			return l.emit(kErrorAmbiguousNumericDotMultiply) // `1.(` `1.x`
		}
	} else if isExpChar(pc, ppc) {
		kind = kFloat
		if pc == 'f' {
			kind = kFloat32
		}
		l.readchar()
		l.acceptAny("+-−")
		if l.acceptBatch(isDigit) {
			pc, ppc = l.dpeekchar()
			if pc == '.' && !isDottableOperatorStartChar(ppc) {
				l.accept('.')
				return l.emit(kErrorInvalidNumericConstant) // `1e1.`
			}
		} else {
			return l.emit(kErrorInvalidNumericConstant) // `1e+`
		}
	} else if l.position()-l.start == 1 && l.chars[0] == '0' {
		isBinOctHexInt := false
		hadDigits := false
		switch pc {
		case 'x':
			kind = kHexInt
			isfloat := false
			l.readchar()
			hadDigits = l.acceptNumber(isHex)
			pc, ppc = l.dpeekchar()
			if pc == '.' && ppc != '.' {
				l.readchar()
				hadDigits = l.acceptNumber(isHex) || hadDigits
				isfloat = true
			}
			if l.acceptAny("pP") {
				kind = kFloat
				l.acceptAny("+-−")
				if !l.acceptNumber(isDigit) || !hadDigits {
					return l.emit(kErrorInvalidNumericConstant) // `0x1p` `0x.p0`
				}
			} else if isfloat {
				return l.emit(kErrorHexFloatMustContainP) // `0x.` `0x1.0`
			}
			isBinOctHexInt = !isfloat
		case 'b':
			l.readchar()
			hadDigits = l.acceptNumber(isBinary)
			kind = kBinInt
			isBinOctHexInt = true
		case 'o':
			l.readchar()
			hadDigits = l.acceptNumber(isOctal)
			kind = kOctInt
			isBinOctHexInt = true
		}
		if isBinOctHexInt {
			pc = l.peekchar()
			if !hadDigits || isDigit(pc) || isIdentifierStartChar(pc) {
				l.acceptBatch(func(c rune) bool { return isDigit(c) || isIdentifierStartChar(c) })
				// `0x` `0xg` `0x_` `0x-`
				// `0b123` `0o78p` `0xenomorph` `0xaα`
				return l.emit(kErrorInvalidNumericConstant)
			}
		}
	}
	return l.emit(kind)
}

func (l *Lexer) lexPrime() RawToken {
	k := l.last
	if k == kIdentifier || isContextualKeyword(k) || isWordOperator(k) || k == kDot ||
		k == kRparen || k == kRsquare || k == kRbrace || k == kPrime || k == kEnd || isLiteral(k) {
		return l.emit(kPrime)
	}
	l.states = append(l.states, stringState{raw: true, delim: '\''})
	return l.emitOp(kPrime, false)
}

func (l *Lexer) lexAmper() RawToken {
	switch {
	case l.accept('&'):
		return l.emit(kAndAnd)
	case l.accept('='):
		return l.emit(kOpEq)
	}
	return l.emit(kAmp)
}

// lexQuote parses a token starting with a quote; a '"' has been consumed.
func (l *Lexer) lexQuote() RawToken {
	raw := l.last == kIdentifier || isContextualKeyword(l.last) || isWordOperator(l.last)
	pc, dpc := l.dpeekchar()
	triplestr := pc == '"' && dpc == '"'
	l.states = append(l.states, stringState{triplestr: triplestr, raw: raw, delim: '"'})
	if triplestr {
		l.readchar()
		l.readchar()
		return l.emit(kTripleDquote)
	}
	return l.emit(kDquote)
}

func (l *Lexer) stringTerminates(delim rune, triplestr bool) bool {
	if triplestr {
		c1, c2, c3 := l.peekchar3()
		return c1 == delim && c2 == delim && c3 == delim
	}
	return l.peekchar() == delim
}

// lexForwardslash: a '/' has been consumed.
func (l *Lexer) lexForwardslash() RawToken {
	if l.accept('/') {
		if l.accept('=') {
			return l.emit(kOpEq)
		}
		return l.emit(kRational)
	} else if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kSlash)
}

func (l *Lexer) lexBackslash() RawToken {
	if l.accept('=') {
		return l.emit(kOpEq)
	}
	return l.emit(kBackslash)
}

func (l *Lexer) lexDot() RawToken {
	if l.accept('.') {
		if l.accept('.') {
			return l.emit(kDotDotDot)
		}
		if isDottableOperatorStartChar(l.peekchar()) {
			l.readchar()
			return l.emit(kErrorInvalidOperator)
		}
		return l.emit(kDotDot)
	} else if isDigit(l.peekchar()) {
		return l.lexDigit(kFloat)
	}
	pc, dpc := l.dpeekchar()
	switch {
	case pc == '+':
		l.dotop = true
		l.readchar()
		return l.lexPlus()
	case pc == '-':
		l.dotop = true
		l.readchar()
		return l.lexMinus()
	case pc == '−':
		l.dotop = true
		l.readchar()
		if l.accept('=') {
			return l.emit(kOpEq)
		}
		return l.emit(kMinus)
	case pc == '*':
		l.dotop = true
		l.readchar()
		return l.lexStar()
	case pc == '/':
		l.dotop = true
		l.readchar()
		return l.lexForwardslash()
	case pc == '\\':
		l.dotop = true
		l.readchar()
		return l.lexBackslash()
	case pc == '^':
		l.dotop = true
		l.readchar()
		return l.lexCircumflex()
	case pc == '<':
		l.dotop = true
		l.readchar()
		return l.lexLess()
	case pc == '>':
		l.dotop = true
		l.readchar()
		return l.lexGreater()
	case pc == '&':
		l.dotop = true
		l.readchar()
		if l.accept('=') {
			return l.emit(kOpEq)
		}
		if l.accept('&') {
			return l.emit(kAndAnd)
		}
		return l.emit(kAmp)
	case pc == '%':
		l.dotop = true
		l.readchar()
		return l.lexPercent()
	case pc == '=' && dpc != '>':
		l.dotop = true
		l.readchar()
		return l.lexEqual()
	case pc == '|':
		l.dotop = true
		l.readchar()
		if l.accept('|') {
			return l.emit(kOrOr)
		}
		return l.lexBar()
	case pc == '!' && dpc == '=':
		l.dotop = true
		l.readchar()
		return l.lexExclaim()
	case pc == '⊻':
		l.dotop = true
		l.readchar()
		return l.lexXor()
	case pc == '÷':
		l.dotop = true
		l.readchar()
		return l.lexDivision()
	case pc == '=' && dpc == '>':
		l.dotop = true
		l.readchar()
		return l.lexEqual()
	case isDottableOperatorStartChar(pc):
		l.dotop = true
		return l.nextToken(l.readchar())
	}
	return l.emit(kDot)
}

// lexBacktick: a ` has been consumed.
func (l *Lexer) lexBacktick() RawToken {
	pc, dpc := l.dpeekchar()
	triplestr := pc == '`' && dpc == '`'
	// Backticks always contain raw strings only. See discussion on bug
	// https://github.com/JuliaLang/julia/issues/3150
	l.states = append(l.states, stringState{triplestr: triplestr, raw: true, delim: '`'})
	if triplestr {
		l.readchar()
		l.readchar()
		return l.emit(kTripleBacktick)
	}
	return l.emit(kBacktick)
}

const maxKwLength = 10

var keywordKinds = map[string]Kind{}

func init() {
	for _, k := range []Kind{
		kBaremodule, kBegin, kBreak, kCatch, kConst, kContinue, kDo, kElse, kElseif, kEnd,
		kExport, kFinally, kFor, kFunction, kGlobal, kIf, kImport, kLet, kLocal, kMacro,
		kModule, kPublic, kQuote, kReturn, kStruct, kTry, kUsing, kWhile, kIn, kIsa, kWhere,
		kAbstract, kAs, kDoc, kMutable, kOuter, kPrimitive, kType, kVar,
	} {
		keywordKinds[k.String()] = k
	}
}

func isGraphemeBreak(state *int32, c1, c2 rune) bool {
	if c1 < 0 || c2 < 0 {
		*state = 0
		return true
	}
	return unitab.GraphemeBreak(state, uint32(c1), uint32(c2))
}

// lexIdentifier lexes an identifier or keyword whose first char c has been
// consumed. JuliaSyntax recognises keywords by a 5-bit-per-letter hash of at
// most ten characters, which is exact for its keyword list; a lookup of the
// identifier text is the same function.
func (l *Lexer) lexIdentifier(c rune) RawToken {
	n := 1
	ascii := c < 0x80
	var graphemeState int32
	if ascii {
		graphemeState = 1 // all ASCII id chars are UTF8PROC_BOUNDCLASS_OTHER
	}
	for {
		pc, ppc := l.dpeekchar()
		ascii = ascii && pc >= 0 && pc < 0x80
		if ascii { // fast path
			if (pc == '!' && ppc == '=') || !asciiIsIdentifierChar[pc] {
				break
			}
		} else if isGraphemeBreak(&graphemeState, c, pc) {
			if (pc == '!' && ppc == '=') || !isIdentifierChar(pc) {
				break
			}
		} else if pc == '‌' || pc == '‍' { // ZWNJ/ZWJ control characters
			// ZWJ/ZWNJ only within grapheme sequences, not at end
			peek := graphemeState
			if isGraphemeBreak(&peek, pc, ppc) {
				break
			}
		}
		c = l.readchar()
		n++
	}
	if n > maxKwLength {
		return l.emit(kIdentifier)
	}
	text := l.buf[l.start:l.position()]
	if string(text) == "true" || string(text) == "false" {
		return l.emit(kBool)
	}
	if k, ok := keywordKinds[string(text)]; ok { // no allocation: map index by string(bytes)
		return l.emit(k)
	}
	return l.emit(kIdentifier)
}
