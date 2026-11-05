package core

import "testing"

// jl_type_hash: equal types hash alike, distinct ones apart, whatever the
// key (a DataType, a UnionAll, a Union whose hash fails).
func TestTypeHash(t *testing.T) {
	vi := mustApply(ArrayType, Int64Type, int64(1))
	vi2 := mustApply(ArrayType, Int64Type, int64(1))
	vf := mustApply(ArrayType, Float64Type, int64(1))
	if TypeHash(vi) != TypeHash(vi2) {
		t.Error("equal types hash apart")
	}
	if TypeHash(vi) == TypeHash(vf) || TypeHash(Int64Type) == TypeHash(Float64Type) {
		t.Error("distinct types hash alike")
	}
	if TypeHash(ArrayType) != TypeHash(ArrayType) {
		t.Error("a UnionAll hashes apart from itself")
	}
	// keys the hash fails on (a Union) still hash, deterministically
	u := mustApply(UnionType, Int64Type, Float64Type)
	if TypeHash(u) != TypeHash(u) {
		t.Error("a Union hashes apart from itself")
	}
	_ = TypeHash(int64(3)) // a non-type key
}
