package utf8proc

import (
	"bufio"
	"compress/gzip"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// The oracle is julia 1.12.6's own utf8proc (testdata/oracle.jl): every
// function of this package over every code point, and over strings, against
// the files it wrote.

func lines(t testing.TB, name string) []string {
	t.Helper()
	f, err := os.Open("testdata/" + name + ".golden.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	sc := bufio.NewScanner(z)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func num(t testing.TB, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func hexCPs(t testing.TB, s string) []int32 {
	t.Helper()
	var out []int32
	for _, f := range strings.Fields(s) {
		v, err := strconv.ParseUint(f, 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, int32(v))
	}
	return out
}

func runesOf(cps []int32) string {
	var b strings.Builder
	for _, c := range cps {
		b.Write(EncodeChar(c))
	}
	return b.String()
}

// Every code point: the category, the width, the case maps and the tests.
func TestPerCodePoint(t *testing.T) {
	next := 0
	for _, l := range lines(t, "percp") {
		f := strings.Fields(l)
		lo, hi := num(t, f[0]), num(t, f[1])
		if lo != next {
			t.Fatalf("the golden skips %d..%d", next, lo-1)
		}
		next = hi + 1
		want := make([]int, 8)
		for i := range want {
			want[i] = num(t, f[2+i])
		}
		for cp := lo; cp <= hi; cp++ {
			c := int32(cp)
			got := []int{Category(c), CharWidth(c), b2i(CharWidthAmbiguous(c)), int(ToUpper(c)) - cp, int(ToLower(c)) - cp, int(ToTitle(c)) - cp, b2i(IsLower(c)), b2i(IsUpper(c))}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("U+%04X: field %d is %d, julia %d (category, width, ambiguous, upper, lower, title, islower, isupper)", cp, i, got[i], want[i])
				}
			}
		}
	}
	if next != 0x110000 {
		t.Fatalf("the golden ends at %d", next)
	}
	// out of range is the unassigned code point's
	for _, c := range []int32{-1, 0x110000, 1 << 30} {
		if Category(c) != 0 || CharWidth(c) != 1 || ToUpper(c) != c || ToLower(c) != c || ToTitle(c) != c || IsLower(c) || IsUpper(c) {
			t.Errorf("%d is not the unassigned code point", c)
		}
	}
	if CategoryString(0x41) != "Lu" || CategoryString(0x3042) != "Lo" || CategoryString(-1) != "Cn" || CategoryString(0xE000) != "Co" {
		t.Error("CategoryString")
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Every code point that is not a surrogate, under each option set: the result
// when it is not the code point itself.
func TestNormalizeEveryCodePoint(t *testing.T) {
	all := lines(t, "normalize")
	want := map[string]map[int32][]int32{}
	opts := map[string]Option{}
	var name string
	for _, l := range all {
		if strings.HasPrefix(l, "# ") {
			f := strings.Fields(l)
			name = f[1]
			opts[name] = Option(num(t, f[2]))
			want[name] = map[int32][]int32{}
			continue
		}
		f := strings.SplitN(l, " ", 3)
		cp := hexCPs(t, f[1])[0]
		want[f[0]][cp] = hexCPs(t, f[2])
	}
	if len(opts) != 7 {
		t.Fatalf("%d option sets in the golden", len(opts))
	}
	for name, o := range opts {
		for cp := int32(0); cp < 0x110000; cp++ {
			if cp >= 0xd800 && cp <= 0xdfff {
				continue
			}
			s := runesOf([]int32{cp})
			out, code := Map([]byte(s), o)
			if code != 0 {
				t.Fatalf("%s U+%04X: error %d", name, cp, code)
			}
			w, ok := want[name][cp]
			if !ok {
				w = []int32{cp}
			}
			if string(out) != runesOf(w) {
				t.Fatalf("%s U+%04X: %x, julia %x", name, cp, out, runesOf(w))
			}
		}
	}
}

// Strings: the five forms, the grapheme clusters, and the case maps.
func TestStrings(t *testing.T) {
	forms := []Option{Compose | Stable, DecomposeOpt | Stable, Compose | Compat | Stable, DecomposeOpt | Compat | Stable,
		Compose | Stable | CaseFold | Ignore | Compat}
	n := 0
	for _, l := range lines(t, "strings") {
		f := strings.Split(l, " | ")
		if len(f) != 1+5+1+3 {
			t.Fatalf("a line of %d fields: %q", len(f), l)
		}
		in := hexCPs(t, f[0])
		s := runesOf(in)
		for i, o := range forms {
			out, code := Map([]byte(s), o)
			if code != 0 || string(out) != runesOf(hexCPs(t, f[1+i])) {
				t.Fatalf("%q under %d: %x (%d), julia %s", s, o, out, code, f[1+i])
			}
		}
		// graphemes, as Base.Unicode.isgraphemebreak! walks them
		var got []string
		if len(in) > 0 {
			var state int32
			size := 1
			for i := 1; i < len(in); i++ {
				if GraphemeBreakStateful(in[i-1], in[i], &state) {
					got = append(got, strconv.Itoa(size))
					size = 0
				}
				size++
			}
			got = append(got, strconv.Itoa(size))
		}
		if strings.Join(got, " ") != f[6] {
			t.Fatalf("graphemes of %q: %v, julia %s", s, got, f[6])
		}
		var up, lo []int32
		for _, c := range in {
			up, lo = append(up, ToUpper(c)), append(lo, ToLower(c))
		}
		if runesOf(up) != runesOf(hexCPs(t, f[7])) || runesOf(lo) != runesOf(hexCPs(t, f[8])) {
			t.Fatalf("case of %q: %q %q, julia %s | %s", s, runesOf(up), runesOf(lo), f[7], f[8])
		}
		n++
	}
	if n < 2000 {
		t.Errorf("%d strings in the golden", n)
	}
}

func TestGraphemeBreakPairs(t *testing.T) {
	for _, l := range lines(t, "graphemebreak") {
		f := strings.Fields(l)
		a, b := hexCPs(t, f[0])[0], hexCPs(t, f[1])[0]
		if got := GraphemeBreak(a, b); got != (f[2] == "1") {
			t.Errorf("break between U+%04X and U+%04X: %v, julia %s", a, b, got, f[2])
		}
	}
}

// The code points whose case mappings differ between Go's Unicode tables and
// utf8proc's: those Unicode added between the two versions. utf8proc 2.10.0
// implements Unicode 16.0.0; Go's unicode.Version is what its tables are.
// The test names the ones Unicode 16 added with a case mapping, which a program
// that used Go's unicode.ToUpper would not map as julia does.
func TestUnicodeVersionDifference(t *testing.T) {
	if UnicodeVersion != "16.0.0" {
		t.Fatal(UnicodeVersion)
	}
	// 1. utf8proc maps ß to U+1E9E (capital sharp s), as julia's uppercase("ß")
	// does; Go's unicode.ToUpper leaves it
	if ToUpper(0xDF) != 0x1E9E || unicode.ToUpper(0xDF) != 0xDF {
		t.Errorf("ß: utf8proc U+%04X, Go U+%04X", ToUpper(0xDF), unicode.ToUpper(0xDF))
	}
	// 2. Go 17.0.0's tables know the letters Unicode 17 added, which Unicode 16
	// (utf8proc's) has not: Beria Erfe U+16EA0..U+16ED3 and the Latin letters
	// U+A7CE, U+A7CF, U+A7D2 and U+A7D4 (U+A7D3 and U+A7D5 are in both, their
	// capitals are the new ones). julia maps none of them.
	new17 := []rune{0xA7CE, 0xA7CF, 0xA7D2, 0xA7D3, 0xA7D4, 0xA7D5}
	for c := rune(0x16EA0); c <= 0x16ED3; c++ {
		if c != 0x16EB9 && c != 0x16EBA {
			new17 = append(new17, c)
		}
	}
	for _, c := range new17 {
		if ToUpper(int32(c)) != int32(c) || ToLower(int32(c)) != int32(c) {
			t.Errorf("utf8proc maps U+%04X, which Unicode 16 has no case mapping for", c)
		}
	}
	// 3. and those, with ß, are every difference there is: any other code point
	// maps as it does in Go (when Go's tables are Unicode 17.0.0)
	if unicode.Version != "17.0.0" {
		t.Skipf("Go's Unicode is %s: the list of differences is that of 17.0.0", unicode.Version)
	}
	known := map[rune]bool{0xDF: true}
	for _, c := range new17 {
		known[c] = true
	}
	differ := 0
	for cp := rune(0); cp < 0x110000; cp++ {
		if cp >= 0xd800 && cp <= 0xdfff {
			continue
		}
		if rune(ToUpper(int32(cp))) != unicode.ToUpper(cp) || rune(ToLower(int32(cp))) != unicode.ToLower(cp) {
			differ++
			if !known[cp] && !(cp >= 0x16EBB && cp <= 0x16ED3) {
				t.Errorf("U+%04X maps differently in Go and in utf8proc, and is not a known difference", cp)
			}
		}
	}
	if differ != 57 {
		t.Errorf("%d code points differ between Go %s and utf8proc (Unicode %s), 57 expected", differ, unicode.Version, UnicodeVersion)
	}
}
