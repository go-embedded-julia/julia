package core

import "fmt"

// VarargType is Core.Vararg, the Vararg with neither T nor N that
// Vararg{T} and Vararg{T,N} apply.
var VarargType = &Vararg{}

// isNestableTypeParam is is_nestable_type_param (builtins.c:1517-1534)
// for tuple types.
func isNestableTypeParam(t any) bool {
	d, ok := t.(*DataType)
	if !ok || d.Name != TupleTypeName {
		return false
	}
	for _, pi := range d.Parameters {
		pd, isdt := pi.(*DataType)
		if !(pi == any(SymbolType) || (isdt && pd.isbits()) || isNestableTypeParam(pi)) {
			return false
		}
	}
	return true
}

// ModuleValue is a Julia module as a value core can hold: a module may be a
// type parameter (jl_valid_type_param, builtins.c:1543-1544), as in
// AddrSpace{Core}. The interpreter's modules implement it.
type ModuleValue interface{ ModuleName() string }

// coreModule is Core as a type parameter. core's types are shared by every
// VM of the process while each VM has its own Core module, so the types
// that name Core in their parameters (AddrSpace{Core}, the CPU address
// space and the memory and array types over it) name this one token.
type coreModule struct{}

func (coreModule) ModuleName() string { return "Core" }

// CoreModule is the Core of the built-in types' parameters.
var CoreModule ModuleValue = coreModule{}

// ValidTypeParam is jl_valid_type_param (builtins.c:1536-1545): v may be a
// parameter of a type.
func ValidTypeParam(v any) bool {
	switch x := v.(type) {
	case *Vararg:
		return false
	case *DataType, *Union, *UnionAll, *bottomType, *TypeVar, Symbol, ModuleValue:
		return true
	case *Instance:
		return x.Type.isbits() || isNestableTypeParam(x.Type)
	}
	d, ok := TypeOf(v).(*DataType)
	return ok && d.isbits()
}

// typeCheck is JL_TYPECHK(fname, type, v).
func typeCheck(fname string, v any) error {
	if !IsType(v) {
		return &Error{fmt.Sprintf("TypeError: in %s, expected Type, got a value of type %s", fname, Show(TypeOf(v)))}
	}
	return nil
}

// BuiltinSubtype is the builtin <: (jl_f_issubtype, builtins.c:572-579):
// both arguments must be types, so a bare TypeVar is an error here although
// Subtype accepts one.
func BuiltinSubtype(a, b any) (bool, error) {
	if err := typeCheck("<:", a); err != nil {
		return false, err
	}
	if err := typeCheck("<:", b); err != nil {
		return false, err
	}
	return SubtypeChecked(a, b)
}

// BuiltinIsa is the builtin isa (jl_f_isa, builtins.c:581-586).
func BuiltinIsa(v, t any) (sub bool, err error) {
	if err := typeCheck("isa", t); err != nil {
		return false, err
	}
	defer catchError(&err)
	return Isa(v, t), nil
}

// ApplyType is the builtin Core.apply_type (jl_f_apply_type,
// builtins.c:1547-1595), what T{P...} in source evaluates to.
func ApplyType(tc any, params ...any) (t any, err error) {
	defer catchError(&err)
	switch {
	case tc == any(AnyTupleType):
		for i, pi := range params {
			if _, ok := pi.(*Vararg); ok {
				if i != len(params)-1 {
					throwf("TypeError: in Tuple, in non-final parameter, expected Type, got %s", Show(pi))
				}
			} else if !ValidTypeParam(pi) {
				throwf("TypeError: in Tuple, in parameter, expected Type, got %s", Show(pi))
			}
		}
		return applyType(AnyTupleType, params), nil
	case tc == any(UnionType):
		return TypeUnion(params...), nil
	}
	if vm, ok := tc.(*Vararg); ok {
		if vm.T == nil {
			if len(params) < 1 || len(params) > 2 {
				throwf("apply_type: too few or too many arguments")
			}
			var n any
			if len(params) == 2 {
				n = params[1]
			}
			return wrapVararg(params[0], n, true), nil
		}
		if vm.N == nil {
			if len(params) != 1 {
				throwf("apply_type: too few or too many arguments")
			}
			return wrapVararg(vm.T, params[0], true), nil
		}
	}
	if _, ok := tc.(*UnionAll); ok {
		for _, pi := range params {
			if !ValidTypeParam(pi) {
				expected := "Type"
				if Isa(pi, NumberType) {
					expected = "Int64"
				}
				throwf("TypeError: in Type, in parameter, expected %s, got %s", expected, Show(pi))
			}
		}
		return applyType(tc, params), nil
	}
	return nil, &Error{fmt.Sprintf("TypeError: in Type{...} expression, expected UnionAll, got %s", Show(TypeOf(tc)))}
}
