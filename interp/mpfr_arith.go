package interp

import (
	"math/big"
)

// MPFR's arithmetic (https://www.mpfr.org/mpfr-4.2.2/mpfr.html#Basic-Arithmetic-Functions):
// each result is the exact one rounded in the mode asked, so it is computed
// on integers: the operation's exact value, or enough bits of it and a flag
// that the rest is not zero, then rounded once (mpfrParts).

// mpfrAdd is a+b rounded to prec bits.
func (vm *VM) mpfrAdd(a, b *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	switch {
	case a.kind == kNaN || b.kind == kNaN:
		return vm.nanResult()
	case a.kind == kInf && b.kind == kInf:
		if a.neg != b.neg {
			return vm.nanResult()
		}
		return a, 0
	case a.kind == kInf:
		return a, 0
	case b.kind == kInf:
		return b, 0
	case a.kind == kZero && b.kind == kZero:
		// (+0)+(-0) is +0, but -0 rounding down
		return zeroNum(a.neg && b.neg || a.neg != b.neg && rnd == rndD), 0
	case a.kind == kZero:
		return vm.mpfrSet(b, prec, rnd)
	case b.kind == kZero:
		return vm.mpfrSet(a, prec, rnd)
	}
	if a.exp < b.exp {
		a, b = b, a
	}
	la := int64(a.mant.BitLen())
	if uint64(a.exp)-uint64(b.exp) >= uint64(la)+2 {
		// b lies two places below a's last bit: it only makes the sum a
		// little more or less than a, and a's mantissa two bits longer
		m := new(big.Int).Lsh(a.mant, 2)
		if a.neg != b.neg {
			m.Sub(m, bigOne)
		}
		ex := satAddI(a.exp, int64(m.BitLen())-la-2)
		return vm.mpfrParts(a.neg, m, ex, true, prec, rnd)
	}
	lo := min(a.lsb(), b.lsb())
	sa := new(big.Int).Lsh(a.mant, uint(a.lsb()-lo))
	sb := new(big.Int).Lsh(b.mant, uint(b.lsb()-lo))
	if a.neg {
		sa.Neg(sa)
	}
	if b.neg {
		sb.Neg(sb)
	}
	s := sa.Add(sa, sb)
	if s.Sign() == 0 {
		return zeroNum(rnd == rndD), 0
	}
	neg := s.Sign() < 0
	s.Abs(s)
	return vm.mpfrParts(neg, s, satAddI(lo, int64(s.BitLen())), false, prec, rnd)
}

// mulNum is a*b, exact: not rounded, a regular result of any precision.
func (vm *VM) mulNum(a, b *mpfrNum) *mpfrNum {
	neg := a.neg != b.neg
	switch {
	case a.kind == kNaN || b.kind == kNaN:
		x, _ := vm.nanResult()
		return x
	case a.kind == kInf || b.kind == kInf:
		if a.kind == kZero || b.kind == kZero {
			x, _ := vm.nanResult()
			return x
		}
		return infNum(neg)
	case a.kind == kZero || b.kind == kZero:
		return zeroNum(neg)
	}
	m := new(big.Int).Mul(a.mant, b.mant)
	return &mpfrNum{kind: kReg, neg: neg, exp: satAddI(satAddI(a.lsb(), b.lsb()), int64(m.BitLen())), mant: m}
}

// mpfrMul is a*b rounded to prec bits.
func (vm *VM) mpfrMul(a, b *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	return vm.mpfrSet(vm.mulNum(a, b), prec, rnd)
}

// mpfrFMA is a*b+c, rounded once.
func (vm *VM) mpfrFMA(a, b, c *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	return vm.mpfrAdd(vm.mulNum(a, b), c, prec, rnd)
}

// mpfrDiv is a/b rounded to prec bits.
func (vm *VM) mpfrDiv(a, b *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	neg := a.neg != b.neg
	switch {
	case a.kind == kNaN || b.kind == kNaN, a.kind == kInf && b.kind == kInf, a.kind == kZero && b.kind == kZero:
		return vm.nanResult()
	case a.kind == kInf:
		return infNum(neg), 0
	case b.kind == kInf:
		return zeroNum(neg), 0
	case b.kind == kZero:
		vm.mpfr.flags |= flagDivBy0
		return infNum(neg), 0
	case a.kind == kZero:
		return zeroNum(neg), 0
	}
	la, lb := int64(a.mant.BitLen()), int64(b.mant.BitLen())
	// a quotient of at least prec+2 bits
	s := max(0, prec+3+lb-la)
	q, r := new(big.Int).QuoRem(new(big.Int).Lsh(a.mant, uint(s)), b.mant, new(big.Int))
	l := satAddI(satAddI(a.lsb(), -b.lsb()), -s)
	return vm.mpfrParts(neg, q, satAddI(l, int64(q.BitLen())), r.Sign() != 0, prec, rnd)
}

// mpfrSqrt is the square root of x rounded to prec bits.
func (vm *VM) mpfrSqrt(x *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	switch {
	case x.kind == kNaN, x.kind == kInf && x.neg:
		return vm.nanResult()
	case x.kind != kReg:
		return x, 0
	case x.neg:
		return vm.nanResult()
	}
	m, l := new(big.Int).Set(x.mant), x.lsb()
	if l&1 != 0 {
		m.Lsh(m, 1)
		l--
	}
	// a root of at least prec+2 bits
	if need := 2*(prec+2) - int64(m.BitLen()); need > 0 {
		k := (need + 1) / 2
		m.Lsh(m, uint(2*k))
		l -= 2 * k
	}
	r := new(big.Int).Sqrt(m)
	sticky := new(big.Int).Mul(r, r).Cmp(m) != 0
	return vm.mpfrParts(false, r, satAddI(l/2, int64(r.BitLen())), sticky, prec, rnd)
}

// mpfrMul2 is x*2^n rounded to prec bits.
func (vm *VM) mpfrMul2(x *mpfrNum, n int64, prec int64, rnd int) (*mpfrNum, int) {
	if x.kind == kReg {
		return vm.mpfrParts(x.neg, x.mant, satAddI(x.exp, n), false, prec, rnd)
	}
	return vm.mpfrSet(x, prec, rnd)
}

// numSign is the sign of x: 0 for a zero.
func numSign(x *mpfrNum) int {
	if x.kind == kZero {
		return 0
	}
	return signInt(x.neg)
}

// cmpAbs compares |a| and |b|, neither zero nor NaN.
func cmpAbs(a, b *mpfrNum) int {
	switch {
	case a.kind == kInf || b.kind == kInf:
		return int(b2i(a.kind == kInf)) - int(b2i(b.kind == kInf))
	case a.exp != b.exp:
		if a.exp < b.exp {
			return -1
		}
		return 1
	}
	la, lb := a.mant.BitLen(), b.mant.BitLen()
	x, y := a.mant, b.mant
	if la < lb {
		x = new(big.Int).Lsh(x, uint(lb-la))
	} else {
		y = new(big.Int).Lsh(y, uint(la-lb))
	}
	return x.Cmp(y)
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// cmpNum compares two numbers neither of which is NaN: -1, 0 or 1.
func cmpNum(a, b *mpfrNum) int {
	sa, sb := numSign(a), numSign(b)
	switch {
	case sa != sb:
		if sa < sb {
			return -1
		}
		return 1
	case sa == 0:
		return 0
	case sa < 0:
		return -cmpAbs(a, b)
	}
	return cmpAbs(a, b)
}
