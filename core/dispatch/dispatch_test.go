package dispatch

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// World ages: a redefinition is a new method in a new world; the old one
// stays the answer, and the visible method, in the worlds before it.
func TestWorldAges(t *testing.T) {
	tab := &Table{Name: "f"}
	m1, _ := tab.Add(mustApply(t, core.AnyTupleType, core.Int64Type))
	w1 := tab.World()
	m2, _ := tab.Add(mustApply(t, core.AnyTupleType, core.Int64Type))
	w2 := tab.World()
	call := mustApply(t, core.AnyTupleType, core.Int64Type)
	if m, _, err := tab.Lookup(call, w1); err != nil || m != m1 {
		t.Fatalf("world %d: %v, %v; want the first definition", w1, m, err)
	}
	if m, _, err := tab.Lookup(call, w2); err != nil || m != m2 {
		t.Fatalf("world %d: %v, %v; want the redefinition", w2, m, err)
	}
	if ms := tab.Methods(w2); len(ms) != 1 || ms[0] != m2 {
		t.Fatalf("methods in world %d: %v; want only the redefinition", w2, ms)
	}
	if ms := tab.Methods(w1); len(ms) != 1 || ms[0] != m1 {
		t.Fatalf("methods in world %d: %v; want only the first definition", w1, ms)
	}
	if m, _, err := tab.Lookup(call, 0); !errors.Is(err, ErrNoMethod) || m != nil {
		t.Fatalf("world 0: %v, %v; want no method", m, err)
	}
}

// A disabled method is visible up to the world it was disabled in, and
// only once disabled.
func TestDisable(t *testing.T) {
	tab := &Table{Name: "f"}
	m, _ := tab.Add(mustApply(t, core.AnyTupleType, core.Int64Type))
	w := tab.World()
	if !tab.Disable(m, w) || tab.Disable(m, w) {
		t.Fatal("Disable")
	}
	if len(tab.Methods(w)) != 1 || len(tab.Methods(w+1)) != 0 {
		t.Error("visibility of a disabled method")
	}
}

// Define takes the world from the caller's global counter and carries the
// body; a world that is not newer than the table's is refused.
func TestDefineInAGlobalWorld(t *testing.T) {
	tab := &Table{Name: "f"}
	sig := mustApply(t, core.AnyTupleType, core.Int64Type)
	m, err := tab.Define(sig, 7, "body")
	if err != nil || m.PrimaryWorld != 7 || m.Def != "body" || tab.World() != 7 {
		t.Fatalf("Define: %+v, %v, table world %d", m, err, tab.World())
	}
	if _, err := tab.Define(sig, 7, nil); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Fatalf("a stale world was accepted: %v", err)
	}
	if got, _, err := tab.Lookup(sig, 6); m == got || !errors.Is(err, ErrNoMethod) {
		t.Fatalf("world 6 sees a method of world 7: %v", err)
	}
}

// A table made to answer from its methods alone, as a loaded one is, answers
// as the table it was made from did, in every world: with methods replaced
// and methods deleted in later worlds.
func TestResetAfterLoad(t *testing.T) {
	tab := &Table{Name: "f"}
	tup := func(ts ...any) any { return mustApply(t, core.AnyTupleType, ts...) }
	tab.Add(tup(core.Int64Type))  // 0
	tab.Add(tup(core.NumberType)) // 1
	tab.Add(tup(core.Int64Type))  // 2: replaces the first
	m3, _ := tab.Add(tup(core.Float64Type))
	tab.Add(tup(core.AbstractFloatType)) // 4
	tab.Disable(m3, tab.World())         // the Float64 method is deleted
	tab.Add(tup(core.RealType))
	sigs := []any{tup(core.Int64Type), tup(core.Float64Type), tup(core.Int32Type), tup(core.StringType)}
	answers := func() string {
		var b strings.Builder
		for w := uint64(0); w <= tab.World()+1; w++ {
			for _, s := range sigs {
				m, _, err := tab.Lookup(s, w)
				if err != nil {
					b.WriteString("-")
				} else {
					b.WriteString(string(rune('a' + m.Index)))
				}
			}
			b.WriteString("|")
		}
		return b.String()
	}
	before := answers()
	if !strings.Contains(before, "-") || strings.Count(before, "|") < 6 {
		t.Fatalf("the table answers too little to compare: %s", before)
	}
	tab.ResetAfterLoad()
	if got := answers(); got != before {
		t.Errorf("after a reset:\n got %s\nwant %s", got, before)
	}
	// a reset of a table that was never queried, and one that was reset
	fresh := &Table{Name: "g"}
	fresh.Add(tup(core.Int64Type))
	fresh.ResetAfterLoad()
	fresh.ResetAfterLoad()
	if m, _, err := fresh.Lookup(tup(core.Int64Type), fresh.World()); err != nil || m.Index != 0 {
		t.Errorf("a table reset twice: %v, %v", m, err)
	}
	// the events are those of the methods: an insertion each, and a disabling
	// for the deleted method alone (the replaced one has none)
	var ins, dis int
	for _, ev := range tab.events {
		if ev.disable {
			dis++
		} else {
			ins++
		}
	}
	if ins != len(tab.methods) || dis != 1 {
		t.Errorf("%d insertions and %d disablings for %d methods", ins, dis, len(tab.methods))
	}
}

