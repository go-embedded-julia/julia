// Ported from JuliaSyntax src/literal_parsing.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"bytes"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// errorVal is JuliaSyntax's ErrorVal: the value of a malformed literal.
type errorVal struct{}

// largeInt stands for an integer literal whose value is an Int128, UInt128
// or BigInt. The Expr form never holds those values: it holds a call of the
// named string macro on the literal's text instead.
type largeInt struct{ macro expr.Symbol }

var (
	minInt128 = new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	maxInt128 = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
)

func parseIntLiteral(s string) any {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "_", ""), "−", "-")
	if x, err := strconv.ParseInt(s, 10, 64); err == nil {
		return x
	}
	v, ok := new(big.Int).SetString(strings.TrimPrefix(s, "+"), 10)
	if ok && v.Cmp(minInt128) >= 0 && v.Cmp(maxInt128) <= 0 {
		return largeInt{"@int128_str"}
	}
	return largeInt{"@big_str"}
}

func parseUintLiteral(s string, k Kind) any {
	s = strings.TrimPrefix(strings.ReplaceAll(s, "_", ""), "+")
	ndigits := len(s) - 2
	digits := s[2:]
	parse := func(bits int) uint64 {
		base := 16
		if k == kBinInt {
			base = 2
		}
		v, _ := strconv.ParseUint(digits, base, bits)
		return v
	}
	switch k {
	case kHexInt:
		switch {
		case ndigits <= 2:
			return uint8(parse(8))
		case ndigits <= 4:
			return uint16(parse(16))
		case ndigits <= 8:
			return uint32(parse(32))
		case ndigits <= 16:
			return parse(64)
		case ndigits <= 32:
			return largeInt{"@uint128_str"}
		}
		return largeInt{"@big_str"}
	case kBinInt:
		switch {
		case ndigits <= 8:
			return uint8(parse(8))
		case ndigits <= 16:
			return uint16(parse(16))
		case ndigits <= 32:
			return uint32(parse(32))
		case ndigits <= 64:
			return parse(64)
		case ndigits <= 128:
			return largeInt{"@uint128_str"}
		}
		return largeInt{"@big_str"}
	}
	// OctInt
	x, err := strconv.ParseUint(digits, 8, 64)
	if err != nil {
		v, _ := new(big.Int).SetString(digits, 8)
		if v.BitLen() <= 128 && ndigits <= 43 {
			return largeInt{"@uint128_str"}
		}
		return largeInt{"@big_str"}
	}
	switch {
	case ndigits <= 3 && x <= math.MaxUint8:
		return uint8(x)
	case ndigits <= 6 && x <= math.MaxUint16:
		return uint16(x)
	case ndigits <= 11 && x <= math.MaxUint32:
		return uint32(x)
	case ndigits <= 22:
		return x
	case ndigits <= 43:
		return largeInt{"@uint128_str"}
	}
	return largeInt{"@big_str"}
}

// unescapeRawString processes the escapes of a raw string, 1-based
// [firstind, endind) of txtbuf.
func unescapeRawString(out *bytes.Buffer, txtbuf []byte, firstind, endind int, isCmd bool) {
	delim := byte('"')
	if isCmd {
		delim = '`'
	}
	at := func(i int) byte { return txtbuf[i-1] }
	i := firstind
	for i < endind {
		c := at(i)
		if c != '\\' {
			if c == '\r' {
				// convert literal \r and \r\n in strings to \n (issue #11988)
				if i+1 < endind && at(i+1) == '\n' {
					i++
				}
				c = '\n'
			}
			out.WriteByte(c)
			i++
			continue
		}
		// Process \ escape sequences
		j := i
		for j < endind && at(j) == '\\' {
			j++
		}
		nbackslash := j - i
		if (j < endind && at(j) == delim) || j >= endind {
			// Backslashes before a delimiter must also be escaped
			nbackslash /= 2
		}
		for k := 0; k < nbackslash; k++ {
			out.WriteByte('\\')
		}
		i = j
		if i < endind {
			out.WriteByte(at(i))
			i++
		}
	}
}

func isSyntaxKind(k Kind) bool { return between(k, kBeginSyntaxKinds, kEndSyntaxKinds) }

// normalizeIdentifier is JuliaSyntax's normalize_identifier.
func normalizeIdentifier(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return unitab.NormalizeIdentifier(s)
		}
	}
	return s
}

// parseJuliaLiteral returns the Julia value of a leaf token, as it is
// represented in Expr form (1-based inclusive srcrange [first, last]).
func parseJuliaLiteral(txtbuf []byte, head SyntaxHead, first, last int) any {
	k := head.Kind
	switch {
	case k == kFloat:
		v, code := parseFloatLiteral(64, txtbuf, first, last+1)
		if code != floatOverflow {
			return v
		}
		return errorVal{}
	case k == kFloat32:
		v, code := parseFloatLiteral(32, txtbuf, first, last+1)
		if code != floatOverflow {
			return float32(v)
		}
		return errorVal{}
	case k == kChar:
		var b bytes.Buffer
		var d []Diagnostic
		if unescapeJuliaString(&b, txtbuf, first, last+1, &d) {
			return errorVal{}
		}
		c, n := expr.DecodeChar(b.Bytes())
		if n == b.Len() && n > 0 {
			return c
		}
		return errorVal{}
	case k == kString || k == kCmdString:
		var b bytes.Buffer
		if head.Flags&rawStringFlag != 0 {
			unescapeRawString(&b, txtbuf, first, last+1, k == kCmdString)
		} else {
			var d []Diagnostic
			if unescapeJuliaString(&b, txtbuf, first, last+1, &d) {
				return errorVal{}
			}
		}
		return b.String()
	case k == kBool:
		return txtbuf[first-1] == 't'
	}
	valStr := string(txtbuf[first-1 : last])
	switch {
	case k == kInteger:
		return parseIntLiteral(valStr)
	case k == kBinInt || k == kOctInt || k == kHexInt:
		return parseUintLiteral(valStr, k)
	case k == kIdentifier || k == kPlaceholder:
		if head.Flags&rawStringFlag != 0 {
			var b bytes.Buffer
			unescapeRawString(&b, txtbuf, first, last+1, false)
			return expr.Symbol(normalizeIdentifier(b.String()))
		}
		return expr.Symbol(normalizeIdentifier(valStr))
	case isOperator(k):
		if first > last {
			// synthetic invisible tokens
			return expr.Symbol(k.String())
		}
		return expr.Symbol(normalizeIdentifier(valStr))
	case k == kError:
		return errorVal{}
	case k == kMacroName:
		return expr.Symbol("@" + normalizeIdentifier(valStr))
	case k == kStringMacroName:
		return expr.Symbol("@" + normalizeIdentifier(valStr) + "_str")
	case k == kCmdMacroName:
		return expr.Symbol("@" + normalizeIdentifier(valStr) + "_cmd")
	case isSyntaxKind(k):
		return nil
	case isKeyword(k):
		// This should only happen for tokens nested inside errors
		return expr.Symbol(valStr)
	}
	// Other kinds should only happen for tokens nested inside errors
	return errorVal{}
}
