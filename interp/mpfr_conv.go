package interp

import (
	"fmt"
	"math"
	"math/big"
)

// Rounding to integers, the conversions between BigFloat and the C types,
// and the functions that take a number apart.

// rintExact rounds the regular x to an integer in rnd: the integer, signed,
// and the ternary value; an integer of more than limitBits bits is refused.
func (vm *VM) rintExact(name string, x *mpfrNum, rnd int) (*big.Int, int, error) {
	bl := int64(x.mant.BitLen())
	var n *big.Int
	t := 0
	if x.exp >= bl {
		if err := vm.gmpGuard(name, uint64(x.exp)); err != nil {
			return nil, 0, err
		}
		n = new(big.Int).Lsh(x.mant, uint(x.exp-bl))
	} else {
		q, up, inexact := roundShift(x.mant, uint(bl-x.exp), false, x.neg, rnd)
		n = q
		if inexact {
			t = -1
			if up {
				t = 1
			}
			if x.neg {
				t = -t
			}
		}
	}
	if x.neg {
		n.Neg(n)
	}
	return n, t, nil
}

// mpfrRint is mpfr_trunc, _ceil, _floor, _round and _roundeven: x rounded to
// an integer in rnd, then to prec bits in the same direction.
func (vm *VM) mpfrRint(name string, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
	if x.kind != kReg {
		r, t := vm.mpfrSet(x, prec, rnd)
		return r, t, nil
	}
	n, t1, err := vm.rintExact(name, x, rnd)
	if err != nil {
		return nil, 0, err
	}
	if t1 != 0 {
		vm.mpfr.flags |= flagInexact
	}
	if n.Sign() == 0 {
		return zeroNum(x.neg), t1, nil
	}
	r, t := vm.mpfrSet(numOfInt(n), prec, rnd)
	if t == 0 {
		t = t1
	}
	return r, t, nil
}

// inexPos is INEXPOS of mpfr_modf and mpfr_sin_cos: a ternary value as two
// bits.
func inexPos(t int) int32 {
	switch {
	case t == 0:
		return 0
	case t > 0:
		return 1
	}
	return 2
}

// mpfrModf is mpfr_modf: x's integer part and fractional part, rounded to the
// two precisions.
func (vm *VM) mpfrModf(x *mpfrNum, iprec, fprec int64, rnd int) (ip, fp *mpfrNum, code int32, err error) {
	var ti, tf int
	switch x.kind {
	case kNaN:
		ip, ti = vm.nanResult()
		fp, tf = ip, 0
	case kInf:
		ip, fp = x, zeroNum(x.neg)
	case kZero:
		ip, fp = x, x
	default:
		n, _, err := vm.rintExact("mpfr_modf", x, rndZ)
		if err != nil {
			return nil, nil, 0, err
		}
		ipExact := zeroNum(x.neg)
		if n.Sign() != 0 {
			ipExact = numOfInt(n)
		}
		ip, ti = vm.mpfrSet(ipExact, iprec, rnd)
		// the difference is exact at the precision of x
		exact, _ := vm.mpfrAdd(x, ipExact.negated(), int64(x.mant.BitLen()), rndZ)
		if exact.kind == kZero {
			exact = zeroNum(x.neg)
		}
		fp, tf = vm.mpfrSet(exact, fprec, rnd)
	}
	return ip, fp, inexPos(ti) + 4*inexPos(tf), nil
}

// mpfrFrexp is mpfr_frexp: x = y * 2^e with 0.5 <= |y| < 1.
func (vm *VM) mpfrFrexp(x *mpfrNum, prec int64, rnd int) (y *mpfrNum, e int64, t int) {
	// y of exponent 0: the exponent range does not apply
	if x.kind != kReg {
		y, t = vm.mpfrSet(x, prec, rnd)
		return y, 0, t
	}
	y, t = vm.mpfrParts(x.neg, x.mant, 0, false, prec, rnd)
	// rounding up to a power of two carries into the exponent
	e = x.exp + y.exp
	y.exp = 0
	return y, e, t
}

// mpfrNextAbove is mpfr_nextabove at precision prec: the next number of that
// precision above x.
func (vm *VM) mpfrNextAbove(x *mpfrNum, prec int64) *mpfrNum {
	one := new(big.Int)
	switch x.kind {
	case kNaN:
		vm.mpfr.flags |= flagNaN
		return x
	case kInf:
		if !x.neg {
			return x
		}
		return &mpfrNum{kind: kReg, neg: true, exp: vm.mpfr.emax, mant: one.Sub(one.Lsh(bigOne, uint(prec)), bigOne)}
	case kZero:
		return &mpfrNum{kind: kReg, exp: vm.mpfr.emin, mant: bigOne}
	}
	full := new(big.Int).Lsh(x.mant, uint(prec-int64(x.mant.BitLen())))
	if !x.neg {
		// one place away from zero
		full.Add(full, bigOne)
		ex := x.exp
		if int64(full.BitLen()) > prec {
			full.Rsh(full, 1)
			ex++
		}
		if ex > vm.mpfr.emax {
			return infNum(false)
		}
		return &mpfrNum{kind: kReg, exp: ex, mant: full}
	}
	// one place toward zero
	ex := x.exp
	full.Sub(full, bigOne)
	if int64(full.BitLen()) < prec {
		full.Lsh(full, 1).Or(full, bigOne)
		ex--
	}
	if ex < vm.mpfr.emin {
		return zeroNum(true)
	}
	return &mpfrNum{kind: kReg, neg: true, exp: ex, mant: full}
}

