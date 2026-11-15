package interp

import (
	"bytes"
	"errors"
	"math"
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The BLAS shims, called as LinearAlgebra's ccalls call them: every argument by
// reference, arrays as pointers into a Memory. The oracle is a 300-bit
// big.Float; the contract is go-blas' stated bound (gamma_{k+2}(|alpha||A||B| +
// |beta||C|) for gemm and its analogues), not bit equality.

func blasShim(vm *VM, name string) shim { return vm.libShims[openblasLib][expr.Symbol(name)] }

func blasMemory[T blasFloat](vals []T) (*Memory, any, int) {
	var et any = core.Float64Type
	size := 8
	if _, ok := any(T(0)).(float32); ok {
		et, size = core.Float32Type, 4
	}
	elems := make([]any, len(vals))
	for i, v := range vals {
		elems[i] = v
	}
	return &Memory{Type: memType(et), Elems: elems}, et, size
}

// blasArray is a pointer to element i of a memory of vals.
func blasArray[T blasFloat](vm *VM, vals []T, i int) (*Pointer, *Memory) {
	m, et, size := blasMemory(vals)
	return &Pointer{Type: vm.ptrType(et), Target: m, Off: int64(i * size)}, m
}

// blasRef is a Ref{T}: a pointer to one value.
func blasRef(vm *VM, v any) *Pointer {
	return &Pointer{Type: vm.ptrType(vm.typeOf(v)), Target: &ptrSlot{val: v}}
}

func blasElems[T blasFloat](m *Memory) []T {
	out := make([]T, len(m.Elems))
	for i, e := range m.Elems {
		out[i], _ = e.(T)
	}
	return out
}

func randVals[T blasFloat](r *rand.Rand, n int) []T {
	v := make([]T, n)
	for i := range v {
		v[i] = T(r.Float64()*2 - 1)
	}
	return v
}

func unitRoundoff[T blasFloat]() float64 {
	if _, ok := any(T(0)).(float32); ok {
		return math.Ldexp(1, -24)
	}
	return math.Ldexp(1, -53)
}

func bf(x float64) *big.Float { return new(big.Float).SetPrec(300).SetFloat64(x) }

// within checks |got - want| <= gamma_k * scale, all in 300 bits, and names
// the element when it is not.
func within(t *testing.T, what string, got float64, want, scale *big.Float, k int, u float64) {
	t.Helper()
	ku := float64(k) * u
	gamma := bf(ku / (1 - ku))
	bound := new(big.Float).SetPrec(300).Mul(gamma, scale)
	diff := new(big.Float).SetPrec(300).Sub(bf(got), want)
	if diff.Abs(diff).Cmp(bound) > 0 {
		t.Errorf("%s: got %v, want %v, bound %v", what, got, want.Text('g', 20), bound.Text('g', 5))
	}
}

func transposed(f byte) bool { return f == 'T' || f == 't' || f == 'C' || f == 'c' }

func testGemm[T blasFloat](t *testing.T, vm *VM, name string) {
	r := rand.New(rand.NewPCG(1, 2))
	u := unitRoundoff[T]()
	for _, sz := range [][3]int{{1, 1, 1}, {3, 4, 5}, {7, 5, 9}, {17, 13, 19}, {70, 66, 65}} {
		m, n, k := sz[0], sz[1], sz[2]
		for _, fl := range [][2]byte{{'N', 'N'}, {'T', 'N'}, {'N', 't'}, {'C', 'T'}} {
			for _, ab := range [][2]float64{{1, 0}, {0.5, 2}, {0, 1}, {-1.5, 0.25}} {
				ar, ac := m, k
				if transposed(fl[0]) {
					ar, ac = k, m
				}
				br, bc := k, n
				if transposed(fl[1]) {
					br, bc = n, k
				}
				lda, ldb, ldc := ar+2, br+1, m+3
				a, b, c0 := randVals[T](r, lda*ac), randVals[T](r, ldb*bc), randVals[T](r, ldc*n)
				pa, _ := blasArray(vm, a, 0)
				pb, _ := blasArray(vm, b, 0)
				pc, mc := blasArray(vm, c0, 0)
				_, err := blasShim(vm, name)(vm, []any{
					blasRef(vm, fl[0]), blasRef(vm, fl[1]), blasRef(vm, int64(m)), blasRef(vm, int64(n)), blasRef(vm, int64(k)),
					blasRef(vm, T(ab[0])), pa, blasRef(vm, int64(lda)), pb, blasRef(vm, int64(ldb)), blasRef(vm, T(ab[1])), pc, blasRef(vm, int64(ldc)),
					int64(1), int64(1)})
				if err != nil {
					t.Fatal(err)
				}
				got := blasElems[T](mc)
				for j := 0; j < n; j++ {
					for i := 0; i < ldc; i++ {
						if i >= m { // the padding of C is not touched
							if got[i+j*ldc] != c0[i+j*ldc] {
								t.Fatalf("%s %c%c %v: padding of C written", name, fl[0], fl[1], ab)
							}
							continue
						}
						sum, abs := bf(0), bf(0)
						for l := 0; l < k; l++ {
							var x, y T
							if transposed(fl[0]) {
								x = a[l+i*lda]
							} else {
								x = a[i+l*lda]
							}
							if transposed(fl[1]) {
								y = b[j+l*ldb]
							} else {
								y = b[l+j*ldb]
							}
							p := new(big.Float).SetPrec(300).Mul(bf(float64(x)), bf(float64(y)))
							sum.Add(sum, p)
							abs.Add(abs, p.Abs(p))
						}
						want := new(big.Float).SetPrec(300).Mul(bf(ab[0]), sum)
						scale := new(big.Float).SetPrec(300).Mul(bf(math.Abs(ab[0])), abs)
						if ab[1] != 0 {
							want.Add(want, new(big.Float).SetPrec(300).Mul(bf(ab[1]), bf(float64(c0[i+j*ldc]))))
							scale.Add(scale, new(big.Float).SetPrec(300).Mul(bf(math.Abs(ab[1])), bf(math.Abs(float64(c0[i+j*ldc])))))
						}
						within(t, name, float64(got[i+j*ldc]), want, scale, k+2, u)
					}
				}
			}
		}
	}
}

func TestBLASGemm(t *testing.T) {
	vm := New()
	testGemm[float64](t, vm, "dgemm_64_")
	testGemm[float32](t, vm, "sgemm_64_")
}

func testGemv[T blasFloat](t *testing.T, vm *VM, name string) {
	r := rand.New(rand.NewPCG(3, 4))
	u := unitRoundoff[T]()
	for _, sz := range [][2]int{{1, 1}, {5, 3}, {4, 9}, {33, 40}} {
		m, n := sz[0], sz[1]
		for _, tr := range []byte{'N', 'T', 'c'} {
			for _, inc := range [][2]int{{1, 1}, {2, -1}, {-3, 2}} {
				for _, ab := range [][2]float64{{1, 0}, {-0.5, 3}} {
					lx, ly := n, m
					if tr != 'N' {
						lx, ly = m, n
					}
					lda := m + 2
					a := randVals[T](r, lda*n)
					x := randVals[T](r, 1+(lx-1)*max(inc[0], -inc[0]))
					y0 := randVals[T](r, 1+(ly-1)*max(inc[1], -inc[1]))
					pa, _ := blasArray(vm, a, 0)
					px, _ := blasArray(vm, x, 0)
					py, my := blasArray(vm, y0, 0)
					_, err := blasShim(vm, name)(vm, []any{blasRef(vm, tr), blasRef(vm, int64(m)), blasRef(vm, int64(n)),
						blasRef(vm, T(ab[0])), pa, blasRef(vm, int64(lda)), px, blasRef(vm, int64(inc[0])),
						blasRef(vm, T(ab[1])), py, blasRef(vm, int64(inc[1])), int64(1)})
					if err != nil {
						t.Fatal(err)
					}
					got := blasElems[T](my)
					for i := 0; i < ly; i++ {
						sum, abs := bf(0), bf(0)
						for l := 0; l < lx; l++ {
							var aij T
							if tr != 'N' {
								aij = a[l+i*lda]
							} else {
								aij = a[i+l*lda]
							}
							xl := l
							if inc[0] < 0 {
								xl = lx - 1 - l
							}
							p := new(big.Float).SetPrec(300).Mul(bf(float64(aij)), bf(float64(x[xl*max(inc[0], -inc[0])])))
							sum.Add(sum, p)
							abs.Add(abs, p.Abs(p))
						}
						yi := i
						if inc[1] < 0 {
							yi = ly - 1 - i
						}
						yi *= max(inc[1], -inc[1])
						want := new(big.Float).SetPrec(300).Mul(bf(ab[0]), sum)
						scale := new(big.Float).SetPrec(300).Mul(bf(math.Abs(ab[0])), abs)
						if ab[1] != 0 {
							want.Add(want, new(big.Float).SetPrec(300).Mul(bf(ab[1]), bf(float64(y0[yi]))))
							scale.Add(scale, new(big.Float).SetPrec(300).Mul(bf(math.Abs(ab[1])), bf(math.Abs(float64(y0[yi])))))
						}
						within(t, name, float64(got[yi]), want, scale, lx+2, u)
					}
				}
			}
		}
	}
}

func TestBLASGemv(t *testing.T) {
	vm := New()
	testGemv[float64](t, vm, "dgemv_64_")
	testGemv[float32](t, vm, "sgemv_64_")
}

func testLevel1[T blasFloat](t *testing.T, vm *VM, p string) {
	r := rand.New(rand.NewPCG(5, 6))
	u := unitRoundoff[T]()
	for _, n := range []int{1, 2, 7, 100} {
		for _, inc := range [][2]int{{1, 1}, {2, 3}} {
			x, y0 := randVals[T](r, 1+(n-1)*inc[0]), randVals[T](r, 1+(n-1)*inc[1])
			px, mx := blasArray(vm, x, 0)
			py, my := blasArray(vm, y0, 0)
			alpha := T(0.75)
			// dot: by value, as cblas_ddot is called
			d, err := blasShim(vm, "cblas_"+p+"dot64_")(vm, []any{int64(n), px, int64(inc[0]), py, int64(inc[1])})
			if err != nil {
				t.Fatal(err)
			}
			sum, abs := bf(0), bf(0)
			for i := 0; i < n; i++ {
				q := new(big.Float).SetPrec(300).Mul(bf(float64(x[i*inc[0]])), bf(float64(y0[i*inc[1]])))
				sum.Add(sum, q)
				abs.Add(abs, q.Abs(q))
			}
			within(t, p+"dot", float64(d.(T)), sum, abs, n, u)
			// axpy
			if _, err := blasShim(vm, p+"axpy_64_")(vm, []any{blasRef(vm, int64(n)), blasRef(vm, alpha), px, blasRef(vm, int64(inc[0])), py, blasRef(vm, int64(inc[1])), nil}); err != nil {
				t.Fatal(err)
			}
			got := blasElems[T](my)
			for i := 0; i < n; i++ {
				ax := new(big.Float).SetPrec(300).Mul(bf(float64(alpha)), bf(float64(x[i*inc[0]])))
				want := new(big.Float).SetPrec(300).Add(ax, bf(float64(y0[i*inc[1]])))
				scale := new(big.Float).SetPrec(300).Add(ax.Abs(ax), bf(math.Abs(float64(y0[i*inc[1]]))))
				within(t, p+"axpy", float64(got[i*inc[1]]), want, scale, 2, u)
			}
			// scal
			if _, err := blasShim(vm, p+"scal_64_")(vm, []any{blasRef(vm, int64(n)), blasRef(vm, alpha), px, blasRef(vm, int64(inc[0]))}); err != nil {
				t.Fatal(err)
			}
			gx := blasElems[T](mx)
			for i := 0; i < n; i++ {
				want := new(big.Float).SetPrec(300).Mul(bf(float64(alpha)), bf(float64(x[i*inc[0]])))
				within(t, p+"scal", float64(gx[i*inc[0]]), want, new(big.Float).SetPrec(300).Abs(want), 1, u)
			}
			// nrm2, asum, iamax on the scaled x
			nr, err := blasShim(vm, p+"nrm2_64_")(vm, []any{blasRef(vm, int64(n)), px, blasRef(vm, int64(inc[0]))})
			if err != nil {
				t.Fatal(err)
			}
			ss := bf(0)
			as, best, bi := bf(0), 0.0, 0
			for i := 0; i < n; i++ {
				v := float64(gx[i*inc[0]])
				ss.Add(ss, new(big.Float).SetPrec(300).Mul(bf(v), bf(v)))
				as.Add(as, bf(math.Abs(v)))
				if math.Abs(v) > best {
					best, bi = math.Abs(v), i+1
				}
			}
			root := new(big.Float).SetPrec(300).Sqrt(ss)
			within(t, p+"nrm2", float64(nr.(T)), root, root, n+2, u)
			asum, err := blasShim(vm, p+"asum_64_")(vm, []any{blasRef(vm, int64(n)), px, blasRef(vm, int64(inc[0]))})
			if err != nil {
				t.Fatal(err)
			}
			within(t, p+"asum", float64(asum.(T)), as, as, n, u)
			im, err := blasShim(vm, "i"+p+"amax_64_")(vm, []any{blasRef(vm, int64(n)), px, blasRef(vm, int64(inc[0]))})
			if err != nil || im != int64(bi) {
				t.Errorf("i%samax: %v, %v, want %d", p, im, err, bi)
			}
		}
	}
}

func TestBLASLevel1(t *testing.T) {
	vm := New()
	testLevel1[float64](t, vm, "d")
	testLevel1[float32](t, vm, "s")
}

// An illegal argument prints what OpenBLAS's xerbla prints (the line julia
// 1.12.6 prints for the same call, on stdout), returns, and writes nothing.
func TestBLASXerbla(t *testing.T) {
	vm := New()
	var out bytes.Buffer
	vm.Stdout = &out
	a, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	b, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	c, mc := blasArray(vm, []float64{9, 9, 9, 9}, 0)
	gemm := func(lda int64) {
		_, err := blasShim(vm, "dgemm_64_")(vm, []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), blasRef(vm, int64(2)), blasRef(vm, int64(2)), blasRef(vm, int64(2)),
			blasRef(vm, 1.0), a, blasRef(vm, lda), b, blasRef(vm, int64(2)), blasRef(vm, 0.0), c, blasRef(vm, int64(2)), int64(1), int64(1)})
		if err != nil {
			t.Fatal(err)
		}
	}
	gemm(1)
	if want := " ** On entry to DGEMM  parameter number  8 had an illegal value\n"; out.String() != want {
		t.Errorf("xerbla printed %q, want %q", out.String(), want)
	}
	if got := blasElems[float64](mc); got[0] != 9 || got[3] != 9 {
		t.Errorf("C written by a refused call: %v", got)
	}
	// an array shorter than the call reads: refused against its own parameter
	out.Reset()
	short, _ := blasArray(vm, []float64{1, 2, 3}, 0)
	_, err := blasShim(vm, "dgemm_64_")(vm, []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), blasRef(vm, int64(2)), blasRef(vm, int64(2)), blasRef(vm, int64(2)),
		blasRef(vm, 1.0), short, blasRef(vm, int64(2)), b, blasRef(vm, int64(2)), blasRef(vm, 0.0), c, blasRef(vm, int64(2)), int64(1), int64(1)})
	if err != nil || !strings.Contains(out.String(), "DGEMM  parameter number  7 ") {
		t.Errorf("a short A: %v, %q", err, out.String())
	}
	// a negative leading dimension is the LDA's; a huge one is a valid one
	// that the array is too short for, so the array's
	out.Reset()
	gemm(-5)
	gemm(math.MaxInt64)
	if strings.Count(out.String(), "parameter number  8 ") != 1 || strings.Count(out.String(), "parameter number  7 ") != 1 {
		t.Errorf("lda -5 and 2^63-1: %q", out.String())
	}
	// the single precision routines refuse the same way
	out.Reset()
	f32, _ := blasArray(vm, []float32{1, 2}, 0)
	for name, args := range map[string][]any{
		"sscal_64_":       {blasRef(vm, int64(2)), blasRef(vm, float32(2)), f32, blasRef(vm, int64(5))},
		"cblas_sdot64_":   {int64(2), f32, int64(5), f32, int64(1)},
		"snrm2_64_":       {blasRef(vm, int64(2)), f32, blasRef(vm, int64(5))},
		"sasum_64_":       {blasRef(vm, int64(2)), f32, blasRef(vm, int64(5))},
		"isamax_64_":      {blasRef(vm, int64(2)), f32, blasRef(vm, int64(5))},
		"saxpy_64_":       {blasRef(vm, int64(2)), blasRef(vm, float32(1)), f32, blasRef(vm, int64(5)), f32, blasRef(vm, int64(1))},
		"sgemv_64_":       {blasRef(vm, uint8('N')), blasRef(vm, int64(3)), blasRef(vm, int64(2)), blasRef(vm, float32(1)), f32, blasRef(vm, int64(2)), f32, blasRef(vm, int64(1)), blasRef(vm, float32(0)), f32, blasRef(vm, int64(1))},
		"cblas_sdot64_ n": {int64(2), f32, int64(1), f32, int64(-5)},
	} {
		name, _, _ = strings.Cut(name, " ")
		if _, err := blasShim(vm, name)(vm, args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := strings.Count(out.String(), " ** On entry to "); n != 8 {
		t.Errorf("%d xerbla lines for 8 refused calls: %q", n, out.String())
	}
	// an error that is no ArgError is returned as it is
	if err := vm.xerbla(errors.New("other")); err == nil || err.Error() != "other" {
		t.Errorf("xerbla of another error: %v", err)
	}
}

// What a call cannot make sense of is refused as not ported, not misread.
func TestBLASRefusals(t *testing.T) {
	vm := New()
	d, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	f, _ := blasArray(vm, []float32{1, 2, 3, 4}, 0)
	odd := &Pointer{Type: vm.ptrType(core.Float64Type), Target: d.Target, Off: 4}
	neg := &Pointer{Type: vm.ptrType(core.Float64Type), Target: d.Target, Off: -8}
	str := vm.stringPtrOf("abcd")
	i, x := blasRef(vm, int64(2)), blasRef(vm, 1.0)
	one := blasRef(vm, int64(1))
	bad := &Pointer{Type: vm.ptrType(core.Int64Type), Target: 3}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"dscal_64_", []any{i, x}},
		{"dscal_64_", []any{blasRef(vm, int32(2)), x, d, one}},   // a dimension that is no BlasInt
		{"dscal_64_", []any{i, blasRef(vm, float32(1)), d, one}}, // a scalar of another precision
		{"dscal_64_", []any{i, x, f, one}},                       // an array of another precision
		{"dscal_64_", []any{i, x, odd, one}},                     // between elements
		{"dscal_64_", []any{i, x, neg, one}},                     // before the memory
		{"dscal_64_", []any{i, x, str, one}},                     // no memory
		{"dscal_64_", []any{i, x, int64(1), one}},                // no pointer
		{"dgemm_64_", []any{blasRef(vm, int64(1)), blasRef(vm, uint8('N'))}},
		{"dgemm_64_", []any{blasRef(vm, int64(1)), blasRef(vm, uint8('N')), i, i, i, x, d, i, d, i, x, d, i}},     // a flag that is no byte
		{"dgemm_64_", []any{blasRef(vm, uint8('N')), blasRef(vm, int64(1)), i, i, i, x, d, i, d, i, x, d, i}},     // the second
		{"dgemm_64_", []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), i, i, i, x, str, i, d, i, x, d, i}}, // A
		{"dgemm_64_", []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), i, i, i, x, d, i, str, i, x, d, i}}, // B
		{"dgemm_64_", []any{blasRef(vm, uint8('N')), blasRef(vm, uint8('N')), i, i, i, x, d, i, d, i, x, str, i}}, // C
		{"dgemv_64_", []any{blasRef(vm, uint8('N')), i, i, x, str, i, d, i, x, d, i}},
		{"dgemv_64_", []any{blasRef(vm, uint8('N')), i, i, x, d, i, str, i, x, d, i}},
		{"dgemv_64_", []any{blasRef(vm, uint8('N')), i, i, x, d, i, d, i, x, str, i}},
		{"dgemv_64_", []any{blasRef(vm, uint8('N'))}},
		{"cblas_ddot64_", []any{int64(1)}},
		{"cblas_ddot64_", []any{int64(1), str, int64(1), d, int64(1)}},
		{"cblas_ddot64_", []any{int64(1), d, int64(1), str, int64(1)}},
		{"cblas_ddot64_", []any{float64(1), d, int64(1), d, int64(1)}},
		{"daxpy_64_", []any{i}},
		{"daxpy_64_", []any{i, x, str, one, d, one}},
		{"daxpy_64_", []any{i, x, d, one, str, one}},
		{"dnrm2_64_", []any{i}},
		{"dnrm2_64_", []any{i, str, one}},
		{"dasum_64_", []any{i, d, blasRef(vm, int32(1))}},
		{"idamax_64_", []any{i}},
		{"idamax_64_", []any{i, str, one}},
		{"idamax_64_", []any{i, d, blasRef(vm, int32(1))}},
		{"dscal_64_", []any{i}},
		{"dgemv_64_", []any{blasRef(vm, uint8('N')), blasRef(vm, int32(2)), i, x, d, i, d, i, x, d, i}}, // a dimension that is no BlasInt
		{"daxpy_64_", []any{blasRef(vm, int32(2)), x, d, one, d, one}},
	} {
		if _, err := blasShim(vm, c.name)(vm, c.args); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %v: %v", c.name, c.args, err)
		}
	}
	// a Ref that points at nothing readable is julia's invalid pointer error
	for _, c := range []struct {
		name string
		args []any
	}{
		{"dscal_64_", []any{bad, x, d, one}},
		{"dscal_64_", []any{i, bad, d, one}},
		{"dgemm_64_", []any{bad, blasRef(vm, uint8('N')), i, i, i, x, d, i, d, i, x, d, i}},
	} {
		if _, err := blasShim(vm, c.name)(vm, c.args); err == nil || !strings.Contains(err.Error(), "invalid pointer") {
			t.Errorf("%s of a bad Ref: %v", c.name, err)
		}
	}
	// a NULL array with nothing to read is no error; undefined elements read as 0
	null := &Pointer{Type: vm.ptrType(core.Float64Type)}
	if r, err := blasShim(vm, "dnrm2_64_")(vm, []any{blasRef(vm, int64(0)), null, one}); err != nil || r != 0.0 {
		t.Errorf("dnrm2 of nothing: %v, %v", r, err)
	}
	m, _, _ := blasMemory([]float64{1, 2})
	m.Elems[0] = undef
	if r, err := blasShim(vm, "dasum_64_")(vm, []any{i, &Pointer{Type: vm.ptrType(core.Float64Type), Target: m}, one}); err != nil || r != 2.0 {
		t.Errorf("dasum of an undefined element and 2: %v, %v", r, err)
	}
}

