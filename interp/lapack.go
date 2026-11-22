package interp

import (
	"errors"
	"fmt"

	"github.com/go-blas/blas"
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The LAPACK libblastrampoline forwards to go-blas/blas/lapack, for s and d: the
// LU and Cholesky factorizations getrf and potrf and the solves getrs and potrs,
// and lacpy, the copy Cholesky's setup makes.
// They follow the shape of the BLAS shims (blas.go): every argument by reference,
// the written array recorded for the undo log by load, a refused argument printed
// as xerbla does. What LAPACK adds is INFO, the last argument, which julia
// reads back (stdlib/LinearAlgebra/src/lapack.jl): getrf! ends in
// chkargsok(info[]) (line 591), potrf! in chkargsok(info[]) (line 3296) and
// returns info[] for the Cholesky object to keep, getrs! and potrs! in
// chklapackerror(info[]) (lines 1060 and 3348). So the shim writes INFO on every
// call, since julia allocates it uninitialised (Ref{BlasInt}()):
//
//   - 0 when the routine ran, and for a solve whatever its factor holds (a zero
//     on the diagonal of U is not tested for, as in the reference);
//   - i > 0 for a factorization whose result is singular (getrf: U(i,i) is
//     exactly zero, the factorization being completed) or not positive definite
//     (potrf: the leading minor of order i), which julia's lu and cholesky turn
//     into SingularException and PosDefException (chknonsingular, chkposdef,
//     lapack.jl lines 46 and 52);
//   - -p for an illegal parameter p, with xerbla's line printed and nothing
//     written, as OpenBLAS's interface/lapack/*.c does (xerbla, then *Info = -info);
//     julia's chkargsok then raises ArgumentError.
//
// The pivots ipiv are a Vector{BlasInt}, a Memory of Int64 that keeps boxed
// values: getrf writes them (the old ones recorded first for the undo log, by
// Memory.Set), getrs reads them, 1-based as in LAPACK and as go-blas keeps them.

// pivots is an Int64 array argument: the memory a pointer points into, the
// element it starts at and how many elements the call reads or writes.
type pivots struct {
	mem        *Memory
	start, ext int
}

// pivotArg is the array of BlasInt a pointer argument points into, ext elements
// of it from the pointer on (fewer when the memory ends first: the routine then
// refuses the argument as too short). NULL gives an empty array.
func pivotArg(v any, ext int) (pivots, error) {
	p, ok := v.(*Pointer)
	if !ok {
		return pivots{}, fmt.Errorf("%w: a LAPACK pivot array of %s", ErrUnsupported, showValue(v))
	}
	if p.Target == nil {
		return pivots{}, nil
	}
	m, ok := p.Target.(*Memory)
	if !ok || m.Type.Parameters[1] != any(core.Int64Type) || p.Off < 0 || p.Off%8 != 0 {
		return pivots{}, fmt.Errorf("%w: a LAPACK pivot array of %s", ErrUnsupported, showValue(v))
	}
	start := int(min(p.Off/8, int64(m.Len())))
	return pivots{m, start, max(0, min(ext, m.Len()-start))}, nil
}

// read is the pivots as ints; an element that is not an Int64 reads as 0.
func (p pivots) read() []int {
	out := make([]int, p.ext)
	for i := range out {
		if v, ok := p.mem.Get(p.start + i).(int64); ok {
			out[i] = int(v)
		}
	}
	return out
}

// write stores ipiv as the elements of the array, through Memory.Set, which logs
// the old value for Kernel.Reset.
func (p pivots) write(ipiv []int) {
	for i, v := range ipiv {
		p.mem.Set(p.start+i, int64(v))
	}
}

// pivotExt is the number of pivots of a getrf of an m x n matrix.
func pivotExt(m, n int) int { return max(0, min(m, n)) }

// lapackInfo is INFO of a call that returned err: 0 for none, and for a refused
// argument the xerbla line is printed and INFO is -Param. Any other error is the
// caller's to return.
func (vm *VM) lapackInfo(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	if e := vm.xerbla(err); e != nil {
		return 0, e
	}
	var ae *blas.ArgError
	errors.As(err, &ae) // xerbla returned nil: it is one
	return -ae.Param, nil
}

// setInfo writes INFO through the pointer argument that holds it.
func (vm *VM) setInfo(v any, info int) error {
	p, ok := v.(*Pointer)
	if !ok {
		return fmt.Errorf("%w: a LAPACK INFO of %s", ErrUnsupported, showValue(v))
	}
	return vm.ptrStore("ccall", p, int64(info), 1)
}

// registerLapack registers the LAPACK shims of one precision: p is 's' or 'd'.
func registerLapack[T blasFloat](fns map[expr.Symbol]shim, p string, ops *blasOps[T]) {
	// DLACPY(UPLO, M, N, A, LDA, B, LDB), the copy of all of A, its upper or its
	// lower triangle into B. Cholesky's copy (copytrito!, lapack.jl lacpy!) calls it;
	// go-blas has no lacpy and a copy has no rounding, so it is written here.
	// Reference LAPACK checks nothing: arrays too short for their leading
	// dimension are refused instead of read out of bounds.
	fns[expr.Symbol(p+"lacpy_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("lacpy", a, 7); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		u, e1 := c.flagArg(a[0])
		m, e2 := c.intArg(a[1])
		n, e3 := c.intArg(a[2])
		lda, e4 := c.intArg(a[4])
		ldb, e5 := c.intArg(a[6])
		if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
			return nil, err
		}
		if m <= 0 || n <= 0 {
			return nil, nil
		}
		ra, err := c.region(a[3], matExt(m, n, lda))
		if err != nil {
			return nil, err
		}
		rb, err := c.region(a[5], matExt(m, n, ldb))
		if err != nil {
			return nil, err
		}
		if lda < m || ldb < m || ra.ext < matExt(m, n, lda) || rb.ext < matExt(m, n, ldb) {
			return nil, fmt.Errorf("%w: lacpy of %d x %d with lda %d, ldb %d and arrays too short", ErrUnsupported, m, n, lda, ldb)
		}
		c.written(rb)
		c.load()
		src, dst := c.view(ra), c.view(rb)
		for j := 0; j < n; j++ {
			lo, hi := 0, m
			switch byte(u) {
			case 'U', 'u':
				hi = min(m, j+1)
			case 'L', 'l':
				lo = min(m, j)
			}
			copy(dst[j*ldb+lo:j*ldb+hi], src[j*lda+lo:j*lda+hi])
		}
		c.store()
		return nil, nil
	}
	// DGETRF(M, N, A, LDA, IPIV, INFO)
	fns[expr.Symbol(p+"getrf_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("getrf", a, 6); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		m, e1 := c.intArg(a[0])
		n, e2 := c.intArg(a[1])
		lda, e3 := c.intArg(a[3])
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, err
		}
		ra, err := c.region(a[2], matExt(m, n, lda))
		if err != nil {
			return nil, err
		}
		piv, err := pivotArg(a[4], pivotExt(m, n))
		if err != nil {
			return nil, err
		}
		c.written(ra)
		c.load()
		ipiv := piv.read()
		info, err := ops.getrf(m, n, c.view(ra), lda, ipiv)
		if err != nil {
			if info, err = vm.lapackInfo(err); err != nil {
				return nil, err
			}
			return nil, vm.setInfo(a[5], info)
		}
		c.store()
		piv.write(ipiv)
		return nil, vm.setInfo(a[5], info)
	}
	// DGETRS(TRANS, N, NRHS, A, LDA, IPIV, B, LDB, INFO)
	fns[expr.Symbol(p+"getrs_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("getrs", a, 9); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		t, e1 := c.flagArg(a[0])
		n, e2 := c.intArg(a[1])
		nrhs, e3 := c.intArg(a[2])
		lda, e4 := c.intArg(a[4])
		ldb, e5 := c.intArg(a[7])
		if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
			return nil, err
		}
		ra, err := c.region(a[3], matExt(n, n, lda))
		if err != nil {
			return nil, err
		}
		piv, err := pivotArg(a[5], max(0, n))
		if err != nil {
			return nil, err
		}
		rb, err := c.region(a[6], matExt(n, nrhs, ldb))
		if err != nil {
			return nil, err
		}
		c.written(rb)
		c.load()
		info, err := vm.lapackInfo(ops.getrs(t, n, nrhs, c.view(ra), lda, piv.read(), c.view(rb), ldb))
		if err != nil {
			return nil, err
		}
		if info == 0 {
			c.store()
		}
		return nil, vm.setInfo(a[8], info)
	}
	// DPOTRF(UPLO, N, A, LDA, INFO)
	fns[expr.Symbol(p+"potrf_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("potrf", a, 5); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		uplo, e1 := c.flagArg(a[0])
		n, e2 := c.intArg(a[1])
		lda, e3 := c.intArg(a[3])
		if err := errors.Join(e1, e2, e3); err != nil {
			return nil, err
		}
		ra, err := c.region(a[2], matExt(n, n, lda))
		if err != nil {
			return nil, err
		}
		c.written(ra)
		c.load()
		info, err := ops.potrf(blas.Uplo(uplo), n, c.view(ra), lda)
		if err != nil {
			if info, err = vm.lapackInfo(err); err != nil {
				return nil, err
			}
			return nil, vm.setInfo(a[4], info)
		}
		c.store()
		return nil, vm.setInfo(a[4], info)
	}
	// DPOTRS(UPLO, N, NRHS, A, LDA, B, LDB, INFO)
	fns[expr.Symbol(p+"potrs_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("potrs", a, 8); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		uplo, e1 := c.flagArg(a[0])
		n, e2 := c.intArg(a[1])
		nrhs, e3 := c.intArg(a[2])
		lda, e4 := c.intArg(a[4])
		ldb, e5 := c.intArg(a[6])
		if err := errors.Join(e1, e2, e3, e4, e5); err != nil {
			return nil, err
		}
		ra, err := c.region(a[3], matExt(n, n, lda))
		if err != nil {
			return nil, err
		}
		rb, err := c.region(a[5], matExt(n, nrhs, ldb))
		if err != nil {
			return nil, err
		}
		c.written(rb)
		c.load()
		info, err := vm.lapackInfo(ops.potrs(blas.Uplo(uplo), n, nrhs, c.view(ra), lda, c.view(rb), ldb))
		if err != nil {
			return nil, err
		}
		if info == 0 {
			c.store()
		}
		return nil, vm.setInfo(a[7], info)
	}
}
