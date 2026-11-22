package interp

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/go-blas/blas"
	"github.com/go-blas/blas/lapack"
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The LAPACK shims (getrf, getrs, potrf, potrs), called as LinearAlgebra's
// ccalls call them: every argument by reference, INFO the last. The oracle is a
// 300-bit big.Float: a factorization followed by a solve satisfies, elementwise,
//
//	|A x - b| <= (gamma_{3r+2} + gamma_{2n+8}) |P^T L| |U| |x|     (LU)
//	|A x - b| <= (gamma_{3n+3} + gamma_{2n+8}) |C^T| |C| |x|       (Cholesky)
//
// (go-blas/blas/lapack's package comment, by the triangle inequality). The
// tests assert it with the k of gamma widened to 6n+12 so that the rounding of
// the bound's own evaluation has room; the bound is not bit equality with
// OpenBLAS.

// pivotArray is a pointer to a Memory of Int64 holding vals.
func pivotArray(vm *VM, vals []int64) (*Pointer, *Memory) {
	elems := make([]any, len(vals))
	for i, v := range vals {
		elems[i] = v
	}
	m := newMemory(memType(core.Int64Type), elems)
	return &Pointer{Type: vm.ptrType(core.Int64Type), Target: m}, m
}

func pivotElems(m *Memory) []int {
	out := make([]int, m.Len())
	for i, e := range m.All() {
		out[i] = int(e.(int64))
	}
	return out
}

// infoOf is the value a Ref{BlasInt} holds.
func infoOf(p *Pointer) int64 { return p.Target.(*ptrSlot).val.(int64) }

// matVec is out[i] = sum over l of |g(i, l)| * v[l], in 300 bits.
func matVec(n int, g func(i, l int) float64, v []*big.Float) []*big.Float {
	out := make([]*big.Float, n)
	for i := range out {
		out[i] = bf(0)
		for l := 0; l < n; l++ {
			out[i].Add(out[i], new(big.Float).SetPrec(300).Mul(bf(math.Abs(g(i, l))), v[l]))
		}
	}
	return out
}

func absVec[T blasFloat](x []T, off, n int) []*big.Float {
	out := make([]*big.Float, n)
	for i := range out {
		out[i] = bf(math.Abs(float64(x[off+i])))
	}
	return out
}

// residualWithin checks |sum_l a(i,l) x(l) - b| <= gamma_{6n+12} * (w + |b|) for
// one row.
func residualWithin(t *testing.T, what string, n int, u float64, a func(l int) float64, x func(l int) float64, b float64, w *big.Float) {
	t.Helper()
	sum, _ := sumOf(n, func(l int) (float64, float64) { return a(l), x(l) })
	diff := new(big.Float).SetPrec(300).Sub(sum, bf(b))
	k := float64(6*n+12) * u
	bound := new(big.Float).SetPrec(300).Mul(bf(k/(1-k)), new(big.Float).SetPrec(300).Add(w, bf(math.Abs(b))))
	if diff.Abs(diff).Cmp(bound) > 0 {
		t.Errorf("%s: residual %v over the bound %v", what, diff.Text('g', 5), bound.Text('g', 5))
	}
}

