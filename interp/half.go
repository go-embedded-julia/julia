package interp

import (
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Float16 and BFloat16, the values of julia's 16-bit float types: their bit
// patterns. julia's runtime computes on them in Float32 and rounds back
// (runtime_intrinsics.c:762-817, 883-895); these are its conversions.
type (
	Float16  uint16
	BFloat16 uint16
)

// halfToFloat is half_to_float (runtime_intrinsics.c:18-63).
func halfToFloat(ival uint16) float32 {
	sign := uint32(ival&0x8000) >> 15
	exp := uint32(ival&0x7c00) >> 10
	sig := uint32(ival & 0x3ff)
	var ret uint32
	switch {
	case exp == 0 && sig == 0:
		ret = sign << 31
	case exp == 0:
		nBit := uint32(1)
		bit := uint32(0x0200)
		for bit&sig == 0 {
			nBit++
			bit >>= 1
		}
		ret = sign<<31 | (127-14-nBit)<<23 | ((sig&^bit)<<nBit)<<(23-10)
	case exp == 0x1f && sig == 0:
		ret = 0x7f800000 | sign<<31
	case exp == 0x1f:
		ret = 0x7fc00000 | sign<<31 | sig<<(23-10)
	default:
		ret = sign<<31 | (exp-15+127)<<23 | sig<<(23-10)
	}
	return math.Float32frombits(ret)
}

// basetable and shifttable are the tables of runtime_intrinsics.c:71-157,
// generated from their layout: for each float32 sign and biased exponent i,
// the half's base bits and the shift of the significand (implicit bit set).
// A subnormal half has base 0 and keeps the implicit bit, shifted by
// 126 - i; below i = 102 everything shifts out.
var basetable, shifttable = func() (b [512]uint16, s [512]uint8) {
	for i := 0; i < 256; i++ {
		switch {
		case i < 102:
			b[i], s[i] = 0x0000, 25
		case i <= 112: // subnormal
			b[i], s[i] = 0x0000, uint8(126-i)
		case i <= 142: // normal
			b[i], s[i] = uint16(i-112)<<10, 13
		case i < 255: // overflow to infinity
			b[i], s[i] = 0x7c00, 24
		default: // infinity and NaN
			b[i], s[i] = 0x7c00, 13
		}
		b[i|0x100], s[i|0x100] = b[i]|0x8000, s[i]
	}
	return
}()

// floatToHalf is float_to_half (runtime_intrinsics.c:159-188): round to
// nearest, ties to even; a NaN is quieted keeping its sign and the upper
// bits of its payload.
func floatToHalf(param float32) uint16 {
	f := math.Float32bits(param)
	if param != param {
		return uint16(f>>16&0x8000) | 0x7c00 | 0x0200 | uint16(f>>13&0x03ff)
	}
	i := (f &^ 0x007fffff) >> 23
	sh := shifttable[i]
	f &= 0x007fffff
	f |= 0x007fffff + 1
	h := basetable[i] + uint16((f>>sh)&0x03ff)
	nextbit := (f >> (sh - 1)) & 1
	if nextbit != 0 && h&0x7c00 != 0x7c00 {
		if h&1 == 1 || f&(1<<(sh-1)-1) != 0 {
			h++
		}
	}
	return h
}

// doubleToHalf is double_to_half (runtime_intrinsics.c:190-215): a Float64
// rounded once to a half, through a Float32 nudged off a tie.
func doubleToHalf(param float64) uint16 {
	temp := float32(param)
	tempi := math.Float32bits(temp)
	if tempi&0x7fffffff < 0x38800000 {
		if shift := 113 - (tempi&0x7f800000)>>23; shift < 23 {
			tempi |= 0x00800000
			tempi >>= shift
		}
	}
	if tempi&0x1fff == 0x1000 {
		tempi = math.Float32bits(temp)
		tempi += nudge(float64(temp), param)
		temp = math.Float32frombits(tempi)
	}
	return floatToHalf(temp)
}

// nudge is (fabs(temp) < fabs(param)) - (fabs(param) < fabs(temp)) as a
// uint32 step.
func nudge(temp, param float64) uint32 {
	var d uint32
	if math.Abs(temp) < math.Abs(param) {
		d++
	}
	if math.Abs(param) < math.Abs(temp) {
		d--
	}
	return d
}

// floatToBfloat is float_to_bfloat (runtime_intrinsics.c:307-317).
func floatToBfloat(param float32) uint16 {
	if param != param {
		return 0x7fc0
	}
	bits := math.Float32bits(param)
	bits += 0x7fff + (bits>>16)&1
	return uint16(bits >> 16)
}

// doubleToBfloat is double_to_bfloat (runtime_intrinsics.c:319-336).
func doubleToBfloat(param float64) uint16 {
	temp := float32(param)
	tempi := math.Float32bits(temp)
	if tempi&0x1ff == 0x100 {
		tempi += nudge(float64(temp), param)
		temp = math.Float32frombits(tempi)
	}
	return floatToBfloat(temp)
}

// bfloatToFloat is bfloat_to_float (runtime_intrinsics.c:338-344).
func bfloatToFloat(param uint16) float32 { return math.Float32frombits(uint32(param) << 16) }

// showHalf is Ryu.writeshortest (base/ryu/shortest.jl:228-456) for a
// Float16 with typed set, as show prints one (Float16(1.5)), or without it,
// as an array whose element type is Float16 prints its elements.
func showHalf(h uint16, typed bool) string {
	x := float64(halfToFloat(h))
	open, close := "", ""
	if typed {
		open, close = "Float16(", ")"
	}
	sign := ""
	if math.Signbit(x) {
		sign = "-"
	}
	special := func(s string) string {
		if typed {
			s += "16"
		}
		return s
	}
	switch {
	case x == 0:
		return open + sign + "0.0" + close
	case math.IsNaN(x):
		// show(io, ::Float16) prints a NaN without its sign
		return special("NaN")
	case math.IsInf(x, 0):
		return sign + special("Inf")
	}
	digits, nexp := shortestHalf(h &^ 0x8000)
	olength := len(digits)
	pt := nexp + olength
	var b strings.Builder
	b.WriteString(open + sign)
	if -4 < pt && pt <= 3 && !(pt >= olength && math.Abs(juliaMod(x+0.05, math.Pow(10, float64(pt-olength)))-0.05) > 0.05) {
		switch {
		case pt <= 0:
			b.WriteString("0." + strings.Repeat("0", -pt) + digits)
		case pt >= olength:
			b.WriteString(digits + strings.Repeat("0", nexp) + ".0")
		default:
			b.WriteString(digits[:pt] + "." + digits[pt:])
		}
	} else {
		rest := digits[1:]
		if rest == "" {
			rest = "0"
		}
		b.WriteString(digits[:1] + "." + rest + "e" + strconv.Itoa(nexp+olength-1))
	}
	b.WriteString(close)
	return b.String()
}

// juliaMod is mod(x, y) of two Float64s: the remainder with y's sign.
func juliaMod(x, y float64) float64 {
	r := math.Mod(x, y)
	if r != 0 && (r < 0) != (y < 0) {
		r += y
	}
	return r
}

// shortestHalf is reduce_shortest for a positive finite Float16: the
// fewest decimal digits that read back as h, without trailing zeros, and
// the power of ten of the last one. Of the decimals of that length that do,
// the nearest to h wins, as Ryu's rounding of its last digit picks it, the
// even one on a tie. All 65536 are julia's (TestHalfAgainstJulia).
func shortestHalf(h uint16) (string, int) {
	x := float64(halfToFloat(h))
	// a Float16 needs at most 5 digits; 17 (exact for a float64) bounds
	// the search whatever the conversions answer
	for prec := 1; prec < 17; prec++ {
		mant, exp := decimalAt(x, prec)
		best, found := uint64(0), false
		var bd *big.Rat
		for _, m := range []uint64{mant - 1, mant, mant + 1} {
			r := decimalRat(m, exp)
			v, _ := r.Float64()
			if m == 0 || doubleToHalf(v) != h {
				continue
			}
			// the distance exactly: on a tie the even last digit, as Ryu
			// rounds (2.1875 is 2.188)
			d := r.Sub(r, new(big.Rat).SetFloat64(x))
			d.Abs(d)
			better := !found
			if found {
				c := d.Cmp(bd)
				better = c < 0 || c == 0 && m%2 == 0
			}
			if better {
				best, found, bd = m, true, d
			}
		}
		// no trailing zero: one would have been found a digit shorter
		if found {
			return strconv.FormatUint(best, 10), exp
		}
	}
	mant, exp := decimalAt(x, 17)
	return strconv.FormatUint(mant, 10), exp
}

// decimalRat is m * 10^exp exactly.
func decimalRat(m uint64, exp int) *big.Rat {
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(max(exp, -exp))), nil)
	r := new(big.Rat).SetInt(new(big.Int).SetUint64(m))
	if exp >= 0 {
		return r.Mul(r, new(big.Rat).SetInt(p))
	}
	return r.Quo(r, new(big.Rat).SetInt(p))
}

// decimalAt is x rounded to prec significant digits: mant * 10^exp, with
// mant of prec digits.
func decimalAt(x float64, prec int) (uint64, int) {
	s := strconv.FormatFloat(x, 'e', prec-1, 64)
	m, e, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(e)
	mant, _ := strconv.ParseUint(strings.Replace(m, ".", "", 1), 10, 64)
	return mant, exp - (prec - 1)
}
