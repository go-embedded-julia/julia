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
	"try-arg-name":  {"(try-arg-name %s %s)", func(_ *lowerer, a []any) any { return tryArgName(a[0], truthy(a[1])) }},
	"let-decl-var":  {"(let-decl-var %s)", func(_ *lowerer, a []any) any { return letDeclVar(a[0]) }},
	"decl-var*":     {"(decl-var* %s)", func(_ *lowerer, a []any) any { return declVarStar(a[0]) }},
	"typevar-names": {"(typevar-names %s)", func(_ *lowerer, a []any) any { return typevarNames(asList(a[0])) }},
	"arg-type":      {"(arg-type %s)", func(_ *lowerer, a []any) any { return argType(a[0]) }},
	"undotop":       {"(undotop %s)", func(_ *lowerer, a []any) any { return undotop(a[0]) }},
	"flatten-ex":    {"(flatten-ex %s %s)", func(_ *lowerer, a []any) any { return flattenEx(a[0].(sym), a[1]) }},
	// strings print as symbols: ser does not escape them
	"deparse":    {"(symbol (deparse %s))", func(_ *lowerer, a []any) any { return sym(deparse(a[0])) }},
	"string":     {"(symbol (string %s))", func(_ *lowerer, a []any) any { return sym(symString(a[0])) }},
	"format-loc": {"(symbol (format-loc %s))", func(_ *lowerer, a []any) any { return sym(formatLoc(a[0])) }},
	"format-file-line": {"(symbol (format-file-line %s %s %s))", func(_ *lowerer, a []any) any {
		return sym(formatFileLine(a[0], a[1], truthy(a[2])))
	}},
	"strip-op-suffix": {"(strip-op-suffix %s)", func(_ *lowerer, a []any) any { return sym(stripOpSuffix(string(a[0].(sym)))) }},
	"something":       {"(something %s)", func(_ *lowerer, a []any) any { return something(asList(a[0])) }},
	"dots->vararg":    {"(dots->vararg %s)", func(_ *lowerer, a []any) any { return dotsToVararg(asList(a[0])) }},
	"replace-vars":    {"(replace-vars %s %s)", func(_ *lowerer, a []any) any { return replaceVars(a[0], asList(a[1])) }},
	// the renames as (old new) lists, consed into pairs on the Scheme side
	"replace-vars-pairs": {"(replace-vars %s (map (lambda (p) (cons (car p) (cadr p))) %s))", func(_ *lowerer, a []any) any {
		return replaceVarsPairs(a[0], mapL(car, asList(a[1])), mapL(cadr, asList(a[1])))
	}},
	"macroify-name": {"(macroify-name %s)", func(_ *lowerer, a []any) any { return macroifyName(a[0]) }},
	"ctor-sig":      {"(ctor-sig %s)", func(_ *lowerer, a []any) any { return ctorSig(a[0]) }},
	"expand-unionall-def": {"(expand-unionall-def %s %s %s)", func(lw *lowerer, a []any) any {
		return lw.expandUnionallDef(a[0], a[1], truthy(a[2]))
	}},
	// a table's iteration order: the hash, probing and growth of htable.inc
	"table-keys": {"(let ((t (table))) (for-each (lambda (k) (put! t k #t)) %s) (table.keys t))", func(_ *lowerer, a []any) any {
		t := newTable()
		for _, k := range asList(a[0]) {
			t.put(k, true)
		}
		return t.keysList()
	}},
	"error-wrap": {"(error-wrap (lambda () (expand-toplevel-expr %s 'none 0)))", func(lw *lowerer, a []any) any {
		return lw.errorWrap(func() any { return lw.expandToplevelExpr(a[0], sym("none"), 0) })
	}},
	"closure-convert":      {"(closure-convert %s)", func(lw *lowerer, a []any) any { return lw.closureConvert(a[0]) }},
	"linearize":            {"(linearize %s)", func(lw *lowerer, a []any) any { return lw.linearize(a[0]) }},
	"compact-and-renumber": {"(compact-and-renumber %s 'none 0)", func(lw *lowerer, a []any) any { return lw.compactAndRenumber(a[0], sym("none"), int64(0)) }},
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
	{"expand-toplevel-expr", "(block (= f (lambda (x) (block x))))"},
	{"expand-toplevel-expr", "(block (string (string \"a\") b))"},
	{"expand-toplevel-expr", "(block (break-block foo (block (break foo))))"},
	{"expand-toplevel-expr", "(block (scope-block (block (local x) (assign-const-if-global x))))"},
	{"resolve-expansion-vars", "(block x (= y 1))", "#f"},
	{"arg-type", "1"},
	{"arg-type", "(... x 2)"},
	{"arg-type", "(|::| (call f) T)"},
	{"arg-type", "(meta foo x)"},
	{"arg-type", "(meta nospecialize (|::| x T))"},
	{"arg-type", "(kw (|::| x T) 1)"},
	{"arg-type", "(call f)"},
	{"undotop", "|.+|"},
	{"undotop", "x"},
	{"undotop", "(globalref m |.*|)"},
	{"flatten-ex", "block", "x"},
	{"flatten-ex", "block", "(block a (block b (block c)) d)"},
	{"deparse", "\"s\""},
	{"deparse", "#t"},
	{"deparse", "#f"},
	{"deparse", "()"},
	{"deparse", "(false)"},
	{"deparse", "(string \"a\" x (call f))"},
	{"string", "#t"},
	{"string", "#f"},
	{"string", "()"},
	{"string", "x"},
	{"string", "\"s\""},
	{"string", "12"},
	{"string", "(a (b c))"},
	{"format-loc", "(foo 1)"},
	{"format-loc", "(line 3)"},
	{"format-loc", "(line 3 f)"},
	{"format-loc", "(line 0 f)"},
	{"format-loc", "#f"},
	{"format-loc", "()"},
	{"format-file-line", "f", "3", "#t"},
	{"format-file-line", "f", "3", "#f"},
	{"format-file-line", "none", "3", "#t"},
	{"strip-op-suffix", "|+′|"},
	{"strip-op-suffix", "|′|"},
	{"strip-op-suffix", "|+|"},
	{"strip-op-suffix", "|+₁′|"},
	{"something", "((null) (null))"},
	{"something", "((null) 1 2)"},
	{"dots->vararg", "()"},
	{"dots->vararg", "(a (... b))"},
	{"dots->vararg", "(a b)"},
	{"replace-vars", "(call f a (quote a) (-> a a) (function a) (scope-block a) b)", "((a 1) (b c d))"},
	{"replace-vars-pairs", "(call f a (quote a) (-> a a) (function a) (scope-block a) b)", "((a x) (b y))"},
	{"macroify-name", "(call f)"},
	{"macroify-name", "(quote m)"},
	{"macroify-name", "(inert m)"},
	{"macroify-name", "(|.| A (quote m))"},
	{"macroify-name", "(globalref M m)"},
	{"ctor-sig", "(call (|::| x T) a)"},
	{"ctor-sig", "(call (|::| |#ctor-self#| T) a)"},
	{"ctor-sig", "(|::| (block) T)"},
	{"ctor-sig", "(where (call S x) T)"},
	{"ctor-sig", "(foo)"},
	{"expand-unionall-def", "A", "(curly B T)", "#t"},
	{"expand-unionall-def", "(curly A)", "T", "#f"},
	{"expand-unionall-def", "(curly A T)", "(curly B T)", "#f"},
	{"table-keys", "(#t #f \"a\" \"bc\" x 1 (a b) \"a\" #t)"},
	{"table-keys", "(a1 a2 a3 a4 a5 a6 a7 a8 a9 b1 b2 b3 b4 b5 b6 b7 b8 b9 c1 c2 c3 c4 c5 c6 c7 c8 c9 d1 d2 d3 d4 d5 d6 d7 d8 d9 \"s1\" \"s2\" #t 1 2 3)"},
	// closure conversion of crafted lambdas: (lambda args (vars captured nssa sparams) body)
	{"closure-convert", "x"},
	{"closure-convert", "(block x (call f y))"},
	{"closure-convert", "(lambda () (() ((x (core Any) 0)) 0 ()) (block (= x 1)))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (= y 1)))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (= (call f) 1)))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (scope-block (block 1))))"},
	{"closure-convert", "(lambda () (((x (core Any) 4)) () 0 ()) (block (local x) x))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (const x)))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (atomic x) (isdefined anon) (isdefined (ssavalue 1)) (isdefined y)))"},
	{"closure-convert", "(lambda () (() () 0 ()) (block (with-static-parameters x T)))"},
	// linearization of crafted lambdas
	{"linearize", "x"},
	{"linearize", "(block (lambda () (() () 0 ()) (block 1)))"},
	{"linearize", "(lambda () (() () 0 ()) (block #f))"},
	{"linearize", "(lambda (x) (((x (core Any) 0)) () 0 ()) (block (toplevel-only struct_type) (null)))"},
	{"linearize", "(lambda (x) (((x (core Any) 0)) () 0 ()) (block (toplevel-only method) (null)))"},
	{"linearize", "(lambda () (() () 0 ()) (block (if (return 1) 2)))"},
	{"linearize", "(lambda () (() () 0 ()) (block (_while (true) (null))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (_do_while (null) (true))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (globaldecl x)))"},
	{"linearize", "(lambda () (() () 0 ()) (block (local-def x) 1))"},
	{"linearize", "(lambda () (() () 0 ()) (block (atomic x) 1))"},
	{"linearize", "(lambda () (() () 0 ()) (block (method f (call (core svec)) (lambda (x) (((x (core Any) 0)) () 0 ()) (block x)))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (method f)))"},
	{"linearize", "(lambda () (() () 0 ()) (block (call g (method f))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (lambda () (() () 0 ()) (block 1))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (lambda () (() () 0 ()) (block 1)) 2))"},
	{"linearize", "(lambda (x) (((x (core Any) 0)) () 0 ()) (block (line 3) (method f)))"},
	{"linearize", "(lambda (x) (((x (core Any) 0)) () 0 ()) (block (method f)))"},
	{"linearize", "(lambda () (() () 0 ()) (block (line 3) _))"},
	{"linearize", "(lambda () (() () 0 ()) (block (line 1 a) (block (line 2 b) (core x))))"},
	{"linearize", "(lambda () (() () 0 ()) (block (line 1 a) (symboliclabel L) (block (line 2 b) (symbolicgoto L))))"},
	{"closure-convert", "(lambda () (() ((x (block (= (ssavalue 1) (call f)) (call g (ssavalue 1) (ssavalue 2))) 3)) 0 ()) (block x))"},
	{"error-wrap", "(block (error \"incomplete: x\"))"},
	{"error-wrap", "(block (call f (error \"y\")))"},
	{"error-wrap", "(block (lambda))"},
	{"error-wrap", "(block (call f (... x)))"},
	{"compact-and-renumber", "x"},
	{"compact-and-renumber", "(block x y)"},
	{"compact-and-renumber", "(lambda () (() () 0 ()) (block (return |#unused#|)))"},
	{"compact-and-renumber", "(lambda () (() () 0 ()) (block (return (ssavalue 5))))"},
}

