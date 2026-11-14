package core

import "testing"

// TupleType is Tuple{params...}, the same type Apply makes, with the
// parameters copied.
func TestTupleType(t *testing.T) {
	params := []any{Int64Type, Float64Type}
	got := TupleType(params...)
	if want := mustApply(AnyTupleType, Int64Type, Float64Type); !TypesEqual(got, want) {
		t.Errorf("Tuple{Int,Float64}: %s", Show(got))
	}
	params[0] = StringType
	if got.Parameters[0] != any(Int64Type) {
		t.Error("the parameters were not copied")
	}
	if got := TupleType(); got != EmptyTupleType {
		t.Errorf("Tuple{}: %s", Show(got))
	}
	va, err := WrapVararg(Int64Type, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := TupleType(Int64Type, va); !IsVaTuple(got) {
		t.Errorf("Tuple{Int,Vararg{Int}}: %s", Show(got))
	}
}
