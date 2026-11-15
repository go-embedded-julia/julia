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
