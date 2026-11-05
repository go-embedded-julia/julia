package interp

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"math/bits"
	"strings"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// libgmp: the GMP functions Base's GMP module calls (gmp.jl), backed by
// math/big. GMP's integer results are fully determined by the manual's
// definitions (https://gmplib.org/manual/Integer-Functions), so math/big
// answers them exactly; the interpreter models the GMP julia 1.12.6 ships,
// 6.3.0, and the darwin build's library name.
//
// A BigInt is `mutable struct BigInt; alloc::Cint; size::Cint; d::Ptr{Limb}`:
// size is the signed count of limbs and d a Ptr{UInt64} into a
// Memory{UInt64} of at least alloc little-endian limbs, which Base reads
// directly (hash, unsafe_load(x.d, i), unsafe_store!(z.d, ...)). After every
// shim the three fields are what GMP would leave, except alloc, which GMP
// leaves to its allocator's rounding: here it is the memory's length, at
// least the limbs the value needs.

const (
	gmpLib     = "@rpath/libgmp.10.dylib"
	gmpVersion = "6.3.0"
)

var memUInt64Type = must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.UInt64Type, core.CPU)).(*core.DataType)

// gmpFn is one GMP function: the kinds of its arguments, one letter each
// (z an mpz_t, q an mpq_t, u an unsigned long or size_t, i a long, c an int,
// d a double, p a pointer), checked once before the body runs, which then
// finds them as *Struct, uint64, int64, int32, float64 and *Pointer.
type gmpFn struct {
	sig string
	fn  func(vm *VM, a []any) (any, error)
}

// bootGMP registers libgmp's functions, and the C globals and runtime entry
// points gmp.jl's load reaches.
func (vm *VM) bootGMP() {
	if vm.libShims == nil {
		vm.libShims = map[string]map[expr.Symbol]shim{}
	}
	fns := vm.gmpFunctions()
	shims := make(map[expr.Symbol]shim, len(fns))
	for name, f := range fns {
		shims[expr.Symbol(name)] = func(vm *VM, a []any) (any, error) {
			args, err := vm.gmpArgs(name, f.sig, a)
			if err != nil {
				return nil, err
			}
			return f.fn(vm, args)
		}
	}
	vm.libShims[gmpLib] = shims
	// Core.getptls and the pointer finalizer gmp.jl registers (BigInt's
	// finalizer is __gmpz_clear): this VM runs no Julia finalizers
	// (jl_gc_disable_finalizers_internal), and a Go value is collected whole
	vm.shims["jl_get_ptls_states"] = func(vm *VM, a []any) (any, error) {
		return &Pointer{Type: vm.ptrType(core.NothingType), Target: ptlsStates{}}, nil
	}
	vm.shims["jl_gc_add_ptr_finalizer"] = func(vm *VM, a []any) (any, error) { return nil, nil }
}

// ptlsStates is what jl_get_ptls_states points at.
type ptlsStates struct{}

// bootGMPGlobals makes the C variables of libgmp cglobal names, and the
// runtime functions whose addresses gmp.jl hands to GMP.
func (vm *VM) bootGMPGlobals() {
	vm.libCglobals = map[string]map[expr.Symbol]*ptrSlot{gmpLib: {
		"__gmp_version":       {name: "__gmp_version", val: vm.stringPtrOf(gmpVersion)},
		"__gmp_bits_per_limb": {name: "__gmp_bits_per_limb", val: int32(64)},
	}}
	for _, n := range []expr.Symbol{"jl_gc_counted_malloc", "jl_gc_counted_realloc_with_old_size",
		"jl_gc_counted_free_with_size", "jl_throw_out_of_memory_error"} {
		vm.cglobals[n] = &ptrSlot{name: n}
	}
}

// gmpArgs checks a call's arguments against its signature.
func (vm *VM) gmpArgs(fname, sig string, a []any) ([]any, error) {
	if len(a) != len(sig) {
		return nil, vm.errorf("%s: %d arguments, expected %d", fname, len(a), len(sig))
	}
	out := make([]any, len(a))
	for i := range a {
		var ok bool
		switch sig[i] {
		case 'z':
			out[i], ok = mpzStruct(a[i], 3)
		case 'q':
			out[i], ok = mpzStruct(a[i], 7)
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
		}
		if !ok {
			return nil, vm.errorf("%s: argument %d is %s, not of kind %q", fname, i+1, showValue(a[i]), sig[i])
		}
	}
	return out, nil
}

// mpzStruct is the struct a Ref{BigInt} (n = 3 fields) or a Ref of the MPQ
// module's _MPQ (n = 7) points at: a mutable struct whose fields start
// (Cint, Cint, Ptr), once for a BigInt, twice for a rational.
func mpzStruct(v any, n int) (*Struct, bool) {
	p, ok := v.(*Pointer)
	if !ok {
		return nil, false
	}
	st, ok := p.Target.(*Struct)
	if !ok || p.Off != 0 || len(st.Fields) != n || !st.Type.Name.Mutable {
		return nil, false
	}
	ft, _ := st.Type.FieldTypes()
	for i := 0; i < n && i < 6; i += 3 {
		d, isPtr := ft[i+2].(*core.DataType)
		if ft[i] != any(core.Int32Type) || ft[i+1] != any(core.Int32Type) || !isPtr || d.Name != typeNameOf(core.PtrType) {
			return nil, false
		}
	}
	return st, true
}

// limitBits is the largest integer, in bits, a result may have.
func (vm *VM) limitBits() uint64 { return uint64(vm.MaxMemoryElems) * 64 }

// gmpGuard refuses a result of more than limitBits bits, before it is
// computed: GMP aborts on a memory it cannot get.
func (vm *VM) gmpGuard(fname string, nbits uint64) error {
	if nbits > vm.limitBits() {
		return fmt.Errorf("%w: %s of a result of %d bits (VM.MaxMemoryElems is %d limbs)", ErrMemoryLimit, fname, nbits, vm.MaxMemoryElems)
	}
	return nil
}

