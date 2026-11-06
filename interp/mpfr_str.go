package interp

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
)

// Strings: mpfr_set_str (mpfr_strtofr's grammar,
// https://www.mpfr.org/mpfr-4.2.2/mpfr.html#Assignment-Functions) and
// mpfr_asprintf for the two formats mpfr.jl passes (%Re and %.<k>Re).

// mpfrParsed is a string's number: NaN, infinity, or ±n * b^m * 2^c with n,
// m and c integers (c is the binary exponent of a 0x or 0b number).
type mpfrParsed struct {
	kind int
	neg  bool
	n    *big.Int
	b    int
	m, c *big.Int
}

// hasPrefixFold is strings.HasPrefix without the case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
}

// mpfrParse reads s as mpfr_strtofr does in base (0, or 2 to 62): the whole
// string must be a number, after leading white space.
func mpfrParse(s string, base int) (mpfrParsed, bool) {
	s = strings.TrimLeftFunc(s, func(r rune) bool { return r < 128 && isCSpace(byte(r)) })
	p := mpfrParsed{kind: kReg}
	if s != "" && (s[0] == '+' || s[0] == '-') {
		p.neg = s[0] == '-'
		s = s[1:]
	}
	// NaN and infinity, in any case
	for _, sp := range []struct {
		name string
		kind int
	}{{"@nan@", kNaN}, {"nan", kNaN}, {"@inf@", kInf}, {"infinity", kInf}, {"inf", kInf}} {
		if !hasPrefixFold(s, sp.name) {
			continue
		}
		rest := s[len(sp.name):]
		if sp.name == "nan" && strings.HasPrefix(rest, "(") {
			// nan(n-char-sequence)
			i := 1
			for i < len(rest) && (rest[i] == '_' || '0' <= rest[i] && rest[i] <= '9' || 'a' <= rest[i]|32 && rest[i]|32 <= 'z') {
				i++
			}
			if i < len(rest) && rest[i] == ')' {
				rest = rest[i+1:]
			}
		}
		p.kind = sp.kind
		return p, rest == ""
	}
	if base < 0 || base == 1 || base > 62 {
		return p, false
	}
	switch {
	case (base == 0 || base == 16) && hasPrefixFold(s, "0x"):
		base, s = 16, s[2:]
	case (base == 0 || base == 2) && hasPrefixFold(s, "0b"):
		base, s = 2, s[2:]
	case base == 0:
		base = 10
	}
	p.b = base
	// digits with at most one point
	digits := []byte{}
	frac := int64(0)
	point := false
	i := 0
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' && !point {
			point = true
			continue
		}
		if gmpDigitValue(c, base) >= base {
			break
		}
		digits = append(digits, byte(swapCase(rune(c))))
		if point {
			frac++
		}
	}
	if len(digits) == 0 {
		return p, false
	}
	p.n = new(big.Int)
	p.n.SetString(string(digits), base)
	p.m, p.c = big.NewInt(-frac), new(big.Int)
	s = s[i:]
	if s == "" {
		return p, true
	}
	// an exponent: @ always, e or E in bases up to 10, p or P in powers of 2
	binary := false
	switch c := s[0]; {
	case c == '@', (c == 'e' || c == 'E') && base <= 10:
	case (c == 'p' || c == 'P') && base&(base-1) == 0:
		binary = true
	default:
		return p, false
	}
	s = s[1:]
	eneg := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		eneg = s[0] == '-'
		s = s[1:]
	}
	if s == "" {
		return p, false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return p, false
		}
	}
	e, _ := new(big.Int).SetString(s, 10)
	if eneg {
		e.Neg(e)
	}
	if binary {
		p.c = e
	} else {
		p.m.Add(p.m, e)
	}
	return p, true
}

// mpfrOfParsed is the parsed number rounded to prec bits in rnd. A power of
// the base too large for the VM is refused, unless it is so large that the
// value is out of every exponent range.
func (vm *VM) mpfrOfParsed(p mpfrParsed, prec int64, rnd int) (*mpfrNum, int, error) {
	switch p.kind {
	case kNaN:
		x, t := vm.nanResult()
		return x, t, nil
	case kInf:
		return infNum(p.neg), 0, nil
	}
	if p.n.Sign() == 0 {
		return zeroNum(p.neg), 0, nil
	}
	// the binary exponent, clamped: past 2^63 the value is out of range anyway
	c := int64(math.MaxInt64)
	if p.c.IsInt64() {
		c = p.c.Int64()
	} else if p.c.Sign() < 0 {
		c = math.MinInt64
	}
	// b^|m| has |m| log2 b bits
	m := new(big.Int).Abs(p.m)
	est := math.Inf(1)
	if f, _ := new(big.Float).SetInt(m).Float64(); !math.IsInf(f, 0) {
		est = f * math.Log2(float64(p.b))
	}
	if est > float64(vm.limitBits()) {
		if est < 1<<63 {
			return nil, 0, fmt.Errorf("%w: mpfr_set_str of a power of %d with %s digits (VM.MaxMemoryElems is %d limbs)", ErrMemoryLimit, p.b, m, vm.MaxMemoryElems)
		}
		if p.m.Sign() > 0 {
			x, t := vm.mpfrOverflow(p.neg, rnd, prec)
			return x, t, nil
		}
		if rnd == rndN {
			rnd = rndZ
		}
		x, t := vm.mpfrUnderflow(p.neg, rnd, prec)
		return x, t, nil
	}
	pw := new(big.Int).Exp(big.NewInt(int64(p.b)), m, nil)
	if p.m.Sign() >= 0 {
		v := new(big.Int).Mul(p.n, pw)
		x, t := vm.mpfrParts(p.neg, v, satAddI(c, int64(v.BitLen())), false, prec, rnd)
		return x, t, nil
	}
	s := max(0, prec+3+int64(pw.BitLen())-int64(p.n.BitLen()))
	q, r := new(big.Int).QuoRem(new(big.Int).Lsh(p.n, uint(s)), pw, new(big.Int))
	x, t := vm.mpfrParts(p.neg, q, satAddI(satAddI(c, -s), int64(q.BitLen())), r.Sign() != 0, prec, rnd)
	return x, t, nil
}

