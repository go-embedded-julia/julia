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
func TestWorkBoundIsAnError(t *testing.T) {
	tab := &Table{Name: "f"}
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	// signatures with unions on both sides of their mutual comparison, so
	// that specificity needs more steps than matching the call: some bound
	// fails in matching, some later bound in specificity
	S := &core.TypeVar{Name: "S", LB: core.Bottom, UB: core.AnyType}
	vecInt := mustApply(t, core.ArrayType, core.Int64Type, int64(1))
	u := func(v *core.TypeVar, arr any) any {
		return core.TypeUnion(arr, mustApply(t, core.RefType, v), mustApply(t, core.PairType, v, v))
	}
	tab.Add(core.TypeUnionAll(T, mustApply(t, core.AnyTupleType,
		u(T, mustApply(t, core.ArrayType, T, int64(1))), core.TypeUnion(T, core.NothingType))))
	tab.Add(core.TypeUnionAll(S, mustApply(t, core.AnyTupleType,
		u(S, mustApply(t, core.AbstractArrayType, S, int64(1))), core.TypeUnion(S, core.NothingType))))
	call := mustApply(t, core.AnyTupleType, vecInt, core.Int64Type)
	saved := core.SetMaxSubtypeSteps(1)
	defer core.SetMaxSubtypeSteps(saved)
	failures := 0
	for n := 1; n < 1000; n++ {
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
	core.SetMaxSubtypeSteps(saved)
	if failures == 0 {
		t.Fatal("no bound made the lookup fail: the test did not exercise the error paths")
	}
	if _, _, err := tab.Lookup(call, tab.World()); err != nil {
		t.Fatalf("under the real bound: %v", err)
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
