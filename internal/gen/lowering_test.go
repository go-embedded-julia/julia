package gen

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func rec(tag, in, out string) string {
	enc := base64.StdEncoding.EncodeToString
	return tag + "\t" + enc([]byte(in)) + "\t" + enc([]byte(out)) + "\n"
}

func TestParseLoweringForms(t *testing.T) {
	forms, err := parseLoweringForms(rec("F", "a", "b") + "\n" + rec("E", "c", "d"))
	if err != nil || len(forms) != 2 || forms[0] != (LoweringForm{"F", "a", "b"}) || forms[1].Tag != "E" {
		t.Fatalf("%v %v", forms, err)
	}
	for _, bad := range []string{"F\tYQ==", "X\tYQ==\tYQ==", "F\t!!\tYQ==", "F\tYQ==\t!!"} {
		if _, err := parseLoweringForms(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func upper(s string) (string, error) {
	if strings.Contains(s, "bad") {
		return "", errBoom
	}
	return strings.ToUpper(s), nil
}

func TestLoweringSnippets(t *testing.T) {
	answer := rec("F", "x", "y") + rec("E", "m", "msg")
	o := fake{func(op string, args []string) (string, error) {
		if op != "lowerforms" || args[1] != LoweringSnippetPath {
			t.Fatalf("%s %v", op, args)
		}
		if args[0] == "fail" {
			return "", errBoom
		}
		if args[0] == "badout" {
			return rec("F", "x", "bad"), nil
		}
		if args[0] == "garbage" {
			return "?", nil
		}
		return answer, nil
	}}
	data, err := LoweringSnippets(o, []string{"s"}, upper)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := ReadJSONLines[LoweringSnippet](data)
	if len(got) != 1 || got[0].Src != "s" || got[0].Forms[0] != (LoweringForm{"F", "X", "Y"}) ||
		got[0].Forms[1] != (LoweringForm{"E", "M", "msg"}) {
		t.Fatalf("%+v", got)
	}
	for _, s := range []string{"fail", "badout", "garbage"} {
		if _, err := LoweringSnippets(o, []string{s}, upper); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestLoweringCorpus(t *testing.T) {
	var fail string
	o := fake{func(op string, args []string) (string, error) {
		if op == fail {
			return "", errBoom
		}
		switch op {
		case "corpus":
			return "base/a.jl\n\nbase/b.jl", nil
		case "lowerfile":
			if fail == "garbage" {
				return "?", nil
			}
			if fail == "badout" {
				return rec("F", "x", "bad"), nil
			}
			return rec("F", "x", "y") + rec("F", "z", "w") + rec("E", "m", "msg") + rec("L", "n", "msg"), nil
		}
		return "", errors.New("unexpected " + op)
	}}
	side := LoweringSide{Canonical: upper, Lower: func(forms []LoweringForm) []bool {
		out := make([]bool, len(forms))
		out[0] = forms[0].Input == "x"
		return out
	}}
	data, err := LoweringCorpus(o, side)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := ReadJSONLines[LoweringCorpusEntry](data)
	want := LoweringCorpusEntry{Path: "base/a.jl", Forms: 2, Unexpanded: 1, LowerErrors: 1,
		Digests: ShortDigest("Y") + " " + ShortDigest("W"), Identical: 1}
	if len(got) != 2 || got[0] != want {
		t.Fatalf("%+v", got)
	}
	for _, f := range []string{"corpus", "lowerfile", "garbage", "badout"} {
		fail = f
		if _, err := LoweringCorpus(o, side); err == nil {
			t.Errorf("%s failure accepted", f)
		}
	}
}

func TestLoweringForms(t *testing.T) {
	o := fake{func(op string, args []string) (string, error) {
		if op == "lowerforms" {
			return "", errBoom
		}
		return rec("F", "a", "b"), nil
	}}
	if _, err := LoweringForms(o, "x", "f.jl"); err == nil {
		t.Fatal("failure accepted")
	}
	if f, err := LoweringFile(o, "f.jl"); err != nil || len(f) != 1 {
		t.Fatalf("%v %v", f, err)
	}
}
