package expr

import (
	"math"
	"testing"
	"unicode/utf8"
)

func TestSerializeEveryType(t *testing.T) {
	e := New("call", Symbol("+"), int64(-1), int8(-2), int16(3), int32(4), uint8(5), uint16(6),
		uint32(7), uint64(8), 1.5, float32(0.25), Char('a'<<24), true, false, nil, "q\"\\\x01é\xff",
		LineNumberNode{Line: 3, File: Symbol("f.jl")}, LineNumberNode{Line: 4},
		QuoteNode{Value: Symbol("x")}, GlobalRef{Mod: "Core", Name: "@doc"},
		&ParseError{Msg: "bad"}, Symbol(""), Symbol("a1!"), Symbol("1a"), Symbol("a b"), struct{}{})
	want := `(:call :"+" -1 {Int8 -2} {Int16 3} {Int32 4} {UInt8 5} {UInt16 6} {UInt32 7} {UInt64 8} ` +
		`{Float64 0x3ff8000000000000} {Float32 0x3e800000} {Char 0x61000000} true false nothing ` +
		`"q\"\\\x01é\xff" {L 3 :"f.jl"} {L 4 nothing} {Q :x} {G :Core :@doc} {ParseError "bad"} ` +
		`:"" :a1! :"1a" :"a b" {? "struct {}" "{}"})`
	if got := Serialize(e); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := Quote("\x7f\"x"); got != `"\x7f\"x"` {
		t.Fatalf("Quote = %s", got)
	}
	if len(New("block").Args) != 0 || !Is(New("x"), "x") || Is(1, "x") {
		t.Fatal("New/Is")
	}
}

func TestCharEncoding(t *testing.T) {
	for _, r := range []rune{0, 'a', 0x7f, 0x80, 0x7ff, 0x800, 0xffff, 0x10000, 0x10ffff} {
		c := CharFromRune(r)
		b := c.Bytes()
		if string(b) != string(r) {
			t.Fatalf("U+%04X: bytes %x", r, b)
		}
		if !c.IsValid() || c.Rune() != r {
			t.Fatalf("U+%04X: valid=%v rune=%x", r, c.IsValid(), c.Rune())
		}
		d, n := DecodeChar(append(b, 'x'))
		if d != c || n != len(b) {
			t.Fatalf("U+%04X: DecodeChar = %x, %d", r, uint32(d), n)
		}
		if string(c.AppendUTF8([]byte("<"))) != "<"+string(r) {
			t.Fatal("AppendUTF8")
		}
	}
	// Surrogates are well-formed, not overlong, and invalid.
	s := CharFromRune(0xd800)
	if s.IsValid() || s.IsMalformed() || s.IsOverlong() || s.Rune() != -1 {
		t.Fatal("surrogate")
	}
	if cp, ok := s.CodePoint(); !ok || cp != 0xd800 {
		t.Fatalf("surrogate code point %x %v", cp, ok)
	}
	if c, n := DecodeChar(nil); c != EOFChar || n != 0 {
		t.Fatal("empty input")
	}
}

func TestDecodeCharGrouping(t *testing.T) {
	for _, tc := range []struct {
		in        string
		n         int
		malformed bool
		overlong  bool
	}{
		{"\xff", 1, true, false},      // five leading ones
		{"\x80", 1, true, false},      // stray continuation byte
		{"\xe2\x88a", 2, true, false}, // truncated three-byte sequence
		{"\xc0\x80", 2, false, true},  // overlong NUL
		{"\xe0\x80\x80", 3, false, true},
		{"\xf0\x80\x80\x80", 4, false, true},
		{"\xf5\x80\x80\x80", 4, false, false}, // beyond U+10FFFF
	} {
		c, n := DecodeChar([]byte(tc.in))
		if n != tc.n || c.IsMalformed() != tc.malformed || c.IsOverlong() != tc.overlong || c.IsValid() {
			t.Errorf("%q: n=%d malformed=%v overlong=%v valid=%v", tc.in, n, c.IsMalformed(), c.IsOverlong(), c.IsValid())
		}
		if _, ok := c.CodePoint(); ok != !(tc.malformed || tc.overlong) {
			t.Errorf("%q: CodePoint ok=%v", tc.in, ok)
		}
	}
	if Char(0).IsMalformed() || trailingZeros32(0) != 32 {
		t.Fatal("NUL")
	}
}

// FuzzDecodeChar checks the Julia grouping against Go's own decoder: a
// valid Julia character is exactly a valid UTF-8 sequence of the same
// length.
func FuzzDecodeChar(f *testing.F) {
	f.Add([]byte("é"))
	f.Add([]byte("\xed\xa0\x80"))
	f.Add([]byte("\xf4\x8f\xbf\xbf"))
	f.Fuzz(func(t *testing.T, b []byte) {
		c, n := DecodeChar(b)
		if len(b) == 0 {
			return
		}
		r, size := utf8.DecodeRune(b)
		goValid := !(r == utf8.RuneError && size <= 1)
		if c.IsValid() != (goValid && size == n) {
			t.Fatalf("%x: julia valid=%v n=%d, go rune %x size %d", b, c.IsValid(), n, r, size)
		}
		if c.IsValid() && (c.Rune() != r || string(c.Bytes()) != string(b[:n])) {
			t.Fatalf("%x: rune %x vs %x", b, c.Rune(), r)
		}
		_ = Serialize(string(b))
	})
}

func TestFloatBits(t *testing.T) {
	if Serialize(math.Inf(-1)) != "{Float64 0xfff0000000000000}" {
		t.Fatal("-Inf")
	}
}
