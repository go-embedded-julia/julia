package core

import "testing"

// TypeName.Hash is a function of the module and the name only: a name nothing
// has hashed yet answers what it will store, and reading it stores nothing.
func TestTypeNameHash(t *testing.T) {
	a := &TypeName{Module: "Main", Name: "A"}
	h := a.Hash()
	if h == 0 || h&1 == 0 {
		t.Errorf("hash %x: want nonzero and odd as julia's", h)
	}
	if a.hash != 0 {
		t.Error("reading the hash of an unhashed name stored it")
	}
	if b := (&TypeName{Module: "Main", Name: "A"}); b.Hash() != h {
		t.Error("two names of one module and name hash differently")
	}
	if b := (&TypeName{Module: "Main", Name: "B"}); b.Hash() == h {
		t.Error("two names collide")
	}
	if b := (&TypeName{Module: "Other", Name: "A"}); b.Hash() == h {
		t.Error("two modules collide")
	}
	// a name that was hashed (precomputeMemoized) answers the stored hash
	a.hash = 0x1234567
	if a.Hash() != 0x1234567 {
		t.Error("a hashed name answers another hash")
	}
}

// DataType.Hash reads the hash of the name and parameters.
func TestDataTypeHash(t *testing.T) {
	tuple := func(p ...any) *DataType {
		t.Helper()
		v, err := Apply(TupleTypeName.Wrapper, p...)
		if err != nil {
			t.Fatal(err)
		}
		return v.(*DataType)
	}
	v1, v2 := tuple(Int64Type), tuple(Float64Type)
	if v1.Hash() == 0 || v1.Hash() == v2.Hash() {
		t.Errorf("Tuple hashes %x, %x", v1.Hash(), v2.Hash())
	}
	if v1.Hash() != tuple(Int64Type).Hash() {
		t.Error("one type, two hashes")
	}
}
