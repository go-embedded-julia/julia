package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// intrinsicNames is enum intrinsic (intrinsics.h:3-121) in order: an
// IntrinsicFunction is a 32-bit primitive value, its index here
// (reinterpret(Int32, have_fma) is 91). An alias has its own number.
var intrinsicNames = []expr.Symbol{
	"bitcast",
	"neg_int", "add_int", "sub_int", "mul_int", "sdiv_int", "udiv_int", "srem_int", "urem_int",
	"neg_float", "add_float", "sub_float", "mul_float", "div_float", "min_float", "max_float",
	"fma_float", "muladd_float",
	"neg_float_fast", "add_float_fast", "sub_float_fast", "mul_float_fast", "div_float_fast",
	"min_float_fast", "max_float_fast",
	"eq_int", "ne_int", "slt_int", "ult_int", "sle_int", "ule_int",
	"eq_float", "ne_float", "lt_float", "le_float",
	"eq_float_fast", "ne_float_fast", "lt_float_fast", "le_float_fast", "fpiseq",
	"and_int", "or_int", "xor_int", "not_int", "shl_int", "lshr_int", "ashr_int",
	"bswap_int", "ctpop_int", "ctlz_int", "cttz_int",
	"sext_int", "zext_int", "trunc_int", "fptoui", "fptosi", "uitofp", "sitofp", "fptrunc", "fpext",
	"checked_sadd_int", "checked_uadd_int", "checked_ssub_int", "checked_usub_int",
	"checked_smul_int", "checked_umul_int", "checked_sdiv_int", "checked_udiv_int",
	"checked_srem_int", "checked_urem_int",
	"abs_float", "copysign_float", "flipsign_int", "ceil_llvm", "floor_llvm", "trunc_llvm",
	"rint_llvm", "sqrt_llvm", "sqrt_llvm_fast",
	"add_ptr", "sub_ptr", "pointerref", "pointerset",
	"atomic_fence", "atomic_pointerref", "atomic_pointerset", "atomic_pointerswap",
	"atomic_pointermodify", "atomic_pointerreplace",
	"cglobal", "llvmcall", "have_fma",
	"cglobal_auto",
}

var intrinsicIDs = func() map[expr.Symbol]int32 {
	m := make(map[expr.Symbol]int32, len(intrinsicNames))
	for i, n := range intrinsicNames {
		m[n] = int32(i)
	}
	return m
}()

// newIntrinsic is the intrinsic julia numbers name, taking n arguments,
// recorded by its number. A name julia has no intrinsic for is a defect
// of the VM, found at boot.
func (vm *VM) newIntrinsic(name expr.Symbol, n int, fn func(vm *VM, a []any) (any, error)) *Intrinsic {
	id, ok := intrinsicIDs[name]
	if !ok {
		panic("interp: " + string(name) + " is not one of julia's intrinsics")
	}
	x := &Intrinsic{Name: name, ID: id, Fn: func(vm *VM, args []any) (any, error) {
		// jl_f_intrinsic_call: JL_NARGS(intrinsic_call, n, n)
		if err := vm.nargs("intrinsic_call", args, n, n); err != nil {
			return nil, err
		}
		return fn(vm, args)
	}}
	vm.intrinsics[id] = x
	return x
}

// intrinsicOf is the IntrinsicFunction numbered id: the VM's intrinsic, or
// for a number no intrinsic has one that shows as invalid and refuses to
// be called (julia reads past its tables there).
func (vm *VM) intrinsicOf(id int32) *Intrinsic {
	if id >= 0 && int(id) < len(vm.intrinsics) && vm.intrinsics[id] != nil {
		return vm.intrinsics[id]
	}
	return &Intrinsic{Name: "invalid", ID: id, Fn: func(vm *VM, args []any) (any, error) {
		return nil, fmt.Errorf("%w: calling the invalid intrinsic %d", ErrUnsupported, id)
	}}
}
