package expr

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	mathFloat64bits = math.Float64bits
	mathFloat32bits = math.Float32bits
)

// Serialize prints v in the canonical, type-tagged text form shared with the
// reference oracle (internal/oracle/oracle.jl). Two trees serialize to the
// same string if and only if they are the same Julia value tree, which is how
// parser output is compared with julia's.
//
//	(head arg …)        Expr
//	:name  :"a b"       Symbol (quoted unless it matches [A-Za-z_@][A-Za-z0-9_!@]*)
//	"…"                 String; \" \\ and \xHH for control bytes and invalid UTF-8
//	42                  Int64
//	{UInt8 7}           other integer types, by Julia type name
//	{Float64 0x…}       floats by their IEEE bits
//	{Char 0x…}          Char by its 32-bit Julia representation
//	{L line file}       LineNumberNode
//	{Q value}           QuoteNode
//	{G :Mod :name}      GlobalRef
//	{ParseError "…"}    Meta.ParseError
func Serialize(v any) string {
	var b strings.Builder
	ser(&b, v)
	return b.String()
}

// Quote returns s quoted as Serialize quotes strings.
func Quote(s string) string {
	var b strings.Builder
	serBytes(&b, s)
	return b.String()
}

func ser(b *strings.Builder, v any) {
	switch x := v.(type) {
	case *Expr:
		b.WriteByte('(')
		serSym(b, x.Head)
		for _, a := range x.Args {
			b.WriteByte(' ')
			ser(b, a)
		}
		b.WriteByte(')')
	case Symbol:
		serSym(b, x)
	case string:
		serBytes(b, x)
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case nil:
		b.WriteString("nothing")
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int8:
		tagged(b, "Int8", strconv.FormatInt(int64(x), 10))
	case int16:
		tagged(b, "Int16", strconv.FormatInt(int64(x), 10))
	case int32:
		tagged(b, "Int32", strconv.FormatInt(int64(x), 10))
	case uint8:
		tagged(b, "UInt8", strconv.FormatUint(uint64(x), 10))
	case uint16:
		tagged(b, "UInt16", strconv.FormatUint(uint64(x), 10))
	case uint32:
		tagged(b, "UInt32", strconv.FormatUint(uint64(x), 10))
	case uint64:
		tagged(b, "UInt64", strconv.FormatUint(x, 10))
	case float64:
		tagged(b, "Float64", fmt.Sprintf("0x%016x", mathFloat64bits(x)))
	case float32:
		tagged(b, "Float32", fmt.Sprintf("0x%08x", mathFloat32bits(x)))
	case Char:
		tagged(b, "Char", fmt.Sprintf("0x%08x", uint32(x)))
	case LineNumberNode:
		b.WriteString("{L ")
		b.WriteString(strconv.Itoa(x.Line))
		b.WriteByte(' ')
		ser(b, x.File)
		b.WriteByte('}')
	case QuoteNode:
		b.WriteString("{Q ")
		ser(b, x.Value)
		b.WriteByte('}')
	case GlobalRef:
		b.WriteString("{G ")
		serSym(b, x.Mod)
		b.WriteByte(' ')
		serSym(b, x.Name)
		b.WriteByte('}')
	case *ParseError:
		b.WriteString("{ParseError ")
		serBytes(b, x.Msg)
		b.WriteByte('}')
	default:
		b.WriteString("{? ")
		serBytes(b, fmt.Sprintf("%T", v))
		b.WriteByte(' ')
		serBytes(b, fmt.Sprintf("%v", v))
		b.WriteByte('}')
	}
}

func tagged(b *strings.Builder, tag, val string) {
	b.WriteByte('{')
	b.WriteString(tag)
	b.WriteByte(' ')
	b.WriteString(val)
	b.WriteByte('}')
}

func isSimpleSymbol(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c == '_', c == '@':
		case i > 0 && (c >= '0' && c <= '9' || c == '!'):
		default:
			return false
		}
	}
	return true
}

func serSym(b *strings.Builder, s Symbol) {
	b.WriteByte(':')
	if isSimpleSymbol(string(s)) {
		b.WriteString(string(s))
		return
	}
	serBytes(b, string(s))
}

const hexDigits = "0123456789abcdef"

func hexByte(b *strings.Builder, c byte) {
	b.WriteString(`\x`)
	b.WriteByte(hexDigits[c>>4])
	b.WriteByte(hexDigits[c&15])
}

// serBytes quotes s, iterating characters the way Julia iterates a String.
func serBytes(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); {
		c, n := DecodeChar([]byte(s[i:min(len(s), i+4)]))
		switch {
		case !c.IsValid():
			for j := 0; j < n; j++ {
				hexByte(b, s[i+j])
			}
		case n == 1 && s[i] == '"':
			b.WriteString(`\"`)
		case n == 1 && s[i] == '\\':
			b.WriteString(`\\`)
		case n == 1 && (s[i] < ' ' || s[i] == 0x7f):
			hexByte(b, s[i])
		default:
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	b.WriteByte('"')
}
