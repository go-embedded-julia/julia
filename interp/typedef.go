package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
)

// The builtins lowered `struct` and `abstract type` definitions call
// (builtins.c:1978-2359), and the constructors a type gets: calling a type
// dispatches on the methods defined for Type{T}, the default ones built by
// jl_ctor_def (method.c:1327-1454). Types with parameters are not built yet.

func (vm *VM) bootTypedefs() {
	for name, fn := range map[expr.Symbol]func(*VM, []any) (any, error){
		"_abstracttype":  biAbstracttype,
		"_structtype":    biStructtype,
		"_setsuper!":     biSetsuper,
		"_typebody!":     biTypebody,
		"_equiv_typedef": biEquivTypedef,
		"_defaultctors":  biDefaultctors,
		"fieldtype":      biFieldtype,
	} {
		t := mustType("Core", "#"+string(name), vm.builtinType, false, nil, nil)
		vm.setCore(name, &Builtin{Name: name, Type: t, Fn: fn})
	}
}

// typeArgs checks the module, name and parameter arguments every type
// definition builtin starts with.
func (vm *VM) typeArgs(fname expr.Symbol, args []any) (*Module, expr.Symbol, error) {
	m, ok := args[0].(*Module)
	if err := vm.typechk(fname, core.ModuleType, args[0], ok); err != nil {
		return nil, "", err
	}
	name, ok := args[1].(expr.Symbol)
	if err := vm.typechk(fname, core.SymbolType, args[1], ok); err != nil {
		return nil, "", err
	}
	params, ok := args[2].(SVec)
	if err := vm.typechk(fname, core.SimpleVectorType, args[2], ok); err != nil {
		return nil, "", err
	}
	if len(params) > 0 {
		return nil, "", fmt.Errorf("%w: a type with parameters (%s)", ErrUnsupported, name)
	}
	return m, name, nil
}

// biAbstracttype is jl_f__abstracttype (builtins.c:1997-2005).
func biAbstracttype(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_abstracttype", args, 3, 3); err != nil {
		return nil, err
	}
	m, name, err := vm.typeArgs("_abstracttype", args)
	if err != nil {
		return nil, err
	}
	return core.NewIncompleteType(string(m.Name), string(name), true, false, nil), nil
}

// biStructtype is jl_f__structtype (builtins.c:1978-1995). Field attributes
// (const, atomic) are not supported yet.
func biStructtype(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_structtype", args, 7, 7); err != nil {
		return nil, err
	}
	m, name, err := vm.typeArgs("_structtype", args)
	if err != nil {
		return nil, err
	}
	fnames, ok := args[3].(SVec)
	if err := vm.typechk("_structtype", core.SimpleVectorType, args[3], ok); err != nil {
		return nil, err
	}
	fattrs, ok := args[4].(SVec)
	if err := vm.typechk("_structtype", core.SimpleVectorType, args[4], ok); err != nil {
		return nil, err
	}
	mutable, ok := args[5].(bool)
	if err := vm.typechk("_structtype", core.BoolType, args[5], ok); err != nil {
		return nil, err
	}
	_, ok = args[6].(int64)
	if err := vm.typechk("_structtype", core.Int64Type, args[6], ok); err != nil {
		return nil, err
	}
	if len(fattrs) > 0 {
		return nil, fmt.Errorf("%w: field attributes (%s)", ErrUnsupported, name)
	}
	names := make([]expr.Symbol, len(fnames))
	for i, f := range fnames {
		s, ok := f.(expr.Symbol)
		if !ok {
			return nil, vm.typeError("_structtype", "", core.SymbolType, f)
		}
		names[i] = s
	}
	return core.NewIncompleteType(string(m.Name), string(name), false, mutable, names), nil
}

