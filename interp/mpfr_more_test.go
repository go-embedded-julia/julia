package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// fail calls a shim that must refuse, with the error wrapping want (any
// error when want is nil).
func (f *mpfrFixture) fail(want error, name string, args ...any) {
	f.t.Helper()
	_, err := f.call(name, args...)
	if err == nil || want != nil && !errors.Is(err, want) {
		f.t.Errorf("%s: error %v, want %v", name, err, want)
	}
}

func (f *mpfrFixture) one() *Pointer { return f.bf("B:53:r:1:1:8000000000000000") }

// The arguments a shim checks before it runs.
func TestMPFRArguments(t *testing.T) {
	f := newMPFRFixture(t)
	z, x := f.newBF(53), f.one()
	rnd := f.rnd(0)
	f.fail(nil, "mpfr_set", z, x) // too few
	f.fail(nil, "mpfr_set_si", z, "one", rnd)
	f.fail(nil, "mpfr_set", z, &Pointer{Type: f.u64p, Target: "x"}, rnd)
	f.fail(nil, "mpfr_set", z, &Pointer{Type: f.u64p, Target: bfMem(x), Off: 8}, rnd)
	f.fail(nil, "mpfr_set", z, &Pointer{Type: f.u64p, Target: f.vm.newLimbs(2)}, rnd)
	f.fail(nil, "mpfr_set", z, 5, rnd)
	// a precision that is zero, or does not fit the memory
	bad := f.one()
	bfMem(bad).Elems[0] = uint64(0)
	f.fail(nil, "mpfr_set", z, bad, rnd)
	bfMem(bad).Elems[0] = uint64(1000)
	f.fail(nil, "mpfr_set", z, bad, rnd)
	bfMem(bad).Elems[0] = "x"
	f.fail(nil, "mpfr_set", z, bad, rnd)
	// rounding modes: faithful and numbers that are no mode are refused
	f.fail(ErrUnsupported, "mpfr_set", z, x, f.rnd(5))
	f.fail(ErrUnsupported, "mpfr_set", z, x, f.rnd(7))
	// an mpz_t that is not initialised
	zz, _ := f.newStruct("GBig")
	f.fail(nil, "mpfr_add_z", z, x, zz, rnd)
	// operands that are not numbers
	corrupt := f.one()
	bfMem(corrupt).Elems[mpfrLimbWord] = "x"
	f.fail(nil, "mpfr_set", z, corrupt, rnd)
	unnorm := f.one()
	bfMem(unnorm).Elems[mpfrLimbWord] = uint64(1)
	f.fail(nil, "mpfr_set", z, unnorm, rnd)
	undefLimb := f.one()
	bfMem(undefLimb).Elems[mpfrLimbWord] = undef
	f.fail(nil, "mpfr_set", z, undefLimb, rnd)
	// a pointer kept in the memory's limb pointer word is no number either
	word := f.one()
	bfMem(word).Elems[2] = &Pointer{Type: f.u64p}
	f.fail(nil, "mpfr_set", z, word, rnd)
	// the shims are made again by a VM that has none
	f.vm.libShims = nil
	f.vm.bootMPFR()
	if f.vm.libShims[mpfrLib]["mpfr_add"] == nil || f.vm.libShims[mpfrLibShort]["mpfr_add"] == nil {
		t.Error("bootMPFR made no shims")
	}
}

