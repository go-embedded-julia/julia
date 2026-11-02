package interp

import "testing"

// The cost of one Float64 intrinsic call: add_float, mul_float and
// lt_float, as float.jl's arithmetic makes them.
func BenchmarkFloatIntrinsics(b *testing.B) {
	vm := New()
	add := vm.modules["Intrinsics"].own("add_float").Value
	mul := vm.modules["Intrinsics"].own("mul_float").Value
	lt := vm.modules["Intrinsics"].own("lt_float").Value
	args := []any{1.25, 3.5}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vm.apply(add, args); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(mul, args); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(lt, args); err != nil {
			b.Fatal(err)
		}
	}
}
