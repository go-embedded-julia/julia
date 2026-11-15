package core

import (
	"math"
	"testing"
)

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
