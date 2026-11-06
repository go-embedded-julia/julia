package interp

import (
	"fmt"
	"math"
	"math/big"
	"math/bits"

	"github.com/go-embedded-julia/julia/expr"
)

// libmpfr: the MPFR functions Base's MPFR module calls (mpfr.jl), for the
// MPFR julia 1.12.6 ships, 4.2.2 (https://www.mpfr.org/mpfr-4.2.2/mpfr.html).
// MPFR's results are fully determined by its documentation (the correctly
// rounded value in the rounding mode asked, its ternary value, the flags it
// sets), so this file computes them exactly: the arithmetic on big integers,
// the elementary functions with go-bignum/bigfloat, which rounds correctly at
// any precision in all modes.
//
// A BigFloat is `struct BigFloat; d::Memory{Limb}`, the memory holding
// mpfr_t's four fields and then the limbs (mpfr.jl:120-136, 143-171):
//
//	word 0   prec  (Clong)
//	word 1   sign  (Cint, low half): +1 or -1
//	word 2   exp   (Clong): x = 0.1xxx... * 2^exp, or one of the three special
//	               exponents (zero, NaN, infinity)
//	word 3   d     (the pointer to word 4, which Base maintains)
//	word 4.. the limbs, least significant first, of ceil(prec/64) words, the
//	               top bit of the top limb set, the unused low bits zero
//
// which Base reads directly (x.prec, x.sign, x.exp, x.d[i]). After every shim
// the fields are what MPFR would leave; a NaN result leaves the sign alone,
// as MPFR does.

const (
	mpfrLib      = "@rpath/libmpfr.6.dylib"
	mpfrLibShort = "libmpfr" // ccall((:mpfr_sinu, :libmpfr), …) names it so
	mpfrVersion  = "4.2.2"
)

const (
	// the special exponents of mpfr-impl.h (MPFR_EXP_ZERO, _NAN, _INF)
	mpfrExpZero = math.MinInt64 + 1
	mpfrExpNaN  = math.MinInt64 + 2
	mpfrExpInf  = math.MinInt64 + 3
	// the exponent range MPFR allows, and its default
	mpfrExpInvalid  = int64(1) << 62
	mpfrEmaxMax     = mpfrExpInvalid - 1
	mpfrEminMin     = 1 - mpfrExpInvalid
	mpfrEmaxDefault = int64(1)<<30 - 1
	mpfrEminDefault = -(int64(1)<<30 - 1)
	// the offset of the limbs in a BigFloat's memory, in words
	mpfrLimbWord = 4
)

// MPFR's rounding modes, mpfr_rnd_t. rndNA, to nearest with ties away from
// zero, is MPFR's internal MPFR_RNDNA (mpfr_round).
const (
	rndN = iota
	rndZ
	rndU
	rndD
	rndA
	rndF
	rndNA = -1
)

// the flags, mpfr_flags_t
const (
	flagUnderflow = 1 << iota
	flagOverflow
	flagNaN
	flagInexact
	flagErange
	flagDivBy0
)

// mpfrEnv is libmpfr's global state.
type mpfrEnv struct {
	emin, emax int64
	flags      uint8
}

// the kinds of an mpfrNum
const (
	kNaN = iota
	kInf
	kZero
	kReg
)

// mpfrNum is an MPFR number of no particular precision: a regular value is
// ±mant * 2^(exp - bitlen(mant)), mant > 0 with no trailing zero bit.
type mpfrNum struct {
	kind int
	neg  bool
	exp  int64
	mant *big.Int
}

func nanNum() *mpfrNum             { return &mpfrNum{kind: kNaN} }
func infNum(neg bool) *mpfrNum     { return &mpfrNum{kind: kInf, neg: neg} }
func zeroNum(neg bool) *mpfrNum    { return &mpfrNum{kind: kZero, neg: neg} }
func (x *mpfrNum) isSpecial() bool { return x.kind != kReg }

// lsb is the exponent of x's least significant bit.
func (x *mpfrNum) lsb() int64 { return satAddI(x.exp, -int64(x.mant.BitLen())) }

