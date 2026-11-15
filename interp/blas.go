package interp

import (
	"errors"
	"fmt"
	"math"

	"github.com/go-blas/blas"
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The BLAS that libblastrampoline forwards to: julia 1.12.6's LinearAlgebra
// calls ccall((:dgemm_64_, libblastrampoline), ...) with every argument by
// reference (Ref{UInt8} flags, Ref{BlasInt} dimensions, Ref{Float64} scalars,
// Ptr{Float64} arrays) and the hidden Clong string lengths after them. The
// routines are go-blas/blas', for s and d: gemm, gemv and level 1 (dot, axpy,
// scal, nrm2, asum, iamax). The library the trampoline forwards to is named as
// julia 1.12.6 names OpenBLAS (libopenblas64_), so lbt_forward of its path
// forwards these.
//
// A Memory keeps its elements as boxed values ([]any), so a call copies the
// elements it reads and writes into []float64 (or []float32), calls the routine
// and writes back what the routine writes. That is O(n^2) for gemm, and O(n)
// for gemv and level 1: the same order as their work (see the issue on a typed
// backing store).

const openblasLib = "libopenblas64_"

type blasFloat interface{ float32 | float64 }

// blasOps are the routines of one precision.
type blasOps[T blasFloat] struct {
	elem  *core.DataType
	size  int
	gemm  func(tA, tB blas.Transpose, m, n, k int, alpha T, a []T, lda int, b []T, ldb int, beta T, c []T, ldc int) error
	gemv  func(t blas.Transpose, m, n int, alpha T, a []T, lda int, x []T, incx int, beta T, y []T, incy int) error
	dot   func(n int, x []T, incx int, y []T, incy int) (T, error)
	axpy  func(n int, alpha T, x []T, incx int, y []T, incy int) error
	scal  func(n int, alpha T, x []T, incx int) error
	nrm2  func(n int, x []T, incx int) (T, error)
	asum  func(n int, x []T, incx int) (T, error)
	iamax func(n int, x []T, incx int) (int, error)
}

var blasDouble = blasOps[float64]{
	elem: core.Float64Type, size: 8,
	gemm: blas.Dgemm, gemv: blas.Dgemv, dot: blas.Ddot, axpy: blas.Daxpy, scal: blas.Dscal,
	nrm2: blas.Dnrm2, asum: blas.Dasum, iamax: blas.Idamax,
}

var blasSingle = blasOps[float32]{
	elem: core.Float32Type, size: 4,
	gemm: blas.Sgemm, gemv: blas.Sgemv, dot: blas.Sdot, axpy: blas.Saxpy, scal: blas.Sscal,
	nrm2: blas.Snrm2, asum: blas.Sasum, iamax: blas.Isamax,
}

// blasRegion is an array argument: the memory a pointer points into, the
// element it starts at, and how many elements the call reads or writes.
type blasRegion struct {
	mem        *Memory
	start, ext int
}

// blasCall is one call's arguments, collected before any element is copied, so
// that two pointers into one memory share one buffer, as two pointers into
// one array do in C.
type blasCall[T blasFloat] struct {
	vm   *VM
	ops  *blasOps[T]
	bufs map[*Memory]*blasBuf[T]
}

type blasBuf[T blasFloat] struct {
	lo, hi  int
	data    []T
	written bool
}

// maxExt is the extent of "everything from the pointer on", for an argument
// whose extent cannot be computed (a negative leading dimension): the routine
// refuses it by its own parameter number.
const maxExt = math.MaxInt

func newBlasCall[T blasFloat](vm *VM, ops *blasOps[T]) *blasCall[T] {
	return &blasCall[T]{vm: vm, ops: ops, bufs: map[*Memory]*blasBuf[T]{}}
}

// extMul is a*b clamped to maxExt, a and b not negative.
func extMul(a, b int) int {
	if a != 0 && b > maxExt/a {
		return maxExt
	}
	return a * b
}

// vecExt is the elements a vector of n elements spaced by inc spans.
func vecExt(n, inc int) int {
	if n <= 0 {
		return 0
	}
	if inc == math.MinInt {
		return maxExt
	}
	return min(maxExt-1, extMul(n-1, max(inc, -inc))) + 1
}

// matExt is the elements a column-major matrix of rows x cols with leading
// dimension ld spans.
func matExt(rows, cols, ld int) int {
	if rows <= 0 || cols <= 0 {
		return 0
	}
	if ld < 0 {
		return maxExt
	}
	return min(maxExt-rows, extMul(cols-1, ld)) + rows
}

// scalarArg is the value of an argument passed by reference (a Ptr to the
// value) or by value.
func (c *blasCall[T]) scalarArg(v any) (any, error) {
	if p, ok := v.(*Pointer); ok {
		return c.vm.ptrLoad("ccall", p, 1)
	}
	return v, nil
}

func (c *blasCall[T]) intArg(v any) (int, error) {
	x, err := c.scalarArg(v)
	if err != nil {
		return 0, err
	}
	n, ok := x.(int64)
	if !ok {
		return 0, fmt.Errorf("%w: a BLAS dimension of %s", ErrUnsupported, showValue(x))
	}
	return int(max(min(n, math.MaxInt), math.MinInt)), nil
}

func (c *blasCall[T]) floatArg(v any) (T, error) {
	x, err := c.scalarArg(v)
	if err != nil {
		return 0, err
	}
	f, ok := x.(T)
	if !ok {
		return 0, fmt.Errorf("%w: a BLAS scalar of %s", ErrUnsupported, showValue(x))
	}
	return f, nil
}

func (c *blasCall[T]) flagArg(v any) (blas.Transpose, error) {
	x, err := c.scalarArg(v)
	if err != nil {
		return 0, err
	}
	b, ok := x.(uint8)
	if !ok {
		return 0, fmt.Errorf("%w: a BLAS flag of %s", ErrUnsupported, showValue(x))
	}
	return blas.Transpose(b), nil
}

// region is the array a pointer argument points into, ext elements of it from
// the pointer on (fewer when the memory ends first: the routine then refuses
// the argument as too short).
func (c *blasCall[T]) region(v any, ext int) (blasRegion, error) {
	p, ok := v.(*Pointer)
	if !ok {
		return blasRegion{}, fmt.Errorf("%w: a BLAS array of %s", ErrUnsupported, showValue(v))
	}
	if p.Target == nil {
		return blasRegion{}, nil // NULL: nothing to read, and the routine says so if it needs
	}
	m, ok := p.Target.(*Memory)
	if !ok || m.Type.Parameters[1] != any(c.ops.elem) || p.Off < 0 || p.Off%int64(c.ops.size) != 0 {
		return blasRegion{}, fmt.Errorf("%w: a BLAS array of %s", ErrUnsupported, showValue(v))
	}
	start := int(min(p.Off/int64(c.ops.size), int64(m.Len())))
	ext = min(ext, m.Len()-start)
	r := blasRegion{m, start, ext}
	if b := c.bufs[m]; b == nil {
		c.bufs[m] = &blasBuf[T]{lo: start, hi: start + ext}
	} else {
		b.lo, b.hi = min(b.lo, start), max(b.hi, start+ext)
	}
	return r, nil
}

// load copies the elements of every region into its buffer. An element that is
// not a number (an undefined one) reads as 0.
func (c *blasCall[T]) load() {
	for m, b := range c.bufs {
		b.data = make([]T, b.hi-b.lo)
		for i := range b.data {
			if f, ok := m.Get(b.lo + i).(T); ok {
				b.data[i] = f
			}
		}
	}
}

// view is the slice of its buffer a region is: from its start to the end of
// the buffer, so that regions of one memory overlap as they do in C.
func (c *blasCall[T]) view(r blasRegion) []T {
	if r.mem == nil {
		return nil
	}
	b := c.bufs[r.mem]
	return b.data[r.start-b.lo:]
}

// written marks a region the routine writes.
func (c *blasCall[T]) written(r blasRegion) {
	if r.mem != nil {
		c.bufs[r.mem].written = true
	}
}

// store writes back the buffers of the memories the routine wrote.
func (c *blasCall[T]) store() {
	for m, b := range c.bufs {
		if b.written {
			for i, f := range b.data {
				m.Set(b.lo+i, f)
			}
		}
	}
}

// xerbla is what OpenBLAS's xerbla prints, on stdout, for an illegal argument:
// the call then returns, with nothing written.
func (vm *VM) xerbla(err error) error {
	var ae *blas.ArgError
	if !errors.As(err, &ae) {
		return err
	}
	fmt.Fprintf(vm.Stdout, " ** On entry to %-6s parameter number %2d had an illegal value\n", ae.Routine, ae.Param)
	return nil
}

// need checks that a call has n arguments, the hidden string lengths aside.
func blasArity(name string, a []any, n int) error {
	if len(a) < n {
		return fmt.Errorf("%w: %s with %d arguments", ErrUnsupported, name, len(a))
	}
	return nil
}

// the shims of one precision, named as julia's @blasfunc names them (the
// suffix 64_ of the ILP64 interface): p is 's' or 'd'.
func registerBlas[T blasFloat](fns map[expr.Symbol]shim, p string, ops *blasOps[T]) {
	// DGEMM(TRANSA, TRANSB, M, N, K, ALPHA, A, LDA, B, LDB, BETA, C, LDC)
	fns[expr.Symbol(p+"gemm_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("gemm", a, 13); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		tA, e1 := c.flagArg(a[0])
		tB, e2 := c.flagArg(a[1])
		m, e3 := c.intArg(a[2])
		n, e4 := c.intArg(a[3])
		k, e5 := c.intArg(a[4])
		alpha, e6 := c.floatArg(a[5])
		lda, e7 := c.intArg(a[7])
		ldb, e8 := c.intArg(a[9])
		beta, e9 := c.floatArg(a[10])
		ldc, e10 := c.intArg(a[12])
		if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8, e9, e10); err != nil {
			return nil, err
		}
		ar, ac := m, k // the stored shape of A and of B
		if tA == 'T' || tA == 't' || tA == 'C' || tA == 'c' {
			ar, ac = k, m
		}
		br, bc := k, n
		if tB == 'T' || tB == 't' || tB == 'C' || tB == 'c' {
			br, bc = n, k
		}
		ra, err := c.region(a[6], matExt(ar, ac, lda))
		if err != nil {
			return nil, err
		}
		rb, err := c.region(a[8], matExt(br, bc, ldb))
		if err != nil {
			return nil, err
		}
		rc, err := c.region(a[11], matExt(m, n, ldc))
		if err != nil {
			return nil, err
		}
		c.written(rc)
		c.load()
		if err := ops.gemm(tA, tB, m, n, k, alpha, c.view(ra), lda, c.view(rb), ldb, beta, c.view(rc), ldc); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
	// DGEMV(TRANS, M, N, ALPHA, A, LDA, X, INCX, BETA, Y, INCY)
	fns[expr.Symbol(p+"gemv_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("gemv", a, 11); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		t, e1 := c.flagArg(a[0])
		m, e2 := c.intArg(a[1])
		n, e3 := c.intArg(a[2])
		alpha, e4 := c.floatArg(a[3])
		lda, e5 := c.intArg(a[5])
		incx, e6 := c.intArg(a[7])
		beta, e7 := c.floatArg(a[8])
		incy, e8 := c.intArg(a[10])
		if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8); err != nil {
			return nil, err
		}
		lx, ly := n, m
		if t != 'N' && t != 'n' {
			lx, ly = m, n
		}
		ra, err := c.region(a[4], matExt(m, n, lda))
		if err != nil {
			return nil, err
		}
		rx, err := c.region(a[6], vecExt(lx, incx))
		if err != nil {
			return nil, err
		}
		ry, err := c.region(a[9], vecExt(ly, incy))
		if err != nil {
			return nil, err
		}
		c.written(ry)
		c.load()
		if err := ops.gemv(t, m, n, alpha, c.view(ra), lda, c.view(rx), incx, beta, c.view(ry), incy); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
	// cblas_ddot(N, X, INCX, Y, INCY): by value, as julia calls it
	dot := func(vm *VM, a []any) (any, error) {
		if err := blasArity("dot", a, 5); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		n, e1 := c.intArg(a[0])
		incx, e2 := c.intArg(a[2])
		incy, e3 := c.intArg(a[4])
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, err
		}
		rx, err := c.region(a[1], vecExt(n, incx))
		if err != nil {
			return nil, err
		}
		ry, err := c.region(a[3], vecExt(n, incy))
		if err != nil {
			return nil, err
		}
		c.load()
		d, err := ops.dot(n, c.view(rx), incx, c.view(ry), incy)
		if err != nil {
			return T(0), vm.xerbla(err)
		}
		return d, nil
	}
	fns[expr.Symbol("cblas_"+p+"dot64_")] = dot
	// DAXPY(N, DA, DX, INCX, DY, INCY)
	fns[expr.Symbol(p+"axpy_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("axpy", a, 6); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		n, e1 := c.intArg(a[0])
		alpha, e2 := c.floatArg(a[1])
		incx, e3 := c.intArg(a[3])
		incy, e4 := c.intArg(a[5])
		if err := errors.Join(e1, e2, e3, e4); err != nil {
			return nil, err
		}
		rx, err := c.region(a[2], vecExt(n, incx))
		if err != nil {
			return nil, err
		}
		ry, err := c.region(a[4], vecExt(n, incy))
		if err != nil {
			return nil, err
		}
		c.written(ry)
		c.load()
		if err := ops.axpy(n, alpha, c.view(rx), incx, c.view(ry), incy); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
	// DSCAL(N, DA, DX, INCX)
	fns[expr.Symbol(p+"scal_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("scal", a, 4); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		n, e1 := c.intArg(a[0])
		alpha, e2 := c.floatArg(a[1])
		incx, e3 := c.intArg(a[3])
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, err
		}
		rx, err := c.region(a[2], vecExt(n, incx))
		if err != nil {
			return nil, err
		}
		c.written(rx)
		c.load()
		if err := ops.scal(n, alpha, c.view(rx), incx); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
	// DNRM2(N, X, INCX), DASUM(N, X, INCX) and IDAMAX(N, X, INCX)
	norm := func(name string, f func(n int, x []T, incx int) (T, error)) shim {
		return func(vm *VM, a []any) (any, error) {
			if err := blasArity(name, a, 3); err != nil {
				return nil, err
			}
			c := newBlasCall(vm, ops)
			n, e1 := c.intArg(a[0])
			incx, e2 := c.intArg(a[2])
			if err := errors.Join(e1, e2); err != nil {
				return nil, err
			}
			rx, err := c.region(a[1], vecExt(n, incx))
			if err != nil {
				return nil, err
			}
			c.load()
			r, err := f(n, c.view(rx), incx)
			if err != nil {
				return T(0), vm.xerbla(err)
			}
			return r, nil
		}
	}
	fns[expr.Symbol(p+"nrm2_64_")] = norm("nrm2", ops.nrm2)
	fns[expr.Symbol(p+"asum_64_")] = norm("asum", ops.asum)
	fns[expr.Symbol("i"+p+"amax_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("iamax", a, 3); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		n, e1 := c.intArg(a[0])
		incx, e2 := c.intArg(a[2])
		if err := errors.Join(e1, e2); err != nil {
			return nil, err
		}
		rx, err := c.region(a[1], vecExt(n, incx))
		if err != nil {
			return nil, err
		}
		c.load()
		i, err := ops.iamax(n, c.view(rx), incx)
		if err != nil {
			return int64(0), vm.xerbla(err)
		}
		return int64(i), nil
	}
}

// bootBLAS registers the BLAS of go-blas as the functions of the library
// libblastrampoline forwards to, under the name julia 1.12.6 gives OpenBLAS.
func (vm *VM) bootBLAS() {
	fns := map[expr.Symbol]shim{}
	registerBlas(fns, "d", &blasDouble)
	registerBlas(fns, "s", &blasSingle)
	vm.libShims[openblasLib] = fns
}
