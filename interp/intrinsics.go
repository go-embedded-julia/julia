package interp

import (
	"fmt"
	"math"
	"math/bits"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The intrinsics of src/runtime_intrinsics.c over the primitive types that
// have a Go counterpart: the integers up to 128 bits, Bool, Char and the four
// floats (the float ones are floatintrinsics.go). An integer intrinsic takes
// any primitive value as its bits (a u128) and its width, so that one
// implementation serves every width, as julia's does (jl_iintrinsic_2,
// runtime_intrinsics.c:1174-1211).

func (vm *VM) bootIntrinsics() {
	im := newModule("Intrinsics", vm.Core)
	vm.modules["Intrinsics"] = im
	b := vm.Core.declare("Intrinsics", 0)
	b.Value, b.Const = im, true
	vm.intrinsics = make([]*Intrinsic, len(intrinsicNames))
	add := func(name expr.Symbol, n int, fn func(vm *VM, a []any) (any, error)) {
		x := vm.newIntrinsic(name, n, fn)
		ib := im.declare(name, 0)
		ib.Value, ib.Const, ib.Exports = x, true, true
	}
	for name, op := range map[expr.Symbol]func(a, b u128, w uint) u128{
		"add_int": func(a, b u128, w uint) u128 { return a.add(b) },
		"sub_int": func(a, b u128, w uint) u128 { return a.sub(b) },
		"mul_int": func(a, b u128, w uint) u128 { return a.mul(b) },
		"and_int": func(a, b u128, w uint) u128 { return a.and(b) },
		"or_int":  func(a, b u128, w uint) u128 { return a.or(b) },
		"xor_int": func(a, b u128, w uint) u128 { return a.xor(b) },
	} {
		add(name, 2, vm.intBinary(name, op))
	}
	for name, cmp := range map[expr.Symbol]func(a, b u128, w uint) bool{
		"eq_int":  func(a, b u128, w uint) bool { return a == b },
		"ne_int":  func(a, b u128, w uint) bool { return a != b },
		"ult_int": func(a, b u128, w uint) bool { return a.ult(b) },
		"ule_int": func(a, b u128, w uint) bool { return !b.ult(a) },
		"slt_int": func(a, b u128, w uint) bool { return a.slt(b, w) },
		"sle_int": func(a, b u128, w uint) bool { return !b.slt(a, w) },
	} {
		add(name, 2, vm.intCompare(name, cmp))
	}
	// the overflow-checked arithmetic: (result, overflowed). Below 128 bits
	// the exact result fits a u128 and overflows when it does not survive
	// being cut to w bits; at 128 the signs or the high half tell.
	fits := func(r u128, w uint) bool { return r.mask(w).sext(w) == r }
	for name, op := range map[expr.Symbol]func(a, b u128, w uint) (u128, bool){
		"checked_sadd_int": func(a, b u128, w uint) (u128, bool) {
			x, y := a.sext(w), b.sext(w)
			r := x.add(y)
			if w < 128 {
				return r, !fits(r, w)
			}
			return r, x.neg128() == y.neg128() && r.neg128() != x.neg128()
		},
		"checked_ssub_int": func(a, b u128, w uint) (u128, bool) {
			x, y := a.sext(w), b.sext(w)
			r := x.sub(y)
			if w < 128 {
				return r, !fits(r, w)
			}
			return r, x.neg128() != y.neg128() && r.neg128() != x.neg128()
		},
		"checked_smul_int": func(a, b u128, w uint) (u128, bool) {
			if w <= 64 {
				// exact in 128 bits: the signed product of the low halves
				x, y := int64(a.sext(w).lo), int64(b.sext(w).lo)
				hi, lo := bits.Mul64(uint64(x), uint64(y))
				// the unsigned product's high half, corrected for the signs
				if x < 0 {
					hi -= uint64(y)
				}
				if y < 0 {
					hi -= uint64(x)
				}
				r := u128{hi, lo}
				return r, !fits(r, w)
			}
			x, y := a.sext(w), b.sext(w)
			r := x.mul(y)
			ax, ay := x, y
			if x.neg128() {
				ax = x.neg()
			}
			if y.neg128() {
				ay = y.neg()
			}
			hi, lo := ax.mulFull(ay)
			min := from64(1).shl(127)
			negative := x.neg128() != y.neg128() && !x.isZero() && !y.isZero()
			return r, !hi.isZero() || (lo.neg128() && !(negative && lo == min))
		},
		"checked_uadd_int": func(a, b u128, w uint) (u128, bool) {
			r := a.add(b)
			if w < 128 {
				return r, !r.lshr(w).isZero()
			}
			return r, r.ult(a)
		},
		"checked_usub_int": func(a, b u128, w uint) (u128, bool) {
			return a.sub(b), a.ult(b)
		},
		"checked_umul_int": func(a, b u128, w uint) (u128, bool) {
			hi, lo := a.mulFull(b)
			if w < 128 {
				return lo, !hi.isZero() || !lo.lshr(w).isZero()
			}
			return lo, !hi.isZero()
		},
	} {
		add(name, 2, vm.intChecked(name, op))
	}
	// runtime_intrinsics.c:1569-1574, each op on a's C type
	tooFar := func(b u128, w uint) bool { return b.hi != 0 || b.lo >= uint64(w) }
	add("shl_int", 2, vm.intShift("shl_int", func(a, b u128, w uint) u128 {
		if tooFar(b, w) {
			return u128{}
		}
		return a.shl(uint(b.lo))
	}))
	add("lshr_int", 2, vm.intShift("lshr_int", func(a, b u128, w uint) u128 {
		if tooFar(b, w) {
			return u128{}
		}
		return a.lshr(uint(b.lo))
	}))
	add("ashr_int", 2, vm.intShift("ashr_int", func(a, b u128, w uint) u128 {
		x, s := a.sext(w), b.sext(w)
		if s.neg128() || tooFar(s, w) {
			return x.ashr(w - 1)
		}
		return x.ashr(uint(s.lo))
	}))
	add("neg_int", 1, vm.intUnary("neg_int", func(a u128) u128 { return a.neg() }))
	add("not_int", 1, vm.intUnary("not_int", func(a u128) u128 { return a.not() }))
	// the quotient's overflow is MIN / -1 (sdivmod is not asked then)
	isMin := func(x u128, w uint) bool { return x == from64(1).shl(w-1).sext(w) }
	minusOne := u128{^uint64(0), ^uint64(0)}
	for name, div := range map[expr.Symbol]func(a, b u128, w uint) (u128, bool){
		"checked_sdiv_int": func(a, b u128, w uint) (u128, bool) {
			x, y := a.sext(w), b.sext(w)
			if y.isZero() || (y == minusOne && isMin(x, w)) {
				return u128{}, true
			}
			q, _ := a.sdivmod(b, w)
			return q, false
		},
		"checked_srem_int": func(a, b u128, w uint) (u128, bool) {
			y := b.sext(w)
			if y.isZero() {
				return u128{}, true
			}
			if y == minusOne {
				return u128{}, false
			}
			_, r := a.sdivmod(b, w)
			return r, false
		},
		"checked_udiv_int": func(a, b u128, w uint) (u128, bool) {
			if b.isZero() {
				return u128{}, true
			}
			q, _ := a.udivmod(b)
			return q, false
		},
		"checked_urem_int": func(a, b u128, w uint) (u128, bool) {
			if b.isZero() {
				return u128{}, true
			}
			_, r := a.udivmod(b)
			return r, false
		},
	} {
		add(name, 2, vm.intCheckedDiv(name, div))
	}
	vm.bootFloatIntrinsics(add)
	add("trunc_int", 2, vm.intCvt("trunc_int", "Trunc", -1, func(b u128, from, to uint) u128 { return b.mask(to) }))
	add("zext_int", 2, vm.intCvt("zext_int", "ZExt", 1, func(b u128, from, to uint) u128 { return b.mask(from) }))
	add("sext_int", 2, vm.intCvt("sext_int", "SExt", 1, func(b u128, from, to uint) u128 { return b.sext(from).mask(to) }))
	// LLVMByteSwap, LLVMPopcount, LLVMCountl_zero and LLVMCountr_zero
	// (APInt-C.cpp), over the value's width; a count is stored in the
	// value's own type (jl_intrinsiclambda_u1, runtime_intrinsics.c:1005-1020)
	add("bswap_int", 1, vm.intUnaryW("bswap_int", func(a u128, w uint) u128 {
		return u128{bits.ReverseBytes64(a.lo), bits.ReverseBytes64(a.hi)}.lshr(128 - w)
	}))
	add("ctpop_int", 1, vm.intUnaryW("ctpop_int", func(a u128, w uint) u128 {
		return from64(uint64(bits.OnesCount64(a.hi) + bits.OnesCount64(a.lo)))
	}))
	add("ctlz_int", 1, vm.intUnaryW("ctlz_int", func(a u128, w uint) u128 {
		return from64(uint64(a.leadingZeros() - int(128-w)))
	}))
	add("cttz_int", 1, vm.intUnaryW("cttz_int", func(a u128, w uint) u128 {
		n := uint64(bits.TrailingZeros64(a.lo))
		if a.lo == 0 {
			n = 64 + uint64(bits.TrailingZeros64(a.hi))
		}
		return from64(min(n, uint64(w)))
	}))
	add("bitcast", 2, biBitcast)
	add("cglobal", 2, biCglobal)
	vm.bootAtomics(add)
	// no runtime function: jl_f_intrinsic_call refuses it (builtins.c:
	// 2371-2373), before counting the arguments
	vm.newIntrinsic("llvmcall", 0, nil).Fn = func(vm *VM, args []any) (any, error) {
		return nil, vm.errorf("`llvmcall` requires the compiler")
	}
	llvmcall := im.declare("llvmcall", 0)
	llvmcall.Value, llvmcall.Const, llvmcall.Exports = vm.intrinsics[intrinsicIDs["llvmcall"]], true, true
	add("pointerref", 3, biPointerref)
	add("pointerset", 4, biPointerset)
	add("add_ptr", 2, ptrArith("add_ptr", 1))
	add("sub_ptr", 2, ptrArith("sub_ptr", -1))
	for name, op := range map[expr.Symbol]func(a, b u128, w uint) u128{
		// flipsign (runtime_intrinsics.c:1710-1712): (b >= 0) ? a : -a
		"flipsign_int": func(a, b u128, w uint) u128 {
			if b.sext(w).neg128() {
				return a.neg()
			}
			return a
		},
		"sdiv_int": func(a, b u128, w uint) u128 {
			x, y := a.sext(w), b.sext(w)
			if y.isZero() {
				return u128{}
			}
			if y == minusOne {
				return x.neg() // MIN/-1 wraps to MIN
			}
			q, _ := a.sdivmod(b, w)
			return q
		},
		"udiv_int": func(a, b u128, w uint) u128 {
			if b.isZero() {
				return u128{}
			}
			q, _ := a.udivmod(b)
			return q
		},
		"srem_int": func(a, b u128, w uint) u128 {
			y := b.sext(w)
			if y.isZero() {
				return a
			}
			if y == minusOne {
				return u128{}
			}
			_, r := a.sdivmod(b, w)
			return r
		},
		"urem_int": func(a, b u128, w uint) u128 {
			if b.isZero() {
				return a
			}
			_, r := a.udivmod(b)
			return r
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
// msub: x/0 is 0, x%0 is x, MIN/-1 is MIN and MIN%-1 is 0; the 128-bit
// ones answer the same.

// primBits returns the bits of any primitive value this VM represents and
// its width: the integers, Bool, Char, and the floats' bit patterns.
func primBits(v any) (u128, uint, bool) {
	// the integers first: they are what the integer intrinsics are given
	if b, w, ok := intBits(v); ok {
		return b, w, true
	}
	switch x := v.(type) {
	case float64:
		return from64(math.Float64bits(x)), 64, true
	case float32:
		return from64(uint64(math.Float32bits(x))), 32, true
	case Float16:
		return from64(uint64(x)), 16, true
	case BFloat16:
		return from64(uint64(x)), 16, true
	case *Intrinsic:
		return from64(uint64(uint32(x.ID))), 32, true
	case PrimValue:
		return x.Bits, uint(x.Type.Name.Bits), true
	}
	return u128{}, 0, false
}

// bitsValue is jl_new_bits: the value of primitive type t with these bits
// (truncated to t's width), false for a primitive type without a Go
// representation here (Float16, BFloat16, a user's primitive type).
func bitsValue(t *core.DataType, bits u128) (any, bool) {
	switch t {
	case core.Int8Type:
		return int8(bits.lo), true
	case core.Int16Type:
		return int16(bits.lo), true
	case core.Int32Type:
		return int32(bits.lo), true
	case core.Int64Type:
		return int64(bits.lo), true
	case core.Int128Type:
		return Int128{bits.hi, bits.lo}, true
	case core.UInt8Type:
		return uint8(bits.lo), true
	case core.UInt16Type:
		return uint16(bits.lo), true
	case core.UInt32Type:
		return uint32(bits.lo), true
	case core.UInt64Type:
		return bits.lo, true
	case core.UInt128Type:
		return UInt128{bits.hi, bits.lo}, true
	case core.BoolType:
		return bits.lo&1 == 1, true
	case core.CharType:
		return expr.Char(bits.lo), true
	case core.Float32Type:
		return math.Float32frombits(uint32(bits.lo)), true
	case core.Float64Type:
		return math.Float64frombits(bits.lo), true
	case core.Float16Type:
		return Float16(bits.lo), true
	case core.BFloat16Type:
		return BFloat16(bits.lo), true
	}
	// a primitive type a program declares (primitive type Cstring 64 end,
	// refpointer.jl:87): its bits, to its width; pointers and intrinsics
	// have values of their own
	if t.Name.Bits > 0 && t.IsConcrete && t.Name != typeNameOf(core.PtrType) && t.Name.Name != "IntrinsicFunction" {
		return PrimValue{t, bits.mask(uint(t.Name.Bits))}, true
	}
	return nil, false
}

// PrimValue is a value of a primitive type without a Go representation of
// its own: the type and the bits.
type PrimValue struct {
	Type *core.DataType
	Bits u128
}

// cvtTarget is the target check of jl_intrinsic_cvt and jl_bitcast
// (runtime_intrinsics.c:397-399, 1034-1036).
func (vm *VM) cvtTarget(name expr.Symbol, ty any) (*core.DataType, error) {
	t, ok := ty.(*core.DataType)
	if !ok {
		return nil, vm.typeError(name, "", core.DataTypeType, ty)
	}
	if !t.IsConcrete || t.Name.Bits == 0 {
		return nil, vm.errorf("%s: target type not a leaf primitive type", name)
	}
	return t, nil
}

// intCvt is jl_intrinsic_cvt (runtime_intrinsics.c:1032-1045) for trunc_int,
// sext_int and zext_int: the value's bits resized to the target's width,
// which LLVMTrunc wants narrower (grow -1) and LLVMSExt and LLVMZExt wider
// (grow 1) (APInt-C.cpp:431-476).
func (vm *VM) intCvt(name expr.Symbol, llvm string, grow int, op func(bits u128, from, to uint) u128) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		t, err := vm.cvtTarget(name, a[0])
		if err != nil {
			return nil, err
		}
		x, w, ok := primBits(a[1])
		if !ok {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		if to := uint(t.Name.Bits); grow > 0 && to <= w {
			return nil, vm.errorf("%s: output bitsize must be > input bitsize", llvm)
		} else if grow < 0 && to >= w {
			return nil, vm.errorf("%s: output bitsize must be < input bitsize", llvm)
		}
		v, ok := bitsValue(t, op(x, w, uint(t.Name.Bits)))
		if !ok {
			return nil, fmt.Errorf("%w: %s to %s", ErrUnsupported, name, t.Name.Name)
		}
		return v, nil
	}
}

// ptrAddress is a pointer's bits: its address in the VM's address space, or
// its offset when it points at nothing (C_NULL, an integer made a pointer).
func (vm *VM) ptrAddress(p *Pointer) (uint64, error) {
	if p.Target == nil {
		return uint64(p.Off), nil
	}
	return vm.addressOf(p)
}

// biBitcast is jl_bitcast (runtime_intrinsics.c:395-409).
func biBitcast(vm *VM, a []any) (any, error) {
	t, err := vm.cvtTarget("bitcast", a[0])
	if err != nil {
		return nil, err
	}
	// pointers without host addresses (vptr.go, addrspace.go): a pointer
	// retyped keeps what it points into; a pointer made an integer is its
	// address in the VM's address space; an integer made a pointer points
	// into the target whose region holds it, or at nothing (C_NULL)
	isPtr := t.Name == core.PtrType.Body.(*core.DataType).Name
	if p, ok := a[1].(*Pointer); ok {
		if isPtr {
			return &Pointer{Type: t, Target: p.Target, Off: p.Off}, nil
		}
		if t.Name.Bits != 64 {
			return nil, vm.errorf("bitcast: argument size does not match size of target type")
		}
		addr, err := vm.ptrAddress(p)
		if err != nil {
			return nil, err
		}
		v, _ := bitsValue(t, from64(addr))
		return v, nil
	}
	if isPtr {
		x, w, ok := primBits(a[1])
		if !ok || w != 64 {
			return nil, vm.errorf("bitcast: argument size does not match size of target type")
		}
		if target, off, ok := vm.targetAt(x.lo); ok {
			return &Pointer{Type: t, Target: target, Off: off}, nil
		}
		return &Pointer{Type: t, Off: int64(x.lo)}, nil
	}
	x, w, ok := primBits(a[1])
	if !ok {
		return nil, vm.errorf("bitcast: value not a primitive type")
	}
	if int(w) != t.Name.Bits {
		return nil, vm.errorf("bitcast: argument size does not match size of target type")
	}
	if vm.typeOf(a[1]) == any(t) {
		return a[1], nil
	}
	if t == vm.intrinsicType {
		return vm.intrinsicOf(int32(x.lo)), nil
	}
	v, ok := bitsValue(t, x)
	if !ok {
		return nil, fmt.Errorf("%w: bitcast to %s", ErrUnsupported, t.Name.Name)
	}
	return v, nil
}

// intBits returns the bits of an integer-like primitive value and its width.
func intBits(v any) (u128, uint, bool) {
	switch x := v.(type) {
	case int8:
		return from64(uint64(uint8(x))), 8, true
	case int16:
		return from64(uint64(uint16(x))), 16, true
	case int32:
		return from64(uint64(uint32(x))), 32, true
	case int64:
		return from64(uint64(x)), 64, true
	case Int128:
		return u128{x.Hi, x.Lo}, 128, true
	case uint8:
		return from64(uint64(x)), 8, true
	case uint16:
		return from64(uint64(x)), 16, true
	case uint32:
		return from64(uint64(x)), 32, true
	case uint64:
		return from64(x), 64, true
	case UInt128:
		return u128{x.Hi, x.Lo}, 128, true
	case bool:
		if x {
			return from64(1), 8, true
		}
		return u128{}, 8, true
	case expr.Char:
		return from64(uint64(x)), 32, true
	}
	return u128{}, 0, false
}

// fromBits is jl_new_bits for the type of like: the bits truncated to its
// width; a Bool keeps the low bit (jl_new_bits returns jl_true or jl_false).
func fromBits(like any, bits u128) any {
	if _, ok := like.(int64); ok {
		return int64(bits.lo)
	}
	switch like.(type) {
	case int8:
		return int8(bits.lo)
	case int16:
		return int16(bits.lo)
	case int32:
		return int32(bits.lo)
	case Int128:
		return Int128{bits.hi, bits.lo}
	case uint8:
		return uint8(bits.lo)
	case uint16:
		return uint16(bits.lo)
	case uint32:
		return uint32(bits.lo)
	case UInt128:
		return UInt128{bits.hi, bits.lo}
	case bool:
		return bits.lo&1 == 1
	case expr.Char:
		return expr.Char(bits.lo)
	case float32:
		return math.Float32frombits(uint32(bits.lo))
	case float64:
		return math.Float64frombits(bits.lo)
	case Float16:
		return Float16(bits.lo)
	case BFloat16:
		return BFloat16(bits.lo)
	}
	return bits.lo
}

// sameIntTypes is the check of jl_iintrinsic_2 without cvtb.
func (vm *VM) sameIntTypes(name expr.Symbol, a, b any) (u128, u128, uint, error) {
	// two Int64s, the common case, without typeOf's search of its cases
	if x, ok := a.(int64); ok {
		if y, ok := b.(int64); ok {
			return from64(uint64(x)), from64(uint64(y)), 64, nil
		}
	}
	if vm.typeOf(a) != vm.typeOf(b) {
		return u128{}, u128{}, 0, vm.errorf("%s: types of a and b must match", name)
	}
	// intBits first, without primBits' call, on the hot path of integers
	x, w, ok := intBits(a)
	if !ok {
		if x, w, ok = primBits(a); !ok {
			return u128{}, u128{}, 0, vm.errorf("%s: a is not a primitive type", name)
		}
	}
	y, _, ok := intBits(b)
	if !ok {
		y, _, _ = primBits(b)
	}
	return x, y, w, nil
}

func (vm *VM) intBinary(name expr.Symbol, op func(a, b u128, w uint) u128) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return fromBits(a[0], op(x, y, w).mask(w)), nil
	}
}

// intChecked is jl_iintrinsic_2 over jl_intrinsiclambda_checked
// (runtime_intrinsics.c:1228-1244): the wrapped result and whether it
// overflowed, as a Tuple{T, Bool}.
func (vm *VM) intChecked(name expr.Symbol, op func(a, b u128, w uint) (u128, bool)) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		r, ovf := op(x, y, w)
		return vm.newTuple([]any{fromBits(a[0], r.mask(w)), ovf}), nil
	}
}

