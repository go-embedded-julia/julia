package core

// White-box tests: branches the differential tests cannot reach from Julia
// source, either because they serve paths not ported yet (intersection,
// specificity) or because they need a state only a direct call builds.
// Expectations cite the C they follow, or say "julia 1.12.6:" when julia
// was asked.

import (
	"strings"
	"testing"
)

func mk(t *testing.T, tc any, ps ...any) any {
	t.Helper()
	v, err := Apply(tc, ps...)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func fresh(name string) *TypeVar { return &TypeVar{Name: name, LB: Bottom, UB: AnyType} }

func mustPanic(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s: no panic", what)
		}
	}()
	f()
}

// Built before any test can cache Type{TypeofBottom}: Type{Type{Union{}}}
// then misses the cache and is normalised to Type{TypeofBottom}
// (jltypes.c:2165-2169). An init func, not a package variable: variables
// are initialised before boot.go's init has built Type.
var typeOfTypeOfBottom any

func init() {
	inner, err := Apply(TypeType, Bottom)
	if err != nil {
		panic(err)
	}
	if typeOfTypeOfBottom, err = Apply(TypeType, inner); err != nil {
		panic(err)
	}
}

func TestWhiteboxNormalisedTypeOfBottom(t *testing.T) {
	d := typeOfTypeOfBottom.(*DataType)
	if d.Parameters[0] != any(TypeofBottomType) {
		t.Errorf("Type{Type{Union{}}} = %s, want Type{Core.TypeofBottom}", Show(d))
	}
}

func TestWhiteboxErrors(t *testing.T) {
	if (&Error{"m"}).Error() != "m" {
		t.Error("Error()")
	}
	mustPanic(t, "catchError re-panics a non-*Error", func() { _ = try(func() { panic(1) }) })
	mustPanic(t, "mustApply", func() { mustApply(Int64Type, Int64Type) })
	if _, err := BuiltinIsa(int64(1), int64(2)); err == nil {
		t.Error("isa(1, 2) did not raise")
	}
	if _, err := ApplyType(AnyTupleType, &Instance{Type: StringType, Bits: `"s"`}); err == nil {
		t.Error(`Tuple{"s"} did not raise`)
	}
	// a non-isbits Number value as a parameter: the message expects Int64
	// (builtins.c:1585-1589)
	bign := define(spec{module: "Main", name: "BigN", mutable: true,
		super: func([]any) any { return NumberType }}).(*DataType)
	_, err := ApplyType(ArrayType, &Instance{Type: bign, Bits: "big(1)"})
	if err == nil || !strings.Contains(err.Error(), "expected Int64") {
		t.Errorf("Array{big(1)}: %v", err)
	}
}

func TestWhiteboxTypeParams(t *testing.T) {
	tupSym := mk(t, AnyTupleType, SymbolType)
	for v, want := range map[any]bool{
		Int64Type: false,
		mk(t, AnyTupleType, Int64Type, SymbolType): true,
		mk(t, AnyTupleType, StringType):            false,
		mk(t, AnyTupleType, tupSym):                true,
	} {
		if got := isNestableTypeParam(v); got != want {
			t.Errorf("isNestableTypeParam(%s) = %v", Show(v), got)
		}
	}
	if !ValidTypeParam(&Instance{Type: mk(t, AnyTupleType, Int64Type).(*DataType)}) ||
		!ValidTypeParam(&Instance{Type: tupSym.(*DataType)}) ||
		ValidTypeParam(&Instance{Type: StringType}) {
		t.Error("ValidTypeParam of instances")
	}
}

func TestWhiteboxExtractWrapperAndSubstitute(t *testing.T) {
	u := &Union{A: &TypeVar{Name: "T", LB: Bottom, UB: Bottom}, B: Int64Type}
	if extractWrapper(u) != any(Int64Type) {
		t.Error("extractWrapper skips a component without a wrapper")
	}
	T := fresh("T")
	if r := mk(t, RefType, T); substituteVar(r, T, T) != r {
		t.Error("substituteVar(t, v, v)")
	}
	if substituteVar(mk(t, RefType, T), T, Int64Type) != mk(t, RefType, Int64Type) {
		t.Error("substituteVar")
	}
}

