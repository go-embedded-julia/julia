package interp

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// More of src/builtins.c, the ones Base's first files call.

func (vm *VM) bootBuiltins2() {
	for name, fn := range map[expr.Symbol]func(*VM, []any) (any, error){
		"donotdelete":          biDonotdelete,
		"compilerbarrier":      biCompilerbarrier,
		"_svec_len":            biSvecLen,
		"_svec_ref":            biSvecRef,
		"isdefined":            biIsdefined,
		"throw_methoderror":    biThrowMethoderror,
		"current_scope":        biCurrentScope,
		"finalizer":            biFinalizer,
		"_call_in_world_total": biCallInWorldTotal,
		"invoke":               biInvoke,
		"applicable":           biApplicable,
	} {
		t := mustType("Core", "#"+string(name), vm.builtinType, false, nil, nil)
		vm.setCore(name, &Builtin{Name: name, Type: t, Fn: fn})
	}
}

// biDonotdelete is jl_f_donotdelete (builtins.c:2060-2063): it keeps its
// arguments alive for the optimizer, and does nothing.
func biDonotdelete(vm *VM, args []any) (any, error) { return nil, nil }

// biCompilerbarrier is jl_f_compilerbarrier (builtins.c:2065-2076).
func biCompilerbarrier(vm *VM, args []any) (any, error) {
	if err := vm.nargs("compilerbarrier", args, 2, 2); err != nil {
		return nil, err
	}
	s, ok := args[0].(expr.Symbol)
	if err := vm.typechk("compilerbarrier", core.SymbolType, args[0], ok); err != nil {
		return nil, err
	}
	if s != "type" && s != "const" && s != "conditional" {
		return nil, vm.errorf("The first argument of `compilerbarrier` must be either of `:type`, `:const` or `:conditional`.")
	}
	return args[1], nil
}

// biSvecLen is jl_f__svec_len (builtins.c:2100-2106).
func biSvecLen(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_svec_len", args, 1, 1); err != nil {
		return nil, err
	}
	s, ok := args[0].(SVec)
	if err := vm.typechk("_svec_len", core.SimpleVectorType, args[0], ok); err != nil {
		return nil, err
	}
	return int64(len(s)), nil
}

// biSvecRef is jl_f__svec_ref (builtins.c:2108-2121).
func biSvecRef(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_svec_ref", args, 2, 2); err != nil {
		return nil, err
	}
	s, ok := args[0].(SVec)
	if err := vm.typechk("_svec_ref", core.SimpleVectorType, args[0], ok); err != nil {
		return nil, err
	}
	i, ok := args[1].(int64)
	if err := vm.typechk("_svec_ref", core.Int64Type, args[1], ok); err != nil {
		return nil, err
	}
	if i < 1 || i > int64(len(s)) {
		return nil, vm.exception(vm.tBounds, s, i)
	}
	return s[i-1], nil
}

// biIsdefined is jl_f_isdefined (builtins.c:1274-1326): a module's binding,
// imports and usings included (jl_boundp with allow_import), or an
// object's field. No field is atomic here.
func biIsdefined(vm *VM, args []any) (any, error) {
	if err := vm.nargs("isdefined", args, 2, 3); err != nil {
		return nil, err
	}
	order := expr.Symbol("")
	if len(args) == 3 {
		o, ok := args[2].(expr.Symbol)
		if err := vm.typechk("isdefined", core.SymbolType, args[2], ok); err != nil {
			return nil, err
		}
		if o != "not_atomic" && !validAtomicOrder(o, true, false) {
			return nil, vm.atomicError("invalid atomic ordering")
		}
		order = o
	}
	if m, ok := args[0].(*Module); ok {
		s, ok := args[1].(expr.Symbol)
		if err := vm.typechk("isdefined", core.SymbolType, args[1], ok); err != nil {
			return nil, err
		}
		if order == "not_atomic" {
			return nil, vm.atomicError("isdefined: module binding cannot be accessed non-atomically")
		}
		b := m.lookup(s, vm.taskWorld)
		return b != nil && b.Value != undef, nil
	}
	t, _ := vm.typeOf(args[0]).(*core.DataType)
	var names []expr.Symbol
	n := 0
	var fields []any
	switch x := args[0].(type) {
	case *Struct:
		names, n, fields = x.Type.Name.FieldNames, len(x.Fields), x.Fields
	case *Tuple:
		n = len(x.Elems)
	default:
		if t != nil {
			names = t.Name.FieldNames
			n = len(names)
		}
	}
	idx := -1
	switch a := args[1].(type) {
	case int64:
		if a >= 1 && a <= int64(n) {
			idx = int(a - 1)
		}
	case expr.Symbol:
		for i, nm := range names {
			if nm == a {
				idx = i
			}
		}
	default:
		return nil, vm.typeError("isdefined", "", core.SymbolType, args[1])
	}
	if idx < 0 {
		if order != "" {
			return nil, vm.atomicError("isdefined: atomic ordering cannot be specified for nonexistent field")
		}
		return false, nil
	}
	if order != "" && order != "not_atomic" {
		return nil, vm.atomicError("isdefined: non-atomic field cannot be accessed atomically")
	}
	return fields == nil || fields[idx] != undef, nil
}