// intShift is jl_iintrinsic_2 with cvtb set (runtime_intrinsics.c:1174-1211):
// b may be of another primitive type, zero-extended or truncated to a's
// width before the op sees it.
func (vm *VM) intShift(name expr.Symbol, op func(a, b u128, w uint) u128) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		y, wb, okb := primBits(a[1])
		if vm.typeOf(a[0]) != vm.typeOf(a[1]) && !okb {
			return nil, vm.errorf("%s: b is not a primitive type", name)
		}
		x, w, ok := primBits(a[0])
		if !ok {
			return nil, vm.errorf("%s: a is not a primitive type", name)
		}
		if wb > w {
			y = y.mask(w)
		}
		return fromBits(a[0], op(x, y, w).mask(w)), nil
	}
}

func (vm *VM) intCompare(name expr.Symbol, cmp func(a, b u128, w uint) bool) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		return cmp(x, y, w), nil
	}
}

func (vm *VM) intUnary(name expr.Symbol, op func(a u128) u128) func(*VM, []any) (any, error) {
	return vm.intUnaryW(name, func(a u128, _ uint) u128 { return op(a) })
}

// intUnaryW is jl_iintrinsic_1 (runtime_intrinsics.c:966-995) over any
// primitive value, the op told its width.
func (vm *VM) intUnaryW(name expr.Symbol, op func(a u128, w uint) u128) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, w, ok := primBits(a[0])
		if !ok {
			return nil, vm.errorf("%s: value is not a primitive type", name)
		}
		return fromBits(a[0], op(x, w).mask(w)), nil
	}
}

// intCheckedDiv is jl_intrinsiclambda_checkeddiv (runtime_intrinsics.c:
// 1245-1253): DivideError on a zero divisor or an overflowing quotient.
func (vm *VM) intCheckedDiv(name expr.Symbol, div func(a, b u128, w uint) (u128, bool)) func(*VM, []any) (any, error) {
	return func(vm *VM, a []any) (any, error) {
		x, y, w, err := vm.sameIntTypes(name, a[0], a[1])
		if err != nil {
			return nil, err
		}
		r, ovf := div(x, y, w)
		if ovf {
			return nil, vm.exception(vm.tDivide)
		}
		return fromBits(a[0], r.mask(w)), nil
	}
}
