package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Types built by hand that no program could make (an Array type whose
// element is a GlobalRef into a module this VM does not name): the encoder's
// recursion into a type returns the error of what it cannot write.
func TestIREncodeOfTypesThatFail(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	bad := expr.GlobalRef{Mod: "NoSuch", Name: "x"}
	weird := &core.DataType{Name: typeNameOf(core.ArrayType), Parameters: []any{bad, int64(1)}}
	mem := func(elt any, elems []any) *Memory {
		return &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), elt, core.CPU)).(*core.DataType), Elems: elems}
	}
	stmts := []any{int64(1)}

	vec := vm.newVector(core.AnyType, nil)
	vec.Type = weird
	_, err := f.compress(f.code(stmts, vec))
	check(t, "an array of a type that fails", nil, err, "!a module this VM does not name")

	m := mem(core.AnyType, nil)
	m.Type = weird
	_, err = f.compress(f.code(stmts, m))
	check(t, "a memory of a type that fails", nil, err, "!a module this VM does not name")

	_, err = f.compress(f.code(stmts, &Struct{Type: weird, Fields: []any{}}))
	check(t, "a struct of a type that fails", nil, err, "!a module this VM does not name")

	// an element that is not of the memory's element type: the layout is
	// read through the value's own fields, a union's among them
	two := mem(f.typ("Two"), []any{&core.Union{A: core.Int64Type, B: core.Int64Type}})
	_, err = f.compress(f.code(stmts, two))
	check(t, "a memory of structs holding a union", nil, err, "!the bits of a")
}
