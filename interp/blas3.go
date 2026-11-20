package interp

import (
	"errors"

	"github.com/go-blas/blas"
	"github.com/go-embedded-julia/julia/expr"
)

// The level 3 routines libblastrampoline forwards to go-blas/blas', for s and
// d: trsm, trmm (which write B), syrk and symm (which write C). They follow the
// shape of gemm (blas.go): the arguments by reference, the written array
// recorded for the undo log by load, a refused argument printed as xerbla does.

// leftSide is whether a side flag names the left side, as the routines read it
// ('L' or 'l'); any other flag is the routine's to refuse.
func leftSide(s byte) bool { return s == 'L' || s == 'l' }

// notTrans is whether a transpose flag is 'N' or 'n'.
func notTrans(t blas.Transpose) bool { return t == 'N' || t == 'n' }

// registerBlas3 registers the level 3 shims of one precision: p is 's' or 'd'.
func registerBlas3[T blasFloat](fns map[expr.Symbol]shim, p string, ops *blasOps[T]) {
	// DTRSM and DTRMM(SIDE, UPLO, TRANSA, DIAG, M, N, ALPHA, A, LDA, B, LDB)
	triangular := func(name string, f func(blas.Side, blas.Uplo, blas.Transpose, blas.Diag, int, int, T, []T, int, []T, int) error) shim {
		return func(vm *VM, a []any) (any, error) {
			if err := blasArity(name, a, 11); err != nil {
				return nil, err
			}
			c := newBlasCall(vm, ops)
			side, e1 := c.flagArg(a[0])
			uplo, e2 := c.flagArg(a[1])
			tA, e3 := c.flagArg(a[2])
			diag, e4 := c.flagArg(a[3])
			m, e5 := c.intArg(a[4])
			n, e6 := c.intArg(a[5])
			alpha, e7 := c.floatArg(a[6])
			lda, e8 := c.intArg(a[8])
			ldb, e9 := c.intArg(a[10])
			if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8, e9); err != nil {
				return nil, err
			}
			na := n // A is square, of the order of the side it multiplies from
			if leftSide(byte(side)) {
				na = m
			}
			ra, err := c.region(a[7], matExt(na, na, lda))
			if err != nil {
				return nil, err
			}
			rb, err := c.region(a[9], matExt(m, n, ldb))
			if err != nil {
				return nil, err
			}
			c.written(rb)
			c.load()
			if err := f(blas.Side(side), blas.Uplo(uplo), tA, blas.Diag(diag), m, n, alpha, c.view(ra), lda, c.view(rb), ldb); err != nil {
				return nil, vm.xerbla(err)
			}
			c.store()
			return nil, nil
		}
	}
	fns[expr.Symbol(p+"trsm_64_")] = triangular("trsm", ops.trsm)
	fns[expr.Symbol(p+"trmm_64_")] = triangular("trmm", ops.trmm)
	// DSYRK(UPLO, TRANS, N, K, ALPHA, A, LDA, BETA, C, LDC)
	fns[expr.Symbol(p+"syrk_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("syrk", a, 10); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		uplo, e1 := c.flagArg(a[0])
		t, e2 := c.flagArg(a[1])
		n, e3 := c.intArg(a[2])
		k, e4 := c.intArg(a[3])
		alpha, e5 := c.floatArg(a[4])
		lda, e6 := c.intArg(a[6])
		beta, e7 := c.floatArg(a[7])
		ldc, e8 := c.intArg(a[9])
		if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8); err != nil {
			return nil, err
		}
		ar, ac := n, k
		if !notTrans(t) {
			ar, ac = k, n
		}
		ra, err := c.region(a[5], matExt(ar, ac, lda))
		if err != nil {
			return nil, err
		}
		rc, err := c.region(a[8], matExt(n, n, ldc))
		if err != nil {
			return nil, err
		}
		c.written(rc)
		c.load()
		if err := ops.syrk(blas.Uplo(uplo), t, n, k, alpha, c.view(ra), lda, beta, c.view(rc), ldc); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
	// DSYMM(SIDE, UPLO, M, N, ALPHA, A, LDA, B, LDB, BETA, C, LDC)
	fns[expr.Symbol(p+"symm_64_")] = func(vm *VM, a []any) (any, error) {
		if err := blasArity("symm", a, 12); err != nil {
			return nil, err
		}
		c := newBlasCall(vm, ops)
		side, e1 := c.flagArg(a[0])
		uplo, e2 := c.flagArg(a[1])
		m, e3 := c.intArg(a[2])
		n, e4 := c.intArg(a[3])
		alpha, e5 := c.floatArg(a[4])
		lda, e6 := c.intArg(a[6])
		ldb, e7 := c.intArg(a[8])
		beta, e8 := c.floatArg(a[9])
		ldc, e9 := c.intArg(a[11])
		if err := errors.Join(e1, e2, e3, e4, e5, e6, e7, e8, e9); err != nil {
			return nil, err
		}
		na := n
		if leftSide(byte(side)) {
			na = m
		}
		ra, err := c.region(a[5], matExt(na, na, lda))
		if err != nil {
			return nil, err
		}
		rb, err := c.region(a[7], matExt(m, n, ldb))
		if err != nil {
			return nil, err
		}
		rc, err := c.region(a[10], matExt(m, n, ldc))
		if err != nil {
			return nil, err
		}
		c.written(rc)
		c.load()
		if err := ops.symm(blas.Side(side), blas.Uplo(uplo), m, n, alpha, c.view(ra), lda, c.view(rb), ldb, beta, c.view(rc), ldc); err != nil {
			return nil, vm.xerbla(err)
		}
		c.store()
		return nil, nil
	}
}
