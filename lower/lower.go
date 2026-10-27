// Package lower turns macro-expanded Julia syntax trees into julia's lowered
// form: the Expr(:thunk, CodeInfo) that Meta.lower returns.
//
// The reference is the front end julia 1.12.6 uses by default, femtolisp's
// src/julia-syntax.scm (with ast.scm, jlfrontend.scm and the C glue of
// src/ast.c and src/method.c): Meta.lower is that code, so porting it is
// how the output can be identical. The port works, as the Scheme code
// does, on S-expressions (sx.go); its passes are the pipeline of
// julia-expand1:
//
//	expand-forms        desugaring             desugar*.go
//	resolve-scopes      scope analysis         scope.go
//	analyze-variables!  captured, assigned     vars.go
//	closure-convert     closures to structs    closure.go
//	linearize           statements, labels     linear.go
//	compact-and-renumber                       renumber.go
//
// Macro expansion runs user code and stays with the caller (the oracle's
// goldens record julia's expansion as the input).
package lower

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// ErrUnsupported is wrapped by the errors of forms whose lowering is not
// ported yet.
var ErrUnsupported = errors.New("lower: not ported yet")

// ErrInternal is wrapped by the errors of broken invariants of the port.
var ErrInternal = errors.New("lower: internal error")

// debugPanics lets a test see the stack of a port failure.
var debugPanics = false

type unsupported string

func unsupportedf(format string, args ...any) unsupported {
	return unsupported(fmt.Sprintf(format, args...))
}

// State is the state lowering reads and updates across the top-level
// forms of a file: the module's counter and bindings (closure names come
// from them, src/ast.c: fl_module_unique_name) and the front end's gensym
// counters.
type State struct {
	moduleCounter int
	bindings      map[string]bool
	gensyms       gensyms
	ssaCounter    int
	mod           expr.Module
}

// NewState returns the state of a fresh module, Main.L as the oracle
// creates it.
func NewState() *State {
	return &State{bindings: map[string]bool{}, mod: expr.Module{Path: []expr.Symbol{"Main", "L"}}}
}

// lowerer holds what one lowering reads and writes.
type lowerer struct {
	st         *State
	desugarLoc any // *current-desugar-loc*
}

// Lower is Meta.lower(m, ex) for a macro-expanded top-level form ex
// (jl_expand_with_loc_warn of src/ast.c with file "none" and line 0).
func (st *State) Lower(ex any) (out any, err error) {
	defer func() {
		if r := recover(); r != nil {
			if debugPanics {
				panic(r)
			}
			switch x := r.(type) {
			case unsupported:
				out, err = nil, fmt.Errorf("%w: %s", ErrUnsupported, string(x))
			case internalError:
				out, err = nil, fmt.Errorf("%w: %s", ErrInternal, string(x))
			case runtime.Error:
				// a broken invariant the port did not check by name
				out, err = nil, fmt.Errorf("%w: %v", ErrInternal, x)
			default:
				panic(r)
			}
		}
	}()
	lw := &lowerer{st: st}
	res := lw.errorWrap(func() any { return lw.expandToplevelExpr(toSxChecked(ex), sym("none"), 0) })
	c := &converter{mod: st.mod}
	return c.fromSx(res), nil
}

// toSxChecked is julia_to_scm; its errors become an error result.
func toSxChecked(ex any) (out any) {
	return toSx(ex)
}

// errorWrap is error-wrap (jlfrontend.scm): a lowering error becomes the
// expression (error msg), or (incomplete msg).
func (lw *lowerer) errorWrap(f func() any) (out any) {
	defer func() {
		if r := recover(); r != nil {
			msg, ok := r.(loweringError)
			if !ok {
				panic(r)
			}
			if strings.HasPrefix(string(msg), "incomplete:") {
				out = list(sym("incomplete"), sstr(msg))
			} else {
				out = list(sym("error"), sstr(msg))
			}
		}
	}()
	return f()
}

// firstLineno is first-lineno.
func firstLineno(blk any) any {
	switch {
	case !isPair(blk):
		return false
	case car(blk) == sLine:
		return blk
	case car(blk) == sym("hygienic-scope") && isPair(cdddr(blk)) && is(cadddr(blk), sLine):
		return cadddr(blk)
	case inList(car(blk), sym("escape"), sym("hygienic-scope")):
		return firstLineno(cadr(blk))
	case inList(car(blk), sym("toplevel"), sBlock):
		for _, x := range cdr(blk) {
			if elt := firstLineno(x); truthy(elt) {
				return elt
			}
		}
	}
	return false
}

// toplevelOnly is toplevel-only-expr?.
func toplevelOnly(e any) bool {
	if !isPair(e) {
		return false
	}
	switch car(e) {
	case sym("toplevel"), sLine, sym("module"), sym("export"), sym("public"), sym("error"), sym("incomplete"):
		return true
	case sym("global"), sym("const"):
		return everyL(isSymbol, cdr(e))
	}
	return false
}

