package interp

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

func (f *gmpFixture) wantErr(name string, want string, args ...any) {
	f.t.Helper()
	_, err := f.call(name, args...)
	if err == nil || !strings.Contains(err.Error(), want) {
		f.t.Errorf("%s: got %v, want an error containing %q", name, err, want)
	}
}

func (f *gmpFixture) wantLimit(name string, args ...any) {
	f.t.Helper()
	_, err := f.call(name, args...)
	if !errors.Is(err, ErrMemoryLimit) {
		f.t.Errorf("%s: got %v, want ErrMemoryLimit", name, err)
	}
}

func (f *gmpFixture) val(p *Pointer) string { return f.value(p).String() }

// A call with a wrong number of arguments, or one of the wrong kind at any
// position, is refused; so is a struct that is no BigInt.
func TestGMPArguments(t *testing.T) {
	f := newGMPFixture(t)
	valid := map[byte]func() any{
		'z': func() any { p, _ := f.big("5"); return p },
		'q': func() any { p, _ := f.qrat(f.bigPtr(1), f.bigPtr(2)); return p },
		'u': func() any { return uint64(1) },
		'i': func() any { return int64(1) },
		'c': func() any { return int32(10) },
		'd': func() any { return 1.5 },
		'p': func() any { return &Pointer{Type: f.u8p} },
	}
	for name, fn := range f.vm.gmpFunctions() {
		args := make([]any, len(fn.sig))
		for i := range args {
			args[i] = valid[fn.sig[i]]()
		}
		f.wantErr(name, "arguments, expected", append(args, int64(1))...)
		for i := range args {
			bad := append([]any(nil), args...)
			bad[i] = "bad"
			f.wantErr(name, "not of kind", bad...)
		}
	}
	// mpzStruct
	ptr := func(typ string) *Pointer { p, _ := f.newStruct(typ); return p }
	p2, st2 := f.newStruct("GBig")
	offp := &Pointer{Type: p2.Type, Target: st2, Off: 8}
	for _, c := range []struct {
		name string
		v    any
		n    int
		want bool
	}{
		{"a BigInt", ptr("GBig"), 3, true},
		{"an _MPQ", ptr("GRat"), 7, true},
		{"not a pointer", int64(1), 3, false},
		{"a pointer to a string", &Pointer{Type: f.u8p, Target: "x"}, 3, false},
		{"an offset", offp, 3, false},
		{"another count", ptr("GRat"), 3, false},
		{"a short struct", ptr("GSmall"), 3, false},
		{"a wide size", ptr("GBad"), 3, false},
		{"a BigInt for an _MPQ", ptr("GBig"), 7, false},
	} {
		if _, ok := mpzStruct(c.v, c.n); ok != c.want {
			t.Errorf("%s: %v", c.name, ok)
		}
	}
	// an immutable struct is no BigInt
	im := must(f.s.Eval("struct GImm; a::Int32; b::Int32; c::Ptr{UInt64}; end; GImm"))
	ist := &Struct{Type: im.(*core.DataType), Fields: []any{int32(0), int32(0), &Pointer{}}}
	if _, ok := mpzStruct(&Pointer{Type: f.u64p, Target: ist}, 3); ok {
		t.Error("an immutable struct")
	}
}

