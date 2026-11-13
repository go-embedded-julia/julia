package interp

import (
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// llvmcall without LLVM. Base writes Threads.Atomic's operations and
// atomic_fence as LLVM IR (atomics.jl:360-468), one template per operation
// and element type. The interpreter recognizes those templates by their
// instruction and performs the operation on the value the pointer reaches
// (an Atomic's field). There is one thread, so every ordering is met. Any
// other IR still requires the compiler, as julia's interpreter says.

// llvmRMW maps an atomicrmw operation to the intrinsics that compute the
// new value from the old one and the operand; xchg, nand and the
// comparisons are handled apart.
var llvmRMW = map[string]string{"add": "add_int", "sub": "sub_int", "and": "and_int", "or": "or_int", "xor": "xor_int"}

// llvmcall is Core.Intrinsics.llvmcall(ir, rettype, argtypes, args...).
func (vm *VM) llvmcall(args []any) (any, error) {
	ir, _ := args[0].(string)
	refuse := func() (any, error) { return nil, vm.errorf("`llvmcall` requires the compiler") }
	if strings.Contains(ir, "fence seq_cst") && !strings.Contains(ir, "%0") {
		return nil, nil
	}
	if len(args) < 4 {
		return refuse()
	}
	p, ok := args[3].(*Pointer)
	if !ok {
		return refuse()
	}
	op := func(name string, a, b any) (any, error) {
		return vm.intrinsics[intrinsicIDs[expr.Symbol(name)]].Fn(vm, []any{a, b})
	}
	switch {
	case strings.Contains(ir, "load atomic") && len(args) == 4:
		return vm.ptrLoad("llvmcall", p, 1)
	case strings.Contains(ir, "store atomic") && len(args) == 5:
		return nil, vm.ptrStore("llvmcall", p, args[4], 1)
	case strings.Contains(ir, "cmpxchg") && len(args) == 6:
		old, err := vm.ptrLoad("llvmcall", p, 1)
		if err != nil {
			return nil, err
		}
		if vm.egal(old, args[4]) {
			if err := vm.ptrStore("llvmcall", p, args[5], 1); err != nil {
				return nil, err
			}
		}
		return old, nil
	case strings.Contains(ir, "atomicrmw ") && len(args) == 5:
		name := strings.Fields(ir[strings.Index(ir, "atomicrmw ")+len("atomicrmw "):])[0]
		old, err := vm.ptrLoad("llvmcall", p, 1)
		if err != nil {
			return nil, err
		}
		v := args[4]
		var nv any
		switch name {
		case "xchg":
			nv = v
		case "nand":
			if nv, err = op("and_int", old, v); err == nil {
				nv, err = vm.intrinsics[intrinsicIDs["not_int"]].Fn(vm, []any{nv})
			}
		case "max", "min", "umax", "umin":
			cmp := "slt_int"
			if strings.HasPrefix(name, "u") {
				cmp = "ult_int"
			}
			// max takes v when old < v, min when v < old
			a, b := old, v
			if strings.HasSuffix(name, "min") {
				a, b = v, old
			}
			var less any
			less, err = op(cmp, a, b)
			nv = old
			if less == any(true) {
				nv = v
			}
		default:
			fn, known := llvmRMW[name]
			if !known {
				return refuse()
			}
			nv, err = op(fn, old, v)
		}
		if err != nil {
			return nil, err
		}
		return old, vm.ptrStore("llvmcall", p, nv, 1)
	}
	return refuse()
}
