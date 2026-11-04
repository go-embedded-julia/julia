package core

import "testing"

// jl_has_intersect_kind_not_type and jl_has_intersect_type_not_kind
// (subtype.c:2444-2474) on Any, kinds, Type, unions, variables and others.
func TestHasIntersectKindAndType(t *testing.T) {
	tv := &TypeVar{Name: "T", LB: Bottom, UB: DataTypeType}
	typeInt, err := ApplyType(TypeType, Int64Type)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name      string
		ty        any
		kind, typ bool
	}{
		{"Any", AnyType, true, true},
		{"DataType", DataTypeType, true, false},
		{"Union", UnionType, true, false},
		{"Int64", Int64Type, false, false},
		{"Type{Int64}", typeInt, false, true},
		{"Type", TypeType, false, true},
		{"Union{Int64, DataType}", TypeUnion(Int64Type, DataTypeType), true, false},
		{"Union{Int64, String}", TypeUnion(Int64Type, StringType), false, false},
		{"T<:DataType", tv, true, false},
		{"Union{Int64, Type{Int64}}", TypeUnion(Int64Type, typeInt), false, true},
	} {
		if got := HasIntersectKindNotType(c.ty); got != c.kind {
			t.Errorf("HasIntersectKindNotType(%s) = %v", c.name, got)
		}
		if got := HasIntersectTypeNotKind(c.ty); got != c.typ {
			t.Errorf("HasIntersectTypeNotKind(%s) = %v", c.name, got)
		}
	}
}
