package core

// The built-in types, created in the order of jl_init_types
// (jltypes.c:2950-3360) and base/boot.jl:248-274, plus the few Base types
// the tests name. Fields, layouts and methods come with the rest of M3.

var (
	AnyType          *DataType
	TypeTypeName     *TypeName
	TypeType         *UnionAll // Type{T} where T
	typeTypePrimary  *DataType // Type{T}, T free
	DataTypeType     *DataType
	TypeNameType     *DataType
	SymbolType       *DataType
	SimpleVectorType *DataType
	NothingType      *DataType
	TypeVarType      *DataType
	TypeofBottomType *DataType
	UnionAllType     *DataType
	UnionType        *DataType
	TypeofVarargType *DataType
	TupleTypeName    *TypeName
	AnyTupleType     *DataType
	EmptyTupleType   *DataType

	NumberType, RealType, AbstractFloatType, IntegerType, SignedType, UnsignedType *DataType
	Float16Type, Float32Type, Float64Type, BFloat16Type                            *DataType
	BoolType, AbstractCharType, CharType                                           *DataType
	Int8Type, UInt8Type, Int16Type, UInt16Type, Int32Type, UInt32Type              *DataType
	Int64Type, UInt64Type, Int128Type, UInt128Type                                 *DataType
	AbstractStringType, StringType, FunctionType, ModuleType, ExceptionType        *DataType

	AbstractArrayType, DenseArrayType, ArrayType, RefType, PtrType, PairType *UnionAll
	ValType, SomeType, AbstractDictType, DictType                            *UnionAll
	MissingType                                                              *DataType
)

// spec describes one built-in type.
type spec struct {
	module, name      string
	abstract, mutable bool
	singleton, isbits bool
	vars              []*TypeVar
	super             func(vars []any) any
	// fields are the field types of an immutable struct, in terms of vars
	fields func(vars []any) []any
}

// define creates a type from its spec and returns its wrapper.
func define(s spec) any {
	tn := &TypeName{Name: s.name, Module: s.module, Abstract: s.abstract, Mutable: s.mutable,
		Singleton: s.singleton, Isbits: s.isbits}
	params := make([]any, len(s.vars))
	for i, v := range s.vars {
		params[i] = v
	}
	if s.fields != nil {
		tn.Fields = s.fields(params)
	}
	dt := &DataType{Name: tn, Parameters: params, HasConcreteSubtype: true}
	tn.Primary = dt
	precomputeMemoized(dt, len(params) == 0)
	if s.super != nil {
		dt.Super = s.super(params).(*DataType)
	} else {
		dt.Super = AnyType
	}
	var w any = dt
	for i := len(s.vars) - 1; i >= 0; i-- {
		w = &UnionAll{Var: s.vars[i], Body: w}
	}
	tn.Wrapper = w
	return w
}

func tv(name string) *TypeVar { return &TypeVar{Name: name, LB: Bottom, UB: AnyType} }

func core0(name string, super *DataType, abstract bool) *DataType {
	return define(spec{module: "Core", name: name, abstract: abstract, super: func([]any) any { return super }}).(*DataType)
}

func primitive(name string, super *DataType) *DataType {
	return define(spec{module: "Core", name: name, isbits: true, super: func([]any) any { return super }}).(*DataType)
}

func mustApply(tc any, params ...any) any {
	t, err := Apply(tc, params...)
	if err != nil {
		panic(err)
	}
	return t
}

