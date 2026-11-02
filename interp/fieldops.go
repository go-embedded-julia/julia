package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The builtins that write a field and read it in one step: swapfield!,
// modifyfield!, replacefield! and setfieldonce! (builtins.c:1081-1177),
// over swap_, modify_, replace_ and set_nth_fieldonce (datatype.c:1964-
// 2286). There is one thread here, so each is the plain read and write its
// atomic loop reduces to when nothing else writes; the checks, their order
// and the values returned are julia's.

// writableField is get_checked_fieldindex with mutabl set (builtins.c:994-
// 1021): the value's mutability, the field, its constness.
func (vm *VM) writableField(fname expr.Symbol, v, arg any) (*core.DataType, int, error) {
	var t *core.DataType
	n := 0
	switch x := v.(type) {
	case *Module:
		return nil, 0, vm.errorf("cannot assign variables in other modules")
	case *Struct:
		t, n = x.Type, len(x.Fields)
	default:
		t = vm.typeOf(v).(*core.DataType)
		n = len(t.FieldNames())
	}
	if !t.Name.Mutable {
		return nil, 0, vm.errorf("%s: immutable struct of type %s cannot be changed", fname, t.Name.Name)
	}
	i, err := vm.fieldIndex(fname, t, t.FieldNames(), n, v, arg)
	if err != nil {
		return nil, 0, err
	}
	if fieldMarked(t.Name.ConstFields, i) {
		return nil, 0, vm.errorf("%s: const field .%s of type %s cannot be changed", fname, t.FieldNames()[i], t.Name.Name)
	}
	return t, i, nil
}

// fieldOrderSuits is the check each of them makes of an order against the
// field: isatomic == (order == notatomic) is the mismatch (builtins.c:
// 1068-1071), what being "written" or "accessed".
func (vm *VM) fieldOrderSuits(fname expr.Symbol, t *core.DataType, i int, order expr.Symbol, what string) error {
	isatomic := fieldMarked(t.Name.AtomicFields, i)
	if isatomic != (order == "not_atomic") {
		return nil
	}
	if isatomic {
		return vm.atomicError(fmt.Sprintf("%s: atomic field cannot be %s non-atomically", fname, what))
	}
	return vm.atomicError(fmt.Sprintf("%s: non-atomic field cannot be %s atomically", fname, what))
}

// orderRank is jl_memory_order's value (julia_atomics.h:46-57), for the
// failure order that may not be stronger than the success order.
var orderRank = map[expr.Symbol]int{"not_atomic": 0, "unordered": 1, "monotonic": 2, "acquire": 4,
	"release": 5, "acquire_release": 6, "sequentially_consistent": 7}

// fieldOp parses the orders of one of them, args[k:] (the success order,
// then for replacefield! and setfieldonce! the failure order), checks the
// value and the field, and gives the struct, the field and its type.
func (vm *VM) fieldOp(fname expr.Symbol, args []any, k int, failure bool) (*Struct, int, any, error) {
	success := expr.Symbol("not_atomic")
	if len(args) > k {
		var err error
		if success, err = vm.atomicOrder(fname, args[k], true, true); err != nil {
			return nil, 0, nil, err
		}
	}
	fail := success
	if failure {
		if len(args) > k+1 {
			var err error
			if fail, err = vm.atomicOrder(fname, args[k+1], true, false); err != nil {
				return nil, 0, nil, err
			}
		}
		if orderRank[fail] > orderRank[success] {
			return nil, 0, nil, vm.atomicError("invalid atomic ordering")
		}
	}
	t, i, err := vm.writableField(fname, args[0], args[1])
	if err != nil {
		return nil, 0, nil, err
	}
	if err := vm.fieldOrderSuits(fname, t, i, success, "written"); err != nil {
		return nil, 0, nil, err
	}
	if failure {
		if err := vm.fieldOrderSuits(fname, t, i, fail, "accessed"); err != nil {
			return nil, 0, nil, err
		}
	}
	s, ok := args[0].(*Struct)
	if !ok {
		return nil, 0, nil, fmt.Errorf("%w: %s of a %s's %s", ErrUnsupported, fname, t.Name.Name, t.FieldNames()[i])
	}
	var ft any = core.AnyType
	if fts, ok := t.FieldTypes(); ok {
		ft = fts[i]
	}
	return s, i, ft, nil
}

