package interp

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestUtf8procBase runs the functions of Base.Unicode on julia's Base image
// (JLGO_BASE=1; JLGO_KERNEL_IMAGE names an image, else Base is built) against
// what julia 1.12.6 answered (internal/utf8proc/testdata/strings.golden.gz,
// written by oracle.jl): the four normal forms, uppercase and lowercase of
// strings, and the grapheme break between pairs. And it times uppercase and
// normalize on a large string (the numbers go to the log; julia's are
// testdata/bench.jl's).
func TestUtf8procBase(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 (and optionally JLGO_KERNEL_IMAGE, JLGO_IMAGE_VERSION)")
	}
	tag := os.Getenv("JLGO_IMAGE_VERSION")
	if tag == "" {
		tag = "v0.0.1"
	}
	var img []byte
	if file := os.Getenv("JLGO_KERNEL_IMAGE"); file != "" {
		fakeReleaseVersion(t, tag)
		var err error
		if img, err = os.ReadFile(file); err != nil {
			t.Fatal(err)
		}
	} else {
		img = buildBaseImage(t, tag)
	}
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 40 * time.Minute, MaxAlloc: -1}})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	ctx := context.Background()
	// normalize is the Unicode stdlib's name for Base.Unicode.normalize (not one
	// of the seven stdlibs of the image)
	if r := k.Eval(ctx, `const N = Base.Unicode; hx(s) = join((string(UInt32(c), base=16) for c in s), " ")`); r.Err != nil {
		t.Fatal(r.Err)
	}

	f, err := os.Open("../internal/utf8proc/testdata/strings.golden.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	z, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	sc := bufio.NewScanner(z)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() && n < 300 {
		fields := strings.Split(sc.Text(), " | ")
		if fields[0] == "" {
			continue
		}
		var lits []string
		for _, h := range strings.Fields(fields[0]) {
			lits = append(lits, "0x"+h)
		}
		src := fmt.Sprintf(`let s = String(Char[%s]); join((hx(N.normalize(s, :NFC)), hx(N.normalize(s, :NFD)), hx(N.normalize(s, :NFKC)), hx(N.normalize(s, :NFKD)), hx(uppercase(s)), hx(lowercase(s))), " | "); end`, strings.Join(lits, ", "))
		r := k.Eval(ctx, src)
		if r.Err != nil {
			t.Fatalf("%s\n  %v", src, r.Err)
		}
		got, err := strconv.Unquote(r.Value)
		if err != nil {
			t.Fatalf("%q: %v", r.Value, err)
		}
		want := strings.Join([]string{fields[1], fields[2], fields[3], fields[4], fields[7], fields[8]}, " | ")
		if got != want {
			t.Fatalf("string %s\n  ours:  %s\n  julia: %s", fields[0], got, want)
		}
		n++
	}
	if n < 100 {
		t.Errorf("%d strings compared", n)
	}
	// ß is capital sharp s in julia (utf8proc maps it), and the rest of what a notebook asks
	for src, want := range map[string]string{
		`uppercase("straße")`: `"STRAẞE"`, `lowercase("ÀÉÎ")`: `"àéî"`, `titlecase("hello wörld")`: `"Hello Wörld"`,
		`isuppercase('É')`: `true`, `islowercase('é')`: `true`, `isletter('é')`: `true`, `textwidth("日本語")`: `6`,
		`Base.Unicode.isgraphemebreak('a', '́')`: `false`, `Base.Unicode.isgraphemebreak('a', 'b')`: `true`,
		`N.normalize("é", :NFC) == "é"`: `true`, `N.normalize("ﬁ", :NFKC)`: `"fi"`, `N.normalize("É", casefold=true)`: `"é"`,
		`N.normalize("ÀB̀", stripmark=true)`: `"AB"`, `Base.Unicode.category_abbrev('é')`: `"Ll"`, `uppercasefirst("élan")`: `"Élan"`,
	} {
		if r := k.Eval(ctx, src); r.Err != nil || r.Value != want {
			t.Errorf("%s = %q, %v; julia %s", src, r.Value, r.Err, want)
		}
	}
	// an invalid string is julia's error
	if r := k.Eval(ctx, `N.normalize(String(UInt8[0xff, 0x41]), :NFC)`); r.Err == nil || !strings.Contains(r.Err.Error(), "Invalid UTF-8 string") {
		t.Errorf("normalize of invalid UTF-8: %+v", r)
	}

	// large strings: the cost of the calls, best of 5 rounds
	k.Eval(ctx, `big = repeat("Héllo wörld ǆ ß Straße ﬁ é 각 ΑΣ ", 20000); mid = first(big, 66000); nothing`) // uppercase and lowercase: Julia code over a ccall per character
	for _, c := range []struct{ name, src, of string }{{"uppercase", `uppercase(mid); nothing`, "mid"}, {"lowercase", `lowercase(mid); nothing`, "mid"},
		{"normalize NFC", `N.normalize(big, :NFC); nothing`, "big"}, {"normalize NFD", `N.normalize(big, :NFD); nothing`, "big"}} {
		best := time.Hour
		for i := 0; i < 3; i++ {
			r := k.Eval(ctx, c.src)
			if r.Err != nil {
				t.Fatalf("%s: %v", c.name, r.Err)
			}
			best = min(best, r.Elapsed)
		}
		t.Logf("%s of %s characters: %v (load %.1f)", c.name, k.Eval(ctx, `length(`+c.of+`)`).Value, best, loadAverage())
	}
}
