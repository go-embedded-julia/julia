package syntax

import (
	"errors"
	"fmt"
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

// guarded runs f, turning a parser limit abort into an error.
func guarded(f func()) (err error) {
	defer func() {
		if r := recover(); r != nil {
			le, ok := r.(parseLimitError)
			if !ok {
				panic(r)
			}
			err = fmt.Errorf("%w: %s", ErrLimit, le.msg)
		}
	}()
	f()
	return nil
}
