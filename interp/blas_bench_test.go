package interp

import (
	"fmt"
	"testing"

	"github.com/go-blas/blas"
)

// What the shims cost over go-blas called directly (#81): dgemv, ddot and dgemm
// at n = 64, 256 and 1024, the shim on a Memory{Float64} against blas.Dgemv,
// blas.Ddot and blas.Dgemm on a []float64. The overhead is the ratio of the two.
//
//	go test ./interp -run '^$' -bench 'BLAS(Shim|Direct)' -benchmem

func benchVals(n int) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = float64(i%7) * 0.125
	}
	return v
}

func BenchmarkBLASShim(b *testing.B) {
	vm := New()
	for _, n := range []int{64, 256, 1024} {
		a, x, y := benchVals(n*n), benchVals(n), benchVals(n)
		pa, _ := blasArray(vm, a, 0)
		px, _ := blasArray(vm, x, 0)
		py, _ := blasArray(vm, y, 0)
		one, zero, ref := blasRef(vm, 1.0), blasRef(vm, 0.0), func(v int64) *Pointer { return blasRef(vm, v) }
		nn, inc := ref(int64(n)), ref(1)
		b.Run(fmt.Sprintf("dgemv n=%d", n), func(b *testing.B) {
			args := []any{blasRef(vm, uint8('N')), nn, nn, one, pa, nn, px, inc, zero, py, inc, int64(1)}
			for b.Loop() {
				if _, err := blasShim(vm, "dgemv_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("ddot n=%d", n*n), func(b *testing.B) {
			// the dot of a vector of n*n elements (the size of the matrix)
			big := benchVals(n * n)
			p1, _ := blasArray(vm, big, 0)
			p2, _ := blasArray(vm, big, 0)
			args := []any{int64(n * n), p1, int64(1), p2, int64(1)}
			for b.Loop() {
				if _, err := blasShim(vm, "cblas_ddot64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("dgemm n=%d", n), func(b *testing.B) {
			if n > 256 && testing.Short() {
				b.Skip()
			}
			pb, _ := blasArray(vm, benchVals(n*n), 0)
			pc, _ := blasArray(vm, benchVals(n*n), 0)
			args := []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), nn, nn, nn, one, pa, nn, pb, nn, zero, pc, nn, int64(1), int64(1)}
			for b.Loop() {
				if _, err := blasShim(vm, "dgemm_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBLASDirect(b *testing.B) {
	for _, n := range []int{64, 256, 1024} {
		a, x, y := benchVals(n*n), benchVals(n), benchVals(n)
		b.Run(fmt.Sprintf("dgemv n=%d", n), func(b *testing.B) {
			for b.Loop() {
				if err := blas.Dgemv(blas.NoTrans, n, n, 1, a, n, x, 1, 0, y, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("ddot n=%d", n*n), func(b *testing.B) {
			big := benchVals(n * n)
			for b.Loop() {
				if _, err := blas.Ddot(n*n, big, 1, big, 1); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("dgemm n=%d", n), func(b *testing.B) {
			bb, c := benchVals(n*n), benchVals(n*n)
			for b.Loop() {
				if err := blas.Dgemm(blas.NoTrans, blas.NoTrans, n, n, n, 1, a, n, bb, n, 0, c, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
