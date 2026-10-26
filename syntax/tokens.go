package syntax

import (
	"strconv"
	"strings"
)

func itoa(i int) string { return strconv.Itoa(i) }

// Tokenize runs the raw lexer over src and returns its tokens, the final
// EndMarker included, as JuliaSyntax.Tokenize.tokenize does.
func Tokenize(src []byte) []RawToken {
	l := NewLexer(src, 0)
	var out []RawToken
	for {
		t := l.NextToken()
		out = append(out, t)
		if t.Kind == kEndMarker {
			return out
		}
	}
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// TokenDump prints the raw tokens of src in the oracle's `tokens` format:
// one line per token, "kind first last dotop suffix" with 0-based bytes.
func TokenDump(src []byte) string {
	var b strings.Builder
	for _, t := range Tokenize(src) {
		b.WriteString(t.Kind.String())
		b.WriteString(" " + itoa(t.StartByte) + " " + itoa(t.EndByte) + " " + b01(t.DotOp) + " " + b01(t.Suffix) + "\n")
	}
	return b.String()
}