func TestWhiteboxMaySubstituteUB(t *testing.T) {
	T := fresh("T")
	refT := mk(t, RefType, T)
	n := 0
	S := &TypeVar{Name: "S", LB: refT, UB: AnyType}
	if maySubstituteUB(&UnionAll{Var: S, Body: S}, T, false, &n) {
		t.Error("T in an invariant lower bound")
	}
	S2 := &TypeVar{Name: "S", LB: Bottom, UB: refT}
	if maySubstituteUB(&UnionAll{Var: S2, Body: S2}, T, false, &n) {
		t.Error("T in an invariant upper bound")
	}
	if maySubstituteUB(&Union{A: refT, B: Int64Type}, T, false, &n) {
		t.Error("T invariant in a union")
	}
	if maySubstituteUB(&Vararg{T: Int64Type, N: T}, T, false, &n) {
		t.Error("T as a Vararg length")
	}
	if !maySubstituteUB(&Vararg{}, T, false, &n) {
		t.Error("an empty Vararg")
	}
}

func TestWhiteboxNormalizeUnionAlls(t *testing.T) {
	T := &TypeVar{Name: "T", LB: Bottom, UB: Int64Type}
	tupT := &UnionAll{Var: T, Body: mk(t, AnyTupleType, T)}
	u := normalizeUnionAlls(&Union{A: tupT, B: NothingType})
	if uu, ok := u.(*Union); !ok || uu.A != mk(t, AnyTupleType, Int64Type) {
		t.Errorf("normalised union %s", Show(u))
	}
	S := fresh("S")
	ua := normalizeUnionAlls(&UnionAll{Var: S, Body: &Union{A: tupT, B: mk(t, RefType, S)}})
	if _, ok := ua.(*UnionAll); !ok {
		t.Errorf("normalised UnionAll %s", Show(ua))
	}
}

func TestWhiteboxInstType(t *testing.T) {
	T, N := fresh("T"), fresh("N")
	raw := instType(&Union{A: T, B: Int64Type}, &typeEnv{T, StringType, nil}, nil, false)
	if u, ok := raw.(*Union); !ok || u.A != any(StringType) {
		t.Errorf("unchecked union %s", Show(raw))
	}
	same := TypeUnion(Int64Type, StringType)
	if instType(same, &typeEnv{T, StringType, nil}, nil, true) != same {
		t.Error("an unbound union is returned as is")
	}
	tup := mk(t, AnyTupleType, Int64Type)
	if instType(tup, &typeEnv{T, StringType, nil}, nil, true) != tup {
		t.Error("an unbound tuple is returned as is")
	}
	ntuple := mk(t, AnyTupleType, &Vararg{T: T, N: N})
	if err := try(func() {
		instType(ntuple, &typeEnv{T, Int64Type, &typeEnv{N, int64(-1), nil}}, nil, true)
	}); err == nil {
		t.Error("NTuple{-1,Int} did not raise")
	}
	if try(func() { tupletypeFill(2, VarargType, true) }) == nil {
		t.Error("NTuple{2,Vararg} did not raise")
	}
	if try(func() { tupletypeFill(maxTupleFill+1, Int64Type, false) }) == nil {
		t.Error("tupletypeFill over the bound did not raise")
	}
	if _, err := Apply(AnyTupleType, Int64Type, &Vararg{T: Int64Type, N: int64(maxTupleFill + 1)}); err == nil {
		t.Error("Tuple{Int, Vararg{Int, huge}} did not raise")
	}
	if !TypesEgal(Apply2(t, UnionType, Int64Type, StringType), TypeUnion(Int64Type, StringType)) {
		t.Error("Apply(Union, ...)")
	}
}

func Apply2(t *testing.T, tc any, ps ...any) any { return mk(t, tc, ps...) }

