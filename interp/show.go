package interp

import (
	"fmt"
	"math"
	"math/bits"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// showValue is what julia's repr prints for the values this version makes,
// so that a result can be compared with the reference's: the default show
// of a struct (TypeName(field, …)), of a tuple, and the literals. Base's
// show is Julia code that does not run here yet; this is its output for
// these values, not a port of it.
func showValue(v any) string {
	var b strings.Builder
	writeValue(&b, v)
	return b.String()
}

// showFloat64 is julia's shortest round-trip form of a Float64: the digits
// Go's 'g' with precision -1 chooses, at the same exponent thresholds (below
// -4 or from 6 on), spelled as julia spells them: 1.0e6, 2.0, 1.0e-5.
func showFloat64(x float64) string { return showFloat(x, 64) }

// showFloat is showFloat64 for a float of the given width: a Float32 has
// the digits of its own shortest form, an f exponent always (1.0f0, 1.0f6)
// and the specials NaN32, Inf32, -Inf32.
func showFloat(x float64, bits int) string {
	suffix, e := "", "e"
	if bits == 32 {
		suffix, e = "32", "f"
	}
	switch {
	case math.IsNaN(x):
		return "NaN" + suffix
	case math.IsInf(x, 1):
		return "Inf" + suffix
	case math.IsInf(x, -1):
		return "-Inf" + suffix
	}
	s := strconv.FormatFloat(x, 'g', -1, bits)
	mant, exp, hasExp := strings.Cut(s, "e")
	if !strings.Contains(mant, ".") {
		mant += ".0"
	}
	if !hasExp {
		if bits == 32 {
			return mant + "f0"
		}
		return mant
	}
	sign := ""
	if exp[0] == '-' {
		sign = "-"
	}
	return mant + e + sign + strings.TrimLeft(exp[1:], "0")
}

// showChar is show(io, c::AbstractChar) (base/char.jl:293-327): the
// backslash escapes, a malformed or overlong character byte by byte
// (show_invalid, :270-280), a printable one as itself, any other as \x,
// \u or \U and its code point in hex.
func showChar(c expr.Char) string {
	u := uint32(c)
	if u <= 0x5c000000 { // c <= '\\'
		if esc, ok := map[uint32]byte{0: '0', 0x07000000: 'a', 0x08000000: 'b', 0x09000000: 't',
			0x0a000000: 'n', 0x0b000000: 'v', 0x0c000000: 'f', 0x0d000000: 'r', 0x1b000000: 'e',
			0x27000000: '\'', 0x5c000000: '\\'}[u]; ok {
			return "'\\" + string(esc) + "'"
		}
	}
	cp, ok := charCodepoint(u)
	if !ok {
		var b strings.Builder
		b.WriteByte('\'')
		for {
			fmt.Fprintf(&b, "\\x%02x", u>>24)
			if u <<= 8; u == 0 {
				break
			}
		}
		b.WriteByte('\'')
		return b.String()
	}
	// isprint (base/strings/unicode.jl:604): categories Lu to Zs
	if cat := unitab.Category(cp); cat >= 1 && cat <= 23 {
		return "'" + string(utf8.AppendRune(nil, rune(cp))) + "'"
	}
	letter, d := byte('x'), max(2, 8-bits.LeadingZeros32(cp)>>2)
	if cp > 0xffff {
		letter = 'U'
	} else if cp > 0x7f {
		letter = 'u'
	}
	return fmt.Sprintf("'\\%c%0*x'", letter, d, cp)
}

// charCodepoint is codepoint(c::Char) (base/char.jl:63-75), false where
// that throws: a malformed or overlong encoding.
func charCodepoint(u uint32) (uint32, bool) {
	if u < 0x80000000 {
		return u >> 24, true
	}
	l1 := bits.LeadingZeros32(^u)
	t0 := bits.TrailingZeros32(u) & 56
	overlong := u>>24 == 0xc0 || u>>24 == 0xc1 || u>>21 == 0x0704 || u>>20 == 0x0f08
	if l1 == 1 || 8*l1+t0 > 32 || ((u&0x00c0c0c0)^0x00808080)>>t0 != 0 || overlong {
		return 0, false
	}
	u &= 0xffffffff >> l1
	u >>= t0
	return u&0x7f | (u&0x7f00)>>2 | (u&0x7f0000)>>4 | (u&0x7f000000)>>6, true
}

// modulePrefix is how julia qualifies a type defined in a module of a
// session, which Main does not see: Main.L. for module L (the modules a VM
// creates are under Main). Types of Core, Base and Main are shown bare.
// Only the outermost name is qualified; a session type inside the
// parameters of another type is shown bare.
func modulePrefix(d *core.DataType) string {
	switch d.Name.Module {
	case "Core", "Base", "Main", "":
		return ""
	}
	return "Main." + d.Name.Module + "."
}

// writeTypeVar is show(io, ::TypeVar) (base/show.jl:2798-2830) for a type
// variable shown on its own, outside a where clause: the lower bound when it
// is not Union{}, the upper bound when it is not Any.
func writeTypeVar(b *strings.Builder, tv *core.TypeVar) {
	bound := func(v any) {
		_, isUA := v.(*core.UnionAll)
		paren := isUA && Repr(v) != showWrapperName(v)
		if paren {
			b.WriteByte('(')
		}
		writeValue(b, v)
		if paren {
			b.WriteByte(')')
		}
	}
	if tv.LB != any(core.Bottom) {
		if tv.UB == any(core.AnyType) {
			b.WriteString(tv.Name + ">:")
			bound(tv.LB)
		} else {
			bound(tv.LB)
			b.WriteString("<:" + tv.Name)
		}
	} else {
		b.WriteString(tv.Name)
	}
	if tv.UB != any(core.AnyType) {
		b.WriteString("<:")
		bound(tv.UB)
	}
}

// showWrapperName is the name a type wrapper is shown by, or "" when v is
// not a wrapper (print_without_params).
func showWrapperName(v any) string {
	if d, ok := core.UnwrapUnionAll(v).(*core.DataType); ok && d.Name.Wrapper == v {
		return d.Name.Name
	}
	return ""
}

// Repr is showValue, exported for the notebook and the tests.
func Repr(v any) string { return showValue(v) }

func writeValue(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("nothing")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int8, int16, int32:
		fmt.Fprintf(b, "%d", x)
	case uint8:
		fmt.Fprintf(b, "0x%02x", x)
	case uint16:
		fmt.Fprintf(b, "0x%04x", x)
	case uint32:
		fmt.Fprintf(b, "0x%08x", x)
	case uint64:
		fmt.Fprintf(b, "0x%016x", x)
	case float64:
		b.WriteString(showFloat64(x))
	case float32:
		b.WriteString(showFloat(float64(x), 32))
	case expr.Char:
		b.WriteString(showChar(x))
	case string:
		b.WriteString(strconv.Quote(x))
	case expr.Symbol:
		b.WriteString(":" + string(x))
	case *Tuple:
		b.WriteByte('(')
		for i, e := range x.Elems {
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, e)
		}
		if len(x.Elems) == 1 {
			b.WriteByte(',')
		}
		b.WriteByte(')')
	case *Memory:
		writeIn(b, x, nil)
	case *MemoryRef:
		// julia shows the element's address, which has no value here: the
		// element's index takes its place
		writeType(b, x.Type)
		fmt.Fprintf(b, "(%d, ", x.Off+1)
		writeIn(b, x.Mem, nil)
		b.WriteByte(')')
	case *Struct:
		if _, _, _, ok := arrayOf(x); ok {
			writeIn(b, x, nil)
			return
		}
		// the instance's whole type, parameters included: Q{Int64}(1)
		writeType(b, x.Type)
		b.WriteByte('(')
		for i, f := range x.Fields {
			if i > 0 {
				b.WriteString(", ")
			}
			if f == undef {
				b.WriteString("#undef")
				continue
			}
			writeValue(b, f)
		}
		b.WriteByte(')')
	case *Function:
		b.WriteString(string(x.Name))
	case *Builtin:
		b.WriteString(string(x.Name))
	case *Intrinsic:
		b.WriteString("Core.Intrinsics." + string(x.Name))
	case *Module:
		// fullname: every enclosing module, outermost first
		if x.Parent != nil {
			writeValue(b, x.Parent)
			b.WriteByte('.')
		}
		b.WriteString(string(x.Name))
	case SVec:
		b.WriteString("svec(")
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeValue(b, e)
		}
		b.WriteByte(')')
	case *core.TypeVar:
		writeTypeVar(b, x)
	case *core.UnionAll, *core.DataType, *core.Union, *core.Vararg:
		writeType(b, x)
	default:
		if core.IsType(v) || core.TypeOf(v) != nil {
			b.WriteString(core.Show(v))
			return
		}
		fmt.Fprintf(b, "%v", v)
	}
}
