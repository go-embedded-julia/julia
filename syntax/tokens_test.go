package syntax

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
)

func init() {
	levels = append(levels, level{
		name:   "tokens",
		run:    func(_ testing.TB, src []byte, _ string) string { return TokenDump(src) },
		golden: func(s gen.Snippet) string { return s.Tokens },
		digest: func(e gen.CorpusEntry) (string, bool) { return e.TokensSHA256, e.TokensParity },
	})
}

func TestKindNames(t *testing.T) {
	if K("call").String() != "call" || Kind(0xffff).String() != "Kind(65535)" {
		t.Fatal("Kind.String")
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "unknown Kind name") {
			t.Fatalf("K(bogus) recovered %v", r)
		}
	}()
	K("no such kind")
}

// FuzzLex checks that the lexer's tokens tile any input exactly.
func FuzzLex(f *testing.F) {
	for _, sn := range readGolden[gen.Snippet](f, "snippets.jsonl") {
		f.Add([]byte(sn.Source()))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		pos := 0
		toks := Tokenize(src)
		for _, tok := range toks {
			if tok.StartByte != pos || tok.EndByte < tok.StartByte-1 {
				t.Fatalf("token %v at %d..%d, expected start %d", tok.Kind, tok.StartByte, tok.EndByte, pos)
			}
			pos = tok.EndByte + 1
		}
		if pos != len(src) || toks[len(toks)-1].Kind != kEndMarker {
			t.Fatalf("tokens cover %d of %d bytes", pos, len(src))
		}
	})
}
