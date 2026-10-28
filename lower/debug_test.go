package lower

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// TestDebugLower (JLGO_DEBUG_INPUT=<serialised form>) lowers one form with
// port failures panicking, to see their stack. With JLGO_DEBUG_STAGE=expand
// (or scopes, vars, closure, linear), it prints the Go output of the passes
// up to that one, and femtolisp's, from `julia --lisp`, for comparison.
func TestDebugLower(t *testing.T) {
	src := os.Getenv("JLGO_DEBUG_INPUT")
	if src == "" {
		t.Skip("needs JLGO_DEBUG_INPUT")
	}
	in, err := expr.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	debugPanics = true
	defer func() { debugPanics = false }()
	if stage := os.Getenv("JLGO_DEBUG_STAGE"); stage != "" {
		lw := &lowerer{st: NewState()}
		lw.st.gensyms.reset()
		e := toSx(in)
		lno := firstLineno(e)
		ex := lw.expand0(lw.expandMacroscope(e), list(sLine, int64(0), sym("none")))
		var blno any
		if truthy(lno) {
			blno = lno
		}
		lam := list(sLambda, []any{}, []any{}, list(sym("scope-block"), blockify(ex, blno)))
		scheme := "(julia-expand0 '" + schemeText(toSx(in)) + " '(line 0 none))"
		goOut := any(lam)
		steps := []struct {
			name string
			f    func(any) any
			fl   string
		}{
			{"scopes", lw.resolveScopes, "resolve-scopes"},
			{"vars", lw.analyzeVariables, "analyze-variables!"},
			{"closure", lw.closureConvert, "closure-convert"},
			{"linear", lw.linearize, "linearize"},
		}
		var fs []func(any) any
		if stage != "expand" {
			scheme = "`(lambda () () (scope-block ,(blockify " + scheme + " #f)))"
			for _, s := range steps {
				fs = append(fs, s.f)
				scheme = "(" + s.fl + " " + scheme + ")"
				if s.name == stage {
					break
				}
			}
		}
		prog := schemeSer + "(display (ser " + scheme + "))(newline)\n"
		cmd := exec.Command("pkgx", "julia@1.12.6", "--lisp")
		cmd.Stdin = strings.NewReader(prog)
		out, _ := cmd.CombinedOutput()
		t.Logf("femtolisp:\n%s", out)
		if stage == "expand" {
			goOut = ex
		}
		for _, f := range fs {
			goOut = f(goOut)
		}
		t.Logf("go:\n%s", schemeText(goOut))
		return
	}
	out, err := NewState().Lower(in)
	t.Logf("%s\n%v", expr.Serialize(Canonical(out)), err)
}

// schemeSer prints femtolisp values in schemeText's format.
const schemeSer = `(define (ser x)
  (cond ((pair? x) (string "(" (string.join (map ser x) " ") ")"))
        ((null? x) "()")
        ((symbol? x) (string "|" x "|"))
        ((number? x) (number->string x))
        ((string? x) (string "\"" x "\""))
        ((eq? x #t) "#t")
        ((eq? x #f) "#f")
        (else (string x))))
`

// schemeText prints an S-expression in femtolisp's reader syntax.
func schemeText(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, len(x))
		for i, e := range x {
			parts[i] = schemeText(e)
		}
		return "(" + strings.Join(parts, " ") + ")"
	case envEntry:
		if l, ok := x.val.([]any); ok {
			return schemeText(cons(x.key, l))
		}
		return "(" + schemeText(x.key) + " . " + schemeText(x.val) + ")"
	case sym:
		return "|" + string(x) + "|"
	case int64:
		return strconv.FormatInt(x, 10)
	case sstr:
		return strconv.Quote(string(x))
	case string:
		return strconv.Quote(x)
	case bool:
		if x {
			return "#t"
		}
		return "#f"
	case nil:
		return "#f"
	}
	return "|" + expr.Serialize(unbox(v)) + "|"
}
