package interp

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The floating-point intrinsics of src/runtime_intrinsics.c over the four
// float types. A Float16 or BFloat16 is computed on in Float32 and rounded
// back (runtime_intrinsics.c:762-817, 883-909); here every operand is
// widened to a float64, which holds the four exactly, and the result is
// rounded to the operand's type: for +, -, *, / and sqrt, rounding a
// float64 result to Float32 is the Float32 operation's rounding, the float64
// carrying more than twice Float32's precision.

// fkind is the float type of a value.
type fkind uint8

const (
	notFloat fkind = iota
	f16
	bf16
	f32
	f64
)

// floatOf is a float value widened, and its kind.
func floatOf(v any) (float64, fkind) {
	switch x := v.(type) {
	case float64:
		return x, f64
	case float32:
		return widen32(x), f32
	case Float16:
		return widen32(halfToFloat(uint16(x))), f16
	case BFloat16:
		return widen32(bfloatToFloat(uint16(x))), bf16
	}
	return 0, notFloat
}

// value is r computed in kind k's C type (float for the 16-bit ones) and
// stored as k: float_to_half or float_to_bfloat of the Float32 result.
func (k fkind) value(r float64) any {
	switch k {
	case f64:
		return r
	case f32:
		return narrow64(r)
	case f16:
		return Float16(floatToHalf(narrow64(r)))
	}
	return BFloat16(floatToBfloat(narrow64(r)))
}

// isPrimitive is jl_is_primitivetype(jl_typeof(v)) for the values this VM
// holds.
func isPrimitive(v any) bool {
	if _, _, ok := primBits(v); ok {
		return true
	}
	_, ok := v.(*Pointer)
	return ok
}

const floatTypesMsg = ": runtime floating point intrinsics require both arguments to be Float16, BFloat16, Float32, or Float64"

func (vm *VM) bootFloatIntrinsics(add func(expr.Symbol, int, func(*VM, []any) (any, error))) {
	// un_fintrinsic (runtime_intrinsics.c:1713-1728): the messages carry the
	// _withtype name, an alias the name it stands for (intrinsics.h:25-46)
	for name, op := range map[expr.Symbol]func(float64) float64{
		"neg_float":      func(x float64) float64 { return -x },
		"abs_float":      math.Abs,
		"ceil_llvm":      math.Ceil,
		"floor_llvm":     math.Floor,
		"trunc_llvm":     math.Trunc,
		"rint_llvm":      math.RoundToEven,
		"sqrt_llvm":      math.Sqrt,
		"sqrt_llvm_fast": math.Sqrt,
	} {
		add(name, 1, vm.floatUnary(name+"_withtype", op))
	}
	add("neg_float_fast", 1, vm.floatUnary("neg_float_withtype", func(x float64) float64 { return -x }))
	binary := map[expr.Symbol]func(a, b float64) float64{
		"add_float": func(a, b float64) float64 { return a + b },
		"sub_float": func(a, b float64) float64 { return a - b },
		"mul_float": func(a, b float64) float64 { return a * b },
		"div_float": func(a, b float64) float64 { return a / b },
		// min_float and max_float (runtime_intrinsics.c:1372-1408): x - y
		// tells which, and is the answer when either is a NaN
		"min_float": func(x, y float64) float64 {
			d := x - y
			if math.IsNaN(x) || math.IsNaN(y) {
				return d
			}
			if math.Signbit(d) {
				return x
			}
			return y
		},
		"max_float": func(x, y float64) float64 {
			d := x - y
			if math.IsNaN(x) || math.IsNaN(y) {
				return d
			}
			if math.Signbit(d) {
				return y
			}
			return x
		},
		"copysign_float": math.Copysign,
	}
	for name, op := range binary {
		add(name, 2, vm.floatBinary(name, op))
	}
	compare := map[expr.Symbol]func(a, b float64) bool{
		"eq_float": func(a, b float64) bool { return a == b },
		"ne_float": func(a, b float64) bool { return a != b },
		"lt_float": func(a, b float64) bool { return a < b },
		"le_float": func(a, b float64) bool { return a <= b },
		// fpiseq_n (runtime_intrinsics.c:1544-1554): widening keeps two
		// values' bits equal exactly when they were
		"fpiseq": func(a, b float64) bool {
			return math.IsNaN(a) && math.IsNaN(b) || math.Float64bits(a) == math.Float64bits(b)
		},
	}
	for name, cmp := range compare {
		add(name, 2, vm.floatCompare(name, cmp))
	}
	for _, name := range []expr.Symbol{"add_float", "sub_float", "mul_float", "div_float", "min_float", "max_float"} {
		add(name+"_fast", 2, vm.floatBinary(name, binary[name]))
	}
	for _, name := range []expr.Symbol{"eq_float", "ne_float", "lt_float", "le_float"} {
		add(name+"_fast", 2, vm.floatCompare(name, compare[name]))
	}
	// muladd's a * b + c is contracted to a fused multiply-add on arm64,
	// as julia 1.12.6 answers there (muladd_float(0.1, 10.0, -1.0) is
	// 5.551115123125783e-17, not 0.0)
	add("fma_float", 3, vm.floatTernary("fma_float"))
	add("muladd_float", 3, vm.floatTernary("muladd_float"))
	add("have_fma", 1, func(vm *VM, a []any) (any, error) {
		// jl_have_fma (runtime_intrinsics.c:1731-1740); jl_cpu_has_fma is
		// true on arm64 for both
		if _, ok := a[0].(*core.DataType); !ok {
			return nil, vm.typeError("have_fma", "", core.DataTypeType, a[0])
		}
		return a[0] == any(core.Float32Type) || a[0] == any(core.Float64Type), nil
	})
	add("fptrunc", 2, vm.fpResize("fptrunc", false))
	add("fpext", 2, vm.fpResize("fpext", true))
	add("sitofp", 2, vm.intToFloat("sitofp", true,
		"SItoFP: runtime floating point intrinsics are not implemented for bit sizes other than 16, 32 and 64"))
	add("uitofp", 2, vm.intToFloat("uitofp", false,
		"UItoFP: runtime floating point intrinsics are not implemented for bit sizes other than 32 and 64"))
	add("fptosi", 2, vm.floatToInt("fptosi", true))
	add("fptoui", 2, vm.floatToInt("fptoui", false))
}