// biThrowMethoderror is jl_f_throw_methoderror (builtins.c:604-610).
func biThrowMethoderror(vm *VM, args []any) (any, error) {
	if err := vm.nargs("throw_methoderror", args, 1, -1); err != nil {
		return nil, err
	}
	return nil, vm.methodErrorFor(args[0], args[1:])
}

// biCurrentScope is jl_f_current_scope (builtins.c:619-623): the task's
// scope, which no ScopedValue sets here.
func biCurrentScope(vm *VM, args []any) (any, error) {
	if err := vm.nargs("current_scope", args, 0, 0); err != nil {
		return nil, err
	}
	return nil, nil
}

// biFinalizer is jl_f_finalizer (builtins.c:2078-2085). The interpreter's
// values are Go's, which the Go collector frees without running Julia
// code: a finalizer is accepted and never runs.
func biFinalizer(vm *VM, args []any) (any, error) {
	if err := vm.nargs("finalizer", args, 2, 4); err != nil {
		return nil, err
	}
	return nil, nil
}

// biInvoke is jl_f_invoke (builtins.c:1606-1653) for a tuple type: the
// arguments must be of argtypes, and the method of f that argtypes selects
// runs (jl_gf_invoke, gf.c:4265-4283), its static parameters taken from the
// actual arguments. invoke of a Method or a CodeInstance, and of a callee
// other than a generic function, is not here.
func biInvoke(vm *VM, args []any) (any, error) {
	if err := vm.nargs("invoke", args, 2, -1); err != nil {
		return nil, err
	}
	f, argtypes, rest := args[0], args[1], args[2:]
	if !core.IsTupleType(core.UnwrapUnionAll(argtypes)) {
		// jl_anytuple_type_type: Type{T} where T<:Tuple
		tv := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyTupleType}
		return nil, vm.typeError("invoke", "", core.TypeUnionAll(tv, must(core.Apply(core.TypeType, tv))), argtypes)
	}
	actual := vm.newTuple(rest)
	if !vm.isa(actual, argtypes) {
		return nil, vm.typeError("invoke: argument type error", "", argtypes, actual)
	}
	fn, ok := f.(*Function)
	if !ok {
		return nil, fmt.Errorf("%w: invoke of a %s", ErrUnsupported, showValue(vm.typeOf(f)))
	}
	m, _, err := fn.Table.Lookup(argtypes, vm.taskWorld)
	if err != nil {
		if errors.Is(err, dispatch.ErrNoMethod) || errors.Is(err, dispatch.ErrAmbiguous) {
			return nil, vm.exception(vm.tMethodError, f, argtypes, vm.taskWorld)
		}
		return nil, err
	}
	return vm.invokeMethod(m, f, rest)
}

// invokeMethod runs method m of f on args, its static parameters those of
// the actual arguments (jl_gf_invoke_by_method).
func (vm *VM) invokeMethod(m *dispatch.Method, f any, args []any) (any, error) {
	env, _, err := m.Env(vm.argTupleType(args))
	if err != nil {
		return nil, err
	}
	return vm.callMethod(m, env, f, args)
}

// biApplicable is jl_f_applicable (builtins.c): whether a call of f with
// these arguments finds one method in the task's world, by the table apply
// would dispatch on. A builtin or an intrinsic takes any arguments at
// dispatch (Tuple{typeof(f), Vararg{Any}}).
func biApplicable(vm *VM, args []any) (any, error) {
	if err := vm.nargs("applicable", args, 1, -1); err != nil {
		return nil, err
	}
	f, rest := args[0], args[1:]
	tab, tt := vm.others, vm.argTupleType(args)
	switch fn := f.(type) {
	case *Builtin, *Intrinsic:
		return true, nil
	case *Function:
		tab, tt = fn.Table, vm.argTupleType(rest)
	case *core.DataType, *core.UnionAll:
		if d, ok := core.UnwrapUnionAll(fn).(*core.DataType); ok && vm.ctors[d.Name] != nil {
			tab = vm.ctors[d.Name]
		}
	}
	_, _, err := tab.Lookup(tt, vm.taskWorld)
	if err != nil && !errors.Is(err, dispatch.ErrNoMethod) && !errors.Is(err, dispatch.ErrAmbiguous) {
		return nil, err
	}
	return err == nil, nil
}

// biCallInWorldTotal is jl_f__call_in_world_total (builtins.c:905-925):
// invoke_in_world for a callback the compiler treats as pure.
func biCallInWorldTotal(vm *VM, args []any) (any, error) {
	if err := vm.nargs("_call_in_world_total", args, 2, -1); err != nil {
		return nil, err
	}
	w, ok := args[0].(uint64)
	if err := vm.typechk("_call_in_world_total", core.UInt64Type, args[0], ok); err != nil {
		return nil, err
	}
	last := vm.taskWorld
	defer func() { vm.taskWorld = last }()
	vm.taskWorld = min(vm.World(), w)
	return vm.apply(args[1], args[2:])
}
