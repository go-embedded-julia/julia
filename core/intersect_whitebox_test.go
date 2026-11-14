package core

// White-box tests of the helpers no public intersection query reaches: the
// nothrow failure returns of inst_type_w_ (jltypes.c:2597-2747), which only
// jl_substitute_var_nothrow's callers use. Each expectation follows the C
// lines cited.

import "testing"

// lim is Lim{A<:Integer}; instantiating its body with A := String is the
// type that cannot be built (check_datatype_parameters, jltypes.c:1888-1939).
func limBody(t *testing.T) (*DataType, *TypeVar) {
	t.Helper()
	if limDT == nil {
		A := &TypeVar{Name: "A", LB: Bottom, UB: IntegerType}
		limW = define(spec{module: "Main", name: "Lim", vars: []*TypeVar{A}})
		limDT = UnwrapUnionAll(limW).(*DataType)
	}
	return limDT, limDT.Parameters[0].(*TypeVar)
}

var (
	limDT *DataType
	limW  any
)

func TestWhiteboxNothrowInst(t *testing.T) {
	lim, A := limBody(t)
	bad := &typeEnv{A, StringType, nil}
	for _, nothrow := range []int{1, 2} {
		if got := instTypeW(lim, bad, nil, true, nothrow); got != null {
			t.Fatalf("Lim{String} with nothrow %d: %s, not NULL", nothrow, Show(got))
		}
	}

	// wrapVararg (jltypes.c:2812-2828): a count that is not an Int, a
	// negative count and a type that is no type parameter give NULL.
	if wrapVararg(Int64Type, StringType, true, 1) != nil {
		t.Error("Vararg{Int, String} with nothrow")
	}
	if wrapVararg(Int64Type, int64(-1), true, 1) != nil {
		t.Error("Vararg{Int, -1} with nothrow")
	}
	if wrapVararg(&Vararg{T: Int64Type}, nil, true, 1) != nil {
		t.Error("Vararg{Vararg} with nothrow")
	}

	// UnionAll (jltypes.c:2622-2626): a lower bound that fails is NULL.
	body := Int64Type
	if instTypeW(&UnionAll{Var: &TypeVar{Name: "L", LB: lim, UB: AnyType}, Body: body}, bad, nil, true, 1) != null {
		t.Error("UnionAll with a failing lower bound")
	}
	// (2629-2634): a failing upper bound is NULL, unless the lower bound is
	// Union{}, which then makes the variable Union{}
	if instTypeW(&UnionAll{Var: &TypeVar{Name: "U", LB: Int64Type, UB: lim}, Body: body}, bad, nil, true, 1) != null {
		t.Error("UnionAll with a failing upper bound")
	}
	uv := &TypeVar{Name: "U", LB: Bottom, UB: lim}
	if got := instTypeW(&UnionAll{Var: uv, Body: uv}, bad, nil, true, 1); got != any(Bottom) {
		t.Errorf("UnionAll{U<:Lim{String}, U}: %s", Show(got))
	}
	// (2644-2647): a failing body is NULL
	if instTypeW(&UnionAll{Var: fresh("V"), Body: lim}, bad, nil, true, 1) != null {
		t.Error("UnionAll with a failing body")
	}

	// Union (jltypes.c:2667-2682): a member that is not a type is NULL; one
	// NULL member is NULL with nothrow 1 and dropped with nothrow 2.
	T := fresh("T")
	u := &Union{A: T, B: Int64Type}
	notType := &typeEnv{T, int64(3), nil}
	if instTypeW(u, notType, nil, true, 1) != null {
		t.Error("Union{T, Int} with T := 3, nothrow 1")
	}
	if instTypeW(u, notType, nil, true, 2) != any(Int64Type) {
		t.Error("Union{T, Int} with T := 3, nothrow 2")
	}
	if instTypeW(&Union{A: lim, B: Int64Type}, bad, nil, true, 1) != null {
		t.Error("Union{Lim{String}, Int}, nothrow 1")
	}
	if instTypeW(&Union{A: lim, B: Int64Type}, bad, nil, true, 2) != any(Int64Type) {
		t.Error("Union{Lim{String}, Int}, nothrow 2")
	}

	// Vararg (jltypes.c:2698-2710): a failing T is NULL, or Union{} with
	// nothrow 2; a failing N is NULL.
	if instTypeW(&Vararg{T: lim, N: int64(2)}, bad, nil, true, 1) != null {
		t.Error("Vararg{Lim{String}, 2}, nothrow 1")
	}
	if v, ok := instTypeW(&Vararg{T: lim, N: int64(2)}, bad, nil, true, 2).(*Vararg); !ok || v.T != any(Bottom) || v.N != any(int64(2)) {
		t.Error("Vararg{Lim{String}, 2}, nothrow 2")
	}
	if instTypeW(&Vararg{T: Int64Type, N: lim}, bad, nil, true, 1) != null {
		t.Error("Vararg{Int, Lim{String}}")
	}

	// a datatype parameter that fails (2734-2737)
	if instTypeW(mk(t, RefType, lim), bad, nil, true, 1) != null {
		t.Error("Ref{Lim{String}}")
	}

	// inst_tuple_w_ (jltypes.c:2556-2560, 2576-2584): a negative length, and
	// a failing element that is Union{} with nothrow 2 (which Tuple then
	// refuses)
	TT, N := fresh("T"), fresh("N")
	ntuple := mk(t, AnyTupleType, &Vararg{T: TT, N: N})
	if instTypeW(ntuple, &typeEnv{N, int64(-1), &typeEnv{TT, Int64Type, nil}}, nil, true, 1) != null {
		t.Error("NTuple{-1, Int}, nothrow 1")
	}
	if instTypeW(mk(t, AnyTupleType, lim, Int64Type), bad, nil, true, 2) != null {
		t.Error("Tuple{Lim{String}, Int}, nothrow 2")
	}
	// jl_tupletype_fill (jltypes.c:2058-2085): a length filled with a value
	// that is no type parameter
	if instTypeW(ntuple, &typeEnv{N, int64(2), &typeEnv{TT, &Vararg{T: Int64Type}, nil}}, nil, true, 1) != null {
		t.Error("NTuple{2, Vararg}, nothrow 1")
	}
}