// sameSig is type equality, and a failure of the algebra's work bound is not
// equality.
func TestSameSig(t *testing.T) {
	a := mustApply(t, core.AnyTupleType, core.Int64Type)
	if !sameSig(a, mustApply(t, core.AnyTupleType, core.Int64Type)) || sameSig(a, mustApply(t, core.AnyTupleType, core.Int32Type)) {
		t.Error("sameSig of equal and of different signatures")
	}
	// signatures whose comparison takes many steps: with each bound from 1
	// up, some fail it, and none of the comparisons says they are equal
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	S := &core.TypeVar{Name: "S", LB: core.Bottom, UB: core.AnyType}
	u := func(v *core.TypeVar, arr any) any {
		return core.TypeUnion(arr, mustApply(t, core.RefType, v), mustApply(t, core.PairType, v, v))
	}
	sig1 := core.TypeUnionAll(T, mustApply(t, core.AnyTupleType,
		u(T, mustApply(t, core.ArrayType, T, int64(1))), core.TypeUnion(T, core.NothingType)))
	sig2 := core.TypeUnionAll(S, mustApply(t, core.AnyTupleType,
		u(S, mustApply(t, core.AbstractArrayType, S, int64(1))), core.TypeUnion(S, core.NothingType)))
	saved := core.SetMaxSubtypeSteps(1)
	defer core.SetMaxSubtypeSteps(saved)
	for n := 1; n < 200; n++ {
		core.SetMaxSubtypeSteps(n)
		if sameSig(sig1, sig2) {
			t.Fatalf("bound %d: unequal signatures are equal", n)
		}
	}
}

// Static parameters come back from jl_subtype_env.
func TestStaticParameters(t *testing.T) {
	tab := &Table{Name: "f"}
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	sig := core.TypeUnionAll(T, mustApply(t, core.AnyTupleType, mustApply(t, core.ArrayType, T, int64(1)), T))
	if _, err := tab.Add(sig); err != nil {
		t.Fatal(err)
	}
	call := mustApply(t, core.AnyTupleType, mustApply(t, core.ArrayType, core.Int64Type, int64(1)), core.Int64Type)
	m, env, err := tab.Lookup(call, tab.World())
	if err != nil || len(env) != 1 || env[0] != any(core.Int64Type) {
		t.Fatalf("sparams %v, %v; want [Int64]", env, err)
	}
	// a repeated lookup finds the static parameters in the cache, the same
	// slice each time (no copy per call) with the same contents
	m2, env2, err := tab.Lookup(call, tab.World())
	m3, env3, err3 := tab.Lookup(call, tab.World())
	if err != nil || err3 != nil || m2 != m || m3 != m || len(env2) != 1 || env2[0] != any(core.Int64Type) || &env2[0] != &env3[0] {
		t.Errorf("cached sparams: %v %v, %v, %v", env2, env3, err, err3)
	}
	// Env: a method's parameters for another tuple, false outside its
	// signature (what invoke asks once it has chosen by a declared tuple)
	call8 := mustApply(t, core.AnyTupleType, mustApply(t, core.ArrayType, core.Int8Type, int64(1)), core.Int8Type)
	if env, ok, err := m.Env(call8); err != nil || !ok || env[0] != any(core.Int8Type) {
		t.Errorf("Env: %v, %v, %v; want [Int8]", env, ok, err)
	}
	if _, ok, err := m.Env(mustApply(t, core.AnyTupleType, core.Int8Type)); err != nil || ok {
		t.Errorf("Env outside the signature: %v, %v", ok, err)
	}
}

