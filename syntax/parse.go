package syntax

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// ParseAll parses src as a file of top-level statements, as
// Meta.parseall(src; filename) does in Julia: the result is an
// Expr(:toplevel, …). Inputs over the DefaultLimits yield an Expr(:error, …)
// naming the exceeded limit.
//
// Code that does not parse yields, for now, Expr(:toplevel, Expr(:error,
// …)) with the first error message only; julia's own error Expr (the
// statements before the error and the formatted diagnostics) is the next
// step of the port.
func ParseAll(src []byte, filename string) any {
	ex, err := ParseAllLimits(src, filename, DefaultLimits)
	if err != nil {
		return expr.New("error", &expr.ParseError{Msg: err.Error()})
	}
	return ex
}

// ParseAllLimits is ParseAll with explicit limits; exceeding one is
// reported as an error wrapping ErrLimit.
func ParseAllLimits(src []byte, filename string, lim Limits) (any, error) {
	if len(src) > lim.MaxBytes {
		return nil, fmt.Errorf("%w: %d bytes, the limit is %d", ErrLimit, len(src), lim.MaxBytes)
	}
	var ex any
	err := guarded(func() {
		s := newParseStream(src, 1, SyntaxVersion, lim)
		newParseState(s).parseToplevel()
		s.validateTokens()
		if !anyError(s.diagnostics) {
			ex = s.buildExpr(filename, 1)
			return
		}
		for _, d := range s.diagnostics {
			if d.IsError() {
				ex = expr.New("toplevel", expr.New("error", &expr.ParseError{Msg: d.Message}))
				return
			}
		}
	})
	return ex, err
}
