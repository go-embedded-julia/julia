package core

import (
	"errors"
	"slices"
	"testing"
)

func tupleValue(xs ...any) *Tuple {
	types := make([]any, len(xs))
	for i, x := range xs {
		types[i] = TypeOf(x)
	}
	return &Tuple{Type: mustApply(AnyTupleType, types...).(*DataType), Elems: xs}
}

// A tuple value as a type parameter, and NamedTuple{names, T}'s fields
// from its parameters (jltypes.c:2327-2373), each error julia 1.12.6's.
func TestNamedTupleType(t *testing.T) {
	ab := tupleValue(Symbol("a"), Symbol("b"))
	tt := mustApply(AnyTupleType, Int64Type, Float64Type)
	v, err := ApplyType(NamedTupleType, ab, tt)
	if err != nil {
		t.Fatal(err)
	}
	d := v.(*DataType)
	if !slices.Equal(d.FieldNames(), []Symbol{"a", "b"}) || !d.IsConcrete || !d.IsBits() {
		t.Errorf("NamedTuple{(:a, :b), Tuple{Int64, Float64}}: names %v, concrete %v, isbits %v", d.FieldNames(), d.IsConcrete, d.IsBits())
	}
	if ft, ok := d.FieldTypes(); !ok || !slices.Equal(ft, []any{Int64Type, Float64Type}) {
		t.Errorf("field types %v %v", ft, ok)
	}
	// the type cache finds it again through an equal names tuple
	if again, _ := ApplyType(NamedTupleType, tupleValue(Symbol("a"), Symbol("b")), tt); again != v {
		t.Error("an equal NamedTuple is another type")
	}
	if TypeOf(ab) != any(ab.Type) || Show(ab) != "(:(a), :(b))" || Show(tupleValue(Symbol("a"))) != "(:(a),)" {
		t.Errorf("a tuple value: %v %s", TypeOf(ab), Show(ab))
	}
	if !Egal(ab, tupleValue(Symbol("a"), Symbol("b"))) || Egal(ab, tupleValue(Symbol("b"), Symbol("a"))) || Egal(ab, int64(1)) {
		t.Error("egal of tuple values")
	}
	if !ValidTypeParam(ab) || ValidTypeParam(&Tuple{Type: mustApply(AnyTupleType, AnyType).(*DataType)}) {
		t.Error("tuple values as type parameters")
	}
	if d.Name.FieldNames != nil || TupleTypeName.Primary.FieldNames() != nil {
		t.Error("a NamedTuple's names are its instance's, not its name's")
	}

	for _, c := range []struct {
		name          string
		names, types  any
		fn, ctx, want string
	}{
		{"names not a tuple", int64(1), EmptyTupleType, "NamedTuple", "names", "TypeError: in NamedTuple, in names, expected Tuple, got 1"},
		{"a name not a symbol", tupleValue(int64(1)), mustApply(AnyTupleType, Int64Type), "NamedTuple", "name", "TypeError: in NamedTuple, in name, expected Symbol, got 1"},
		{"a duplicate name", tupleValue(Symbol("a"), Symbol("a")), mustApply(AnyTupleType, Int64Type, Int64Type), "", "", "duplicate field name in NamedTuple: \"a\" is not unique"},
		{"fewer names than types", tupleValue(Symbol("a")), tt, "", "", "NamedTuple names and field types must have matching lengths"},
		{"a Vararg", tupleValue(Symbol("a")), mustApply(AnyTupleType, &Vararg{T: Int64Type}), "", "", "NamedTuple names and field types must have matching lengths"},
		{"a Union of tuples", tupleValue(Symbol("a")), TypeUnion(mustApply(AnyTupleType, Int64Type), mustApply(AnyTupleType, Float64Type)), "", "", "NamedTuple field type must be a tuple datatype"},
		{"no names and Union{}", EmptyTupleType.Parameters, Bottom, "", "", ""},
		{"T not a Tuple", tupleValue(Symbol("a")), Int64Type, "NamedTuple", "T", "TypeError: in NamedTuple, in T, expected T, got Int64"},
	} {
		names := c.names
		if c.name == "no names and Union{}" {
			names = tupleValue()
			c.want = "NamedTuple field type must be a tuple datatype"
		}
		_, err := ApplyType(NamedTupleType, names, c.types)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
			continue
		}
		var te *TypeError
		if isTE := errors.As(err, &te); isTE != (c.fn != "") || isTE && (te.Func != c.fn || te.Context != c.ctx) {
			t.Errorf("%s: %#v", c.name, err)
		}
	}

	// Union{} as the types: as many Union{} fields as names
	ub, err := ApplyType(NamedTupleType, tupleValue(Symbol("a")), Bottom)
	if ft, _ := ub.(*DataType).FieldTypes(); err != nil || !slices.Equal(ft, []any{Bottom}) {
		t.Errorf("NamedTuple{(:a,), Union{}}: %v %v", ft, err)
	}
	// a free type variable leaves the instance no field types
	body := UnwrapUnionAll(mustApply(NamedTupleType, ab)).(*DataType)
	if ft, ok := body.FieldTypes(); !ok || len(ft) != 0 || body.FieldNames() != nil {
		t.Errorf("NamedTuple{(:a, :b), T}: %v %v %v", ft, ok, body.FieldNames())
	}
	if UnwrapUnionAll(NamedTupleType).(*DataType).IsBits() {
		t.Error("the NamedTuple wrapper's body is isbits")
	}
	// a struct between _structtype and _typebody! has no field types to be
	// isbits by
	if NewIncompleteType("Main", "Pending", nil, false, false, []Symbol{"x"}).IsBits() {
		t.Error("a struct without field types is isbits")
	}
}