// negated is x with the opposite sign (a NaN's is left alone).
func (x *mpfrNum) negated() *mpfrNum {
	y := *x
	if x.kind != kNaN {
		y.neg = !x.neg
	}
	return &y
}

// satAddI is int64 addition that stops at the extremes: an exponent beyond
// ±2^62 is out of every exponent range MPFR has, wherever it stops.
func satAddI(a, b int64) int64 {
	s := a + b
	if (s > a) != (b > 0) {
		if b > 0 {
			return math.MaxInt64
		}
		return math.MinInt64
	}
	return s
}

// mpfrFn is one libmpfr function: the kinds of its arguments, one letter
// each (f a BigFloat, r a rounding mode, z an mpz_t, u an unsigned long, i a
// long, c an int, d a double, p a pointer, s a C string), checked once before
// the body runs, which then finds them as *mpfrRef, int, *Struct, uint64,
// int64, int32, float64, *Pointer and string.
type mpfrFn struct {
	sig string
	fn  func(vm *VM, a []any) (any, error)
}

// mpfrRef is a BigFloat argument: its memory and precision.
type mpfrRef struct {
	m    *Memory
	prec int64
}

// bootMPFR registers libmpfr's functions.
func (vm *VM) bootMPFR() {
	vm.mpfr = mpfrEnv{emin: mpfrEminDefault, emax: mpfrEmaxDefault}
	if vm.libShims == nil {
		vm.libShims = map[string]map[expr.Symbol]shim{}
	}
	fns := vm.mpfrFunctions()
	shims := make(map[expr.Symbol]shim, len(fns))
	for name, f := range fns {
		shims[expr.Symbol(name)] = func(vm *VM, a []any) (any, error) {
			args, err := vm.mpfrArgs(name, f.sig, a)
			if err != nil {
				return nil, err
			}
			return f.fn(vm, args)
		}
	}
	vm.libShims[mpfrLib] = shims
	vm.libShims[mpfrLibShort] = shims
}

// mpfrArgs checks a call's arguments against its signature.
func (vm *VM) mpfrArgs(fname, sig string, a []any) ([]any, error) {
	if len(a) != len(sig) {
		return nil, vm.errorf("%s: %d arguments, expected %d", fname, len(a), len(sig))
	}
	out := make([]any, len(a))
	for i := range a {
		var ok bool
		switch sig[i] {
		case 'f':
			var err error
			if out[i], err = vm.mpfrRefOf(fname, i, a[i]); err != nil {
				return nil, err
			}
			continue
		case 'r':
			pv, isPrim := a[i].(PrimValue)
			ok = isPrim && pv.Type.Name.Name == "MPFRRoundingMode"
			if ok {
				out[i] = int(int32(pv.Bits.lo))
			}
		case 'z':
			out[i], ok = mpzStruct(a[i], 3)
		case 'u':
			out[i], ok = a[i].(uint64)
		case 'i':
			out[i], ok = a[i].(int64)
		case 'c':
			out[i], ok = a[i].(int32)
		case 'd':
			out[i], ok = a[i].(float64)
		case 'p':
			out[i], ok = a[i].(*Pointer)
		case 's':
			out[i], ok = vm.cstringArg(a[i])
		}
		if !ok {
			return nil, vm.errorf("%s: argument %d is %s, not of kind %q", fname, i+1, showValue(a[i]), sig[i])
		}
	}
	return out, nil
}

// mpfrRefOf checks that v points at the start of a BigFloat's memory, and
// reads its precision.
func (vm *VM) mpfrRefOf(fname string, i int, v any) (*mpfrRef, error) {
	p, ok := v.(*Pointer)
	var m *Memory
	if ok {
		m, ok = p.Target.(*Memory)
	}
	if !ok || p.Off != 0 || m.Type != memUInt64Type || len(m.Elems) < mpfrLimbWord {
		return nil, vm.errorf("%s: argument %d is %s, not a BigFloat", fname, i+1, showValue(v))
	}
	prec, ok := memWord(m, 0)
	if !ok || int64(prec) < 1 || int64(prec) > math.MaxInt64-63 || (int64(prec)-1)/64+1 > int64(len(m.Elems)-mpfrLimbWord) {
		return nil, vm.errorf("%s: argument %d is a BigFloat whose precision %d does not fit its %d words", fname, i+1, int64(prec), len(m.Elems))
	}
	return &mpfrRef{m: m, prec: int64(prec)}, nil
}

