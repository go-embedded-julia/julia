package lower

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The femtolisp oracle: functions of the front end called on
// S-expressions julia's own values cannot spell (a #f where julia has
// (false), a malformed lambda), in `julia --lisp` and in Go, compared
// as printed. A femtolisp error and a Go panic both print as ERROR.

// lispFunc is one front-end function: how femtolisp calls it on the
// quoted arguments, and the Go port's equivalent.
type lispFunc struct {
	scheme string // a format with one %s per argument
	port   func(lw *lowerer, args []any) any
}

var lispFuncs = map[string]lispFunc{
	"expand-toplevel-expr": {"(expand-toplevel-expr %s 'none 0)", func(lw *lowerer, a []any) any {
		return lw.expandToplevelExpr(a[0], sym("none"), 0)
	}},
	"julia-expand-macroscope": {"(julia-expand-macroscope %s)", func(lw *lowerer, a []any) any {
		return lw.expandMacroscope(a[0])
	}},
	"resolve-expansion-vars": {"(resolve-expansion-vars %s %s '())", func(lw *lowerer, a []any) any {
		return lw.resolveWithNewEnv(a[0], nil, a[1], nil, nil, false, true)
	}},
	"vars-introduced": {"(let ((r (pattern-expand1 vars-introduced-by-patterns %s))) (if (and (pair? r) (eq? (car r) 'varlist)) r 'none))",
		func(lw *lowerer, a []any) any {
			v, ok := lw.introducedVars(a[0])
			if !ok {
				return sym("none")
			}
			return cons(sym("varlist"), v)
		}},
	"keywords-introduced-by": {"(keywords-introduced-by %s)", func(lw *lowerer, a []any) any { return lw.keywordsIntroducedBy(a[0]) }},
	"find-declared-vars-in-expansion": {"(find-declared-vars-in-expansion %s %s)", func(_ *lowerer, a []any) any {
		return findDeclaredVarsInExpansion(a[0], a[1].(sym), true)
	}},
	"find-assigned-vars-in-expansion": {"(find-assigned-vars-in-expansion %s)", func(_ *lowerer, a []any) any {
		return findAssignedVarsInExpansion(a[0], true)
	}},
	"try-arg-name": {"(try-arg-name %s %s)", func(_ *lowerer, a []any) any { return tryArgName(a[0], truthy(a[1])) }},
	"let-decl-var":  {"(let-decl-var %s)", func(_ *lowerer, a []any) any { return letDeclVar(a[0]) }},
	"decl-var*":     {"(decl-var* %s)", func(_ *lowerer, a []any) any { return declVarStar(a[0]) }},
	"typevar-names": {"(typevar-names %s)", func(_ *lowerer, a []any) any { return typevarNames(asList(a[0])) }},
}

