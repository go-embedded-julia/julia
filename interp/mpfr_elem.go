package interp

import (
	"fmt"
	"math"
	"math/big"

	"github.com/go-bignum/bigfloat"
)

// MPFR's elementary functions (https://www.mpfr.org/mpfr-4.2.2/mpfr.html#Special-Functions),
// computed by github.com/go-bignum/bigfloat, which returns the exact result
// rounded in the receiver's precision and mode, with the side of the exact
// value (Acc) that is MPFR's ternary value. What math/big cannot hold is
// settled here: NaN, and exponents beyond math/big's int32, which refuse.

// goRounding is the math/big mode of one of MPFR's.
func goRounding(rnd int) big.RoundingMode {
	return [...]big.RoundingMode{rndN: big.ToNearestEven, rndZ: big.ToZero, rndU: big.ToPositiveInf, rndD: big.ToNegativeInf, rndA: big.AwayFromZero}[rnd]
}

// accTernary is the ternary value of a math/big result.
func accTernary(a big.Accuracy) int { return int(a) }

// bfEval runs f at prec bits in rnd on the operands as big.Floats. Where the
// operands are special (NaN excepted, which the callers settle) an infinite or
// zero result is exact; for regular operands it is an overflow or underflow of
// math/big's exponent range, which MPFR's is larger than, and refused.
func (vm *VM) bfEval(name string, prec int64, rnd int, args []*mpfrNum, f func(z *big.Float, x []*big.Float)) (*mpfrNum, int, error) {
	special := false
	xs := make([]*big.Float, len(args))
	for i, a := range args {
		special = special || a.kind != kReg
		var ok bool
		if xs[i], ok = bigFloatOf(a); !ok {
			return nil, 0, fmt.Errorf("%w: %s of an argument with exponent %d, beyond math/big's", ErrUnsupported, name, a.exp)
		}
	}
	z := new(big.Float).SetPrec(uint(prec)).SetMode(goRounding(rnd))
	var failure any
	func() {
		defer func() { failure = recover() }()
		f(z, xs)
	}()
	switch r := failure.(type) {
	case nil:
	case bigfloat.ErrNaN, big.ErrNaN:
		x, t := vm.nanResult()
		return x, t, nil
	default:
		return nil, 0, fmt.Errorf("%w: %s refused by bigfloat: %v", ErrUnsupported, name, r)
	}
	switch {
	case z.IsInf() && special:
		return infNum(z.Signbit()), 0, nil
	case z.Sign() == 0 && (special || z.Acc() == big.Exact):
		return zeroNum(z.Signbit()), 0, nil
	case z.IsInf() || z.Sign() == 0:
		return nil, 0, fmt.Errorf("%w: %s of a result beyond math/big's exponent range (MPFR's is larger)", ErrUnsupported, name)
	}
	mant := new(big.Float)
	e := z.MantExp(mant)
	mant.SetMantExp(mant, int(z.Prec()))
	m, _ := mant.Int(nil)
	neg := z.Signbit()
	x, t := vm.mpfrFinish(neg, m.Abs(m), int64(e), accTernary(z.Acc()), prec, rnd)
	return x, t, nil
}

// bfUnary is an elementary function of one argument: bigfloat's f, with pre
// settling the arguments MPFR gives a pole or a NaN (handled reports it).
type bfUnary struct {
	f   func(z, x *big.Float) *big.Float
	pre func(vm *VM, x *mpfrNum) (r *mpfrNum, t int, handled bool)
}

// pole is x's result of a function that is infinite at a point: ±Inf with
// the divide-by-zero flag.
func (vm *VM) pole(neg bool) (*mpfrNum, int, bool) {
	vm.mpfr.flags |= flagDivBy0
	return infNum(neg), 0, true
}

func (vm *VM) nanHandled() (*mpfrNum, int, bool) {
	x, t := vm.nanResult()
	return x, t, true
}

// logDomain is log's argument rule: a zero is a pole at -Inf, a negative
// number has no real logarithm.
func logDomain(vm *VM, x *mpfrNum) (*mpfrNum, int, bool) {
	switch {
	case x.kind == kZero:
		return vm.pole(true)
	case x.neg && x.kind != kNaN:
		return vm.nanHandled()
	}
	return nil, 0, false
}

var bfUnaries = map[string]bfUnary{
	"exp":   {f: bigfloat.Exp},
	"expm1": {f: bigfloat.Expm1},
	"exp2": {f: func(z, x *big.Float) *big.Float {
		return bigfloat.Pow(z, new(big.Float).SetInt64(2), x)
	}},
	"exp10": {f: func(z, x *big.Float) *big.Float {
		return bigfloat.Pow(z, new(big.Float).SetInt64(10), x)
	}},
	"cosh":  {f: bigfloat.Cosh},
	"sinh":  {f: bigfloat.Sinh},
	"tanh":  {f: bigfloat.Tanh},
	"cbrt":  {f: bigfloat.Cbrt},
	"log":   {f: bigfloat.Log, pre: logDomain},
	"log2":  {f: bigfloat.Log2, pre: logDomain},
	"log10": {f: bigfloat.Log10, pre: logDomain},
	"log1p": {f: bigfloat.Log1p, pre: func(vm *VM, x *mpfrNum) (*mpfrNum, int, bool) {
		switch cmpNum(x, numOfInt64(-1)) {
		case 0:
			return vm.pole(true)
		case -1:
			return vm.nanHandled()
		}
		return nil, 0, false
	}},
	"sin":   {f: bigfloat.Sin},
	"cos":   {f: bigfloat.Cos},
	"tan":   {f: bigfloat.Tan},
	"acos":  {f: bigfloat.Acos},
	"asin":  {f: bigfloat.Asin},
	"atan":  {f: bigfloat.Atan},
	"acosh": {f: bigfloat.Acosh},
	"asinh": {f: bigfloat.Asinh},
	"atanh": {f: bigfloat.Atanh, pre: func(vm *VM, x *mpfrNum) (*mpfrNum, int, bool) {
		switch cmpAbs1(x) {
		case 0:
			return vm.pole(x.neg)
		case 1:
			return vm.nanHandled()
		}
		return nil, 0, false
	}},
}