func TestWhiteboxCache(t *testing.T) {
	tup := mk(t, AnyTupleType, Int64Type).(*DataType)
	if typekeyEq(tup, []any{Int64Type, Int64Type}) {
		t.Error("typekeyEq ignores length")
	}
	tt := mk(t, AnyTupleType, mk(t, TypeType, Int64Type)).(*DataType)
	if typekeyEq(tt, []any{DataTypeType}) {
		t.Error("Tuple{Type{Int}} matched Tuple{DataType}")
	}
	for _, v := range []any{Symbol("a"), &Instance{Type: Int64Type, Bits: "1"}, fresh("T"), false} {
		_ = objectID(v)
	}
	// a key that does not hash (its type's hash is 0, as for a type whose
	// parameters failed to hash) goes to the linear cache and misses; the
	// wrapper it is normalised to then hits the hashed cache
	// (jltypes.c:1121-1130, 2157-2176)
	refW := mk(t, PairType, RefType, Int64Type)
	R := fresh("T")
	rawRef := &DataType{Name: RefType.Body.(*DataType).Name, Parameters: []any{R}, Super: AnyType, HasConcreteSubtype: true}
	precomputeMemoized(rawRef, false)
	rawRef.hash = 0
	if mk(t, PairType, &UnionAll{Var: R, Body: rawRef}, Int64Type) != refW {
		t.Error("the normalised key did not find Pair{Ref, Int}")
	}
	vec := mk(t, ArrayType, Int64Type, int64(1)).(*DataType)
	dup := &DataType{Name: vec.Name, Parameters: []any{Int64Type, int64(1)}, HasConcreteSubtype: true}
	precomputeMemoized(dup, true)
	if cacheOrGet(dup) != vec {
		t.Error("cacheOrGet did not return the cached equal type")
	}
	// Type{W} for a W equal to, but not, the wrapper: the first lookup
	// misses, normalisation puts the wrapper in, the second lookup hits
	// (jltypes.c:2157-2176)
	want := mk(t, TypeType, ArrayType)
	A, N := fresh("T"), fresh("N")
	freshArray := &UnionAll{Var: A, Body: &UnionAll{Var: N, Body: mk(t, ArrayType, A, N)}}
	if mk(t, TypeType, freshArray) != want {
		t.Error("Type{Array{T,N} where N where T} is not Type{Array}")
	}
	// the stack of types under construction answers first
	T := fresh("T")
	ref := mk(t, RefType, T).(*DataType)
	if instDatatypeInner(RefType.Body.(*DataType), []any{T}, &typeStack{ref, nil}, nil, false) != ref {
		t.Error("type stack lookup")
	}
	// zero parameters outside a check: the empty tuple (jltypes.c:2188-2192)
	if instDatatypeInner(RefType.Body.(*DataType), []any{}, nil, nil, false) != any(EmptyTupleType) {
		t.Error("empty parameter list")
	}
	if hasConcreteSupertype(int64(1)) {
		t.Error("hasConcreteSupertype(1)")
	}
}

// Foo{A, B<:A}: a type whose bounds depend on an earlier parameter
// (check_datatype_parameters, jltypes.c:1888-1939).
func TestWhiteboxDependentBounds(t *testing.T) {
	A := fresh("A")
	B := &TypeVar{Name: "B", LB: Bottom, UB: A}
	foo := define(spec{module: "Main", name: "Foo", vars: []*TypeVar{A, B}})
	if _, err := Apply(foo, IntegerType, Int64Type); err != nil {
		t.Error(err)
	}
	if _, err := Apply(foo, Int64Type, IntegerType); err == nil {
		t.Error("Foo{Int, Integer} did not raise")
	}
	// applying the inner UnionAll of the wrapper: a bound violation is left
	// to check_datatype_parameters (jltypes.c:1452-1466)
	C := fresh("C")
	D := &TypeVar{Name: "D", LB: Bottom, UB: IntegerType}
	bar := define(spec{module: "Main", name: "Bar", vars: []*TypeVar{C, D}}).(*UnionAll)
	if _, err := Apply(bar.Body, StringType); err == nil {
		t.Error("Bar{C, String} did not raise")
	}
}

func TestWhiteboxShowAndTypes(t *testing.T) {
	T := &TypeVar{Name: "T", LB: Int64Type, UB: IntegerType}
	for v, want := range map[any]string{
		nil:                                   "#null",
		true:                                  "true",
		Symbol("a"):                           ":(a)",
		&Instance{Type: Int64Type, Bits: "1"}: "1",
		3.5:                                   "#unknown",
		&UnionAll{Var: T, Body: mk(t, RefType, T)}: "(Ref{T} where {Int64<:T<:Integer})",
	} {
		if got := Show(v); got != want {
			t.Errorf("Show = %q, want %q", got, want)
		}
	}
	_ = Int64Type.String()
	_ = TypeUnion(Int64Type, StringType).(*Union).String()
	_ = RefType.String()
	_ = T.String()
	_ = (&Vararg{T: Int64Type}).String()
	_ = Bottom.String()
	if !IsTupleType(EmptyTupleType) || IsTupleType(Int64Type) {
		t.Error("IsTupleType")
	}
	if unwrapVararg(&Vararg{}) != any(AnyType) {
		t.Error("unwrapVararg")
	}
	if TypeOf(&Instance{Type: Int64Type}) != any(Int64Type) || TypeOf(3.5) != nil {
		t.Error("TypeOf")
	}
}

