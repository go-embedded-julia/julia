package interp

import (
	"math"
	"math/big"
	"math/rand/v2"
	"testing"
)

// The level 3 shims (trsm, trmm, syrk, symm), checked against a 300-bit
// big.Float: trmm, syrk and symm to gamma_{k+2}(|alpha||A||B| + |beta||C|), trsm
// by its residual (a triangular solve is backward stable: |op(A) X - alpha B| <=
// gamma_{n}|op(A)||X|, with room for the rounding of the residual's own scale).

// a column-major element of an array with leading dimension ld
func at[T blasFloat](a []T, ld, i, j int) float64 { return float64(a[i+j*ld]) }

// triElem is element (i, j) of op(A) for a triangular A stored in the uplo
// triangle of a: zero outside it, one on the diagonal when unit.
func triElem[T blasFloat](a []T, lda int, uplo, tA byte, unit bool, i, j int) float64 {
	if transposed(tA) {
		i, j = j, i
	}
	if i == j && unit {
		return 1
	}
	if (uplo == 'U' && i > j) || (uplo == 'L' && i < j) {
		return 0
	}
	return at(a, lda, i, j)
}

// symElem is element (i, j) of the symmetric matrix the uplo triangle of a holds.
func symElem[T blasFloat](a []T, lda int, uplo byte, i, j int) float64 {
	if (uplo == 'U' && i > j) || (uplo == 'L' && i < j) {
		i, j = j, i
	}
	return at(a, lda, i, j)
}

// sumOf is the exact sum of x(l)*y(l) for l < k and the sum of |x(l)*y(l)|.
func sumOf(k int, get func(l int) (float64, float64)) (sum, abs *big.Float) {
	sum, abs = bf(0), bf(0)
	for l := 0; l < k; l++ {
		x, y := get(l)
		p := new(big.Float).SetPrec(300).Mul(bf(x), bf(y))
		sum.Add(sum, p)
		abs.Add(abs, p.Abs(p))
	}
	return
}

func scaled(c float64, x *big.Float) *big.Float { return new(big.Float).SetPrec(300).Mul(bf(c), x) }

// level3Args is a call's argument list with every scalar by reference.
func level3Args(vm *VM, args ...any) []any {
	out := make([]any, len(args))
	for i, a := range args {
		switch a.(type) {
		case *Pointer:
			out[i] = a
		default:
			out[i] = blasRef(vm, a)
		}
	}
	return out // the hidden string lengths are not read
}

// wellConditioned is a square matrix of order n with leading dimension ld whose
// diagonal dominates, so that a triangular solve with any of its triangles is
// well conditioned.
func wellConditioned[T blasFloat](r *rand.Rand, n, ld int) []T {
	a := randVals[T](r, ld*n)
	for i := range a {
		a[i] /= T(n)
	}
	for i := 0; i < n; i++ {
		a[i+i*ld] = T(2 + r.Float64())
	}
	return a
}