// lispCases are calls: a function of lispFuncs and its arguments, in
// femtolisp's reader syntax (each argument is quoted).
var lispCases = [][]string{
	{"vars-introduced", "(try a #f b)"},
	{"vars-introduced", "(try a #f b c)"},
	{"vars-introduced", "(function (where f T) (block))"},
	{"vars-introduced", "(function (where (|::| (call f x) T) S) (block))"},
	{"vars-introduced", "(generator)"},
	{"vars-introduced", "(generator x)"},
	{"vars-introduced", "(let (block (escape (|::| x Int)) (|::| y)) 1)"},
	{"vars-introduced", "(let (block (= (hygienic-scope a m) 1)) 1)"},
	{"typevar-names", "(T (<: S 1) 1 (comparison A <: T >: B) (var-bounds V #f #f))"},
	{"let-decl-var", "(hygienic-scope (escape (|::| x Int)) m)"},
	{"let-decl-var", "(|::| Int)"},
	{"let-decl-var", "(call f)"},
	{"decl-var*", "(where (curly (call f x) T) S)"},
	{"try-arg-name", "(escape x)", "#t"},
	{"try-arg-name", "(tuple (escape x) y (meta nospecialize (escape z)))", "#t"},
	{"try-arg-name", "(tuple (escape x) y (meta nospecialize z) (meta foo w))", "#f"},
	{"try-arg-name", "(|::| T)", "#f"},
	{"try-arg-name", "1", "#f"},
	{"try-arg-name", "(call f)", "#f"},
	{"find-declared-vars-in-expansion", "(block (local x) (hygienic-scope (block (local y) (escape (local z)) (hygienic-scope (escape (escape (local w))) m)) m) (quote (local q)) (lambda (local l)))", "local"},
	{"find-declared-vars-in-expansion", "(block (hygienic-scope (block (lambda () (escape (local a))) (module (escape (local b))) (toplevel (escape (local c))) (quote (escape (local d))) 1) m))", "local"},
	{"find-assigned-vars-in-expansion", "(block (= x 1) (hygienic-scope (block (= y 1) (escape (= z 1))) m) (function (call f) (= a 1)) (tuple (= b 1) c))"},
	{"find-assigned-vars-in-expansion", "(block (function f) (function (tuple a b) 1) (function (call g x) 1) (= (call h x) 1) (-> x 1) (= (tuple p (escape q)) 1))"},
	{"resolve-expansion-vars", "(block (= x 1) (escape (escape y)) (begin end ccall cglobal _ __) (ssavalue 1) (symboliclabel a) (symbolicgoto a) (macrocall m) (meta inline) (using (escape A)) (line 1))", "m"},
	{"resolve-expansion-vars", "(block (global (escape x) (= (escape y) 1) (tuple (escape a) b) (|::| (escape c) Int) (kw (escape d) 1) (call f)) (toplevel (line 1 f) (= z 1) (line 2 (null))))", "m"},
	{"resolve-expansion-vars", "(block (parameters x (escape y) (kw z 1)) (kw a) (kw (|::| Int) 1) (kw (|::| b Int) 1) (call f (kw (|::| c Int) 2) (kw d 3) (parameters e)))", "m"},
	{"resolve-expansion-vars", "(block (-> (where (tuple x) T) x))", "m"},
	{"resolve-expansion-vars", "(block (-> (escape x) x) (-> x x) (-> (tuple x) x) (-> (call f x) x))", "m"},
	{"resolve-expansion-vars", "(block (struct #f S (block a (escape (|::| b Int)) (const (escape c)) (atomic d) (escape (escape (|::| e T))) (= f 1))))", "m"},
	{"resolve-expansion-vars", "(block (let (block (= x 1) y (escape z) (= (escape w) 2)) x) (for (escape (block (= i 1) (= j 2))) i) (for (= k 1) k) (generator x (filter c (= x r))) (generator y (= y s)))", "m"},
	{"resolve-expansion-vars", "(block (hygienic-scope (block (= p 1) (escape (= q 1)) (toplevel (= r 1))) n (line 3 g)) (tuple (= s 1) t))", "m"},
	{"julia-expand-macroscope", "(block (hygienic-scope (block (symboliclabel a) (symbolicgoto a) (symbolicgoto 1) (escape (symbolicgoto b)) (hygienic-scope (block (symboliclabel a) (escape (symboliclabel a))) n)) m) (symbolicgoto c) (quote (symbolicgoto d)))"},
	{"julia-expand-macroscope", "(block (escape (hygienic-scope (= x 1) m)) (inert (hygienic-scope 1 m)) (module (hygienic-scope 1 m)) (toplevel (hygienic-scope 1 m)))"},
	{"keywords-introduced-by", "(function (where (call (curly f T) (parameters (kw k 1) (escape j) (... r)) x) S) (block))"},
	{"keywords-introduced-by", "(= (|::| (call f (parameters k)) T) 1)"},
	{"keywords-introduced-by", "(= (where (call f (parameters k)) T) 1)"},
	{"keywords-introduced-by", "(= (call (curly f T) (parameters k)) 1)"},
	{"keywords-introduced-by", "(function (where (tuple x) T) 1)"},
	{"expand-toplevel-expr", "(block (lambda))"},
	{"expand-toplevel-expr", "(block (= x (lambda (a) () (scope-block (block a)))))"},
	{"expand-toplevel-expr", "(block (const x y z))"},
	{"expand-toplevel-expr", "(block (with-static-parameters (= x 1) T))"},
	{"expand-toplevel-expr", "(block (call f (escape x)))"},
	{"expand-toplevel-expr", "(block (incomplete \"x\"))"},
	{"expand-toplevel-expr", "(block (error \"x\"))"},
	{"expand-toplevel-expr", "(block (line 1) (call f (error \"incomplete: x\")))"},
}

const lispGolden = "lisp.jsonl"

type lispCase struct {
	Call []string `json:"call"`
	Out  string   `json:"out"`
}

// lispSer prints femtolisp values as schemeText prints Go's.
const lispSer = `(define (ser x)
  (cond ((and (pair? x) (list? x)) (string "(" (string.join (map ser x) " ") ")"))
        ((pair? x) (string "(" (ser (car x)) " . " (ser (cdr x)) ")"))
        ((null? x) "()")
        ((symbol? x) (string "|" x "|"))
        ((number? x) (number->string x))
        ((string? x) (string "\"" x "\""))
        ((eq? x #t) "#t")
        ((eq? x #f) "#f")
        (else (string x))))
`