// What a ccall that names a library by a spliced (name, library) tuple, as
// gmp.jl's ccall($(gmpz(op)), …) does, reaches.
func TestGMPSplicedCcall(t *testing.T) {
	f := newGMPFixture(t)
	f.vm.libShims["libtest"] = map[expr.Symbol]shim{"twice": func(vm *VM, a []any) (any, error) { return a[0].(int64) * 2, nil }}
	for _, c := range []struct{ src, want string }{
		{`Core.eval(Main, Expr(:call, :ccall, (:twice, "libtest"), :Int, Expr(:tuple, :Int), 21))`, "42"},
		{`Core.eval(Main, Expr(:call, :ccall, (:twice, "libother"), :Int, Expr(:tuple, :Int), 21))`, `!not ported yet: ccall((:twice, "libother"))`},
		{`Core.eval(Main, Expr(:call, :ccall, (:twice, 1), :Int, Expr(:tuple, :Int), 21))`, "!not ported yet: ccall((:twice, 1))"},
		{`Core.eval(Main, Expr(:call, :ccall, ("twice", "libtest"), :Int, Expr(:tuple, :Int), 21))`, "!not ported yet: a ccall to"},
		{`Core.eval(Main, Expr(:call, :ccall, (:twice, "libtest", 3), :Int, Expr(:tuple, :Int), 21))`, "!not ported yet: a ccall to"},
	} {
		v, err := f.s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}

// cglobal of (name, library): GMP's version and limb size.
func TestGMPCglobal(t *testing.T) {
	f := newGMPFixture(t)
	vm := f.vm
	tup := func(xs ...any) *Tuple { return vm.newTuple(xs) }
	p, err := biCglobal(vm, []any{tup(expr.Symbol("__gmp_version"), gmpLib), vm.ptrType(core.Int8Type)})
	if err != nil {
		t.Fatal(err)
	}
	v, err := vm.ptrLoad("pointerref", p.(*Pointer), 1)
	if err != nil {
		t.Fatal(err)
	}
	s, err := vm.cstrToString([]any{v})
	check(t, "the version", s, err, `"6.3.0"`)
	p, err = biCglobal(vm, []any{tup(expr.Symbol("__gmp_bits_per_limb"), gmpLib), core.Int32Type})
	if err != nil {
		t.Fatal(err)
	}
	v, err = vm.ptrLoad("pointerref", p.(*Pointer), 1)
	check(t, "the limb size", v, err, "64")
	for _, c := range []struct {
		name string
		a    any
		want string
	}{
		{"an unknown variable", tup(expr.Symbol("nope"), gmpLib), "!not ported yet: cglobal((:nope"},
		{"an unknown library", tup(expr.Symbol("__gmp_version"), "libother"), "!not ported yet: cglobal((:__gmp_version"},
		{"a library that is no string", tup(expr.Symbol("__gmp_version"), int64(1)), "!TypeError"},
		{"a name that is no symbol", tup("x", gmpLib), "!TypeError"},
		{"a tuple of one", tup(expr.Symbol("x")), "!TypeError"},
		{"the allocator functions", expr.Symbol("jl_gc_counted_malloc"), ""},
	} {
		_, err := biCglobal(vm, []any{c.a, core.NothingType})
		check(t, c.name, nil, err, c.want)
	}
	// Core.getptls and the pointer finalizer
	v, err = vm.shims["jl_get_ptls_states"](vm, nil)
	if err != nil || v.(*Pointer).Target == nil {
		t.Errorf("ptls: %v %v", v, err)
	}
	v, err = vm.shims["jl_gc_add_ptr_finalizer"](vm, nil)
	check(t, "finalizer", v, err, "")
	// cglobal of one argument, a C function's address (gmp.jl:71): a
	// Ptr{Cvoid} code passes on and cannot load through
	cg := vm.intrinsics[intrinsicIDs["cglobal"]]
	v, err = cg.Fn(vm, []any{tup(expr.Symbol("__gmpz_clear"), gmpLib)})
	if err != nil {
		t.Fatal(err)
	}
	fp := v.(*Pointer)
	if fp.Type != vm.ptrType(core.NothingType) || fp.Target != (cFunc{gmpLib, "__gmpz_clear"}) {
		t.Fatalf("cglobal of __gmpz_clear: %s", showValue(fp))
	}
	_, err = vm.ptrLoad("pointerref", &Pointer{Type: vm.ptrType(core.Int8Type), Target: fp.Target}, 1)
	check(t, "a load through a function's address", nil, err, "!")
	_, err = cg.Fn(vm, nil)
	check(t, "cglobal of nothing", nil, err, "!intrinsic_call: too few arguments")
	z, _ := f.big("5")
	v, err = f.call("__gmpz_clear", z)
	check(t, "mpz_clear", v, err, "")
}

// The allocator calls and values the shims leave: gmp.jl's __init__.
func TestGMPAllocator(t *testing.T) {
	f := newGMPFixture(t)
	null := &Pointer{Type: f.u8p}
	f.must("__gmp_set_memory_functions", null, null, null)
	f.must("__gmp_set_alloc_overflow_function", null)
}

// A BigInt that is not initialized, or whose limbs are not where the fields
// say, is refused where it is read.
func TestGMPBrokenBigInt(t *testing.T) {
	f := newGMPFixture(t)
	out, _ := f.fresh()
	unset := func() *Pointer { p, _ := f.newStruct("GBig"); return p } // new(): no field set
	// outputs may be unset, inputs may not
	for _, c := range []struct {
		name  string
		nouts int
	}{{"__gmpz_add", 1}, {"__gmpz_cmp", 0}, {"__gmpz_tdiv_qr", 2}, {"__gmpz_powm", 1}, {"__gmpz_invert", 1}, {"__gmpz_gcdext", 3}} {
		sig := f.vm.gmpFunctions()[c.name].sig
		mk := func(bad int, u any) []any {
			args := make([]any, len(sig))
			for i := range args {
				p, _ := f.big("3")
				if i < c.nouts {
					p, _ = f.fresh()
				}
				args[i] = p
			}
			if bad >= 0 {
				args[bad] = u
			}
			return args
		}
		for bad := 0; bad < len(sig); bad++ {
			if bad < c.nouts {
				f.must(c.name, mk(bad, unset())...)
			} else {
				f.wantErr(c.name, "uninitialized", mk(bad, unset())...)
			}
		}
	}
	_ = out
	for _, name := range []string{"cmp_si", "cmp_ui", "cmp_d", "get_d", "sizeinbase", "popcount", "scan0", "tstbit", "setbit", "realloc2"} {
		fn := f.vm.gmpFunctions()["__gmpz_"+name]
		args := []any{unset()}
		for _, k := range []byte(fn.sig[1:]) {
			args = append(args, map[byte]any{'i': int64(1), 'u': uint64(1), 'd': 1.0, 'c': int32(10)}[k])
		}
		f.wantErr("__gmpz_"+name, "uninitialized", args...)
	}
	// limbs not where size says
	for _, c := range []struct {
		name string
		fix  func(st *Struct)
	}{
		{"a short memory", func(st *Struct) { st.Fields[1] = int32(5) }},
		{"a misaligned pointer", func(st *Struct) { st.Fields[2].(*Pointer).Off = 3 }},
		{"a negative offset", func(st *Struct) { st.Fields[2].(*Pointer).Off = -8 }},
		{"an offset past the end", func(st *Struct) { st.Fields[2].(*Pointer).Off = 800 }},
		{"another memory type", func(st *Struct) {
			st.Fields[2] = &Pointer{Type: f.u8p, Target: &Memory{Type: memUInt8Type, Elems: []any{uint8(1)}}}
		}},
		{"no memory", func(st *Struct) { st.Fields[2] = &Pointer{Type: f.u64p} }},
		{"an undefined limb", func(st *Struct) { st.Fields[2].(*Pointer).Target.(*Memory).Elems[0] = undef }},
	} {
		p, st := f.big("18446744073709551617")
		c.fix(st)
		_, err := f.call("__gmpz_neg", out, p)
		if err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

// Results too large for VM.MaxMemoryElems are refused before they are
// computed, at the defaults and at a small bound.
func TestGMPBombs(t *testing.T) {
	f := newGMPFixture(t)
	huge := uint64(1) << 62
	ten, _ := f.big("10")
	x, _ := f.fresh()
	big1, _ := f.big("1000000000000")
	for _, c := range []struct {
		name string
		args []any
	}{
		{"__gmpz_init2", []any{x, huge}},
		{"__gmpz_realloc2", []any{x, huge}},
		{"__gmpz_mul_2exp", []any{x, ten, huge}},
		{"__gmpz_mul_2exp", []any{x, ten, uint64(math.MaxUint64)}},
		{"__gmpz_pow_ui", []any{x, ten, huge}},
		{"__gmpz_pow_ui", []any{x, ten, uint64(math.MaxInt64)}},
		{"__gmpz_fac_ui", []any{x, huge}},
		{"__gmpz_fac_ui", []any{x, uint64(math.MaxUint64)}},
		{"__gmpz_bin_ui", []any{x, big1, uint64(500000000000)}},
		{"__gmpz_setbit", []any{x, huge}},
		{"__gmpz_limbs_write", []any{x, int64(1) << 60}},
	} {
		f.wantLimit(c.name, c.args...)
	}
	// the same at a small bound: 4 limbs, 256 bits
	f.vm.MaxMemoryElems = 4
	a, _ := f.big("115792089237316195423570985008687907853269984665640564039457584007913129639935") // 2^256-1
	b, _ := f.big("340282366920938463463374607431768211456")                                        // 2^128
	huge2, _ := f.big("1" + strings.Repeat("0", 70))
	for _, c := range []struct {
		name string
		args []any
	}{
		{"__gmpz_mul", []any{x, a, b}},
		{"__gmpz_lcm", []any{x, a, b}},
		{"__gmpz_add", []any{x, a, a}}, // 257 bits: caught when stored
		{"__gmpz_fac_ui", []any{x, uint64(60)}},
		{"__gmpz_pow_ui", []any{x, b, uint64(3)}},
		{"__gmpz_init2", []any{x, uint64(257)}},
		{"__gmpz_realloc2", []any{x, uint64(257)}},
		{"__gmpz_limbs_write", []any{x, int64(5)}},
		{"__gmpz_setbit", []any{x, uint64(300)}},
		{"__gmpz_mul_2exp", []any{x, a, uint64(1)}},
		{"__gmpz_bin_ui", []any{x, f.bigPtr(300), uint64(150)}},
		{"__gmpz_bin_ui", []any{x, huge2, uint64(10)}},
	} {
		f.wantLimit(c.name, c.args...)
	}
	// what fits at the bound is done
	f.must("__gmpz_mul", x, b, f.bigPtr(1<<40))
	if got := f.val(x); got != new(big.Int).Lsh(big.NewInt(1), 168).String() {
		t.Errorf("2^168 = %s", got)
	}
}

// A binomial past 64 bits is a product; a large k is refused.
func TestGMPBinomialBig(t *testing.T) {
	f := newGMPFixture(t)
	x, _ := f.fresh()
	n, _ := f.big("18446744073709551616") // 2^64
	f.must("__gmpz_bin_ui", x, n, uint64(3))
	want := new(big.Int)
	want.SetString("18446744073709551616", 10)
	// n(n-1)(n-2)/6
	nn := new(big.Int).Set(want)
	r := new(big.Int).Mul(nn, new(big.Int).Sub(nn, big.NewInt(1)))
	r.Mul(r, new(big.Int).Sub(nn, big.NewInt(2))).Quo(r, big.NewInt(6))
	if f.val(x) != r.String() {
		t.Errorf("C(2^64, 3) = %s", f.val(x))
	}
	f.wantErr("__gmpz_bin_ui", "not ported yet", x, n, uint64(1<<17))
}

// The pointers the text functions take.
func TestGMPText(t *testing.T) {
	f := newGMPFixture(t)
	ten, _ := f.big("-12345678901234567890")
	mem := func(n int) (*Pointer, *Memory) {
		m := &Memory{Type: memUInt8Type, Elems: make([]any, n)}
		for i := range m.Elems {
			m.Elems[i] = uint8(0xff)
		}
		return &Pointer{Type: f.u8p, Target: m}, m
	}
	text := func(m *Memory) string {
		var b []byte
		for _, e := range m.Elems {
			if e.(uint8) == 0 || e.(uint8) == 0xff {
				break
			}
			b = append(b, e.(uint8))
		}
		return string(b)
	}
	// room for the digits and the NUL; for the digits only; for fewer
	p, m := mem(22)
	f.must("__gmpz_get_str", p, int32(10), ten)
	if text(m) != "-12345678901234567890" || m.Elems[21] != uint8(0) {
		t.Errorf("with a NUL: %q", text(m))
	}
	p, m = mem(21)
	f.must("__gmpz_get_str", p, int32(10), ten)
	if text(m) != "-12345678901234567890" {
		t.Errorf("without: %q", text(m))
	}
	p, _ = mem(20)
	f.wantErr("__gmpz_get_str", "does not fit", p, int32(10), ten)
	// within the bound's slack the digits are counted
	p, _ = mem(30)
	z, _ := f.big("1" + strings.Repeat("0", 30))
	p, _ = mem(30)
	f.wantErr("__gmpz_get_str", "does not fit", p, int32(10), z)
	// at an offset into the memory
	p, m = mem(30)
	p.Off = 5
	f.must("__gmpz_get_str", p, int32(-16), ten)
	if text(&Memory{Elems: m.Elems[5:]}) != "-AB54A98CEB1F0AD2" {
		t.Errorf("at an offset: %q", text(&Memory{Elems: m.Elems[5:]}))
	}
	// a base GMP refuses answers NULL; a NULL buffer is allocated
	for _, base := range []int32{0, 1, 63, -1, -37} {
		v, err := f.call("__gmpz_get_str", p, base, ten)
		if err != nil || v.(*Pointer).Target != nil {
			t.Errorf("base %d: %v %v", base, v, err)
		}
	}
	v := f.must("__gmpz_get_str", &Pointer{Type: f.u8p}, int32(7), ten)
	if s, _ := f.vm.cstrToString([]any{v}); s != "-1000636513413054164161" && s == "" {
		t.Errorf("a NULL buffer: %v", s)
	}
	// a String the VM allocated, written and not
	sv, err := f.vm.allocString([]any{int64(25)})
	if err != nil {
		t.Fatal(err)
	}
	f.must("__gmpz_get_str", &Pointer{Type: f.u8p, Target: sv}, int32(10), ten)
	if s := sv.(string); !strings.HasPrefix(s, "-12345678901234567890\x00") {
		t.Errorf("a String: %q", s)
	}
	f.wantErr("__gmpz_get_str", "not ported yet", &Pointer{Type: f.u8p, Target: "constant"}, int32(10), ten)
	f.wantErr("__gmpz_get_str", "not ported yet", &Pointer{Type: f.u8p, Target: &Memory{Type: memUInt64Type}}, int32(10), ten)
	f.wantErr("__gmpz_get_str", "not ported yet", &Pointer{Type: f.u8p, Target: 5}, int32(10), ten)
	pm, _ := mem(10)
	pm.Off = 11
	f.wantErr("__gmpz_get_str", "not ported yet", pm, int32(10), ten)
	fresh, _ := f.newStruct("GBig")
	f.wantErr("__gmpz_get_str", "uninitialized", p, int32(10), fresh)
	// set_str of something that is no string
	x, _ := f.fresh()
	f.wantErr("__gmpz_set_str", "not ported yet", x, &Pointer{Type: f.u8p, Target: 5}, int32(10))
	// the input is unchanged on an error
	f.must("__gmpz_set_str", x, f.str("42"), int32(10))
	rc := f.must("__gmpz_set_str", x, f.str("4x2"), int32(10))
	if rc != int32(-1) || f.val(x) != "42" {
		t.Errorf("a refused string: %v %s", rc, f.val(x))
	}
}

func (f *gmpFixture) str(s string) *Pointer { return &Pointer{Type: f.u8p, Target: s} }

// mpz_export's words, and what it refuses.
func TestGMPExport(t *testing.T) {
	f := newGMPFixture(t)
	v, _ := f.big("-4660") // 0x1234
	mk := func(et *core.DataType, n int) (*Pointer, *Memory) {
		m := &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType), Elems: make([]any, n)}
		z, _ := bitsValue(et, u128{})
		for i := range m.Elems {
			m.Elems[i] = z
		}
		return &Pointer{Type: f.vm.ptrType(et), Target: m}, m
	}
	cnt := func() (*Pointer, *Struct) { p, st := f.newStruct("GRef64"); st.Fields[0] = uint64(0); return p, st }
	c, cst := cnt()
	r, m := mk(core.UInt8Type, 4)
	if got := f.must("__gmpz_export", r, c, int32(1), uint64(1), int32(0), uint64(0), v); got != any(r) || cst.Fields[0] != uint64(2) {
		t.Errorf("returns %v, count %v", got, cst.Fields[0])
	}
	if m.Elems[0] != uint8(0x12) || m.Elems[1] != uint8(0x34) || m.Elems[2] != uint8(0) {
		t.Errorf("most significant first: %v", m.Elems)
	}
	// at an element offset
	r.Off = 1
	f.must("__gmpz_export", r, c, int32(-1), uint64(1), int32(0), uint64(0), v)
	if m.Elems[1] != uint8(0x34) || m.Elems[2] != uint8(0x12) {
		t.Errorf("at an offset: %v", m.Elems)
	}
	// a word too far
	r.Off = 3
	f.wantErr("__gmpz_export", "do not fit", r, c, int32(-1), uint64(1), int32(0), uint64(0), v)
	r.Off = 0
	for _, bad := range []struct {
		name string
		args []any
	}{
		{"a word of 3 bytes", []any{r, c, int32(1), uint64(3), int32(0), uint64(0), v}},
		{"nails of a whole word", []any{r, c, int32(1), uint64(1), int32(0), uint64(8), v}},
		{"order 0", []any{r, c, int32(0), uint64(1), int32(0), uint64(0), v}},
		{"endian 2", []any{r, c, int32(1), uint64(1), int32(2), uint64(0), v}},
		{"endian -2", []any{r, c, int32(1), uint64(1), int32(-2), uint64(0), v}},
		{"a NULL buffer", []any{&Pointer{Type: f.u8p}, c, int32(1), uint64(1), int32(0), uint64(0), v}},
		{"a buffer of other words", []any{r, c, int32(1), uint64(2), int32(0), uint64(0), v}},
		{"a buffer of no elements", []any{&Pointer{Type: f.u8p, Target: &Memory{Type: memAnyType}}, c, int32(1), uint64(1), int32(0), uint64(0), v}},
		{"a negative offset", []any{&Pointer{Type: r.Type, Target: m, Off: -1}, c, int32(1), uint64(1), int32(0), uint64(0), v}},
		{"a misaligned offset", []any{r, c, int32(1), uint64(2), int32(0), uint64(0), v}},
	} {
		f.wantErr("__gmpz_export", "not ported yet", bad.args...)
	}
	fresh, _ := f.newStruct("GBig")
	f.wantErr("__gmpz_export", "uninitialized", r, c, int32(1), uint64(1), int32(0), uint64(0), fresh)
	f.wantErr("__gmpz_export", "", r, &Pointer{Type: r.Type, Target: "x"}, int32(1), uint64(1), int32(0), uint64(0), v)
	// big endian words of 16 bits
	r2, m2 := mk(core.UInt16Type, 2)
	f.must("__gmpz_export", r2, c, int32(1), uint64(2), int32(1), uint64(0), v)
	if m2.Elems[0] != uint16(0x3412) {
		t.Errorf("big endian: %v", m2.Elems)
	}
}

// limbs_write and limbs_finish, as the manual describes them.
func TestGMPLimbs(t *testing.T) {
	f := newGMPFixture(t)
	x, st := f.fresh()
	if st.Fields[0] != int32(1) {
		t.Fatalf("alloc %v", st.Fields[0])
	}
	d := f.must("__gmpz_limbs_write", x, int64(3)).(*Pointer)
	m := d.Target.(*Memory)
	if len(m.Elems) != 3 || st.Fields[0] != int32(3) || st.Fields[1] != int32(0) {
		t.Fatalf("after limbs_write: %v", st.Fields)
	}
	m.Elems[0], m.Elems[1], m.Elems[2] = uint64(7), uint64(0), uint64(0)
	f.must("__gmpz_limbs_finish", x, int64(-3))
	if got := f.raw(st.Fields[:3]); got != "Z:-1;7" {
		t.Errorf("finished: %s", got)
	}
	// room enough: the same memory
	d2 := f.must("__gmpz_limbs_write", x, int64(2)).(*Pointer)
	if d2.Target != any(m) {
		t.Error("the space was not kept")
	}
	// a new, unset struct
	fresh, fst := f.newStruct("GBig")
	f.must("__gmpz_limbs_write", fresh, int64(2))
	if fst.Fields[0] != int32(2) {
		t.Errorf("an unset struct: %v", fst.Fields)
	}
	f.wantErr("__gmpz_limbs_write", "limbs", x, int64(-1))
	f.wantErr("__gmpz_limbs_finish", "uninitialized", fresh2(f), int64(1))
	f.wantErr("__gmpz_limbs_finish", "not in a Memory", x, int64(9))
	f.wantErr("__gmpz_limbs_finish", "not in a Memory", x, int64(math.MinInt64))
	// realloc2: a value that fits is kept, one that does not is set to 0
	y, _ := f.big("18446744073709551617")
	f.must("__gmpz_realloc2", y, uint64(200))
	if f.val(y) != "18446744073709551617" {
		t.Errorf("realloc2 lost %s", f.val(y))
	}
	f.must("__gmpz_realloc2", y, uint64(64))
	if f.val(y) != "0" {
		t.Errorf("realloc2 kept %s", f.val(y))
	}
	f.must("__gmpz_init", y)
	if y.Target.(*Struct).Fields[0] != int32(1) {
		t.Error("init")
	}
}

func fresh2(f *gmpFixture) *Pointer { p, _ := f.newStruct("GBig"); return p }

// the rest of the integer functions' refusals
func TestGMPRefusals(t *testing.T) {
	f := newGMPFixture(t)
	x, _ := f.fresh()
	one, _ := f.big("1")
	zero, _ := f.big("0")
	neg, _ := f.big("-4")
	nan := math.NaN()
	f.wantErr("__gmpz_cmp_d", "NaN", one, nan)
	f.wantErr("__gmpz_sizeinbase", "base", one, int32(1))
	f.wantErr("__gmpz_sizeinbase", "base", one, int32(63))
	for _, name := range []string{"__gmpz_tdiv_q", "__gmpz_fdiv_r", "__gmpz_cdiv_q"} {
		f.wantErr(name, "DivideError", x, one, zero)
	}
	f.wantErr("__gmpz_tdiv_qr", "DivideError", x, x, one, zero)
	f.wantErr("__gmpz_sqrt", "DivideError", x, neg)
	f.wantErr("__gmpz_set_d", "DivideError", x, math.Inf(1))
	f.wantErr("__gmpz_powm", "DivideError", x, one, one, zero)
	f.wantErr("__gmpz_powm", "DivideError", x, f.bigPtr(2), f.bigPtr(-1), f.bigPtr(4))
	f.wantErr("__gmpn_cmp", "limbs", &Pointer{Type: f.u64p}, &Pointer{Type: f.u64p}, int64(-1))
	d, _ := f.limbPtr(one)
	f.wantErr("__gmpn_cmp", "not in a Memory", d, &Pointer{Type: f.u64p}, int64(1))
	f.wantErr("__gmpn_cmp", "not in a Memory", &Pointer{Type: f.u64p}, d, int64(1))
	f.wantErr("__gmpn_popcount", "not in a Memory", &Pointer{Type: f.u64p}, uint64(1))
	// invert: no inverse, a zero modulus
	if rc := f.must("__gmpz_invert", x, f.bigPtr(2), f.bigPtr(4)); rc != int32(0) {
		t.Error("2 mod 4")
	}
	if rc := f.must("__gmpz_invert", x, f.bigPtr(2), zero); rc != int32(0) {
		t.Error("mod 0")
	}
	// a mpz_setbit on a negative number: nothing above its top, and one below
	y, _ := f.big("-5")
	f.must("__gmpz_setbit", y, uint64(1000))
	f.must("__gmpz_setbit", y, uint64(1))
	if f.val(y) != "-5" {
		t.Errorf("setbit: %s", f.val(y))
	}
	// an _MPQ whose numbers are unset, a zero denominator
	fresh := &Struct{Type: must(f.s.Eval("GRat")).(*core.DataType), Fields: []any{undef, undef, undef, undef, undef, undef, nil}}
	fp := &Pointer{Type: f.vm.ptrType(fresh.Type), Target: fresh}
	q, _ := f.qrat(f.bigPtr(1), f.bigPtr(2))
	q0, _ := f.qrat(f.bigPtr(1), zero)
	n0, _ := f.qrat(zero, f.bigPtr(1))
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"__gmpq_canonicalize", []any{q0}, "DivideError"},
		{"__gmpq_canonicalize", []any{fp}, "uninitialized"},
		{"__gmpq_set", []any{q, fp}, "uninitialized"},
		{"__gmpq_set_z", []any{q, fresh2(f)}, "uninitialized"},
		{"__gmpq_add", []any{q, q0, q}, "DivideError"},
		{"__gmpq_add", []any{q, q, q0}, "DivideError"},
		{"__gmpq_add", []any{q, fp, q}, "uninitialized"},
		{"__gmpq_add", []any{q, q, fp}, "uninitialized"},
		{"__gmpq_div", []any{q, q, n0}, "DivideError"},
		{"__gmpq_cmp", []any{q0, q}, "DivideError"},
		{"__gmpq_cmp", []any{q, q0}, "DivideError"},
	} {
		f.wantErr(c.name, c.want, c.args...)
	}
	// raw settings, as MPQ makes 1//0
	f.must("__gmpq_set_si", q, int64(-1), uint64(0))
	f.must("__gmpq_set_ui", q, uint64(3), uint64(0))
	f.must("__gmpq_set_z", q, f.bigPtr(9))
	f.must("__gmpq_set", q, n0)
	// results that do not fit
	big2, _ := f.bigOf(new(big.Int).Lsh(big.NewInt(1), 64))
	qa, _ := f.qrat(f.bigPtr(1), f.bigPtr(1))
	qbig, _ := f.qrat(f.bigPtr(1), big2)
	qb, _ := f.qrat(f.bigPtr(1), f.bigPtr(1))
	f.vm.MaxMemoryElems = 1
	f.wantLimit("__gmpq_set_z", qa, big2)
	// the denominator's store fails
	f.wantLimit("__gmpq_set", qb, qbig)
	f.must("__gmpq_set_ui", qa, ^uint64(0), uint64(1)) // fits
}