func TestWhiteboxFreeVars(t *testing.T) {
	T := fresh("T")
	S := &TypeVar{Name: "S", LB: Bottom, UB: T}
	if !HasFreeTypeVars(&UnionAll{Var: S, Body: S}) {
		t.Error("a free variable in an upper bound")
	}
	S1 := &TypeVar{Name: "S", LB: T, UB: AnyType}
	if !HasFreeTypeVars(&UnionAll{Var: S1, Body: S1}) {
		t.Error("a free variable in a lower bound")
	}
	S2 := &TypeVar{Name: "S", LB: mk(t, RefType, T), UB: mk(t, RefType, T)}
	var out []*TypeVar
	findFreeTypeVars(&UnionAll{Var: S2, Body: &Union{A: S2, B: &Vararg{T: T, N: T}}}, nil, &out)
	findFreeTypeVars(&Vararg{}, nil, &out)
	if len(out) == 0 || out[0] != T {
		t.Errorf("findFreeTypeVars found %v", out)
	}
	if HasTypeVar(&Vararg{}, T) {
		t.Error("HasTypeVar of Vararg")
	}
	if !Egal(&Instance{Type: Int64Type, Bits: "1"}, &Instance{Type: Int64Type, Bits: "1"}) {
		t.Error("Egal of instances")
	}
}

func TestWhiteboxUnions(t *testing.T) {
	if datatypeNameCmp(Int64Type, fresh("T")) != -1 {
		t.Error("datatypeNameCmp")
	}
	T, S := fresh("T"), fresh("S")
	va := &UnionAll{Var: T, Body: mk(t, ArrayType, T, int64(1))}
	vb := &UnionAll{Var: S, Body: mk(t, AbstractArrayType, S, int64(1))}
	if datatypeNameCmp(mk(t, RefType, va), mk(t, RefType, vb)) == 0 {
		t.Error("datatypeNameCmp of unionall parameters")
	}
	vi := mk(t, AnyTupleType, &Vararg{T: Int64Type})
	ti := func(n int) any {
		ps := make([]any, n)
		for i := range ps {
			ps[i] = Int64Type
		}
		return mk(t, AnyTupleType, ps...)
	}
	tail := mk(t, AnyTupleType, Int64Type, Int64Type, &Vararg{T: Int64Type})
	temp := []any{ti(0), ti(1), tail}
	mergeVarargUnions(temp)
	if !TypesEgal(temp[2], vi) || temp[0] != nil || temp[1] != nil {
		t.Errorf("merge: %v", temp)
	}
	temp = []any{mk(t, AnyTupleType, StringType), ti(2), tail}
	mergeVarargUnions(temp)
	temp = []any{ti(0), tail}
	mergeVarargUnions(temp)
	if TypeUnion(Int64Type) != any(Int64Type) {
		t.Error("Union{Int}")
	}
	if ab, ba := simpleSubtype2(Bottom, Int64Type, 0, false); !ab || ba {
		t.Error("simpleSubtype2(Union{}, Int)")
	}
	if ab, ba := simpleSubtype2(Int64Type, Bottom, 0, false); ab || !ba {
		t.Error("simpleSubtype2(Int, Union{})")
	}
	if ab, ba := simpleSubtype2(mk(t, TypeType, Int64Type), mk(t, TypeType, va), 0, false); ab || ba {
		t.Error("simpleSubtype2(Type{Int}, Type{Vector})")
	}
	vs := &UnionAll{Var: S, Body: mk(t, ArrayType, S, int64(1))}
	if ab, ba := simpleSubtype2(va, vs, 0, false); !ab || !ba {
		t.Error("simpleSubtype2 of alpha-equal types")
	}
	if u := simpleUnion(&Union{A: Int64Type, B: IntegerType}, StringType); Show(u) != "Union{Integer, String}" {
		t.Errorf("simpleUnion = %s", Show(u))
	}
	if simpleIntersect(Int64Type, IntegerType, true) != any(Int64Type) ||
		simpleIntersect(IntegerType, Int64Type, true) != any(Int64Type) {
		t.Error("simpleIntersect of nested types")
	}
	if r := simpleIntersect(TypeUnion(Int64Type, StringType), IntegerType, true); r != any(Int64Type) {
		t.Errorf("simpleIntersect(Union{Int,String}, Integer) = %s", Show(r))
	}
}

func TestWhiteboxUnionState(t *testing.T) {
	var st unionState
	if st.get(100) {
		t.Error("an unset bit beyond the stack")
	}
	if try(func() { st.set(32767, true) }) == nil {
		t.Error("the union stack bound did not raise")
	}
	s := pushUnionState(&unionState{used: 100})
	var dst unionState
	popUnionState(&dst, savedUnionState{stack: []uint32{1, 2}})
	if len(s.stack) != 0 || len(dst.stack) != 2 {
		t.Error("push/pop of short stacks")
	}
}