func lispCall(c []string) string {
	f, ok := lispFuncs[c[0]]
	if !ok {
		panic("no lisp function " + c[0])
	}
	args := make([]any, len(c)-1)
	for i, a := range c[1:] {
		args[i] = "'" + a
	}
	return fmt.Sprintf(f.scheme, args...)
}

// TestLispGolden (JLGO_ORACLE=1 JLGO_REGEN=1) runs every case in
// femtolisp.
func TestLispGolden(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") == "" || os.Getenv("JLGO_REGEN") == "" {
		t.Skip("needs JLGO_ORACLE=1 and JLGO_REGEN=1")
	}
	const start, end = "\x01BEGIN\x01", "\x01END\x01"
	var prog strings.Builder
	prog.WriteString(lispSer)
	for _, c := range lispCases {
		prog.WriteString("(begin (display \"" + start + "\")(display (trycatch (ser " + lispCall(c) + ") (lambda (e) \"ERROR\")))(display \"" + end + "\"))\n")
	}
	cmd := exec.Command("pkgx", "julia@1.12.6", "--lisp")
	cmd.Stdin = strings.NewReader(prog.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	chunks := strings.Split(string(out), start)[1:]
	if len(chunks) != len(lispCases) {
		t.Fatalf("%d answers for %d cases", len(chunks), len(lispCases))
	}
	cases := make([]lispCase, len(lispCases))
	for i, c := range lispCases {
		text, _, ok := strings.Cut(chunks[i], end)
		if !ok {
			t.Fatalf("%v: no end marker", c)
		}
		cases[i] = lispCase{c, text}
	}
	writeJSONLines(t, filepath.Join("..", "testdata", "golden", lispGolden), cases)
}

// TestLisp calls the Go port on every recorded case.
func TestLisp(t *testing.T) {
	for _, c := range readGolden[lispCase](t, lispGolden) {
		got := lispPort(t, c.Call)
		if renumberText(got) != renumberText(c.Out) {
			t.Errorf("%v:\n  got  %s\n  want %s", c.Call, got, c.Out)
		}
	}
}

func lispPort(t *testing.T, c []string) (out string) {
	f := lispFuncs[c[0]]
	args := make([]any, len(c)-1)
	for i, a := range c[1:] {
		v, rest, err := readSx(a)
		if err != nil || strings.TrimSpace(rest) != "" {
			t.Fatalf("%v: %v %q", c, err, rest)
		}
		args[i] = v
	}
	defer func() {
		if r := recover(); r != nil {
			out = "ERROR"
		}
	}()
	lw := &lowerer{st: NewState()}
	return schemeText(f.port(lw, args))
}

var gensymText = regexp.MustCompile(`#s?[0-9]+`)

// renumberText renumbers the front end's gensyms of a printed value by
// first appearance: the two sides' counters differ.
func renumberText(s string) string {
	ids := map[string]string{}
	return gensymText.ReplaceAllStringFunc(s, func(g string) string {
		if n, ok := ids[g]; ok {
			return n
		}
		n := "#g" + strconv.Itoa(len(ids)+1)
		ids[g] = n
		return n
	})
}

// readSx reads one S-expression in femtolisp's reader syntax: lists,
// integers, strings, #t, #f, and symbols (|quoted| or bare).
func readSx(s string) (any, string, error) {
	s = strings.TrimLeft(s, " \t\n")
	if s == "" {
		return nil, s, fmt.Errorf("end of input")
	}
	switch s[0] {
	case '(':
		var l []any
		s = s[1:]
		for {
			s = strings.TrimLeft(s, " \t\n")
			if s == "" {
				return nil, s, fmt.Errorf("unclosed list")
			}
			if s[0] == ')' {
				if l == nil {
					l = []any{}
				}
				return l, s[1:], nil
			}
			v, rest, err := readSx(s)
			if err != nil {
				return nil, rest, err
			}
			l = append(l, v)
			s = rest
		}
	case '"':
		i := strings.IndexByte(s[1:], '"')
		if i < 0 {
			return nil, s, fmt.Errorf("unclosed string")
		}
		return sstr(s[1 : 1+i]), s[2+i:], nil
	case '|':
		i := strings.IndexByte(s[1:], '|')
		if i < 0 {
			return nil, s, fmt.Errorf("unclosed symbol")
		}
		return sym(s[1 : 1+i]), s[2+i:], nil
	}
	j := strings.IndexAny(s, " \t\n()")
	if j < 0 {
		j = len(s)
	}
	tok := s[:j]
	switch tok {
	case "#t":
		return true, s[j:], nil
	case "#f":
		return false, s[j:], nil
	}
	if n, err := strconv.ParseInt(tok, 10, 64); err == nil {
		return n, s[j:], nil
	}
	return sym(tok), s[j:], nil
}