// floatUnary is jl_fintrinsic_1 (runtime_intrinsics.c:1068-1092).
func (vm *VM) floatUnary(name expr.Symbol, op func(float64) float64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, k := floatOf(a[0])
		if k == notFloat {
			if !isPrimitive(a[0]) {
				return nil, vm.errorf("%s: value is not a primitive type", name)
			}
			return nil, vm.errorf("%s%s", name, floatTypesMsg)
		}
		return k.value(op(x)), nil
	}
}

// floatArgs is the check of bi_fintrinsic and bool_fintrinsic
// (runtime_intrinsics.c:1257-1314).
func (vm *VM) floatArgs(name expr.Symbol, a, b any) (float64, float64, fkind, error) {
	if vm.typeOf(a) != vm.typeOf(b) {
		return 0, 0, notFloat, vm.errorf("%s: types of a and b must match", name)
	}
	x, k := floatOf(a)
	if k == notFloat {
		if !isPrimitive(a) {
			return 0, 0, k, vm.errorf("%s: values are not primitive types", name)
		}
		return 0, 0, k, vm.errorf("%s%s", name, floatTypesMsg)
	}
	y, _ := floatOf(b)
	return x, y, k, nil
}

func (vm *VM) floatBinary(name expr.Symbol, op func(a, b float64) float64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		// two Float64s, the common case, without the checks' type lookups
		if x, ok := a[0].(float64); ok {
			if y, ok := a[1].(float64); ok {
				return op(x, y), nil
			}
		}
		x, y, k, err := vm.floatArgs(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return k.value(op(x, y)), nil
	}
}

func (vm *VM) floatCompare(name expr.Symbol, cmp func(a, b float64) bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		if x, ok := a[0].(float64); ok {
			if y, ok := a[1].(float64); ok {
				return cmp(x, y), nil
			}
		}
		x, y, _, err := vm.floatArgs(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return cmp(x, y), nil
	}
}

