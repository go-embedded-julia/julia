// Package lower turns macro-expanded Julia syntax trees into julia's lowered
// form: the Expr(:thunk, CodeInfo) that Meta.lower returns.
//
// The reference is the front end julia 1.12.6 uses by default, femtolisp's
// src/julia-syntax.scm (with ast.scm and the C glue of src/ast.c and
// src/method.c): Meta.lower is that code, so porting it is how the output
// can be identical. Its passes are the pipeline of julia-expand1:
//
//	expand-forms        desugaring
//	resolve-scopes      scope analysis
//	analyze-variables!  captured and assigned variables
//	closure-convert     closures to structs, global assignments
//	linearize           statements, SSA values, labels
//	compact-and-renumber
//
// Macro expansion runs user code and stays with the caller (the oracle's
// goldens record julia's expansion as the input).
package lower

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// ErrUnsupported is wrapped by the errors of forms whose lowering is not
// ported yet.
var ErrUnsupported = errors.New("lower: not ported yet")

// State is the per-module state lowering reads and updates across the
// top-level forms of a file, as julia's module does (closure names come
// from the module's counter and its existing bindings).
type State struct {
	counter  int
	bindings map[expr.Symbol]bool
}

// NewState returns the state of a fresh module.
func NewState() *State {
	return &State{bindings: map[expr.Symbol]bool{}}
}

// Lower is Meta.lower(m, ex) for a macro-expanded top-level form ex (src/
// jlfrontend.scm: expand-toplevel-expr).
func (st *State) Lower(ex any) (any, error) {
	if isAtom(ex) || toplevelOnly(ex) {
		if s, ok := ex.(expr.Symbol); ok && isUnderscore(s) {
			return expr.New("error", "all-underscore identifiers are write-only and their values cannot be used in expressions"), nil
		}
		return ex, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrUnsupported, headOf(ex))
}

// isAtom is femtolisp's atom? of the value julia_to_scm makes of v
// (src/ast.c): Expr, LineNumberNode, QuoteNode, GlobalRef, GotoNode,
// NewvarNode and the constants true, false and nothing become lists.
func isAtom(v any) bool {
	switch v.(type) {
	case *expr.Expr, expr.LineNumberNode, expr.QuoteNode, expr.GlobalRef,
		expr.GotoNode, expr.NewvarNode, bool, nil:
		return false
	}
	return true
}

// toplevelOnly is toplevel-only-expr? (jlfrontend.scm).
func toplevelOnly(v any) bool {
	e, ok := v.(*expr.Expr)
	if !ok {
		return false
	}
	switch e.Head {
	case "toplevel", "line", "module", "export", "public", "error", "incomplete":
		return true
	case "global", "const":
		for _, a := range e.Args {
			if _, ok := a.(expr.Symbol); !ok {
				return false
			}
		}
		return true
	}
	return false
}

// isUnderscore is underscore-symbol? (ast.scm): a name made of underscores.
func isUnderscore(s expr.Symbol) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] != '_' {
			return false
		}
	}
	return true
}

func headOf(v any) string {
	if e, ok := v.(*expr.Expr); ok {
		return string(e.Head)
	}
	return fmt.Sprintf("%T", v)
}