// check_datatype_parameters with nothrow (jltypes.c:1888-1939): a later
// bound that cannot be instantiated with an earlier parameter is invalid.
func TestWhiteboxNothrowBounds(t *testing.T) {
	limBody(t)
	A := fresh("A")
	B := &TypeVar{Name: "B", LB: Bottom, UB: mk(t, limW, A)}
	foo := define(spec{module: "Main", name: "Foo2", vars: []*TypeVar{A, B}}).(*UnionAll)
	tn := UnwrapUnionAll(foo).(*DataType).Name
	// A := String makes the bound Lim{A} the invalid Lim{String}
	if !checkDatatypeParameters(tn, []any{StringType, Int8Type}, 1) {
		t.Error("Foo2{String, Int8}: the bound Lim{String} is invalid")
	}
	// A := Int makes it Lim{Int}
	if checkDatatypeParameters(tn, []any{Int64Type, mk(t, limW, Int64Type)}, 1) {
		t.Error("Foo2{Int, Lim{Int}} is valid")
	}
}

// inst_datatype_inner with nothrow (jltypes.c:2327-2373, 2400-2430): invalid
// NamedTuple names, and a supertype that cannot be instantiated.
func TestWhiteboxNothrowDatatype(t *testing.T) {
	nt := UnwrapUnionAll(NamedTupleType).(*DataType)
	ab := tupleValue(Symbol("a"), Symbol("b"))
	tt2 := mk(t, AnyTupleType, Int64Type, Int64Type)
	mkNT := func(names, tt any) *DataType {
		return &DataType{Name: NamedTupleTypeName, Parameters: []any{names, tt}}
	}
	for _, c := range []struct {
		what    string
		names   any
		tt      any
		invalid bool
	}{
		{"names not a tuple", Int64Type, tt2, true},
		{"a name that is no Symbol", tupleValue(int64(1)), mk(t, AnyTupleType, Int64Type), true},
		{"a duplicate name", tupleValue(Symbol("a"), Symbol("a")), tt2, true},
		{"mismatched lengths", tupleValue(Symbol("a")), tt2, true},
		{"a Vararg field tuple", ab, mk(t, AnyTupleType, &Vararg{T: Int64Type}), true},
		{"valid", ab, tt2, false},
	} {
		if got := !namedTupleFields(mkNT(c.names, c.tt), 1); got != c.invalid {
			t.Errorf("namedTupleFields, %s: invalid %v", c.what, got)
		}
	}
	// ... which makes the instance NULL
	dup := tupleValue(Symbol("a"), Symbol("a"))
	if instDatatypeInner(nt, []any{dup, tt2}, nil, nil, true, 1) != null {
		t.Error("NamedTuple{(:a, :a), Tuple{Int, Int}} with nothrow")
	}
	// a supertype that fails (jltypes.c:2431-2440)
	lim, A := limBody(t)
	bad := &typeEnv{A, StringType, nil}
	refDT := RefType.Body.(*DataType)
	ref := &DataType{Name: refDT.Name, Parameters: refDT.Parameters, Super: lim}
	if instDatatypeInner(ref, []any{fresh("R")}, nil, bad, true, 1) != null {
		t.Error("an instance whose supertype is Lim{String}")
	}
}

// intersectEnv is a fresh environment in intersection mode (init_stenv with
// intersection and ignore_free set, subtype.c:4565-4656).
func intersectEnv() *stenv {
	e := getStenv(nil)
	e.intersection, e.ignoreFree = true, true
	return e
}

