// Ported from JuliaSyntax src/kinds.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): the kind predicates of the parser and the token error
// descriptions.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

// untokenize(k) is the kind's name wherever the parser uses it: in error
// messages it is applied only to kinds that stand for exactly one token
// text (closing tokens, reserved words, `<`, `>`), so the `unique=true`
// filtering of kinds.jl never applies there; Kind.String serves.

var tokenErrorDescriptions = map[Kind]string{}

func init() {
	for n, d := range map[string]string{
		"ErrorEofMultiComment":                "unterminated multi-line comment #= ... =#",
		"ErrorInvalidNumericConstant":         "invalid numeric constant",
		"ErrorHexFloatMustContainP":           "hex float literal must contain `p` or `P`",
		"ErrorAmbiguousNumericConstant":       "ambiguous `.` syntax; add whitespace to clarify (eg `1.+2` might be `1.0+2` or `1 .+ 2`)",
		"ErrorAmbiguousNumericDotMultiply":    "numeric constant cannot be implicitly multiplied because it ends with `.`",
		"ErrorInvalidInterpolationTerminator": "interpolated variable ends with invalid character; use `$(...)` instead",
		"ErrorNumericOverflow":                "overflow in numeric literal",
		"ErrorInvalidEscapeSequence":          "invalid string escape sequence",
		"ErrorOverLongCharacter":              "character literal contains multiple characters",
		"ErrorInvalidUTF8":                    "invalid UTF-8 sequence",
		"ErrorInvisibleChar":                  "invisible character",
		"ErrorIdentifierStart":                "identifier cannot begin with character",
		"ErrorUnknownCharacter":               "unknown unicode character",
		"ErrorBidiFormatting":                 "unbalanced bidirectional unicode formatting",
		"ErrorInvalidOperator":                "invalid operator",
		"Error**":                             "use `x^y` instead of `x**y` for exponentiation, and `x...` instead of `**x` for splatting",
		"error":                               "unknown error token",
	} {
		tokenErrorDescriptions[K(n)] = d
	}
}

func isError(k Kind) bool {
	return between(k, kBeginErrors, kEndErrors) || k == kErrorInvalidOperator || k == kErrorStarStar
}
func isKeyword(k Kind) bool { return between(k, kBeginKeywords, kEndKeywords) }
func isBlockContinuationKeyword(k Kind) bool {
	return between(k, kBeginBlockContinuation, kEndBlockContinuation)
}
func isNumber(k Kind) bool { return between(k, kBeginNumbers, kEndNumbers) }

// Predicates for operator precedence.

func isPrecPair(k Kind) bool            { return k == kPairArrow }
func isPrecArrow(k Kind) bool           { return between(k, kBeginArrow, kEndArrow) }
func isPrecLazyOr(k Kind) bool          { return k == kOrOr }
func isPrecLazyAnd(k Kind) bool         { return k == kAndAnd }
func isPrecComparison(k Kind) bool      { return between(k, kBeginComparison, kEndComparison) }
func isPrecPipeLt(k Kind) bool          { return k == kPipeLt }
func isPrecPipeGt(k Kind) bool          { return k == kPipeGt }
func isPrecColon(k Kind) bool           { return between(k, kBeginColon, kEndColon) }
func isPrecPlus(k Kind) bool            { return between(k, kBeginPlus, kEndPlus) }
func isPrecBitshift(k Kind) bool        { return between(k, kShl, kUshr) }
func isPrecTimes(k Kind) bool           { return between(k, kBeginTimes, kEndTimes) }
func isPrecRational(k Kind) bool        { return k == kRational }
func isPrecPower(k Kind) bool           { return between(k, kBeginPower, kEndPower) }
func isSyntacticAssignment(k Kind) bool { return k == kEq || k == kOpEq || k == kColonEq }

func isStringDelim(k Kind) bool { return k == kDquote || k == kTripleDquote }
func isRadicalOp(k Kind) bool   { return k == kSqrt || k == kCbrt || k == kFourthRoot }

func isWhitespace(k Kind) bool { return k == kWhitespace || k == kNewlineWs || k == kComment }

func isSyntacticOperator(k Kind) bool {
	switch k {
	case kAndAnd, kOrOr, kDot, kDotDotDot, kArrow:
		return true
	}
	return isSyntacticAssignment(k)
}