const lispGolden = "lisp.jsonl"

// lispCase is a recorded call. SSA is femtolisp's ssavalue counter before
// the call (make-ssavalue never resets it), so that the port numbers the
// SSA values of the passes that do not renumber them as femtolisp did.
type lispCase struct {
	Call []string `json:"call"`
	SSA  int      `json:"ssa"`
	Out  string   `json:"out"`
}

// lispSer prints femtolisp values as schemeText prints Go's, and an error
// (error "msg") raised by the front end as lispErrorText prints Go's.
const lispSer = `(define (errtext e)
  (if (and (pair? e) (eq? (car e) 'error) (pair? (cdr e)) (string? (cadr e)) (null? (cddr e)))
      (string "ERROR: " (cadr e))
      "ERROR"))
(define (ser x)
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
	const start, sep, end = "\x01BEGIN\x01", "\x01SEP\x01", "\x01END\x01"
	var prog strings.Builder
	prog.WriteString(lispSer)
	for _, c := range lispCases {
		prog.WriteString("(begin (display \"" + start + "\")(display (cadr (make-ssavalue)))(display \"" + sep +
			"\")(display (trycatch (ser " + lispCall(c) + ") (lambda (e) (errtext e))))(display \"" + end + "\"))\n")
	}
	cmd := exec.Command("pkgx", "julia@1.12.6", "--lisp")
	cmd.Stdin = strings.NewReader(prog.String())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	chunks := strings.Split(string(out), start)[1:]
	if len(chunks) != len(lispCases) {
		t.Fatalf("%d answers for %d cases: %s", len(chunks), len(lispCases), out[max(0, len(out)-2000):])
	}
	cases := make([]lispCase, len(lispCases))
	for i, c := range lispCases {
		text, _, ok := strings.Cut(chunks[i], end)
		if !ok {
			t.Fatalf("%v: no end marker", c)
		}
		n, text, ok := strings.Cut(text, sep)
		ssa, err := strconv.Atoi(n)
		if !ok || err != nil {
			t.Fatalf("%v: no ssavalue counter in %q", c, chunks[i])
		}
		cases[i] = lispCase{c, ssa, text}
	}
	writeJSONLines(t, filepath.Join("..", "testdata", "golden", lispGolden), cases)
}

// TestLisp calls the Go port on every recorded case.
func TestLisp(t *testing.T) {
	for _, c := range readGolden[lispCase](t, lispGolden) {
		got := lispPort(t, c.Call, c.SSA)
		if renumberText(got) != renumberText(c.Out) {
			t.Errorf("%v:\n  got  %s\n  want %s", c.Call, got, c.Out)
		}
	}
}

func lispPort(t *testing.T, c []string, ssa int) (out string) {
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
			out = lispErrorText(r)
		}
	}()
	lw := &lowerer{st: NewState()}
	lw.st.ssaCounter = ssa + 1
	return schemeText(f.port(lw, args))
}

// lispErrorText is errtext of lispSer for a panic of the port: the message
// of a lowering error, and ERROR for the others (femtolisp's own errors).
func lispErrorText(r any) string {
	switch x := r.(type) {
	case loweringError:
		return "ERROR: " + string(x)
	case loweringErrorValue:
		// (error msg) with a femtolisp string the input spelled
		if msg, ok := x.msg.(sstr); ok {
			return "ERROR: " + string(msg)
		}
	}
	return "ERROR"
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
