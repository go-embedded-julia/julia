package interp

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// gmpFixture calls the libgmp shims as Base's gmp.jl does: a Ref{BigInt} is
// a pointer to the BigInt's struct, whose limbs are a Memory{UInt64}.
type gmpFixture struct {
	t    *testing.T
	vm   *VM
	s    *Session
	u8p  *core.DataType
	u64p *core.DataType
}

func newGMPFixture(t *testing.T) *gmpFixture {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`mutable struct GBig; alloc::Int32; size::Int32; d::Ptr{UInt64}; end
mutable struct GRat; na::Int32; ns::Int32; nd::Ptr{UInt64}; da::Int32; ds::Int32; dd::Ptr{UInt64}; r::Any; end
mutable struct GBad; alloc::Int32; size::Int64; d::Ptr{UInt64}; end
mutable struct GSmall; alloc::Int32; size::Int32; end
mutable struct GRef64; x::UInt64; end
mutable struct GRef32; x::Int32; end`); err != nil {
		t.Fatal(err)
	}
	return &gmpFixture{t: t, vm: vm, s: s, u8p: vm.ptrType(core.UInt8Type), u64p: vm.ptrType(core.UInt64Type)}
}

func (f *gmpFixture) newStruct(typ string) (*Pointer, *Struct) {
	t := must(f.s.Eval(typ)).(*core.DataType)
	ft, _ := t.FieldTypes()
	fields := make([]any, len(ft))
	for i := range fields {
		fields[i] = undef // new(): nothing set
	}
	st := &Struct{Type: t, Fields: fields}
	return &Pointer{Type: f.vm.ptrType(st.Type), Target: st}, st
}

// big is a BigInt holding the decimal, set by the VM's own store.
func (f *gmpFixture) big(dec string) (*Pointer, *Struct) {
	v, ok := new(big.Int).SetString(dec, 10)
	if !ok {
		f.t.Fatalf("not a number: %q", dec)
	}
	return f.bigOf(v)
}

func (f *gmpFixture) bigOf(v *big.Int) (*Pointer, *Struct) {
	p, st := f.newStruct("GBig")
	if err := f.vm.putMpz(st.Fields[:3], v); err != nil {
		f.t.Fatal(err)
	}
	return p, st
}

// fresh is BigInt(): init2!(new(), 0), as gmp.jl makes one.
func (f *gmpFixture) fresh() (*Pointer, *Struct) {
	p, st := f.newStruct("GBig")
	f.must("__gmpz_init2", p, uint64(0))
	return p, st
}

func (f *gmpFixture) call(name string, args ...any) (any, error) {
	f.t.Helper()
	sh := f.vm.libShims[gmpLib][expr.Symbol(name)]
	if sh == nil {
		f.t.Fatalf("no shim %s", name)
	}
	return sh(f.vm, args)
}

