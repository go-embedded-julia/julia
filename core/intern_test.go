package core

import "testing"

// Intern returns the type already cached for a name and parameters, and
// caches a type that is new; the cache lists what was interned, by hash and
// then the types that do not hash.
func TestInternAndCachedTypes(t *testing.T) {
	tn := &TypeName{Name: "InternHost", Module: "Main"}
	wrapper := &TypeVar{Name: "T", LB: Bottom, UB: AnyType}
	prim := &DataType{Name: tn, Super: AnyType, Parameters: []any{wrapper}, HasConcreteSubtype: true}
	tn.Primary = prim
	precomputeMemoized(prim, false)
	tn.Wrapper = &UnionAll{Var: wrapper, Body: prim}

	mk := func(p any) *DataType {
		d := &DataType{Name: tn, Super: AnyType, Parameters: []any{p}, HasConcreteSubtype: true}
		_, isUnion := p.(*Union)
		precomputeMemoized(d, !isUnion) // a Union parameter does not hash unless forced to
		return d
	}
	a, b := mk(Int64Type), mk(Float64Type)
	if Intern(a) != a || Intern(b) != b {
		t.Fatal("a new type is not cached as itself")
	}
	if dup := mk(Int64Type); Intern(dup) != a {
		t.Error("an equal type is not the cached one")
	}
	// a parameter that does not hash (a Union) is cached in the linear list,
	// after the hashed types
	u := mk(TypeUnion(Int8Type, Int16Type))
	if u.Hash() != 0 {
		t.Fatalf("a Union parameter hashes (%x)", u.Hash())
	}
	if Intern(u) != u {
		t.Fatal("a type that does not hash is not cached as itself")
	}
	got := tn.CachedTypes()
	if len(got) != 3 || got[2] != u {
		t.Fatalf("cached types %v: want the two hashed ones, then the Union's", got)
	}
	var seen int
	for _, d := range got[:2] {
		if d == a || d == b {
			seen++
		}
	}
	if seen != 2 || a.Hash() == b.Hash() {
		t.Errorf("the hashed part holds %d of the two types", seen)
	}
	if got[0].Hash() > got[1].Hash() {
		t.Error("hashed types are not in the order of their hashes")
	}
	// the type cache of a name nothing was interned under is empty
	if n := (&TypeName{}).CachedTypes(); len(n) != 0 {
		t.Errorf("%d cached types in a new name", len(n))
	}
}

// Type{Wrapper} is cached on the wrapper's name by interning, set once, and
// reported by the name.
func TestTypeofWrapper(t *testing.T) {
	tn := &TypeName{Name: "WrapHost", Module: "Main"}
	if tn.TypeofWrapper() != nil {
		t.Fatal("a new name has a Type{Wrapper}")
	}
	a, b := &DataType{Name: TypeTypeName}, &DataType{Name: TypeTypeName}
	if !tn.SetTypeofWrapper(a) || tn.TypeofWrapper() != a {
		t.Error("the first Type{Wrapper} is not kept")
	}
	if !tn.SetTypeofWrapper(a) {
		t.Error("the same Type{Wrapper} is refused")
	}
	if tn.SetTypeofWrapper(b) || tn.TypeofWrapper() != a {
		t.Error("another Type{Wrapper} replaced the first")
	}
	// interning Type{W} for a wrapper W sets it, as instantiation does
	hosted := mustApply(TypeType, Int8Type).(*DataType)
	if Int8Type.Name.TypeofWrapper() != nil && Int8Type.Name.TypeofWrapper() != hosted {
		t.Error("Int8's Type{Wrapper} is not Type{Int8}")
	}
}
