// Julia's repr of strings and characters, as JuliaSyntax interpolates them
// into diagnostics. Ported from Base (base/strings/io.jl escape_string and
// print_quoted, base/char.jl show(::IO, ::AbstractChar)), Julia 1.12.6.
// Copyright (c) 2009-2024 Jeff Bezanson, Stefan Karpinski, Viral B. Shah, and
// other contributors. MIT License; see NOTICE.

package syntax

import (
	"strconv"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// juliaChars cuts s into Julia characters.
func juliaChars(s string) []expr.Char {
	var out []expr.Char
	for i := 0; i < len(s); {
		c, n := expr.DecodeChar([]byte(s[i:min(len(s), i+4)]))
		out = append(out, c)
		i += n
	}
	return out
}

// isPrintChar is Base.isprint for the characters it is asked about here,
// which are never malformed or overlong (the callers test those first).
func isPrintChar(c expr.Char) bool {
	cp, _ := c.CodePoint()
	cat := unitab.Category(cp)
	return cat >= 1 && cat <= unitab.CatZs
}

func hexPad(u uint32, pad int) string {
	s := strconv.FormatUint(uint64(u), 16)
	if len(s) < pad {
		s = strings.Repeat("0", pad-len(s)) + s
	}
	return s
}

func isXDigit(c expr.Char) bool {
	r := c.Rune()
	return r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F'
}

// escapeString is escape_string(io, s, esc) with keep=(), ascii=false,
// fullhex=false.
func escapeString(b *strings.Builder, s string, esc string) {
	cs := juliaChars(s)
	for i, c := range cs {
		var next *expr.Char
		if i+1 < len(cs) {
			next = &cs[i+1]
		}
		r := c.Rune()
		u := uint32(c)
		switch {
		case r >= 0 && r < 0x80 && strings.ContainsRune(esc, r):
			b.WriteByte('\\')
			b.WriteRune(r)
		case u < 0x80000000: // isascii
			switch {
			case r == 0:
				if next != nil && next.Rune() >= '0' && next.Rune() <= '7' {
					b.WriteString(`\x00`)
				} else {
					b.WriteString(`\0`)
				}
			case r == 0x1b:
				b.WriteString(`\e`)
			case r == '\\':
				b.WriteString(`\\`)
			case r >= 0x07 && r <= '\r':
				b.WriteByte('\\')
				b.WriteByte("abtnvfr"[r-7])
			case isPrintChar(c):
				b.WriteRune(r)
			default:
				b.WriteString(`\x` + hexPad(uint32(r), 2))
			}
		case !c.IsOverlong() && !c.IsMalformed():
			cp, _ := c.CodePoint()
			full := next != nil && isXDigit(*next)
			// (escape_string's `c <= '\x7f'` branch is for other AbstractChar
			// types: a non-ASCII Char that is neither overlong nor malformed
			// has a code point above 0x7f.)
			switch {
			case isPrintChar(c):
				b.Write(c.Bytes())
			case cp <= 0xffff:
				pad := 2
				if full {
					pad = 4
				}
				b.WriteString(`\u` + hexPad(cp, pad))
			default:
				pad := 4
				if full {
					pad = 8
				}
				b.WriteString(`\U` + hexPad(cp, pad))
			}
		default: // malformed or overlong
			for _, x := range c.Bytes() {
				b.WriteString(`\x` + hexPad(uint32(x), 2))
			}
		}
	}
}

// reprString is repr(::String): print_quoted, escaping " and $.
func reprString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	escapeString(&b, s, `"$`)
	b.WriteByte('"')
	return b.String()
}

// reprChar is repr(::Char).
func reprChar(c expr.Char) string {
	r := c.Rune()
	if uint32(c) <= uint32('\\')<<24 {
		esc := map[rune]byte{0: '0', 7: 'a', 8: 'b', '\t': 't', '\n': 'n', '\v': 'v', '\f': 'f', '\r': 'r', 0x1b: 'e', '\'': '\'', '\\': '\\'}
		if e, ok := esc[r]; ok {
			return "'\\" + string(e) + "'"
		}
	}
	if c.IsOverlong() || c.IsMalformed() {
		var b strings.Builder
		b.WriteByte('\'')
		for _, x := range c.Bytes() {
			b.WriteString(`\x` + hexPad(uint32(x), 2))
		}
		b.WriteByte('\'')
		return b.String()
	}
	if isPrintChar(c) {
		return "'" + string(c.Bytes()) + "'"
	}
	cp, _ := c.CodePoint()
	prefix := `\U`
	switch {
	case cp <= 0x7f:
		prefix = `\x`
	case cp <= 0xffff:
		prefix = `\u`
	}
	d := 8 - bitsLeadingZeros32(cp)>>2
	return "'" + prefix + hexPad(cp, max(2, d)) + "'"
}

func bitsLeadingZeros32(u uint32) int {
	n := 0
	for i := 31; i >= 0 && u&(1<<uint(i)) == 0; i-- {
		n++
	}
	return n
}
