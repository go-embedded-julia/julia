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