// floatTernary is ter_fintrinsic (runtime_intrinsics.c:1316-1344) over fma:
// fma for a Float64, fmaf for the others.
func (vm *VM) floatTernary(name expr.Symbol) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		ty := vm.typeOf(a[0])
		if vm.typeOf(a[1]) != ty || vm.typeOf(a[2]) != ty {
			return nil, vm.errorf("%s: types of a, b, and c must match", name)
		}
		x, k := floatOf(a[0])
		if k == notFloat {
			if !isPrimitive(a[0]) {
				return nil, vm.errorf("%s: values are not primitive types", name)
			}
			return nil, vm.errorf("%s%s", name, floatTypesMsg)
		}
		y, _ := floatOf(a[1])
		z, _ := floatOf(a[2])
		if k == f64 {
			return math.FMA(x, y, z), nil
		}
		return k.value(widen32(fmaf(narrow64(x), narrow64(y), narrow64(z)))), nil
	}
}

// fmaf is julia_fmaf, Base.fma_emulated(::Float32, ::Float32, ::Float32)
// (runtime_intrinsics.c:1437-1447): the product is exact in a float64, and
// a sum that lands on a Float32 tie is moved off it by the lost low part.
// It rounds once, as the hardware's fmaf does.
func fmaf(a, b, c float32) float32 {
	ab := float64(a) * float64(b)
	res := ab + float64(c)
	if math.Float64bits(res)&0x1fffffff == 0x10000000 {
		var reslo float64
		if math.Abs(float64(c)) > math.Abs(ab) {
			reslo = ab - (res - float64(c))
		} else {
			reslo = float64(c) - (res - ab)
		}
		if reslo != 0 {
			res = math.Nextafter(res, math.Copysign(math.Inf(1), reslo))
		}
	}
	return float32(res)
}

// fpResize is fptrunc and fpext (runtime_intrinsics.c:1616-1665).
func (vm *VM) fpResize(name expr.Symbol, ext bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		t, err := vm.cvtTarget(name, a[0])
		if err != nil {
			return nil, err
		}
		_, isize, ok := primBits(a[1])
		if !ok {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		osize := uint(t.Name.Bits)
		if ext && osize <= isize {
			return nil, vm.errorf("fpext: output bitsize must be > input bitsize")
		}
		if !ext && osize >= isize {
			return nil, vm.errorf("fptrunc: output bitsize must be < input bitsize")
		}
		x, k := floatOf(a[1])
		switch {
		case k == notFloat:
		case t == core.Float16Type && !ext:
			return Float16(doubleToHalf(x)), nil
		case t == core.BFloat16Type && !ext:
			return BFloat16(doubleToBfloat(x)), nil
		case t == core.Float32Type && (ext || k == f64):
			return narrow64(x), nil
		case t == core.Float64Type && ext:
			return x, nil
		}
		// fpext's message names fptrunc too (runtime_intrinsics.c:1660)
		return nil, vm.errorf("fptrunc%s", floatTypesMsg)
	}
}

// intToFloat is LLVMSItoFP and LLVMUItoFP (APInt-C.cpp:389-429): the
// value's bits as an integer, APInt::roundToDouble'd, then stored in the
// target type, a Float16 or BFloat16 through a Float32.
func (vm *VM) intToFloat(name expr.Symbol, signed bool, msg string) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		t, err := vm.cvtTarget(name, a[0])
		if err != nil {
			return nil, err
		}
		x, w, ok := primBits(a[1])
		if !ok {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		val := roundToDouble(x, w, signed)
		switch t {
		case core.Float16Type:
			return Float16(floatToHalf(narrow64(val))), nil
		case core.BFloat16Type:
			return BFloat16(floatToBfloat(narrow64(val))), nil
		case core.Float32Type:
			return narrow64(val), nil
		case core.Float64Type:
			return val, nil
		}
		return nil, vm.errorf("%s", msg)
	}
}

// roundToDouble is LLVM's APInt::roundToDouble (llvm-project
// julia-18.1.7-4, llvm/lib/Support/APInt.cpp:849-903) of the w-bit value
// x, as julia's runtime answers with it: above 64 active bits it truncates
// rather than rounds, counts the exponent one too high and ORs the leading
// bit into the exponent field (sitofp(Float64, Int128(2)^100 + 1) is
// 3.802951800684688e30, uitofp(Float64, typemax(UInt128)) is NaN). The
// shifts it makes by more than 63 are undefined in C and take their count
// modulo 64 on arm64, as here.
func roundToDouble(x u128, w uint, signed bool) float64 {
	if w <= 64 || x.hi == 0 {
		if signed {
			return float64(signExtend64(x.lo, w))
		}
		return float64(x.lo)
	}
	isNeg := signed && x.shr1(w-1)
	tmp := x
	if isNeg {
		tmp = x.neg().mask(w)
	}
	n := uint(128 - tmp.leadingZeros())
	exp := uint64(n) + 1023
	var mantissa uint64
	if (n-1)/64 == 0 {
		mantissa = tmp.lo
		if n > 52 {
			mantissa >>= n - 52
		}
	} else {
		k := n % 64
		mantissa = tmp.hi<<((52-k)&63) | tmp.lo>>((11+k)&63)
	}
	var sign uint64
	if isNeg {
		sign = 1 << 63
	}
	return math.Float64frombits(sign | exp<<52 | mantissa)
}

