package syntax

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

func init() {
	levels = append(levels, level{
		name:   "green trees",
		run:    greenOf,
		golden: func(s gen.Snippet) string { return s.Green },
		digest: func(e gen.CorpusEntry) (string, bool) { return e.GreenSHA256, e.GreenParity },
	})
}

func greenOf(t testing.TB, src []byte, _ string) string {
	t.Helper()
	g, err := GreenDump(src)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// fuzzLimits keep each fuzz input cheap; the depth bound is what stands
// between a hostile input and the goroutine stack.
var fuzzLimits = Limits{MaxBytes: 1 << 16, MaxDepth: 500}

// FuzzGreen checks, for any input, that the parse stream terminates without
// panicking (or reports a limit) and that its green tree covers the input
// losslessly, every node spanning exactly its children.
func FuzzGreen(f *testing.F) {
	for _, sn := range readGolden[gen.Snippet](f, "snippets.jsonl") {
		f.Add([]byte(sn.Source()))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		var g *greenNode
		if err := guarded(func() {
			s := newParseStream(src, 1, SyntaxVersion, fuzzLimits)
			newParseState(s).parseToplevel()
			s.validateTokens()
			g = s.buildGreen()
		}); err != nil {
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("non-limit error %v", err)
			}
			return
		}
		if g.span != len(src) {
			t.Fatalf("green tree spans %d of %d bytes", g.span, len(src))
		}
		var check func(n *greenNode)
		check = func(n *greenNode) {
			sum := 0
			for _, c := range n.children {
				check(c)
				sum += c.span
			}
			if !n.leaf && len(n.children) > 0 && sum != n.span {
				t.Fatalf("%v spans %d, children %d", n.head.Kind, n.span, sum)
			}
		}
		check(g)
	})
}

func TestStreamLimits(t *testing.T) {
	// An ncat dimension that does not fit the flags. JuliaSyntax raises an
	// error here (and julia falls back to its flisp parser).
	wide := []byte("[a " + strings.Repeat(";", 300) + " b]")
	if _, err := GreenDump(wide); !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "Numeric flags") {
		t.Fatalf("numeric flags: %v", err)
	}
	deep := []byte(strings.Repeat("(", 5000))
	if _, err := GreenDump(deep); !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "depth limit") {
		t.Fatalf("depth: %v", err)
	}
	if (parseLimitError{"m"}).Error() != "m" {
		t.Fatal("parseLimitError.Error")
	}
}

// TestOlderSyntaxVersion: syntax added after the requested version is an
// error (JuliaSyntax's min_supported_version), and `public` is a plain
// identifier before 1.11.
func TestOlderSyntaxVersion(t *testing.T) {
	saved := SyntaxVersion
	defer func() { SyntaxVersion = saved }()
	SyntaxVersion = [2]int{1, 5}
	g := greenOf(t, []byte("import A as B"), "")
	if !strings.Contains(g, "`import ... as` not supported in Julia version 1.5 < 1.6") {
		t.Fatalf("version error missing: %s", g)
	}
	SyntaxVersion = [2]int{1, 10}
	if g := greenOf(t, []byte("public = 1"), ""); strings.Contains(g, "error") || strings.Contains(g, "warning") {
		t.Fatalf("public as an identifier before 1.11: %s", g)
	}
}

func TestDiagnosticLevels(t *testing.T) {
	ds := []Diagnostic{newDiag(1, 1, levelWarning, "w")}
	if ds[0].IsError() || anyError(ds) || !anyError(append(ds, newDiag(1, 1, levelError, "e"))) {
		t.Fatal("levels")
	}
}

func TestGuardedRepanics(t *testing.T) {
	defer func() {
		if r := recover(); r != "other" {
			t.Fatalf("recovered %v", r)
		}
	}()
	guarded(func() { panic("other") })
}

func TestStuckParser(t *testing.T) {
	s := newParseStream([]byte("x"), 1, SyntaxVersion, DefaultLimits)
	err := guarded(func() {
		for {
			s.peek(1, false)
		}
	})
	if !errors.Is(err, ErrLimit) || !strings.Contains(err.Error(), "stuck") {
		t.Fatalf("stuck: %v", err)
	}
}