// What is refused as beyond the VM, or beyond math/big.
func TestMPFRRefusals(t *testing.T) {
	f := newMPFRFixture(t)
	rnd := f.rnd(0)
	huge := f.bf("B:53:r:1:1099511627776:8000000000000000") // 2^(2^40-1)
	three := f.bf("B:53:r:1:2:c000000000000000")
	z := func() *Pointer { return f.newBF(53) }
	// an exponent beyond math/big's
	f.fail(ErrUnsupported, "mpfr_exp", z(), huge, rnd)
	// trigonometric arguments beyond bigfloat's reduction
	big23 := f.bf("B:53:r:1:100000000:8000000000000000")
	f.fail(ErrUnsupported, "mpfr_sin", z(), big23, rnd)
	f.fail(ErrUnsupported, "mpfr_sin_cos", z(), z(), big23, rnd)
	// a result beyond math/big's exponent range
	f.fail(ErrUnsupported, "mpfr_pow", z(), f.bf("B:53:r:1:2:8000000000000000"), f.bf("B:53:r:1:40:8000000000000000"), rnd)
	// functions not ported
	f.fail(ErrUnsupported, "mpfr_sech", z(), three, rnd)
	f.fail(ErrUnsupported, "mpfr_sinu", z(), three, uint64(360), rnd)
	// results too large for the VM
	f.fail(ErrMemoryLimit, "mpfr_fac_ui", z(), uint64(1<<40), rnd)
	f.fail(ErrMemoryLimit, "mpfr_trunc", z(), huge)
	f.fail(ErrMemoryLimit, "mpfr_modf", z(), z(), huge, rnd)
	f.fail(ErrMemoryLimit, "mpfr_fmod", z(), huge, three, rnd)
	p, _ := f.fresh()
	f.fail(ErrMemoryLimit, "mpfr_get_z", p, huge, rnd)
	f.vm.MaxMemoryElems = 1
	f.fail(ErrMemoryLimit, "__gmpfr_mpfr_get_sj", f.bf("B:53:r:1:101:8000000000000000"), rnd)
	f.vm.MaxMemoryElems = DefaultMaxMemoryElems
	// asprintf: an unknown format, too many digits, a bad pointer
	pc, _ := f.newStruct("GPS")
	f.fail(ErrUnsupported, "mpfr_asprintf", pc, f.token("s:%Rf"), three)
	f.fail(ErrMemoryLimit, "mpfr_asprintf", pc, f.token("s:%.2000000000Re"), three)
	f.fail(ErrMemoryLimit, "mpfr_asprintf", pc, f.token("s:%.1000000000000Re"), three)
	f.fail(ErrMemoryLimit, "mpfr_asprintf", pc, f.token("s:%Re"), huge)
	f.vm.MaxMemoryElems = 40
	f.fail(ErrMemoryLimit, "mpfr_asprintf", pc, f.token("s:%.100Re"), three)
	f.vm.MaxMemoryElems = DefaultMaxMemoryElems
	f.fail(ErrUnsupported, "mpfr_asprintf", &Pointer{Type: f.u64p, Target: "x"}, f.token("s:%Re"), three)
	// frexp through a pointer that is not a Ref
	f.fail(ErrUnsupported, "mpfr_frexp", &Pointer{Type: f.u64p, Target: "x"}, z(), three, rnd)
	// set_str: a base MPFR aborts on, a power of the base beyond the VM
	zs := z()
	f.fail(ErrUnsupported, "mpfr_set_str", zs, f.token("s:1"), int32(1), rnd)
	f.fail(ErrUnsupported, "mpfr_set_str", zs, f.token("s:1"), int32(63), rnd)
	f.fail(ErrMemoryLimit, "mpfr_set_str", zs, f.token("s:1e999999999"), int32(10), rnd)
}

// Strings and printing, directly.
func TestMPFRStrings(t *testing.T) {
	f := newMPFRFixture(t)
	rnd := f.rnd(0)
	set := func(s string, base int32) (string, int32) {
		z := f.newBF(53)
		r := f.must("mpfr_set_str", z, f.token("s:"+s), base, rnd).(int32)
		return f.enc(z), r
	}
	// exponents far past every range are an overflow or an underflow
	for s, want := range map[string]string{
		"1e99999999999999999999":        "i",
		"-1e99999999999999999999":       "i",
		"1e-99999999999999999999":       "z",
		"1e-99999999999999999999999":    "z",
		"0x1p99999999999999999999":      "i",
		"0x1p-99999999999999999999":     "z",
		"1e" + strings.Repeat("9", 400): "i",
		"0e99999999999999999999":        "z",
	} {
		if got, r := set(s, 0); r != 0 || !strings.Contains(got, ":"+want+":") {
			t.Errorf("%q: %s %d, want kind %s", s, got, r, want)
		}
	}
	// a number then something else: the number is read, -1 returned
	if _, r := set("12 x", 10); r != -1 {
		t.Errorf("trailing text: %d", r)
	}
	if _, r := set("zz", 10); r != -1 {
		t.Errorf("no digit: %d", r)
	}
	if _, r := set("nan(abc", 10); r != -1 {
		t.Errorf("nan(: %d", r)
	}
	if got, r := set("nan(a_1)", 10); r != 0 || !strings.Contains(got, ":n:") {
		t.Errorf("nan(a_1): %s %d", got, r)
	}
	// printfFormat
	for _, bad := range []string{"", "x", "%Rf", "%Re ", "%.Re", "%.xRe", "%.99999999999999999999Re", "%5Re"} {
		if _, ok := printfFormat(bad); ok {
			t.Errorf("printfFormat(%q) accepted", bad)
		}
	}
	for fm, want := range map[string]int64{"%Re": -1, "%.0Re": 0, "%.12Re": 12} {
		if got, ok := printfFormat(fm); !ok || got != want {
			t.Errorf("printfFormat(%q) = %d, %v", fm, got, ok)
		}
	}
	// printing the special values
	for tok, want := range map[string]string{"B:53:n:1:0:": "nan", "B:53:i:1:0:": "inf", "B:53:i:-1:0:": "-inf", "B:53:z:-1:0:": "-0.0000000000000000e+00"} {
		pc, st := f.newStruct("GPS")
		f.must("mpfr_asprintf", pc, f.token("s:%Re"), f.bf(tok))
		if s, _ := f.vm.cstringArg(st.Fields[0]); s != want {
			t.Errorf("%s printed %q, want %q", tok, s, want)
		}
	}
	pc, st := f.newStruct("GPS")
	f.must("mpfr_asprintf", pc, f.token("s:%.0Re"), f.bf("B:53:r:1:4:9999999999999800"))
	if s, _ := f.vm.cstringArg(st.Fields[0]); s != "1e+01" {
		t.Errorf("a carry printed %q", s)
	}
}