// printfFormat parses the formats mpfr.jl gives mpfr_asprintf: %Re, and
// %.<k>Re; the precision is -1 when missing.
func printfFormat(f string) (prec int64, ok bool) {
	rest, found := strings.CutPrefix(f, "%")
	if !found {
		return 0, false
	}
	rest, found = strings.CutSuffix(rest, "Re")
	if !found {
		return 0, false
	}
	if rest == "" {
		return -1, true
	}
	d, found := strings.CutPrefix(rest, ".")
	if !found || d == "" {
		return 0, false
	}
	for _, c := range []byte(d) {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	k, err := strconv.ParseInt(d, 10, 64)
	return k, err == nil
}

// pow10Cmp compares num/den with 10^j.
func pow10Cmp(num, den *big.Int, j int64) int {
	a, b := new(big.Int).Set(num), new(big.Int).Set(den)
	p := new(big.Int).Exp(big.NewInt(10), big.NewInt(max(j, -j)), nil)
	if j >= 0 {
		b.Mul(b, p)
	} else {
		a.Mul(a, p)
	}
	return a.Cmp(b)
}

// formatE is x, of precision xprec, as %Re and %.<prec>Re print it: one digit,
// a point and the others, rounded to nearest, and a signed exponent of at
// least two digits. Without a precision the digits are those that tell the
// number from its neighbours, 1+ceil(xprec*log10(2)).
func (vm *VM) formatE(x *mpfrNum, xprec, prec int64) (string, error) {
	sign := ""
	if x.neg {
		sign = "-"
	}
	switch x.kind {
	case kNaN:
		return "nan", nil
	case kInf:
		return sign + "inf", nil
	}
	n := prec + 1
	if prec < 0 {
		n = 1 + int64(math.Ceil(float64(xprec)*math.Log10(2)))
	}
	if err := vm.gmpGuard("mpfr_asprintf", uint64(n)); err != nil {
		return "", err
	}
	digits, e10 := strings.Repeat("0", int(n)), int64(0)
	if x.kind == kReg {
		if err := vm.gmpGuard("mpfr_asprintf", uint64(max(x.exp, -x.exp))); err != nil {
			return "", err
		}
		l := x.lsb()
		num, den := new(big.Int).Set(x.mant), big.NewInt(1)
		if l >= 0 {
			num.Lsh(num, uint(l))
		} else {
			den.Lsh(den, uint(-l))
		}
		e10 = int64(math.Floor(float64(x.exp-1) * math.Log10(2)))
		for pow10Cmp(num, den, e10) < 0 {
			e10--
		}
		for pow10Cmp(num, den, e10+1) >= 0 {
			e10++
		}
		// d = round(x / 10^k), k = e10-n+1
		k := e10 - n + 1
		pw := new(big.Int).Exp(big.NewInt(10), big.NewInt(max(k, -k)), nil)
		if k >= 0 {
			den.Mul(den, pw)
		} else {
			num.Mul(num, pw)
		}
		d, r := new(big.Int).QuoRem(num, den, new(big.Int))
		if c := r.Lsh(r, 1).Cmp(den); c > 0 || c == 0 && d.Bit(0) == 1 {
			d.Add(d, bigOne)
		}
		if len(d.String()) > int(n) {
			// rounded up to 10^n
			d.Quo(d, big.NewInt(10))
			e10++
		}
		digits = d.String()
	}
	var sb strings.Builder
	sb.WriteString(sign)
	sb.WriteByte(digits[0])
	if n > 1 {
		sb.WriteByte('.')
		sb.WriteString(digits[1:])
	}
	es := "+"
	if e10 < 0 {
		es, e10 = "-", -e10
	}
	fmt.Fprintf(&sb, "e%s%02d", es, e10)
	return sb.String(), nil
}