// satAdd and satMul are uint64 arithmetic that stops at the maximum.
func satAdd(a, b uint64) uint64 {
	s, c := bits.Add64(a, b, 0)
	if c != 0 {
		return math.MaxUint64
	}
	return s
}

func satMul(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	if hi != 0 {
		return math.MaxUint64
	}
	return lo
}

// limbsAt is the n limbs a Ptr{Limb} points at.
func (vm *VM) limbsAt(p *Pointer, n uint64) ([]uint64, error) {
	m, ok := p.Target.(*Memory)
	if !ok || m.Type != memUInt64Type || p.Off < 0 || p.Off%8 != 0 || p.Off/8 > int64(len(m.Elems)) || n > uint64(int64(len(m.Elems))-p.Off/8) {
		return nil, vm.errorf("gmp: %d limbs at %s are not in a Memory{UInt64}", n, showValue(p))
	}
	l := make([]uint64, n)
	for i := range l {
		w, ok := m.Elems[p.Off/8+int64(i)].(uint64)
		if !ok {
			return nil, vm.exception(vm.tUndefRef)
		}
		l[i] = w
	}
	return l, nil
}

func limbsToInt(l []uint64, neg bool) *big.Int {
	b := make([]byte, 8*len(l))
	for i, w := range l {
		binary.BigEndian.PutUint64(b[8*(len(l)-1-i):], w)
	}
	z := new(big.Int).SetBytes(b)
	if neg {
		z.Neg(z)
	}
	return z
}

// intToLimbs is |v|'s limbs, least significant first.
func intToLimbs(v *big.Int) []uint64 {
	b := v.Bytes()
	l := make([]uint64, (len(b)+7)/8)
	for i := range l {
		hi := len(b) - 8*i
		lo := max(hi-8, 0)
		var w [8]byte
		copy(w[8-(hi-lo):], b[lo:hi])
		l[i] = binary.BigEndian.Uint64(w[:])
	}
	return l
}

// getMpz is the integer a BigInt's three fields (alloc, size, d) hold.
func (vm *VM) getMpz(f []any) (*big.Int, error) {
	size, ok1 := f[1].(int32)
	d, ok2 := f[2].(*Pointer)
	if !ok1 || !ok2 {
		return nil, vm.errorf("gmp: an uninitialized mpz_t")
	}
	if size == 0 {
		return new(big.Int), nil
	}
	n := int64(size)
	if n < 0 {
		n = -n
	}
	l, err := vm.limbsAt(d, uint64(n))
	if err != nil {
		return nil, err
	}
	return limbsToInt(l, size < 0), nil
}

func (vm *VM) newLimbs(n int) *Memory {
	m := &Memory{Type: memUInt64Type, Elems: make([]any, n)}
	for i := range m.Elems {
		m.Elems[i] = uint64(0)
	}
	return m
}

// putMpz leaves v in a BigInt's fields, in the memory it has when that is
// large enough.
func (vm *VM) putMpz(f []any, v *big.Int) error {
	return vm.putMpzAlloc(f, v, 0, true)
}

// putMpzAlloc is putMpz with a memory of at least alloc limbs, the BigInt's
// own only when reuse says so.
func (vm *VM) putMpzAlloc(f []any, v *big.Int, alloc int, reuse bool) error {
	l := intToLimbs(v)
	if len(l) > vm.MaxMemoryElems || alloc > vm.MaxMemoryElems {
		return fmt.Errorf("%w: an integer of %d limbs (VM.MaxMemoryElems is %d)", ErrMemoryLimit, max(len(l), alloc), vm.MaxMemoryElems)
	}
	need := max(len(l), alloc, 1)
	var m *Memory
	if d, ok := f[2].(*Pointer); ok && reuse {
		if mem, ok := d.Target.(*Memory); ok && mem.Type == memUInt64Type && d.Off == 0 && len(mem.Elems) >= need {
			m = mem
		}
	}
	if m == nil {
		m = vm.newLimbs(need)
	}
	for i, w := range l {
		m.Elems[i] = w
	}
	size := int32(len(l))
	if v.Sign() < 0 {
		size = -size
	}
	f[0], f[1], f[2] = int32(len(m.Elems)), size, &Pointer{Type: vm.ptrType(core.UInt64Type), Target: m}
	return nil
}

// cld64 is ceil(a/b) for b > 0.
func cld64(a, b uint64) uint64 {
	q := a / b
	if a%b != 0 {
		q++
	}
	return q
}

var bigOne = big.NewInt(1)

// divMode is mpz_{c,f,t}div_{q,r}'s rounding: 1 up, -1 down, 0 to zero.
func divMode(q, r, a, b *big.Int, mode int) {
	q.QuoRem(a, b, r)
	if r.Sign() == 0 {
		return
	}
	switch {
	case mode < 0 && r.Sign() != b.Sign():
		q.Sub(q, bigOne)
		r.Add(r, b)
	case mode > 0 && r.Sign() == b.Sign():
		q.Add(q, bigOne)
		r.Sub(r, b)
	}
}

// gcdExt is mpz_gcdext: g = a*s + b*t, with the s and t the manual defines
// (|s| < |b|/(2g) and |t| < |a|/(2g), and its exceptions).
func gcdExt(a, b *big.Int) (g, s, t *big.Int) {
	g = new(big.Int)
	switch {
	case b.Sign() == 0:
		return g.Abs(a), big.NewInt(int64(a.Sign())), new(big.Int)
	case a.CmpAbs(b) == 0:
		return g.Abs(a), new(big.Int), big.NewInt(int64(b.Sign()))
	}
	x := new(big.Int)
	g.GCD(x, nil, a, b)
	bp := new(big.Int).Quo(new(big.Int).Abs(b), g)
	s = new(big.Int).Mod(x, bp)
	if bp.Cmp(big.NewInt(2)) == 0 {
		// s is odd: the manual's exception, s = sgn(a)
		s.SetInt64(int64(a.Sign()))
	} else if new(big.Int).Lsh(s, 1).Cmp(bp) > 0 {
		s.Sub(s, bp)
	}
	t = new(big.Int).Mul(a, s)
	t.Sub(g, t)
	t.Quo(t, b)
	return g, s, t
}

