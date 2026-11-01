package interp

import (
	"math/bits"
	"strconv"
)

// u128 is the bit pattern of an integer of up to 128 bits, the one form the
// integer intrinsics compute on: each operation works on the low w bits, as
// julia's runtime intrinsics work on a value's C type of w bits
// (runtime_intrinsics.c), and its result is cut back to w bits.
type u128 struct{ hi, lo uint64 }

// Int128 and UInt128 are the values of Julia's 128-bit integer types: their
// two's-complement bit pattern, high half first.
type (
	Int128  struct{ Hi, Lo uint64 }
	UInt128 struct{ Hi, Lo uint64 }
)

func from64(x uint64) u128 { return u128{lo: x} }

func (a u128) add(b u128) u128 {
	lo, c := bits.Add64(a.lo, b.lo, 0)
	hi, _ := bits.Add64(a.hi, b.hi, c)
	return u128{hi, lo}
}

func (a u128) sub(b u128) u128 {
	lo, br := bits.Sub64(a.lo, b.lo, 0)
	hi, _ := bits.Sub64(a.hi, b.hi, br)
	return u128{hi, lo}
}

// mulFull is the 256-bit product of a and b, as (high 128, low 128).
func (a u128) mulFull(b u128) (u128, u128) {
	// schoolbook over 64-bit limbs
	h00, l00 := bits.Mul64(a.lo, b.lo)
	h01, l01 := bits.Mul64(a.lo, b.hi)
	h10, l10 := bits.Mul64(a.hi, b.lo)
	h11, l11 := bits.Mul64(a.hi, b.hi)
	r1, c1 := bits.Add64(h00, l01, 0)
	r1, c2 := bits.Add64(r1, l10, 0)
	r2, c3 := bits.Add64(h01, h10, c1)
	r2, c4 := bits.Add64(r2, l11, c2)
	r3 := h11 + c3 + c4
	return u128{r3, r2}, u128{r1, l00}
}

func (a u128) mul(b u128) u128 {
	_, lo := a.mulFull(b)
	return lo
}

func (a u128) and(b u128) u128 { return u128{a.hi & b.hi, a.lo & b.lo} }
func (a u128) or(b u128) u128  { return u128{a.hi | b.hi, a.lo | b.lo} }
func (a u128) xor(b u128) u128 { return u128{a.hi ^ b.hi, a.lo ^ b.lo} }
func (a u128) not() u128       { return u128{^a.hi, ^a.lo} }
func (a u128) neg() u128       { return u128{}.sub(a) }
func (a u128) isZero() bool    { return a.hi == 0 && a.lo == 0 }

func (a u128) shl(n uint) u128 {
	switch {
	case n >= 128:
		return u128{}
	case n >= 64:
		return u128{a.lo << (n - 64), 0}
	case n == 0:
		return a
	}
	return u128{a.hi<<n | a.lo>>(64-n), a.lo << n}
}

func (a u128) lshr(n uint) u128 {
	switch {
	case n >= 128:
		return u128{}
	case n >= 64:
		return u128{0, a.hi >> (n - 64)}
	case n == 0:
		return a
	}
	return u128{a.hi >> n, a.lo>>n | a.hi<<(64-n)}
}

// ashr is the arithmetic shift of a 128-bit pattern.
func (a u128) ashr(n uint) u128 {
	if n >= 128 {
		n = 127
	}
	s := a.lshr(n)
	if a.hi>>63 == 1 && n > 0 {
		s = s.or(u128{^uint64(0), ^uint64(0)}.shl(128 - n))
	}
	return s
}

// mask keeps the low w bits.
func (a u128) mask(w uint) u128 {
	if w >= 128 {
		return a
	}
	return a.and(u128{^uint64(0), ^uint64(0)}.lshr(128 - w))
}

// sext is the w-bit pattern a sign-extended to 128 bits.
func (a u128) sext(w uint) u128 { return a.shl(128 - w).ashr(128 - w) }

func (a u128) neg128() bool { return a.hi>>63 == 1 }

func (a u128) ult(b u128) bool { return a.hi < b.hi || a.hi == b.hi && a.lo < b.lo }

// slt compares the w-bit patterns as signed integers.
func (a u128) slt(b u128, w uint) bool {
	x, y := a.sext(w), b.sext(w)
	if x.neg128() != y.neg128() {
		return x.neg128()
	}
	return x.ult(y)
}

// udivmod is the unsigned quotient and remainder; b is not zero.
func (a u128) udivmod(b u128) (q, r u128) {
	if a.hi == 0 && b.hi == 0 {
		return from64(a.lo / b.lo), from64(a.lo % b.lo)
	}
	for i := 127; i >= 0; i-- {
		r = r.shl(1)
		r.lo |= a.lshr(uint(i)).lo & 1
		if !r.ult(b) {
			r = r.sub(b)
			q = q.or(from64(1).shl(uint(i)))
		}
	}
	return q, r
}

// sdivmod is C's signed / and % of w-bit patterns: the quotient truncated
// toward zero, the remainder of the dividend's sign; b is not zero and the
// quotient is representable.
func (a u128) sdivmod(b u128, w uint) (q, r u128) {
	x, y := a.sext(w), b.sext(w)
	ax, ay := x, y
	if x.neg128() {
		ax = x.neg()
	}
	if y.neg128() {
		ay = y.neg()
	}
	q, r = ax.udivmod(ay)
	if x.neg128() != y.neg128() {
		q = q.neg()
	}
	if x.neg128() {
		r = r.neg()
	}
	return q.mask(w), r.mask(w)
}

// decimal is the w-bit pattern a in base 10, signed or not.
func (a u128) decimal(w uint, signed bool) string {
	neg := false
	if signed {
		a = a.sext(w)
		if a.neg128() {
			neg, a = true, a.neg()
		}
	}
	if a.hi == 0 {
		s := strconv.FormatUint(a.lo, 10)
		if neg {
			return "-" + s
		}
		return s
	}
	var digits []byte
	ten := from64(10)
	for !a.isZero() {
		var r u128
		a, r = a.udivmod(ten)
		digits = append(digits, byte('0'+r.lo))
	}
	if neg {
		digits = append(digits, '-')
	}
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