// signExtend64 is llvm::SignExtend64 (llvm/include/llvm/Support/
// MathExtras.h:460-464); for a 128-bit APInt the shift count 64 - B wraps
// to 0 modulo 64, so the low word reads as an int64.
func signExtend64(x uint64, b uint) int64 {
	s := (64 - b) & 63
	return int64(x<<s) >> s
}

func (x u128) leadingZeros() int {
	if x.hi != 0 {
		return bits.LeadingZeros64(x.hi)
	}
	return 64 + bits.LeadingZeros64(x.lo)
}

// shr1 is whether bit i of x is set.
func (x u128) shr1(i uint) bool { return x.lshr(i).lo&1 == 1 }

// floatToInt is LLVMFPtoSI and LLVMFPtoUI (APInt-C.cpp:316-373).
func (vm *VM) floatToInt(name expr.Symbol, signed bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		t, err := vm.cvtTarget(name, a[0])
		if err != nil {
			return nil, err
		}
		if !isPrimitive(a[1]) {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		x, k := floatOf(a[1])
		if k == notFloat {
			// fptoui's message names FPtoSI too (APInt-C.cpp:327)
			return nil, vm.errorf("FPtoSI: runtime floating point intrinsics are not implemented for bit sizes other than 16, 32 and 64")
		}
		w := uint(t.Name.Bits)
		var r u128
		switch {
		case w > 64:
			r = apfloatToInt(x, w, signed)
		case signed:
			r = from64(uint64(fcvtzs(x)))
		default:
			r = from64(fcvtzu(x))
		}
		v, ok := bitsValue(t, r.mask(w))
		if !ok {
			return nil, fmt.Errorf("%w: %s to %s", ErrUnsupported, name, t.Name.Name)
		}
		return v, nil
	}
}

// fcvtzs and fcvtzu are the C casts double to int64_t and uint64_t of
// LLVMFPtoInt's fast path: undefined in C out of range, they are arm64's
// FCVTZS and FCVTZU there, which saturate and take a NaN to 0
// (fptosi(Int32, 1.0e300) is -1, the low half of typemax(Int64)).
func fcvtzs(x float64) int64 {
	switch {
	case math.IsNaN(x):
		return 0
	case x >= 0x1p63:
		return math.MaxInt64
	case x <= -0x1p63:
		return math.MinInt64
	}
	return int64(x)
}

func fcvtzu(x float64) uint64 {
	switch {
	case math.IsNaN(x) || x <= -1:
		return 0
	case x >= 0x1p64:
		return math.MaxUint64
	case x < 0:
		return 0
	}
	return uint64(x)
}

// apfloatToInt is APFloat::convertToInteger toward zero to a w-bit
// integer, above 64 bits: saturating, a NaN to 0 (fptosi(Int128, 1.0e300)
// is typemax(Int128), fptoui(UInt128, -1.0) is 0).
func apfloatToInt(x float64, w uint, signed bool) u128 {
	t := math.Trunc(x)
	top := float64(w)
	if signed {
		top--
	}
	switch {
	case math.IsNaN(x):
		return u128{}
	case t >= math.Pow(2, top):
		m := from64(1).shl(uint(top)).sub(from64(1))
		return m
	case signed && t < -math.Pow(2, top):
		return from64(1).shl(uint(top)).neg()
	case !signed && t < 0:
		return u128{}
	}
	b := math.Float64bits(math.Abs(t))
	var r u128
	if e := int(b>>52) - 1075; b != 0 {
		m := from64(b&(1<<52-1) | 1<<52)
		if e >= 0 {
			r = m.shl(uint(e))
		} else {
			r = m.lshr(uint(-e))
		}
	}
	if t < 0 {
		r = r.neg()
	}
	return r
}