// obviously_disjoint (subtype.c:541-630) and obviously_unequal
// (subtype.c:436-505) on the forms the subtype path does not give them.
func TestWhiteboxObvious(t *testing.T) {
	T := fresh("T")
	refName := RefType.Body.(*DataType).Name
	ref2 := &DataType{Name: refName, Parameters: []any{Int64Type, Int64Type}, Super: AnyType}
	if !obviouslyUnequal(ref2, mk(t, RefType, Int64Type)) {
		t.Error("parameter counts differ")
	}
	if !obviouslyUnequal(Symbol("a"), int64(1)) {
		t.Error("Symbol vs Int")
	}
	rawTupBottom := &DataType{Name: TupleTypeName, Parameters: []any{Bottom}, Super: AnyType}
	for _, c := range []struct {
		a, b any
		spec bool
		want bool
	}{
		{TypeofBottomType, Int64Type, true, false},
		{TypeUnion(Int64Type, StringType), Float64Type, false, true},
		{Float64Type, TypeUnion(Int64Type, StringType), false, true},
		{Int64Type, IntegerType, true, false},
		// with Varargs only the leading fixed parameters are compared:
		// np = min(na, nb) after dropping them (subtype.c:582-595)
		{mk(t, AnyTupleType, Int64Type, &Vararg{T: Int64Type}), mk(t, AnyTupleType, StringType, &Vararg{T: StringType}), false, true},
		{mk(t, AnyTupleType, Int64Type), mk(t, AnyTupleType, &Vararg{T: StringType}), false, false},
		{mk(t, RefType, T), mk(t, RefType, Int64Type), false, false},
		{rawTupBottom, mk(t, AnyTupleType, Int64Type), false, false},
		{mk(t, RefType, mk(t, AnyTupleType, Int64Type)), mk(t, RefType, mk(t, AnyTupleType, StringType)), false, true},
		{mk(t, ValType, Int64Type), mk(t, ValType, int64(1)), false, true},
		{mk(t, ValType, int64(1)), mk(t, ValType, Int64Type), false, true},
		{mk(t, ValType, int64(1)), mk(t, ValType, int64(2)), false, true},
		{Bottom, Int64Type, false, true},
		{mk(t, AnyTupleType, IntegerType), mk(t, AnyTupleType, IntegerType, IntegerType), false, true},
		// non-concrete, so the parameters are compared (subtype.c:601-624)
		{mk(t, RefType, Int64Type), mk(t, RefType, int64(1)), false, true},
		{mk(t, RefType, int64(1)), mk(t, RefType, Int64Type), false, true},
		{mk(t, RefType, int64(1)), mk(t, RefType, int64(2)), false, true},
		{mk(t, RefType, AnyType), mk(t, RefType, int64(1)), false, false},
	} {
		if got := obviouslyDisjoint(c.a, c.b, c.spec); got != c.want {
			t.Errorf("obviouslyDisjoint(%s, %s, %v) = %v", Show(c.a), Show(c.b), c.spec, got)
		}
	}
	// simple_join and simple_meet (subtype.c:634-674)
	L := &TypeVar{Name: "L", LB: Int64Type, UB: AnyType}
	if simpleJoin(L, Int64Type) != any(L) {
		t.Error("simpleJoin with the lower bound")
	}
	if simpleMeet(Int64Type, AnyType, true) != any(Int64Type) ||
		simpleMeet(int64(1), Int64Type, true) != any(Bottom) ||
		simpleMeet(DataTypeType, mk(t, TypeType, Int64Type), true) != mk(t, TypeType, Int64Type) ||
		simpleMeet(mk(t, TypeType, Int64Type), DataTypeType, true) != mk(t, TypeType, Int64Type) {
		t.Error("simpleMeet")
	}
	UV := &TypeVar{Name: "U", LB: Bottom, UB: Int64Type}
	if simpleMeet(UV, Int64Type, true) != any(UV) {
		t.Error("simpleMeet with the upper bound")
	}
}

