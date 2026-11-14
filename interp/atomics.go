package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The atomic pointer intrinsics (runtime_intrinsics.c:455-632): the loads
// and stores of pointerref and pointerset with julia's order and size
// checks. There is one thread here, so a compare-and-swap loop is its one
// read and write.

// maxPointerAtomicSize is MAX_POINTERATOMIC_SIZE on 64-bit (julia.h:42).
const maxPointerAtomicSize = 16

func (vm *VM) bootAtomics(add func(expr.Symbol, int, func(*VM, []any) (any, error))) {
	add("atomic_fence", 1, func(vm *VM, a []any) (any, error) {
		_, err := vm.atomicOrder("fence", a[0], true, true)
		return nil, err
	})
	add("atomic_pointerref", 2, func(vm *VM, a []any) (any, error) {
		p, err := vm.atomicPtr("atomic_pointerref", a[0], a[1], true, false)
		if err != nil {
			return nil, err
		}
		if err := vm.atomicElem("atomic_pointerref", p, nil); err != nil {
			return nil, err
		}
		return vm.ptrLoad("atomic_pointerref", p, 1)
	})
	add("atomic_pointerset", 3, func(vm *VM, a []any) (any, error) {
		p, err := vm.atomicPtr("atomic_pointerset", a[0], a[2], false, true)
		if err != nil {
			return nil, err
		}
		if err := vm.atomicElem("atomic_pointerset", p, []any{a[1]}); err != nil {
			return nil, err
		}
		return p, vm.ptrStore("atomic_pointerset", p, a[1], 1)
	})
	add("atomic_pointerswap", 3, func(vm *VM, a []any) (any, error) {
		p, err := vm.atomicPtr("atomic_pointerswap", a[0], a[2], true, true)
		if err != nil {
			return nil, err
		}
		if err := vm.atomicElem("atomic_pointerswap", p, []any{a[1]}); err != nil {
			return nil, err
		}
		old, err := vm.ptrLoad("atomic_pointerswap", p, 1)
		if err != nil {
			return nil, err
		}
		return old, vm.ptrStore("atomic_pointerswap", p, a[1], 1)
	})
	add("atomic_pointermodify", 4, func(vm *VM, a []any) (any, error) {
		p, err := vm.atomicPtr("atomic_pointermodify", a[0], a[3], true, true)
		if err != nil {
			return nil, err
		}
		if err := vm.atomicElem("atomic_pointermodify", p, nil); err != nil {
			return nil, err
		}
		old, err := vm.ptrLoad("atomic_pointermodify", p, 1)
		if err != nil {
			return nil, err
		}
		y, err := vm.apply(a[1], []any{old, a[2]})
		if err != nil {
			return nil, err
		}
		ety := p.Type.Parameters[0]
		if ety != any(core.AnyType) && vm.typeOf(y) != ety {
			return nil, vm.typeError("atomic_pointermodify", "", ety, y)
		}
		if err := vm.ptrStore("atomic_pointermodify", p, y, 1); err != nil {
			return nil, err
		}
		return vm.modifyResult("atomic_pointermodify", ety, old, y)
	})
	add("atomic_pointerreplace", 5, func(vm *VM, a []any) (any, error) {
		p, ok := a[0].(*Pointer)
		if err := vm.typechk("atomic_pointerreplace", core.PtrType, a[0], ok); err != nil {
			return nil, err
		}
		for _, o := range a[3:5] {
			if _, ok := o.(expr.Symbol); !ok {
				return nil, vm.typeError("atomic_pointerreplace", "", core.SymbolType, o)
			}
		}
		success, err := vm.atomicOrder("atomic_pointerreplace", a[3], true, true)
		if err != nil {
			return nil, err
		}
		failure, err := vm.atomicOrder("atomic_pointerreplace", a[4], true, false)
		if err != nil {
			return nil, err
		}
		if orderRank[failure] > orderRank[success] {
			return nil, vm.atomicError("atomic_pointerreplace: invalid atomic ordering")
		}
		if err := vm.atomicElem("atomic_pointerreplace", p, []any{a[2]}); err != nil {
			return nil, err
		}
		old, err := vm.ptrLoad("atomic_pointerreplace", p, 1)
		if err != nil {
			return nil, err
		}
		// a bits type compares its bytes (jl_atomic_cmpswap_bits), egal
		// for a value of the same type
		ok = vm.egal(old, a[1])
		if ok {
			if err := vm.ptrStore("atomic_pointerreplace", p, a[2], 1); err != nil {
				return nil, err
			}
		}
		return vm.cmpswapResult(p.Type.Parameters[0], old, ok)
	})
}

// atomicPtr is the checks every atomic pointer intrinsic begins with:
// a pointer, then a valid order for loading or storing.
func (vm *VM) atomicPtr(fname expr.Symbol, pv, order any, loading, storing bool) (*Pointer, error) {
	p, ok := pv.(*Pointer)
	if err := vm.typechk(fname, core.PtrType, pv, ok); err != nil {
		return nil, err
	}
	if _, ok := order.(expr.Symbol); !ok {
		return nil, vm.typeError(fname, "", core.SymbolType, order)
	}
	_, err := vm.atomicOrder(fname, order, loading, storing)
	return p, err
}

// atomicElem checks the pointer's element type: Any, or a concrete type
// with a layout that is not an array (is_valid_intrinsic_elptr,
// julia_internal.h:1653-1656); a value stored of exactly that type; a
// size that is a power of 2 up to MAX_POINTERATOMIC_SIZE.
func (vm *VM) atomicElem(fname expr.Symbol, p *Pointer, stored []any) error {
	ety := p.Type.Parameters[0]
	if ety == any(core.AnyType) {
		return nil
	}
	d, ok := ety.(*core.DataType)
	if !ok || !d.IsConcrete || d.Name == core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType).Name {
		return vm.errorf("%s: invalid pointer", fname)
	}
	size, _, err := dataLayout(d)
	if err != nil {
		return err // a layout the VM does not compute yet
	}
	for _, x := range stored {
		if vm.typeOf(x) != ety {
			return vm.typeError(fname, "", ety, x)
		}
	}
	if size&(size-1) != 0 || size > maxPointerAtomicSize {
		return vm.errorf("%s: invalid pointer for atomic operation", fname)
	}
	return nil
}
