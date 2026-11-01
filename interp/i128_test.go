package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The integer intrinsics on Int128 and UInt128, each want julia 1.12.6's
// repr of the same call (a = Int128(-123456789012345678901234567890),
// b = Int128(987654321098765), given by their bit patterns).
func TestIntrinsics128(t *testing.T) {
	vm := New()
	const max = ^uint64(0)
	M, m := Int128{max >> 1, max}, Int128{1 << 63, 0}
	U := UInt128{max, max}
	a, b := Int128{0xfffffffe7116f009, 0x3c8c1f11b1c0f52e}, Int128{0, 0x0003824430f8500d}
	one, mone := Int128{0, 1}, Int128{max, max}
	for _, c := range []struct {
		f    expr.Symbol
		args []any
		want string
	}{
		{"add_int", []any{M, one}, "-170141183460469231731687303715884105728"},
		{"sub_int", []any{m, one}, "170141183460469231731687303715884105727"},
		{"mul_int", []any{a, b}, "68837024295856616922144003854886949718"},
		{"mul_int", []any{U, U}, "0x00000000000000000000000000000001"},
		{"sdiv_int", []any{a, b}, "-124999998860937"},
		{"srem_int", []any{a, b}, "-547854957125085"},
		{"udiv_int", []any{U, UInt128{0, 7}}, "0x24924924924924924924924924924924"},
		{"urem_int", []any{U, UInt128{0, 7}}, "0x00000000000000000000000000000003"},
		{"shl_int", []any{one, int64(127)}, "-170141183460469231731687303715884105728"},
		{"shl_int", []any{one, int64(128)}, "0"},
		{"lshr_int", []any{U, int64(100)}, "0x0000000000000000000000000fffffff"},
		{"ashr_int", []any{a, int64(64)}, "-6692605943"},
		{"ashr_int", []any{a, int64(200)}, "-1"},
		{"slt_int", []any{a, b}, "true"},
		{"slt_int", []any{m, M}, "true"},
		{"ult_int", []any{UInt128{0, 1}, U}, "true"},
		{"checked_sadd_int", []any{M, one}, "(-170141183460469231731687303715884105728, true)"},
		{"checked_sadd_int", []any{a, b}, "(-123456789012344691246913469125, false)"},
		{"checked_ssub_int", []any{m, one}, "(170141183460469231731687303715884105727, true)"},
		{"checked_ssub_int", []any{M, mone}, "(-170141183460469231731687303715884105728, true)"},
		{"checked_smul_int", []any{m, mone}, "(-170141183460469231731687303715884105728, true)"},
		{"checked_smul_int", []any{M, Int128{0, 2}}, "(-2, true)"},
		{"checked_smul_int", []any{m, one}, "(-170141183460469231731687303715884105728, false)"},
		{"checked_smul_int", []any{Int128{0, 1 << 62}, Int128{max, 0}}, "(-85070591730234615865843651857942052864, false)"},
		{"checked_smul_int", []any{a, b}, "(68837024295856616922144003854886949718, true)"},
		{"checked_uadd_int", []any{U, UInt128{0, 1}}, "(0x00000000000000000000000000000000, true)"},
		{"checked_usub_int", []any{UInt128{}, UInt128{0, 1}}, "(0xffffffffffffffffffffffffffffffff, true)"},
		{"checked_umul_int", []any{U, UInt128{0, 2}}, "(0xfffffffffffffffffffffffffffffffe, true)"},
		{"checked_umul_int", []any{UInt128{1, 0}, UInt128{0, 1 << 63}}, "(0x80000000000000000000000000000000, false)"},
		{"checked_sdiv_int", []any{a, b}, "-124999998860937"},
		{"checked_srem_int", []any{a, b}, "-547854957125085"},
		{"checked_udiv_int", []any{U, UInt128{0, 3}}, "0x55555555555555555555555555555555"},
		{"checked_urem_int", []any{U, UInt128{0, 3}}, "0x00000000000000000000000000000000"},
		{"sext_int", []any{core.Int128Type, int8(-1)}, "-1"},
		{"zext_int", []any{core.UInt128Type, int8(-1)}, "0x000000000000000000000000000000ff"},
		{"trunc_int", []any{core.Int8Type, a}, "46"},
		{"bitcast", []any{core.UInt128Type, m}, "0x80000000000000000000000000000000"},
		{"neg_int", []any{m}, "-170141183460469231731687303715884105728"},
		{"not_int", []any{UInt128{}}, "0xffffffffffffffffffffffffffffffff"},
		{"checked_sdiv_int", []any{m, mone}, "!DivideError()"},
		{"checked_srem_int", []any{a, Int128{}}, "!DivideError()"},
		{"checked_udiv_int", []any{U, UInt128{}}, "!DivideError()"},
	} {
		v, err := vm.apply(intrinsic(t, vm, c.f), c.args)
		check(t, string(c.f), v, err, c.want)
	}
	if got := Repr(vm.newTuple([]any{m, M, U, a})); got != "(-170141183460469231731687303715884105728, 170141183460469231731687303715884105727, 0xffffffffffffffffffffffffffffffff, -123456789012345678901234567890)" {
		t.Errorf("show: %s", got)
	}
	if vm.typeOf(a) != any(core.Int128Type) || vm.typeOf(U) != any(core.UInt128Type) || !vm.isa(a, core.SignedType) {
		t.Error("the types of Int128 and UInt128")
	}
	if v, _ := biSizeof(vm, []any{a}); v != int64(16) {
		t.Errorf("sizeof(Int128): %v", v)
	}
	// shifts by the whole width or more
	x := u128{1 << 63, 0}
	if !x.shl(128).isZero() || !x.lshr(130).isZero() || x.ashr(200) != (u128{max, max}) {
		t.Error("shifts of 128 bits or more")
	}

	// bitcast to and from pointers, each answer julia's
	u8p := vm.ptrType(core.UInt8Type)
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{u8p, uint64(16)}, "Ptr{UInt8}(0x0000000000000010)"},
		{[]any{vm.ptrType(core.Int8Type), &Pointer{Type: u8p, Off: 16}}, "Ptr{Int8}(0x0000000000000010)"},
		{[]any{core.UInt64Type, &Pointer{Type: u8p}}, "0x0000000000000000"},
		{[]any{core.UInt64Type, &Pointer{Type: u8p, Target: "s"}}, "!not ported yet: the address of"},
		{[]any{core.UInt32Type, &Pointer{Type: u8p}}, "!not ported yet: the address of"},
		{[]any{u8p, uint32(1)}, "!bitcast: argument size does not match size of target type"},
		{[]any{u8p, "s"}, "!bitcast: argument size does not match size of target type"},
	} {
		v, err := biBitcast(vm, c.args)
		check(t, "bitcast "+showValue(c.args[0]), v, err, c.want)
	}
}