func testLapackLU[T blasFloat](t *testing.T, vm *VM, p string) {
	r := rand.New(rand.NewPCG(7, 8))
	u := unitRoundoff[T]()
	for _, n := range []int{1, 2, 3, 8, 17, 40} {
		for _, tr := range []byte{'N', 't'} {
			nrhs, lda, ldb := 2, n+2, n+3
			a0, b0 := randVals[T](r, lda*n), randVals[T](r, ldb*nrhs)
			pa, ma := blasArray(vm, a0, 0)
			pb, mb := blasArray(vm, b0, 0)
			pip, mip := pivotArray(vm, make([]int64, n))
			info := blasRef(vm, int64(-99)) // uninitialised, as julia's Ref{BlasInt}()
			if _, err := blasShim(vm, p+"getrf_64_")(vm, level3Args(vm, int64(n), int64(n), pa, int64(lda), pip, info)); err != nil {
				t.Fatal(err)
			}
			if infoOf(info) != 0 {
				t.Fatalf("%sgetrf n=%d: INFO %d", p, n, infoOf(info))
			}
			lu, piv := blasElems[T](ma), pivotElems(mip)
			for i := 0; i < lda; i++ {
				for j := 0; j < n; j++ {
					if i >= n && lu[i+j*lda] != a0[i+j*lda] {
						t.Fatalf("%sgetrf: padding of A written", p)
					}
				}
			}
			perm := make([]int, n) // row i of P A is row perm[i] of A
			for i := range perm {
				perm[i] = i
			}
			for i, pv := range piv {
				if pv < 1 || pv > n {
					t.Fatalf("%sgetrf: ipiv %v", p, piv)
				}
				perm[i], perm[pv-1] = perm[pv-1], perm[i]
			}
			if _, err := blasShim(vm, p+"getrs_64_")(vm, level3Args(vm, tr, int64(n), int64(nrhs), pa, int64(lda), pip, pb, int64(ldb), info)); err != nil {
				t.Fatal(err)
			}
			if infoOf(info) != 0 {
				t.Fatalf("%sgetrs n=%d: INFO %d", p, n, infoOf(info))
			}
			x := blasElems[T](mb)
			Lg := func(i, l int) float64 {
				switch {
				case i > l:
					return float64(lu[i+l*lda])
				case i == l:
					return 1
				}
				return 0
			}
			Ug := func(i, l int) float64 {
				if i <= l {
					return float64(lu[i+l*lda])
				}
				return 0
			}
			for j := 0; j < nrhs; j++ {
				xj := x[j*ldb : j*ldb+n]
				for i := n; i < ldb; i++ {
					if x[i+j*ldb] != b0[i+j*ldb] {
						t.Fatalf("%sgetrs: padding of B written", p)
					}
				}
				what := fmt.Sprintf("%sgetrs %c n=%d col %d", p, tr, n, j)
				if tr == 'N' {
					w := matVec(n, Lg, matVec(n, Ug, absVec(xj, 0, n)))
					for i := 0; i < n; i++ {
						residualWithin(t, what, n, u, func(l int) float64 { return float64(a0[perm[i]+l*lda]) }, func(l int) float64 { return float64(xj[l]) }, float64(b0[perm[i]+j*ldb]), w[i])
					}
					continue
				}
				// A^T = U^T L^T P: W = |U^T| (|L^T| |P x|)
				y := make([]*big.Float, n)
				for i := range y {
					y[i] = bf(math.Abs(float64(xj[perm[i]])))
				}
				w := matVec(n, func(i, k int) float64 { return Ug(k, i) }, matVec(n, func(k, l int) float64 { return Lg(l, k) }, y))
				for i := 0; i < n; i++ {
					residualWithin(t, what, n, u, func(l int) float64 { return float64(a0[l+i*lda]) }, func(l int) float64 { return float64(xj[l]) }, float64(b0[i+j*ldb]), w[i])
				}
			}
		}
	}
}

func TestLapackLU(t *testing.T) {
	vm := New()
	testLapackLU[float64](t, vm, "d")
	testLapackLU[float32](t, vm, "s")
}