func TestWhiteboxVars(t *testing.T) {
	e := &stenv{}
	if !e.subtypeLeftVar(int64(1), int64(1), 0) {
		t.Error("subtypeLeftVar(1, 1)")
	}
	if !e.subtypeLeftVar(TypeUnion(Int64Type, StringType), TypeUnion(Int64Type, StringType), 0) {
		t.Error("subtypeLeftVar of egal unions")
	}
	A, B, C := fresh("A"), fresh("B"), fresh("C")
	if e.varOutside(A, B) {
		t.Error("varOutside with no bindings")
	}
	// lengths compared under an offset (subtype.c:764-766, 805-807, 840-849)
	e = &stenv{vars: &varBinding{v: B, lb: Bottom, ub: AnyType, right: true}, loffset: 1}
	if e.varLt(B, Int64Type, 0) || e.varGt(B, Int64Type, 0) {
		t.Error("a type against a length variable under an offset")
	}
	_ = e.subtypeVar(B, int64(2), true, 0)
	e.loffset = 1
	_ = e.subtypeVar(B, int64(2), false, 0)
	if e.loffset != 1 {
		t.Error("subtypeVar did not restore the offset")
	}
	// an exists variable whose lower bound is a forall variable inside it
	// (subtype.c:786-793)
	aa := &varBinding{v: A, lb: A.LB, ub: A.UB, depth0: 1}
	bb := &varBinding{v: B, lb: A, ub: AnyType, right: true}
	aa.prev = bb
	e = &stenv{vars: aa}
	_ = e.varLt(B, A, 0)
	// var_occurs_inside through a UnionAll (subtype.c:2885-2892)
	if varOccursInside(&UnionAll{Var: A, Body: A}, A, true, false) {
		t.Error("a variable is not inside its own UnionAll")
	}
	S := &TypeVar{Name: "S", LB: Bottom, UB: C}
	if !varOccursInside(&UnionAll{Var: S, Body: S}, C, true, false) {
		t.Error("a variable in a bound")
	}
	// unalias_unionall through innervars (subtype.c:936-944)
	e = &stenv{vars: &varBinding{v: B, lb: B.LB, ub: B.UB, innervars: []*TypeVar{C}}}
	u := &UnionAll{Var: C, Body: mk(t, RefType, C)}
	if e.unaliasUnionAll(u) == u {
		t.Error("an inner variable was not renamed")
	}
	// equal_var on a free and on a forall variable (subtype.c:1709-1715)
	// a free variable: x <: lb and ub <: x (subtype.c:1709-1712)
	e = &stenv{}
	if e.equalVar(fresh("F"), Int64Type) {
		t.Error("equal_var(free F, Int): Int <: Union{} holds")
	}
	e = &stenv{vars: &varBinding{v: A, lb: A.LB, ub: A.UB}}
	if e.equalVar(A, Int64Type) {
		t.Error("equal_var(forall A, Int)")
	}
	if e.forallExistsEqual(mk(t, AnyTupleType, &Vararg{T: Int64Type}), mk(t, AnyTupleType, Int64Type)) {
		t.Error("indefinite vs definite tuple")
	}
}

func TestWhiteboxTuples(t *testing.T) {
	N, M := fresh("N"), fresh("M")
	e := &stenv{}
	// N = 0 on the left exhausts it (subtype.c:1097-1111)
	if !e.subtypeTupleVarargs(&Vararg{T: Int64Type, N: int64(0)}, &Vararg{T: Int64Type, N: int64(0)}, nil, nil, 1, 1, 1, 1) {
		t.Error("Vararg{Int,0} vs Vararg{Int,0}")
	}
	if !e.subtypeTupleVarargs(&Vararg{T: Int64Type, N: int64(0)}, &Vararg{T: Int64Type}, nil, nil, 1, 1, 1, 1) {
		t.Error("Vararg{Int,0} vs Vararg{Int}")
	}
	// both lengths known
	if !e.subtypeTupleVarargs(&Vararg{T: Int64Type, N: int64(2)}, &Vararg{T: Int64Type, N: int64(2)}, nil, nil, 1, 1, 1, 1) {
		t.Error("Vararg{Int,2} vs Vararg{Int,2}")
	}
	// the element check fixes the right length to an Int (subtype.c:1144-1157)
	e = &stenv{vars: &varBinding{v: N, lb: Bottom, ub: AnyType, right: true}}
	if e.subtypeTupleVarargs(&Vararg{T: int64(2)}, &Vararg{T: N, N: N}, nil, nil, 1, 1, 1, 1) {
		t.Error("an unbounded Vararg against a length fixed meanwhile")
	}
	// a length variable whose lower bound is an Int (subtype.c:1182-1193)
	e = &stenv{vars: &varBinding{v: M, lb: int64(3), ub: AnyType, right: true}}
	_ = e.subtypeTupleVarargs(&Vararg{T: Int64Type, N: M}, &Vararg{T: Int64Type, N: int64(3)}, nil, nil, 1, 1, 1, 1)
	if !e.subtypeTuple(EmptyTupleType, EmptyTupleType, 0) {
		t.Error("Tuple{} <: Tuple{}")
	}
	tupN := mk(t, AnyTupleType, &Vararg{T: Int64Type, N: N})
	e = &stenv{vars: &varBinding{v: N, lb: Bottom, ub: int64(1), right: true}}
	if e.subtypeTuple(tupN.(*DataType), mk(t, AnyTupleType, Int64Type, Int64Type).(*DataType), 0) {
		t.Error("a length bounded by 1 against two elements")
	}
	e = &stenv{vars: &varBinding{v: N, lb: int64(2), ub: AnyType}}
	_ = e.subtypeTuple(tupN.(*DataType), mk(t, AnyTupleType, Int64Type, Int64Type).(*DataType), 0)
}