// mpfrFmod is mpfr_fmod (rem false) and mpfr_remainder (rem true): x - n*y
// with n the quotient truncated, or rounded to nearest with ties to even.
func (vm *VM) mpfrFmod(rem bool, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
	switch {
	case x.kind == kNaN || y.kind == kNaN || x.kind == kInf || y.kind == kZero:
		r, t := vm.nanResult()
		return r, t, nil
	case x.kind == kZero:
		return x, 0, nil
	case y.kind == kInf:
		r, t := vm.mpfrSet(x, prec, rnd)
		return r, t, nil
	}
	lo := min(x.lsb(), y.lsb())
	// the shifts are the operands' exponent difference: refused when huge
	if d := max(x.lsb(), y.lsb()) - lo; uint64(d) > vm.limitBits() {
		return nil, 0, fmt.Errorf("%w: %s of operands whose exponents differ by %d bits (VM.MaxMemoryElems is %d limbs)", ErrMemoryLimit, "mpfr_fmod", uint64(d), vm.MaxMemoryElems)
	}
	a := new(big.Int).Lsh(x.mant, uint(x.lsb()-lo))
	b := new(big.Int).Lsh(y.mant, uint(y.lsb()-lo))
	q, r := new(big.Int).QuoRem(a, b, new(big.Int))
	if rem {
		twice := new(big.Int).Lsh(r, 1)
		if c := twice.Cmp(b); c > 0 || c == 0 && q.Bit(0) == 1 {
			r.Sub(r, b)
		}
	}
	if r.Sign() == 0 {
		return zeroNum(x.neg), 0, nil
	}
	neg := x.neg != (r.Sign() < 0)
	r.Abs(r)
	res, t := vm.mpfrParts(neg, r, satAddI(lo, int64(r.BitLen())), false, prec, rnd)
	return res, t, nil
}

// mpfrGetInt is mpfr_get_sj and mpfr_get_uj: x rounded to an integer, which
// must lie in [lo, hi]; otherwise the nearest bound, and the range flag.
func (vm *VM) mpfrGetInt(name string, x *mpfrNum, rnd int, lo, hi *big.Int) (*big.Int, error) {
	e := &vm.mpfr
	n := new(big.Int)
	switch {
	case x.kind == kNaN:
		e.flags |= flagErange
		return n, nil
	case x.kind == kInf, x.kind == kReg && x.exp > 130:
		// beyond any 64-bit integer
		e.flags |= flagErange
		if x.neg {
			return lo, nil
		}
		return hi, nil
	case x.kind == kReg:
		var t int
		var err error
		if n, t, err = vm.rintExact(name, x, rnd); err != nil {
			return nil, err
		}
		// the inexact flag is for a value that fits
		switch {
		case n.Cmp(lo) < 0:
			e.flags |= flagErange
			return lo, nil
		case n.Cmp(hi) > 0:
			e.flags |= flagErange
			return hi, nil
		case t != 0:
			e.flags |= flagInexact
		}
	}
	return n, nil
}

var (
	int64Min  = big.NewInt(math.MinInt64)
	int64Max  = big.NewInt(math.MaxInt64)
	uint64Max = new(big.Int).SetUint64(math.MaxUint64)
)

// mpfrGetZ is mpfr_get_z: x rounded to an integer.
func (vm *VM) mpfrGetZ(x *mpfrNum, rnd int) (*big.Int, int, error) {
	if x.kind == kNaN || x.kind == kInf {
		vm.mpfr.flags |= flagErange
		return new(big.Int), 0, nil
	}
	if x.kind == kZero {
		return new(big.Int), 0, nil
	}
	n, t, err := vm.rintExact("mpfr_get_z", x, rnd)
	if t != 0 {
		vm.mpfr.flags |= flagInexact
	}
	return n, t, err
}

// mpfrCmp is mpfr_cmp and its relatives: the sign of x-y, 0 and the range
// flag for a NaN.
func (vm *VM) mpfrCmp(x, y *mpfrNum) int32 {
	if x.kind == kNaN || y.kind == kNaN {
		vm.mpfr.flags |= flagErange
		return 0
	}
	return int32(cmpNum(x, y))
}

// mpfrPred is mpfr_less_p and its relatives: false for a NaN, with no flag.
func mpfrPred(x, y *mpfrNum, ok func(c int) bool) int32 {
	if x.kind == kNaN || y.kind == kNaN {
		return 0
	}
	return b2i(ok(cmpNum(x, y)))
}

// isInteger is mpfr_integer_p.
func isInteger(x *mpfrNum) bool {
	switch x.kind {
	case kZero:
		return true
	case kReg:
		return x.exp >= int64(x.mant.BitLen())
	}
	return false
}