func testLapackCholesky[T blasFloat](t *testing.T, vm *VM, p string) {
	r := rand.New(rand.NewPCG(9, 10))
	u := unitRoundoff[T]()
	for _, n := range []int{1, 2, 3, 8, 17, 40} {
		for _, uplo := range []byte{'U', 'l'} {
			nrhs, lda, ldb := 2, n+2, n+1
			// symmetric positive definite: M^T M + n I, built in the type of the call
			m := randVals[T](r, n*n)
			a0 := make([]T, lda*n)
			for i := 0; i < n; i++ {
				for j := 0; j <= i; j++ {
					s := 0.0
					for l := 0; l < n; l++ {
						s += float64(m[l+i*n]) * float64(m[l+j*n])
					}
					if i == j {
						s += float64(n)
					}
					a0[i+j*lda], a0[j+i*lda] = T(s), T(s)
				}
			}
			b0 := randVals[T](r, ldb*nrhs)
			pa, ma := blasArray(vm, a0, 0)
			pb, mb := blasArray(vm, b0, 0)
			info := blasRef(vm, int64(-99))
			if _, err := blasShim(vm, p+"potrf_64_")(vm, level3Args(vm, uplo, int64(n), pa, int64(lda), info)); err != nil {
				t.Fatal(err)
			}
			if infoOf(info) != 0 {
				t.Fatalf("%spotrf n=%d: INFO %d", p, n, infoOf(info))
			}
			c := blasElems[T](ma)
			upper := uplo == 'U'
			inTri := func(i, j int) bool { return (upper && i <= j) || (!upper && i >= j) }
			for i := 0; i < lda; i++ {
				for j := 0; j < n; j++ {
					if !(i < n && inTri(i, j)) && c[i+j*lda] != a0[i+j*lda] {
						t.Fatalf("%spotrf %c: (%d,%d) outside the triangle written", p, uplo, i, j)
					}
				}
			}
			if _, err := blasShim(vm, p+"potrs_64_")(vm, level3Args(vm, uplo, int64(n), int64(nrhs), pa, int64(lda), pb, int64(ldb), info)); err != nil {
				t.Fatal(err)
			}
			if infoOf(info) != 0 {
				t.Fatalf("%spotrs n=%d: INFO %d", p, n, infoOf(info))
			}
			x := blasElems[T](mb)
			// A = C^T C (upper) or C C^T (lower): W = |C^T| (|C| |x|), or |C| (|C^T| |x|)
			Cg := func(i, l int) float64 {
				if inTri(i, l) {
					return float64(c[i+l*lda])
				}
				return 0
			}
			first, second := Cg, func(i, l int) float64 { return Cg(l, i) }
			if !upper {
				first, second = second, first
			}
			for j := 0; j < nrhs; j++ {
				xj := x[j*ldb : j*ldb+n]
				w := matVec(n, second, matVec(n, first, absVec(xj, 0, n)))
				what := fmt.Sprintf("%spotrs %c n=%d col %d", p, uplo, n, j)
				for i := 0; i < n; i++ {
					residualWithin(t, what, n, u, func(l int) float64 { return float64(a0[i+l*lda]) }, func(l int) float64 { return float64(xj[l]) }, float64(b0[i+j*ldb]), w[i])
				}
			}
		}
	}
}

func TestLapackCholesky(t *testing.T) {
	vm := New()
	testLapackCholesky[float64](t, vm, "d")
	testLapackCholesky[float32](t, vm, "s")
}

// A singular or not positive definite matrix is a result: INFO > 0, the
// factorization completed (getrf) or stopped (potrf), which julia's lu and
// cholesky turn into SingularException and PosDefException.
func testLapackPositiveInfo[T blasFloat](t *testing.T, vm *VM, p string) {
	call := func(name string, args ...any) int64 {
		t.Helper()
		info := blasRef(vm, int64(-99))
		if _, err := blasShim(vm, p+name)(vm, level3Args(vm, append(args, info)...)); err != nil {
			t.Fatal(err)
		}
		return infoOf(info)
	}
	two, three := int64(2), int64(3)
	// [1 2; 2 4]: the second pivot is exactly zero
	pa, ma := blasArray(vm, []T{1, 2, 2, 4}, 0)
	pip, mip := pivotArray(vm, []int64{0, 0})
	if got := call("getrf_64_", two, two, pa, two, pip); got != 2 {
		t.Errorf("%sgetrf of a singular matrix: INFO %d, want 2", p, got)
	}
	if lu, piv := blasElems[T](ma), pivotElems(mip); lu[0] != 2 || lu[1] != 0.5 || lu[2] != 4 || lu[3] != 0 || piv[0] != 2 || piv[1] != 2 {
		t.Errorf("%sgetrf of a singular matrix: factors %v pivots %v", p, lu, piv)
	}
	// the zero matrix: the first pivot
	pz, _ := blasArray(vm, make([]T, 9), 0)
	pzp, _ := pivotArray(vm, make([]int64, 3))
	if got := call("getrf_64_", three, three, pz, three, pzp); got != 1 {
		t.Errorf("%sgetrf of zero: INFO %d, want 1", p, got)
	}
	// [1 2; 2 1] is indefinite: the leading minor of order 2
	pc, _ := blasArray(vm, []T{1, 2, 2, 1}, 0)
	if got := call("potrf_64_", uint8('U'), two, pc, two); got != 2 {
		t.Errorf("%spotrf of an indefinite matrix: INFO %d, want 2", p, got)
	}
	// a negative first diagonal: order 1, for the lower triangle too
	pn, _ := blasArray(vm, []T{-1, 0, 0, 1}, 0)
	if got := call("potrf_64_", uint8('L'), two, pn, two); got != 1 {
		t.Errorf("%spotrf of a negative diagonal: INFO %d, want 1", p, got)
	}
	// nothing to do: INFO 0, and NULL arrays are not read
	null := &Pointer{Type: vm.ptrType(core.Int64Type)}
	if got := call("getrf_64_", int64(0), int64(0), pc, int64(1), null); got != 0 {
		t.Errorf("%sgetrf of an empty matrix: INFO %d", p, got)
	}
}