// scanBit is mpz_scan0 (want 0) and mpz_scan1 (want 1): the index of the
// first bit of x, in two's complement, from start on, that is want; the
// largest unsigned long when there is none.
func scanBit(x *big.Int, start uint64, want uint) uint64 {
	y := x
	if x.Sign() < 0 {
		y, want = new(big.Int).Not(x), want^1
	}
	w := new(big.Int)
	if start < uint64(y.BitLen()) {
		w.Rsh(y, uint(start))
	}
	if want == 1 {
		if w.Sign() == 0 {
			return math.MaxUint64
		}
		return start + uint64(w.TrailingZeroBits())
	}
	return start + uint64(w.Add(w, bigOne).TrailingZeroBits())
}

// gmpSizeInBase is mpz_sizeinbase: exact for a power-of-two base, else GMP's
// bound, floor(bits*log_base(2)) + 1, which is the count or one more.
func gmpSizeInBase(a *big.Int, base int) uint64 {
	nb := uint64(a.BitLen())
	if nb == 0 {
		return 1
	}
	if base&(base-1) == 0 {
		return cld64(nb, uint64(bits.TrailingZeros(uint(base))))
	}
	return uint64(float64(nb)*math.Ln2/math.Log(float64(base))) + 1
}

// swapCase is the digits of base > 36: GMP's 0-9, A-Z, a-z, where math/big's
// are 0-9, a-z, A-Z.
func swapCase(r rune) rune {
	switch {
	case 'a' <= r && r <= 'z':
		return r - 32
	case 'A' <= r && r <= 'Z':
		return r + 32
	}
	return r
}

// gmpDigitValue is the value of digit c in a base: letters are one case
// only (A-Z, then a-z) past base 36, either up to it; 255 for no digit.
func gmpDigitValue(c byte, base int) int {
	switch {
	case '0' <= c && c <= '9':
		return int(c - '0')
	case 'A' <= c && c <= 'Z':
		return int(c-'A') + 10
	case 'a' <= c && c <= 'z' && base <= 36:
		return int(c-'a') + 10
	case 'a' <= c && c <= 'z':
		return int(c-'a') + 36
	}
	return 255
}

func isCSpace(c byte) bool { return c == ' ' || '\t' <= c && c <= '\r' }

// gmpSetStr is mpz_set_str: the integer the string holds, in base 2 to 62,
// or 0 for C's prefixes; false for a string GMP refuses.
func gmpSetStr(s string, base int) (*big.Int, bool) {
	// base 1, which the manual does not allow, reads zeros only
	if base < 0 || base > 62 {
		return nil, false
	}
	i := 0
	next := func() byte {
		if i < len(s) {
			i++
			return s[i-1]
		}
		i++
		return 0
	}
	c := next()
	for isCSpace(c) {
		c = next()
	}
	neg := c == '-'
	if neg {
		c = next()
	}
	// the first character must be a digit, of base 10 or less for C's
	// prefixes
	first := base
	if base == 0 {
		first = 10
	}
	if gmpDigitValue(c, first) >= first {
		return nil, false
	}
	if base == 0 {
		base = 10
		if c == '0' {
			c = next()
			switch {
			case c == 'x' || c == 'X':
				base, c = 16, next()
			case c == 'b' || c == 'B':
				base, c = 2, next()
			default:
				base = 8
			}
		}
	}
	for c == '0' || isCSpace(c) {
		c = next()
	}
	var digits []byte
	for ; c != 0; c = next() {
		if isCSpace(c) {
			continue
		}
		if gmpDigitValue(c, base) >= base {
			return nil, false
		}
		// math/big's digits: lower case first, past base 36 upper after
		digits = append(digits, byte(swapCase(rune(c))))
	}
	z := new(big.Int)
	if len(digits) > 0 {
		z.SetString(string(digits), base)
	}
	if neg {
		z.Neg(z)
	}
	return z, true
}

// writableTail is how many bytes a Ptr{UInt8} may write: the rest of a
// Memory{UInt8} or of a String the VM allocated.
func (vm *VM) writableTail(p *Pointer) (int64, bool) {
	switch t := p.Target.(type) {
	case *Memory:
		if t.Type == memUInt8Type && p.Off >= 0 && p.Off <= int64(len(t.Elems)) {
			return int64(len(t.Elems)) - p.Off, true
		}
	case string:
		if b, ok := vm.writableBytes(t); ok && p.Off >= 0 && p.Off <= int64(len(b)) {
			return int64(len(b)) - p.Off, true
		}
	}
	return 0, false
}