// The environment-only helpers of intersection, each called with a
// hand-built environment; the expectations follow the C lines cited.
func TestWhiteboxIntersectHelpers(t *testing.T) {
	x, y, w := fresh("x"), fresh("y"), fresh("w")
	e := intersectEnv()
	defer putStenv(e)

	// set_var_to_const (subtype.c:2605-2617): a variable with the bounds
	// Union{}..Any at a nonzero offset takes only an Int.
	e.loffset = 1
	if got := e.setVarToConst(&varBinding{v: x, lb: Bottom, ub: AnyType}, Symbol("s"), false); got != any(Bottom) {
		t.Errorf("setVarToConst(:s) at offset 1: %v", got)
	}
	e.loffset = 0

	// set_bound (subtype.c:2697-2709): nothing is set when val is the
	// variable itself, or when a binding bounded by the variable has val's
	// own variable.
	var bound any
	e.setBound(&bound, x, x)
	e.vars = &varBinding{v: y, lb: x, ub: AnyType}
	e.setBound(&bound, y, x)
	if bound != nil {
		t.Errorf("setBound set %v", bound)
	}
	e.setBound(&bound, Int64Type, x)
	if bound != any(Int64Type) {
		t.Errorf("setBound did not set Int: %v", bound)
	}

	// compareto_var and subtype_by_bounds (subtype.c:3853-3878): x's upper
	// bound is y, w is bounded by y on both sides.
	e.vars = &varBinding{v: w, lb: y, ub: y, prev: &varBinding{v: y, lb: Bottom, ub: AnyType,
		prev: &varBinding{v: x, lb: Bottom, ub: y}}}
	for _, c := range []struct {
		x    any
		cmp  int
		want bool
	}{
		{x, -1, true},           // x <= y: ub(x) is y
		{x, 1, false},           // x >= y: lb(x) is Union{}, not a variable
		{x, 0, false},           // both asked: lb(x) fails
		{w, 0, true},            // lb(w) and ub(w) are both y
		{Int64Type, -1, false},  // not a variable
		{fresh("u"), -1, false}, // a variable with no binding
	} {
		if got := e.comparetoVar(c.x, y, c.cmp); got != c.want {
			t.Errorf("comparetoVar(%s, y, %d) = %v", Show(c.x), c.cmp, got)
		}
	}
	if !e.subtypeByBounds(x, y) || e.subtypeByBounds(y, x) || e.subtypeByBounds(x, Int64Type) {
		t.Error("subtypeByBounds")
	}
	// try_subtype_by_bounds (subtype.c:2677-2678)
	if !e.trySubtypeByBounds(x, y) {
		t.Error("trySubtypeByBounds(x, y)")
	}

	// reachable_var (subtype.c:2734-2759): x <: x does not loop
	e.vars = &varBinding{v: x, lb: Bottom, ub: x}
	if e.reachableVar(x, y) {
		t.Error("reachableVar through a cycle")
	}

	// intersect_var (subtype.c:2779-2871): a bound that reaches the variable
	// gives a back unchanged (2793-2794) ...
	e.vars = &varBinding{v: x, lb: x, ub: AnyType}
	if got := e.intersectVar(x, Int64Type, false, 0); got != any(Int64Type) {
		t.Errorf("intersectVar with a reachable bound: %s", Show(got))
	}
	// ... and under triangular, an upper bound x == Ref{x} is unsatisfiable
	// (2851-2854)
	e.vars = &varBinding{v: x, lb: Bottom, ub: AnyType, constraintkind: 1}
	e.triangular = 1
	if got := e.intersectVar(x, mk(t, RefType, x), false, 0); got != any(Bottom) {
		t.Errorf("intersectVar of Ref{x} against x: %s", Show(got))
	}
	e.triangular = 0
	e.vars = nil

	// the step bound (subtype.c:3984 counts calls too)
	e.calls = maxSubtypeCalls
	mustPanic(t, "intersect over the step bound", func() { e.intersect(Int64Type, StringType, 0) })
	e.calls = 0

	// intersect_type_type (subtype.c:3829-3849): Type{Int} and its type
	if got := e.intersectTypeType(mk(t, TypeType, Int64Type), DataTypeType); got != any(mk(t, TypeType, Int64Type)) {
		t.Errorf("Type{Int} against DataType: %s", Show(got))
	}
	// super_of: a datatype with no supertype set (jl_supertype) is below Any
	if superOf(&DataType{}) != AnyType {
		t.Error("superOf")
	}

	// intersect on two non-types that are egal (subtype.c:4262-4266)
	e.loffset = 1 // skips the subtype shortcut (subtype.c:4004)
	if got := e.intersect(tupleValue(Symbol("a")), tupleValue(Symbol("a")), 2); !Egal(got, tupleValue(Symbol("a"))) {
		t.Errorf("intersect of equal tuple values: %v", got)
	}
	e.loffset = 0
	// ... and a union against a union that it contains
	u2 := &Union{A: Int64Type, B: StringType}
	u3 := &Union{A: u2, B: NothingType}
	e.loffset = 1 // skips the subtype shortcut (subtype.c:4004)
	if got := e.intersect(u2, u3, 0); got != any(u2) {
		t.Errorf("Union{Int,String} against Union{Int,String,Nothing}: %s", Show(got))
	}
	e.loffset = 0

	// intersect_vararg_length asserts N is a variable (subtype.c:3544)
	mustPanic(t, "varargLengthVar", func() { varargLengthVar(&Union{A: Int64Type, B: StringType}) })
	// intersect_tuple of two empty tuples (subtype.c:3614-3615)
	if got := e.intersectTuple(EmptyTupleType, EmptyTupleType, 0); got != any(EmptyTupleType) {
		t.Errorf("Tuple{} against Tuple{}: %s", Show(got))
	}
	// a Vararg too long to allocate (julia would allocate it)
	huge := &DataType{Name: TupleTypeName, Parameters: []any{&Vararg{T: Int64Type, N: int64(maxTupleFill + 1)}}}
	mustPanic(t, "intersectTuple over the length bound", func() { e.intersectTuple(huge, mk(t, AnyTupleType, Int64Type).(*DataType), 0) })

	// newTypeVarChecked raises on bounds that are not types
	mustPanic(t, "newTypeVarChecked", func() { newTypeVarChecked("v", int64(1), AnyType) })

	// has_typevar_via_flatten_env on a Vararg (subtype.c:2967-3008)
	if !hasTypeVarViaFlattenEnv(&Vararg{T: x, N: y}, y, nil, nil) || !hasTypeVarViaFlattenEnv(&Vararg{T: x}, x, nil, nil) ||
		hasTypeVarViaFlattenEnv(&Vararg{T: Int64Type}, x, nil, nil) {
		t.Error("hasTypeVarViaFlattenEnv on a Vararg")
	}

	// intersect_var_ccheck_in_env at a nonzero offset (subtype.c:3908-3919):
	// closed bounds are checked in an empty environment, the second pair
	// with the offset flipped
	e.loffset = 1
	if !e.intersectVarCcheckInEnv(Int64Type, IntegerType, Int64Type, IntegerType, false) {
		t.Error("Int..Integer against Int..Integer")
	}
	if e.intersectVarCcheckInEnv(Int64Type, IntegerType, StringType, IntegerType, false) {
		t.Error("String below Integer")
	}
	if e.intersectVarCcheckInEnv(StringType, IntegerType, Int64Type, IntegerType, false) {
		t.Error("Int against String..Integer")
	}
	e.loffset = 0
}

