package core

import "sort"

// The process-static objects: what core builds at package init and every VM
// of the process shares (core/types.go: "types are shared by every VM"). A
// system image does not hold them; it names them, by their index in this
// table, as julia's image names its tagged objects (staticdata.c:123-290).
//
// The table is the exported variables of this package in declaration order
// (boot.go's, then the others), and after them, in the order they are first
// met, everything reachable from those at the end of init: the names of the
// types, their wrappers, the type variables, the interned instances (Type{
// Union{}}, Ptr{Nothing}, the memory types the arrays are made of). An object
// made after init is not in it.

// Static is a named process-static object: a variable of this package.
type Static struct {
	Name  string
	Value any
}

// StaticVars are the variables the table starts from, in declaration order.
// A test parses this package's source and fails if an exported variable is
// missing here.
func StaticVars() []Static {
	return []Static{
		{"AnyType", AnyType}, {"TypeTypeName", TypeTypeName}, {"TypeType", TypeType},
		{"typeTypePrimary", typeTypePrimary}, {"DataTypeType", DataTypeType}, {"TypeNameType", TypeNameType},
		{"SymbolType", SymbolType}, {"SimpleVectorType", SimpleVectorType}, {"NothingType", NothingType},
		{"TypeVarType", TypeVarType}, {"TypeofBottomType", TypeofBottomType}, {"UnionAllType", UnionAllType},
		{"UnionType", UnionType}, {"TypeofVarargType", TypeofVarargType}, {"TupleTypeName", TupleTypeName},
		{"AnyTupleType", AnyTupleType}, {"EmptyTupleType", EmptyTupleType},
		{"NamedTupleType", NamedTupleType}, {"NamedTupleTypeName", NamedTupleTypeName},
		{"NumberType", NumberType}, {"RealType", RealType}, {"AbstractFloatType", AbstractFloatType},
		{"IntegerType", IntegerType}, {"SignedType", SignedType}, {"UnsignedType", UnsignedType},
		{"Float16Type", Float16Type}, {"Float32Type", Float32Type}, {"Float64Type", Float64Type},
		{"BFloat16Type", BFloat16Type},
		{"BoolType", BoolType}, {"AbstractCharType", AbstractCharType}, {"CharType", CharType},
		{"Int8Type", Int8Type}, {"UInt8Type", UInt8Type}, {"Int16Type", Int16Type}, {"UInt16Type", UInt16Type},
		{"Int32Type", Int32Type}, {"UInt32Type", UInt32Type},
		{"Int64Type", Int64Type}, {"UInt64Type", UInt64Type}, {"Int128Type", Int128Type}, {"UInt128Type", UInt128Type},
		{"AbstractStringType", AbstractStringType}, {"StringType", StringType}, {"FunctionType", FunctionType},
		{"ModuleType", ModuleType}, {"ExceptionType", ExceptionType},
		{"AbstractArrayType", AbstractArrayType}, {"DenseArrayType", DenseArrayType}, {"ArrayType", ArrayType},
		{"RefType", RefType}, {"PtrType", PtrType}, {"PairType", PairType},
		{"AddrSpaceType", AddrSpaceType}, {"GenericMemoryType", GenericMemoryType},
		{"GenericMemoryRefType", GenericMemoryRefType},
		{"CPU", CPU},
		{"ValType", ValType}, {"SomeType", SomeType}, {"AbstractDictType", AbstractDictType}, {"DictType", DictType},
		{"MissingType", MissingType},
		// the other exported variables
		{"VarargType", VarargType}, {"CoreModule", CoreModule}, {"Bottom", Bottom},
	}
}

var (
	staticList  []any
	staticIndex map[any]int
)

// StaticObjects is the process-static objects in table order.
func StaticObjects() []any { return append([]any(nil), staticList...) }

// StaticIndex is the index of v in the table: v is a pointer to a static
// object, the Bottom value, or CoreModule.
func StaticIndex(v any) (int, bool) {
	i, ok := staticIndex[v]
	return i, ok
}

// buildStatics fills the table. It runs at the end of init, before any VM
// could add to an interned cache.
func buildStatics() {
	staticList, staticIndex = nil, map[any]int{}
	add := func(v any) bool {
		if _, dup := staticIndex[v]; dup {
			return false
		}
		staticIndex[v] = len(staticList)
		staticList = append(staticList, v)
		return true
	}
	for _, s := range StaticVars() {
		staticWalk(s.Value, add)
	}
}

// staticWalk calls add on v and on what it reaches, in field order; add
// reports whether v was new, and what is not new is not entered again. The
// interned caches of a name are visited by hash, then in the order the types
// were made.
func staticWalk(v any, add func(any) bool) {
	switch x := v.(type) {
	case *DataType:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.Name, add)
		staticWalk(x.Super, add)
		for _, p := range x.Parameters {
			staticWalk(p, add)
		}
		for _, p := range x.ntTypes {
			staticWalk(p, add)
		}
	case *TypeName:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.Primary, add)
		staticWalk(x.Wrapper, add)
		staticWalk(x.typeofWrapper, add)
		for _, f := range x.Fields {
			staticWalk(f, add)
		}
		keys := make([]uint32, 0, len(x.hashed))
		for k := range x.hashed {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, k := range keys {
			for _, d := range x.hashed[k] {
				staticWalk(d, add)
			}
		}
		for _, d := range x.linear {
			staticWalk(d, add)
		}
	case *UnionAll:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.Var, add)
		staticWalk(x.Body, add)
	case *TypeVar:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.LB, add)
		staticWalk(x.UB, add)
	case *Union:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.A, add)
		staticWalk(x.B, add)
	case *Vararg:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.T, add)
		staticWalk(x.N, add)
	case *Instance:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.Type, add)
	case *Tuple:
		if x == nil || !add(x) {
			return
		}
		staticWalk(x.Type, add)
		for _, e := range x.Elems {
			staticWalk(e, add)
		}
	case *bottomType, coreModule:
		add(x)
	}
}
