package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Bindings as type inference reads them (runtime_internals.jl:202-257):
// Core.Binding and the BindingPartition that says what a name is in a
// world (jltypes.c:3262-3282). julia keeps a binding's history as a list
// of partitions; the VM keeps a binding's current state and the world it
// was declared in, so a binding has two partitions: a guard before that
// world, and its current kind from it on.

// The partition kinds and flags (julia.h, runtime_internals.jl:203-221).
const (
	bkConst          = 0x0
	bkImplicitGlobal = 0x3
	bkImplicitConst  = 0x4
	bkExplicit       = 0x5
	bkImported       = 0x6
	bkDeclared       = 0x8
	bkGlobal         = 0x2
	bkGuard          = 0x9
	bkUndefConst     = 0xa
	bkFlagExported   = 0x10
	bkFlagDeprecated = 0x20
	bkFlagDepwarn    = 0x40
)

// bootBindingTypes defines Binding and BindingPartition with julia's
// layouts, their atomic and const fields marked.
func (vm *VM) bootBindingTypes() {
	vm.tBindingPartition = must(core.NewType("Core", "BindingPartition", core.AnyType, false, true,
		[]expr.Symbol{"restriction", "min_world", "max_world", "next", "kind"},
		[]any{core.AnyType, core.UInt64Type, core.UInt64Type, core.AnyType, core.UInt64Type}))
	vm.tBindingPartition.Name.AtomicFields = marked(5, 1, 2, 3)
	vm.tBindingPartition.Name.ConstFields = marked(5, 0, 4)
	vm.setCore("BindingPartition", vm.tBindingPartition)
	vm.tBinding = must(core.NewType("Core", "Binding", core.AnyType, false, true,
		[]expr.Symbol{"globalref", "value", "partitions", "backedges", "flags"},
		[]any{core.AnyType, core.AnyType, vm.tBindingPartition, core.AnyType, core.UInt8Type}))
	vm.tBinding.Name.AtomicFields = marked(5, 1, 2, 4)
	vm.tBinding.Name.ConstFields = marked(5, 0)
	vm.setCore("Binding", vm.tBinding)
}

// bindingField is getfield of a Binding: its GlobalRef, value and newest
// partition; it keeps no backedges, and none of julia's flags are set.
func (vm *VM) bindingField(b *Binding, arg any) (any, error) {
	i, err := vm.fieldIndex("getfield", vm.tBinding, vm.tBinding.FieldNames(), 5, b, arg)
	if err != nil {
		return nil, err
	}
	switch i {
	case 0:
		return gref(b.Mod, b.Name), nil
	case 1:
		if b.Value == undef {
			return nil, vm.exception(vm.tUndefRef)
		}
		return b.Value, nil
	case 2:
		return vm.bindingPartition(b, ^uint64(0)), nil
	case 3:
		if b.backedges == nil {
			return nil, vm.exception(vm.tUndefRef)
		}
		return b.backedges, nil
	}
	return b.flags, nil
}

// bindingFlagAnyImplicitEdges is BINDING_FLAG_ANY_IMPLICIT_EDGES
// (julia.h:789).
const bindingFlagAnyImplicitEdges = 0x8

// addBindingBackedge is jl_add_binding_backedge (module.c:1648-1663): edge
// appended to b's backedges, unless it is already the last.
func (vm *VM) addBindingBackedge(b *Binding, edge any) {
	if b.backedges == nil {
		b.backedges = vm.newVector(core.AnyType, nil)
	} else if _, _, es, _ := arrayOf(b.backedges); len(es) > 0 && sameObject(es[len(es)-1], edge) {
		return
	}
	vm.vectorPush(b.backedges, edge)
}

// maybeAddBindingBackedge is jl_maybe_add_binding_backedge(b, edge,
// for_method) (module.c:1665-1683): a backedge for a GlobalRef into
// another module than the method's; one into its own marks the binding
// and the method (did_scan_source 0x2) for invalidation's forward scan.
func (vm *VM) maybeAddBindingBackedge(a []any) (any, error) {
	b, ok := a[0].(*Binding)
	if !ok {
		return nil, vm.typeError("ccall", "", vm.tBinding, a[0])
	}
	m, ok := a[2].(*Struct)
	if !ok || m.Type != vm.tMethod {
		return nil, vm.typeError("ccall", "", vm.tMethod, a[2])
	}
	if a[1] == nil || a[1] == undef {
		return int32(0), nil
	}
	if mod, _ := m.Fields[methodModule].(*Module); b.Mod == mod {
		b.flags |= bindingFlagAnyImplicitEdges
		f, _ := m.Fields[methodDidScanSource].(uint8)
		m.Fields[methodDidScanSource] = f | 0x2
		return int32(1), nil
	}
	vm.addBindingBackedge(b, a[1])
	return int32(0), nil
}