func TestLapackPositiveInfo(t *testing.T) {
	vm := New()
	testLapackPositiveInfo[float64](t, vm, "d")
	testLapackPositiveInfo[float32](t, vm, "s")
}

// An illegal argument prints xerbla's line, writes nothing and leaves -Param in
// INFO, so that julia's chkargsok raises ArgumentError.
func testLapackXerbla[T blasFloat](t *testing.T, vm *VM, p string) {
	var out bytes.Buffer
	vm.Stdout = &out
	two, one := int64(2), int64(1)
	fl := func(c byte) *Pointer { return blasRef(vm, c) }
	for _, c := range []struct {
		name  string
		param int64
		args  func(a, ip, b *Pointer) []any
	}{
		{"getrf_64_", 4, func(a, ip, b *Pointer) []any { return level3Args(vm, two, two, a, one, ip) }},
		{"getrs_64_", 5, func(a, ip, b *Pointer) []any { return level3Args(vm, uint8('N'), two, one, a, one, ip, b, two) }},
		{"getrs_64_", 6, func(a, ip, b *Pointer) []any { // a pivot outside 1..n
			bad, _ := pivotArray(vm, []int64{1, 3})
			return level3Args(vm, uint8('N'), two, one, a, two, bad, b, two)
		}},
		{"potrf_64_", 1, func(a, ip, b *Pointer) []any { return []any{fl('X'), blasRef(vm, two), a, blasRef(vm, two)} }},
		{"potrs_64_", 5, func(a, ip, b *Pointer) []any { return level3Args(vm, uint8('U'), two, one, a, one, b, two) }},
	} {
		out.Reset()
		pa, ma := blasArray(vm, []T{4, 1, 1, 3}, 0)
		pb, mb := blasArray(vm, []T{9, 9, 9, 9}, 0)
		pip, mip := pivotArray(vm, []int64{7, 7})
		info := blasRef(vm, int64(-99))
		args := append(c.args(pa, pip, pb), info)
		if _, err := blasShim(vm, p+c.name)(vm, args); err != nil {
			t.Fatal(c.name, err)
		}
		routine := strings.ToUpper(p + c.name[:5])
		if want := fmt.Sprintf(" ** On entry to %-6s parameter number %2d had an illegal value\n", routine, c.param); out.String() != want {
			t.Errorf("%s%s: xerbla printed %q, want %q", p, c.name, out.String(), want)
		}
		if got := infoOf(info); got != -c.param {
			t.Errorf("%s%s: INFO %d, want %d", p, c.name, got, -c.param)
		}
		if a, b, ip := blasElems[T](ma), blasElems[T](mb), pivotElems(mip); a[0] != 4 || a[3] != 3 || b[0] != 9 || b[3] != 9 || ip[0] != 7 || ip[1] != 7 {
			t.Errorf("%s%s: written by a refused call: %v %v %v", p, c.name, a, b, ip)
		}
	}
}

func TestLapackXerbla(t *testing.T) {
	vm := New()
	testLapackXerbla[float64](t, vm, "d")
	testLapackXerbla[float32](t, vm, "s")
}

