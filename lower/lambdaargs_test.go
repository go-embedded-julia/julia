package lower

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// A lambda whose argument list is a Vector lowers as one whose list is a list
// (julia_to_scm_, ast.c:794-800): what Base's GeneratedFunctionStub hands
// jl_expand (expr.jl:1697), the argument names an array. The lambda, lowered
// as a top-level form, is a CodeInfo whose slots are those names.
func TestLambdaArgumentVector(t *testing.T) {
	lam := func(names any) *expr.Expr {
		return expr.New("lambda", names,
			expr.New("scope-block", expr.New("block", expr.New("return", expr.Symbol("y")))))
	}
	st := NewState()
	byVector, err := st.Lower(lam(expr.Vector{expr.Symbol("y"), expr.Symbol("z")}))
	if err != nil {
		t.Fatal(err)
	}
	ci, ok := byVector.(*expr.CodeInfo)
	if !ok || len(ci.SlotNames) != 2 || ci.SlotNames[0] != expr.Symbol("y") || ci.SlotNames[1] != expr.Symbol("z") {
		t.Fatalf("a lambda with an argument Vector: %#v", byVector)
	}
	// a lambda with no argument at all is not changed by the conversion
	if _, err := NewState().Lower(expr.New("lambda")); err != nil {
		t.Errorf("a lambda without arguments: %v", err)
	}
}