// a buffer far too small, a store that fails in tdiv_qr, a VM without the
// library table
func TestGMPMore(t *testing.T) {
	f := newGMPFixture(t)
	m := &Memory{Type: memUInt8Type, Elems: []any{uint8(0), uint8(0), uint8(0)}}
	neg, _ := f.big("-12345678901234567890")
	f.wantErr("__gmpz_get_str", "does not fit", &Pointer{Type: f.u8p, Target: m}, int32(10), neg)
	x, _ := f.fresh()
	y, _ := f.fresh()
	a, _ := f.big("18446744073709551617")
	f.vm.MaxMemoryElems = 1
	f.wantLimit("__gmpz_tdiv_qr", x, y, a, f.bigPtr(1))
	vm := New()
	vm.libShims = nil
	vm.bootGMP()
	if vm.libShims[gmpLib]["__gmpz_add"] == nil {
		t.Error("no table")
	}
}

func TestGMPSplitArgs(t *testing.T) {
	f := newGMPFixture(t)
	if got := fmt.Sprint(cld64(10, 4), satAdd(math.MaxUint64, 1), satMul(1<<63, 4), ceilBits(1e30), ceilBits(2.5)); got != "3 18446744073709551615 18446744073709551615 18446744073709551615 3" {
		t.Error(got)
	}
	_ = f
}
