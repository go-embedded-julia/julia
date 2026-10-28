package lower

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// deparseCases are serialised expressions whose deparse is compared with
// femtolisp's own (ast.scm), the text of lowering's error messages.
var deparseCases = []string{
	`:x`, `1`, `"s"`, `true`, `nothing`,
	`(:call :f :x)`, `(:call :"+" :a :b)`, `(:call :":" :a :b)`, `(:call :":" :a :b :c)`,
	`(:call (:"::" :f :T) :x)`, `(:call :":")`, `(:call :f (:parameters :k) :x)`,
	`(:call :f (:parameters :k))`, `(:ref :":" 1)`,
	`(:"." :a {Q :b})`, `(:"." :a (:copyast (:inert :b)))`, `(:"." :a :b)`, `(:"." :f)`,
	`(:"." :a {Q :".+"})`, `(:"..." :x)`, `(:"'" :x)`, `(:"&&" :a :b)`, `(:"=" :a 1)`,
	`(:"->" :x :y)`, `(:"<:" :a :b)`, `(:"<:" :a)`, `(:">:" :a :b)`, `(:"-->" :a :b)`, `(:"..." (:call :f))`,
	`(:as (:"." :A) :B)`, `(:"$" :x)`, `(:"$" (:call :f :x))`, `(:"$" (:tuple :a))`, `(:"&" :x)`,
	`(:"::" :x :T)`, `(:"::" :T)`, `(:comparison :a :"<" :b)`, `(:macrocall :@m nothing :x)`,
	`(:kw :a 1)`, `(:where :T :S)`, `(:where :T :S :U)`, `(:parameters :a :b)`,
	`(:tuple :a)`, `(:tuple :a :b)`, `(:tuple (:parameters :k) :a)`, `(:tuple (:parameters :k) :a :b)`,
	`(:ref :a 1)`, `(:curly :A :T)`, `(:vect 1 2)`, `(:vcat 1 2)`, `(:typed_vcat :T 1 2)`,
	`(:hcat 1 2)`, `(:typed_hcat :T 1 2)`, `(:ncat 2 1 2)`, `(:ncat 3 1)`, `(:typed_ncat :T 2 1 2)`,
	`(:row 1 2)`, `(:nrow 2 1 2)`, `(:braces 1 2)`, `(:bracescat 1 2)`,
	`(:string "a" :b (:call :f))`,
	`(:generator :x (:"=" :x :y))`, `(:generator :x (:"=" :x :y) (:"=" :z :w))`,
	`(:flatten (:generator (:generator :x (:"=" :x :y)) (:"=" :y :z)))`,
	`(:filter :c (:"=" :x :y))`, `(:comprehension (:generator :x (:"=" :x :y)))`,
	`(:typed_comprehension :T (:generator :x (:"=" :x :y)))`,
	`(:block :a :b)`, `(:block)`, `(:toplevel :a :b)`,
	`(:function (:call :f :x) (:block :x))`, `(:function :f)`, `(:macro (:call :m) (:block))`,
	`(:for (:"=" :i :r) (:block :i))`, `(:while :c (:block :x))`, `(:function (:call :f) :x)`,
	`(:return :x)`, `(:break)`, `(:continue)`,
	`(:if :c (:block :a))`, `(:if :c (:block :a) (:block :b))`,
	`(:if :c (:block :a) (:elseif (:block {L 1 :f} :d) (:block :b)))`,
	`(:if :c (:block :a) (:elseif (:block {L 1 :f} :d) (:block :b) (:block :e)))`,
	`(:let (:block (:"=" :a 1) :b) (:block :a))`,
	`(:try (:block :a) :e (:block :b))`, `(:try (:block :a) false (:block :b))`,
	`(:try (:block :a) false false (:block :f))`, `(:try (:block :a) :e (:block :b) false (:block :g))`,
	`(:try (:block :a) false false false)`,
	`(:do (:call :f :x) (:"->" (:tuple :y) (:block :y)))`, `(:do (:call :f) (:"->" (:tuple) (:block :y)))`,
	`(:struct true :S (:block :a))`, `(:struct false :S (:block))`, `(:abstract :A)`, `(:primitive :P 8)`,
	`(:module true :M (:block :a :b))`, `(:module false :M (:block))`,
	`(:import (:"." :A :b))`, `(:using (:"." :"." :A))`, `(:import (:":" (:"." :A) (:"." :b) (:"." :c)))`,
	`(:import :x)`, `(:global :a :b)`, `(:local :a)`, `(:export :a)`, `(:public :a)`, `(:const (:"=" :a 1))`,
	`(:top :x)`, `(:core :x)`, `(:ssavalue 1)`, `(:line 1)`, `(:line 1 :f)`, `(:copyast :x)`,
	`(:quote :x)`, `(:quote :"=x")`, `(:quote :":x")`, `(:quote (:call :f))`, `(:inert :x)`,
	`(:unknown 1 2)`,
	`(:function (:call :f) (:block (:if :c (:block :a)) (:try (:block :a) :e (:block :b) (:block :c)) (:let (:block) (:block :z))))`,
}

const deparseGolden = "deparse.jsonl"

type deparseCase struct {
	Input string `json:"input"`
	Text  string `json:"text"`
}

// TestDeparseGolden (JLGO_ORACLE=1 JLGO_REGEN=1) asks femtolisp, `julia
// --lisp` with julia's front end loaded, to deparse every case.
func TestDeparseGolden(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_REGEN") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_REGEN=1")
	}
	const start, end = "\x01BEGIN\x01", "\x01END\x01"
	var prog strings.Builder
	for _, c := range deparseCases {
		if _, err := expr.Parse(c); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		prog.WriteString("(begin (display \"" + start + "\")(display (deparse '" + schemeText(toSx(mustParse(t, c))) + "))(display \"" + end + "\"))\n")
	}
	cmd := exec.Command("pkgx", "julia@1.12.6", "--lisp")
	cmd.Stdin = strings.NewReader(prog.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	// the REPL echoes around the answers: each is between the markers
	chunks := strings.Split(string(out), start)[1:]
	if len(chunks) != len(deparseCases) {
		t.Fatalf("%d answers for %d cases", len(chunks), len(deparseCases))
	}
	cases := make([]deparseCase, len(deparseCases))
	for i, c := range deparseCases {
		text, _, ok := strings.Cut(chunks[i], end)
		if !ok {
			t.Fatalf("%s: no end marker", c)
		}
		cases[i] = deparseCase{c, text}
	}
	writeJSONLines(t, filepath.Join("..", "testdata", "golden", deparseGolden), cases)
}

// TestDeparse compares deparse with femtolisp's on every recorded case.
func TestDeparse(t *testing.T) {
	for _, c := range readGolden[deparseCase](t, deparseGolden) {
		if got := deparse(toSx(mustParse(t, c.Input))); got != c.Text {
			t.Errorf("%s: %q, want %q", c.Input, got, c.Text)
		}
	}
}