// moduleBinding is jl_get_module_binding(m, var, alloc) (module.c:1524):
// m's own binding of name; with alloc, one is made, which resolves
// through m's usings until it is declared or assigned.
func (vm *VM) moduleBinding(m *Module, name expr.Symbol, alloc bool) *Binding {
	if b := m.own(name); b != nil || !alloc {
		return b
	}
	b := m.declare(name, 0)
	b.implicit = true
	return b
}

// bindingPartition is jl_get_binding_partition (module.c): b's partition
// for world w. Its partitions begin at the world it was declared in and at
// each deprecation, a guard before them; each names the older as next.
func (vm *VM) bindingPartition(b *Binding, w uint64) *Struct {
	if w < b.World {
		return &Struct{Type: vm.tBindingPartition, Fields: []any{undef, uint64(0), b.World - 1, nil, uint64(bkGuard)}}
	}
	start, end := b.World, ^uint64(0)
	for _, c := range b.deprecations {
		if c.world <= w {
			start = max(start, c.world)
		} else {
			end = min(end, c.world-1)
		}
	}
	restriction, kind := vm.bindingKind(b)
	if b.Exports {
		kind |= bkFlagExported
	}
	kind |= b.depFlags(w)
	var next any // nothing
	if start > 0 {
		next = vm.bindingPartition(b, start-1)
	}
	return &Struct{Type: vm.tBindingPartition, Fields: []any{restriction, start, end, next, uint64(kind)}}
}

// bindingKind is b's current kind and restriction (module.c:294-320 for an
// implicit one): an import names the binding it imports; an implicit
// binding is its using's const value or global binding, a guard when no
// using exports it; a constant restricts to its value; a global to its
// declared type, or is declared and unassigned.
func (vm *VM) bindingKind(b *Binding) (any, uint8) {
	switch {
	case b.Alias != nil && b.explicit:
		return b.Alias, bkExplicit
	case b.Alias != nil:
		return b.Alias, bkImported
	case b.implicit && b.Value == undef:
		t := b.Mod.lookup(b.Name, ^uint64(0))
		switch {
		case t == nil:
			return undef, bkGuard
		case t.Const && t.Value != undef:
			return t.Value, bkImplicitConst
		}
		return t, bkImplicitGlobal
	case b.Const && b.Value == undef:
		return undef, bkUndefConst
	case b.Const:
		return b.Value, bkConst
	case b.Type != nil:
		return b.Type, bkGlobal
	case b.Value == undef:
		return undef, bkDeclared
	}
	return core.AnyType, bkGlobal
}

