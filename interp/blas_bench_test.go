package interp

import (
	"fmt"
	"testing"

	"github.com/go-blas/blas"
)

// What the boxed []any of a Memory costs a call: the shim (unbox, call,
// write back) against go-blas called on a []float64 directly, for the same
// operands. Run: go test ./interp -run '^$' -bench BLAS -benchmem
// (the ratios are the cost of the copy; gemm is O(n^3) work for an O(n^2)
// copy, gemv and level 1 are O(n) for O(n)).

func benchVals(n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = float64(i%17) / 16
	}
	return v
}

func BenchmarkBLASGemm(b *testing.B) {
	vm := New()
	for _, n := range []int{64, 256, 1024} {
		a, bb, c := benchVals(n*n), benchVals(n*n), make([]float64, n*n)
		b.Run(fmt.Sprintf("direct/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if err := blas.Dgemm(blas.NoTrans, blas.NoTrans, n, n, n, 1, a, n, bb, n, 0, c, n); err != nil {
					b.Fatal(err)
				}
			}
		})
		pa, _ := blasArray(vm, a, 0)
		pb, _ := blasArray(vm, bb, 0)
		pc, _ := blasArray(vm, c, 0)
		args := []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), blasRef(vm, int64(n)), blasRef(vm, int64(n)), blasRef(vm, int64(n)),
			blasRef(vm, 1.0), pa, blasRef(vm, int64(n)), pb, blasRef(vm, int64(n)), blasRef(vm, 0.0), pc, blasRef(vm, int64(n)), int64(1), int64(1)}
		b.Run(fmt.Sprintf("shim/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if _, err := blasShim(vm, "dgemm_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBLASGemv(b *testing.B) {
	vm := New()
	for _, n := range []int{256, 1024, 4096} {
		a, x, y := benchVals(n*n), benchVals(n), make([]float64, n)
		b.Run(fmt.Sprintf("direct/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if err := blas.Dgemv(blas.NoTrans, n, n, 1, a, n, x, 1, 0, y, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		pa, _ := blasArray(vm, a, 0)
		px, _ := blasArray(vm, x, 0)
		py, _ := blasArray(vm, y, 0)
		args := []any{blasRef(vm, uint8('N')), blasRef(vm, int64(n)), blasRef(vm, int64(n)), blasRef(vm, 1.0), pa, blasRef(vm, int64(n)),
			px, blasRef(vm, int64(1)), blasRef(vm, 0.0), py, blasRef(vm, int64(1)), int64(1)}
		b.Run(fmt.Sprintf("shim/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if _, err := blasShim(vm, "dgemv_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBLASDot(b *testing.B) {
	vm := New()
	for _, n := range []int{1 << 10, 1 << 16, 1 << 20} {
		x, y := benchVals(n), benchVals(n)
		b.Run(fmt.Sprintf("direct/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if _, err := blas.Ddot(n, x, 1, y, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		px, _ := blasArray(vm, x, 0)
		py, _ := blasArray(vm, y, 0)
		args := []any{int64(n), px, int64(1), py, int64(1)}
		b.Run(fmt.Sprintf("shim/n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if _, err := blasShim(vm, "cblas_ddot64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
