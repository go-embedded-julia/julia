package utf8proc

import (
	"strings"
	"testing"
)

// The same string as testdata/bench.jl: a 33-character unit repeated 20000
// times (660 000 characters), and 2000 times for the per-character maps.
const benchUnit = "Héllo wörld ǆ ß Straße ﬁ é 각 ΑΣ "

func BenchmarkMapNFC(b *testing.B) {
	s := []byte(strings.Repeat(benchUnit, 20000))
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		if _, code := Map(s, Compose|Stable); code != 0 {
			b.Fatal(code)
		}
	}
}

func BenchmarkMapNFD(b *testing.B) {
	s := []byte(strings.Repeat(benchUnit, 20000))
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		if _, code := Map(s, DecomposeOpt|Stable); code != 0 {
			b.Fatal(code)
		}
	}
}

// BenchmarkToUpper maps every character of 66 000 as julia's
// map(uppercase, s) does, one call per character.
func BenchmarkToUpper(b *testing.B) {
	s := []byte(strings.Repeat(benchUnit, 2000))
	b.SetBytes(int64(len(s)))
	for i := 0; i < b.N; i++ {
		out := make([]byte, 0, len(s)+len(s)/8)
		for r := s; len(r) > 0; {
			c, n := Iterate(r)
			out = appendChar(out, ToUpper(c), false)
			r = r[n:]
		}
	}
}
