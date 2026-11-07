package core

import (
	"fmt"
	"testing"
)

// defStruct makes a struct type the way _structtype, _setsuper! and
// _typebody! do: over params, its field types built from the type itself (an
// instance over its own variables for a parametric one).
func defStruct(t *testing.T, name string, params []*TypeVar, mutable bool, super *DataType, n int, fields func(self any) []any) *DataType {
	t.Helper()
	names := make([]Symbol, n)
	for i := range names {
		names[i] = Symbol(fmt.Sprintf("f%d", i))
	}
	d := NewIncompleteType("Main", name, params, false, mutable, names)
	if super == nil {
		super = AnyType
	}
	if err := d.SetSuper(super); err != nil {
		t.Fatal(err)
	}
	var self any = d
	if len(params) > 0 {
		vars := make([]any, len(params))
		for i, p := range params {
			vars[i] = p
		}
		self = mustApply(d.Name.Wrapper, vars...)
	}
	if err := d.SetFieldTypes(fields(self)); err != nil {
		t.Fatal(err)
	}
	return d
}

func tvar(name string) *TypeVar { return &TypeVar{Name: name, LB: Bottom, UB: AnyType} }

// mayinlinealloc is decided as _typebody! does (builtins.c:2254-2269): an
// immutable type that cannot reach itself through a position that decides a
// layout; the answers are julia 1.12.6's (name.flags in
// interp/testdata/layout.golden has the same of the types the runtime
// defines).
func TestMayInlineAlloc(t *testing.T) {
	T, N := tvar("T"), tvar("N")
	one := func(f any) func(any) []any { return func(any) []any { return []any{f} } }
	selfIn := func(wrap func(self any) any) func(any) []any {
		return func(self any) []any { return []any{wrap(self)} }
	}
	// Q{T}: a layout that is fixed whatever T is, with a free type variable in
	// a field; R{T}: one whose fields have none
	q := defStruct(t, "Q", []*TypeVar{T}, false, nil, 2, func(self any) []any {
		return []any{Int64Type, mustApply(ArrayType, T, int64(1))}
	})
	r := defStruct(t, "R", []*TypeVar{T}, false, nil, 1, one(Int64Type))
	// U <: Ref{S}, a bound that reaches S
	ub := tvar("U")
	bound := func(self any) []any {
		ub.UB = mustApply(RefType, self)
		return []any{&UnionAll{Var: ub, Body: mustApply(RefType, ub)}}
	}
	later := NewIncompleteType("Main", "Later", []*TypeVar{tvar("X")}, false, false, []Symbol{"x"})
	if err := later.SetSuper(AnyType); err != nil {
		t.Fatal(err)
	}
	ab := NewIncompleteType("Main", "AB", []*TypeVar{tvar("X")}, true, false, nil)
	if err := ab.SetSuper(AnyType); err != nil {
		t.Fatal(err)
	}
	nt := func(self any) any {
		return mustApply(NamedTupleType, tupleValue(Symbol("a")), mustApply(AnyTupleType, self))
	}
	for _, c := range []struct {
		name    string
		params  []*TypeVar
		mutable bool
		n       int
		fields  func(self any) []any
		want    bool
	}{
		{"plain", nil, false, 1, one(Int64Type), true},
		{"empty", nil, false, 0, func(any) []any { return nil }, true},
		{"self through a Union", nil, false, 1, selfIn(func(s any) any { return TypeUnion(NothingType, s) }), false},
		{"mutable", nil, true, 1, selfIn(func(s any) any { return TypeUnion(NothingType, s) }), false},
		{"a pointer to itself", nil, false, 1, selfIn(func(s any) any { return mustApply(PtrType, s) }), true},
		{"an array of itself", nil, false, 1, selfIn(func(s any) any { return mustApply(ArrayType, s, int64(1)) }), false},
		{"a Ref of itself", nil, false, 1, selfIn(func(s any) any { return mustApply(RefType, s) }), false},
		{"a bound that reaches it", nil, false, 1, bound, false},
		{"a Vararg over a parameter", []*TypeVar{N}, false, 1, one(mustApply(AnyTupleType, &Vararg{T: Int64Type, N: N})), true},
		{"a Vararg of itself", nil, false, 1, selfIn(func(s any) any { return mustApply(AnyTupleType, &Vararg{T: s}) }), false},
		{"a tuple of itself", nil, false, 1, selfIn(func(s any) any { return mustApply(AnyTupleType, Int64Type, s) }), false},
		{"a tuple wrapper without a layout", nil, false, 1, one(mustApply(AnyTupleType, Int64Type, &Vararg{T: Int64Type})), true},
		{"a named tuple of itself", nil, false, 1, selfIn(nt), false},
		{"a fixed layout over T, with T in a field", []*TypeVar{T}, false, 1, one(mustApply(q.Name.Wrapper, T)), true},
		{"a fixed layout over T, without T in a field", []*TypeVar{T}, false, 1, one(mustApply(r.Name.Wrapper, T)), true},
		{"itself over its own parameter", []*TypeVar{T}, false, 1, selfIn(func(s any) any { return TypeUnion(NothingType, s) }), false},
		{"itself through a type not yet given its fields", nil, false, 1, selfIn(func(s any) any { return mustApply(later.Name.Wrapper, s) }), false},
	} {
		d := defStruct(t, "S", c.params, c.mutable, nil, c.n, c.fields)
		if d.Name.MayInlineAlloc != c.want {
			t.Errorf("%s: mayinlinealloc %v, julia %v", c.name, d.Name.MayInlineAlloc, c.want)
		}
	}
	// a supertype that reaches the type: not, with fields; with none, whatever
	for n, want := range map[int]bool{1: false, 0: true} {
		sub := NewIncompleteType("Main", "Sub", nil, false, false, make([]Symbol, n))
		if err := sub.SetSuper(mustApply(ab.Name.Wrapper, sub).(*DataType)); err != nil {
			t.Fatal(err)
		}
		fields := make([]any, n)
		for i := range fields {
			fields[i] = Int64Type
		}
		if err := sub.SetFieldTypes(fields); err != nil || sub.Name.MayInlineAlloc != want {
			t.Errorf("a type under AB{itself} with %d fields: mayinlinealloc %v, %v", n, sub.Name.MayInlineAlloc, err)
		}
	}
	// a self-referential type is not isbits, nor a singleton, nor does the walk
	// of its fields end in an overflow
	self := defStruct(t, "Self", nil, false, nil, 1, func(self any) []any { return []any{TypeUnion(NothingType, self)} })
	if self.IsBits() || self.IsSingleton() {
		t.Error("a type that may reference itself is isbits or a singleton")
	}
	if !NothingType.IsSingleton() || Int64Type.IsSingleton() {
		t.Error("Nothing is a singleton, Int64 is not")
	}
	// a concrete immutable type whose fields are not recorded is not isbits
	if (&DataType{Name: &TypeName{Name: "X", Module: "Main", MayInlineAlloc: true}, IsConcrete: true}).IsBits() {
		t.Error("a type without recorded fields is isbits")
	}
}

