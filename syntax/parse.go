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
// DefaultLimits yield an Expr(:error, …) naming the exceeded limit, and so
// do the rare inputs on which JuliaSyntax itself fails (ErrParserFailed).
func ParseAll(src []byte, filename string) any {
	ex, err := ParseAllLimits(src, filename, DefaultLimits)
	if err != nil {
		return expr.New("error", &expr.ParseError{Msg: err.Error()})
	}
	return ex
}

// ParseAllLimits is ParseAll with explicit limits; exceeding one is
// reported as an error wrapping ErrLimit, a failure of the JuliaSyntax
// algorithm itself as one wrapping ErrParserFailed.
func ParseAllLimits(src []byte, filename string, lim Limits) (any, error) {
	ex, _, err := ParseRule(src, filename, 1, 0, "all", lim)
	return ex, err
}

// ParseRule is core_parser_hook (JuliaSyntax hooks.jl:139-254), the parser
// behind Core._parse: from the 0-based byte offset, rule "all" parses a
// file of statements, "statement" one statement (or several joined by
// semicolons) and the newline after it, "atom" one literal, identifier or
// parenthesised expression (parser_api.jl:47-64). It returns the
// expression, nil when a statement or atom finds only whitespace before the
// end, and the 0-based offset where it stopped; a syntax error is the
// expression, as ParseAll's are, the toplevel form only for "all".
func ParseRule(src []byte, filename string, line, offset int, rule string, lim Limits) (any, int, error) {
	if len(src) > lim.MaxBytes {
		return nil, 0, fmt.Errorf("%w: %d bytes, the limit is %d", ErrLimit, len(src), lim.MaxBytes)
	}
	if rule != "all" && rule != "statement" && rule != "atom" {
		return nil, 0, fmt.Errorf("unknown grammar rule %s", rule)
	}
	var ex any
	var last int
	err := guarded(func() {
		s := newParseStream(src, offset+1, SyntaxVersion, lim)
		if rule != "all" {
			// as the flisp driver: an atom consumes the trivia before it,
			// a statement the trivia before and after it
			s.bumpTrivia(emptyFlags, true, "")
			if s.peek(1, false) == kEndMarker {
				last = s.lastByte()
				return
			}
		}
		ps := newParseState(s)
		switch rule {
		case "all":
			ps.parseToplevel()
		case "statement":
			ps.parseStmts()
		default:
			ps.parseAtom(true, false)
		}
		s.validateTokens()
		if rule == "statement" {
			s.bumpTrivia(emptyFlags, false, "")
			if s.peek(1, false) == kNewlineWs {
				s.bump(emptyFlags, false, "", kNone)
			}
		}
		switch {
		case !anyError(s.diagnostics):
			ex = s.buildExpr(filename, line)
		case rule == "all":
			ex = s.errorExpr(filename, line)
		default:
			_, ex = s.errorTree(filename, line)
		}
		last = s.lastByte()
	})
	return ex, last, err
}