func TestBumpAtEnd(t *testing.T) {
	s := newParseStream([]byte(" "), 1, SyntaxVersion, DefaultLimits)
	s.bump(emptyFlags, false, "", kNone) // bumps the whitespace, stops at EndMarker
	if s.tokens[len(s.tokens)-1].head.Kind != kWhitespace {
		t.Fatalf("last token %v", s.tokens[len(s.tokens)-1].head.Kind)
	}
}

func TestBuildTreeWrapper(t *testing.T) {
	// Two top-level nodes are wrapped, as build_tree does.
	s := newParseStream([]byte("a b"), 1, SyntaxVersion, DefaultLimits)
	ps := newParseState(s)
	ps.parseAtom(true, false)
	ps.parseAtom(true, false)
	g := s.buildGreen()
	if g.head.Kind != kWrapper || len(g.children) != 3 {
		t.Fatalf("wrapper: %v with %d children", g.head.Kind, len(g.children))
	}
}

var reprCases = []string{
	"a", "\x00", "\x001", "\x00a", "\x1b", "\\", "\a\b\t\n\v\f\r", "$\"", "\x7f", "\x01",
	"­", "­a", "", "\U0010ffff", "\U000f0000", "\U000f0000a", "😀", "é",
	"\xff", "\xc0\x80", "\xe2\x88", "\xed\xa0\x80", "'",
}

// TestRepr pins Julia's repr of strings and characters (as diagnostics
// quote them). The expected texts were checked against julia by
// TestReprAgainstJulia.
func TestRepr(t *testing.T) {
	bu, bU := "\\"+"u", "\\"+"U" // written by concatenation: these escapes must stay escapes
	want := []string{
		`"a"`, `"\0"`, `"\x001"`, `"\0a"`, `"\e"`, `"\\"`, `"\a\b\t\n\v\f\r"`, `"\$\""`, `"\x7f"`, `"\x01"`,
		`"` + bu + `ad"`, `"` + bu + `00ada"`, `"` + bu + `e000"`, `"` + bU + `10ffff"`, `"` + bU + `f0000"`,
		`"` + bU + `000f0000a"`, `"😀"`, `"é"`, `"\xff"`, `"\xc0\x80"`, `"\xe2\x88"`, `"` + bu + `d800"`, `"'"`,
	}
	wantChar := []string{
		`'a'`, `'\0'`, `'\0'`, `'\0'`, `'\e'`, `'\\'`, `'\a'`, `'$'`, `'\x7f'`, `'\x01'`,
		`'` + bu + `ad'`, `'` + bu + `ad'`, `'` + bu + `e000'`, `'` + bU + `10ffff'`, `'` + bU + `f0000'`,
		`'` + bU + `f0000'`, `'😀'`, `'é'`, `'\xff'`, `'\xc0\x80'`, `'\xe2\x88'`, `'` + bu + `d800'`, `'\''`,
	}
	for i, s := range reprCases {
		if got := reprString(s); got != want[i] {
			t.Errorf("reprString(%q) = %s, want %s", s, got, want[i])
		}
		if got := reprChar(juliaChars(s)[0]); got != wantChar[i] {
			t.Errorf("reprChar(%q) = %s, want %s", s, got, wantChar[i])
		}
	}
	if hexPad(0xab, 1) != "ab" || bitsLeadingZeros32(1) != 31 {
		t.Fatal("helpers")
	}
}

// TestReprAgainstJulia checks reprString and reprChar on the cases above
// with the reference julia (JLGO_ORACLE=1).
func TestReprAgainstJulia(t *testing.T) {
	if !live {
		t.Skip("needs JLGO_ORACLE=1")
	}
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, s := range reprCases {
		if j, _ := p.Call("repr", s); j != reprString(s) {
			t.Errorf("repr(%q): julia %s, go %s", s, j, reprString(s))
		}
		if j, _ := p.Call("reprchar", s); j != reprChar(juliaChars(s)[0]) {
			t.Errorf("repr(first(%q)): julia %s, go %s", s, j, reprChar(juliaChars(s)[0]))
		}
	}
}
