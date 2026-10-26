package syntax_test

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

func ExampleParseAll() {
	ex := syntax.ParseAll([]byte("f(x) = x^2 + 1"), "cell.jl")
	fmt.Println(expr.Serialize(ex))
	// Output:
	// (:toplevel {L 1 :"cell.jl"} (:"=" (:call :f :x) (:block {L 1 :"cell.jl"} (:call :"+" (:call :"^" :x 2) 1))))
}

func ExampleParseAll_error() {
	// The statements before the first error, then julia's error Expr.
	ex := syntax.ParseAll([]byte("y = 2\nx = (1,"), "cell.jl").(*expr.Expr)
	last := ex.Args[len(ex.Args)-1].(*expr.Expr)
	fmt.Println(last.Head)
	fmt.Println(last.Args[0].(*expr.ParseError).Msg)
	// Output:
	// incomplete
	// ParseError:
	// # Error @ cell.jl:2:8
	// y = 2
	// x = (1,
	// #      └ ── Expected `)` or `,`
}