// omit_bad_union (subtype.c:2913-2964): u without the union members that
// mention t.
func TestWhiteboxOmitBadUnion(t *testing.T) {
	tv := fresh("t")
	bad := mk(t, RefType, tv) // mentions t, is no union: Union{}
	if got := omitBadUnion(bad, tv); got != any(Bottom) {
		t.Errorf("Ref{t}: %s", Show(got))
	}
	if got := omitBadUnion(&Union{A: Int64Type, B: bad}, tv); got != any(Int64Type) {
		t.Errorf("Union{Int, Ref{t}}: %s", Show(got))
	}
	// the body loses S: the UnionAll is its body (2924-2925)
	S := fresh("S")
	if got := omitBadUnion(&UnionAll{Var: S, Body: &Union{A: Int64Type, B: bad}}, tv); got != any(Int64Type) {
		t.Errorf("Union{Int, Ref{t}} where S: %s", Show(got))
	}
	// a lower bound mentioning t (2927-2928)
	L := &TypeVar{Name: "L", LB: bad, UB: AnyType}
	if got := omitBadUnion(&UnionAll{Var: L, Body: &Union{A: L, B: bad}}, tv); got != any(Bottom) {
		t.Errorf("L>:Ref{t}: %s", Show(got))
	}
	// an upper bound that vanishes under a lower bound that is not Union{}
	// (2932-2933)
	V := &TypeVar{Name: "V", LB: Int64Type, UB: bad}
	if got := omitBadUnion(&UnionAll{Var: V, Body: &Union{A: mk(t, RefType, V), B: bad}}, tv); got != any(Bottom) {
		t.Errorf("Int<:V<:Ref{t}: %s", Show(got))
	}
	// Union{}<:V<:Ref{t}: the bounds become equal, V := Union{} (2935-2936)
	E := &TypeVar{Name: "E", LB: Bottom, UB: bad}
	if got := omitBadUnion(&UnionAll{Var: E, Body: &Union{A: mk(t, RefType, E), B: bad}}, tv); got != any(mk(t, RefType, Bottom)) {
		t.Errorf("V<:Ref{t} in Ref{V}: %s", Show(got))
	}
	// ... which Tuple{Union{}} refuses: Union{} (2937-2938)
	if got := omitBadUnion(&UnionAll{Var: E, Body: &Union{A: mk(t, AnyTupleType, E), B: bad}}, tv); got != any(Bottom) {
		t.Errorf("V<:Ref{t} in Tuple{V}: %s", Show(got))
	}
	// the upper bound shrinks to Integer: a new variable Int<:V<:Integer
	// replaces V in the body (2941-2945)
	W := &TypeVar{Name: "W", LB: Int64Type, UB: &Union{A: IntegerType, B: bad}}
	got, ok := omitBadUnion(&UnionAll{Var: W, Body: &Union{A: mk(t, RefType, W), B: bad}}, tv).(*UnionAll)
	if !ok || got.Var == W || got.Var.LB != any(Int64Type) || got.Var.UB != any(IntegerType) || Show(got.Body) != Show(mk(t, RefType, got.Var)) {
		t.Errorf("Int<:W<:Union{Integer,Ref{t}}: %v", got)
	}
	// an upper bound that does not mention t stays: the same variable
	K := &TypeVar{Name: "K", LB: Int64Type, UB: IntegerType}
	got, ok = omitBadUnion(&UnionAll{Var: K, Body: &Union{A: mk(t, RefType, K), B: bad}}, tv).(*UnionAll)
	if !ok || got.Var != K || Show(got.Body) != Show(mk(t, RefType, K)) {
		t.Errorf("Int<:K<:Integer: %v", got)
	}
}

