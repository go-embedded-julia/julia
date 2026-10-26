// Ported from JuliaSyntax src/literal_parsing.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1): the literal checks validate_tokens runs (floats, string
// and character escapes).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// floatStatus is the outcome of jl_strtod_c / jl_strtof_c.
type floatStatus int

const (
	floatOK floatStatus = iota
	floatUnderflow
	floatOverflow
)

// parseFloatLiteral parses a Float or Float32 token's text (1-based
// [firstind, endind) of txtbuf) as Julia's parse_float_literal does: '_' is
// dropped, '−' read as '-', and Float32's 'f' exponent read as 'e'.
func parseFloatLiteral(bits int, txtbuf []byte, firstind, endind int) (float64, floatStatus) {
	s := string(txtbuf[firstind-1 : endind-1])
	s = strings.ReplaceAll(strings.ReplaceAll(s, "_", ""), "−", "-")
	if bits == 32 {
		if i := strings.LastIndexByte(s, 'f'); i >= 0 {
			s = s[:i] + "e" + s[i+1:]
		}
	}
	x, err := strconv.ParseFloat(s, bits)
	if err != nil {
		return x, floatOverflow
	}
	if x == 0 && mantissaNonzero(s) {
		return x, floatUnderflow
	}
	return x, floatOK
}

// mantissaNonzero reports whether a float literal's mantissa has a nonzero
// digit, i.e. whether a zero result is an underflow.
func mantissaNonzero(s string) bool {
	hex := strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "-0x") || strings.HasPrefix(s, "+0x")
	if hex {
		s = s[strings.Index(s, "x")+1:]
	}
	for _, c := range s {
		switch {
		case hex && (c == 'p' || c == 'P'), !hex && (c == 'e' || c == 'E'):
			return false
		case c >= '1' && c <= '9', hex && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'):
			return true
		}
	}
	return false
}

// unescapeJuliaString processes the escapes of a non-raw string or char,
// 1-based [firstind, endind) of txtbuf, appending diagnostics for invalid
// escapes. It reports whether there was an error.
func unescapeJuliaString(out *bytes.Buffer, txtbuf []byte, firstind, endind int, diags *[]Diagnostic) bool {
	at := func(i int) byte { return txtbuf[i-1] }
	hadError := false
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
		// Process \ escape sequences. See also Base.unescape_string which
		// some of this code derives from (but which disallows \` \' \$)
		escstart := i
		i++
		if i >= endind {
			*diags = append(*diags, newDiag(escstart, endind-1, levelError, "invalid escape sequence"))
			hadError = true
			break
		}
		c = at(i)
		switch {
		case c == 'x' || c == 'u' || c == 'U':
			n, k := 0, 0
			m := 8
			switch c {
			case 'x':
				m = 2
			case 'u':
				m = 4
			}
			for {
				k++
				if !(k <= m && i+1 < endind) {
					break
				}
				d, ok := hexValue(at(i + 1))
				if !ok {
					break
				}
				n = n<<4 + d
				i++
			}
			if k == 1 || n > 0x10ffff {
				msg := "invalid unicode escape sequence"
				if m == 2 {
					msg = "invalid hex escape sequence"
				}
				*diags = append(*diags, newDiag(escstart, i, levelError, msg))
				hadError = true
			} else if m == 2 {
				out.WriteByte(byte(n))
			} else {
				out.Write(expr.CharFromRune(rune(n)).Bytes())
			}
		case c >= '0' && c <= '7':
			k := 1
			n := int(c - '0')
			for {
				k++
				if !(k <= 3 && i+1 < endind) {
					break
				}
				c = at(i + 1)
				if !(c >= '0' && c <= '7') {
					break
				}
				n = n<<3 + int(c-'0')
				i++
			}
			if n > 255 {
				*diags = append(*diags, newDiag(escstart, i, levelError, "invalid octal escape sequence"))
				hadError = true
			} else {
				out.WriteByte(byte(n))
			}
		default:
			var u byte
			ok := true
			switch c {
			case 'n':
				u = '\n'
			case 't':
				u = '\t'
			case 'r':
				u = '\r'
			case 'e':
				u = 0x1b
			case 'b':
				u = '\b'
			case 'f':
				u = '\f'
			case 'v':
				u = '\v'
			case 'a':
				u = 0x07
			case '\\', '\'', '"', '$', '`':
				u = c
			default:
				ok = false
			}
			if !ok {
				*diags = append(*diags, newDiag(escstart, i, levelError, "invalid escape sequence"))
				hadError = true
			} else {
				out.WriteByte(u)
			}
		}
		// For non-ascii characters we may not be in the middle of the UTF-8
		// encoding for that char, but this doesn't matter because unescaping
		// only relies on the ascii subset.
		i++
	}
	return hadError
}

func hexValue(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}