// The work bound of a subtyping query reaches Lookup as an error, never a
// panic, wherever it strikes: raising it step by step from 1 makes each
// call of core inside Lookup the one that fails, until the lookup succeeds.
// A table whose typemap failed to build stays failed (sync), so each bound
// gets a new table.
func TestWorkBoundIsAnError(t *testing.T) {
	var tab *Table
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	// signatures with unions on both sides of their mutual comparison, so
	// that specificity needs more steps than matching the call: some bound
	// fails in matching, some later bound in specificity
	S := &core.TypeVar{Name: "S", LB: core.Bottom, UB: core.AnyType}
	vecInt := mustApply(t, core.ArrayType, core.Int64Type, int64(1))
	u := func(v *core.TypeVar, arr any) any {
		return core.TypeUnion(arr, mustApply(t, core.RefType, v), mustApply(t, core.PairType, v, v))
	}
	sig1 := core.TypeUnionAll(T, mustApply(t, core.AnyTupleType,
		u(T, mustApply(t, core.ArrayType, T, int64(1))), core.TypeUnion(T, core.NothingType)))
	sig2 := core.TypeUnionAll(S, mustApply(t, core.AnyTupleType,
		u(S, mustApply(t, core.AbstractArrayType, S, int64(1))), core.TypeUnion(S, core.NothingType)))
	fresh := func() {
		tab = &Table{Name: "f"}
		tab.Add(sig1)
		tab.Add(sig2)
	}
	call := mustApply(t, core.AnyTupleType, vecInt, core.Int64Type)
	saved := core.SetMaxSubtypeSteps(1)
	defer core.SetMaxSubtypeSteps(saved)
	failures := 0
	for n := 1; n < 1000; n++ {
		core.SetMaxSubtypeSteps(saved)
		fresh()
		core.SetMaxSubtypeSteps(n)
		_, _, err := tab.Lookup(call, tab.World())
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "exceeded") {
			t.Fatalf("bound %d: %v; want the work-bound error", n, err)
		}
		failures++
	}
	// the scan, which names a failed lookup's error, fails alike
	scanFailures := 0
	for n := 1; n < 1000; n++ {
		core.SetMaxSubtypeSteps(saved)
		fresh()
		core.SetMaxSubtypeSteps(n)
		_, _, err := tab.lookup(call, tab.World())
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "exceeded") {
			t.Fatalf("scan, bound %d: %v; want the work-bound error", n, err)
		}
		scanFailures++
	}
	core.SetMaxSubtypeSteps(saved)
	fresh()
	if scanFailures == 0 {
		t.Fatal("no bound made the scan fail")
	}
	if failures == 0 {
		t.Fatal("no bound made the lookup fail: the test did not exercise the error paths")
	}
	if _, _, err := tab.Lookup(call, tab.World()); err != nil {
		t.Fatalf("under the real bound: %v", err)
	}
}

// The work bound striking while a definition looks for the method it
// replaces is an error, and the table keeps its world and methods.
func TestWorkBoundInDefine(t *testing.T) {
	tab := &Table{Name: "f"}
	// Tuple{T} where T and Tuple{Any}: equal types that only subtyping
	// tells equal
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	if _, err := tab.Add(core.TypeUnionAll(T, mustApply(t, core.AnyTupleType, T))); err != nil {
		t.Fatal(err)
	}
	w := tab.World()
	again := mustApply(t, core.AnyTupleType, core.AnyType)
	saved := core.SetMaxSubtypeSteps(1)
	_, err := tab.Add(again)
	core.SetMaxSubtypeSteps(saved)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("%v; want the work-bound error", err)
	}
	if tab.World() != w || len(tab.methods) != 1 || tab.methods[0].MaxWorld != ^uint64(0) {
		t.Fatalf("the table changed: world %d, %d methods", tab.World(), len(tab.methods))
	}
}

func TestBadInputs(t *testing.T) {
	tab := &Table{Name: "f"}
	if _, err := tab.Add(core.Int64Type); err == nil {
		t.Error("a non-tuple signature was accepted")
	}
	if _, _, err := tab.Lookup(core.RealType, 0); err == nil {
		t.Error("a non-tuple call was accepted")
	}
	if _, _, err := tab.Lookup(mustApply(t, core.AnyTupleType, core.DataTypeType), 0); !errors.Is(err, ErrNoMethod) {
		t.Errorf("a call tuple holding a kind: %v; want no method in an empty table", err)
	}
	if _, _, err := tab.Lookup(core.Int64Type, 0); err == nil {
		t.Error("a concrete non-tuple was accepted")
	}
}

// A type argument dispatches as Type{T} (jl_inst_arg_tuple_type): the call
// tuple is a dispatch tuple without being concrete, and a method on
// Type{Int64} is chosen over one on DataType.
func TestTypeArgumentDispatch(t *testing.T) {
	tab := &Table{Name: "f"}
	onDataType, _ := tab.Add(mustApply(t, core.AnyTupleType, core.DataTypeType))
	onTypeInt, _ := tab.Add(mustApply(t, core.AnyTupleType, mustApply(t, core.TypeType, core.Int64Type)))
	call := mustApply(t, core.AnyTupleType, mustApply(t, core.TypeType, core.Int64Type))
	if m, _, err := tab.Lookup(call, tab.World()); err != nil || m != onTypeInt {
		t.Fatalf("f(Int64): %v, %v; want the Type{Int64} method", m, err)
	}
	other := mustApply(t, core.AnyTupleType, mustApply(t, core.TypeType, core.BoolType))
	if m, _, err := tab.Lookup(other, tab.World()); err != nil || m != onDataType {
		t.Fatalf("f(Bool): %v, %v; want the DataType method", m, err)
	}
}
