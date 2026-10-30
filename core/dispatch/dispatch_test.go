package dispatch

import (
	"errors"
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

// Static parameters come back from jl_subtype_env.
func TestStaticParameters(t *testing.T) {
	tab := &Table{Name: "f"}
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	sig := core.TypeUnionAll(T, mustApply(t, core.AnyTupleType, mustApply(t, core.ArrayType, T, int64(1)), T))
	if _, err := tab.Add(sig); err != nil {
		t.Fatal(err)
	}
	call := mustApply(t, core.AnyTupleType, mustApply(t, core.ArrayType, core.Int64Type, int64(1)), core.Int64Type)
	_, env, err := tab.Lookup(call, tab.World())
	if err != nil || len(env) != 1 || env[0] != any(core.Int64Type) {
		t.Fatalf("sparams %v, %v; want [Int64]", env, err)
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
	if _, _, err := tab.Lookup(mustApply(t, core.AnyTupleType, core.RealType), 0); err == nil {
		t.Error("an abstract call tuple was accepted")
	}
}
