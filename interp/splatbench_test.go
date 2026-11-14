package interp

import "testing"

// The cost of _apply_iterate spreading a tuple and a simple vector, as
// f(xs...) lowers to.
func BenchmarkApplyIterate(b *testing.B) {
	vm := New()
	ai := vm.Core.own("_apply_iterate").Value
	tuple := vm.Core.own("tuple").Value
	args := []any{nil, tuple, vm.newTuple([]any{int64(1), int64(2), int64(3)}), SVec{int64(4), int64(5)}}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vm.apply(ai, args); err != nil {
			b.Fatal(err)
		}
	}
}
