// Package expr holds the Go representation of Julia's surface syntax tree,
// Base.Expr, and the values that appear inside it.
//
// A parsed Julia program is a tree of [*Expr] nodes whose arguments are any of
// the following Go values, chosen so that each Julia type maps to exactly one
// Go type and back:
//
//	Julia                    Go
//	Expr                     *Expr
//	Symbol                   Symbol
//	LineNumberNode           LineNumberNode
//	QuoteNode                QuoteNode
//	GlobalRef                GlobalRef
//	String                   string
//	Char                     Char (Julia's own UTF-8 bit layout, malformed chars kept)
//	Bool                     bool
//	nothing                  nil
//	Int64 (Int)              int64
//	Int8 … Int32             int8, int16, int32
//	UInt8 … UInt64           uint8, uint16, uint32, uint64
//	Float64, Float32         float64, float32
//	Meta.ParseError          *ParseError
//
// Integer literals too large for Int64/UInt64 never appear as values: the
// parser represents them, as Julia does, by a macro call such as
// (macrocall (GlobalRef Core @int128_str) nothing "…").
package expr

// Symbol is a Julia Symbol.
type Symbol string

// Expr is a Julia Expr: a head symbol and a list of arguments.
type Expr struct {
	Head Symbol
	Args []any
}

// New returns an Expr with the given head and arguments.
func New(head Symbol, args ...any) *Expr {
	if args == nil {
		args = []any{}
	}
	return &Expr{Head: head, Args: args}
}

// Is reports whether v is an *Expr with the given head.
func Is(v any, head Symbol) bool {
	e, ok := v.(*Expr)
	return ok && e.Head == head
}

// LineNumberNode records a source line. File is a Symbol or nil (nothing).
type LineNumberNode struct {
	Line int
	File any
}

// QuoteNode is Julia's inert quotation of a value.
type QuoteNode struct {
	Value any
}

// GlobalRef names a global binding in a module.
type GlobalRef struct {
	Mod  Symbol
	Name Symbol
}

// ParseError is the payload of Expr(:error) and Expr(:incomplete) nodes
// produced for code that does not parse; Msg is the formatted diagnostic.
type ParseError struct {
	Msg string
}

// Char is a Julia Char: up to four UTF-8 bytes stored left-aligned in a
// 32-bit word, exactly as Julia stores them, so that malformed characters
// survive unchanged.
type Char uint32

// EOFChar is Julia's typemax(Char), used by the lexer as an end marker.
const EOFChar Char = 0xffffffff

// CharFromRune returns the Char encoding the code point r. Surrogates and
// out-of-range values are encoded as Julia would encode the code point,
// which makes them invalid characters.
func CharFromRune(r rune) Char {
	u := uint32(r)
	switch {
	case u < 0x80:
		return Char(u << 24)
	case u < 0x800:
		return Char((0xc0|u>>6)<<24 | (0x80|u&0x3f)<<16)
	case u < 0x10000:
		return Char((0xe0|u>>12)<<24 | (0x80|(u>>6)&0x3f)<<16 | (0x80|u&0x3f)<<8)
	default:
		return Char((0xf0|(u>>18)&0x07)<<24 | (0x80|(u>>12)&0x3f)<<16 | (0x80|(u>>6)&0x3f)<<8 | (0x80 | u&0x3f))
	}
}

// DecodeChar reads one Char from b the way Julia's read(io, Char) does: the
// lead byte announces a length and continuation bytes are taken greedily up
// to it. It returns the character and the number of bytes consumed (zero
// only when b is empty, with EOFChar).
func DecodeChar(b []byte) (Char, int) {
	if len(b) == 0 {
		return EOFChar, 0
	}
	b0 := b[0]
	c := uint32(b0) << 24
	n := 1
	lead := leadingOnes8(b0)
	if lead >= 2 && lead <= 4 {
		for s := 16; n < lead && n < len(b) && b[n]&0xc0 == 0x80; s -= 8 {
			c |= uint32(b[n]) << uint(s)
			n++
		}
	}
	return Char(c), n
}

func leadingOnes8(b byte) int {
	n := 0
	for b&0x80 != 0 {
		n++
		b <<= 1
	}
	return n
}

// Bytes returns the UTF-8 bytes of c (one to four).
func (c Char) Bytes() []byte {
	u := uint32(c)
	out := []byte{byte(u >> 24)}
	for s := 16; s >= 0 && byte(u>>uint(s)) != 0; s -= 8 {
		out = append(out, byte(u>>uint(s)))
	}
	return out
}

// IsValid reports whether c is a valid Unicode scalar value encoded in
// minimal UTF-8, as Julia's isvalid(::Char).
func (c Char) IsValid() bool {
	cp, ok := c.CodePoint()
	return ok && cp <= 0x10ffff && !(cp >= 0xd800 && cp <= 0xdfff)
}

// Rune returns the code point of a valid Char and -1 otherwise.
func (c Char) Rune() rune {
	if !c.IsValid() {
		return -1
	}
	cp, _ := c.CodePoint()
	return rune(cp)
}

// AppendUTF8 appends the bytes of c to b.
func (c Char) AppendUTF8(b []byte) []byte {
	return append(b, c.Bytes()...)
}

// IsMalformed reports Julia's ismalformed(c): the bytes are not a
// structurally well-formed UTF-8 sequence (overlong forms and surrogates are
// well-formed).
func (c Char) IsMalformed() bool {
	u := uint32(c)
	l1 := uint32(leadingOnes32(u)) << 3
	t0 := uint32(trailingZeros32(u)) & 56
	return l1 == 8 || l1+t0 > 32 || ((u&0x00c0c0c0)^0x00808080)>>t0 != 0
}

// IsOverlong reports Julia's isoverlong(c).
func (c Char) IsOverlong() bool {
	u := uint32(c)
	return u>>24 == 0xc0 || u>>24 == 0xc1 || u>>21 == 0x0704 || u>>20 == 0x0f08
}

// CodePoint returns the code point Julia's UInt32(c) computes for a
// well-formed, non-overlong Char (surrogates included), and ok=false where
// Julia would throw.
func (c Char) CodePoint() (uint32, bool) {
	u := uint32(c)
	if u < 0x80000000 {
		return u >> 24, true
	}
	if c.IsMalformed() || c.IsOverlong() {
		return 0, false
	}
	l1 := uint32(leadingOnes32(u))
	t0 := uint32(trailingZeros32(u)) & 56
	u &= 0xffffffff >> l1
	u >>= t0
	return (u & 0x7f) | (u&0x7f00)>>2 | (u&0x7f0000)>>4 | (u&0x7f000000)>>6, true
}

func leadingOnes32(u uint32) int {
	n := 0
	for u&0x80000000 != 0 {
		n++
		u <<= 1
	}
	return n
}

func trailingZeros32(u uint32) int {
	if u == 0 {
		return 32
	}
	n := 0
	for u&1 == 0 {
		n++
		u >>= 1
	}
	return n
}
