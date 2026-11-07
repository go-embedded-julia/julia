package syntax

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// parseRuleCase is one call of Core._parse: the text, filename, line,
// 0-based offset and rule; Want is julia's answer, the serialised
// expression and the offset where it stopped, tab-separated.
type parseRuleCase struct {
	Text     string `json:"text"`
	Filename string `json:"filename"`
	Line     int    `json:"line"`
	Offset   int    `json:"offset"`
	Rule     string `json:"rule"`
	Want     string `json:"want"`
}

const parseRuleGolden = "parserule.jsonl"

// parseRuleInputs are Meta.parse's own examples (meta.jl:217-235), what
// lazy"..." parses after each $ (strings/lazy.jl:72), and the driver's
// edges: trivia before and after, the end of input, errors, a first line.
func parseRuleInputs() []parseRuleCase {
	c := func(text string, offset int, rule string) parseRuleCase {
		return parseRuleCase{Text: text, Filename: "none", Line: 1, Offset: offset, Rule: rule}
	}
	cases := []parseRuleCase{
		c("(α, β) = 3, 5", 0, "statement"),
		c("(α, β) = 3, 5", 0, "atom"),
		c("(α, β) = 3, 5", 15, "statement"),
		c("(α, β) = 3, 5", 10, "statement"),
		c("(α, β) = 3, 5", 10, "atom"),
		c("$n is too large to look up in the table; consider using `factorial(big($n))` instead", 1, "atom"),
		c("$(n+1) is $(f(x)[2])", 1, "atom"),
		c("$(n+1) is $(f(x)[2])", 11, "atom"),
		c("x = 1 + 2\ny", 0, "statement"),
		c("x = 1 + 2\ny", 10, "statement"),
		c("x = 1 + 2  # comment\n\ny", 0, "statement"),
		c("x; y\nz", 0, "statement"),
		c("  # only a comment\n  ", 0, "statement"),
		c("   ", 0, "atom"),
		c("", 0, "statement"),
		c("  #= c =# x + 1", 0, "atom"),
		c("x = ", 0, "statement"),
		c("1.0.2", 0, "statement"),
		c(")", 0, "atom"),
		c("function f(x)\n  x\n", 0, "statement"),
		c("a\nb = 2\n", 0, "all"),
		c("a\n)\n", 0, "all"),
	}
	lined := c("a +\n b", 0, "statement")
	lined.Line, lined.Filename = 41, "file.jl"
	return append(cases, lined)
}

func (c parseRuleCase) run() (string, error) {
	ex, last, err := ParseRule([]byte(c.Text), c.Filename, c.Line, c.Offset, c.Rule, DefaultLimits)
	if err != nil {
		return "", err
	}
	return expr.Serialize(ex) + "\t" + strconv.Itoa(last), nil
}

// TestParseRule replays julia 1.12.6's answers of Core._parse
// (JuliaSyntax.core_parser_hook), recorded with JLGO_ORACLE=1.
func TestParseRule(t *testing.T) {
	path := filepath.Join("..", "testdata", "golden", parseRuleGolden)
	if live {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		var b strings.Builder
		for _, c := range parseRuleInputs() {
			c.Want, err = p.Call("parserule", c.Text, c.Filename, strconv.Itoa(c.Line), strconv.Itoa(c.Offset), c.Rule)
			if err != nil {
				t.Fatalf("%q: %v", c.Text, err)
			}
			line, _ := json.Marshal(c)
			b.Write(line)
			b.WriteByte('\n')
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := readGolden[parseRuleCase](t, parseRuleGolden)
	if len(cases) != len(parseRuleInputs()) {
		t.Fatalf("%d golden answers for %d inputs: regenerate with JLGO_ORACLE=1", len(cases), len(parseRuleInputs()))
	}
	for _, c := range cases {
		got, err := c.run()
		if err != nil || got != c.Want {
			t.Errorf("%s of %q from %d:\n got %s %v\nwant %s", c.Rule, c.Text, c.Offset, got, err, c.Want)
		}
	}
}

// A rule Core._parse does not know, and an input over the limits, are
// errors rather than parses.
func TestParseRuleRefusals(t *testing.T) {
	if _, _, err := ParseRule([]byte("x"), "none", 1, 0, "toplevel", DefaultLimits); err == nil {
		t.Error("an unknown rule")
	}
	lim := DefaultLimits
	lim.MaxBytes = 1
	if _, _, err := ParseRule([]byte("xy"), "none", 1, 0, "atom", lim); !errors.Is(err, ErrLimit) {
		t.Errorf("over the byte limit: %v", err)
	}
}