func (f *gmpFixture) must(name string, args ...any) any {
	f.t.Helper()
	v, err := f.call(name, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return v
}

// raw is the fields julia's code reads: size, then the limbs of *d, which
// must also hold the invariants GMP keeps: the memory is alloc limbs long and
// the top limb is not zero.
func (f *gmpFixture) raw(fields []any) string {
	f.t.Helper()
	alloc, size := fields[0].(int32), fields[1].(int32)
	n := size
	if n < 0 {
		n = -n
	}
	d := fields[2].(*Pointer)
	m := d.Target.(*Memory)
	if int(alloc) != len(m.Elems) || alloc < n || alloc < 1 || d.Off != 0 || d.Type != f.u64p {
		f.t.Errorf("alloc %d, size %d, %d limbs at offset %d of type %s", alloc, size, len(m.Elems), d.Off, showValue(d.Type))
	}
	var limbs []string
	for i := int32(0); i < n; i++ {
		limbs = append(limbs, strconv.FormatUint(m.Elems[i].(uint64), 16))
	}
	if n > 0 && m.Elems[n-1].(uint64) == 0 {
		f.t.Errorf("a zero top limb in size %d", size)
	}
	return fmt.Sprintf("Z:%d;%s", size, strings.Join(limbs, ","))
}

// token decodes a golden input.
func (f *gmpFixture) token(tok string) any {
	kind, val, _ := strings.Cut(tok, ":")
	switch kind {
	case "z":
		p, _ := f.big(val)
		return p
	case "u":
		return must(strconv.ParseUint(val, 10, 64))
	case "i":
		return must(strconv.ParseInt(val, 10, 64))
	case "c":
		return int32(must(strconv.ParseInt(val, 10, 32)))
	case "f":
		return math.Float64frombits(must(strconv.ParseUint(val, 16, 64)))
	case "s":
		return &Pointer{Type: f.u8p, Target: must(strconv.Unquote(`"` + val + `"`))}
	}
	f.t.Fatalf("token %q", tok)
	return nil
}

func structOf(p any) *Struct { return p.(*Pointer).Target.(*Struct) }

func signStr(x any) string {
	switch n := x.(type) {
	case int32:
		return fmt.Sprint(big.NewInt(int64(n)).Sign())
	}
	panic("sign of a non int32")
}

func intStr(x any) string {
	switch n := x.(type) {
	case int32:
		return fmt.Sprint(n)
	case uint64:
		return fmt.Sprint(int64(n))
	}
	panic("not an integer")
}

// run is one golden line: the outputs of the shim the function names.
func (f *gmpFixture) run(op string, in []string) ([]string, error) {
	args := make([]any, len(in))
	for i, tok := range in {
		args[i] = f.token(tok)
	}
	one := func(name string, pre ...any) ([]string, error) {
		o, os := f.fresh()
		_, err := f.call(name, append([]any{o}, append(pre, args...)...)...)
		if err != nil {
			return nil, err
		}
		return []string{f.raw(os.Fields[:3])}, nil
	}
	switch op {
	case "add", "sub", "mul", "fdiv_q", "tdiv_q", "cdiv_q", "fdiv_r", "tdiv_r", "cdiv_r", "gcd", "lcm", "and", "ior", "xor",
		"add_ui", "sub_ui", "mul_ui", "mul_si", "mul_2exp", "fdiv_q_2exp", "pow_ui", "bin_ui", "ui_sub",
		"neg", "com", "sqrt", "set", "fac_ui", "set_ui", "set_si", "set_d":
		return one("__gmpz_" + op)
	case "tdiv_qr", "gcdext":
		outs := []*Pointer{}
		sts := []*Struct{}
		nout := 2
		if op == "gcdext" {
			nout = 3
		}
		for range nout {
			p, st := f.fresh()
			outs, sts = append(outs, p), append(sts, st)
		}
		a := []any{}
		for _, o := range outs {
			a = append(a, o)
		}
		if _, err := f.call("__gmpz_"+op, append(a, args...)...); err != nil {
			return nil, err
		}
		var res []string
		for _, st := range sts {
			res = append(res, f.raw(st.Fields[:3]))
		}
		return res, nil
	case "invert":
		o, st := f.fresh()
		rc, err := f.call("__gmpz_invert", o, args[0], args[1])
		if err != nil {
			return nil, err
		}
		if rc.(int32) == 0 {
			return []string{"I:0"}, nil
		}
		return []string{"I:1", f.raw(st.Fields[:3])}, nil
	case "powm":
		return one("__gmpz_powm")
	case "cmp", "cmp_ui", "cmp_si", "cmp_d":
		rc, err := f.call("__gmpz_"+op, args...)
		if err != nil {
			return nil, err
		}
		return []string{"I:" + signStr(rc)}, nil
	case "sizeinbase", "popcount", "scan0", "scan1", "tstbit":
		rc, err := f.call("__gmpz_"+op, args...)
		if err != nil {
			return nil, err
		}
		return []string{"I:" + intStr(rc)}, nil
	case "get_d":
		rc, err := f.call("__gmpz_get_d", args...)
		if err != nil {
			return nil, err
		}
		return []string{"F:" + strconv.FormatUint(math.Float64bits(rc.(float64)), 16)}, nil
	case "setbit":
		if _, err := f.call("__gmpz_setbit", args...); err != nil {
			return nil, err
		}
		return []string{f.raw(structOf(args[0]).Fields[:3])}, nil
	case "mpn_popcount":
		// mpn_popcount(abs(a))
		d, n := f.limbPtr(args[0])
		rc, err := f.call("__gmpn_popcount", d, uint64(n))
		if err != nil {
			return nil, err
		}
		return []string{"I:" + intStr(rc)}, nil
	case "mpn_cmp":
		d, n := f.limbPtr(args[0])
		e, _ := f.limbPtr(args[1])
		rc, err := f.call("__gmpn_cmp", d, e, int64(n))
		if err != nil {
			return nil, err
		}
		return []string{"I:" + signStr(rc)}, nil
	case "get_str":
		return f.getStr(args[0], args[1].(int32))
	case "set_str":
		o, st := f.fresh()
		rc, err := f.call("__gmpz_set_str", o, args[0], args[1])
		if err != nil {
			return nil, err
		}
		if rc.(int32) != 0 {
			return []string{"I:" + intStr(rc)}, nil
		}
		return []string{"I:0", f.raw(st.Fields[:3])}, nil
	case "export":
		return f.export(args)
	case "q_canon", "q_add", "q_sub", "q_mul", "q_div", "q_cmp":
		return f.rational(op, args)
	}
	// the destination is an operand: add_a is add!(x, x, b), add_b add!(x, a, x)
	if i := strings.LastIndex(op, "_"); i > 0 && (op[i+1:] == "a" || op[i+1:] == "b") {
		base, which := op[:i], op[i+1:]
		var x *Pointer
		var st *Struct
		var a []any
		if which == "a" {
			x, st = f.bigOf(f.value(args[0]))
			a = []any{x, x, args[1]}
		} else {
			x, st = f.bigOf(f.value(args[1]))
			a = []any{x, args[0], x}
		}
		if _, err := f.call("__gmpz_"+base, a...); err != nil {
			return nil, err
		}
		return []string{f.raw(st.Fields[:3])}, nil
	}
	f.t.Fatalf("unknown op %q", op)
	return nil, nil
}

// value is the integer a BigInt pointer holds.
func (f *gmpFixture) value(p any) *big.Int {
	v, err := f.vm.getMpz(structOf(p).Fields[:3])
	if err != nil {
		f.t.Fatal(err)
	}
	return v
}

// limbPtr is the Ptr{Limb} of a BigInt's limbs and their count.
func (f *gmpFixture) limbPtr(p any) (*Pointer, int) {
	st := structOf(p)
	n := int(st.Fields[1].(int32))
	return st.Fields[2].(*Pointer), max(n, -n)
}

func (f *gmpFixture) getStr(z any, base int32) ([]string, error) {
	room := int(gmpSizeInBase(f.value(z), int(max(base, -base)))) + 3
	m := &Memory{Type: memUInt8Type, Elems: make([]any, room)}
	for i := range m.Elems {
		m.Elems[i] = uint8(0)
	}
	buf := &Pointer{Type: f.u8p, Target: m}
	if _, err := f.call("__gmpz_get_str", buf, base, z); err != nil {
		return nil, err
	}
	var text []byte
	for _, e := range m.Elems {
		if e.(uint8) == 0 {
			break
		}
		text = append(text, e.(uint8))
	}
	return []string{"S:" + string(text)}, nil
}

func (f *gmpFixture) export(a []any) ([]string, error) {
	size, order, endian, nails := a[1].(uint64), a[2].(int32), a[3].(int32), a[4].(uint64)
	et := map[uint64]*core.DataType{1: core.UInt8Type, 2: core.UInt16Type, 4: core.Int32Type, 8: core.UInt64Type}[size]
	n := int(cld64(gmpSizeInBase(f.value(a[0]), 2), 8*size-nails))
	m := &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), et, core.CPU)).(*core.DataType), Elems: make([]any, n)}
	zero, _ := bitsValue(et, u128{})
	for i := range m.Elems {
		m.Elems[i] = zero
	}
	count, cst := f.newStruct("GRef64")
	cst.Fields[0] = uint64(0)
	rop := &Pointer{Type: f.vm.ptrType(et), Target: m}
	if _, err := f.call("__gmpz_export", rop, count, order, size, endian, nails, a[0]); err != nil {
		return nil, err
	}
	got := cst.Fields[0].(uint64)
	var words []string
	for _, e := range m.Elems[:got] {
		bits, _, _ := primBits(e)
		w := bits.lo
		if size < 8 {
			w &= 1<<(8*size) - 1
		}
		words = append(words, strconv.FormatUint(w, 16))
	}
	return []string{"I:" + fmt.Sprint(got), "H:" + strings.Join(words, ",")}, nil
}

