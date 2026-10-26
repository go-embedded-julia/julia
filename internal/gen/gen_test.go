package gen

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// fake answers oracle requests from a function, failing on demand.
type fake struct {
	answer func(op string, args []string) (string, error)
}

func (f fake) Call(op string, args ...string) (string, error) { return f.answer(op, args) }

var errBoom = errors.New("boom")

func TestUnicodeTables(t *testing.T) {
	o := fake{func(op string, _ []string) (string, error) {
		return "unicode 16.0.0\nprop idstart\n0 64 0\n65 90 1\n91 1114111 0\n" +
			"prop idchar\n0 1114111 1\nprop cat\n0 1114111 2\nprop space\n0 1114111 0\n" +
			"prop bound\n0 1114111 1\nprop width\n0 1114111 1\n", nil
	}}
	src, err := UnicodeTables(o, "1.12.6")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, `const UnicodeVersion = "16.0.0"`) || !strings.Contains(s, "{0x41, 0x5a, 1}") ||
		strings.Contains(s, "{0x0, 0x40, 0}") {
		t.Fatalf("tables:\n%s", s)
	}
	for _, bad := range []string{
		"prop idstart\n0 1 0\n",            // no version
		"unicode 1\nprop idstart\n0 1\n",   // short line
		"unicode 1\n0 1 0\n",               // line before any prop
		"unicode 1\nprop idstart\n0 x 0\n", // not a number
		"unicode 1\nprop idstart\n0 1 0\n", // missing tables
	} {
		o := fake{func(string, []string) (string, error) { return bad, nil }}
		if _, err := UnicodeTables(o, "x"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if _, err := UnicodeTables(fake{func(string, []string) (string, error) { return "", errBoom }}, "x"); err != errBoom {
		t.Errorf("oracle error: %v", err)
	}
}

func TestGraphemeGolden(t *testing.T) {
	calls := 0
	o := fake{func(op string, args []string) (string, error) {
		calls++
		n := strings.Count(args[0], ",")
		return strings.TrimSuffix(strings.Repeat("1,", n), ","), nil
	}}
	data, err := GraphemeGolden(o)
	if err != nil || calls != len(GraphemeSequences()) || !strings.HasPrefix(string(data), "128512,8205,128512:1,1\n") {
		t.Fatalf("calls %d, err %v, data %.40q", calls, err, data)
	}
	if _, err := GraphemeGolden(fake{func(string, []string) (string, error) { return "", errBoom }}); err != errBoom {
		t.Errorf("oracle error: %v", err)
	}
}

func TestSnippets(t *testing.T) {
	list := base64.StdEncoding.EncodeToString([]byte("a")) + "\n\n" + base64.StdEncoding.EncodeToString([]byte("\xff"))
	o := fake{func(op string, args []string) (string, error) {
		if op == "snippets" {
			return list, nil
		}
		return op + ":" + args[0], nil
	}}
	data, err := Snippets(o, []string{"b"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReadJSONLines[Snippet](data)
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Source() != "a" || got[0].Expr != "parseall:a" || got[0].Tokens != "tokens:a" || got[0].Green != "green:a" {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].Src != "" || got[1].Source() != "\xff" {
		t.Fatalf("binary source: %+v", got[1])
	}
	for _, failOn := range []string{"snippets", "tokens", "green", "parseall"} {
		o := fake{func(op string, args []string) (string, error) {
			if op == failOn {
				return "", errBoom
			}
			if op == "snippets" {
				return list, nil
			}
			return "x", nil
		}}
		if _, err := Snippets(o, nil); !errors.Is(err, errBoom) {
			t.Errorf("failure of %s not reported: %v", failOn, err)
		}
	}
	bad := fake{func(string, []string) (string, error) { return "!!!", nil }}
	if _, err := Snippets(bad, nil); err == nil {
		t.Error("bad base64 accepted")
	}
	if got := SplitExtra("#----\na\n#----\n\n#----\nb\nc\n"); len(got) != 2 || got[1] != "b\nc" {
		t.Errorf("SplitExtra = %q", got)
	}
}

func TestCorpus(t *testing.T) {
	o := fake{func(op string, args []string) (string, error) {
		switch op {
		case "corpus":
			return "a.jl\n\nb.jl", nil
		case "readfile":
			return "src of " + args[0], nil
		case "parsefile":
			return "expr of " + args[0], nil
		}
		return op + " of " + args[0], nil
	}}
	same := GoSide{
		Tokens: func(src []byte) string { return "tokens of " + string(src) },
		Green:  func(src []byte) string { return "other" },
		Expr:   func(_ []byte, f string) string { return "expr of " + f },
	}
	data, err := Corpus(o, same)
	if err != nil {
		t.Fatal(err)
	}
	es, _ := ReadJSONLines[CorpusEntry](data)
	if len(es) != 2 || !es[0].TokensParity || es[0].GreenParity || !es[0].Parity || es[0].Bytes != len("src of a.jl") ||
		es[0].SrcSHA256 != Digest("src of a.jl") {
		t.Fatalf("%+v", es)
	}
	data, _ = Corpus(o, GoSide{})
	es, _ = ReadJSONLines[CorpusEntry](data)
	if es[0].TokensParity || es[0].Parity {
		t.Fatal("parity without a Go side")
	}
	for _, failOn := range []string{"corpus", "readfile", "tokens", "green", "parsefile"} {
		o := fake{func(op string, args []string) (string, error) {
			if op == failOn {
				return "", errBoom
			}
			if op == "corpus" {
				return "a.jl", nil
			}
			return "x", nil
		}}
		if _, err := Corpus(o, same); !errors.Is(err, errBoom) {
			t.Errorf("failure of %s not reported: %v", failOn, err)
		}
	}
}

func TestPrograms(t *testing.T) {
	o := fake{func(op string, args []string) (string, error) { return op, nil }}
	data, err := Programs(o, []string{"p.jl"}, map[string]string{"p.jl": "1"})
	if err != nil {
		t.Fatal(err)
	}
	ps, _ := ReadJSONLines[Program](data)
	if len(ps) != 1 || ps[0].Parse != "parseall" || ps[0].Lower != "lower" || ps[0].Eval != "eval" {
		t.Fatalf("%+v", ps)
	}
	for _, failOn := range []string{"parseall", "lower", "eval"} {
		o := fake{func(op string, args []string) (string, error) {
			if op == failOn {
				return "", errBoom
			}
			return "x", nil
		}}
		if _, err := Programs(o, []string{"p.jl"}, map[string]string{"p.jl": "1"}); !errors.Is(err, errBoom) {
			t.Errorf("failure of %s not reported: %v", failOn, err)
		}
	}
	if _, err := ReadJSONLines[Program]([]byte("{")); err == nil {
		t.Error("bad JSON accepted")
	}
}
