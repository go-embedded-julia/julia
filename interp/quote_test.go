package interp

import (
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// quoteString against julia 1.12.6's repr (testdata/nulshow.jl wrote
// quote.golden: the bytes in hex, then the repr).
func TestQuoteString(t *testing.T) {
	data, err := os.ReadFile("testdata/quote.golden")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) < 40 {
		t.Fatalf("quote.golden has %d cases", len(lines))
	}
	for _, line := range lines {
		h, want, ok := strings.Cut(line, " ")
		s, err := hex.DecodeString(h)
		if !ok || err != nil {
			t.Fatalf("quote.golden: %q", line)
		}
		if got := quoteString(string(s)); got != want {
			t.Errorf("repr of %s: got %s, want %s", h, got, want)
		}
	}
}

// Every byte string has a repr, which names each byte: it is valid UTF-8
// and as long as the escapes make it.
func FuzzQuoteString(f *testing.F) {
	for _, s := range []string{"", "\x00", "\xe2\x82(", "\U0010ffff0", "\xf0\x9f\x98"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		q := quoteString(s)
		if !utf8.ValidString(q) || len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
			t.Errorf("quoteString(%q) = %s", s, q)
		}
		if len(q) > 2+10*len(s) {
			t.Errorf("quoteString(%q) is %d bytes", s, len(q))
		}
	})
}
