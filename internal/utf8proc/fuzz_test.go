package utf8proc

import (
	"testing"
	"unicode/utf8"
)

// FuzzMap feeds the string-taking entry points arbitrary bytes and options:
// nothing may panic or loop, the answer is the same twice, an option set utf8proc
// refuses is a negative code and never a result, and for valid UTF-8 the
// normal forms are what they are defined to be (idempotent, NFD of NFC is NFD).
// The corpus is committed (testdata/fuzz/FuzzMap).
func FuzzMap(f *testing.F) {
	for _, s := range []string{"", "a", "é", "é", "각", "각", "ﬁ", "\xff", "\xe2\x82", "a\x00b", "\U0001f468‍\U0001f469", "क़̈́"} {
		f.Add(s, int(Compose|Stable))
		f.Add(s, int(DecomposeOpt|Compat|Stable|CaseFold))
	}
	f.Fuzz(func(t *testing.T, s string, opts int) {
		o := Option(opts)
		out1, code1 := Map([]byte(s), o)
		out2, code2 := Map([]byte(s), o)
		if code1 != code2 || string(out1) != string(out2) {
			t.Fatalf("Map is not deterministic on %q, %d", s, opts)
		}
		if code1 < 0 && out1 != nil {
			t.Fatalf("an error with a result: %q", out1)
		}
		_ = Decompose([]byte(s), nil, o)
		// the grapheme state machine over the string's code points
		var state int32
		prev := int32(-1)
		for _, r := range s {
			if prev >= 0 {
				GraphemeBreakStateful(prev, int32(r), &state)
			}
			prev = int32(r)
		}
		if !utf8.ValidString(s) || o&(NullTerm|Compose|DecomposeOpt|StripMark|RejectNA|StripNA|CharBound) != 0 {
			return
		}
		for _, form := range []func(string) (string, int){NFC, NFD, NFKC, NFKD} {
			a, code := form(s)
			if code != 0 {
				t.Fatalf("a valid string is an error: %q %d", s, code)
			}
			b, _ := form(a)
			if a != b {
				t.Fatalf("a normal form is not idempotent on %q: %q then %q", s, a, b)
			}
		}
		nfc, _ := NFC(s)
		nfd, _ := NFD(s)
		if d2, _ := NFD(nfc); d2 != nfd {
			t.Fatalf("NFD(NFC(x)) != NFD(x) on %q", s)
		}
	})
}
