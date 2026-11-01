package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The builtins of src/builtins.c this first version needs, each with
// julia's argument checks (JL_NARGS, JL_TYPECHK) and messages.

func (vm *VM) bootBuiltins() {
	for name, fn := range map[expr.Symbol]func(*VM, []any) (any, error){
		"===":              biIs,
		"typeof":           biTypeof,
		"isa":              biIsa,
		"<:":               biIssubtype,
		"typeassert":       biTypeassert,
		"throw":            biThrow,
		"ifelse":           biIfelse,
		"tuple":            biTuple,
		"svec":             biSvec,
		"getfield":         biGetfield,
		"nfields":          biNfields,
		"getglobal":        biGetglobal,
		"setglobal!":       biSetglobal,
		"isdefinedglobal":  biIsdefinedglobal,
		"get_binding_type": biGetBindingType,
		"apply_type":       biApplyType,
		"_expr":            biExpr,
	} {
		t := mustType("Core", "#"+string(name), vm.builtinType, false, nil, nil)
		vm.setCore(name, &Builtin{Name: name, Type: t, Fn: fn})
	}
}

// typechk is JL_TYPECHK: a TypeError naming the builtin when v is not of
// the expected kind.
func (vm *VM) typechk(fname expr.Symbol, expected any, v any, ok bool) error {
	if ok {
		return nil
	}
	return vm.typeError(fname, "", expected, v)
}

// egal is jl_egal over the values of this package and core's.
func (vm *VM) egal(a, b any) bool {
	switch x := a.(type) {
	case *Tuple:
		y, ok := b.(*Tuple)
		if !ok || x.Type != y.Type {
			return false
		}
		for i := range x.Elems {
			if !vm.egal(x.Elems[i], y.Elems[i]) {
				return false
			}
		}
		return true
	case *Struct:
		y, ok := b.(*Struct)
		if !ok || x.Type != y.Type {
			return false
		}
		if x == y || x.Type.Name.Mutable {
			return x == y
		}
		for i := range x.Fields {
			if !vm.egal(x.Fields[i], y.Fields[i]) {
				return false
			}
		}
		return true
	case SVec:
		y, ok := b.(SVec)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !vm.egal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Function, *Builtin, *Intrinsic, *Module:
		return a == b
	}
	switch b.(type) {
	case *Tuple, *Struct, SVec, *Function, *Builtin, *Intrinsic, *Module:
		return false
	}
	return core.Egal(a, b)
}

func biIs(vm *VM, args []any) (any, error) {
	if err := vm.nargs("===", args, 2, 2); err != nil {
		return nil, err
	}
	return vm.egal(args[0], args[1]), nil
}

func biTypeof(vm *VM, args []any) (any, error) {
	if err := vm.nargs("typeof", args, 1, 1); err != nil {
		return nil, err
	}
	return vm.typeOf(args[0]), nil
}

// isa is jl_isa over this package's values: a type query on the value's
// type, with core.Isa for what core models itself.
func (vm *VM) isa(v, t any) bool {
	switch v.(type) {
	case *Tuple, *Struct, SVec, *Function, *Builtin, *Intrinsic, *Module, nil, string,
		int8, int16, int32, uint8, uint16, uint32, uint64, float32, float64, expr.Char,
		*expr.Expr, expr.QuoteNode, expr.LineNumberNode, expr.GlobalRef:
		return core.Subtype(vm.typeOf(v), t)
	}
	return core.Isa(v, t)
}

func biIsa(vm *VM, args []any) (any, error) {
	if err := vm.nargs("isa", args, 2, 2); err != nil {
		return nil, err
	}
	if err := vm.typechk("isa", core.TypeType, args[1], core.IsType(args[1])); err != nil {
		return nil, err
	}
	return vm.isa(args[0], args[1]), nil
}

func biIssubtype(vm *VM, args []any) (any, error) {
	if err := vm.nargs("<:", args, 2, 2); err != nil {
		return nil, err
	}
	for _, a := range args {
		if err := vm.typechk("<:", core.TypeType, a, core.IsType(a)); err != nil {
			return nil, err
		}
	}
	return core.Subtype(args[0], args[1]), nil
}

func biTypeassert(vm *VM, args []any) (any, error) {
	if err := vm.nargs("typeassert", args, 2, 2); err != nil {
		return nil, err
	}
	if err := vm.typechk("typeassert", core.TypeType, args[1], core.IsType(args[1])); err != nil {
		return nil, err
	}
	if !vm.isa(args[0], args[1]) {
		return nil, vm.typeError("typeassert", "", args[1], args[0])
	}
	return args[0], nil
}

func biThrow(vm *VM, args []any) (any, error) {
	if err := vm.nargs("throw", args, 1, 1); err != nil {
		return nil, err
	}
	return nil, &Thrown{Value: args[0]}
}

func biIfelse(vm *VM, args []any) (any, error) {
	if err := vm.nargs("ifelse", args, 3, 3); err != nil {
		return nil, err
	}
	c, ok := args[0].(bool)
	if err := vm.typechk("ifelse", core.BoolType, args[0], ok); err != nil {
		return nil, err
	}
	if c {
		return args[1], nil
	}
	return args[2], nil
}

// tuple is jl_f_tuple: a Tuple of the arguments' types.
func biTuple(vm *VM, args []any) (any, error) { return vm.newTuple(args), nil }

func (vm *VM) newTuple(elems []any) *Tuple {
	return &Tuple{Type: vm.tupleTypeOf(elems), Elems: append([]any(nil), elems...)}
}

// tupleTypeOf is the concrete Tuple type of values. Applying Tuple to the
// types of values cannot fail: each is a concrete DataType, a valid
// parameter.
func (vm *VM) tupleTypeOf(vals []any) *core.DataType {
	types := make([]any, len(vals))
	for i, v := range vals {
		types[i] = vm.typeOf(v)
	}
	t, _ := core.Apply(core.AnyTupleType, types...)
	return t.(*core.DataType)
}

