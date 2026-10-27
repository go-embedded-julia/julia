package core

import "testing"

func app(t *testing.T, tc any, ps ...any) any {
	t.Helper()
	v, err := Apply(tc, ps...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func tvb(name string, lb, ub any) *TypeVar { return &TypeVar{Name: name, LB: lb, UB: ub} }

// Answers read from julia 1.12.6 (julia -e 'println(A <: B)').
func TestSubtypeSmoke(t *testing.T) {
	vecInt := app(t, ArrayType, Int64Type, int64(1))
	T := tvb("T", Bottom, AnyType)
	vecT := &UnionAll{Var: T, Body: app(t, ArrayType, T, int64(1))}
	R := tvb("R", Bottom, RealType)
	tupRR := TypeUnionAll(R, app(t, AnyTupleType, R, R))
	S := tvb("S", Bottom, AnyType)
	tupSS := TypeUnionAll(S, app(t, AnyTupleType, S, S))
	cases := []struct {
		x, y any
		want bool
	}{
		{Int64Type, RealType, true},
		{RealType, Int64Type, false},
		{Int64Type, AnyType, true},
		{Bottom, Int64Type, true},
		{vecInt, vecT, true},
		{vecT, vecInt, false},
		{app(t, ArrayType, RealType, int64(1)), app(t, ArrayType, Int64Type, int64(1)), false},
		{TypeUnion(Int64Type, Float64Type), RealType, true},
		{RealType, TypeUnion(Int64Type, Float64Type), false},
		{app(t, AnyTupleType, Int64Type, Int64Type), tupSS, true},
		{app(t, AnyTupleType, Int64Type, Float64Type), tupSS, false},
		{app(t, AnyTupleType, Int64Type, Float64Type), tupRR, false},
		{app(t, AnyTupleType, Int64Type, Int64Type), tupRR, true},
		{app(t, AnyTupleType, RealType, RealType), tupSS, false},
		{app(t, TypeType, Int64Type), DataTypeType, true},
		{DataTypeType, TypeType, true},
		{TypeofBottomType, app(t, TypeType, Bottom), true},
		{app(t, AnyTupleType, Int64Type, Int64Type, Int64Type),
			app(t, AnyTupleType, &Vararg{T: Int64Type}), true},
		{app(t, AnyTupleType, Int64Type, Float64Type),
			app(t, AnyTupleType, &Vararg{T: Int64Type}), false},
		{app(t, DenseArrayType, Int64Type, int64(2)), app(t, AbstractArrayType, Int64Type, int64(2)), true},
		{app(t, ArrayType, Int64Type, int64(2)), app(t, AbstractArrayType, Int64Type, int64(1)), false},
	}
	for i, c := range cases {
		if got := Subtype(c.x, c.y); got != c.want {
			t.Errorf("%d: %s <: %s = %v, want %v", i, Show(c.x), Show(c.y), got, c.want)
		}
	}
	if s := Show(app(t, AnyTupleType, &Vararg{T: Int64Type, N: int64(2)})); s != "Tuple{Int64, Int64}" {
		t.Errorf("Tuple{Vararg{Int64,2}} shows as %s", s)
	}
	if app(t, ArrayType, Int64Type, int64(1)) != vecInt {
		t.Error("Array{Int64,1} is not interned")
	}
}