// getStr is mpz_get_str(str, base, op): op's digits and the NUL after them,
// where the buffer has room for it (a String's own NUL lies past its
// length), at str; NULL for a base GMP refuses.
func (vm *VM) getStr(a []any) (any, error) {
	dst, base, st := a[0].(*Pointer), int(a[1].(int32)), a[2].(*Struct)
	null := &Pointer{Type: dst.Type}
	ubase := base
	if base < 0 {
		ubase = -base
	}
	if ubase < 2 || ubase > 62 || base < 0 && ubase > 36 {
		return null, nil
	}
	v, err := vm.getMpz(st.Fields[:3])
	if err != nil {
		return nil, err
	}
	room := int64(-1)
	if dst.Target != nil {
		var ok bool
		if room, ok = vm.writableTail(dst); !ok {
			return nil, fmt.Errorf("%w: mpz_get_str into %s", ErrUnsupported, showValue(dst))
		}
		// the digits are as many as the bound or one fewer, a sign more
		sign := int64(0)
		if v.Sign() < 0 {
			sign = 1
		}
		if int64(gmpSizeInBase(v, ubase))-1+sign > room {
			return nil, vm.errorf("mpz_get_str: the number does not fit the %d bytes of its buffer", room)
		}
	}
	text := v.Text(ubase)
	switch {
	case base < 0:
		text = strings.ToUpper(text)
	case ubase > 36:
		text = strings.Map(swapCase, text)
	}
	if dst.Target == nil {
		return vm.stringPtrOf(text), nil
	}
	if int64(len(text)) > room {
		return nil, vm.errorf("mpz_get_str: the number does not fit the %d bytes of its buffer", room)
	}
	b := []byte(text)
	if int64(len(b)) < room {
		b = append(b, 0)
	}
	return dst, vm.writeBytes(dst, b)
}

// setStr is mpz_set_str(rop, str, base): 0, or -1 for a string that is not
// a number, rop then unchanged.
func (vm *VM) setStr(a []any) (any, error) {
	st, base := a[0].(*Struct), int(a[2].(int32))
	s, ok := vm.cstringArg(a[1])
	if !ok {
		return nil, fmt.Errorf("%w: mpz_set_str of %s", ErrUnsupported, showValue(a[1]))
	}
	v, ok := gmpSetStr(s, base)
	if !ok {
		return int32(-1), nil
	}
	return int32(0), vm.putMpz(st.Fields[:3], v)
}

// export is mpz_export(rop, countp, order, size, endian, nails, op): |op| as
// words of size bytes (1, 2, 4 or 8) of 8*size-nails bits each, into a
// Memory whose elements are such words, countp storing how many.
func (vm *VM) export(a []any) (any, error) {
	rop, countp := a[0].(*Pointer), a[1]
	order, size, endian, nails, st := a[2].(int32), a[3].(uint64), a[4].(int32), a[5].(uint64), a[6].(*Struct)
	v, err := vm.getMpz(st.Fields[:3])
	if err != nil {
		return nil, err
	}
	m, _ := rop.Target.(*Memory)
	var et *core.DataType
	if m != nil {
		et, _ = m.Type.Parameters[1].(*core.DataType)
	}
	if (size != 1 && size != 2 && size != 4 && size != 8) || nails >= 8*size || (order != 1 && order != -1) ||
		endian < -1 || endian > 1 || m == nil || et == nil || uint64(et.Name.Bits) != 8*size || rop.Off < 0 || rop.Off%int64(size) != 0 {
		return nil, fmt.Errorf("%w: mpz_export of %s with %d-byte words, %d nails, order %d", ErrUnsupported, showValue(rop), size, nails, order)
	}
	per := 8*size - nails
	count := cld64(uint64(v.BitLen()), per)
	first := rop.Off / int64(size)
	if count > uint64(int64(len(m.Elems))-first) {
		return nil, vm.errorf("mpz_export: %d words do not fit the memory", count)
	}
	mask := new(big.Int).Sub(new(big.Int).Lsh(bigOne, uint(per)), bigOne)
	abs := new(big.Int).Abs(v)
	for i := uint64(0); i < count; i++ {
		w := new(big.Int).And(new(big.Int).Rsh(abs, uint(i*per)), mask).Uint64()
		if endian > 0 {
			w = bits.ReverseBytes64(w) >> (64 - 8*size)
		}
		at := int64(i)
		if order > 0 {
			at = int64(count - 1 - i)
		}
		x, _ := bitsValue(et, from64(w))
		m.Elems[first+at] = x
	}
	return rop, vm.storeAs("mpz_export", countp, count)
}

// bounds of the functions that make big results, computed before they are
// run, the result's size at least:

// ceilBits is a bit count from a float, the maximum for one past it.
func ceilBits(x float64) uint64 {
	if x >= 1<<63 {
		return math.MaxUint64
	}
	return uint64(math.Ceil(x))
}

// popCount is the number of one bits in limbs.
func popCount(l []uint64) uint64 {
	n := 0
	for _, w := range l {
		n += bits.OnesCount64(w)
	}
	return uint64(n)
}

// log2Fact is log2(n!), as the float the gamma function gives.
func log2Fact(n uint64) float64 {
	lg, _ := math.Lgamma(float64(n) + 1)
	return lg / math.Ln2
}

// log2Binomial is log2(C(n, k)) for 0 <= k <= n.
func log2Binomial(n, k uint64) float64 {
	return log2Fact(n) - log2Fact(k) - log2Fact(n-k)
}

// binomial is mpz_bin_ui(n, k): C(n, k), n negative by C(-m, k) =
// (-1)^k C(m+k-1, k).
func (vm *VM) binomial(n *big.Int, k uint64) (*big.Int, error) {
	m, neg := new(big.Int).Set(n), false
	if n.Sign() < 0 {
		m.Neg(n).Add(m, new(big.Int).SetUint64(k)).Sub(m, bigOne)
		neg = k&1 == 1
	}
	if m.Cmp(new(big.Int).SetUint64(k)) < 0 {
		return new(big.Int), nil // k > n: none
	}
	// C(m, k) = C(m, m-k): the smaller index
	if rest := new(big.Int).Sub(m, new(big.Int).SetUint64(k)); rest.IsUint64() {
		k = min(k, rest.Uint64())
	}
	z := new(big.Int)
	if m.IsInt64() {
		if err := vm.gmpGuard("mpz_bin_ui", ceilBits(log2Binomial(m.Uint64(), k))); err != nil {
			return nil, err
		}
		z.Binomial(m.Int64(), int64(k))
	} else {
		// C(m, k) has k*(bits(m) - bits(k) - 1) bits at least, and the
		// product below is quadratic in k: refused past a small one
		if k > 1<<16 {
			return nil, fmt.Errorf("%w: mpz_bin_ui of an n of %d bits and a k of %d", ErrUnsupported, m.BitLen(), k)
		}
		if err := vm.gmpGuard("mpz_bin_ui", satMul(k, uint64(m.BitLen()-bits.Len64(k)-1))); err != nil {
			return nil, err
		}
		z.SetInt64(1)
		for i := uint64(1); i <= k; i++ {
			z.Mul(z, new(big.Int).Sub(m, new(big.Int).SetUint64(k-i)))
			z.Quo(z, new(big.Int).SetUint64(i))
		}
	}
	if neg {
		z.Neg(z)
	}
	return z, nil
}