// expandToplevelExpr is expand-toplevel-expr.
func (lw *lowerer) expandToplevelExpr(e any, file sym, line int64) any {
	if isAtomS(e) || toplevelOnly(e) {
		if underscoreSymbol(e) {
			errorf("all-underscore identifiers are write-only and their values cannot be used in expressions")
		}
		return e
	}
	lw.st.gensyms.reset()
	return lw.expandToplevelExpr_(e, file, line)
}

// expandToplevelExpr_ is expand-toplevel-expr--.
func (lw *lowerer) expandToplevelExpr_(e any, file sym, line int64) any {
	lno := firstLineno(e)
	ex0 := lw.expandMacroscope(e)
	if truthy(lno) && (!lengthEq(lno, 3) || !isAtomS(caddr(lno))) {
		lno = false
	}
	if toplevelOnly(ex0) {
		if isPair(e) && inList(car(ex0), sym("error"), sym("incomplete")) {
			return ex0
		}
		if truthy(lno) {
			return list(sym("toplevel"), lno, ex0)
		}
		return ex0
	}
	var linenode any
	if truthy(lno) && (line == 0 || file == "none") {
		linenode = lno
	} else {
		linenode = list(sLine, line, file)
	}
	ex := lw.expand0(ex0, linenode)
	var blno any
	if truthy(lno) {
		blno = lno
	}
	th := lw.expand1(list(sLambda, []any{}, []any{}, list(sym("scope-block"), blockify(ex, blno))), file, line)
	body := lamBody(th)
	if len(cdadr(caddr(th))) == 0 && lengthEq(body, 2) && isReturn(cadr(body)) {
		retval := cadr(cadr(body))
		if is(retval, sLambda) || simpleAtom(retval) {
			return retval
		}
	}
	return list(sym("thunk"), th)
}

// expandMacroscope is julia-expand-macroscope (macroexpand.scm). The input
// is already macro-expanded with hygiene resolved, so only quoted forms
// and stray hygienic scopes would be left to handle.
func (lw *lowerer) expandMacroscope(e any) any {
	if contains(func(x any) bool { return is(x, "hygienic-scope") }, e) {
		panic(unsupported("hygienic-scope in a macro-expanded form"))
	}
	return lw.expandQuotes(e)
}

// expandQuotes is julia-expand-quotes.
func (lw *lowerer) expandQuotes(e any) any {
	switch {
	case !isPair(e), car(e) == sInert, car(e) == sym("module"):
		return e
	case car(e) == sQuote:
		return lw.expandQuotes(lw.bqMacro(cadr(e)))
	case !contains(func(x any) bool { return is(x, sQuote) }, cdr(e)):
		return e
	}
	return cons(car(e), mapL(lw.expandQuotes, cdr(e)))
}

// expand0 is julia-expand0: desugaring, with the location of the current
// line node appended to its error messages.
func (lw *lowerer) expand0(ex any, lno any) any {
	saved := lw.desugarLoc
	lw.desugarLoc = lno
	defer func() {
		lw.desugarLoc = saved
		if r := recover(); r != nil {
			if msg, ok := r.(loweringError); ok {
				panic(loweringError(string(msg) + formatLoc(lw.desugarLoc)))
			}
			panic(r)
		}
	}()
	return lw.expandForms(ex)
}

// expand1 is julia-expand1.
func (lw *lowerer) expand1(ex any, file sym, line int64) any {
	return lw.compactAndRenumber(lw.linearize(lw.closureConvert(lw.analyzeVariables(lw.resolveScopes(ex)))), file, line)
}

// formatLoc is format-loc (jlfrontend.scm).
func formatLoc(lno any) string {
	line, file := extractLineFile(lno)
	return formatFileLine(file, line, false)
}

func extractLineFile(lno any) (any, any) {
	switch {
	case !truthy(lno) || lno == nil || isNull(lno):
		return int64(0), sym("none")
	case !is(lno, sLine):
		errorf("lno is not a line number node")
	case lengthEq(lno, 2):
		return cadr(lno), sym("none")
	}
	return cadr(lno), caddr(lno)
}

func formatFileLine(file, line any, exact bool) string {
	if line == int64(0) || file == sym("none") {
		return ""
	}
	pre := " around "
	if exact {
		pre = " at "
	}
	return pre + symString(file) + ":" + symString(line)
}

// reprJuliaString is julia's repr of a String.
func reprJuliaString(s string) string { return syntax.ReprString(s) }

// juliaRepr is the text julia prints for an opaque value in an error
// message.
func juliaRepr(v any) string {
	switch x := v.(type) {
	case expr.Opaque:
		return x.Repr
	case expr.Module:
		parts := make([]string, len(x.Path))
		for i, p := range x.Path {
			parts[i] = string(p)
		}
		return strings.Join(parts, ".")
	case expr.Char:
		return syntax.ReprChar(x)
	}
	panic(unsupportedf("repr of %T", v))
}
