package interp

import (
	"math"

	"github.com/go-embedded-julia/julia/expr"
)

// The intrinsics of src/runtime_intrinsics.c over the primitive types that
// have a Go counterpart: the integers up to 64 bits, Bool, Char, Float32 and
// Float64. An integer is handled as its bits in a uint64 and its width, so
// that one implementation serves every width, as julia's does
// (jl_iintrinsic_2, runtime_intrinsics.c:1174-1211).

func (vm *VM) bootIntrinsics() {
	im := newModule("Intrinsics", vm.Core)
	vm.modules["Intrinsics"] = im
	b := vm.Core.declare("Intrinsics", 0)
	b.Value, b.Const = im, true
	add := func(name expr.Symbol, n int, fn func(vm *VM, a []any) (any, error)) {
		x := &Intrinsic{Name: name, Fn: func(vm *VM, args []any) (any, error) {
			// jl_f_intrinsic_call: JL_NARGS(intrinsic_call, n, n)
			if err := vm.nargs("intrinsic_call", args, n, n); err != nil {
				return nil, err
			}
			return fn(vm, args)
		}}
		ib := im.declare(name, 0)
		ib.Value, ib.Const, ib.Exports = x, true, true
	}
	for name, op := range map[expr.Symbol]func(a, b uint64, w uint) uint64{
		"add_int": func(a, b uint64, w uint) uint64 { return a + b },
		"sub_int": func(a, b uint64, w uint) uint64 { return a - b },
		"mul_int": func(a, b uint64, w uint) uint64 { return a * b },
		"and_int": func(a, b uint64, w uint) uint64 { return a & b },
		"or_int":  func(a, b uint64, w uint) uint64 { return a | b },
		"xor_int": func(a, b uint64, w uint) uint64 { return a ^ b },
	} {
		add(name, 2, vm.intBinary(name, op))
	}
	for name, cmp := range map[expr.Symbol]func(a, b uint64, w uint) bool{
		"eq_int":  func(a, b uint64, w uint) bool { return a == b },
		"ne_int":  func(a, b uint64, w uint) bool { return a != b },
		"ult_int": func(a, b uint64, w uint) bool { return a < b },
		"ule_int": func(a, b uint64, w uint) bool { return a <= b },
		"slt_int": func(a, b uint64, w uint) bool { return signExtend(a, w) < signExtend(b, w) },
		"sle_int": func(a, b uint64, w uint) bool { return signExtend(a, w) <= signExtend(b, w) },
	} {
		add(name, 2, vm.intCompare(name, cmp))
	}
	add("neg_int", 1, vm.intUnary("neg_int", func(a uint64) uint64 { return -a }))
	add("not_int", 1, vm.intUnary("not_int", func(a uint64) uint64 { return ^a }))
	for name, div := range map[expr.Symbol]func(a, b uint64, w uint) (uint64, bool){
		"checked_sdiv_int": func(a, b uint64, w uint) (uint64, bool) {
			x, y := signExtend(a, w), signExtend(b, w)
			if y == 0 || (y == -1 && x == math.MinInt64>>(64-w)) {
				return 0, true
			}
			return uint64(x / y), false
		},
		"checked_srem_int": func(a, b uint64, w uint) (uint64, bool) {
			x, y := signExtend(a, w), signExtend(b, w)
			if y == 0 {
				return 0, true
			}
			if y == -1 {
				return 0, false
			}
			return uint64(x % y), false
		},
		"checked_udiv_int": func(a, b uint64, w uint) (uint64, bool) {
			if b == 0 {
				return 0, true
			}
			return a / b, false
		},
		"checked_urem_int": func(a, b uint64, w uint) (uint64, bool) {
			if b == 0 {
				return 0, true
			}
			return a % b, false
		},
	} {
		add(name, 2, vm.intCheckedDiv(name, div))
	}
	for name, op := range map[expr.Symbol]func(a, b float64) float64{
		"add_float": func(a, b float64) float64 { return a + b },
		"sub_float": func(a, b float64) float64 { return a - b },
		"mul_float": func(a, b float64) float64 { return a * b },
		"div_float": func(a, b float64) float64 { return a / b },
	} {
		add(name, 2, vm.floatBinary(name, op))
	}
	for name, cmp := range map[expr.Symbol]func(a, b float64) bool{
		"eq_float": func(a, b float64) bool { return a == b },
		"ne_float": func(a, b float64) bool { return a != b },
		"lt_float": func(a, b float64) bool { return a < b },
		"le_float": func(a, b float64) bool { return a <= b },
	} {
		add(name, 2, vm.floatCompare(name, cmp))
	}
	// jl_fintrinsic_1 (runtime_intrinsics.c:1068-1092), reached through
	// un_fintrinsic's jl_neg_float_withtype, whose name the messages carry
	// (:1059-1064)
	add("neg_float", 1, func(vm *VM, a []any) (any, error) {
		switch x := a[0].(type) {
		case float64:
			return -x, nil
		case float32:
			return -x, nil
		}
		if _, _, ok := intBits(a[0]); !ok {
			return nil, vm.errorf("neg_float_withtype: value is not a primitive type")
		}
		return nil, vm.errorf("neg_float_withtype%s", floatTypesMsg)
	})
	for name, op := range map[expr.Symbol]func(a, b uint64, w uint) uint64{
		"sdiv_int": func(a, b uint64, w uint) uint64 {
			x, y := signExtend(a, w), signExtend(b, w)
			if y == 0 {
				return 0
			}
			if y == -1 {
				return uint64(-x) // MIN/-1 wraps to MIN
			}
			return uint64(x / y)
		},
		"udiv_int": func(a, b uint64, w uint) uint64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"srem_int": func(a, b uint64, w uint) uint64 {
			x, y := signExtend(a, w), signExtend(b, w)
			if y == 0 {
				return a
			}
			if y == -1 {
				return 0
			}
			return uint64(x % y)
		},
		"urem_int": func(a, b uint64, w uint) uint64 {
			if b == 0 {
				return a
			}
			return a % b
		},
	} {
		add(name, 2, vm.intBinary(name, op))
	}
}