func init() {
	anyTN := &TypeName{Name: "Any", Module: "Core", Abstract: true}
	AnyType = &DataType{Name: anyTN, HasConcreteSubtype: true}
	AnyType.Super = AnyType
	anyTN.Wrapper, anyTN.Primary = AnyType, AnyType
	precomputeMemoized(AnyType, true)

	TypeTypeName = &TypeName{Name: "Type", Module: "Core", Abstract: true}
	T := tv("T")
	typeTypePrimary = &DataType{Name: TypeTypeName, Super: AnyType, Parameters: []any{T}, HasConcreteSubtype: true}
	TypeTypeName.Primary = typeTypePrimary
	precomputeMemoized(typeTypePrimary, false)
	TypeType = &UnionAll{Var: T, Body: typeTypePrimary}
	TypeTypeName.Wrapper = TypeType

	DataTypeType = define(spec{module: "Core", name: "DataType", mutable: true, super: func([]any) any { return typeTypePrimary }}).(*DataType)
	TypeNameType = define(spec{module: "Core", name: "TypeName", mutable: true}).(*DataType)
	SymbolType = define(spec{module: "Core", name: "Symbol", mutable: true}).(*DataType)
	SimpleVectorType = define(spec{module: "Core", name: "SimpleVector", mutable: true}).(*DataType)
	NothingType = define(spec{module: "Core", name: "Nothing", singleton: true, isbits: true}).(*DataType)
	TypeVarType = define(spec{module: "Core", name: "TypeVar", mutable: true}).(*DataType)
	TypeofBottomType = define(spec{module: "Core", name: "TypeofBottom", singleton: true, super: func([]any) any { return typeTypePrimary }}).(*DataType)
	UnionAllType = define(spec{module: "Core", name: "UnionAll", super: func([]any) any { return typeTypePrimary }}).(*DataType)
	UnionType = define(spec{module: "Core", name: "Union", super: func([]any) any { return typeTypePrimary }}).(*DataType)
	TypeofVarargType = define(spec{module: "Core", name: "TypeofVararg"}).(*DataType)

	TupleTypeName = &TypeName{Name: "Tuple", Module: "Core"}
	AnyTupleType = &DataType{Name: TupleTypeName, Super: AnyType, Parameters: []any{&Vararg{T: AnyType}}, HasConcreteSubtype: true}
	precomputeMemoized(AnyTupleType, false)
	AnyTupleType.IsConcrete = false
	AnyTupleType.MaybeSubtypeOfCache = false
	TupleTypeName.Wrapper, TupleTypeName.Primary = AnyTupleType, AnyTupleType

	TypeofBottomType.Super = mustApply(TypeType, Bottom).(*DataType)
	EmptyTupleType = instDatatypeInner(AnyTupleType, nil, nil, nil, false).(*DataType)

	NumberType = core0("Number", AnyType, true)
	RealType = core0("Real", NumberType, true)
	AbstractFloatType = core0("AbstractFloat", RealType, true)
	IntegerType = core0("Integer", RealType, true)
	SignedType = core0("Signed", IntegerType, true)
	UnsignedType = core0("Unsigned", IntegerType, true)
	Float16Type = primitive("Float16", AbstractFloatType)
	Float32Type = primitive("Float32", AbstractFloatType)
	Float64Type = primitive("Float64", AbstractFloatType)
	BFloat16Type = primitive("BFloat16", AbstractFloatType)
	BoolType = primitive("Bool", IntegerType)
	AbstractCharType = core0("AbstractChar", AnyType, true)
	CharType = primitive("Char", AbstractCharType)
	Int8Type = primitive("Int8", SignedType)
	UInt8Type = primitive("UInt8", UnsignedType)
	Int16Type = primitive("Int16", SignedType)
	UInt16Type = primitive("UInt16", UnsignedType)
	Int32Type = primitive("Int32", SignedType)
	UInt32Type = primitive("UInt32", UnsignedType)
	Int64Type = primitive("Int64", SignedType)
	UInt64Type = primitive("UInt64", UnsignedType)
	Int128Type = primitive("Int128", SignedType)
	UInt128Type = primitive("UInt128", UnsignedType)
	AbstractStringType = core0("AbstractString", AnyType, true)
	StringType = define(spec{module: "Core", name: "String", mutable: true, super: func([]any) any { return AbstractStringType }}).(*DataType)
	FunctionType = core0("Function", AnyType, true)
	ModuleType = define(spec{module: "Core", name: "Module", mutable: true}).(*DataType)
	ExceptionType = core0("Exception", AnyType, true)

	AbstractArrayType = define(spec{module: "Core", name: "AbstractArray", abstract: true,
		vars: []*TypeVar{tv("T"), tv("N")}}).(*UnionAll)
	DenseArrayType = define(spec{module: "Core", name: "DenseArray", abstract: true,
		vars:  []*TypeVar{tv("T"), tv("N")},
		super: func(v []any) any { return mustApply(AbstractArrayType, v...) }}).(*UnionAll)
	ArrayType = define(spec{module: "Core", name: "Array", mutable: true,
		vars:  []*TypeVar{tv("T"), tv("N")},
		super: func(v []any) any { return mustApply(DenseArrayType, v...) }}).(*UnionAll)
	RefType = define(spec{module: "Core", name: "Ref", abstract: true, vars: []*TypeVar{tv("T")}}).(*UnionAll)
	PtrType = define(spec{module: "Core", name: "Ptr", isbits: true, vars: []*TypeVar{tv("T")},
		super: func(v []any) any { return mustApply(RefType, v...) }}).(*UnionAll)
	PairType = define(spec{module: "Core", name: "Pair", vars: []*TypeVar{tv("A"), tv("B")},
		fields: func(v []any) []any { return v }}).(*UnionAll)

	ValType = define(spec{module: "Base", name: "Val", singleton: true, vars: []*TypeVar{tv("x")}}).(*UnionAll)
	SomeType = define(spec{module: "Base", name: "Some", vars: []*TypeVar{tv("T")},
		fields: func(v []any) []any { return v }}).(*UnionAll)
	MissingType = define(spec{module: "Base", name: "Missing", singleton: true, isbits: true}).(*DataType)
	AbstractDictType = define(spec{module: "Base", name: "AbstractDict", abstract: true,
		vars: []*TypeVar{tv("K"), tv("V")}}).(*UnionAll)
	DictType = define(spec{module: "Base", name: "Dict", mutable: true, vars: []*TypeVar{tv("K"), tv("V")},
		super: func(v []any) any { return mustApply(AbstractDictType, v...) }}).(*UnionAll)
}