// switch_union_tuple (subtype.c:4460-4530), rewrap_unionall_ and
// might_intersect_concrete (subtype.c:4535-4562, jltypes.c:1599-1608).
func TestWhiteboxSwitchUnionTuple(t *testing.T) {
	tup := func(ps ...any) any { return mk(t, AnyTupleType, ps...) }
	same := func(what string, got, want any) {
		t.Helper()
		if got == nil || want == nil {
			if got != want {
				t.Errorf("%s: %v, want %v", what, got, want)
			}
			return
		}
		if !TypesEqual(got, want) {
			t.Errorf("%s: %s, want %s", what, Show(got), Show(want))
		}
	}
	union := TypeUnion(Int64Type, StringType)
	// tuples of one length: the tuple of pointwise unions (4519-4527)
	same("Tuple{Int}, Tuple{String}", switchUnionTuple(tup(Int64Type), tup(StringType)), tup(union))
	// not tuples, or tuples of different lengths (4512-4517)
	same("Int, Int", switchUnionTuple(Int64Type, Int64Type), nil)
	same("Tuple{Int}, Tuple{Int,Int}", switchUnionTuple(tup(Int64Type), tup(Int64Type, Int64Type)), nil)
	same("a Vararg tuple", switchUnionTuple(tup(&Vararg{T: Int64Type}), tup(Int64Type)), nil)
	// a union of tuples on the left (4494-4501) or on the right (4503-4510)
	two := &Union{A: tup(Int64Type), B: tup(StringType)}
	three := TypeUnion(union, NothingType)
	same("Union{Tuple{Int},Tuple{String}}, Tuple{Nothing}", switchUnionTuple(two, tup(NothingType)), tup(three))
	same("Tuple{Nothing}, Union{Tuple{Int},Tuple{String}}", switchUnionTuple(tup(NothingType), two), tup(three))
	// ... whose members cannot be joined
	same("Union{Int,String}, Tuple{Int}", switchUnionTuple(union, tup(Int64Type)), nil)
	same("Tuple{Int}, Union{Int,String}", switchUnionTuple(tup(Int64Type), union), nil)
	// UnionAlls: two with equal bounds share the variable (4464-4475); one
	// alone wraps the answer (4477-4492); a failed answer stays failed
	S, R := fresh("S"), fresh("R")
	uS := &UnionAll{Var: S, Body: tup(S)}
	uR := &UnionAll{Var: R, Body: tup(R)}
	same("Tuple{S} where S, Tuple{R} where R", switchUnionTuple(uS, uR), uS)
	same("Tuple{S} where S, Tuple{Int}", switchUnionTuple(uS, tup(Int64Type)), &UnionAll{Var: S, Body: tup(TypeUnion(S, Int64Type))})
	same("Tuple{Int}, Tuple{S} where S", switchUnionTuple(tup(Int64Type), uS), &UnionAll{Var: S, Body: tup(TypeUnion(Int64Type, S))})
	same("Ref{S} where S, Tuple{Int}", switchUnionTuple(&UnionAll{Var: S, Body: mk(t, RefType, S)}, tup(Int64Type)), nil)
	same("Tuple{Int}, Ref{S} where S", switchUnionTuple(tup(Int64Type), &UnionAll{Var: S, Body: mk(t, RefType, S)}), nil)
	// the UnionAlls with different bounds fall to the one-sided case
	B := &TypeVar{Name: "B", LB: Bottom, UB: IntegerType}
	same("Tuple{S} where S, Tuple{B} where B<:Integer", switchUnionTuple(uS, &UnionAll{Var: B, Body: tup(B)}),
		&UnionAll{Var: S, Body: &UnionAll{Var: B, Body: tup(TypeUnion(S, B))}})

	// rewrap_unionall_ puts t under u's variables, unsimplified
	rw, _ := rewrapUnionAllRaw(Int64Type, &UnionAll{Var: S, Body: &UnionAll{Var: R, Body: tup(S, R)}}).(*UnionAll)
	if rw == nil || rw.Var != S {
		t.Fatalf("rewrapUnionAllRaw: %v", rw)
	}
	if in, _ := rw.Body.(*UnionAll); in == nil || in.Var != R || in.Body != any(Int64Type) {
		t.Errorf("rewrapUnionAllRaw body: %v", rw.Body)
	}
	// might_intersect_concrete: a tuple with a Union{} parameter might
	// (4555-4556), a datatype that is no tuple does not
	if !mightIntersectConcrete(&DataType{Name: TupleTypeName, Parameters: []any{Bottom}}) ||
		mightIntersectConcrete(&DataType{Name: RefType.Body.(*DataType).Name, Parameters: []any{Bottom}}) {
		t.Error("mightIntersectConcrete with Union{}")
	}
}