// gmpOp is a function of mpz_t arguments with one result, the first
// argument: its other arguments are decoded by the kinds of sig[1:], and
// f gets them, as big.Ints for the z ones.
func gmpOp(_, sig string, f func(vm *VM, args []any) (*big.Int, error)) gmpFn {
	return gmpFn{sig, func(vm *VM, a []any) (any, error) {
		args := make([]any, len(a)-1)
		for i, x := range a[1:] {
			if st, ok := x.(*Struct); ok {
				v, err := vm.getMpz(st.Fields[:3])
				if err != nil {
					return nil, err
				}
				x = v
			}
			args[i] = x
		}
		r, err := f(vm, args)
		if err != nil {
			return nil, err
		}
		return nil, vm.putMpz(a[0].(*Struct).Fields[:3], r)
	}}
}

func (vm *VM) gmpFunctions() map[string]gmpFn {
	fns := map[string]gmpFn{}
	div := func(name string, mode int, quot bool) {
		fns[name] = gmpOp(name, "zzz", func(vm *VM, a []any) (*big.Int, error) {
			x, y := a[0].(*big.Int), a[1].(*big.Int)
			if y.Sign() == 0 {
				return nil, vm.exception(vm.tDivide)
			}
			q, r := new(big.Int), new(big.Int)
			divMode(q, r, x, y, mode)
			if quot {
				return q, nil
			}
			return r, nil
		})
	}
	for name, mode := range map[string]int{"cdiv": 1, "fdiv": -1, "tdiv": 0} {
		div("__gmpz_"+name+"_q", mode, true)
		div("__gmpz_"+name+"_r", mode, false)
	}
	two := func(name string, f func(vm *VM, x, y *big.Int) (*big.Int, error)) {
		fns["__gmpz_"+name] = gmpOp(name, "zzz", func(vm *VM, a []any) (*big.Int, error) {
			return f(vm, a[0].(*big.Int), a[1].(*big.Int))
		})
	}
	two("add", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).Add(x, y), nil })
	two("sub", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).Sub(x, y), nil })
	two("mul", func(vm *VM, x, y *big.Int) (*big.Int, error) {
		if err := vm.gmpGuard("mpz_mul", uint64(x.BitLen()+y.BitLen())); err != nil {
			return nil, err
		}
		return new(big.Int).Mul(x, y), nil
	})
	two("gcd", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).GCD(nil, nil, x, y), nil })
	two("lcm", func(vm *VM, x, y *big.Int) (*big.Int, error) {
		if x.Sign() == 0 || y.Sign() == 0 {
			return new(big.Int), nil
		}
		if err := vm.gmpGuard("mpz_lcm", uint64(x.BitLen()+y.BitLen())); err != nil {
			return nil, err
		}
		g := new(big.Int).GCD(nil, nil, x, y)
		return g.Mul(new(big.Int).Quo(new(big.Int).Abs(x), g), new(big.Int).Abs(y)), nil
	})
	two("and", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).And(x, y), nil })
	two("ior", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).Or(x, y), nil })
	two("xor", func(vm *VM, x, y *big.Int) (*big.Int, error) { return new(big.Int).Xor(x, y), nil })

	// (rop, op, unsigned long)
	ui := func(name string, f func(vm *VM, x *big.Int, n uint64) (*big.Int, error)) {
		fns["__gmpz_"+name] = gmpOp(name, "zzu", func(vm *VM, a []any) (*big.Int, error) {
			return f(vm, a[0].(*big.Int), a[1].(uint64))
		})
	}
	ui("add_ui", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		return new(big.Int).Add(x, new(big.Int).SetUint64(n)), nil
	})
	ui("sub_ui", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		return new(big.Int).Sub(x, new(big.Int).SetUint64(n)), nil
	})
	ui("mul_ui", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		return new(big.Int).Mul(x, new(big.Int).SetUint64(n)), nil
	})
	ui("mul_2exp", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		if x.Sign() == 0 {
			return new(big.Int), nil
		}
		if err := vm.gmpGuard("mpz_mul_2exp", satAdd(uint64(x.BitLen()), n)); err != nil {
			return nil, err
		}
		return new(big.Int).Lsh(x, uint(n)), nil
	})
	ui("fdiv_q_2exp", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		return new(big.Int).Rsh(x, uint(min(n, math.MaxInt32))), nil
	})
	ui("pow_ui", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) {
		// |x|^n has (bits-1)*n + 1 bits at least
		if x.CmpAbs(bigOne) > 0 {
			if err := vm.gmpGuard("mpz_pow_ui", satAdd(satMul(uint64(x.BitLen()-1), n), 1)); err != nil {
				return nil, err
			}
		}
		return new(big.Int).Exp(x, new(big.Int).SetUint64(n), nil), nil
	})
	ui("bin_ui", func(vm *VM, x *big.Int, n uint64) (*big.Int, error) { return vm.binomial(x, n) })

	fns["__gmpz_ui_sub"] = gmpOp("ui_sub", "zuz", func(vm *VM, a []any) (*big.Int, error) {
		return new(big.Int).Sub(new(big.Int).SetUint64(a[0].(uint64)), a[1].(*big.Int)), nil
	})
	fns["__gmpz_mul_si"] = gmpOp("mul_si", "zzi", func(vm *VM, a []any) (*big.Int, error) {
		return new(big.Int).Mul(a[0].(*big.Int), big.NewInt(a[1].(int64))), nil
	})

	one := func(name string, f func(vm *VM, x *big.Int) (*big.Int, error)) {
		fns["__gmpz_"+name] = gmpOp(name, "zz", func(vm *VM, a []any) (*big.Int, error) { return f(vm, a[0].(*big.Int)) })
	}
	one("neg", func(vm *VM, x *big.Int) (*big.Int, error) { return new(big.Int).Neg(x), nil })
	one("com", func(vm *VM, x *big.Int) (*big.Int, error) { return new(big.Int).Not(x), nil })
	one("set", func(vm *VM, x *big.Int) (*big.Int, error) { return x, nil })
	one("sqrt", func(vm *VM, x *big.Int) (*big.Int, error) {
		if x.Sign() < 0 {
			return nil, vm.exception(vm.tDivide)
		}
		return new(big.Int).Sqrt(x), nil
	})
	fns["__gmpz_fac_ui"] = gmpOp("fac_ui", "zu", func(vm *VM, a []any) (*big.Int, error) {
		n := a[0].(uint64)
		if err := vm.gmpGuard("mpz_fac_ui", ceilBits(log2Fact(n))); err != nil {
			return nil, err
		}
		return new(big.Int).MulRange(1, int64(n)), nil
	})
	fns["__gmpz_set_ui"] = gmpOp("set_ui", "zu", func(vm *VM, a []any) (*big.Int, error) {
		return new(big.Int).SetUint64(a[0].(uint64)), nil
	})
	fns["__gmpz_set_si"] = gmpOp("set_si", "zi", func(vm *VM, a []any) (*big.Int, error) {
		return big.NewInt(a[0].(int64)), nil
	})
	fns["__gmpz_set_d"] = gmpOp("set_d", "zd", func(vm *VM, a []any) (*big.Int, error) {
		d := a[0].(float64)
		if math.IsNaN(d) || math.IsInf(d, 0) {
			return nil, vm.exception(vm.tDivide) // julia's GMP raises where C's is undefined
		}
		z, _ := new(big.Float).SetFloat64(d).Int(nil)
		return z, nil
	})

	fns["__gmpz_init"] = gmpFn{"z", func(vm *VM, a []any) (any, error) {
		return nil, vm.putMpzAlloc(a[0].(*Struct).Fields[:3], new(big.Int), 1, false)
	}}
	fns["__gmpz_init2"] = gmpFn{"zu", func(vm *VM, a []any) (any, error) {
		limbs := cld64(a[1].(uint64), 64)
		if err := vm.gmpGuard("mpz_init2", satMul(limbs, 64)); err != nil {
			return nil, err
		}
		return nil, vm.putMpzAlloc(a[0].(*Struct).Fields[:3], new(big.Int), int(limbs), false)
	}}
	fns["__gmpz_realloc2"] = gmpFn{"zu", func(vm *VM, a []any) (any, error) {
		f := a[0].(*Struct).Fields[:3]
		limbs := cld64(a[1].(uint64), 64)
		if err := vm.gmpGuard("mpz_realloc2", satMul(limbs, 64)); err != nil {
			return nil, err
		}
		v, err := vm.getMpz(f)
		if err != nil {
			return nil, err
		}
		if uint64(len(intToLimbs(v))) > limbs {
			v = new(big.Int) // the value does not fit: set to 0
		}
		return nil, vm.putMpzAlloc(f, v, int(max(limbs, 1)), false)
	}}
	fns["__gmpz_limbs_write"] = gmpFn{"zi", func(vm *VM, a []any) (any, error) {
		f, n := a[0].(*Struct).Fields[:3], a[1].(int64)
		if n < 0 {
			return nil, vm.errorf("mpz_limbs_write: %d limbs", n)
		}
		// the old value is discarded; the space is kept when it is enough
		if alloc, ok := f[0].(int32); !ok || int64(alloc) < n {
			if err := vm.putMpzAlloc(f, new(big.Int), int(n), false); err != nil {
				return nil, err
			}
		}
		return f[2], nil
	}}
	fns["__gmpz_limbs_finish"] = gmpFn{"zi", func(vm *VM, a []any) (any, error) {
		f, s := a[0].(*Struct).Fields[:3], a[1].(int64)
		d, ok := f[2].(*Pointer)
		if !ok {
			return nil, vm.errorf("gmp: an uninitialized mpz_t")
		}
		n := uint64(s)
		if s < 0 {
			n = -n
		}
		l, err := vm.limbsAt(d, n)
		if err != nil {
			return nil, err
		}
		// the high zero limbs are stripped, the sign is s's
		return nil, vm.putMpz(f, limbsToInt(l, s < 0))
	}}

	// results that are not one integer
	fns["__gmpz_tdiv_qr"] = gmpFn{"zzzz", func(vm *VM, a []any) (any, error) {
		x, y, err := vm.getTwo(a[2], a[3])
		if err != nil {
			return nil, err
		}
		if y.Sign() == 0 {
			return nil, vm.exception(vm.tDivide)
		}
		q, r := new(big.Int), new(big.Int)
		divMode(q, r, x, y, 0)
		return nil, vm.putAll([]*Struct{a[0].(*Struct), a[1].(*Struct)}, q, r)
	}}
	fns["__gmpz_gcdext"] = gmpFn{"zzzzz", func(vm *VM, a []any) (any, error) {
		x, y, err := vm.getTwo(a[3], a[4])
		if err != nil {
			return nil, err
		}
		g, s, t := gcdExt(x, y)
		return nil, vm.putAll([]*Struct{a[0].(*Struct), a[1].(*Struct), a[2].(*Struct)}, g, s, t)
	}}
	fns["__gmpz_invert"] = gmpFn{"zzz", func(vm *VM, a []any) (any, error) {
		x, m, err := vm.getTwo(a[1], a[2])
		if err != nil {
			return nil, err
		}
		if m.Sign() == 0 {
			return int32(0), nil
		}
		r := new(big.Int).ModInverse(x, new(big.Int).Abs(m))
		if r == nil {
			return int32(0), nil
		}
		return int32(1), vm.putMpz(a[0].(*Struct).Fields[:3], r)
	}}
	fns["__gmpz_powm"] = gmpFn{"zzzz", func(vm *VM, a []any) (any, error) {
		x, e, err := vm.getTwo(a[1], a[2])
		if err != nil {
			return nil, err
		}
		m, err := vm.getMpz(a[3].(*Struct).Fields[:3])
		if err != nil {
			return nil, err
		}
		if m.Sign() == 0 {
			return nil, vm.exception(vm.tDivide)
		}
		am := new(big.Int).Abs(m)
		x = new(big.Int).Mod(x, am) // in [0, |m|), whatever x's sign
		if e.Sign() < 0 {
			if x = new(big.Int).ModInverse(x, am); x == nil {
				return nil, vm.exception(vm.tDivide) // no inverse for a negative exponent
			}
			e = new(big.Int).Neg(e)
		}
		r := new(big.Int).Exp(x, e, am)
		return nil, vm.putMpz(a[0].(*Struct).Fields[:3], r)
	}}

	// queries on one integer; sig is the kinds of the arguments after it
	query := func(name, sig string, f func(vm *VM, x *big.Int, a []any) (any, error)) {
		fns["__gmpz_"+name] = gmpFn{"z" + sig, func(vm *VM, a []any) (any, error) {
			x, err := vm.getMpz(a[0].(*Struct).Fields[:3])
			if err != nil {
				return nil, err
			}
			return f(vm, x, a[1:])
		}}
	}
	fns["__gmpz_cmp"] = gmpFn{"zz", func(vm *VM, a []any) (any, error) {
		x, y, err := vm.getTwo(a[0], a[1])
		if err != nil {
			return nil, err
		}
		return int32(x.Cmp(y)), nil
	}}
	query("cmp_si", "i", func(vm *VM, x *big.Int, a []any) (any, error) {
		return int32(x.Cmp(big.NewInt(a[0].(int64)))), nil
	})
	query("cmp_ui", "u", func(vm *VM, x *big.Int, a []any) (any, error) {
		return int32(x.Cmp(new(big.Int).SetUint64(a[0].(uint64)))), nil
	})
	query("cmp_d", "d", func(vm *VM, x *big.Int, a []any) (any, error) {
		d := a[0].(float64)
		if math.IsNaN(d) {
			return nil, vm.errorf("mpz_cmp_d of NaN")
		}
		return int32(new(big.Float).SetInt(x).Cmp(big.NewFloat(d))), nil
	})
	query("get_d", "", func(vm *VM, x *big.Int, a []any) (any, error) {
		// truncated toward zero, to infinity past the range
		d, _ := new(big.Float).SetPrec(53).SetMode(big.ToZero).SetInt(x).Float64()
		return d, nil
	})
	query("sizeinbase", "c", func(vm *VM, x *big.Int, a []any) (any, error) {
		base := int(a[0].(int32))
		if base < 2 || base > 62 {
			return nil, vm.errorf("mpz_sizeinbase: base %d", base)
		}
		return gmpSizeInBase(x, base), nil
	})
	query("popcount", "", func(vm *VM, x *big.Int, a []any) (any, error) {
		if x.Sign() < 0 {
			return uint64(math.MaxUint64), nil
		}
		return popCount(intToLimbs(x)), nil
	})
	for name, want := range map[string]uint{"scan0": 0, "scan1": 1} {
		query(name, "u", func(vm *VM, x *big.Int, a []any) (any, error) {
			return scanBit(x, a[0].(uint64), want), nil
		})
	}
	query("tstbit", "u", func(vm *VM, x *big.Int, a []any) (any, error) {
		n := a[0].(uint64)
		if n < uint64(x.BitLen()) {
			return int32(x.Bit(int(n))), nil
		}
		// past the top: the sign's bit, ones above a negative number
		if x.Sign() < 0 {
			return int32(1), nil
		}
		return int32(0), nil
	})
	fns["__gmpz_setbit"] = gmpFn{"zu", func(vm *VM, a []any) (any, error) {
		f, n := a[0].(*Struct).Fields[:3], a[1].(uint64)
		x, err := vm.getMpz(f)
		if err != nil {
			return nil, err
		}
		if x.Sign() >= 0 {
			if err := vm.gmpGuard("mpz_setbit", satAdd(n, 1)); err != nil {
				return nil, err
			}
			return nil, vm.putMpz(f, x.SetBit(x, int(n), 1))
		}
		// a negative number has ones above its top: nothing to set there
		if n >= uint64(x.BitLen()) {
			return nil, nil
		}
		return nil, vm.putMpz(f, x.SetBit(x, int(n), 1))
	}}
	fns["__gmpz_get_str"] = gmpFn{"pcz", func(vm *VM, a []any) (any, error) { return vm.getStr(a) }}
	fns["__gmpz_set_str"] = gmpFn{"zpc", func(vm *VM, a []any) (any, error) { return vm.setStr(a) }}
	fns["__gmpz_export"] = gmpFn{"ppcucuz", func(vm *VM, a []any) (any, error) { return vm.export(a) }}
	fns["__gmpn_popcount"] = gmpFn{"pu", func(vm *VM, a []any) (any, error) {
		l, err := vm.limbsAt(a[0].(*Pointer), a[1].(uint64))
		if err != nil {
			return nil, err
		}
		return popCount(l), nil
	}}
	fns["__gmpn_cmp"] = gmpFn{"ppi", func(vm *VM, a []any) (any, error) {
		n := a[2].(int64)
		if n < 0 {
			return nil, vm.errorf("mpn_cmp of %d limbs", n)
		}
		x, err := vm.limbsAt(a[0].(*Pointer), uint64(n))
		if err != nil {
			return nil, err
		}
		y, err := vm.limbsAt(a[1].(*Pointer), uint64(n))
		if err != nil {
			return nil, err
		}
		for i := len(x) - 1; i >= 0; i-- {
			if x[i] != y[i] {
				if x[i] < y[i] {
					return int32(-1), nil
				}
				return int32(1), nil
			}
		}
		return int32(0), nil
	}}

	// the allocator gmp.jl installs: Go's own is used throughout, so the
	// functions are not called, and the out-of-memory function is never
	// needed, the limits above refusing first
	fns["__gmp_set_memory_functions"] = gmpFn{"ppp", func(vm *VM, a []any) (any, error) { return nil, nil }}
	fns["__gmp_set_alloc_overflow_function"] = gmpFn{"p", func(vm *VM, a []any) (any, error) { return nil, nil }}
	vm.gmpRationals(fns)
	return fns
}

