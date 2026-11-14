package interp

import "testing"

// The cost of one 64-bit integer intrinsic call, the interpreter's
// innermost operation: add_int, slt_int and checked_smul_int on Int64.
func BenchmarkIntIntrinsics(b *testing.B) {
	vm := New()
	add := vm.modules["Intrinsics"].own("add_int").Value
	slt := vm.modules["Intrinsics"].own("slt_int").Value
	mul := vm.modules["Intrinsics"].own("checked_smul_int").Value
	args := []any{int64(123456789), int64(987654321)}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vm.apply(add, args); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(slt, args); err != nil {
			b.Fatal(err)
		}
		if _, err := vm.apply(mul, args); err != nil {
			b.Fatal(err)
		}
	}
}