// intersect of two instances of one type whose parameters intersect to an
// instance julia cannot build (JL_TRY around jl_apply_type, subtype.c:4244).
func TestWhiteboxIntersectSameNameInvalid(t *testing.T) {
	lim, _ := limBody(t)
	A, B := fresh("A"), fresh("B")
	e := intersectEnv()
	defer putStenv(e)
	// A and B are existential variables of the query
	e.vars = &varBinding{v: B, lb: Bottom, ub: AnyType, right: true,
		prev: &varBinding{v: A, lb: Bottom, ub: AnyType, right: true}}
	// Lim{Pair{A, Int}} and Lim{Pair{Int, B}} intersect in Lim{Pair{Int, Int}},
	// where Pair{Int, Int} is no Integer
	xd := &DataType{Name: lim.Name, Parameters: []any{mk(t, PairType, A, Int64Type)}}
	yd := &DataType{Name: lim.Name, Parameters: []any{mk(t, PairType, Int64Type, B)}}
	if got := e.intersectSameName(xd, yd); got != any(Bottom) {
		t.Errorf("Lim{Pair{A,Int}} against Lim{Pair{Int,B}}: %s", Show(got))
	}
}

// bindVar pushes a binding onto e's environment.
func bindVar(e *stenv, v *TypeVar, lb, ub any, depth int) *varBinding {
	b := &varBinding{v: v, lb: lb, ub: ub, depth0: depth, prev: e.vars}
	e.vars = b
	return b
}

// intersect_vars with param 2 (subtype.c:4001-4131) on hand-built bindings.
func TestWhiteboxIntersectVars(t *testing.T) {
	x, y := fresh("x"), fresh("y")
	e := intersectEnv()
	defer putStenv(e)

	// variables of different depth: y when y's bounds are consistent, else
	// Union{} (4006-4009)
	bindVar(e, y, Bottom, Bottom, 0)
	bindVar(e, x, Bottom, AnyType, 1)
	if got := e.intersectVars(x, y, 2); got != any(y) {
		t.Errorf("depth 1 against depth 0, y == Union{}: %s", Show(got))
	}
	e.vars = nil
	bindVar(e, y, Bottom, Int64Type, 0)
	bindVar(e, x, Bottom, AnyType, 1)
	if got := e.intersectVars(x, y, 2); got != any(Bottom) {
		t.Errorf("depth 1 against depth 0, y <: Int: %s", Show(got))
	}

	// y is the constant 3, x an unbound variable (4050-4064): x takes the
	// constant, shifted by the offset of the Vararg lengths
	yc := fresh("yc")
	for _, c := range []struct {
		off  int64
		xlb  any
		want any
	}{
		{0, Bottom, int64(3)},
		{1, Bottom, int64(3)}, // the shorter Vararg's length is returned
		{-1, Bottom, int64(2)},
		{0, Int64Type, any(Bottom)}, // x has other bounds and is not egal to 3
	} {
		e.vars = nil
		bindVar(e, yc, int64(3), int64(3), 0)
		e.loffset = c.off
		if got := e.intersectVars(&TypeVar{Name: "xu", LB: c.xlb, UB: AnyType}, yc, 2); got != c.want {
			t.Errorf("offset %d, x lb %s against y == 3: %v, want %v", c.off, Show(c.xlb), got, c.want)
		}
	}
	// not an Int: no shifted constant (4056-4063)
	e.vars = nil
	bindVar(e, yc, Symbol("s"), Symbol("s"), 0)
	e.loffset = 1
	if got := e.intersectVars(fresh("xu"), yc, 2); got != any(Bottom) {
		t.Errorf("offset 1 against y == :s: %v", got)
	}

	// x is the constant 3, y an unbound variable (4065-4079)
	xc := fresh("xc")
	for _, c := range []struct {
		off  int64
		ylb  any
		want any
	}{
		{0, Bottom, int64(3)},
		{-1, Bottom, int64(3)}, // the shorter Vararg's length
		// julia unboxes ylb here (subtype.c:4075), which is Union{}: undefined;
		// the port subtracts from xlb, the Int the branch tested
		{1, Bottom, int64(2)},
		{0, Int64Type, any(Bottom)}, // y has other bounds
	} {
		e.vars = nil
		bindVar(e, xc, int64(3), int64(3), 0)
		e.loffset = c.off
		if got := e.intersectVars(xc, &TypeVar{Name: "yu", LB: c.ylb, UB: AnyType}, 2); got != c.want {
			t.Errorf("offset %d, x == 3 against y lb %s: %v, want %v", c.off, Show(c.ylb), got, c.want)
		}
	}
	e.vars = nil
	bindVar(e, xc, Symbol("s"), Symbol("s"), 0)
	e.loffset = 1
	if got := e.intersectVars(xc, fresh("yu"), 2); got != any(Bottom) {
		t.Errorf("offset 1, x == :s: %v", got)
	}
	e.loffset = 0
}

