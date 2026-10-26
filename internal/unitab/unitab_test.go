package unitab

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestGraphemeBreakGolden replays utf8proc's stateful grapheme breaks,
// recorded from the reference julia, through the Go port.
func TestGraphemeBreakGolden(t *testing.T) {
	f, err := os.Open("testdata/graphemebreak.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		cpsStr, ansStr, _ := strings.Cut(sc.Text(), ":")
		var cps []uint32
		for _, s := range strings.Split(cpsStr, ",") {
			v, _ := strconv.ParseUint(s, 10, 32)
			cps = append(cps, uint32(v))
		}
		want := strings.Split(ansStr, ",")
		var st int32
		for i := 0; i+1 < len(cps); i++ {
			got := "0"
			if GraphemeBreak(&st, cps[i], cps[i+1]) {
				got = "1"
			}
			if got != want[i] {
				t.Fatalf("%s: break %d is %s, utf8proc says %s", cpsStr, i, got, want[i])
			}
		}
		n++
	}
	if n < 1000 {
		t.Fatalf("only %d sequences", n)
	}
}

// TestNormalizeSingletons checks the identifier normalisation of every code
// point julia's normalize_identifier changes.
func TestNormalizeSingletons(t *testing.T) {
	data, err := os.ReadFile("testdata/normalize.txt")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		cpStr, outStr, _ := strings.Cut(line, " ")
		cp, _ := strconv.ParseUint(cpStr, 10, 32)
		var want strings.Builder
		for _, s := range strings.Split(outStr, ",") {
			v, _ := strconv.ParseUint(s, 10, 32)
			want.WriteRune(rune(v))
		}
		if got := NormalizeIdentifier(string(rune(cp))); got != want.String() {
			t.Errorf("U+%04X: got %q, julia %q", cp, got, want.String())
		}
		n++
	}
	if n < 100 {
		t.Fatalf("only %d singletons", n)
	}
	if got := NormalizeIdentifier("é"); got != "é" {
		t.Errorf("composition: %q", got)
	}
	if got := NormalizeIdentifier("\xffa"); got != "\xffa" {
		t.Errorf("invalid UTF-8 changed: %q", got)
	}
}

func TestLookups(t *testing.T) {
	if !IsIDStart('a') || IsIDStart('1') || !IsIDChar('1') || IsIDChar(' ') {
		t.Error("identifier tables")
	}
	if Category('a') != 2 || Category(0x0301) != CatMn || Category(0x10ffff) != 0 {
		t.Error("category table")
	}
	if !IsSpace(' ') || IsSpace('a') || Width('a') != 1 || Width(0x4e00) != 2 || Width(0x0301) != 0 {
		t.Error("space or width table")
	}
	if bc, icb := Bound(0x094d); bc != bcExtend || icb != icbLinker {
		t.Errorf("Bound(virama) = %d, %d", bc, icb)
	}
	if UnicodeVersion == "" {
		t.Error("no Unicode version")
	}
	// No code point has bound class 0 (UTF8PROC_BOUNDCLASS_START): the
	// table covers all of 0..0x10FFFF with nonzero classes.
	next := uint32(0)
	for _, r := range boundTable {
		if r.lo != next || r.v&0x3f == 0 {
			t.Fatalf("bound table gap or START class at 0x%x", next)
		}
		next = r.hi + 1
	}
	if next != 0x110000 {
		t.Fatalf("bound table ends at 0x%x", next)
	}
}
