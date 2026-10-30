package core

import (
	"encoding/binary"
	"testing"
	"time"
)

// FuzzSubtype draws a pair of types from the fuzzer's bytes and checks that
// construction and the type predicates never panic (a julia error is an
// *Error, nothing else), and the properties julia's algorithm guarantees on
// closed types: x <: x, Union{} <: x <: Any, and == agreeing with <: both
// ways.
func FuzzSubtype(f *testing.F) {
	for _, s := range []uint64{0, 1, 2, 3, 42, 1 << 40} {
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], s)
		f.Add(b[:])
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		var seed uint64
		for i, c := range data {
			seed ^= uint64(c) << (8 * (i % 8))
			seed = seed*0x9e3779b97f4a7c15 + 1
		}
		g := newTypeGen(seed)
		x, y := g.pair()
		check := func(name string, f func() bool) bool {
			var r bool
			if err := try(func() { r = f() }); err != nil {
				t.Fatalf("%s raised %v on %s, %s", name, err, Show(x), Show(y))
			}
			return r
		}
		sub := check("x <: y", func() bool { return Subtype(x, y) })
		sup := check("y <: x", func() bool { return Subtype(y, x) })
		if !check("x <: x", func() bool { return Subtype(x, x) }) {
			t.Fatalf("%s <: itself is false", Show(x))
		}
		if !check("Union{} <: x", func() bool { return Subtype(Bottom, x) }) ||
			!check("x <: Any", func() bool { return Subtype(x, AnyType) }) {
			t.Fatalf("%s is not between Union{} and Any", Show(x))
		}
		if eq := check("x == y", func() bool { return TypesEqual(x, y) }); eq != (sub && sup) {
			t.Fatalf("%s == %s is %v, but <: gives %v and %v", Show(x), Show(y), eq, sub, sup)
		}
		check("isa(x, y)", func() bool { return Isa(x, y) })
		_ = Show(x) + Show(y)
	})
}

// The work bound is a mechanism test: no short query was found that reaches
// maxSubtypeCalls (julia's shortcuts, obviously_in_union and matching a
// whole union against a variable, defuse the obvious exponential shapes),
// so the bound is lowered for the test and a query known to need more
// steps must then raise, while the same query answers under the real bound.
func TestSubtypeWorkIsBounded(t *testing.T) {
	x := mustApply(AnyTupleType, TypeUnion(Int64Type, StringType), TypeUnion(Int64Type, StringType))
	T := tv("T")
	y := TypeUnionAll(T, mustApply(AnyTupleType, TypeUnion(T, NothingType), TypeUnion(T, NothingType)))
	if _, err := SubtypeChecked(x, y); err != nil {
		t.Fatalf("under the real bound: %v", err)
	}
	saved := SetMaxSubtypeSteps(3)
	defer SetMaxSubtypeSteps(saved)
	if _, err := SubtypeChecked(x, y); err == nil {
		t.Fatal("a 3-step bound did not stop the query")
	}
	start := time.Now()
	if _, err := SubtypeChecked(x, y); time.Since(start) > time.Second || err == nil {
		t.Fatalf("the bounded query took %v (err %v)", time.Since(start), err)
	}
}