func (vm *VM) getTwo(a, b any) (*big.Int, *big.Int, error) {
	x, err := vm.getMpz(a.(*Struct).Fields[:3])
	if err != nil {
		return nil, nil, err
	}
	y, err := vm.getMpz(b.(*Struct).Fields[:3])
	return x, y, err
}

// putAll stores each value in the corresponding BigInt.
func (vm *VM) putAll(dst []*Struct, vals ...*big.Int) error {
	for i, v := range vals {
		if err := vm.putMpz(dst[i].Fields[:3], v); err != nil {
			return err
		}
	}
	return nil
}

// gmpRationals is the mpq_t functions of MPQ (gmp.jl): an _MPQ holds a
// numerator and a denominator as two BigInts' fields.
func (vm *VM) gmpRationals(fns map[string]gmpFn) {
	get := func(vm *VM, a any) (*big.Int, *big.Int, error) {
		f := a.(*Struct).Fields
		n, err := vm.getMpz(f[:3])
		if err != nil {
			return nil, nil, err
		}
		d, err := vm.getMpz(f[3:6])
		return n, d, err
	}
	rat := func(vm *VM, a any) (*big.Rat, error) {
		n, d, err := get(vm, a)
		if err != nil {
			return nil, err
		}
		if d.Sign() == 0 {
			return nil, vm.exception(vm.tDivide)
		}
		return new(big.Rat).SetFrac(n, d), nil
	}
	put := func(vm *VM, a any, n, d *big.Int) error {
		f := a.(*Struct).Fields
		if err := vm.putMpz(f[:3], n); err != nil {
			return err
		}
		return vm.putMpz(f[3:6], d)
	}
	fns["__gmpq_canonicalize"] = gmpFn{"q", func(vm *VM, a []any) (any, error) {
		r, err := rat(vm, a[0])
		if err != nil {
			return nil, err
		}
		return nil, put(vm, a[0], new(big.Int).Set(r.Num()), new(big.Int).Set(r.Denom()))
	}}
	fns["__gmpq_set"] = gmpFn{"qq", func(vm *VM, a []any) (any, error) {
		n, d, err := get(vm, a[1])
		if err != nil {
			return nil, err
		}
		return nil, put(vm, a[0], n, d)
	}}
	fns["__gmpq_set_z"] = gmpFn{"qz", func(vm *VM, a []any) (any, error) {
		n, err := vm.getMpz(a[1].(*Struct).Fields[:3])
		if err != nil {
			return nil, err
		}
		return nil, put(vm, a[0], n, big.NewInt(1))
	}}
	fns["__gmpq_set_ui"] = gmpFn{"quu", func(vm *VM, a []any) (any, error) {
		return nil, put(vm, a[0], new(big.Int).SetUint64(a[1].(uint64)), new(big.Int).SetUint64(a[2].(uint64)))
	}}
	fns["__gmpq_set_si"] = gmpFn{"qiu", func(vm *VM, a []any) (any, error) {
		return nil, put(vm, a[0], big.NewInt(a[1].(int64)), new(big.Int).SetUint64(a[2].(uint64)))
	}}
	for name, f := range map[string]func(z, x, y *big.Rat) *big.Rat{
		"add": (*big.Rat).Add, "sub": (*big.Rat).Sub, "mul": (*big.Rat).Mul,
		"div": func(z, x, y *big.Rat) *big.Rat { return z.Quo(x, y) },
	} {
		fns["__gmpq_"+name] = gmpFn{"qqq", func(vm *VM, a []any) (any, error) {
			x, err := rat(vm, a[1])
			if err != nil {
				return nil, err
			}
			y, err := rat(vm, a[2])
			if err != nil {
				return nil, err
			}
			if name == "div" && y.Sign() == 0 {
				return nil, vm.exception(vm.tDivide)
			}
			z := f(new(big.Rat), x, y)
			return nil, put(vm, a[0], new(big.Int).Set(z.Num()), new(big.Int).Set(z.Denom()))
		}}
	}
	fns["__gmpq_cmp"] = gmpFn{"qq", func(vm *VM, a []any) (any, error) {
		x, err := rat(vm, a[0])
		if err != nil {
			return nil, err
		}
		y, err := rat(vm, a[1])
		if err != nil {
			return nil, err
		}
		return int32(x.Cmp(y)), nil
	}}
}
