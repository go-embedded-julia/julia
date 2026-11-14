package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// typeInstance is a DataType's instance field: the unique instance of a
// singleton type (jl_is_datatype_make_singleton, datatype.c:346-359:
// concrete, immutable, of size 0, without pointers), a memory type's empty
// instance (:617-626), the ones the C runtime sets itself (nothing, Union{}
// and (), jltypes.c:3079, 3094, 3143); undef, C's NULL, for any other
// type. A generic function's and a builtin's type have the function as
// their instance.
func (vm *VM) typeInstance(d *core.DataType) any {
	switch {
	case d == core.NothingType:
		return nil
	case d == vm.typeOf(core.Bottom):
		return core.Bottom
	case d == core.EmptyTupleType:
		return vm.newTuple(nil)
	case d.Name == memoryName(core.GenericMemoryType):
		kind, addrspace := d.Parameters[0], d.Parameters[2]
		if !d.IsConcrete || addrspace != core.CPU || kind != any(expr.Symbol("not_atomic")) && kind != any(expr.Symbol("atomic")) {
			return undef
		}
		if m := vm.emptyMemory[d]; m != nil {
			return m
		}
		m := &Memory{Type: d}
		vm.emptyMemory[d] = m
		return m
	}
	if !d.IsConcrete || d.Name.Mutable {
		return undef
	}
	// the answer, undef included, is kept on the type, as julia's
	// dt->instance: :new asks it of every immutable struct
	if v, ok := d.CachedInstance(); ok {
		return v
	}
	var v any
	if fn := vm.funcs[d]; fn != nil {
		v = fn
	} else if b, ok := vm.singletons[d]; ok {
		v = b // a builtin's
	} else {
		// none while it is being made: a type holding itself, through its
		// fields or theirs, holds a pointer, and is no singleton
		d.SetCachedInstance(undef)
		v = vm.makeSingleton(d)
	}
	d.SetCachedInstance(v)
	return v
}

// makeSingleton is the instance of a concrete immutable type d whose
// fields all hold immutable singletons, which makes it of size 0 without
// pointers; undef for any other. A primitive type has bits.
func (vm *VM) makeSingleton(d *core.DataType) any {
	ft, ok := d.FieldTypes()
	if !ok || d.Name.Bits > 0 {
		return undef
	}
	fields := make([]any, len(ft))
	for i, t := range ft {
		f, ok := t.(*core.DataType)
		if !ok || f.Name.Mutable {
			return undef
		}
		if fields[i] = vm.typeInstance(f); fields[i] == undef {
			return undef
		}
	}
	if core.IsTupleType(d) {
		return &Tuple{Type: d, Elems: fields} // Tuple{Nothing}'s is (nothing,)
	}
	return &Struct{Type: d, Fields: fields}
}
