package syntax

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// ParseAll parses src as a file of top-level statements, as
// Meta.parseall(src; filename) does in Julia: the result is an
// Expr(:toplevel, …). Code that does not parse yields, as in Julia, the
// statements before the first error followed by an Expr(:error, …) or
// Expr(:incomplete, …) holding the formatted diagnostics. Inputs over the
// DefaultLimits yield an Expr(:error, …) naming the exceeded limit.
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
		} else {
			ex = s.errorExpr(filename, 1)
		}
	})
	return ex, err
}