// qrat is an _MPQ of a numerator and a denominator.
func (f *gmpFixture) qrat(num, den any) (*Pointer, *Struct) {
	p, st := f.newStruct("GRat")
	for i, v := range []any{num, den} {
		if err := f.vm.putMpz(st.Fields[3*i:3*i+3], f.value(v)); err != nil {
			f.t.Fatal(err)
		}
	}
	return p, st
}

func (f *gmpFixture) rational(op string, a []any) ([]string, error) {
	x, _ := f.qrat(a[0], a[1])
	if op == "q_canon" {
		// canonicalize of a numerator and a denominator, as Rational{BigInt} makes it
		if _, err := f.call("__gmpq_canonicalize", x); err != nil {
			return nil, err
		}
		return f.qraw(structOf(x)), nil
	}
	y, _ := f.qrat(a[2], a[3])
	if op == "q_cmp" {
		rc, err := f.call("__gmpq_cmp", x, y)
		if err != nil {
			return nil, err
		}
		return []string{"I:" + signStr(rc)}, nil
	}
	z, zs := f.qrat(f.bigPtr(0), f.bigPtr(1))
	if _, err := f.call("__gmpq_"+strings.TrimPrefix(op, "q_"), z, x, y); err != nil {
		return nil, err
	}
	return f.qraw(zs), nil
}