// memWord is word k of a Memory{UInt64}: an unset one reads as zero.
func memWord(m *Memory, k int) (uint64, bool) {
	if w, ok := m.Elems[k].(uint64); ok {
		return w, true
	}
	return 0, m.Elems[k] == undef
}

// mpfrRead is the number a BigFloat holds.
func (vm *VM) mpfrRead(r *mpfrRef) (*mpfrNum, error) {
	sw, _ := memWord(r.m, 1)
	ew, _ := memWord(r.m, 2)
	x := &mpfrNum{neg: int32(sw) < 0}
	switch exp := int64(ew); exp {
	case mpfrExpZero:
		x.kind = kZero
		return x, nil
	case mpfrExpNaN:
		x.kind = kNaN
		return x, nil
	case mpfrExpInf:
		x.kind = kInf
		return x, nil
	default:
		x.kind, x.exp = kReg, exp
	}
	n := int((r.prec-1)/64 + 1)
	limbs := make([]uint64, n)
	for i := range limbs {
		w, ok := memWord(r.m, mpfrLimbWord+i)
		if !ok {
			return nil, vm.errorf("mpfr: a limb of a BigFloat is %s", showValue(r.m.Elems[mpfrLimbWord+i]))
		}
		limbs[i] = w
	}
	if limbs[n-1]>>63 == 0 {
		return nil, vm.errorf("mpfr: a BigFloat whose top limb is not normalized")
	}
	x.mant = limbsToInt(limbs, false)
	x.mant.Rsh(x.mant, x.mant.TrailingZeroBits())
	// the exponent as mant = limbs / 2^(64n): the stored one counts from the
	// top bit, as this one does
	return x, nil
}

// mpfrWrite leaves x in a BigFloat, which x fits (its mantissa has at most
// the precision's bits).
func (vm *VM) mpfrWrite(r *mpfrRef, x *mpfrNum) {
	setWord := func(k int, w uint64) { r.m.Elems[k] = w }
	setSign := func(neg bool) {
		old, _ := memWord(r.m, 1)
		s := int32(1)
		if neg {
			s = -1
		}
		setWord(1, old&^0xffffffff|uint64(uint32(s)))
	}
	special := [...]int64{kNaN: mpfrExpNaN, kInf: mpfrExpInf, kZero: mpfrExpZero}
	switch x.kind {
	case kNaN:
		setWord(2, uint64(special[kNaN]))
	case kInf, kZero:
		setSign(x.neg)
		setWord(2, uint64(special[x.kind]))
	default:
		setSign(x.neg)
		setWord(2, uint64(x.exp))
		n := int((r.prec-1)/64 + 1)
		m := new(big.Int).Lsh(x.mant, uint(n*64-x.mant.BitLen()))
		l := intToLimbs(m)
		for i := 0; i < n; i++ {
			w := uint64(0)
			if i < len(l) {
				w = l[i]
			}
			setWord(mpfrLimbWord+i, w)
		}
	}
}

// ---- rounding ----

// roundShift drops the sh low bits of m > 0, rounding in rnd: the quotient,
// whether it was rounded up, and whether anything was lost. sticky says the
// value is a little more than m.
func roundShift(m *big.Int, sh uint, sticky, neg bool, rnd int) (q *big.Int, up, inexact bool) {
	q = new(big.Int).Rsh(m, sh)
	half := sh > 0 && m.Bit(int(sh-1)) == 1
	low := sh > 1 && m.TrailingZeroBits() < sh-1
	inexact = half || low || sticky
	switch rnd {
	case rndN:
		up = half && (low || sticky || q.Bit(0) == 1)
	case rndNA:
		up = half
	case rndU:
		up = !neg && inexact
	case rndD:
		up = neg && inexact
	case rndA:
		up = inexact
	}
	if up {
		q.Add(q, bigOne)
	}
	return q, up, inexact
}