// Two pointers into one memory share one buffer, as two pointers into one
// array do: y := y + 2x with x the first and y the second half of a memory, and
// with x = y, which is y := 3y.
func TestBLASAliasing(t *testing.T) {
	vm := New()
	px, m := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	py := &Pointer{Type: px.Type, Target: m, Off: 16}
	axpy := func(x, y *Pointer) {
		if _, err := blasShim(vm, "daxpy_64_")(vm, []any{blasRef(vm, int64(2)), blasRef(vm, 2.0), x, blasRef(vm, int64(1)), y, blasRef(vm, int64(1))}); err != nil {
			t.Fatal(err)
		}
	}
	axpy(px, py)
	if got := blasElems[float64](m); got[0] != 1 || got[2] != 5 || got[3] != 8 {
		t.Errorf("y := y + 2x over halves of one memory: %v", got)
	}
	axpy(py, py)
	if got := blasElems[float64](m); got[2] != 15 || got[3] != 24 {
		t.Errorf("x = y: %v", got)
	}
}

// The extent of an argument is clamped, never overflowed: a huge increment, a
// huge leading dimension, and the most negative increment.
func TestBLASExtents(t *testing.T) {
	for _, c := range []struct {
		name      string
		got, want int
	}{
		{"no elements", vecExt(0, 5), 0},
		{"one element", vecExt(1, math.MaxInt/2), 1},
		{"stride 3", vecExt(4, 3), 10},
		{"negative stride", vecExt(4, -3), 10},
		{"the most negative stride", vecExt(2, math.MinInt), maxExt},
		{"overflowing stride", vecExt(math.MaxInt/2, 3), maxExt},
		{"no matrix", matExt(0, 3, 2), 0},
		{"2 x 3, ld 5", matExt(2, 3, 5), 12},
		{"a negative ld", matExt(2, 3, -1), maxExt},
		{"overflowing ld", matExt(2, math.MaxInt/2, 3), maxExt},
	} {
		if c.got != c.want {
			t.Errorf("%s: %d, want %d", c.name, c.got, c.want)
		}
	}
	if extMul(0, maxExt) != 0 || extMul(2, 3) != 6 {
		t.Error("extMul")
	}
}
