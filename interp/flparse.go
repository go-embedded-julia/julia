package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// flParse is jl_fl_parse (ast.c:842-894), Base.fl_parse's ccall
// (flparse.jl:13) and so Core._parse once Base_compiler.jl:383 installs it:
// text_len bytes of text parsed from the 0-based offset by rule :all,
// :statement or :atom, answered svec(expr, offset where parsing stopped).
// The parser is JuliaSyntax's (syntax.ParseRule, as julia's Core._parse
// after its boot); a syntax error's message is a String, as flisp gives it.
func (vm *VM) flParse(a []any) (any, error) {
	p, ok := a[0].(*Pointer)
	if !ok {
		return nil, vm.typeError("ccall", "", vm.ptrType(core.UInt8Type), a[0])
	}
	n, ok1 := a[1].(uint64)
	filename, ok2 := a[2].(string)
	line, ok3 := a[3].(uint64)
	offset, ok4 := a[4].(uint64)
	rule, ok5 := a[5].(expr.Symbol)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return nil, fmt.Errorf("%w: jl_fl_parse of %s", ErrUnsupported, showValue(vm.newTuple(a)))
	}
	text, ok := vm.bytesAt(p, n)
	if !ok {
		return nil, fmt.Errorf("%w: jl_fl_parse of %d bytes at %s", ErrUnsupported, n, showValue(p))
	}
	if offset > n {
		return nil, vm.exception(vm.tBounds, string(text), int64(offset+1))
	}
	if !ok5 || rule != "all" && rule != "statement" && rule != "atom" {
		return nil, vm.errorf("jl_fl_parse: unrecognized parse options")
	}
	if offset != 0 && rule == "all" {
		return nil, vm.errorf("Parse `all`: offset not supported")
	}
	ex, last, err := syntax.ParseRule(text, filename, int(line), int(offset), string(rule), syntax.DefaultLimits)
	if err != nil {
		// over the parser's limits, or a failure of JuliaSyntax's own
		// (after which julia would fall back to flisp): a syntax error
		ex, last = expr.New("error", err.Error()), int(offset)
	}
	return SVec{parseErrorsAsStrings(ex), int64(last)}, nil
}

// parseErrorsAsStrings gives an Expr(:error or :incomplete, ParseError) the
// message as its argument, flisp's form, where the parser puts it: the
// expression itself, or the last form of a toplevel one.
func parseErrorsAsStrings(ex any) any {
	e, ok := ex.(*expr.Expr)
	if !ok || len(e.Args) == 0 {
		return ex
	}
	if e.Head == "toplevel" {
		e.Args[len(e.Args)-1] = parseErrorsAsStrings(e.Args[len(e.Args)-1])
		return e
	}
	if pe, ok := e.Args[0].(*expr.ParseError); ok && (e.Head == "error" || e.Head == "incomplete") {
		e.Args[0] = pe.Msg
	}
	return e
}
