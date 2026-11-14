package core

import "testing"

// jl_instantiate_type_in_env: a type with the variables of an environment's
// UnionAlls set to values, the outermost first; Union{} when the values make
// no valid type (jltypes.c:2772-2784, JL_TRY and the catch's jl_bottom_type).
func TestInstantiateTypeInEnv(t *testing.T) {
	newVar := func(name string, ub any) *TypeVar {
		v, err := NewTypeVar(name, Bottom, ub)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	tv, sv := newVar("T", AnyType), newVar("S", AnyType)
	tup2 := mustApply(AnyTupleType, tv, sv)
	// env is Tuple{T,S} where S where T: T is the outer variable
	env := TypeUnionAll(tv, TypeUnionAll(sv, tup2))

	// both variables set
	got := InstantiateTypeInEnv(tup2, env, []any{Int64Type, Float64Type})
	if want := mustApply(AnyTupleType, Int64Type, Float64Type); !TypesEqual(got, want) {
		t.Errorf("both set: %s, want %s", Show(got), Show(want))
	}
	// only the outer one: the inner stays
	got = InstantiateTypeInEnv(tup2, env, []any{Int64Type})
	if want := TypeUnionAll(sv, mustApply(AnyTupleType, Int64Type, sv)); !TypesEqual(got, want) {
		t.Errorf("the outer set: %s, want %s", Show(got), Show(want))
	}
	// more values than variables: the extra ones are ignored
	got = InstantiateTypeInEnv(tup2, env, []any{Int64Type, Float64Type, Int8Type})
	if want := mustApply(AnyTupleType, Int64Type, Float64Type); !TypesEqual(got, want) {
		t.Errorf("too many values: %s, want %s", Show(got), Show(want))
	}
	// no values, and an env that is no UnionAll: the type as it is
	if got := InstantiateTypeInEnv(tup2, env, nil); got != any(tup2) {
		t.Errorf("no values: %s", Show(got))
	}
	if got := InstantiateTypeInEnv(tup2, Int64Type, []any{Int64Type}); got != any(tup2) {
		t.Errorf("an env that is none: %s", Show(got))
	}
	// values that make no type: Union{}
	vec := mustApply(ArrayType, tv, int64(1))
	venv := TypeUnionAll(tv, vec)
	if got := InstantiateTypeInEnv(vec, venv, []any{Int64Type}); !TypesEqual(got, mustApply(ArrayType, Int64Type, int64(1))) {
		t.Errorf("a valid Array: %s", Show(got))
	}
	for _, bad := range []any{int64(3), "x", nil} {
		var got any
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("%v made a panic, not Union{}: %v", bad, p)
				}
			}()
			got = InstantiateTypeInEnv(mustApply(AnyTupleType, varargOf(tv)), TypeUnionAll(tv, mustApply(AnyTupleType, varargOf(tv))), []any{bad})
		}()
		_ = got
	}
	un := mustApply(UnionType, tv, Int64Type)
	if got := InstantiateTypeInEnv(un, TypeUnionAll(tv, un), []any{int64(3)}); got != any(Bottom) {
		t.Errorf("Union{T,Int} with T=3: %s, want Union{}", Show(got))
	}
}

// varargOf is Vararg{T} as a tuple parameter.
func varargOf(t any) any {
	v, err := WrapVararg(t, nil)
	if err != nil {
		panic(err)
	}
	return v
}