// mpfrRoundBits rounds ±m * 2^(ex - bitlen(m)), plus a fraction when sticky,
// to prec bits in rnd with no limit on the exponent: the mantissa, the
// exponent, and the ternary value (the sign of the rounded value minus the
// exact one).
func mpfrRoundBits(neg bool, m *big.Int, ex int64, sticky bool, prec int64, rnd int) (*big.Int, int64, int) {
	bl := int64(m.BitLen())
	if sticky && bl < prec+2 {
		m = new(big.Int).Lsh(m, uint(prec+2-bl))
		bl = prec + 2
	}
	if bl <= prec {
		return m, ex, 0
	}
	q, up, inexact := roundShift(m, uint(bl-prec), sticky, neg, rnd)
	t := 0
	if inexact {
		t = -1
		if up {
			t = 1
		}
		if neg {
			t = -t
		}
	}
	if up && int64(q.BitLen()) > prec {
		q.Rsh(q, 1)
		ex = satAddI(ex, 1)
	}
	return q, ex, t
}

func isPow2(m *big.Int) bool { return m.TrailingZeroBits()+1 == uint(m.BitLen()) }

// mpfrFinish is mpfr_check_range: the rounded mantissa and exponent (t the
// ternary value of the rounding) as a number of the exponent range, or the
// underflow or overflow it comes to; it sets the inexact flag.
func (vm *VM) mpfrFinish(neg bool, m *big.Int, ex int64, t int, prec int64, rnd int) (*mpfrNum, int) {
	e := &vm.mpfr
	switch {
	case ex < e.emin:
		// in rounding to nearest, mpfr_underflow rounds away from zero: round
		// to zero when |x| < 2^(emin-2), or |x| = 2^(emin-2) and the exact
		// value is no more
		if rnd == rndN && (ex+1 < e.emin || isPow2(m) && (neg && t <= 0 || !neg && t >= 0)) {
			rnd = rndZ
		}
		return vm.mpfrUnderflow(neg, rnd, prec)
	case ex > e.emax:
		return vm.mpfrOverflow(neg, rnd, prec)
	}
	if t != 0 {
		e.flags |= flagInexact
	}
	return &mpfrNum{kind: kReg, neg: neg, exp: ex, mant: m}, t
}

// likeZ is MPFR_IS_LIKE_RNDZ.
func likeZ(rnd int, neg bool) bool {
	return rnd == rndZ || rnd == rndU && neg || rnd == rndD && !neg
}

// mpfrUnderflow is mpfr_underflow: zero, or the smallest positive number,
// by the rounding mode.
func (vm *VM) mpfrUnderflow(neg bool, rnd int, prec int64) (*mpfrNum, int) {
	vm.mpfr.flags |= flagUnderflow | flagInexact
	if likeZ(rnd, neg) {
		return zeroNum(neg), signInt(neg)
	}
	return &mpfrNum{kind: kReg, neg: neg, exp: vm.mpfr.emin, mant: new(big.Int).Lsh(bigOne, 0)}, -signInt(neg)
}

// mpfrOverflow is mpfr_overflow: infinity, or the largest number, by the
// rounding mode.
func (vm *VM) mpfrOverflow(neg bool, rnd int, prec int64) (*mpfrNum, int) {
	vm.mpfr.flags |= flagOverflow | flagInexact
	if likeZ(rnd, neg) {
		m := new(big.Int).Lsh(bigOne, uint(prec))
		return &mpfrNum{kind: kReg, neg: neg, exp: vm.mpfr.emax, mant: m.Sub(m, bigOne)}, signInt(neg)
	}
	return infNum(neg), -signInt(neg)
}