// argTupleType is jl_inst_arg_tuple_type with leaf set (jltypes.c:
// 2477-2502), the tuple a call dispatches on: each argument's type, or
// Type{a} for an argument a that is a type. Neither step can fail on the
// values of a call.
func (vm *VM) argTupleType(args []any) *core.DataType {
	types := make([]any, len(args))
	for i, a := range args {
		if core.IsType(a) {
			types[i], _ = core.Apply(core.TypeType, a)
		} else {
			types[i] = vm.typeOf(a)
		}
	}
	t, _ := core.Apply(core.AnyTupleType, types...)
	return t.(*core.DataType)
}

func biSvec(vm *VM, args []any) (any, error) { return SVec(append([]any(nil), args...)), nil }

// fieldIndex is get_checked_fieldindex (builtins.c:994-1021) for reads.
func (vm *VM) fieldIndex(fname expr.Symbol, st *core.DataType, names []expr.Symbol, n int, v, arg any) (int, error) {
	switch a := arg.(type) {
	case int64:
		if a < 1 || a > int64(n) {
			return 0, vm.exception(vm.tBounds, v, arg)
		}
		return int(a - 1), nil
	case expr.Symbol:
		for i, name := range names {
			if name == a {
				return i, nil
			}
		}
		// jl_field_index with err=1 (datatype.c)
		return 0, vm.errorf("type %s has no field %s", st.Name.Name, a)
	}
	return 0, vm.typeError(fname, "", core.TypeUnion(core.Int64Type, core.SymbolType), arg)
}

func biGetfield(vm *VM, args []any) (any, error) {
	if err := vm.nargs("getfield", args, 2, 4); err != nil {
		return nil, err
	}
	switch v := args[0].(type) {
	case *Module:
		return biGetglobal(vm, args[:2])
	case *Tuple:
		i, err := vm.fieldIndex("getfield", v.Type, nil, len(v.Elems), v, args[1])
		if err != nil {
			return nil, err
		}
		return v.Elems[i], nil
	case *Struct:
		i, err := vm.fieldIndex("getfield", v.Type, v.Type.Name.FieldNames, len(v.Fields), v, args[1])
		if err != nil {
			return nil, err
		}
		if v.Fields[i] == undef {
			return nil, vm.exception(vm.tUndefRef)
		}
		return v.Fields[i], nil
	case *expr.Expr, expr.QuoteNode, expr.LineNumberNode, expr.GlobalRef:
		// their fields need Vector{Any} and Core.Binding, not built yet
		return nil, fmt.Errorf("%w: getfield of a %s", ErrUnsupported, core.Show(vm.typeOf(v)))
	}
	// a value without fields (an Int, a Symbol …): every index is out of
	// bounds, every name unknown
	_, err := vm.fieldIndex("getfield", vm.typeOf(args[0]).(*core.DataType), nil, 0, args[0], args[1])
	return nil, err
}

func biNfields(vm *VM, args []any) (any, error) {
	if err := vm.nargs("nfields", args, 1, 1); err != nil {
		return nil, err
	}
	switch v := args[0].(type) {
	case *Tuple:
		return int64(len(v.Elems)), nil
	case *Struct:
		return int64(len(v.Fields)), nil
	}
	return int64(0), nil
}

func (vm *VM) moduleSymbol(fname expr.Symbol, args []any) (*Module, expr.Symbol, error) {
	m, ok := args[0].(*Module)
	if err := vm.typechk(fname, core.ModuleType, args[0], ok); err != nil {
		return nil, "", err
	}
	s, ok := args[1].(expr.Symbol)
	if err := vm.typechk(fname, core.SymbolType, args[1], ok); err != nil {
		return nil, "", err
	}
	return m, s, nil
}

func biGetglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("getglobal", args, 2, 3); err != nil {
		return nil, err
	}
	m, s, err := vm.moduleSymbol("getglobal", args)
	if err != nil {
		return nil, err
	}
	return vm.globalValue(m, s)
}

func biSetglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("setglobal!", args, 3, 4); err != nil {
		return nil, err
	}
	m, s, err := vm.moduleSymbol("setglobal!", args)
	if err != nil {
		return nil, err
	}
	if err := vm.assignGlobal(m, s, args[2]); err != nil {
		return nil, err
	}
	return args[2], nil
}

func biIsdefinedglobal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("isdefined", args, 2, 3); err != nil {
		return nil, err
	}
	m, s, err := vm.moduleSymbol("isdefined", args)
	if err != nil {
		return nil, err
	}
	if len(args) == 3 {
		_, ok := args[2].(bool)
		if err := vm.typechk("isdefined", core.BoolType, args[2], ok); err != nil {
			return nil, err
		}
	}
	b := m.lookup(s, vm.taskWorld)
	return b != nil && b.Value != undef, nil
}

// get_binding_type is jl_f_get_binding_type (builtins.c:1403-1414): Any for
// a global declared without a type, the only kind this version has.
func biGetBindingType(vm *VM, args []any) (any, error) {
	if err := vm.nargs("get_binding_type", args, 2, 2); err != nil {
		return nil, err
	}
	if _, _, err := vm.moduleSymbol("get_binding_type", args); err != nil {
		return nil, err
	}
	return core.AnyType, nil
}

func biApplyType(vm *VM, args []any) (any, error) {
	if err := vm.nargs("apply_type", args, 1, -1); err != nil {
		return nil, err
	}
	t, err := core.ApplyType(args[0], args[1:]...)
	if err != nil {
		return nil, vm.errorf("%s", err.Error())
	}
	return t, nil
}