func (f *gmpFixture) bigPtr(v int64) *Pointer {
	p, _ := f.bigOf(big.NewInt(v))
	return p
}

func (f *gmpFixture) qraw(st *Struct) []string {
	return []string{f.raw(st.Fields[:3]), f.raw(st.Fields[3:6])}
}

// The golden is julia 1.12.6's own libgmp, called as gmp.jl calls it.
func TestGMPGolden(t *testing.T) {
	data, err := os.ReadFile("testdata/gmp.golden")
	if err != nil {
		t.Fatal(err)
	}
	f := newGMPFixture(t)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	counts := map[string]int{}
	failed := 0
	for n, line := range lines {
		fields := strings.Split(line, "\t")
		op := fields[0]
		// the inputs are the tokens before the results, which start at the
		// first Z:, I:, F:, S:, H: or E:
		cut := len(fields)
		for i := 1; i < len(fields); i++ {
			if len(fields[i]) > 1 && fields[i][1] == ':' && strings.Contains("ZIFSHE", fields[i][:1]) {
				cut = i
				break
			}
		}
		want := strings.Join(fields[cut:], "\t")
		got, err := f.run(op, fields[1:cut])
		var th *Thrown
		switch {
		case errors.As(err, &th):
			got = []string{"E:" + th.Value.(*Struct).Type.Name.Name}
		case err != nil:
			t.Errorf("line %d %s: %v", n+1, line, err)
			continue
		}
		counts[op]++
		if g := strings.Join(got, "\t"); g != want {
			failed++
			if failed < 20 {
				t.Errorf("line %d: %s\n got %s\nwant %s", n+1, strings.Join(fields[:cut], " "), g, want)
			}
		}
	}
	t.Logf("%d golden lines, %d operations, %d failed", len(lines), len(counts), failed)
}
