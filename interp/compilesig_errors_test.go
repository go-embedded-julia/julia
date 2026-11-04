package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The intersection of a widened signature with the method's own fails when
// the method's signature has a Vararg too long to expand (core's bound, which
// julia has not). ti and the method are separate arguments of the shim, so a
// method whose signature disagrees with its definition reaches it.
func TestNormalizeIntersectionError(t *testing.T) {
	vm, s := compilesigVM(t)
	v2 := vm.lookupMatch(t, must(s.Eval("Tuple{typeof(v2), Int, Int}"))).Fields[2].(*Struct)
	f := must(s.Eval("typeof(v2)"))
	m := &Struct{Type: v2.Type, Fields: append([]any(nil), v2.Fields...)}
	m.Fields[methodSig] = &core.DataType{Name: core.TupleTypeName, Parameters: []any{f, core.AnyType, &core.Vararg{T: core.AnyType, N: int64(70000)}}}
	// Integer is not concrete, so the signature is not a dispatch tuple and
	// is widened (Type{Int} becomes Type) and intersected
	tt := must(s.Eval("Tuple{typeof(v2), Integer, Type{Int}}")).(*core.DataType)
	_, err := vm.shims["jl_normalize_to_compilable_sig"](vm, []any{tt, SVec{}, m, int32(0)})
	check(t, "a method signature with a Vararg too long", nil, err, "!Vararg length exceeds the limit")
	// the same call with the method's own signature widens and answers
	v, err := vm.shims["jl_normalize_to_compilable_sig"](vm, []any{tt, SVec{}, v2, int32(0)})
	if err != nil || !core.TypesEqual(v, must(s.Eval("Tuple{typeof(v2), Integer, Type}"))) {
		t.Errorf("the method's own signature: %s, %v", showValue(v), err)
	}
}