// merge_env (subtype.c:4273-4343): the innervars of a choice and its
// max_offset merge into the saved environment.
func TestWhiteboxMergeEnv(t *testing.T) {
	x, i1, i2 := fresh("x"), fresh("i1"), fresh("i2")
	e := intersectEnv()
	defer putStenv(e)
	b := bindVar(e, x, Bottom, AnyType, 0)
	b.maxOffset = 5
	se, me := e.newSavedEnv(), e.newSavedEnv()
	e.saveEnv(se)
	e.saveEnv(me)
	// no innervars saved: the binding's array is taken as it is (4326-4327)
	first := &[]*TypeVar{i1}
	b.innervars = first
	b.maxOffset = 2                 // below the saved 5 (4335-4336)
	b.occursInv, b.occursCov = 1, 2 // above the saved 0 (4330-4333)
	if n := e.mergeEnv(me, se, 1); n != 2 {
		t.Errorf("mergeEnv count %d", n)
	}
	if me.inner[0] != first || me.buf[2] != 2 || me.buf[0] != 1 || me.buf[1] != 2 {
		t.Errorf("merged innervars %v, buffer %v", me.inner[0], me.buf)
	}
	// another array: appended to the saved one (4324-4325)
	b.innervars = &[]*TypeVar{i2}
	e.mergeEnv(me, se, 2)
	if got := *me.inner[0]; len(got) != 2 || got[0] != i1 || got[1] != i2 {
		t.Errorf("appended innervars %v", got)
	}
	// count 0 only saves (4275-4278)
	if n := e.mergeEnv(me, se, 0); n != 1 {
		t.Errorf("mergeEnv count 0: %d", n)
	}
}

// always_occurs_cov (subtype.c:3428-3458): nothing is certain inside an
// invariant parameter.
func TestWhiteboxAlwaysOccursCov(t *testing.T) {
	x := fresh("x")
	tup := mk(t, AnyTupleType, x)
	if alwaysOccursCov(mk(t, RefType, x), x, 2) || alwaysOccursCov(tup, x, 2) {
		t.Error("param 2")
	}
	if !alwaysOccursCov(tup, x, 0) || alwaysOccursCov(mk(t, RefType, x), x, 0) || !alwaysOccursCov(x, x, 1) || alwaysOccursCov(x, x, 0) {
		t.Error("param 0 and 1")
	}
}

// intersect_unionall_ (subtype.c:3361-3426) when the variable's upper bound
// is circular and the lower bound is not Union{}: omit_bad_union leaves
// Union{} (3418-3420).
func TestWhiteboxUnionAllOnceCircular(t *testing.T) {
	X := fresh("X")
	e := intersectEnv()
	defer putStenv(e)
	vb := &varBinding{v: X, lb: Int64Type, ub: mk(t, RefType, X)}
	if got := e.intersectUnionAllOnce(Int64Type, &UnionAll{Var: X, Body: Int64Type}, false, 0, vb); got != any(Bottom) {
		t.Errorf("Int<:X<:Ref{X}: %s", Show(got))
	}
}

// subtype of a union against a variable that is set equal to another
// variable follows the chain in intersection (subtype.c:1403-1406).
func TestWhiteboxSubtypeVarChain(t *testing.T) {
	Y, Z := fresh("Y"), fresh("Z")
	e := intersectEnv()
	defer putStenv(e)
	zb := bindVar(e, Z, Bottom, AnyType, 0)
	zb.right = true
	yb := bindVar(e, Y, Z, Z, 0)
	yb.right = true
	u := &Union{A: Int64Type, B: StringType}
	if !e.subtype(u, Y, 0) {
		t.Fatal("Union{Int,String} <: Y with Y == Z")
	}
	if zb.lb != any(u) {
		t.Errorf("the lower bound of Z is %s", Show(zb.lb))
	}
}