// biSetsuper is jl_f__setsuper (builtins.c:2051-2058).
func biSetsuper(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_setsuper!", args, 2, 2); err != nil {
		return nil, err
	}
	dt, ok := core.UnwrapUnionAll(args[0]).(*core.DataType)
	if err := vm.typechk("_setsuper!", core.DataTypeType, args[0], ok); err != nil {
		return nil, err
	}
	if sd, ok := args[1].(*core.DataType); ok && core.Subtype(sd, vm.builtinType) {
		return nil, vm.errorf("invalid subtyping in definition of %s: cannot add subtypes to Core.Builtin.", dt.Name.Name)
	}
	if err := dt.SetSuper(args[1]); err != nil {
		return nil, vm.errorf("%s", err.Error())
	}
	return nil, nil
}

// biTypebody is jl_f__typebody (builtins.c:2204-2284): the field types,
// or the previous definition when it has the same ones.
func biTypebody(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_typebody!", args, 2, 3); err != nil {
		return nil, err
	}
	dt, ok := core.UnwrapUnionAll(args[1]).(*core.DataType)
	if err := vm.typechk("_typebody!", core.DataTypeType, args[1], ok); err != nil {
		return nil, err
	}
	if len(args) == 2 {
		return args[1], nil
	}
	ft, ok := args[2].(SVec)
	if err := vm.typechk("_typebody!", core.SimpleVectorType, args[2], ok); err != nil {
		return nil, err
	}
	for _, elt := range ft {
		if _, isVar := elt.(*core.TypeVar); !core.IsType(elt) && !isVar {
			return nil, vm.typeError(expr.Symbol(dt.Name.Name), "type definition", core.TypeType, elt)
		}
	}
	if prev, ok := core.UnwrapUnionAll(args[0]).(*core.DataType); ok && equivFieldTypes(prev.Name.Fields, ft) {
		return args[0], nil
	}
	if err := dt.SetFieldTypes(append([]any(nil), ft...)); err != nil {
		return nil, vm.errorf("%s", err.Error())
	}
	return args[1], nil
}

// equivFieldTypes is equiv_field_types (builtins.c:2123-2142) for field
// types without type variables.
func equivFieldTypes(old []any, ft SVec) bool {
	if old == nil || len(old) != len(ft) {
		return false
	}
	for i := range old {
		if !core.TypesEqual(old[i], ft[i]) {
			return false
		}
	}
	return true
}

// biEquivTypedef is jl_f__equiv_typedef over equiv_type (builtins.c:
// 2287-2352) for types without parameters: same kind, name, mutability,
// field names, and an equal supertype.
func biEquivTypedef(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_equiv_typedef", args, 2, 2); err != nil {
		return nil, err
	}
	a, ok1 := core.UnwrapUnionAll(args[0]).(*core.DataType)
	b, ok2 := core.UnwrapUnionAll(args[1]).(*core.DataType)
	if !ok1 || !ok2 {
		return false, nil
	}
	if a.Name.Name != b.Name.Name || a.Name.Abstract != b.Name.Abstract || a.Name.Mutable != b.Name.Mutable ||
		len(a.Name.FieldNames) != len(b.Name.FieldNames) || len(a.Parameters) != len(b.Parameters) {
		return false, nil
	}
	for i := range a.Name.FieldNames {
		if a.Name.FieldNames[i] != b.Name.FieldNames[i] {
			return false, nil
		}
	}
	return a.Super != nil && b.Super != nil && core.TypesEqual(a.Super, b.Super), nil
}