// cmpAbs1 compares |x| with 1 (x not NaN or zero): -1, 0 or 1.
func cmpAbs1(x *mpfrNum) int {
	if x.kind == kZero {
		return -1
	}
	return cmpAbs(x, numOfInt64(1))
}

// elemValue is an elementary function's result at prec bits.
func (vm *VM) elemValue(name string, u bfUnary, prec int64, x *mpfrNum, rnd int) (*mpfrNum, int, error) {
	if x.kind == kNaN {
		r, t := vm.nanResult()
		return r, t, nil
	}
	if u.pre != nil {
		if r, t, ok := u.pre(vm, x); ok {
			return r, t, nil
		}
	}
	r, t, err := vm.bfEval(name, prec, rnd, []*mpfrNum{x}, func(res *big.Float, a []*big.Float) { u.f(res, a[0]) })
	if err == nil && name == "mpfr_cbrt" && x.kind == kReg && x.neg && r.kind == kInf && rnd == rndD {
		// MPFR 4.2.2's mpfr_cbrt of a negative number rounded down that
		// overflows gives the largest negative number, not -Inf (julia
		// 1.12.6: cbrt(-0.5) at precision 2, emax 0): its overflow is the
		// one of the positive result, rounded down
		m := new(big.Int).Lsh(bigOne, uint(prec))
		return &mpfrNum{kind: kReg, neg: true, exp: vm.mpfr.emax, mant: m.Sub(m, bigOne)}, 1, nil
	}
	return r, t, err
}

// mpfrPow is x^y.
func (vm *VM) mpfrPow(name string, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
	one := numOfInt64(1)
	switch {
	case y.kind == kZero, x.kind == kReg && !x.neg && cmpNum(x, one) == 0:
		// x^±0 and 1^y are 1, whatever the other is
		r, t := vm.mpfrSet(one, prec, rnd)
		return r, t, nil
	case x.kind == kNaN || y.kind == kNaN:
		r, t := vm.nanResult()
		return r, t, nil
	}
	if x.kind == kZero && y.neg && y.kind != kInf {
		vm.mpfr.flags |= flagDivBy0
	}
	return vm.bfEval(name, prec, rnd, []*mpfrNum{x, y}, func(z *big.Float, a []*big.Float) { bigfloat.Pow(z, a[0], a[1]) })
}

// mpfrHypot is sqrt(x^2+y^2): infinite when either is, even beside a NaN.
func (vm *VM) mpfrHypot(x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
	switch {
	case x.kind == kInf || y.kind == kInf:
		return infNum(false), 0, nil
	case x.kind == kNaN || y.kind == kNaN:
		r, t := vm.nanResult()
		return r, t, nil
	case x.kind == kZero && y.kind == kZero:
		return zeroNum(false), 0, nil
	}
	return vm.bfEval("mpfr_hypot", prec, rnd, []*mpfrNum{x, y}, func(z *big.Float, a []*big.Float) { bigfloat.Hypot(z, a[0], a[1]) })
}

// mpfrAtan2 is the angle of the point (x, y).
func (vm *VM) mpfrAtan2(y, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
	if x.kind == kNaN || y.kind == kNaN {
		r, t := vm.nanResult()
		return r, t, nil
	}
	return vm.bfEval("mpfr_atan2", prec, rnd, []*mpfrNum{y, x}, func(z *big.Float, a []*big.Float) { bigfloat.Atan2(z, a[0], a[1]) })
}

// mpfrConst is a constant bigfloat computes at any precision: log(2), pi,
// Euler's gamma, Catalan's constant.
func (vm *VM) mpfrConst(name string, f func(*big.Float) *big.Float, prec int64, rnd int) (*mpfrNum, int, error) {
	return vm.bfEval(name, prec, rnd, nil, func(z *big.Float, _ []*big.Float) { f(z) })
}

// facBits bounds the bits of n!, as the float the gamma function gives, for
// the guard before the product is made.
func facBits(n uint64) uint64 { return ceilBits(log2Fact(n)) }

// mpfrFac is n! rounded to prec bits.
func (vm *VM) mpfrFac(n uint64, prec int64, rnd int) (*mpfrNum, int, error) {
	if err := vm.gmpGuard("mpfr_fac_ui", facBits(n)); err != nil {
		return nil, 0, err
	}
	f := new(big.Int)
	// an empty range, n = 0, is the product 1
	f.MulRange(1, int64(min(n, math.MaxInt64)))
	r, t := vm.mpfrSet(numOfInt(f), prec, rnd)
	return r, t, nil
}
