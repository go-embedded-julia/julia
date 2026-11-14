package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The builtins that write a module's global and read it in one step:
// swapglobal!, modifyglobal!, replaceglobal! and setglobalonce!
// (builtins.c:1416-1511), over jl_checked_swap, _modify, _replace and
// _assignonce (module.c:1909-1945). A global is always atomic: a write
// without an order is a release store, and not_atomic is refused. With one
// thread each compare-and-swap is one read and one write.

// globalOp parses the orders of one of them, args[k:] (the success order,
// then, with failure, the failure order), then checks the module and the
// name, in julia's order. load is whether the success order also loads.
func (vm *VM) globalOp(fname expr.Symbol, args []any, k int, load, failure bool) (*Module, expr.Symbol, error) {
	success := expr.Symbol("release")
	if len(args) > k {
		var err error
		if success, err = vm.atomicOrder(fname, args[k], load, true); err != nil {
			return nil, "", err
		}
	}
	fail := success
	if failure {
		if len(args) > k+1 {
			var err error
			if fail, err = vm.atomicOrder(fname, args[k+1], true, false); err != nil {
				return nil, "", err
			}
		}
		if orderRank[fail] > orderRank[success] {
			return nil, "", vm.atomicError("invalid atomic ordering")
		}
	}
	m, s, err := vm.moduleSymbol(fname, args)
	if err != nil {
		return nil, "", err
	}
	if success == "not_atomic" {
		return nil, "", vm.atomicError(fmt.Sprintf("%s: module binding cannot be written non-atomically", fname))
	}
	if fail == "not_atomic" {
		return nil, "", vm.atomicError(fmt.Sprintf("%s: module binding cannot be accessed non-atomically", fname))
	}
	return m, s, nil
}

// globalType is the type a global's values must have, Any undeclared.
func globalType(b *Binding) any {
	if b.Type != nil {
		return b.Type
	}
	return core.AnyType
}

// biSwapglobal is jl_f_swapglobal over jl_checked_swap: the old value, the
// new one stored, an UndefVarError after the store for a global that had
// none.
func biSwapglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("swapglobal!", args, 3, 4); err != nil {
		return nil, err
	}
	m, s, err := vm.globalOp("swapglobal!", args, 3, true, false)
	if err != nil {
		return nil, err
	}
	b, err := vm.writableGlobal(m, s)
	if err != nil {
		return nil, err
	}
	if err := vm.checkGlobalValue("swapglobal!", m, s, b, args[2]); err != nil {
		return nil, err
	}
	old := b.Value
	b.Value = args[2]
	if old == undef {
		return nil, vm.undefVar(s, vm.taskWorld, m)
	}
	return old, nil
}

// biModifyglobal is jl_f_modifyglobal over jl_checked_modify and
// modify_value (datatype.c:1988-2021): op(old, rhs) stored, old => new
// returned as a Pair of the global's type.
func biModifyglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("modifyglobal!", args, 4, 5); err != nil {
		return nil, err
	}
	m, s, err := vm.globalOp("modifyglobal!", args, 4, true, false)
	if err != nil {
		return nil, err
	}
	b, err := vm.writableGlobal(m, s)
	if err != nil {
		return nil, err
	}
	old := b.Value
	if old == undef {
		return nil, vm.undefVar(s, vm.taskWorld, m)
	}
	y, err := vm.apply(args[2], []any{old, args[3]})
	if err != nil {
		return nil, err
	}
	if err := vm.checkGlobalValue("modifyglobal!", m, s, b, y); err != nil {
		return nil, err
	}
	b.Value = y
	return vm.modifyResult("modifyglobal!", globalType(b), old, y)
}

// biReplaceglobal is jl_f_replaceglobal over jl_checked_replace and
// replace_value (datatype.c:2128-2150): rhs stored when the global is egal
// to expected, and (old = …, success = …) returned.
func biReplaceglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("replaceglobal!", args, 4, 6); err != nil {
		return nil, err
	}
	m, s, err := vm.globalOp("replaceglobal!", args, 4, true, true)
	if err != nil {
		return nil, err
	}
	b, err := vm.writableGlobal(m, s)
	if err != nil {
		return nil, err
	}
	if err := vm.checkGlobalValue("replaceglobal!", m, s, b, args[3]); err != nil {
		return nil, err
	}
	old := b.Value
	if old == undef {
		return nil, vm.undefVar(s, vm.taskWorld, m)
	}
	success := vm.egal(old, args[2])
	if success {
		b.Value = args[3]
	}
	return vm.cmpswapResult(globalType(b), old, success)
}

// biSetglobalonce is jl_f_setglobalonce over jl_checked_assignonce: rhs
// stored only into a global without a value, and whether it was.
func biSetglobalonce(vm *VM, args []any) (any, error) {
	if err := vm.nargs("setglobalonce!", args, 3, 5); err != nil {
		return nil, err
	}
	m, s, err := vm.globalOp("setglobalonce!", args, 3, true, true)
	if err != nil {
		return nil, err
	}
	b, err := vm.writableGlobal(m, s)
	if err != nil {
		return nil, err
	}
	if err := vm.checkGlobalValue("setglobalonce!", m, s, b, args[2]); err != nil {
		return nil, err
	}
	if b.Value != undef {
		return false, nil
	}
	b.Value = args[2]
	return true, nil
}