func testTriangular[T blasFloat](t *testing.T, vm *VM, name string, solve bool) {
	r := rand.New(rand.NewPCG(3, 4))
	u := unitRoundoff[T]()
	for _, sz := range [][2]int{{1, 1}, {3, 4}, {9, 5}, {17, 13}, {1, 6}, {6, 1}} {
		m, n := sz[0], sz[1]
		for _, side := range []byte{'L', 'r'} {
			na := n
			if side == 'L' {
				na = m
			}
			for _, uplo := range []byte{'U', 'l'} {
				for _, tA := range []byte{'N', 'T', 'c'} {
					for _, diag := range []byte{'N', 'u'} {
						for _, alpha := range []float64{1, -0.5} {
							lda, ldb := na+2, m+1
							a, b0 := wellConditioned[T](r, na, lda), randVals[T](r, ldb*n)
							pa, _ := blasArray(vm, a, 0)
							pb, mb := blasArray(vm, b0, 0)
							_, err := blasShim(vm, name)(vm, level3Args(vm, side, uplo, tA, diag, int64(m), int64(n), T(alpha), pa, int64(lda), pb, int64(ldb)))
							if err != nil {
								t.Fatal(err)
							}
							got := blasElems[T](mb)
							op := func(i, j int) float64 { return triElem(a, lda, uplo&^0x20, tA, diag|0x20 == 'u', i, j) }
							for j := 0; j < n; j++ {
								for i := 0; i < ldb; i++ {
									if i >= m {
										if got[i+j*ldb] != b0[i+j*ldb] {
											t.Fatalf("%s: padding of B written", name)
										}
										continue
									}
									what := name + " " + string([]byte{side, uplo, tA, diag})
									if solve {
										// residual of row i, column j: op(A) X (left) or X op(A) (right) against alpha B
										var sum, abs *big.Float
										if side == 'L' {
											sum, abs = sumOf(m, func(l int) (float64, float64) { return op(i, l), float64(got[l+j*ldb]) })
										} else {
											sum, abs = sumOf(n, func(l int) (float64, float64) { return float64(got[i+l*ldb]), op(l, j) })
										}
										want := scaled(alpha, bf(float64(b0[i+j*ldb])))
										scale := new(big.Float).SetPrec(300).Add(abs, scaled(math.Abs(alpha), bf(math.Abs(float64(b0[i+j*ldb])))))
										diff := new(big.Float).SetPrec(300).Sub(sum, want)
										gk := float64(2*na+4) * u
										bound := scaled(gk/(1-gk), scale)
										if diff.Abs(diff).Cmp(bound) > 0 {
											t.Errorf("%s: residual %v over the bound %v at (%d,%d)", what, diff.Text('g', 5), bound.Text('g', 5), i, j)
										}
										continue
									}
									var sum, abs *big.Float
									if side == 'L' {
										sum, abs = sumOf(m, func(l int) (float64, float64) { return op(i, l), float64(b0[l+j*ldb]) })
									} else {
										sum, abs = sumOf(n, func(l int) (float64, float64) { return float64(b0[i+l*ldb]), op(l, j) })
									}
									within(t, what, float64(got[i+j*ldb]), scaled(alpha, sum), scaled(math.Abs(alpha), abs), na+2, u)
								}
							}
						}
					}
				}
			}
		}
	}
}

func TestBLASTrsmTrmm(t *testing.T) {
	vm := New()
	testTriangular[float64](t, vm, "dtrsm_64_", true)
	testTriangular[float32](t, vm, "strsm_64_", true)
	testTriangular[float64](t, vm, "dtrmm_64_", false)
	testTriangular[float32](t, vm, "strmm_64_", false)
}

func testSyrk[T blasFloat](t *testing.T, vm *VM, name string) {
	r := rand.New(rand.NewPCG(5, 6))
	u := unitRoundoff[T]()
	for _, sz := range [][2]int{{1, 1}, {4, 3}, {9, 5}, {17, 13}, {5, 0}} {
		n, k := sz[0], sz[1]
		for _, uplo := range []byte{'U', 'l'} {
			for _, tr := range []byte{'N', 'T', 'c'} {
				for _, ab := range [][2]float64{{1, 0}, {0.5, 2}, {-1.5, 0.25}} {
					ar, ac := n, k
					if transposed(tr) {
						ar, ac = k, n
					}
					lda, ldc := ar+2, n+1
					a, c0 := randVals[T](r, lda*ac), randVals[T](r, ldc*n)
					pa, _ := blasArray(vm, a, 0)
					pc, mc := blasArray(vm, c0, 0)
					_, err := blasShim(vm, name)(vm, level3Args(vm, uplo, tr, int64(n), int64(k), T(ab[0]), pa, int64(lda), T(ab[1]), pc, int64(ldc)))
					if err != nil {
						t.Fatal(err)
					}
					got := blasElems[T](mc)
					elem := func(i, l int) float64 {
						if transposed(tr) {
							return at(a, lda, l, i)
						}
						return at(a, lda, i, l)
					}
					for j := 0; j < n; j++ {
						for i := 0; i < ldc; i++ {
							in := i < n && ((uplo == 'U' && i <= j) || (uplo == 'l' && i >= j))
							if !in { // the other triangle and the padding are not touched
								if got[i+j*ldc] != c0[i+j*ldc] {
									t.Fatalf("%s %c%c: C written outside its triangle at (%d,%d)", name, uplo, tr, i, j)
								}
								continue
							}
							sum, abs := sumOf(k, func(l int) (float64, float64) { return elem(i, l), elem(j, l) })
							want, scale := scaled(ab[0], sum), scaled(math.Abs(ab[0]), abs)
							if ab[1] != 0 {
								want.Add(want, scaled(ab[1], bf(at(c0, ldc, i, j))))
								scale.Add(scale, scaled(math.Abs(ab[1]), bf(math.Abs(at(c0, ldc, i, j)))))
							}
							within(t, name, float64(got[i+j*ldc]), want, scale, k+2, u)
						}
					}
				}
			}
		}
	}
}