// What a call cannot make sense of is refused as not ported.
func TestLapackRefusals(t *testing.T) {
	vm := New()
	var out bytes.Buffer
	vm.Stdout = &out
	d, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	str := vm.stringPtrOf("abcd")
	piv, mp := pivotArray(vm, []int64{1, 2})
	f64Piv, _ := blasArray(vm, []float64{1, 2}, 0)
	skew := &Pointer{Type: vm.ptrType(core.Int64Type), Target: mp, Off: 3}
	i, x := blasRef(vm, int64(2)), blasRef(vm, 1.0)
	fl, bad := blasRef(vm, uint8('N')), blasRef(vm, int64(1))
	info := blasRef(vm, int64(0))
	for _, c := range []struct {
		name string
		args []any
	}{
		{"dgetrf_64_", []any{fl}},
		{"dgetrf_64_", []any{x, i, d, i, piv, info}},   // M
		{"dgetrf_64_", []any{i, i, str, i, piv, info}}, // A
		{"dgetrf_64_", []any{i, i, d, i, int64(1), info}},
		{"dgetrf_64_", []any{i, i, d, i, str, info}},
		{"dgetrf_64_", []any{i, i, d, i, f64Piv, info}},
		{"dgetrf_64_", []any{i, i, d, i, skew, info}},
		{"dgetrf_64_", []any{i, i, d, i, piv, int64(1)}}, // INFO
		{"dgetrs_64_", []any{fl}},
		{"dgetrs_64_", []any{bad, i, i, d, i, piv, d, i, info}},
		{"dgetrs_64_", []any{fl, i, i, str, i, piv, d, i, info}}, // A
		{"dgetrs_64_", []any{fl, i, i, d, i, str, d, i, info}},   // ipiv
		{"dgetrs_64_", []any{fl, i, i, d, i, piv, str, i, info}}, // B
		{"dgetrs_64_", []any{fl, i, i, d, i, piv, d, i, int64(1)}},
		{"dpotrf_64_", []any{fl}},
		{"dpotrf_64_", []any{bad, i, d, i, info}},
		{"dpotrf_64_", []any{fl, i, str, i, info}},
		{"dpotrf_64_", []any{fl, i, d, i, int64(1)}},
		{"dpotrs_64_", []any{fl}},
		{"dpotrs_64_", []any{bad, i, i, d, i, d, i, info}},
		{"dpotrs_64_", []any{fl, i, i, str, i, d, i, info}}, // A
		{"dpotrs_64_", []any{fl, i, i, d, i, str, i, info}}, // B
		{"dpotrs_64_", []any{fl, i, i, d, i, d, i, int64(1)}},
	} {
		if _, err := blasShim(vm, c.name)(vm, c.args); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %v: %v", c.name, c.args, err)
		}
	}
	// an element of ipiv that is not an Int64 reads as 0, which getrs refuses
	undef, _ := pivotArray(vm, nil)
	undef.Target = newMemory(memType(core.Int64Type), make([]any, 2))
	if _, err := blasShim(vm, "dgetrs_64_")(vm, []any{fl, i, blasRef(vm, int64(1)), d, i, undef, d, i, info}); err != nil {
		t.Fatal(err)
	}
	if infoOf(info) != -6 {
		t.Errorf("an undefined pivot: INFO %d, want -6", infoOf(info))
	}
}