func TestWhiteboxSubtypeMisc(t *testing.T) {
	e := &stenv{}
	F := fresh("F")
	_ = e.subtype(TypeUnion(Int64Type, StringType), F, 0)
	if !e.subtype(TypeUnion(Int64Type, StringType), TypeUnion(Int64Type, StringType), 0) {
		t.Error("egal unions")
	}
	// the second, covariant check of a Vararg element fails where the first
	// passed: the left union is split one decision deeper, inside a right
	// union (subtype.c:1133-1137)
	xs := mk(t, AnyTupleType, &Vararg{T: TypeUnion(Int64Type, StringType)})
	ys := TypeUnion(mk(t, AnyTupleType, &Vararg{T: TypeUnion(Int64Type, Int8Type)}), NothingType)
	if (&stenv{}).forallExistsSubtype(xs, ys, 0) || Subtype(xs, ys) {
		t.Error("Tuple{Vararg{Union{Int,String}}} <: Union{Tuple{Vararg{Union{Int,Int8}}}, Nothing}")
	}
	if !hasIntersectTypeNotKind(fresh("H")) {
		t.Error("hasIntersectTypeNotKind(T)")
	}
	T := fresh("T")
	if !e.subtype(mk(t, RefType, Int64Type), &UnionAll{Var: T, Body: AnyType}, 0) {
		t.Error("x <: (Any where T)")
	}
	ua := &UnionAll{Var: T, Body: mk(t, RefType, T)}
	if !e.subtype(ua, ua, 0) {
		t.Error("x <: x for a UnionAll")
	}
	orphan := &DataType{Name: &TypeName{Name: "Orphan", Module: "Main"}, Parameters: []any{T}}
	if try(func() { e.subtype(orphan, Int64Type, 0) }) == nil {
		t.Error("a type with no supertype did not raise")
	}
	// many unions on the left with an exists variable on both sides: the
	// slow ∀∃ loop stops refining after four rounds (subtype.c:1665-1690)
	X := fresh("X")
	e = &stenv{vars: &varBinding{v: X, lb: Bottom, ub: AnyType, right: true}}
	var parts []any
	for _, a := range []any{Int64Type, StringType, Float64Type, BoolType, CharType} {
		parts = append(parts, mk(t, AnyTupleType, a, X))
	}
	_ = e.localForallExistsSubtype(TypeUnion(parts...), mk(t, AnyTupleType, AnyType, X), 0, 1)
	if concreteMin(Bottom) != 1 {
		t.Error("concreteMin(Union{})")
	}
	// find_var_body finds the UnionAll that binds V wherever it sits
	// (subtype.c:1892-1927); Pair{V,Int} where V is no wrapper, so it is not
	// normalised away
	V := fresh("V")
	bindsV := &UnionAll{Var: V, Body: mk(t, PairType, V, Int64Type)}
	for _, h := range []any{
		&UnionAll{Var: &TypeVar{Name: "W", LB: bindsV, UB: AnyType}, Body: Int64Type},
		&UnionAll{Var: &TypeVar{Name: "W", LB: Bottom, UB: bindsV}, Body: Int64Type},
		&Union{A: Int64Type, B: bindsV},
		&Union{A: bindsV, B: Int64Type},
		&Vararg{T: bindsV},
		&Vararg{T: Int64Type, N: bindsV},
		mk(t, RefType, bindsV),
	} {
		if findVarBody(h, V) == nil {
			t.Errorf("findVarBody(%s)", Show(h))
		}
	}
	_ = findVarBody(&Vararg{T: Int64Type, N: int64(1)}, V)
	if !TypesEqual(Bottom, Bottom) || TypesEqual(fresh("Q"), Bottom) {
		t.Error("TypesEqual with Union{}")
	}
	if !hasIntersectTypeNotKind(AnyType) || !hasIntersectTypeNotKind(TypeUnion(Int64Type, mk(t, TypeType, Int64Type))) {
		t.Error("hasIntersectTypeNotKind")
	}
}