func TestBLASSyrk(t *testing.T) {
	vm := New()
	testSyrk[float64](t, vm, "dsyrk_64_")
	testSyrk[float32](t, vm, "ssyrk_64_")
}

func testSymm[T blasFloat](t *testing.T, vm *VM, name string) {
	r := rand.New(rand.NewPCG(7, 8))
	u := unitRoundoff[T]()
	for _, sz := range [][2]int{{1, 1}, {3, 4}, {9, 5}, {17, 13}} {
		m, n := sz[0], sz[1]
		for _, side := range []byte{'L', 'r'} {
			na := n
			if side == 'L' {
				na = m
			}
			for _, uplo := range []byte{'U', 'l'} {
				for _, ab := range [][2]float64{{1, 0}, {0.5, 2}, {-1.5, 0.25}} {
					lda, ldb, ldc := na+2, m+1, m+3
					a, b, c0 := randVals[T](r, lda*na), randVals[T](r, ldb*n), randVals[T](r, ldc*n)
					pa, _ := blasArray(vm, a, 0)
					pb, _ := blasArray(vm, b, 0)
					pc, mc := blasArray(vm, c0, 0)
					_, err := blasShim(vm, name)(vm, level3Args(vm, side, uplo, int64(m), int64(n), T(ab[0]), pa, int64(lda), pb, int64(ldb), T(ab[1]), pc, int64(ldc)))
					if err != nil {
						t.Fatal(err)
					}
					got := blasElems[T](mc)
					sym := func(i, j int) float64 { return symElem(a, lda, uplo&^0x20, i, j) }
					for j := 0; j < n; j++ {
						for i := 0; i < ldc; i++ {
							if i >= m {
								if got[i+j*ldc] != c0[i+j*ldc] {
									t.Fatalf("%s: padding of C written", name)
								}
								continue
							}
							var sum, abs *big.Float
							if side == 'L' {
								sum, abs = sumOf(m, func(l int) (float64, float64) { return sym(i, l), at(b, ldb, l, j) })
							} else {
								sum, abs = sumOf(n, func(l int) (float64, float64) { return at(b, ldb, i, l), sym(l, j) })
							}
							want, scale := scaled(ab[0], sum), scaled(math.Abs(ab[0]), abs)
							if ab[1] != 0 {
								want.Add(want, scaled(ab[1], bf(at(c0, ldc, i, j))))
								scale.Add(scale, scaled(math.Abs(ab[1]), bf(math.Abs(at(c0, ldc, i, j)))))
							}
							within(t, name, float64(got[i+j*ldc]), want, scale, na+2, u)
						}
					}
				}
			}
		}
	}
}

func TestBLASSymm(t *testing.T) {
	vm := New()
	testSymm[float64](t, vm, "dsymm_64_")
	testSymm[float32](t, vm, "ssymm_64_")
}