// finish_unionall (subtype.c:3011-3359) on hand-built environments.
func TestWhiteboxFinishUnionAll(t *testing.T) {
	limBody(t)
	e := intersectEnv()
	defer putStenv(e)
	reset := func() { e.vars = nil }

	// an Int upper bound under a variable lower bound is the value (3017-3019)
	N, M := fresh("N"), fresh("M")
	if got := e.finishUnionAll(mk(t, ValType, N), &varBinding{v: N, lb: M, ub: int64(2)}, nil); Show(got) != "Val{2}" {
		t.Errorf("N == 2 in Val{N}: %s", Show(got))
	}

	// the lower bound of the variable is another variable whose lower bound
	// mentions this one: circular (3117-3121)
	X, Y := fresh("X"), fresh("Y")
	reset()
	bindVar(e, Y, mk(t, RefType, X), AnyType, 0)
	if got := e.finishUnionAll(Int64Type, &varBinding{v: X, lb: Y, ub: AnyType}, nil); got != any(Bottom) {
		t.Errorf("circular lower bounds: %s", Show(got))
	}

	// a value that no outer bound can take: Union{} (3122-3128, 3147-3153)
	LX := &TypeVar{Name: "X", LB: Bottom, UB: IntegerType}
	limX := mk(t, limW, LX)
	reset()
	bindVar(e, Y, limX, AnyType, 0)
	if got := e.finishUnionAll(Int64Type, &varBinding{v: LX, lb: StringType, ub: StringType}, nil); got != any(Bottom) {
		t.Errorf("X == String in the lower bound Lim{X}: %s", Show(got))
	}
	reset()
	bindVar(e, Y, Bottom, limX, 0)
	if got := e.finishUnionAll(Int64Type, &varBinding{v: LX, lb: StringType, ub: StringType}, nil); got != any(Bottom) {
		t.Errorf("X == String in the upper bound Lim{X}: %s", Show(got))
	}

	// an outer variable whose lower bound is this one takes this one's
	// lower bound (3130-3132)
	reset()
	yb := bindVar(e, Y, X, AnyType, 0)
	got, ok := e.finishUnionAll(mk(t, RefType, X), &varBinding{v: X, lb: Int64Type, ub: IntegerType}, nil).(*UnionAll)
	if yb.lb != any(Int64Type) || !ok || got.Var.LB != any(Int64Type) || got.Var.UB != any(IntegerType) {
		t.Errorf("Y>:X: Y.lb %s, result %v", Show(yb.lb), got)
	}

	// this variable's upper bound is the outer variable, whose upper bound
	// is bad: Union{} unless its lower bound is (3139-3145)
	reset()
	bindVar(e, Y, Int64Type, mk(t, RefType, X), 0)
	if got := e.finishUnionAll(Int64Type, &varBinding{v: X, lb: Bottom, ub: Y}, nil); got != any(Bottom) {
		t.Errorf("X<:Y<:Ref{X}: %s", Show(got))
	}

	// the inner variables of a cycle cannot be sorted (3219-3249)
	I1 := &TypeVar{Name: "I1", LB: Bottom, UB: AnyType}
	I2 := &TypeVar{Name: "I2", LB: Bottom, UB: mk(t, RefType, I1)}
	I1.UB = mk(t, RefType, I2)
	reset()
	b := bindVar(e, Y, Bottom, AnyType, 0)
	b.innervars = &[]*TypeVar{I1, I2}
	mustPanic(t, "cyclic inner variables", func() { e.finishUnionAll(Int64Type, &varBinding{v: X, lb: Bottom, ub: AnyType}, nil) })

	// an inner variable whose bounds change is replaced everywhere
	// (3253-3279): X's upper bound I<:X becomes Integer
	I := &TypeVar{Name: "I", LB: Bottom, UB: X}
	reset()
	yb = bindVar(e, Y, mk(t, RefType, I), AnyType, 0)
	vb := &varBinding{v: X, lb: mk(t, RefType, I), ub: IntegerType, innervars: &[]*TypeVar{I}}
	res, ok := e.finishUnionAll(mk(t, PairType, X, I), vb, nil).(*UnionAll)
	if !ok || res.Var.UB != any(IntegerType) {
		t.Fatalf("result %v", res)
	}
	pair := res.Body.(*DataType)
	ni, _ := pair.Parameters[1].(*TypeVar)
	if ni == nil || ni == I || ni.Name != "I" || ni.LB != any(Bottom) || ni.UB != any(IntegerType) || pair.Parameters[0] != any(res.Var) {
		t.Fatalf("result body %s", Show(pair))
	}
	if Show(yb.lb) != Show(mk(t, RefType, ni)) || res.Var.LB == nil || Show(res.Var.LB) != Show(mk(t, RefType, ni)) {
		t.Errorf("Y.lb %s, X's lower bound %s", Show(yb.lb), Show(res.Var.LB))
	}
	if yb.innervars == nil || len(*yb.innervars) != 1 || (*yb.innervars)[0] != ni {
		t.Errorf("Y's inner variables %v", yb.innervars)
	}
	reset()
}

// the diagonal rule (subtype.c:3394-3400): a variable that must be
// concrete cannot have an abstract lower bound
func TestWhiteboxUnionAllOnceDiagonal(t *testing.T) {
	X := fresh("X")
	e := intersectEnv()
	defer putStenv(e)
	vb := &varBinding{v: X, lb: RealType, ub: AnyType, concrete: true}
	if got := e.intersectUnionAllOnce(Int64Type, &UnionAll{Var: X, Body: Int64Type}, false, 0, vb); got != any(Bottom) {
		t.Errorf("concrete Real<:X: %s", Show(got))
	}
	// a leaf lower bound is fine
	e.vars = nil
	vb = &varBinding{v: X, lb: Int64Type, ub: AnyType, concrete: true}
	if got := e.intersectUnionAllOnce(Int64Type, &UnionAll{Var: X, Body: Int64Type}, false, 0, vb); got != any(Int64Type) {
		t.Errorf("concrete Int<:X: %s", Show(got))
	}
}