// signInt is +1 for a positive value (neg false) and -1 for a negative one.
func signInt(neg bool) int {
	if neg {
		return -1
	}
	return 1
}

// mpfrParts rounds ±m * 2^(ex - bitlen(m)) (plus a fraction when sticky) to
// prec bits in rnd, and checks the exponent range.
func (vm *VM) mpfrParts(neg bool, m *big.Int, ex int64, sticky bool, prec int64, rnd int) (*mpfrNum, int) {
	q, ex, t := mpfrRoundBits(neg, m, ex, sticky, prec, rnd)
	return vm.mpfrFinish(neg, q, ex, t, prec, rnd)
}

// nanResult is a NaN with its flag.
func (vm *VM) nanResult() (*mpfrNum, int) {
	vm.mpfr.flags |= flagNaN
	return nanNum(), 0
}

// mpfrSet is mpfr_set: x rounded to prec bits.
func (vm *VM) mpfrSet(x *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
	switch x.kind {
	case kNaN:
		return vm.nanResult()
	case kReg:
		return vm.mpfrParts(x.neg, x.mant, x.exp, false, prec, rnd)
	}
	return x, 0
}

// ---- operands ----

func numOfInt(v *big.Int) *mpfrNum {
	if v.Sign() == 0 {
		return zeroNum(false)
	}
	m := new(big.Int).Abs(v)
	bl := int64(m.BitLen())
	return &mpfrNum{kind: kReg, neg: v.Sign() < 0, exp: bl, mant: m.Rsh(m, m.TrailingZeroBits())}
}

func numOfFloat(d float64) *mpfrNum {
	switch {
	case math.IsNaN(d):
		return nanNum()
	case math.IsInf(d, 0):
		return infNum(d < 0)
	case d == 0:
		return zeroNum(math.Signbit(d))
	}
	f, e := math.Frexp(math.Abs(d))
	m := uint64(f * (1 << 53))
	m >>= bits.TrailingZeros64(m)
	return &mpfrNum{kind: kReg, neg: d < 0, exp: int64(e), mant: new(big.Int).SetUint64(m)}
}

func numOfUint(u uint64) *mpfrNum { return numOfInt(new(big.Int).SetUint64(u)) }
func numOfInt64(i int64) *mpfrNum { return numOfInt(big.NewInt(i)) }

// ---- exact reading of the memory words ----

// mpfrNumArg reads a BigFloat argument.
func (vm *VM) mpfrNumArg(a any) (*mpfrNum, error) { return vm.mpfrRead(a.(*mpfrRef)) }

// roundingArg is the rounding mode of an argument, refused when it is not
// one of the five MPFR defines exactly: faithful rounding (MPFR_RNDF) leaves
// the result to the implementation, and any other number is not a mode.
func (vm *VM) roundingArg(fname string, rnd any) (int, error) {
	r := rnd.(int)
	if r < rndN || r > rndA {
		return 0, fmt.Errorf("%w: %s in rounding mode %d (only the five of MPFR's directed and nearest modes are ported)", ErrUnsupported, fname, r)
	}
	return r, nil
}

// mpfrRet writes x in r and returns the ternary value, which is the sign
// MPFR's functions return.
func (vm *VM) mpfrRet(r *mpfrRef, x *mpfrNum, t int) (any, error) {
	vm.mpfrWrite(r, x)
	return int32(t), nil
}

// bigFloatOf is x as a big.Float: exact, for a number whose exponent is
// math/big's.
func bigFloatOf(x *mpfrNum) (*big.Float, bool) {
	var f *big.Float
	switch x.kind {
	case kZero:
		f = new(big.Float)
	case kInf:
		return new(big.Float).SetInf(x.neg), true
	default:
		if x.exp < math.MinInt32 || x.exp > math.MaxInt32 {
			return nil, false
		}
		bl := x.mant.BitLen()
		f = new(big.Float).SetPrec(uint(bl)).SetInt(x.mant)
		f.SetMantExp(f, int(x.exp)-bl)
	}
	if x.neg {
		f.Neg(f)
	}
	return f, true
}
