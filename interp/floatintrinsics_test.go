package interp

import (
	"math"
	"strconv"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The float intrinsics and the integer ones over any primitive value, each
// want julia 1.12.6's repr of the same call, made at top level so that the
// runtime's C intrinsics answer (Core.eval(Main, :(Core.Intrinsics.f(...)))).
func TestFloatIntrinsics(t *testing.T) {
	vm := New()
	const max = ^uint64(0)
	h := func(b uint16) Float16 { return Float16(b) }
	bf := func(b uint16) BFloat16 { return BFloat16(b) }
	nan := math.NaN()
	const rt = ": runtime floating point intrinsics require both arguments to be Float16, BFloat16, Float32, or Float64"
	for _, c := range []struct {
		f    expr.Symbol
		args []any
		want string
	}{
		// arithmetic; muladd is fused, as on arm64
		{"muladd_float", []any{0.1, 10.0, -1.0}, "5.551115123125783e-17"},
		{"muladd_float", []any{float32(0.1), float32(10), float32(-1)}, "1.4901161f-8"},
		{"fma_float", []any{0.1, 10.0, -1.0}, "5.551115123125783e-17"},
		{"fma_float", []any{h(0x3e00), h(0x3e00), h(0x3c00)}, "Float16(3.25)"},
		{"fma_float", []any{float32(1.0000001), float32(1.0000001), float32(-1.0000002)}, "1.4210855f-14"},
		{"fma_float", []any{1.0, 2.0, int64(3)}, "!fma_float: types of a, b, and c must match"},
		{"fma_float", []any{int64(1), int64(2), int64(3)}, "!fma_float" + rt},
		{"fma_float", []any{"a", "b", "c"}, "!fma_float: values are not primitive types"},
		{"neg_float", []any{h(0x7c01)}, "NaN16"},
		{"neg_float", []any{int64(1)}, "!neg_float_withtype" + rt},
		{"neg_float", []any{"a"}, "!neg_float_withtype: value is not a primitive type"},
		{"neg_float_fast", []any{int64(1)}, "!neg_float_withtype" + rt},
		{"abs_float", []any{"a"}, "!abs_float_withtype: value is not a primitive type"},
		{"abs_float", []any{float32(math.Copysign(0, -1))}, "0.0f0"},
		{"add_float", []any{"a", "b"}, "!add_float: values are not primitive types"},
		{"add_float", []any{bf(0x3fc0), bf(0x3dcd)}, "Core.BFloat16(0x3fcd)"},
		{"add_float_fast", []any{1.0, 2.0}, "3.0"},
		{"eq_float_fast", []any{int64(1), int64(1)}, "!eq_float" + rt},
		{"min_float", []any{1.0, nan}, "NaN"},
		{"min_float", []any{math.Copysign(0, -1), 0.0}, "-0.0"},
		{"max_float", []any{math.Copysign(0, -1), 0.0}, "0.0"},
		{"max_float", []any{1.0, nan}, "NaN"},
		{"max_float", []any{2.0, 1.0}, "2.0"},
		{"min_float", []any{h(0x3e00), h(0x3c00)}, "Float16(1.0)"},
		{"copysign_float", []any{1.0, math.Copysign(0, -1)}, "-1.0"},
		{"copysign_float", []any{float32(1), 2.0}, "!copysign_float: types of a and b must match"},
		{"rint_llvm", []any{2.5}, "2.0"},
		{"rint_llvm", []any{float32(-3.5)}, "-4.0f0"},
		{"sqrt_llvm", []any{-1.0}, "NaN"},
		{"sqrt_llvm", []any{bf(0x4000)}, "Core.BFloat16(0x3fb5)"},
		{"sqrt_llvm_fast", []any{float32(2)}, "1.4142135f0"},
		{"ceil_llvm", []any{h(0x3e00)}, "Float16(2.0)"},
		{"trunc_llvm", []any{-2.7}, "-2.0"},
		{"floor_llvm", []any{float32(-2.5)}, "-3.0f0"},
		{"lt_float", []any{h(0x3e00), h(0x3c00)}, "false"},
		{"fpiseq", []any{nan, -nan}, "true"},
		{"fpiseq", []any{0.0, math.Copysign(0, -1)}, "false"},
		{"fpiseq", []any{int64(1), int64(1)}, "!fpiseq" + rt},
		{"fpiseq", []any{h(0x7e00), h(0x7c01)}, "true"},
		{"have_fma", []any{core.Float64Type}, "true"},
		{"have_fma", []any{core.Float32Type}, "true"},
		{"have_fma", []any{core.Float16Type}, "false"},
		{"have_fma", []any{int64(1)}, "!TypeError(:have_fma, \"\", DataType, 1)"},
		// conversions between the floats
		{"fpext", []any{core.Float64Type, 1.5}, "!fpext: output bitsize must be > input bitsize"},
		{"fpext", []any{core.Int64Type, float32(1.5)}, "!fptrunc" + rt},
		{"fpext", []any{core.Float32Type, bf(0x3dcd)}, "0.100097656f0"},
		{"fpext", []any{core.Float64Type, h(0x3e00)}, "1.5"},
		{"fpext", []any{core.Float64Type, "s"}, "!fpext: value is not a primitive type"},
		{"fpext", []any{int64(1), 1.5}, "!TypeError(:fpext, \"\", DataType, 1)"},
		{"fptrunc", []any{core.Float32Type, float32(1.5)}, "!fptrunc: output bitsize must be < input bitsize"},
		{"fptrunc", []any{core.Int16Type, float32(1.5)}, "!fptrunc" + rt},
		{"fptrunc", []any{core.Float16Type, int64(1)}, "!fptrunc" + rt},
		{"fptrunc", []any{core.BFloat16Type, 1.5}, "Core.BFloat16(0x3fc0)"},
		{"fptrunc", []any{core.BFloat16Type, 0.1}, "Core.BFloat16(0x3dcd)"},
		{"fptrunc", []any{core.Float16Type, 0.1}, "Float16(0.1)"},
		{"fptrunc", []any{core.Float32Type, 0.1}, "0.1f0"},
		// integers to floats, through APInt::roundToDouble's faults above
		// 64 bits
		{"sitofp", []any{core.Float32Type, int64(16777217)}, "1.6777216f7"},
		{"sitofp", []any{core.Float16Type, int64(2049)}, "Float16(2.048e3)"},
		{"sitofp", []any{core.BFloat16Type, int64(257)}, "Core.BFloat16(0x4380)"},
		{"sitofp", []any{core.Float64Type, Int128{1 << 36, 1<<47 | 1}}, "3.802951800684688e30"},
		{"sitofp", []any{core.Float64Type, Int128{1 << 36, 1 << 47}}, "3.802951800684688e30"},
		{"sitofp", []any{core.Float64Type, neg128(Int128{1 << 36, 1<<47 | 1})}, "-3.802951800684688e30"},
		{"sitofp", []any{core.Float32Type, Int128{0, 1 << 63}}, "-9.223372f18"},
		{"sitofp", []any{core.Float64Type, Int128{1 << 63, 0}}, "-3.402823669209385e38"},
		{"sitofp", []any{core.Float64Type, Int128{1 << 56, 12345}}, "2.658455991570287e36"},
		{"sitofp", []any{core.Int64Type, int64(3)}, "!SItoFP: runtime floating point intrinsics are not implemented for bit sizes other than 16, 32 and 64"},
		{"sitofp", []any{core.Float64Type, 1.5}, "4.6094342186137027e18"},
		{"sitofp", []any{core.Float64Type, "s"}, "!sitofp: value is not a primitive type"},
		{"sitofp", []any{int64(1), int64(1)}, "!TypeError(:sitofp, \"\", DataType, 1)"},
		{"uitofp", []any{core.Float64Type, UInt128{max, max}}, "NaN"},
		{"uitofp", []any{core.Float64Type, UInt128{1<<63 | 1<<10, 1}}, "3.402823669209385e38"},
		{"uitofp", []any{core.Float64Type, UInt128{1, 1<<11 | 1}}, "5.5340232221128655e19"},
		{"uitofp", []any{core.Float32Type, UInt128{1, 1<<40 | 1}}, "5.5340232f19"},
		{"uitofp", []any{core.Float64Type, UInt128{1<<52 | 1<<51, 1 << 63}}, "-1.661534994731145e35"},
		{"uitofp", []any{core.Float64Type, UInt128{1, 0}}, "5.5340232221128655e19"},
		{"uitofp", []any{core.Float16Type, uint16(0x0801)}, "Float16(2.048e3)"},
		{"uitofp", []any{core.Int16Type, uint16(0x0801)}, "!UItoFP: runtime floating point intrinsics are not implemented for bit sizes other than 32 and 64"},
		// floats to integers: arm64's saturating casts up to 64 bits,
		// APFloat's above
		{"fptosi", []any{core.Int128Type, 1.0e30}, "1000000000000000019884624838656"},
		{"fptosi", []any{core.Int128Type, 1.0e300}, "170141183460469231731687303715884105727"},
		{"fptosi", []any{core.Int128Type, -1.0e300}, "-170141183460469231731687303715884105728"},
		{"fptosi", []any{core.Int128Type, nan}, "0"},
		{"fptosi", []any{core.Int128Type, 1.5e19}, "15000000000000000000"},
		{"fptosi", []any{core.Int128Type, -2.5}, "-2"},
		{"fptosi", []any{core.Int128Type, h(0x7bff)}, "65504"},
		{"fptosi", []any{core.UInt128Type, 1.5}, "0x00000000000000000000000000000001"},
		{"fptoui", []any{core.UInt128Type, -1.0}, "0x00000000000000000000000000000000"},
		{"fptoui", []any{core.UInt128Type, 1.0e300}, "0xffffffffffffffffffffffffffffffff"},
		{"fptoui", []any{core.UInt128Type, 3.9}, "0x00000000000000000000000000000003"},
		{"fptosi", []any{core.Int32Type, 1.0e300}, "-1"},
		{"fptosi", []any{core.Int8Type, 300.0}, "44"},
		{"fptosi", []any{core.Int64Type, nan}, "0"},
		{"fptosi", []any{core.Int64Type, -1.0e300}, "-9223372036854775808"},
		{"fptosi", []any{core.Float64Type, 3.5}, "1.5e-323"},
		{"fptosi", []any{core.Int64Type, int64(3)}, "!FPtoSI: runtime floating point intrinsics are not implemented for bit sizes other than 16, 32 and 64"},
		{"fptosi", []any{core.Int64Type, "s"}, "!fptosi: value is not a primitive type"},
		{"fptosi", []any{int64(1), 1.5}, "!TypeError(:fptosi, \"\", DataType, 1)"},
		{"fptoui", []any{core.UInt8Type, -1.0}, "0x00"},
		{"fptoui", []any{core.UInt64Type, -1.0}, "0x0000000000000000"},
		{"fptoui", []any{core.UInt64Type, -0.5}, "0x0000000000000000"},
		{"fptoui", []any{core.UInt64Type, 1.0e300}, "0xffffffffffffffff"},
		{"fptoui", []any{core.UInt8Type, h(0x5bd0)}, "0xfa"},
		{"fptoui", []any{core.Int64Type, -3.0}, "0"},
		{"fptoui", []any{core.Int32Type, 5.0e9}, "705032704"},
		// the integer intrinsics take any primitive value's bits
		{"flipsign_int", []any{int64(5), int64(-2)}, "-5"},
		{"flipsign_int", []any{int64(5), int32(-2)}, "!flipsign_int: types of a and b must match"},
		{"flipsign_int", []any{uint8(5), uint8(0xff)}, "0xfb"},
		{"flipsign_int", []any{int64(math.MinInt64), int64(-1)}, "-9223372036854775808"},
		{"flipsign_int", []any{1.5, -1.0}, "-3.0"},
		{"flipsign_int", []any{Int128{0, 7}, Int128{max, max}}, "-7"},
		{"bswap_int", []any{uint16(0x0102)}, "0x0201"},
		{"bswap_int", []any{true}, "true"},
		{"bswap_int", []any{Int128{0, 1}}, "1329227995784915872903807060280344576"},
		{"bswap_int", []any{h(0x3e00)}, "Float16(3.7e-6)"},
		{"bswap_int", []any{1.0}, "3.03865e-319"},
		{"bswap_int", []any{"a"}, "!bswap_int: value is not a primitive type"},
		{"ctpop_int", []any{Int128{max, max}}, "128"},
		{"ctpop_int", []any{true}, "true"},
		{"ctpop_int", []any{expr.Char('a')}, `'\x00\x00\x00\x03'`},
		{"ctlz_int", []any{Int128{0, 1}}, "127"},
		{"ctlz_int", []any{1.0}, "1.0e-323"},
		{"ctlz_int", []any{false}, "false"},
		{"ctlz_int", []any{h(0x0001)}, "Float16(9.0e-7)"},
		{"cttz_int", []any{int16(0)}, "16"},
		{"cttz_int", []any{Int128{1, 0}}, "64"},
		{"sext_int", []any{core.Int8Type, int8(1)}, "!SExt: output bitsize must be > input bitsize"},
		{"zext_int", []any{core.Int8Type, int16(1)}, "!ZExt: output bitsize must be > input bitsize"},
		{"trunc_int", []any{core.Int16Type, int8(1)}, "!Trunc: output bitsize must be < input bitsize"},
		{"trunc_int", []any{core.Float16Type, int64(1)}, "Float16(6.0e-8)"},
		{"bitcast", []any{core.Float16Type, int16(1)}, "Float16(6.0e-8)"},
		{"bitcast", []any{core.Float16Type, uint16(0x3e00)}, "Float16(1.5)"},
		{"bitcast", []any{core.BFloat16Type, uint16(0x7fc0)}, "Core.BFloat16(0x7fc0)"},
		{"bitcast", []any{core.UInt16Type, h(0x3e00)}, "0x3e00"},
	} {
		v, err := vm.apply(intrinsic(t, vm, c.f), c.args)
		check(t, string(c.f), v, err, c.want)
	}
	for _, c := range []struct {
		f    expr.Symbol
		args []any
		want string
	}{
		// fmaf's sums on a float64 tie, moved off it each way
		{"fma_float", []any{float32(1.0000001), math.Float32frombits(0x337fffff), float32(1.0000001)}, "1.0000002f0"},
		{"fma_float", []any{float32(1.0002441), float32(1.0002441), -math.Float32frombits(0x1c800000)}, "1.0004883f0"},
		// 1 + 2^-23 + (2^-24 - 2^-70): the sum lands on the tie, below it
		{"fma_float", []any{float32(1.0000001), math.Float32frombits(0x337ffffe), float32(1.0000001)}, "1.0000001f0"},
		{"min_float", []any{2.0, 1.0}, "1.0"},
		{"neg_float_fast", []any{1.0}, "-1.0"},
		// roundToDouble with the absolute value in one word
		{"sitofp", []any{core.Float64Type, Int128{max, max}}, "-2.0000000000000004"},
		{"sitofp", []any{core.Float64Type, neg128(Int128{0, 1<<60 | 1})}, "-3.458764513820541e18"},
		{"bswap_int", []any{float32(1)}, "4.6006f-41"},
		{"bswap_int", []any{bf(0x3fc0)}, "Core.BFloat16(0xc03f)"},
	} {
		v, err := vm.apply(intrinsic(t, vm, c.f), c.args)
		check(t, string(c.f), v, err, c.want)
	}
	// a user's primitive type has no Go value yet (julia: P64(0x0000000000000001))
	uvm := New()
	if err := uvm.Boot(); err != nil {
		t.Fatal(err)
	}
	if _, err := uvm.CoreSession().Eval("primitive type P64 64 end\nprimitive type P8 8 end"); err != nil {
		t.Fatal(err)
	}
	p64, p8 := coreValue(t, uvm, "P64"), coreValue(t, uvm, "P8")
	for _, c := range []struct {
		f    expr.Symbol
		args []any
	}{
		{"fptosi", []any{p64, 1.0}}, {"trunc_int", []any{p8, int64(1)}}, {"bitcast", []any{p64, int64(1)}},
	} {
		v, err := uvm.apply(intrinsic(t, uvm, c.f), c.args)
		check(t, string(c.f), v, err, "!not ported yet")
	}
	// the digit search is bounded whatever it is asked
	if d, _ := shortestHalf(0x7e00); d == "" {
		t.Error("shortestHalf of a NaN")
	}
	if v, _ := vm.apply(intrinsic(t, vm, "neg_float"), []any{h(0x7c01)}); v != h(0xfe01) {
		t.Errorf("neg_float of a signalling Float16 NaN: %v, julia: 0xfe01", v)
	}
	if vm.typeOf(h(0)) != any(core.Float16Type) || vm.typeOf(bf(0)) != any(core.BFloat16Type) {
		t.Error("the types of Float16 and BFloat16")
	}
}

// Any Float16 shown reads back as itself, and the search ends (it once
// looped forever on a wrong table).
func FuzzShowHalf(f *testing.F) {
	for _, h := range []uint16{0, 1, 0x3c00, 0x2000, 0x4060, 0x7bff, 0x7c00, 0x7e00, 0x8001, 0xfc00} {
		f.Add(h)
	}
	f.Fuzz(func(t *testing.T, h uint16) {
		s := showHalf(h, false)
		x := float64(halfToFloat(h))
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return
		}
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || doubleToHalf(v) != h {
			t.Errorf("%04x shown %q reads back as %04x (%v)", h, s, doubleToHalf(v), err)
		}
	})
}