// bootBindingShims are the ccalls of runtime_internals.jl:233-257.
func (vm *VM) bootBindingShims() {
	bindingArg := func(v any) (*Binding, error) {
		if b, ok := v.(*Binding); ok {
			return b, nil
		}
		return nil, vm.typeError("ccall", "", vm.tBinding, v)
	}
	partArg := func(v any) (*Struct, error) {
		if p, ok := v.(*Struct); ok && p.Type == vm.tBindingPartition {
			return p, nil
		}
		return nil, vm.typeError("ccall", "", vm.tBindingPartition, v)
	}
	partition := func(fname string, b, w any) (any, error) {
		bb, err := bindingArg(b)
		if err != nil {
			return nil, err
		}
		world, ok := w.(uint64)
		if !ok {
			return nil, fmt.Errorf("%w: %s in the world %s", ErrUnsupported, fname, showValue(w))
		}
		return vm.bindingPartition(bb, world), nil
	}
	vm.shims["jl_get_binding_partition"] = func(vm *VM, a []any) (any, error) {
		return partition("jl_get_binding_partition", a[0], a[1])
	}
	// the hint is a partition of a newer world, which only speeds julia up
	vm.shims["jl_get_binding_partition_with_hint"] = func(vm *VM, a []any) (any, error) {
		if _, err := partArg(a[1]); err != nil {
			return nil, err
		}
		return partition("jl_get_binding_partition_with_hint", a[0], a[2])
	}
	// jl_bpart_get_kind (julia_internal.h:1014-1016)
	vm.shims["jl_bpart_get_kind"] = func(vm *VM, a []any) (any, error) {
		p, err := partArg(a[0])
		if err != nil {
			return nil, err
		}
		return uint8(p.Fields[4].(uint64) & 0xf), nil
	}
	// jl_bpart_get_restriction_value (module.c:1015-1021)
	vm.shims["jl_bpart_get_restriction_value"] = func(vm *VM, a []any) (any, error) {
		p, err := partArg(a[0])
		if err != nil {
			return nil, err
		}
		if p.Fields[0] == undef {
			return nil, vm.exception(vm.tUndefRef)
		}
		return p.Fields[0], nil
	}
	vm.shims["jl_get_module_binding"] = func(vm *VM, a []any) (any, error) {
		m, ok1 := a[0].(*Module)
		name, ok2 := a[1].(expr.Symbol)
		alloc, ok3 := a[2].(int32)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("%w: jl_get_module_binding of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		if b := vm.moduleBinding(m, name, alloc != 0); b != nil {
			return b, nil
		}
		return nil, fmt.Errorf("%w: jl_get_module_binding's NULL", ErrUnsupported)
	}
	moduleVar := func(fname string, a []any) (*Module, expr.Symbol, error) {
		m, ok1 := a[0].(*Module)
		name, ok2 := a[1].(expr.Symbol)
		if !ok1 || !ok2 {
			return nil, "", fmt.Errorf("%w: %s of %s", ErrUnsupported, fname, showValue(vm.newTuple(a)))
		}
		return m, name, nil
	}
	// jl_deprecate_binding (module.c:1811-1828): flag 1 deprecated with a
	// warning, 2 without, 0 neither; a change starts a new world
	vm.shims["jl_deprecate_binding"] = func(vm *VM, a []any) (any, error) {
		m, name, err := moduleVar("jl_deprecate_binding", a)
		if err != nil {
			return nil, err
		}
		flag, ok := a[2].(int32)
		if !ok {
			return nil, vm.typeError("ccall", "", core.Int32Type, a[2])
		}
		var flags uint8
		switch flag {
		case 1:
			flags = bkFlagDeprecated | bkFlagDepwarn
		case 2:
			flags = bkFlagDeprecated
		}
		b := vm.moduleBinding(m, name, true)
		if b.depFlags(vm.taskWorld) == flags {
			return nil, nil
		}
		b.deprecations = append(b.deprecations, depChange{vm.newWorld(), flags})
		return nil, nil
	}
	// jl_is_binding_deprecated (module.c:1853-1859, should_depwarn): m's
	// own binding of name, deprecated in the task's world
	vm.shims["jl_is_binding_deprecated"] = func(vm *VM, a []any) (any, error) {
		m, name, err := moduleVar("jl_is_binding_deprecated", a)
		if err != nil {
			return nil, err
		}
		if b := vm.moduleBinding(m, name, false); b != nil && b.depFlags(vm.taskWorld)&bkFlagDeprecated != 0 {
			return int32(1), nil
		}
		return int32(0), nil
	}
	// a Ptr{Cvoid} to the module, which binding_module reads back with
	// unsafe_pointer_to_objref (runtime_internals.jl:183-187)
	vm.shims["jl_get_module_of_binding"] = func(vm *VM, a []any) (any, error) {
		m, name, err := moduleVar("jl_get_module_of_binding", a)
		if err != nil {
			return nil, err
		}
		return &Pointer{Type: vm.ptrType(core.NothingType), Target: vm.moduleOfBinding(m, name, vm.taskWorld)}, nil
	}
	vm.shims["jl_is_const"] = func(vm *VM, a []any) (any, error) {
		m, name, err := moduleVar("jl_is_const", a)
		if err != nil {
			return nil, err
		}
		if vm.isConst(m, name, vm.taskWorld) {
			return int32(1), nil
		}
		return int32(0), nil
	}
}

// moduleOfBinding is jl_get_module_of_binding (module.c:859-872),
// Base.binding_module: the module that holds the binding m's name
// resolves to in world w, through imports and implicit globals; an
// implicit constant's is the module of the binding it was imported from.
// Module.lookup resolves an implicit import to one binding, the first
// using's, so julia's error for a constant several modules export does
// not arise here.
func (vm *VM) moduleOfBinding(m *Module, name expr.Symbol, w uint64) *Module {
	b := vm.moduleBinding(m, name, true)
	for {
		p := vm.bindingPartition(b, w)
		switch p.Fields[4].(uint64) & 0xf {
		case bkExplicit, bkImported, bkImplicitGlobal:
			b = p.Fields[0].(*Binding)
			continue
		case bkImplicitConst:
			return b.Mod.lookup(b.Name, w).Mod
		}
		return b.Mod
	}
}

// isConst is jl_is_const (module.c:1800-1806): m's binding of name, through
// its imports and implicit globals (jl_walk_binding_inplace,
// julia_internal.h:1024-1033), is a constant in world w
// (jl_bkind_is_real_constant, julia_internal.h:997-999).
func (vm *VM) isConst(m *Module, name expr.Symbol, w uint64) bool {
	b := vm.moduleBinding(m, name, true)
	for {
		p := vm.bindingPartition(b, w)
		switch p.Fields[4].(uint64) & 0xf {
		case bkExplicit, bkImported, bkImplicitGlobal:
			b = p.Fields[0].(*Binding)
		case bkConst, bkImplicitConst:
			return true
		default:
			return false
		}
	}
}
