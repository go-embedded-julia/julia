package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// JuliaSyntax, julia's parser, is Julia code Base includes from its source
// (Base.jl:316-317) and makes Core's parser in Base's __init__
// (Base.jl:394-396, JuliaSyntax.enable_in_core!). Here it is package
// syntax, its port to Go, which Core._parse already reaches through
// jl_fl_parse (flparse.go): the Julia source is not interpreted, and Base
// gets a native Base.JuliaSyntax holding what Base itself calls of it.
// Julia code that calls JuliaSyntax's own API (the REPL's completions,
// Markdown's highlighting) has none of it.

// juliaSyntaxPath is the file Base.jl:317 includes.
const juliaSyntaxPath = "JuliaSyntax/src/JuliaSyntax.jl"

// isJuliaSyntaxInclude reports Base.jl:317,
// include(@__MODULE__, string(BUILDROOT, "JuliaSyntax/src/JuliaSyntax.jl")).
func isJuliaSyntaxInclude(form any) bool {
	call, ok := form.(*expr.Expr)
	if !ok || call.Head != "call" || len(call.Args) != 3 || call.Args[0] != any(expr.Symbol("include")) {
		return false
	}
	str, ok := call.Args[2].(*expr.Expr)
	return ok && str.Head == "call" && len(str.Args) == 3 && str.Args[0] == any(expr.Symbol("string")) &&
		str.Args[2] == any(juliaSyntaxPath)
}

// defineJuliaSyntax makes Base.JuliaSyntax: a module of Base with
// enable_in_core!(), which leaves Core's parser the Go port Base already
// set (Base_compiler.jl:383, fl_parse over jl_fl_parse); its syntax errors
// keep jl_fl_parse's String messages, where julia's JuliaSyntax hook gives
// a Meta.ParseError.
func (vm *VM) defineJuliaSyntax(base *Module) {
	js := newModule("JuliaSyntax", base)
	js.usings = []*Module{vm.Core, base}
	b := base.declare("JuliaSyntax", vm.newWorld())
	b.Value, b.Const = js, true
	enable := vm.newFunction(js, "enable_in_core!")
	eb := js.declare("enable_in_core!", vm.newWorld())
	eb.Value, eb.Const = enable, true
	// no arguments: a function's table leaves its own type out of the
	// signatures (dispatch.Table.Head)
	m := must(enable.Table.Define(core.EmptyTupleType, vm.newWorld(), nil))
	m.Def = goMethod(func(vm *VM, args []any) (any, error) { return nil, nil })
}
