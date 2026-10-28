package core

import "testing"

// With no type-variable environment, eq_msp is jl_types_equal and sub_msp
// is jl_subtype (subtype.c:4960-5094 only add the rewrapping of env's
// variables to those): checked on the random pairs of the differential test.
func TestMspHelpersMatchSubtyping(t *testing.T) {
	xs, ys := drawPairs(4000)
	for i := range xs {
		x, y := xs[i], ys[i]
		if got, want := eqMsp(x, y, x, y, nil), TypesEqual(x, y); got != want {
			t.Fatalf("eq_msp(%s, %s) = %v, jl_types_equal = %v", Show(x), Show(y), got, want)
		}
		if got, want := subMsp(x, y, y, nil), Subtype(x, y); got != want {
			t.Fatalf("sub_msp(%s, %s) = %v, jl_subtype = %v", Show(x), Show(y), got, want)
		}
	}
	// two equal, distinct union objects: the types_egal shortcut
	u1, u2 := &Union{Int64Type, StringType}, &Union{Int64Type, StringType}
	if !eqMsp(u1, u2, u1, u2, nil) {
		t.Fatal("eq_msp of two equal unions is false")
	}
}

// count_missing_wrap (subtype.c:4909-4935) counts the variables of env that
// x mentions, plus those a wrapped variable's bounds depend on; a variable
// shadowed in env is counted once.
func TestCountMissingWrap(t *testing.T) {
	S := tv("S")
	T := &TypeVar{Name: "T", LB: Bottom, UB: mustApply(ArrayType, S, int64(1))}
	env := &typeEnv{T, 1, &typeEnv{S, 1, nil}}
	x := mustApply(RefType, T)
	if n := countMissingWrap(x, env); n != 2 {
		t.Fatalf("Ref{T} with T<:Vector{S}: %d, want 2 (T, and S through T's bound)", n)
	}
	shadowed := &typeEnv{T, 1, &typeEnv{T, 1, nil}}
	if n := countMissingWrap(x, shadowed); n != 1 {
		t.Fatalf("T bound twice: %d, want 1", n)
	}
	if n := countMissingWrap(Int64Type, env); n != 0 {
		t.Fatalf("a closed type: %d, want 0", n)
	}
}

func TestSpecificityHelpers(t *testing.T) {
	if nthTupleElt(EmptyTupleType, 0) != nil {
		t.Error("nth_tuple_elt of Tuple{} is not NULL")
	}
	T := tv("T")
	if numOccurs(T, &typeEnv{tv("S"), 2, nil}) != 0 {
		t.Error("num_occurs of a variable absent from env is not 0")
	}
	// count_occurs stops at a UnionAll that rebinds the variable (the body
	// must not equal a wrapper, or construction swaps in the wrapper)
	inner := &UnionAll{Var: T, Body: mustApply(PairType, T, T)}
	if n := countOccurs(mustApply(PairType, T, inner), T); n != 1 {
		t.Errorf("count_occurs through a shadowing UnionAll: %d, want 1", n)
	}
	// tuple_full_length counts a Vararg of known length (subtype.c:5192-5203)
	tup := mustApply(AnyTupleType, Int64Type, &Vararg{T: T, N: int64(2)}).(*DataType)
	if n := tupleFullLength(tup); n != 3 {
		t.Errorf("tuple_full_length(Tuple{Int, Vararg{T,2}}) = %d, want 3", n)
	}
}

// args_morespecific_fix1 (subtype.c:5207-5236) answers `swap` when fixing
// a's length makes it equal to b; and in datatype specificity a free
// parameter against an equal closed one scores for the closed side
// (subtype.c:5417-5421).
func TestSpecificityFixAndScores(t *testing.T) {
	T, N := tv("T"), tv("N")
	a := mustApply(AnyTupleType, mustApply(ArrayType, T, N), &Vararg{T: Int64Type, N: N}).(*DataType)
	b := mustApply(AnyTupleType, mustApply(ArrayType, T, int64(1)), Int64Type).(*DataType)
	env := &typeEnv{T, 2, &typeEnv{N, 2, nil}}
	if got := argsMorespecificFix1(a, b, a, b, true, env); got != 1 {
		t.Errorf("fix1 with equal fixed form, swap: %d, want 1", got)
	}
	if got := argsMorespecificFix1(a, b, a, b, false, env); got != 0 {
		t.Errorf("fix1 with equal fixed form, no swap: %d, want 0", got)
	}
	// Ref{Vector{S}} (S free in env) against Ref{Vector}: neither parameter
	// is more specific, they are equal once S is wrapped, and the free one
	// loses, so Ref{Vector{S}} is not more specific
	S := tv("S")
	vector := TypeUnionAll(T, mustApply(ArrayType, T, int64(1)))
	free := mustApply(RefType, mustApply(ArrayType, S, int64(1))).(*DataType)
	closed := mustApply(RefType, vector).(*DataType)
	if datatypeMorespecific(free, closed, free, closed, &typeEnv{S, 1, nil}) {
		t.Error("Ref{Vector{S}} is more specific than Ref{Vector}")
	}
}
