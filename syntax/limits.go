package syntax

import (
	"errors"
	"fmt"
	"runtime"
)

// SyntaxVersion is the (major, minor) Julia version whose syntax is parsed.
var SyntaxVersion = [2]int{1, 12}

// ErrLimit is wrapped by the errors of inputs that exceed a Limits bound.
var ErrLimit = errors.New("syntax: input exceeds parser limits")

// Limits bound the work a parse may do, so that hostile input cannot hang
// the parser or exhaust memory.
type Limits struct {
	// MaxBytes is the largest accepted input.
	MaxBytes int
	// MaxDepth bounds the nesting of the recursive-descent parser.
	MaxDepth int
}

// DefaultLimits are the limits of the package-level parse functions.
var DefaultLimits = Limits{MaxBytes: 64 << 20, MaxDepth: 4000}

// parseLimitError is raised (by panic, recovered at the API boundary) when
// an input exceeds a resource bound of the parser.
type parseLimitError struct{ msg string }

func (e parseLimitError) Error() string { return e.msg }

// ErrParserFailed is wrapped by the errors of inputs on which JuliaSyntax
// itself fails (an internal assertion, an out-of-bounds index). julia's Core parser hook then
// falls back to its older flisp parser, which this port does not include, so
// there the Go parser reports the failure instead of a tree it cannot trust.
var ErrParserFailed = errors.New("syntax: JuliaSyntax parser failed (julia falls back to its flisp parser here)")

// parseAssertError is raised (by panic, recovered at the API boundary) where
// JuliaSyntax raises an AssertionError.
type parseAssertError struct{ msg string }

// guarded runs f, turning a parser limit abort or a failed JuliaSyntax
// assertion into an error.
func guarded(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			switch e := r.(type) {
			case parseLimitError:
				err = fmt.Errorf("%w: %s", ErrLimit, e.msg)
			case parseAssertError:
				err = fmt.Errorf("%w: AssertionError: %s", ErrParserFailed, e.msg)
			case runtime.Error:
				// julia's Core parser hook catches every exception of
				// JuliaSyntax; the Go port's out-of-range indexing is where
				// JuliaSyntax throws a BoundsError (`0:0:<`, in
				// _internal_node_to_Expr). A hostile input must not crash
				// the embedding program either.
				err = fmt.Errorf("%w: %v", ErrParserFailed, e)
			default:
				panic(r)
			}
		}
	}()
	f()
	return nil
}