// jl_has_fixed_layout (jltypes.c:293-319) and layout_uses_free_typevars
// (jltypes.c:52-106): a concrete type has one; a memory type, an abstract
// type and a tuple type have not; a struct has one unless a field uses a free
// parameter in the layout.
func TestHasFixedLayout(t *testing.T) {
	T := tvar("T")
	U := &TypeVar{Name: "U", LB: Bottom, UB: AnyTupleType}
	field := func(f any) *DataType {
		return defStruct(t, "H", []*TypeVar{T}, false, nil, 1, func(any) []any { return []any{f} })
	}
	r := defStruct(t, "R2", []*TypeVar{T}, false, nil, 1, func(any) []any { return []any{T} })
	boundR := &UnionAll{Var: U, Body: mustApply(AnyTupleType, U)}
	ntFree := mustApply(NamedTupleType, tupleValue(Symbol("a")), U)
	for _, c := range []struct {
		name string
		d    *DataType
		want bool
	}{
		{"a concrete type", Int64Type, true},
		{"a memory type", UnwrapUnionAll(GenericMemoryType).(*DataType), false},
		{"an abstract type", UnwrapUnionAll(RefType).(*DataType), false},
		{"Tuple", AnyTupleType, false},
		{"NamedTuple's body", UnwrapUnionAll(NamedTupleType).(*DataType), false},
		{"a named tuple of a free T", ntFree.(*DataType), false},
		{"a free field", field(T), false},
		{"a parametric field", field(mustApply(r.Name.Wrapper, T)), false},
		{"a bound parameter", field(&UnionAll{Var: T, Body: mustApply(r.Name.Wrapper, T)}), true},
		{"a named tuple field", field(ntFree), false},
		{"a tuple field", field(mustApply(AnyTupleType, T)), false},
		{"a Union with a free member", field(TypeUnion(T, Int64Type)), false},
		{"a Union whose first member is free", field(&Union{A: T, B: Int64Type}), false},
		{"a Union whose second member is concrete", field(TypeUnion(Int64Type, mustApply(r.Name.Wrapper, Int64Type))), true},
		{"a bound tuple", field(boundR), false},
		{"a field without a layout", field(mustApply(ArrayType, T, int64(1))), true},
	} {
		if got := HasFixedLayout(c.d); got != c.want {
			t.Errorf("%s: %v, julia %v", c.name, got, c.want)
		}
	}
}
