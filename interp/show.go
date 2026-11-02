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

// showSym is show_sym (show.jl:1814-1823): an identifier as it is, any
// other name as var"…". julia also prints an operator name bare
// (is_valid_identifier's jl_is_operator, flisp's tables, not ported here),
// which this prints as var"…".
func showSym(s expr.Symbol) string {
	if isIdentifier(string(s)) {
		return string(s)
	}
	return "var" + showValue(string(s))
}

// isIdentifier is Base.isidentifier (show.jl:1565-1572): not true or
// false, an identifier start character, then identifier characters.
func isIdentifier(s string) bool {
	if s == "" || s == "true" || s == "false" {
		return false
	}
	for i, c := range s {
		if c == utf8.RuneError {
			return false
		}
		if i == 0 && !unitab.IsIDStart(uint32(c)) || i > 0 && !unitab.IsIDChar(uint32(c)) {
			return false
		}
	}
	return true
}

// writeNamedTuple is show(io, ::NamedTuple) (namedtuple.jl:198-229): the
// whole type when a field's value is not of the field's type, else
// (a = 1, b = 2), NamedTuple() when empty. The construction checked each
// value against its field type, so the value's type is the field's exactly
// when that is concrete.
func writeNamedTuple(b *strings.Builder, x *Struct) {
	ft, _ := x.Type.FieldTypes()
	for i := range x.Fields {
		if i >= len(ft) || !core.IsConcrete(ft[i]) {
			writeType(b, x.Type)
			b.WriteByte('(')
			writeValue(b, &Tuple{Elems: x.Fields})
			b.WriteByte(')')
			return
		}
	}
	if len(x.Fields) == 0 {
		b.WriteString("NamedTuple()")
		return
	}
	names := x.Type.FieldNames()
	b.WriteByte('(')
	for i, f := range x.Fields {
		b.WriteString(showSym(names[i]) + " = ")
		writeValue(b, f)
		if len(x.Fields) == 1 {
			b.WriteByte(',')
		} else if i < len(x.Fields)-1 {
			b.WriteString(", ")
		}
	}
	b.WriteByte(')')
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
	cp, _ := charCodepoint(u)
	if charOverlong(u) || charMalformed(u) {
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

// quoteString is show(io, s::String), print_quoted (base/strings/io.jl:
// 470-474): s in quotes, through escape_string (:438-465) with " and $
// escaped, over the characters iterate makes of its bytes
// (string.jl:430-459).
func quoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); {
		u, next := stringChar(s, i)
		i = next
		peek := func(pred func(cp uint32) bool) bool {
			if i >= len(s) {
				return false
			}
			p, _ := stringChar(s, i)
			cp, ok := charCodepoint(p)
			return ok && pred(cp)
		}
		cp, valid := charCodepoint(u)
		switch {
		case u == '"'<<24 || u == '$'<<24:
			b.WriteByte('\\')
			b.WriteByte(byte(u >> 24))
		case valid && cp < 0x80:
			switch c := byte(cp); {
			case c == 0:
				// \0 unless an octal digit follows (escape_nul)
				if peek(func(cp uint32) bool { return '0' <= cp && cp <= '7' }) {
					b.WriteString("\\x00")
				} else {
					b.WriteString("\\0")
				}
			case c == 0x1b:
				b.WriteString("\\e")
			case c == '\\':
				b.WriteString("\\\\")
			case c >= '\a' && c <= '\r':
				b.WriteByte('\\')
				b.WriteByte("abtnvfr"[c-'\a'])
			case c >= 0x20 && c < 0x7f:
				b.WriteByte(c)
			default:
				fmt.Fprintf(&b, "\\x%02x", c)
			}
		case valid && !charOverlong(u):
			if cat := unitab.Category(cp); cat >= 1 && cat <= 23 {
				b.WriteRune(rune(cp))
				break
			}
			// \u and \U pad in full before a hex digit (need_full_hex)
			full := peek(func(cp uint32) bool {
				return cp >= '0' && cp <= '9' || cp >= 'a' && cp <= 'f' || cp >= 'A' && cp <= 'F'
			})
			switch {
			case cp <= 0xffff && full:
				fmt.Fprintf(&b, "\\u%04x", cp)
			case cp <= 0xffff:
				fmt.Fprintf(&b, "\\u%02x", cp)
			case full:
				fmt.Fprintf(&b, "\\U%08x", cp)
			default:
				fmt.Fprintf(&b, "\\U%04x", cp)
			}
		default: // malformed or overlong: its bytes
			for {
				fmt.Fprintf(&b, "\\x%02x", u>>24)
				if u <<= 8; u == 0 {
					break
				}
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// stringChar is iterate(s::String, i) (string.jl:430-459) at 0-based i:
// the Char of the bytes from i, as julia packs them (a malformed sequence
// stops at its first non-continuation byte), and the next index.
func stringChar(s string, i int) (uint32, int) {
	u := uint32(s[i]) << 24
	if s[i] < 0x80 || s[i] > 0xf7 || u < 0xc0000000 {
		return u, i + 1
	}
	for k, min := 1, uint32(0); k <= 3; k++ {
		// the k-th continuation byte, when the lead byte asks for it
		switch k {
		case 2:
			min = 0xe0000000
		case 3:
			min = 0xf0000000
		}
		if i+k >= len(s) || u < min || s[i+k]&0xc0 != 0x80 {
			return u, i + k
		}
		u |= uint32(s[i+k]) << (24 - 8*k)
	}
	return u, i + 4
}

// charMalformed and charOverlong are ismalformed and isoverlong
// (base/char.jl:98-111), which decide that show prints a Char's bytes.
func charMalformed(u uint32) bool {
	l1 := bits.LeadingZeros32(^u) << 3
	t0 := bits.TrailingZeros32(u) & 56
	return l1 == 8 || l1+t0 > 32 || ((u&0x00c0c0c0)^0x00808080)>>t0 != 0
}

func charOverlong(u uint32) bool {
	return u>>24 == 0xc0 || u>>24 == 0xc1 || u>>21 == 0x0704 || u>>20 == 0x0f08
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
	case Int128:
		b.WriteString(u128{x.Hi, x.Lo}.decimal(128, true))
	case UInt128:
		fmt.Fprintf(b, "0x%016x%016x", x.Hi, x.Lo)
	case float64:
		b.WriteString(showFloat64(x))
	case float32:
		b.WriteString(showFloat(float64(x), 32))
	case Float16:
		b.WriteString(showHalf(uint16(x), true))
	case BFloat16:
		// Base has no show method for it: the default show of a primitive
		// value, its bits (julia 1.12.6: Core.BFloat16(0x3fc0))
		fmt.Fprintf(b, "Core.BFloat16(0x%04x)", uint16(x))
	case expr.Char:
		b.WriteString(showChar(x))
	case string:
		b.WriteString(quoteString(x))
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
	case *expr.Expr:
		// julia shows the quoted source (show_unquoted, not ported): this is
		// the constructor call, which reads back as the same Expr
		b.WriteString("Expr(:" + string(x.Head))
		for _, a := range x.Args {
			b.WriteString(", ")
			writeValue(b, a)
		}
		b.WriteByte(')')
	case expr.QuoteNode:
		// likewise the constructor call (julia: :(:x))
		b.WriteString("QuoteNode(")
		writeValue(b, x.Value)
		b.WriteByte(')')
	case expr.GlobalRef:
		// :(M.x), the module qualified as a type's is (modulePrefix)
		mod := string(x.Mod)
		switch mod {
		case "Core", "Base", "Main":
		default:
			mod = "Main." + mod
		}
		// a name that is not an identifier is quoted: :(Main.:(var"a b"))
		name := showSym(x.Name)
		if name != string(x.Name) {
			name = ":(" + name + ")"
		}
		b.WriteString(":(" + mod + "." + name + ")")
	case *Memory:
		writeIn(b, x, nil)
	case *Pointer:
		// julia shows the address: a null pointer's is 0; one into a value
		// here has none, and shows what it points into and the offset
		writeType(b, x.Type)
		b.WriteByte('(')
		switch tg := x.Target.(type) {
		case nil:
			fmt.Fprintf(b, "0x%016x", uint64(x.Off))
		case string:
			fmt.Fprintf(b, "%s + %d", quoteString(tg), x.Off)
		case *ptrSlot:
			fmt.Fprintf(b, "cglobal(:%s) + %d", tg.name, x.Off)
		case *outStream:
			fmt.Fprintf(b, "%s + %d", tg.name, x.Off)
		case *Memory:
			fmt.Fprintf(b, "pointer(%s) + %d", showValue(tg.Type), x.Off)
		case layoutOf:
			fmt.Fprintf(b, "%s.layout + %d", showValue(tg.t), x.Off)
		}
		b.WriteByte(')')
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
		if x.Type.Name == core.NamedTupleTypeName {
			writeNamedTuple(b, x)
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
