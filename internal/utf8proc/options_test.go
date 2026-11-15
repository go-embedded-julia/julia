package utf8proc

import (
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// Every combination of the options over crafted inputs, invalid UTF-8
// included, against julia's utf8proc_map: the bytes, or the error message.
func TestOptionsAgainstJulia(t *testing.T) {
	n, skipped := 0, 0
	for _, l := range lines(t, "options") {
		head, res, ok := strings.Cut(l, " | ")
		if !ok {
			t.Fatalf("a line with no result: %q", l)
		}
		f := strings.Fields(head)
		flags, err := strconv.Atoi(f[0])
		if err != nil {
			t.Fatal(err)
		}
		in := []byte{}
		if len(f) > 1 {
			if in, err = hex.DecodeString(f[1]); err != nil {
				t.Fatal(err)
			}
		}
		out, code := Map(in, Option(flags))
		if flags&int(CharBound) != 0 && flags&int(Compose|DecomposeOpt) != 0 {
			// the grapheme-break marker is read by C as a code point of the
			// table before its start (undefined): only that it is handled
			skipped++
			continue
		}
		var got string
		if code < 0 {
			got = "ERR " + Errmsg(code)
		} else {
			got = hex.EncodeToString(out)
		}
		if got != res {
			t.Fatalf("Map(%x, %d) = %s, julia %s", in, flags, got, res)
		}
		n++
	}
	if n < 20000 || skipped == 0 {
		t.Errorf("%d cases compared, %d skipped", n, skipped)
	}
}

func TestErrmsg(t *testing.T) {
	for code, want := range map[int]string{
		ErrNoMem:       "Memory for processing UTF-8 data could not be allocated.",
		ErrOverflow:    "UTF-8 string is too long to be processed.",
		ErrInvalidUTF8: "Invalid UTF-8 string",
		ErrNotAssigned: "Unassigned Unicode code point found in UTF-8 string.",
		ErrInvalidOpts: "Invalid options for UTF-8 processing chosen.",
		-99:            "An unknown error occurred while processing UTF-8 data.",
	} {
		if got := Errmsg(code); got != want {
			t.Errorf("Errmsg(%d) = %q", code, got)
		}
	}
}

func TestIterateAndEncode(t *testing.T) {
	for _, c := range []struct {
		in   []byte
		cp   int32
		n    int
		name string
	}{
		{nil, -1, 0, "empty"}, {[]byte("a"), 'a', 1, "ascii"}, {[]byte("é"), 0xe9, 2, "two bytes"}, {[]byte("€"), 0x20ac, 3, "three"},
		{[]byte("\U0001f600"), 0x1f600, 4, "four"}, {[]byte{0x80}, -1, ErrInvalidUTF8, "a continuation byte"}, {[]byte{0xc1, 0x80}, -1, ErrInvalidUTF8, "overlong lead"},
		{[]byte{0xf5, 0x80, 0x80, 0x80}, -1, ErrInvalidUTF8, "above 0xf4"}, {[]byte{0xc3}, -1, ErrInvalidUTF8, "a cut 2-byte"}, {[]byte{0xc3, 0x41}, -1, ErrInvalidUTF8, "no continuation"},
		{[]byte{0xe2, 0x82}, -1, ErrInvalidUTF8, "a cut 3-byte"}, {[]byte{0xe2, 0x41, 0xac}, -1, ErrInvalidUTF8, "3-byte, bad second"}, {[]byte{0xe2, 0x82, 0x41}, -1, ErrInvalidUTF8, "3-byte, bad third"},
		{[]byte{0xed, 0xa0, 0x80}, -1, ErrInvalidUTF8, "a surrogate"}, {[]byte{0xe0, 0x80, 0x80}, -1, ErrInvalidUTF8, "overlong 3-byte"},
		{[]byte{0xf0, 0x80, 0x80, 0x80}, -1, ErrInvalidUTF8, "overlong 4-byte"}, {[]byte{0xf4, 0x90, 0x80, 0x80}, -1, ErrInvalidUTF8, "above 10FFFF"},
		{[]byte{0xf0, 0x90, 0x80}, -1, ErrInvalidUTF8, "a cut 4-byte"}, {[]byte{0xf0, 0x90, 0x41, 0x80}, -1, ErrInvalidUTF8, "4-byte, bad third"},
		{[]byte{0xf0, 0x90, 0x80, 0x41}, -1, ErrInvalidUTF8, "4-byte, bad fourth"},
	} {
		if cp, n := Iterate(c.in); cp != c.cp || n != c.n {
			t.Errorf("%s: Iterate(%x) = %d, %d; want %d, %d", c.name, c.in, cp, n, c.cp, c.n)
		}
	}
	for cp, want := range map[int32]string{0x41: "A", 0xe9: "é", 0x20ac: "€", 0x1f600: "\U0001f600", 0xd800: "\xed\xa0\x80", -1: "", 0x110000: ""} {
		if got := string(EncodeChar(cp)); got != want {
			t.Errorf("EncodeChar(%#x) = %q, want %q", cp, got, want)
		}
	}
	if string(appendChar(nil, -1, true)) != "\xff" || len(appendChar(nil, -2, true)) != 0 {
		t.Error("the grapheme break marker")
	}
}

func TestDecomposeCharBuffers(t *testing.T) {
	// a buffer too short still counts what there is
	if n := DecomposeChar(0xAC01, nil, DecomposeOpt, nil); n != 3 {
		t.Errorf("a hangul syllable with no room: %d", n)
	}
	for size := 0; size <= 3; size++ {
		buf := make([]int32, size)
		if n := DecomposeChar(0xAC01, buf, DecomposeOpt, nil); n != 3 {
			t.Errorf("size %d: %d", size, n)
		}
	}
	// a syllable with no trailing consonant is two
	if n := DecomposeChar(0xAC00, make([]int32, 2), Compose, nil); n != 2 {
		t.Errorf("LV: %d", n)
	}
	// a decomposition into several, written through a short buffer, and out of range
	if n := DecomposeChar(0x1F82, make([]int32, 1), DecomposeOpt, nil); n < 2 {
		t.Errorf("%d", n)
	}
	if n := DecomposeChar(0x110000, nil, 0, nil); n != ErrNotAssigned {
		t.Errorf("out of range: %d", n)
	}
	if n := DecomposeChar(-1, nil, 0, nil); n != ErrNotAssigned {
		t.Errorf("negative: %d", n)
	}
	// the grapheme state, when the caller keeps one
	var bound int
	var seen []int32
	for _, c := range []int32{'a', 0x301, 'b'} {
		buf := make([]int32, 2)
		n := DecomposeChar(c, buf, CharBound, &bound)
		seen = append(seen, buf[:n]...)
	}
	if len(seen) != 5 || seen[0] != -1 || seen[2] != 0x301 || seen[3] != -1 {
		t.Errorf("char bounds: %v", seen)
	}
	// and a decomposition that overflows through the sequence writer is an error
	// when a nested decomposition is (never, here): the count is the sum
	if n := DecomposeChar(0xFB03, make([]int32, 8), DecomposeOpt|Compat, nil); n != 3 {
		t.Errorf("ffi: %d", n)
	}
}

func TestDecomposeCustomAndNullTerm(t *testing.T) {
	// the custom function maps each code point first
	out, code := MapCustom([]byte("abc"), Stable, func(c int32) int32 { return c + 1 })
	if code != 0 || string(out) != "bcd" {
		t.Errorf("MapCustom: %q %d", out, code)
	}
	// a NUL ends a string under NullTerm, and the end of the slice does too
	for _, in := range []string{"ab\x00cd", "ab"} {
		out, code := Map([]byte(in), NullTerm|Stable)
		if code != 0 || string(out) != "ab" {
			t.Errorf("NullTerm %q: %q %d", in, out, code)
		}
	}
	// a NUL inside the four bytes a multi-byte lead looks at
	if _, code := Map([]byte("\xe2\x00\x00"), NullTerm|Stable); code != ErrInvalidUTF8 {
		t.Errorf("a cut sequence before a NUL: %d", code)
	}
	if _, code := Map([]byte("\xff"), NullTerm|Stable); code != ErrInvalidUTF8 {
		t.Errorf("invalid under NullTerm: %d", code)
	}
	// options that cannot go together
	if _, code := Map([]byte("a"), Compose|DecomposeOpt); code != ErrInvalidOpts {
		t.Errorf("Compose with Decompose: %d", code)
	}
	if _, code := Map([]byte("a"), StripMark); code != ErrInvalidOpts {
		t.Errorf("StripMark alone: %d", code)
	}
	// an error in the second pass: the same input errors at once
	if _, code := Map([]byte("a\xff"), Stable); code != ErrInvalidUTF8 {
		t.Errorf("invalid: %d", code)
	}
	// a character the first pass accepts and the second refuses cannot be built; but RejectNA is refused in both
	if _, code := Map([]byte("͸"), RejectNA); code != ErrNotAssigned {
		t.Errorf("RejectNA: %d", code)
	}
	// Decompose sizes, then fills
	n := Decompose([]byte("é"), nil, DecomposeOpt)
	buf := make([]int32, n)
	if Decompose([]byte("é"), buf, DecomposeOpt) != 2 || buf[0] != 'e' || buf[1] != 0x301 {
		t.Errorf("Decompose: %d %v", n, buf)
	}
}

func TestNormalizeUTF32Branches(t *testing.T) {
	norm := func(in []int32, o Option) []int32 {
		b := append([]int32(nil), in...)
		return b[:NormalizeUTF32(b, len(b), o)]
	}
	eq := func(got, want []int32) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	// newlines: CRLF is one; each of the four options of NLF2LS/NLF2PS
	nl := []int32{'a', 0x0D, 0x0A, 'b', 0x0A, 'c', 0x0D, 'd', 0x85, 'e'}
	for o, want := range map[Option][]int32{
		NLF2LS | NLF2PS: {'a', 0x0A, 'b', 0x0A, 'c', 0x0A, 'd', 0x0A, 'e'},
		NLF2LS:          {'a', 0x2028, 'b', 0x2028, 'c', 0x2028, 'd', 0x2028, 'e'},
		NLF2PS:          {'a', 0x2029, 'b', 0x2029, 'c', 0x2029, 'd', 0x2029, 'e'},
		StripCC:         {'a', ' ', 'b', ' ', 'c', ' ', 'd', ' ', 'e'},
	} {
		if got := norm(nl, o); !eq(got, want) {
			t.Errorf("newlines under %d: %v, want %v", o, got, want)
		}
	}
	// control characters: dropped, except a tab (a space) and VT and FF (newlines)
	cc := []int32{'a', 0x01, 'b', 0x09, 'c', 0x7F, 0x9F, 'd', 0x0B, 0x0C, 'e'}
	if got, want := norm(cc, StripCC), []int32{'a', 'b', ' ', 'c', 'd', ' ', ' ', 'e'}; !eq(got, want) {
		t.Errorf("StripCC: %v, want %v", got, want)
	}
	// a CR at the very end is a newline and not half of a CRLF
	if got := norm([]int32{'a', 0x0D}, NLF2LS|NLF2PS); !eq(got, []int32{'a', 0x0A}) {
		t.Errorf("a final CR: %v", got)
	}
	// composition: hangul, and a mark that cannot combine with the starter
	for _, c := range []struct{ in, want []int32 }{
		{[]int32{0x1100, 0x1161}, []int32{0xAC00}},               // L V
		{[]int32{0x1100, 0x1161, 0x11A8}, []int32{0xAC01}},       // L V T
		{[]int32{0xAC00, 0x11A8}, []int32{0xAC01}},               // LV T
		{[]int32{0xAC01, 0x11A8}, []int32{0xAC01, 0x11A8}},       // LVT T: no
		{[]int32{0x1100, 'a'}, []int32{0x1100, 'a'}},             // L, not a vowel
		{[]int32{0xAC00, 'a'}, []int32{0xAC00, 'a'}},             // LV, not a trailing consonant
		{[]int32{'e', 0x301}, []int32{0xE9}},                     // a pair
		{[]int32{'e', 0x323, 0x301}, []int32{0x1EB9, 0x301}},     // e + dot below, then acute: no pair after
		{[]int32{'a', 0x301, 0x300}, []int32{0xE1, 0x300}},       // a second mark of another class cannot combine further
		{[]int32{'q', 0x301}, []int32{'q', 0x301}},               // no pair: the mark stays
		{[]int32{'e', 0x300, 0x301}, []int32{0xE8, 0x301}},       //
		{[]int32{0x1100, 0x1161, 0x300}, []int32{0xAC00, 0x300}}, // a mark after a syllable
		{[]int32{'a', 0x345, 0x345}, []int32{'a', 0x345, 0x345}}, // a second mark of the same class is blocked
		{[]int32{0x44, 0x307, 0x323}, []int32{0x1E0A, 0x323}},    // D + dot above composes; the dot below (not sorted here) cannot follow
		{[]int32{0x3C, 0x338}, []int32{0x226E}},                  // a pair whose second is the maximum of its row
		{[]int32{0xA0, 0x300}, []int32{0xA0, 0x300}},             // a starter with no pairs
	} {
		if got := norm(c.in, Compose); !eq(got, c.want) {
			t.Errorf("compose %x = %x, want %x", c.in, got, c.want)
		}
	}
}

func TestOverflowAndTheSecondPass(t *testing.T) {
	old := maxWords
	maxWords = 3
	if _, code := Map([]byte("abcdefgh"), Stable); code != ErrOverflow {
		t.Errorf("a string over the bound: %d", code)
	}
	maxWords = old
	// a custom function that answers differently the second time: the second
	// pass is the one that fails
	calls := 0
	_, code := MapCustom([]byte("a"), Stable, func(c int32) int32 {
		if calls++; calls > 1 {
			return 0x110000
		}
		return c
	})
	if code != ErrNotAssigned {
		t.Errorf("an error in the second pass: %d", code)
	}
	// a conjunct's linker followed by something that is neither a consonant nor an extend
	var state int32
	GraphemeBreakStateful(0x915, 0x94D, &state)
	if !GraphemeBreakStateful(0x94D, 'a', &state) {
		t.Error("a letter after a linker is a break")
	}
}

func TestReencodeCharBound(t *testing.T) {
	buf := []int32{-1, 'a', -1, 0x301}
	if got := Reencode(buf, len(buf), CharBound); string(got) != "\xffa\xff́" {
		t.Errorf("%q", got)
	}
	buf = []int32{-1, 'a'}
	if got := Reencode(buf, len(buf), 0); string(got) != "a" {
		t.Errorf("a marker is dropped without CharBound: %q", got)
	}
}

func TestNormalizationForms(t *testing.T) {
	for _, c := range []struct {
		f    func(string) (string, int)
		in   string
		want string
	}{
		{NFD, "é", "é"}, {NFC, "é", "é"}, {NFKD, "ﬁ", "fi"}, {NFKC, "ﬁ", "fi"}, {NFKCCasefold, "ÆﬁẞA", "æfissa"},
	} {
		if got, code := c.f(c.in); code != 0 || got != c.want {
			t.Errorf("%q: %q %d, want %q", c.in, got, code, c.want)
		}
	}
	if _, code := NFC("\xff"); code != ErrInvalidUTF8 {
		t.Errorf("invalid: %d", code)
	}
	if got, _ := Map(nil, Stable); len(got) != 0 {
		t.Errorf("empty: %q", got)
	}
}

func TestGraphemeBreakRules(t *testing.T) {
	// GB1 (start of text) and the rest, by bound class
	for _, c := range []struct {
		l, t int
		want bool
		rule string
	}{
		{boundStart, boundOther, true, "GB1"}, {boundCR, boundLF, false, "GB3"}, {boundControl, boundOther, true, "GB4"},
		{boundOther, boundControl, true, "GB5"}, {boundL, boundV, false, "GB6"}, {boundLV, boundT, false, "GB7"},
		{boundLVT, boundT, false, "GB8"}, {boundOther, boundExtend, false, "GB9"}, {boundOther, boundZWJ, false, "GB9"},
		{boundOther, boundSpacing, false, "GB9a"}, {boundPrepend, boundOther, false, "GB9b"},
		{boundEZWG, boundExtPict, false, "GB11"}, {boundRI, boundRI, false, "GB12"}, {boundOther, boundOther, true, "GB999"},
		{boundT, boundL, true, "T then L"},
	} {
		if got := graphemeBreakSimple(c.l, c.t); got != c.want {
			t.Errorf("%s: (%d, %d) = %v", c.rule, c.l, c.t, got)
		}
	}
	// the stateful walk: an emoji ZWJ sequence, regional indicators, a conjunct
	walk := func(cps ...int32) []int {
		var state int32
		size, out := 1, []int(nil)
		for i := 1; i < len(cps); i++ {
			if GraphemeBreakStateful(cps[i-1], cps[i], &state) {
				out, size = append(out, size), 0
			}
			size++
		}
		return append(out, size)
	}
	family := walk(0x1F468, 0x200D, 0x1F469, 0x200D, 0x1F467)
	if len(family) != 1 || family[0] != 5 {
		t.Errorf("an emoji ZWJ sequence: %v", family)
	}
	flags := walk(0x1F1FA, 0x1F1F8, 0x1F1EB, 0x1F1F7, 0x1F1E9)
	if len(flags) != 3 || flags[0] != 2 || flags[1] != 2 || flags[2] != 1 {
		t.Errorf("regional indicators: %v", flags)
	}
	// a conjunct: KA virama SSA is one cluster, and with an extend between them
	if g := walk(0x915, 0x94D, 0x937); len(g) != 1 {
		t.Errorf("a conjunct: %v", g)
	}
	if g := walk(0x915, 0x94D, 0x200D, 0x937); len(g) != 1 {
		t.Errorf("a conjunct with a joiner: %v", g)
	}
	if g := walk(0x915, 0x93C, 0x94D, 0x937); len(g) != 1 {
		t.Errorf("a conjunct with a nukta: %v", g)
	}
	if g := walk(0x915, 'a'); len(g) != 2 {
		t.Errorf("a consonant then a letter: %v", g)
	}
	if g := walk('a', 0x94D, 0x937); len(g) != 2 {
		t.Errorf("a linker after a letter: %v", g)
	}
}
