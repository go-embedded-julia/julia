package interp

import (
	"fmt"
	"testing"

	"github.com/go-blas/blas"
)

// What the level 3 shims cost over go-blas called directly: dtrsm and dsyrk at
// n = 64, 256 and 1024, the shim on a Memory{Float64} against blas.Dtrsm and
// blas.Dsyrk on a []float64. Each round of -count runs shim and direct back to
// back (interleaved). dtrsm solves in place, so B is restored before every
// call, in both, outside the timer.
//
//	go test ./interp -run '^$' -bench 'BLAS3' -benchmem -count 5

// benchTri is a unit-diagonal triangular matrix with small entries, so that a
// solve with it stays bounded.
func benchTri(n int) []float64 {
	a := make([]float64, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			a[i+j*n] = float64((i+j)%7) * 1e-4
		}
		a[j+j*n] = 1
	}
	return a
}

func BenchmarkBLAS3(b *testing.B) {
	vm := New()
	for _, n := range []int{64, 256, 1024} {
		a, b0 := benchTri(n), benchVals(n*n)
		pa, _ := blasArray(vm, a, 0)
		pb, mb := blasArray(vm, b0, 0)
		pc, _ := blasArray(vm, benchVals(n*n), 0)
		nn := blasRef(vm, int64(n))
		one, zero := blasRef(vm, 1.0), blasRef(vm, 0.0)
		flag := func(c byte) *Pointer { return blasRef(vm, c) }
		b.Run(fmt.Sprintf("shim dtrsm n=%d", n), func(b *testing.B) {
			args := []any{flag('L'), flag('U'), flag('N'), flag('U'), nn, nn, one, pa, nn, pb, nn, int64(1), int64(1), int64(1), int64(1)}
			live, _ := mb.Float64s(0, n*n)
			for b.Loop() {
				b.StopTimer()
				copy(live, b0)
				b.StartTimer()
				if _, err := blasShim(vm, "dtrsm_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("direct dtrsm n=%d", n), func(b *testing.B) {
			x := make([]float64, n*n)
			for b.Loop() {
				b.StopTimer()
				copy(x, b0)
				b.StartTimer()
				if err := blas.Dtrsm(blas.Left, blas.Upper, blas.NoTrans, blas.Unit, n, n, 1, a, n, x, n); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("shim dsyrk n=%d", n), func(b *testing.B) {
			args := []any{flag('U'), flag('T'), nn, nn, one, pa, nn, zero, pc, nn, int64(1), int64(1)}
			for b.Loop() {
				if _, err := blasShim(vm, "dsyrk_64_")(vm, args); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("direct dsyrk n=%d", n), func(b *testing.B) {
			c := benchVals(n * n)
			for b.Loop() {
				if err := blas.Dsyrk(blas.Upper, blas.Trans, n, n, 1, a, n, 0, c, n); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
