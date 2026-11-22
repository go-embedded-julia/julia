package interp

import (
	"fmt"
	"testing"

	"github.com/go-blas/blas"
	"github.com/go-blas/blas/lapack"
)

// What the LAPACK shims cost over go-blas called directly: dgetrf and dpotrf
// at n = 64, 256 and 1024, the shim on a Memory{Float64} against
// lapack.Dgetrf and lapack.Dpotrf on a []float64. Both factor in place, so A
// is restored before every call, in both, outside the timer.
//
//	go test ./interp -run '^$' -bench 'LAPACK' -benchmem -count 5 -cpu 1

// benchDominant is a symmetric, strictly diagonally dominant matrix: positive
// definite for potrf, and a stable pivot sequence for getrf.
func benchDominant(n int) []float64 {
	a := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			a[i+j*n] = float64((i+j)%7) / float64(n)
		}
		a[j+j*n] = 2
	}
	return a
}

func BenchmarkLAPACK(b *testing.B) {
	vm := New()
	for _, n := range []int{64, 256, 1024} {
		a0 := benchDominant(n)
		pa, ma := blasArray(vm, a0, 0)
		live, _ := ma.Float64s(0, n*n)
		pv, _ := pivotArray(vm, make([]int64, n))
		nn := blasRef(vm, int64(n))
		info := blasRef(vm, int64(0))
		uplo := blasRef(vm, byte('U'))
		b.Run(fmt.Sprintf("shim dgetrf n=%d", n), func(b *testing.B) {
			args := []any{nn, nn, pa, nn, pv, info}
			for b.Loop() {
				b.StopTimer()
				copy(live, a0)
				b.StartTimer()
				if _, err := blasShim(vm, "dgetrf_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("direct dgetrf n=%d", n), func(b *testing.B) {
			x := make([]float64, n*n)
			ipiv := make([]int, n)
			for b.Loop() {
				b.StopTimer()
				copy(x, a0)
				b.StartTimer()
				if _, err := lapack.Dgetrf(n, n, x, n, ipiv); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("shim dpotrf n=%d", n), func(b *testing.B) {
			args := []any{uplo, nn, pa, nn, info}
			for b.Loop() {
				b.StopTimer()
				copy(live, a0)
				b.StartTimer()
				if _, err := blasShim(vm, "dpotrf_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("direct dpotrf n=%d", n), func(b *testing.B) {
			x := make([]float64, n*n)
			for b.Loop() {
				b.StopTimer()
				copy(x, a0)
				b.StartTimer()
				if _, err := lapack.Dpotrf(blas.Upper, n, x, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