// The plain sdiv_int, udiv_int, srem_int and urem_int of 8 to 64 bits are C's
// `a / b` and `a % b` in the runtime (bi_iintrinsic_fast,
// runtime_intrinsics.c:1105-1119, 1356-1361), and a zero divisor or MIN/-1
// is undefined behaviour in C. julia answers as the hardware does: x86_64
// traps and julia raises DivideError, arm64 does not trap. The reference
// julia of the goldens runs on arm64, so these follow arm64's sdiv, udiv and
// msub: x/0 is 0, x%0 is x, MIN/-1 is MIN and MIN%-1 is 0.

// intBits returns the bits of an integer-like primitive value and its width.
func intBits(v any) (uint64, uint, bool) {
	switch x := v.(type) {
	case int8:
		return uint64(uint8(x)), 8, true
	case int16:
		return uint64(uint16(x)), 16, true
	case int32:
		return uint64(uint32(x)), 32, true
	case int64:
		return uint64(x), 64, true
	case uint8:
		return uint64(x), 8, true
	case uint16:
		return uint64(x), 16, true
	case uint32:
		return uint64(x), 32, true
	case uint64:
		return x, 64, true
	case bool:
		if x {
			return 1, 8, true
		}
		return 0, 8, true
	case expr.Char:
		return uint64(x), 32, true
	}
	return 0, 0, false
}

// fromBits is jl_new_bits for the type of like: the bits truncated to its
// width; a Bool keeps the low bit (jl_new_bits returns jl_true or jl_false).
func fromBits(like any, bits uint64) any {
	switch like.(type) {
	case int8:
		return int8(bits)
	case int16:
		return int16(bits)
	case int32:
		return int32(bits)
	case int64:
		return int64(bits)
	case uint8:
		return uint8(bits)
	case uint16:
		return uint16(bits)
	case uint32:
		return uint32(bits)
	case bool:
		return bits&1 == 1
	case expr.Char:
		return expr.Char(bits)
	}
	return bits
}

func signExtend(a uint64, w uint) int64 { return int64(a<<(64-w)) >> (64 - w) }

// sameIntTypes is the check of jl_iintrinsic_2 without cvtb.
func (vm *VM) sameIntTypes(name expr.Symbol, a, b any) (uint64, uint64, uint, error) {
	if vm.typeOf(a) != vm.typeOf(b) {
		return 0, 0, 0, vm.errorf("%s: types of a and b must match", name)
	}
	x, w, ok := intBits(a)
	if !ok {
		return 0, 0, 0, vm.errorf("%s: a is not a primitive type", name)
	}
	y, _, _ := intBits(b)
	return x, y, w, nil
}

func (vm *VM) intBinary(name expr.Symbol, op func(a, b uint64, w uint) uint64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return fromBits(a[0], op(x, y, w)), nil
	}
}

func (vm *VM) intCompare(name expr.Symbol, cmp func(a, b uint64, w uint) bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return cmp(x, y, w), nil
	}
}

func (vm *VM) intUnary(name expr.Symbol, op func(a uint64) uint64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, _, ok := intBits(a[0])
		if !ok {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		return fromBits(a[0], op(x)), nil
	}
}

// intCheckedDiv is jl_intrinsiclambda_checkeddiv (runtime_intrinsics.c:
// 1245-1253): DivideError on a zero divisor or an overflowing quotient.
func (vm *VM) intCheckedDiv(name expr.Symbol, div func(a, b uint64, w uint) (uint64, bool)) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		r, ovf := div(x, y, w)
		if ovf {
			return nil, vm.exception(vm.tDivide)
		}
		return fromBits(a[0], r), nil
	}
}

const floatTypesMsg = ": runtime floating point intrinsics require both arguments to be Float16, BFloat16, Float32, or Float64"

// floatArgs is the check of bi_fintrinsic (runtime_intrinsics.c:1262-1284).
func (vm *VM) floatArgs(name expr.Symbol, a, b any) (float64, float64, bool, error) {
	if vm.typeOf(a) != vm.typeOf(b) {
		return 0, 0, false, vm.errorf("%s: types of a and b must match", name)
	}
	switch x := a.(type) {
	case float64:
		return x, b.(float64), false, nil
	case float32:
		return float64(x), float64(b.(float32)), true, nil
	}
	if _, _, ok := intBits(a); !ok {
		return 0, 0, false, vm.errorf("%s: values are not primitive types", name)
	}
	return 0, 0, false, vm.errorf("%s%s", name, floatTypesMsg)
}

func (vm *VM) floatBinary(name expr.Symbol, op func(a, b float64) float64) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, is32, err := vm.floatArgs(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		if is32 {
			// a Float32 operation rounds once, in single precision
			return float32(op(float64(float32(x)), float64(float32(y)))), nil
		}
		return op(x, y), nil
	}
}

func (vm *VM) floatCompare(name expr.Symbol, cmp func(a, b float64) bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, _, err := vm.floatArgs(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return cmp(x, y), nil
	}
}