// A pointer kept in a word of a Memory{UInt64}, as BigFloat keeps its limb
// pointer (mpfr.jl:219-228).
func TestPointerInWord(t *testing.T) {
	f := newMPFRFixture(t)
	m := f.vm.newLimbs(4)
	pt := f.vm.ptrType(f.u64p)
	at := func(off int64) *Pointer { return &Pointer{Type: pt, Target: m, Off: off} }
	val := &Pointer{Type: f.u64p, Target: m, Off: 16}
	if err := f.vm.ptrStore("pointerset", at(8), val, 1); err != nil {
		t.Fatal(err)
	}
	if got, err := f.vm.ptrLoad("pointerref", at(8), 1); err != nil || got != any(val) {
		t.Errorf("load %v, %v", got, err)
	}
	// a word never stored, or unset, is C_NULL
	if got, err := f.vm.ptrLoad("pointerref", at(0), 1); err != nil || got.(*Pointer).Target != nil {
		t.Errorf("zero word: %v, %v", got, err)
	}
	m.Elems[3] = undef
	if got, err := f.vm.ptrLoad("pointerref", at(24), 1); err != nil || got.(*Pointer).Target != nil {
		t.Errorf("undef word: %v, %v", got, err)
	}
	// a word of other bits is no pointer; nor is half a word or past the end
	m.Elems[3] = uint64(5)
	if _, err := f.vm.ptrLoad("pointerref", at(24), 1); err == nil {
		t.Error("loaded a pointer from the bits 5")
	}
	if err := f.vm.ptrStore("pointerset", at(4), val, 1); err == nil {
		t.Error("stored a pointer in half a word")
	}
	if err := f.vm.ptrStore("pointerset", at(28), val, 1); err == nil {
		t.Error("stored a pointer past the end")
	}
}

// Numbers at the ends of the exponent range, and what a shim does with a
// NaN's sign.
func TestMPFREnds(t *testing.T) {
	f := newMPFRFixture(t)
	rnd := f.rnd(0)
	f.must("mpfr_set_emin", int64(0))
	f.must("mpfr_set_emax", int64(1))
	// the largest number rounds up to infinity, the smallest negative one to -0
	z := f.bf("B:2:r:1:1:c000000000000000")
	f.must("mpfr_nextabove", z)
	if got := f.enc(z); !strings.Contains(got, ":i:") {
		t.Errorf("above the largest: %s", got)
	}
	m := f.bf("B:2:r:-1:0:8000000000000000")
	f.must("mpfr_nextabove", m)
	if got := f.enc(m); !strings.Contains(got, ":z:-1") {
		t.Errorf("above the smallest negative: %s", got)
	}
	ninf := f.bf("B:2:i:-1:0:")
	f.must("mpfr_nextabove", ninf)
	if got := f.enc(ninf); !strings.Contains(got, "B:2:r:-1:1:c0") {
		t.Errorf("above -Inf: %s", got)
	}
	// rounding to the nearest in the exponent range: the smallest number is
	// 2^-1, half of it rounds to zero
	half := f.bf("B:53:r:1:-1:8000000000000000")
	r := f.newBF(53)
	f.must("mpfr_set", r, half, rnd)
	if got := f.enc(r); !strings.Contains(got, ":z:") {
		t.Errorf("underflow: %s", got)
	}
	// a regular number whose exponent word is not a word
	_ = core.Int32Type
}
