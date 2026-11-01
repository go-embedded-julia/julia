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
		"setfield!":        biSetfield,
		"invokelatest":     biInvokelatest,
		"invoke_in_world":  biInvokeInWorld,
		"sizeof":           biSizeof,
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
	case *MemoryRef:
		// immutable: the same memory at the same element
		y, ok := b.(*MemoryRef)
		return ok && x.Type == y.Type && x.Mem == y.Mem && x.Off == y.Off
	case *Pointer:
		// bits: the same target at the same offset
		y, ok := b.(*Pointer)
		return ok && x.Type == y.Type && x.Target == y.Target && x.Off == y.Off
	case *Function, *Builtin, *Intrinsic, *Module, *Memory:
		return a == b
	}
	switch b.(type) {
	case *Tuple, *Struct, SVec, *Function, *Builtin, *Intrinsic, *Module, *Memory, *MemoryRef, *Pointer:
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
	case *Tuple, *Struct, SVec, *Function, *Builtin, *Intrinsic, *Module, *Memory, *MemoryRef, *Pointer, nil, string,
		int8, int16, int32, uint8, uint16, uint32, uint64, float32, float64, expr.Char,
		*expr.Expr, expr.QuoteNode, expr.LineNumberNode, expr.GlobalRef,
		expr.SSAValue, expr.SlotNumber, expr.Argument, expr.GotoNode, expr.GotoIfNot, expr.ReturnNode,
		expr.Unreachable, expr.NewvarNode, expr.EnterNode, *expr.CodeInfo:
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

// biInvokelatest is jl_f_invokelatest (builtins.c:874-884): the call runs
// in the latest world, and the task's age is restored after it.
func biInvokelatest(vm *VM, args []any) (any, error) {
	if err := vm.nargs("invokelatest", args, 1, -1); err != nil {
		return nil, err
	}
	last := vm.taskWorld
	defer func() { vm.taskWorld = last }()
	vm.taskWorld = vm.World()
	return vm.apply(args[0], args[1:])
}

// biInvokeInWorld is jl_f_invoke_in_world (builtins.c:888-903): as
// invokelatest, capped at the given world.
func biInvokeInWorld(vm *VM, args []any) (any, error) {
	if err := vm.nargs("invoke_in_world", args, 2, -1); err != nil {
		return nil, err
	}
	w, ok := args[0].(uint64)
	if err := vm.typechk("invoke_in_world", core.UInt64Type, args[0], ok); err != nil {
		return nil, err
	}
	last := vm.taskWorld
	defer func() { vm.taskWorld = last }()
	vm.taskWorld = min(vm.World(), w)
	return vm.apply(args[1], args[2:])
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
// Type{a} for an argument a that is a type. A type with free type
// variables would make Type{a} no dispatch type at all (the TODO of
// jltypes.c:2487-2490); it dispatches on its kind, typeof(a), instead.
// Neither step can fail on the values of a call.
func (vm *VM) argTupleType(args []any) *core.DataType {
	types := make([]any, len(args))
	for i, a := range args {
		if core.IsType(a) && !core.HasFreeTypeVars(a) {
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

// astField is getfield of the AST nodes held as package expr's values, with
// the layouts jltypes.c gives them: Expr(head, args) :3388-3392,
// LineNumberNode(line, file) :3395-3398, QuoteNode(value) :3455-3458,
// GlobalRef(mod, name, binding) :3284-3287. Expr's args (a Vector{Any}) and
// GlobalRef's module and binding have no value here yet.
func (vm *VM) astField(v, arg any) (any, error) {
	t := vm.typeOf(v).(*core.DataType)
	var names []expr.Symbol
	var get func(i int) (any, bool)
	switch x := v.(type) {
	case *expr.Expr:
		names = []expr.Symbol{"head", "args"}
		get = func(i int) (any, bool) {
			if i == 1 {
				return vm.exprArgs(x), true
			}
			return x.Head, true
		}
	case expr.QuoteNode:
		names = []expr.Symbol{"value"}
		get = func(i int) (any, bool) { return x.Value, true }
	case expr.LineNumberNode:
		names = []expr.Symbol{"line", "file"}
		get = func(i int) (any, bool) {
			if i == 0 {
				return int64(x.Line), true
			}
			return x.File, true
		}
	case expr.GlobalRef:
		names = []expr.Symbol{"mod", "name", "binding"}
		get = func(i int) (any, bool) { return x.Name, i == 1 }
	}
	return vm.goField(v, t, names, get, arg)
}

// kindField is getfield of the type-system values core holds, with the
// layouts jltypes.c gives them: DataType :2979-2995, TypeName :3008-3025,
// TypeVar :3081-3084, UnionAll :3096-3099, Union :3104-3107, and Vararg
// (T, N). The fields of runtime bookkeeping (layout, hash, flags, caches)
// have no value here.
func (vm *VM) kindField(v, arg any) (any, error) {
	t := vm.typeOf(v).(*core.DataType)
	var names []expr.Symbol
	var get func(i int) (any, bool)
	switch x := v.(type) {
	case *core.DataType:
		names = []expr.Symbol{"name", "super", "parameters", "types", "instance", "layout", "hash", "flags"}
		get = func(i int) (any, bool) {
			switch i {
			case 0:
				return x.Name, true
			case 1:
				return x.Super, true // Any.super === Any, as core sets it
			case 2:
				return SVec(x.Parameters), true
			case 3:
				// empty for abstract and primitive types; undefined for a
				// struct before _typebody! sets it
				if ft, ok := x.FieldTypes(); ok {
					return SVec(ft), true
				}
				if x.Name.Abstract || x.Name.Bits > 0 {
					return SVec{}, true
				}
				return nil, true
			}
			return nil, false
		}
	case *core.TypeName:
		names = []expr.Symbol{"name", "module", "singletonname", "names", "atomicfields", "constfields",
			"wrapper", "Typeofwrapper", "cache", "linearcache", "partial", "hash", "max_args",
			"n_uninitialized", "flags", "cache_entry_count", "max_methods", "constprop_heuristic"}
		get = func(i int) (any, bool) {
			switch i {
			case 0:
				return expr.Symbol(x.Name), true
			case 3:
				fs := make(SVec, len(x.FieldNames))
				for j, n := range x.FieldNames {
					fs[j] = n
				}
				return fs, true
			case 6:
				return x.Wrapper, true
			}
			return nil, false
		}
	case *core.TypeVar:
		names = []expr.Symbol{"name", "lb", "ub"}
		get = func(i int) (any, bool) { return []any{expr.Symbol(x.Name), x.LB, x.UB}[i], true }
	case *core.UnionAll:
		names = []expr.Symbol{"var", "body"}
		get = func(i int) (any, bool) { return []any{x.Var, x.Body}[i], true }
	case *core.Union:
		names = []expr.Symbol{"a", "b"}
		get = func(i int) (any, bool) { return []any{x.A, x.B}[i], true }
	case *core.Vararg:
		// an absent T or N is undefined, as jl_vararg_t leaves it NULL
		names = []expr.Symbol{"T", "N"}
		get = func(i int) (any, bool) { return []any{x.T, x.N}[i], true }
	}
	f, err := vm.goField(v, t, names, get, arg)
	if err == nil && f == nil {
		return nil, vm.exception(vm.tUndefRef)
	}
	return f, err
}

// goField is getfield of a value package core or expr holds as a Go value:
// the field found by index or name, or ErrUnsupported for a field the
// interpreter keeps no value for.
func (vm *VM) goField(v any, t *core.DataType, names []expr.Symbol, get func(int) (any, bool), arg any) (any, error) {
	i, err := vm.fieldIndex("getfield", t, names, len(names), v, arg)
	if err != nil {
		return nil, err
	}
	f, ok := get(i)
	if !ok {
		return nil, fmt.Errorf("%w: the field %s of a %s", ErrUnsupported, names[i], t.Name.Name)
	}
	return f, nil
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
		return vm.astField(v, args[1])
	case *core.DataType, *core.TypeName, *core.TypeVar, *core.UnionAll, *core.Union, *core.Vararg:
		return vm.kindField(v, args[1])
	case *Memory:
		// (length, ptr) (jltypes.c:3328-3329); a pointer has no value here
		return vm.goField(v, v.Type, []expr.Symbol{"length", "ptr"}, func(i int) (any, bool) {
			return int64(len(v.Elems)), i == 0
		}, args[1])
	case *MemoryRef:
		// (ptr_or_offset, mem) (jltypes.c:3340-3341)
		return vm.goField(v, v.Type, []expr.Symbol{"ptr_or_offset", "mem"}, func(i int) (any, bool) {
			return v.Mem, i == 1
		}, args[1])
	}
	// a value without fields (an Int, a Symbol …): every index is out of
	// bounds, every name unknown
	_, err := vm.fieldIndex("getfield", vm.typeOf(args[0]).(*core.DataType), nil, 0, args[0], args[1])
	return nil, err
}

// biSetfield is jl_f_setfield (builtins.c:1057-1079) over
// get_checked_fieldindex with mutabl set (:994-1021). No field is atomic
// yet, so only the non-atomic order is accepted.
func biSetfield(vm *VM, args []any) (any, error) {
	if err := vm.nargs("setfield!", args, 3, 4); err != nil {
		return nil, err
	}
	order := expr.Symbol("not_atomic")
	if len(args) == 4 {
		o, ok := args[3].(expr.Symbol)
		if err := vm.typechk("setfield!", core.SymbolType, args[3], ok); err != nil {
			return nil, err
		}
		if o != "not_atomic" && !validAtomicOrder(o, false, true) {
			return nil, vm.atomicError("invalid atomic ordering")
		}
		order = o
	}
	// isatomic == (order == notatomic) is the mismatch (builtins.c:1068-1071)
	atomicCheck := func(isatomic bool) error {
		if isatomic == (order == "not_atomic") {
			if isatomic {
				return vm.atomicError("setfield!: atomic field cannot be written non-atomically")
			}
			return vm.atomicError("setfield!: non-atomic field cannot be written atomically")
		}
		return nil
	}
	if tn, ok := args[0].(*core.TypeName); ok {
		i, err := vm.fieldIndex("setfield!", core.TypeNameType, core.TypeNameType.Name.FieldNames, len(core.TypeNameType.Name.FieldNames), tn, args[1])
		if err != nil {
			return nil, err
		}
		// typename_atomicfields (jltypes.c:3015): Typeofwrapper, cache,
		// linearcache, max_args, cache_entry_count
		if err := atomicCheck(i == 7 || i == 8 || i == 9 || i == 12 || i == 15); err != nil {
			return nil, err
		}
		// the fields that steer inference and method caches; there is no
		// inference here, so a hint is accepted and kept nowhere
		switch core.TypeNameType.Name.FieldNames[i] {
		case "max_methods", "constprop_heuristic", "max_args", "cache_entry_count":
			return args[2], nil
		}
		return nil, fmt.Errorf("%w: setfield! of a TypeName's %s", ErrUnsupported, core.TypeNameType.Name.FieldNames[i])
	}
	if _, isMod := args[0].(*Module); !isMod {
		// no other field is atomic here
		if err := atomicCheck(false); err != nil {
			return nil, err
		}
	}
	switch v := args[0].(type) {
	case *Module:
		return nil, vm.errorf("cannot assign variables in other modules")
	case *Struct:
		if !v.Type.Name.Mutable {
			return nil, vm.errorf("setfield!: immutable struct of type %s cannot be changed", v.Type.Name.Name)
		}
		i, err := vm.fieldIndex("setfield!", v.Type, v.Type.Name.FieldNames, len(v.Fields), v, args[1])
		if err != nil {
			return nil, err
		}
		if ft, _ := v.Type.FieldTypes(); ft != nil && !vm.isa(args[2], ft[i]) {
			return nil, vm.typeError("setfield!", "", ft[i], args[2])
		}
		v.Fields[i] = args[2]
		syncArgs(v) // an Expr's args vector: the Expr follows it
		return args[2], nil
	case *expr.Expr:
		return vm.setExprField(v, args[1], args[2])
	}
	t := vm.typeOf(args[0]).(*core.DataType)
	return nil, vm.errorf("setfield!: immutable struct of type %s cannot be changed", t.Name.Name)
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
	m, name, err := vm.moduleSymbol("get_binding_type", args)
	if err != nil {
		return nil, err
	}
	if b := m.own(name); b != nil && b.Type != nil {
		return b.Type, nil
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