// obvious_subtype (subtype.c:1934-2235) on Varargs, malformed types and
// free variables.
func TestWhiteboxObviousSubtype(t *testing.T) {
	var sub bool
	if !obviousSubtype(&Vararg{T: Int64Type}, Int64Type, Int64Type, &sub) || sub {
		t.Error("Vararg vs Int")
	}
	if obviousSubtype(&Vararg{T: Int64Type}, &Vararg{T: Int64Type}, Int64Type, &sub) {
		t.Error("Vararg vs Vararg is not obvious")
	}
	orphan := &DataType{Name: &TypeName{Name: "Orphan", Module: "Main"}}
	if obviousSubtype(orphan, Int64Type, Int64Type, &sub) {
		t.Error("a type with no supertype")
	}
	refName := RefType.Body.(*DataType).Name
	ref2 := &DataType{Name: refName, Parameters: []any{Int64Type, Int64Type}, Super: AnyType}
	if !obviousSubtype(ref2, mk(t, RefType, Int64Type), Int64Type, &sub) || sub {
		t.Error("parameter counts differ")
	}
	S, T := fresh("S"), fresh("T")
	US := TypeUnion(S, Int64Type)
	_ = obviousSubtype(mk(t, RefType, US), mk(t, RefType, Int64Type), Int64Type, &sub)
	tv := mk(t, AnyTupleType, &Vararg{T: T})
	_ = obviousSubtype(mk(t, AnyTupleType, Int64Type, Int64Type), tv, tv, &sub)
	y0 := &UnionAll{Var: T, Body: tv}
	_ = obviousSubtype(mk(t, AnyTupleType, US, Int64Type, Int64Type), tv, y0, &sub)
	_ = obviousSubtype(mk(t, AnyTupleType, Int64Type, Int64Type, US), tv, y0, &sub)
	tvu := mk(t, AnyTupleType, &Vararg{T: US})
	_ = obviousSubtype(mk(t, AnyTupleType, Int64Type, Int64Type), tvu, tvu, &sub)
	// a later element S<:String, Int against the first: S <: Int is not
	// obvious, Int <: S obviously fails (subtype.c:2196-2214)
	SS := &TypeVar{Name: "S", LB: Bottom, UB: StringType}
	if !obviousSubtype(mk(t, AnyTupleType, Int64Type, Int64Type, SS), tv, y0, &sub) || sub {
		t.Error("Tuple{Int,Int,S<:String} against Tuple{Vararg{T}} where T")
	}
}

// Subtyping environments of invariant and dependent variables, answered by
// julia 1.12.6 through ccall(:jl_subtype_env, ...).
func TestWhiteboxEnv(t *testing.T) {
	tt := func(ps ...any) any { return mk(t, AnyTupleType, ps...) }
	ref := func(p any) any { return mk(t, RefType, p) }
	T := fresh("T")
	TU := &TypeVar{Name: "T", LB: Bottom, UB: TypeUnion(Int64Type, StringType, NothingType)}
	SG := &TypeVar{Name: "S", LB: T, UB: AnyType}
	U := TypeUnion(Int64Type, StringType)
	cases := []struct {
		x, y any
		want string
	}{
		{ref(tt(U)), TypeUnionAll(TU, ref(tt(TU))), "Union{Int64, String}"},
		{ref(tt(U)), TypeUnionAll(T, ref(tt(TypeUnion(T, StringType)))), "Int64"},
		{ref(U), TypeUnionAll(TU, ref(TU)), "Union{Int64, String}"},
		{ref(tt(U, U)), TypeUnionAll(T, ref(tt(T, T))), "Union{Int64, String}"},
		{ref(tt(Int64Type, U)), TypeUnionAll(T, ref(tt(T, U))), "Int64"},
		{ref(ref(tt(U))), TypeUnionAll(T, ref(ref(tt(T)))), "Union{Int64, String}"},
		{tt(ref(tt(U)), Int64Type), TypeUnionAll(T, tt(ref(tt(T)), T)), "Union{Int64, String}"},
		{tt(Int64Type, ref(tt(U))), TypeUnionAll(T, tt(T, ref(tt(T)))), "Union{Int64, String}"},
		// julia: T<:Int64, a TypeVar(Union{}, Int64)
		{ref(Int64Type), TypeUnionAll(T, TypeUnionAll(SG, ref(SG))), "T<:Int64"},
	}
	for _, c := range cases {
		env := []any{nil}
		sub, err := SubtypeEnv(c.x, c.y, env)
		got := Show(env[0])
		if v, ok := env[0].(*TypeVar); ok {
			got = v.Name + "<:" + Show(v.UB)
		}
		if err != nil || !sub || got != c.want {
			t.Errorf("env(%s, %s) = %v %v %s, want %s", Show(c.x), Show(c.y), sub, err, got, c.want)
		}
	}
}