// An error that is not an argument error is the caller's, whatever routine
// returned it.
func TestLapackOtherErrors(t *testing.T) {
	vm := New()
	if _, err := vm.lapackInfo(errors.New("boom")); err == nil {
		t.Error("an error that is no ArgError was swallowed")
	}
	if n, err := vm.lapackInfo(nil); n != 0 || err != nil {
		t.Errorf("no error: %d, %v", n, err)
	}
	boom := errors.New("boom")
	ops := blasDouble
	ops.getrf = func(int, int, []float64, int, []int) (int, error) { return 0, boom }
	ops.getrs = func(blas.Transpose, int, int, []float64, int, []int, []float64, int) error { return boom }
	ops.potrf = func(blas.Uplo, int, []float64, int) (int, error) { return 0, boom }
	ops.potrs = func(blas.Uplo, int, int, []float64, int, []float64, int) error { return boom }
	fns := map[expr.Symbol]shim{}
	registerLapack(fns, "d", &ops)
	a, _ := blasArray(vm, []float64{1}, 0)
	ip, _ := pivotArray(vm, []int64{1})
	one, fl, info := int64(1), uint8('U'), blasRef(vm, int64(0))
	for name, args := range map[string][]any{
		"dgetrf_64_": level3Args(vm, one, one, a, one, ip, info),
		"dgetrs_64_": level3Args(vm, fl, one, one, a, one, ip, a, one, info),
		"dpotrf_64_": level3Args(vm, fl, one, a, one, info),
		"dpotrs_64_": level3Args(vm, fl, one, one, a, one, a, one, info),
	} {
		if _, err := fns[expr.Symbol(name)](vm, args); !errors.Is(err, boom) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The writing routines log the old values of what they write (A and ipiv for
// getrf, B for getrs, A for potrf, B for potrs), so that Kernel.Reset restores
// them (#101).
func TestLapackUndo(t *testing.T) {
	vm := New()
	l := vm.undo
	l.on = true
	mem := func(vals ...float64) (*Memory, *Pointer) {
		elems := make([]any, len(vals))
		for i, v := range vals {
			elems[i] = v
		}
		m := newMemory(memF64Type, elems)
		m.undo = l
		return m, &Pointer{Type: vm.ptrType(core.Float64Type), Target: m}
	}
	pip, ipiv := pivotArray(vm, []int64{0, 0})
	ipiv.undo = l
	a, pa := mem(2, 1, 1, 3)
	b, pb := mem(1, 1, 1, 1)
	c, pc := mem(4, 1, 1, 3)
	e, pe := mem(1, 1, 1, 1)
	info := blasRef(vm, int64(0))
	for _, call := range []struct {
		name string
		args []any
	}{
		{"dgetrf_64_", level3Args(vm, int64(2), int64(2), pa, int64(2), pip, info)},
		{"dgetrs_64_", level3Args(vm, uint8('N'), int64(2), int64(1), pa, int64(2), pip, pb, int64(2), info)},
		{"dpotrf_64_", level3Args(vm, uint8('U'), int64(2), pc, int64(2), info)},
		{"dpotrs_64_", level3Args(vm, uint8('U'), int64(2), int64(1), pc, int64(2), pe, int64(2), info)},
	} {
		if _, err := blasShim(vm, call.name)(vm, call.args); err != nil {
			t.Fatal(call.name, err)
		}
	}
	if a.Get(1) == any(1.0) || b.Get(0) == any(1.0) || c.Get(0) == any(4.0) || e.Get(0) == any(1.0) || ipiv.Get(0) == any(int64(0)) {
		t.Fatalf("the calls wrote nothing: %v %v %v %v %v", a.All(), b.All(), c.All(), e.All(), ipiv.All())
	}
	l.on = false
	for i := len(l.entries) - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	for _, x := range []struct {
		m    *Memory
		want []any
	}{
		{a, []any{2.0, 1.0, 1.0, 3.0}}, {b, []any{1.0, 1.0, 1.0, 1.0}}, {c, []any{4.0, 1.0, 1.0, 3.0}},
		{e, []any{1.0, 1.0, 1.0, 1.0}}, {ipiv, []any{int64(0), int64(0)}},
	} {
		got := x.m.All()
		for i, w := range x.want {
			if got[i] != w {
				t.Errorf("after the undo: %v, want %v", got, x.want)
				break
			}
		}
	}
}

// the shims are the routines of go-blas/blas/lapack, in both precisions
func TestLapackIsGoBlas(t *testing.T) {
	vm := New()
	pa, ma := blasArray(vm, []float32{4, 2, 2, 3}, 0)
	info := blasRef(vm, int64(0))
	if _, err := blasShim(vm, "spotrf_64_")(vm, level3Args(vm, uint8('L'), int64(2), pa, int64(2), info)); err != nil {
		t.Fatal(err)
	}
	want := []float32{4, 2, 2, 3}
	if inf, err := lapack.Spotrf(blas.Lower, 2, want, 2); err != nil || inf != 0 {
		t.Fatal(inf, err)
	}
	for i, w := range want {
		if got := blasElems[float32](ma)[i]; got != w {
			t.Errorf("element %d: %v, want %v", i, got, w)
		}
	}
}

// lacpy copies all of A, its upper or its lower triangle, in both precisions,
// for an m x n that is not square and a leading dimension above m.
func testLacpy[T blasFloat](t *testing.T, name string) {
	vm := New()
	const m, n, lda, ldb = 3, 2, 4, 5
	src := make([]T, lda*n)
	for i := range src {
		src[i] = T(i + 1)
	}
	for _, c := range []struct {
		uplo byte
		in   func(i, j int) bool
	}{
		{'U', func(i, j int) bool { return i <= j }},
		{'L', func(i, j int) bool { return i >= j }},
		{'A', func(i, j int) bool { return true }},
	} {
		dst := make([]T, ldb*n)
		for i := range dst {
			dst[i] = -1
		}
		pa, _ := blasArray(vm, src, 0)
		pb, mb := blasArray(vm, dst, 0)
		if _, err := blasShim(vm, name)(vm, level3Args(vm, c.uplo, int64(m), int64(n), pa, int64(lda), pb, int64(ldb), int64(1))); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < n; j++ {
			for i := 0; i < ldb; i++ {
				want := T(-1)
				if i < m && c.in(i, j) {
					want = src[i+j*lda]
				}
				if got := mb.Get(i + j*ldb); got != any(want) {
					t.Errorf("%s %c: B[%d,%d] = %v, want %v", name, c.uplo, i, j, got, want)
				}
			}
		}
	}
}

func TestLapackLacpy(t *testing.T) {
	testLacpy[float64](t, "dlacpy_64_")
	testLacpy[float32](t, "slacpy_64_")
}

func TestLapackLacpyRefusals(t *testing.T) {
	vm := New()
	pa, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	pb, _ := blasArray(vm, []float64{0, 0, 0, 0}, 0)
	pshort, _ := blasArray(vm, []float64{0, 0}, 0)
	call := func(args ...any) error {
		_, err := blasShim(vm, "dlacpy_64_")(vm, level3Args(vm, args...))
		return err
	}
	if err := call(uint8('U'), int64(2)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("arity: %v", err)
	}
	if err := call(int64(1), int64(2), int64(2), pa, int64(2), pb, int64(2)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a flag that is not a byte: %v", err)
	}
	if err := call(uint8('U'), int64(0), int64(2), pa, int64(2), pb, int64(2)); err != nil {
		t.Errorf("an empty copy: %v", err)
	}
	if err := call(uint8('U'), int64(2), int64(2), int64(5), int64(2), pb, int64(2)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("A that is not an array: %v", err)
	}
	if err := call(uint8('U'), int64(2), int64(2), pa, int64(2), int64(5), int64(2)); !errors.Is(err, ErrUnsupported) {
		t.Errorf("B that is not an array: %v", err)
	}
	for _, c := range [][]any{
		{uint8('A'), int64(2), int64(2), pa, int64(1), pb, int64(2)}, // lda < m
		{uint8('A'), int64(2), int64(2), pa, int64(2), pb, int64(1)}, // ldb < m
		{uint8('A'), int64(2), int64(2), pshort, int64(2), pb, int64(2)},
		{uint8('A'), int64(2), int64(2), pa, int64(2), pshort, int64(2)},
	} {
		if err := call(append(c, int64(1))...); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%v: %v", c[4:], err)
		}
	}
}

// An array that is no array is refused as one, not as too short; an empty copy
// reads and writes nothing, whatever the other dimension and leading dimensions.
func TestLapackLacpyGuards(t *testing.T) {
	vm := New()
	pa, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	pb, mb := blasArray(vm, []float64{7, 7, 7, 7}, 0)
	call := func(args ...any) error {
		_, err := blasShim(vm, "dlacpy_64_")(vm, level3Args(vm, args...))
		return err
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{uint8('A'), int64(2), int64(2), int64(5), int64(2), pb, int64(2), int64(1)}, "a BLAS array"},
		{[]any{uint8('A'), int64(2), int64(2), pa, int64(2), int64(5), int64(2), int64(1)}, "a BLAS array"},
		{[]any{uint8('A'), int64(2), int64(2), pa, int64(1), pb, int64(2), int64(1)}, "arrays too short"},
	} {
		if err := call(c.args...); !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v, want a refusal naming %q", c.args[3:], err, c.want)
		}
	}
	for _, c := range [][]any{
		{uint8('A'), int64(0), int64(3), pa, int64(0), pb, int64(2), int64(1)},
		{uint8('A'), int64(2), int64(0), pa, int64(2), pb, int64(2), int64(1)},
		{uint8('A'), int64(-1), int64(3), pa, int64(2), pb, int64(2), int64(1)},
		{uint8('A'), int64(2), int64(-1), pa, int64(2), pb, int64(2), int64(1)},
	} {
		if err := call(c...); err != nil {
			t.Errorf("an empty copy %v: %v", c[1:3], err)
		}
	}
	for i := 0; i < 4; i++ {
		if mb.Get(i) != any(7.0) {
			t.Fatalf("an empty copy wrote B[%d] = %v", i, mb.Get(i))
		}
	}
}