// The 128-bit conversions are total: any bits, any double.
func FuzzConversions128(f *testing.F) {
	f.Add(uint64(0), uint64(0), 0.0)
	f.Add(^uint64(0), ^uint64(0), math.NaN())
	f.Add(uint64(1)<<63, uint64(0), -1.0e300)
	f.Fuzz(func(t *testing.T, hi, lo uint64, x float64) {
		roundToDouble(u128{hi, lo}, 128, true)
		roundToDouble(u128{hi, lo}, 128, false)
		apfloatToInt(x, 128, true)
		apfloatToInt(x, 128, false)
		fcvtzs(x)
		fcvtzu(x)
		doubleToHalf(x)
		doubleToBfloat(x)
	})
}

func neg128(x Int128) Int128 {
	n := u128{x.Hi, x.Lo}.neg()
	return Int128{n.hi, n.lo}
}

// Float16 values shown, each want julia 1.12.6's repr.
func TestShowFloat16(t *testing.T) {
	for _, c := range []struct {
		x    float64
		want string
	}{
		{0.1, "Float16(0.1)"}, {1000, "Float16(1.0e3)"}, {500, "Float16(500.0)"},
		{math.Copysign(0, -1), "Float16(-0.0)"}, {6.0e-8, "Float16(6.0e-8)"}, {65504, "Float16(6.55e4)"},
		{1.0e-5, "Float16(1.0e-5)"}, {123.4, "Float16(123.4)"}, {0.000123, "Float16(0.000123)"},
		{-999.5, "Float16(-999.5)"}, {100, "Float16(100.0)"}, {1, "Float16(1.0)"},
		{math.NaN(), "NaN16"}, {math.Inf(-1), "-Inf16"},
	} {
		if got := Repr(Float16(doubleToHalf(c.x))); got != c.want {
			t.Errorf("%v: %s, julia: %s", c.x, got, c.want)
		}
	}
	if got := Repr(New().newTuple([]any{Float16(0x3e00)})); got != "(Float16(1.5),)" {
		t.Errorf("tuple: %s", got)
	}
	// Float16[1000, -0.0, Inf, NaN, 6.0e-8] shows Float16[1.0e3, -0.0, Inf, NaN, 6.0e-8]
	var b showBuf
	for i, x := range []float64{1000, math.Copysign(0, -1), math.Inf(1), math.NaN(), 6.0e-8} {
		if i > 0 {
			b.WriteString(", ")
		}
		writeIn(&b, Float16(doubleToHalf(x)), core.Float16Type)
	}
	if got := b.String(); got != "1.0e3, -0.0, Inf, NaN, 6.0e-8" {
		t.Errorf("elements: %s", got)
	}
}
