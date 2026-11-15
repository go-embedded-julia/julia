package core

import (
	"math"
	"testing"
)

// A float is a type parameter by its bits: Host{0.0} and Host{-0.0} are two
// types, Host{NaN} is one, and a NaN of another payload is another type. Each
// assertion fails if the type caches compare parameters by value.
func TestFloatTypeParameters(t *testing.T) {
	tn, _ := internHost("FloatHost")
	apply := func(p any) *DataType { return mustApply(tn.Wrapper, p).(*DataType) }
	negZero := math.Copysign(0, -1)
	nan := math.Float64frombits(0x7ff8000000000000)
	nanb := math.Float64frombits(0x7ff8000000000001)
	if apply(0.0) == apply(negZero) {
		t.Error("Host{0.0} is Host{-0.0}")
	}
	if apply(0.0) != apply(0.0) || apply(negZero) != apply(negZero) {
		t.Error("a float parameter is not cached")
	}
	if apply(nan) != apply(nan) {
		t.Error("Host{NaN} is not one type")
	}
	if apply(nan) == apply(nanb) {
		t.Error("two NaNs of different payloads are one type")
	}
	if apply(float32(0)) == apply(float32(negZero)) || apply(float32(nan)) != apply(float32(nan)) {
		t.Error("a Float32 parameter is not by its bits")
	}
	for _, c := range []struct {
		name string
		a, b any
		want bool
	}{
		{"0.0 and -0.0", 0.0, negZero, false},
		{"NaN", nan, nan, true},
		{"NaN and another payload", nan, nanb, false},
		{"1.5", 1.5, 1.5, true},
	} {
		if got := TypesEqual(c.a, c.b); got != c.want {
			t.Errorf("TypesEqual: %s: %v", c.name, got)
		}
		if got := obviouslyEgal(c.a, c.b); got != c.want {
			t.Errorf("obviouslyEgal: %s: %v", c.name, got)
		}
		var sub bool
		if got := obviousSubtype(c.a, c.b, c.b, &sub); got && sub != c.want {
			t.Errorf("obviousSubtype: %s: %v", c.name, sub)
		}
	}
}

// Subtyping compares float parameters by their bits: Host{0.0} is no subtype of
// Host{-0.0}, Host{NaN} is one of Host{NaN}, not of a NaN of another payload;
// in a Union, a Tuple, an invariant parameter and under a UnionAll.
func TestFloatParameterSubtyping(t *testing.T) {
	tn, _ := internHost("FloatSub")
	outer, _ := internHost("FloatSubOuter")
	host := func(p any) *DataType { return mustApply(tn.Wrapper, p).(*DataType) }
	negZero := math.Copysign(0, -1)
	nan := math.Float64frombits(0x7ff8000000000000)
	nanb := math.Float64frombits(0x7ff8000000000001)
	pairs := []struct {
		name string
		a, b any
		want bool
	}{{"0.0 and -0.0", 0.0, negZero, false}, {"-0.0 and 0.0", negZero, 0.0, false}, {"0.0", 0.0, 0.0, true},
		{"NaN", nan, nan, true}, {"NaN and a payload", nan, nanb, false}, {"a payload and NaN", nanb, nan, false},
		{"Float32 0.0 and -0.0", float32(0), float32(negZero), false}, {"Float32 NaN", float32(nan), float32(nan), true}}
	for _, c := range pairs {
		x, y := host(c.a), host(c.b)
		if got := Subtype(x, y); got != c.want {
			t.Errorf("%s: Host{a} <: Host{b} is %v", c.name, got)
		}
		if got := Subtype(TypeUnion(x, Int64Type), TypeUnion(y, Int64Type)); got != c.want {
			t.Errorf("%s: in a Union: %v", c.name, got)
		}
		if got := Subtype(TupleType(x), TupleType(y)); got != c.want {
			t.Errorf("%s: in a Tuple: %v", c.name, got)
		}
		if got := Subtype(mustApply(outer.Wrapper, x), mustApply(outer.Wrapper, y)); got != c.want {
			t.Errorf("%s: in an invariant parameter: %v", c.name, got)
		}
		tv := &TypeVar{Name: "T", LB: Bottom, UB: AnyType}
		// the intersection of two types differing in a float parameter is empty
		// exactly when the floats are two values
		if is, err := TypeIntersection(x, y); err != nil || (is == any(Bottom)) == c.want {
			t.Errorf("%s: Host{a} and Host{b} intersect in %v, %v", c.name, is, err)
		}
		if got := obviouslyDisjoint(x, y, false); got == c.want {
			t.Errorf("%s: obviouslyDisjoint is %v", c.name, got)
		}
		sameVar := TypeUnionAll(tv, TupleType(mustApply(outer.Wrapper, tv), mustApply(outer.Wrapper, tv)))
		if got := Subtype(TupleType(mustApply(outer.Wrapper, x), mustApply(outer.Wrapper, y)), sameVar); got != c.want {
			t.Errorf("%s: under a UnionAll, both bound to T: %v", c.name, got)
		}
	}
}

// Floats are egal (===) when their bits are: NaN === NaN, 0.0 !== -0.0, and a
// Float32 is no Float64.
func TestEgalFloats(t *testing.T) {
	negZero := math.Copysign(0, -1)
	nan := math.Float64frombits(0x7ff8000000000000) // julia's NaN
	for _, c := range []struct {
		name string
		a, b any
		want bool
	}{
		{"NaN", nan, nan, true},
		{"NaN32", float32(nan), float32(nan), true},
		{"a NaN with another payload", math.Float64frombits(0x7ff8000000000001), nan, false},
		{"0.0 and -0.0", 0.0, negZero, false},
		{"0.0 and 0.0", 0.0, 0.0, true},
		{"-0.0 32", float32(negZero), float32(0), false},
		{"1.5", 1.5, 1.5, true},
		{"1.5 and 2.5", 1.5, 2.5, false},
		{"a Float64 and a Float32", 1.5, float32(1.5), false},
		{"a Float32 and a Float64", float32(1.5), 1.5, false},
		{"a Float64 and an Int64", 1.0, int64(1), false},
	} {
		if got := Egal(c.a, c.b); got != c.want {
			t.Errorf("%s: Egal(%v, %v) = %v", c.name, c.a, c.b, got)
		}
	}
}