// biFieldtype is jl_f_fieldtype over get_fieldtype (builtins.c:1179-1265)
// for a struct or tuple type.
func biFieldtype(vm *VM, args []any) (any, error) {
	if err := vm.nargs("fieldtype", args, 2, 3); err != nil {
		return nil, err
	}
	if len(args) == 3 {
		_, ok := args[2].(bool)
		if err := vm.typechk("fieldtype", core.BoolType, args[2], ok); err != nil {
			return nil, err
		}
	}
	st, ok := args[0].(*core.DataType)
	if !ok {
		if core.IsType(args[0]) {
			return nil, fmt.Errorf("%w: fieldtype of %s", ErrUnsupported, core.Show(args[0]))
		}
		return nil, vm.typeError("fieldtype", "", core.DataTypeType, args[0])
	}
	types := st.Name.Fields
	if core.IsTupleType(st) {
		types = st.Parameters
	}
	idx := -1
	switch f := args[1].(type) {
	case int64:
		idx = int(f - 1)
	case expr.Symbol:
		for i, n := range st.Name.FieldNames {
			if n == f {
				idx = i
			}
		}
		if idx < 0 {
			return nil, vm.errorf("type %s has no field %s", st.Name.Name, f)
		}
	default:
		return nil, vm.typeError("fieldtype", "", core.SymbolType, args[1])
	}
	if idx < 0 || idx >= len(types) {
		return nil, vm.exception(vm.tBounds, args[0], args[1])
	}
	return types[idx], nil
}

// biDefaultctors is jl_f__defaultctors, jl_ctor_def (method.c:1327-1454)
// for a type without parameters: an outer constructor taking the field
// types, and an inner one taking Any and converting, unless every field is
// Any (then the two would be the same method).
func biDefaultctors(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_defaultctors", args, 2, 2); err != nil {
		return nil, err
	}
	dt, ok := core.UnwrapUnionAll(args[0]).(*core.DataType)
	if err := vm.typechk("ctor_def", core.DataTypeType, args[0], ok); err != nil {
		return nil, err
	}
	loc, ok := args[1].(expr.LineNumberNode)
	if err := vm.typechk("ctor_def", vm.tLineNumberNode, args[1], ok); err != nil {
		return nil, err
	}
	file, _ := loc.File.(expr.Symbol)
	low := lower.NewState()
	fields := dt.Name.Fields
	tab := vm.ctorTable(dt)
	mod := vm.modules[expr.Symbol(dt.Name.Module)]

	vm.defineCtor(tab, mod, fields, must(low.DefaultOuterCtorBody(dt, len(fields), 0, string(file), int64(loc.Line))), dt)
	allAny := true
	kinds := make([]int64, len(fields))
	anys := make([]any, len(fields))
	for i, ft := range fields {
		anys[i] = core.AnyType
		if ft == any(core.AnyType) {
			kinds[i] = -1
		} else {
			allAny = false
		}
	}
	if allAny {
		return nil, nil
	}
	vm.defineCtor(tab, mod, anys, must(low.DefaultInnerCtorBody(kinds, string(file), int64(loc.Line))), dt)
	return nil, nil
}

// defineCtor defines a constructor of dt taking argument types argtypes,
// with the field names as the slot names of its arguments
// (method.c:1394-1399). Its inputs are the runtime's own: a body lowered
// from a fixed template, field types the definition checked, a world newer
// than every table's; a failure is a broken invariant, which Session.Eval
// reports as ErrInternal.
func (vm *VM) defineCtor(tab *dispatch.Table, mod *Module, argtypes []any, body any, dt *core.DataType) {
	src := body.(*expr.CodeInfo)
	for i, n := range dt.Name.FieldNames {
		if i+1 < len(src.SlotNames) {
			src.SlotNames[i+1] = n
		}
	}
	c := must(prepare(src, false))
	sig := must(core.ApplyType(core.AnyTupleType, argtypes...))
	if mod == nil {
		mod = vm.Main
	}
	must(tab.Define(sig, vm.newWorld(), &lambda{c: c, nargs: len(argtypes) + 1, mod: mod}))
}

// must is the value of a call whose failure would be a bug of the
// interpreter, not of the program.
func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("interp: broken invariant: %v", err))
	}
	return v
}

// ctorTable is the method table of calls of the type dt itself: the
// methods julia keys by Type{dt}.
func (vm *VM) ctorTable(dt *core.DataType) *dispatch.Table {
	tab := vm.ctors[dt]
	if tab == nil {
		tab = &dispatch.Table{Name: dt.Name.Name}
		vm.ctors[dt] = tab
	}
	return tab
}
