// Package core holds Julia's type objects and the algorithms over them that
// the runtime needs before any code runs: construction and instantiation
// (a port of src/jltypes.c), identity (src/builtins.c, egal), and subtyping
// (src/subtype.c), all of julia 1.12.6.
//
// A Julia value that can appear as a type parameter is a Go value:
// *DataType, *Union, *UnionAll, *TypeVar, *Vararg, Bottom, an int64 (a
// Julia Int, as in Vararg{T,3}), an expr.Symbol, a bool, or any other
// comparable Go value standing for an isbits constant. Type objects are
// immutable once built. DataTypes whose parameters are free of type
// variables are interned, as julia's type cache interns them, so that
// pointer identity means what it means in julia (jltypes.c:2097-2141).
package core

import (
	"sync"

	"github.com/go-embedded-julia/julia/expr"
)

// TypeName is the part of a DataType shared by all its instances
// (jl_typename_t, julia.h:521-547).
type TypeName struct {
	Name     string
	Module   string
	Abstract bool
	Mutable  bool
	// Wrapper is the UnionAll over all parameters, or the DataType itself
	// when it has none (Vector's wrapper is Array{T,1} where T, not Array).
	Wrapper any
	// Primary is the DataType at the core of Wrapper, whose parameters are
	// the wrapper's type variables and whose Super refers to them.
	Primary *DataType
	// Singleton reports that instances have no fields and the type has an
	// instance (jl_is_datatype_singleton), for union sorting.
	Singleton bool
	// Isbits reports that instances are plain bits (jl_isbits), for union
	// sorting.
	Isbits bool

	// Fields are the field types of an immutable struct in terms of
	// Primary's parameters, for isbits; nil when it has none or is not one.
	Fields []any

	// typeofWrapper is Type{Wrapper}, cached on the name
	// (jltypes.c:1115-1120).
	typeofWrapper *DataType

	hash uint32

	mu     sync.Mutex
	hashed map[uint32][]*DataType
	linear []*DataType
}

// DataType is a type with a name, a supertype and parameters
// (jl_datatype_t, julia.h:605-626), with the flags subtyping reads.
type DataType struct {
	Name       *TypeName
	Super      *DataType
	Parameters []any

	// hash is typekey_hash of the parameters, 0 when they do not hash
	// (jl_precompute_memoized_dt, jltypes.c:1885).
	hash uint32

	IsConcrete          bool
	HasFreeTypeVars     bool
	IsDispatchTuple     bool
	HasConcreteSubtype  bool
	MaybeSubtypeOfCache bool
}

// Union is Union{A, B} (jl_uniontype_t).
type Union struct{ A, B any }

// UnionAll is Body where Var (jl_unionall_t).
type UnionAll struct {
	Var  *TypeVar
	Body any
}

// TypeVar is a type variable with bounds LB <: Var <: UB (jl_tvar_t).
type TypeVar struct {
	Name   string
	LB, UB any
}

// Vararg is Vararg{T,N}; T and N are nil when absent (jl_vararg_t).
type Vararg struct{ T, N any }

// bottomType is the type of Bottom.
type bottomType struct{}

// Bottom is Union{}, the empty type.
var Bottom = &bottomType{}

func (*bottomType) String() string { return "Union{}" }

// Symbol is the Go value of a Julia Symbol.
type Symbol = expr.Symbol

// IsType reports jl_is_type: v is a DataType, Union, UnionAll or Bottom.
func IsType(v any) bool {
	switch v.(type) {
	case *DataType, *Union, *UnionAll, *bottomType:
		return true
	}
	return false
}

// IsKind reports jl_is_kind: v is one of DataType, Union, UnionAll,
// TypeofBottom (julia.h:1708-1712).
func IsKind(v any) bool {
	return v == any(DataTypeType) || v == any(UnionType) || v == any(UnionAllType) || v == any(TypeofBottomType)
}

// IsConcrete reports jl_is_concrete_type.
func IsConcrete(v any) bool {
	d, ok := v.(*DataType)
	return ok && d.IsConcrete
}

// IsTypeType reports jl_is_type_type: v is Type{T} for some T.
func IsTypeType(v any) bool {
	d, ok := v.(*DataType)
	return ok && d.Name == TypeTypeName
}

// IsTupleType reports jl_is_tuple_type.
func IsTupleType(v any) bool {
	d, ok := v.(*DataType)
	return ok && d.Name == TupleTypeName
}

// IsVaTuple reports jl_is_va_tuple: a tuple type whose last parameter is a
// Vararg.
func IsVaTuple(d *DataType) bool {
	n := len(d.Parameters)
	if n == 0 {
		return false
	}
	_, ok := d.Parameters[n-1].(*Vararg)
	return ok
}

// UnwrapUnionAll strips every leading UnionAll (jl_unwrap_unionall).
func UnwrapUnionAll(v any) any {
	for {
		u, ok := v.(*UnionAll)
		if !ok {
			return v
		}
		v = u.Body
	}
}

// RewrapUnionAll wraps t in the UnionAlls that lead u
// (jl_rewrap_unionall).
func RewrapUnionAll(t, u any) any {
	ua, ok := u.(*UnionAll)
	if !ok {
		return t
	}
	return TypeUnionAll(ua.Var, RewrapUnionAll(t, ua.Body))
}

// TParam0 is the first parameter of a DataType.
func (d *DataType) TParam0() any { return d.Parameters[0] }

// unwrapVararg is jl_unwrap_vararg: T, or Any when T is absent.
func unwrapVararg(v *Vararg) any {
	if v.T == nil {
		return AnyType
	}
	return v.T
}

type varargKind int

const (
	varargNone varargKind = iota
	varargInt
	varargBound
	varargUnbound
)

// kindOfVararg is jl_vararg_kind (julia_internal.h:1153-1163).
func kindOfVararg(v any) varargKind {
	vm, ok := v.(*Vararg)
	if !ok {
		return varargNone
	}
	if vm.N == nil {
		return varargUnbound
	}
	if _, ok := vm.N.(int64); ok {
		return varargInt
	}
	return varargBound
}

// varargLength is jl_vararg_length; N must be an Int.
func varargLength(v any) int64 { return v.(*Vararg).N.(int64) }

// TypeOf is jl_typeof for the values this package handles.
func TypeOf(v any) any {
	switch x := v.(type) {
	case *DataType:
		return DataTypeType
	case *Union:
		return UnionType
	case *UnionAll:
		return UnionAllType
	case *TypeVar:
		return TypeVarType
	case *Vararg:
		return TypeofVarargType
	case *bottomType:
		return TypeofBottomType
	case int64:
		return Int64Type
	case bool:
		return BoolType
	case Symbol:
		return SymbolType
	case *Instance:
		return x.Type
	}
	return nil
}

// Instance is a constant of a user-visible isbits or singleton type, as it
// appears in a type parameter (Val{:x} holds a Symbol; Val{nothing} holds
// the instance of Nothing).
type Instance struct {
	Type *DataType
	Bits string
}