// biSwapfield is jl_f_swapfield: the old value, the new one stored.
func biSwapfield(vm *VM, args []any) (any, error) {
	if err := vm.nargs("swapfield!", args, 3, 4); err != nil {
		return nil, err
	}
	s, i, ft, err := vm.fieldOp("swapfield!", args, 3, false)
	if err != nil {
		return nil, err
	}
	if !vm.isa(args[2], ft) {
		return nil, vm.typeError("swapfield!", "", ft, args[2])
	}
	old := s.Fields[i]
	s.Fields[i] = args[2]
	syncArgs(s)
	if old == undef {
		// the exchange is done before C sees the field was undefined
		return nil, vm.exception(vm.tUndefRef)
	}
	return old, nil
}

// biModifyfield is jl_f_modifyfield over modify_value (datatype.c:1988-
// 2021): op(old, rhs) stored, old => new returned as a Pair{T, T} of the
// field's type.
func biModifyfield(vm *VM, args []any) (any, error) {
	if err := vm.nargs("modifyfield!", args, 4, 5); err != nil {
		return nil, err
	}
	s, i, ft, err := vm.fieldOp("modifyfield!", args, 4, false)
	if err != nil {
		return nil, err
	}
	old := s.Fields[i]
	if old == undef {
		return nil, vm.exception(vm.tUndefRef)
	}
	y, err := vm.apply(args[2], []any{old, args[3]})
	if err != nil {
		return nil, err
	}
	if !vm.isa(y, ft) {
		return nil, vm.typeError("modifyfield!", "", ft, y)
	}
	s.Fields[i] = y
	syncArgs(s)
	return vm.modifyResult("modifyfield!", ft, old, y)
}

// modifyResult is old => new as a Pair{t, t}, jl_apply_modify_type's
// (jltypes.c:1497-1502): boot.jl's Pair, which julia's jl_pair_type is
// NULL before.
func (vm *VM) modifyResult(fname string, t, old, y any) (any, error) {
	b := vm.Core.own("Pair")
	if b == nil {
		return nil, vm.errorf("%s: Core.Pair is not defined before boot.jl", fname)
	}
	pair := must(core.Apply(b.Value, t, t))
	return vm.newStruct(pair.(*core.DataType), []any{old, y})
}

// cmpswapResult is (old = …, success = …), a NamedTuple{(:old, :success),
// Tuple{t, Bool}} (jl_apply_cmpswap_type, jltypes.c:1504-1520).
func (vm *VM) cmpswapResult(t, old any, success bool) (any, error) {
	nt := must(core.Apply(core.NamedTupleType, vm.newTuple([]any{expr.Symbol("old"), expr.Symbol("success")}),
		must(core.Apply(core.AnyTupleType, t, core.BoolType))))
	return vm.newStruct(nt.(*core.DataType), []any{old, success})
}

// biReplacefield is jl_f_replacefield over replace_value (datatype.c:2128-
// 2150): rhs stored when the field is egal to expected, and
// (old = …, success = …) returned, a NamedTuple{(:old, :success),
// Tuple{T, Bool}} of the field's type.
func biReplacefield(vm *VM, args []any) (any, error) {
	if err := vm.nargs("replacefield!", args, 4, 6); err != nil {
		return nil, err
	}
	s, i, ft, err := vm.fieldOp("replacefield!", args, 4, true)
	if err != nil {
		return nil, err
	}
	if !vm.isa(args[3], ft) {
		return nil, vm.typeError("replacefield!", "", ft, args[3])
	}
	old := s.Fields[i]
	if old == undef {
		return nil, vm.exception(vm.tUndefRef)
	}
	success := vm.egal(old, args[2])
	if success {
		s.Fields[i] = args[3]
		syncArgs(s)
	}
	return vm.cmpswapResult(ft, old, success)
}

// biSetfieldonce is jl_f_setfieldonce over set_nth_fieldonce
// (datatype.c:2260-2286): rhs stored only into an undefined field, and
// whether it was.
func biSetfieldonce(vm *VM, args []any) (any, error) {
	if err := vm.nargs("setfieldonce!", args, 3, 5); err != nil {
		return nil, err
	}
	s, i, ft, err := vm.fieldOp("setfieldonce!", args, 3, true)
	if err != nil {
		return nil, err
	}
	if !vm.isa(args[2], ft) {
		return nil, vm.typeError("setfieldonce!", "", ft, args[2])
	}
	if s.Fields[i] != undef {
		return false, nil
	}
	s.Fields[i] = args[2]
	syncArgs(s)
	return true, nil
}
