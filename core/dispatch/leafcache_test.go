package dispatch

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The leaf cache answers as a full lookup would in every world: an answer
// cached in a world stays that world's after later definitions and
// disables; the latest worlds see each change; a non-concrete tuple is not
// cached.
func TestLeafCacheKeepsWorlds(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType)
	intT := mustApply(t, core.AnyTupleType, core.Int64Type)
	call := intT.(*core.DataType)
	m1, _ := tab.Add(anyT)
	w1 := tab.World()
	lookup := func(w uint64) *Method {
		t.Helper()
		m, _, err := tab.Lookup(call, w)
		if err != nil {
			t.Fatalf("world %d: %v", w, err)
		}
		return m
	}
	// twice: the second answer comes from the cache
	if lookup(w1) != m1 || lookup(w1+5) != m1 || lookup(w1) != m1 {
		t.Fatal("before the second method")
	}
	m2, _ := tab.Add(intT)
	w2 := tab.World()
	if lookup(w1) != m1 || lookup(w2) != m2 || lookup(w2+5) != m2 || lookup(w2) != m2 {
		t.Fatal("after the second method")
	}
	// disabled from the next world on
	if !tab.Disable(m2, w2) {
		t.Fatal("disable")
	}
	if lookup(w1) != m1 || lookup(w2) != m2 || lookup(w2+1) != m1 || lookup(w2+9) != m1 {
		t.Fatal("after the disable")
	}
	m3, _ := tab.Add(intT)
	w3 := tab.World()
	if lookup(w3) != m3 || lookup(w3+9) != m3 || lookup(w2) != m2 {
		t.Fatal("after the third method")
	}
	if !tab.Disable(m3, w3) || lookup(w3) != m3 || lookup(w3+1) != m1 || lookup(w2) != m2 || lookup(w1) != m1 {
		t.Fatal("after disabling the third method")
	}
	// an abstract argument tuple goes around the cache
	if m, _, err := tab.Lookup(anyT, w3+1); err != nil || m != m1 {
		t.Fatalf("an abstract tuple: %v, %v", m, err)
	}
	for tt := range tab.leaf {
		if tt != call {
			t.Errorf("cached %s", core.Show(tt))
		}
	}
}

// A change at the first world of a cached answer drops it: no world of its
// range is left.
func TestLeafCacheDropsAnAnswerFromTheChangedWorld(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType)
	intT := mustApply(t, core.AnyTupleType, core.Int64Type)
	call := intT.(*core.DataType)
	m0, _ := tab.Add(anyT)
	m1, _ := tab.Add(intT)
	if !tab.Disable(m1, 5) {
		t.Fatal("disable")
	}
	// m0 from world 6 on
	if m, _, err := tab.Lookup(call, 10); err != nil || m != m0 {
		t.Fatalf("%v, %v", m, err)
	}
	if es := tab.leaf[call]; len(es) != 1 || es[0].minWorld != 6 {
		t.Fatalf("cached %+v", es)
	}
	m2, err := tab.Define(intT, 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range tab.leaf[call] {
		if e.minWorld <= tab.last(e) {
			t.Fatalf("the answer of worlds 6 on still answers up to %d", tab.last(e))
		}
	}
	if m, _, err := tab.Lookup(call, 10); err != nil || m != m2 {
		t.Fatalf("%v, %v", m, err)
	}
	// caching m2 dropped the entry no world was left to
	if es := tab.leaf[call]; len(es) != 1 || es[0].m != m2 {
		t.Fatalf("cached %+v", es)
	}
}

// A change at an older world than an earlier one still ends the entries
// cached before both at the older world, as visiting them at each change
// did: each entry ends before the oldest change made after it.
func TestLeafCacheEndsAtTheOldestLaterChange(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType)
	intT := mustApply(t, core.AnyTupleType, core.Int64Type)
	call := intT.(*core.DataType)
	m0, _ := tab.Add(anyT)
	if m, _, err := tab.Lookup(call, 1); err != nil || m != m0 {
		t.Fatalf("%v, %v", m, err)
	}
	if _, err := tab.Define(intT, 10, nil); err != nil {
		t.Fatal(err)
	}
	if !tab.Disable(m0, 3) {
		t.Fatal("disable")
	}
	es := tab.leaf[call]
	if len(es) != 1 || tab.last(es[0]) != 3 {
		t.Fatalf("cached %+v, last %d", es, tab.last(es[0]))
	}
	if m, _, err := tab.Lookup(call, 3); err != nil || m != m0 {
		t.Fatalf("world 3: %v, %v", m, err)
	}
	if _, _, err := tab.Lookup(call, 5); err == nil {
		t.Fatal("world 5: m0 is disabled and the Int64 method comes at 10")
	}
}

// A typemap that misses a method the scan sees is an error, never a guess.
func TestLookupReportsATypemapDisagreement(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType)
	call := mustApply(t, core.AnyTupleType, core.Int64Type).(*core.DataType)
	// a method the typemap never hears of
	tab.methods = append(tab.methods, &Method{Sig: anyT, PrimaryWorld: 1, MaxWorld: ^uint64(0)})
	tab.world = 1
	if _, _, err := tab.Lookup(call, 1); err == nil || !strings.Contains(err.Error(), "the scan chose") {
		t.Fatalf("%v; want the disagreement", err)
	}
}

// An answer found while the table changed is not cached: its world range
// may reach past the change.
func TestLeafCacheSkipsARacedAnswer(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType)
	call := mustApply(t, core.AnyTupleType, core.Int64Type).(*core.DataType)
	m1, _ := tab.Add(anyT)
	next := tab.World() + 1
	raceHook = func() { tab.mu.Lock(); tab.changed(next); tab.mu.Unlock() }
	defer func() { raceHook = func() {} }()
	if m, _, err := tab.Lookup(call, tab.World()); err != nil || m != m1 {
		t.Fatalf("%v, %v", m, err)
	}
	if len(tab.leaf) != 0 {
		t.Fatalf("cached %d answers", len(tab.leaf))
	}
}

// A tuple holding a Type{X} is no concrete type, so the type cache does not
// intern it and each call builds a new DataType: equal ones must still share
// one cache entry, as julia's leaf cache finds entries by the argument types.
func TestLeafCacheFindsEqualTuplesBuiltAnew(t *testing.T) {
	tab := &Table{Name: "f"}
	anyT := mustApply(t, core.AnyTupleType, core.AnyType, core.AnyType)
	m1, _ := tab.Add(anyT)
	build := func() *core.DataType {
		ty := mustApply(t, core.TypeType, core.Int64Type)
		return mustApply(t, core.AnyTupleType, ty, core.Int64Type).(*core.DataType)
	}
	a, b := build(), build()
	if a == b || !a.IsDispatchTuple {
		t.Skipf("the type cache interns Tuple{Type{Int64}, Int64} (same %v) or it is no dispatch tuple", a == b)
	}
	for _, c := range []*core.DataType{a, b, build()} {
		if m, _, err := tab.Lookup(c, tab.World()); err != nil || m != m1 {
			t.Fatalf("%v, %v", m, err)
		}
	}
	if len(tab.leaf) != 1 || len(tab.canon) != 1 {
		t.Fatalf("%d cached tuples, %d keys; want 1 and 1", len(tab.leaf), len(tab.canon))
	}
}

// canonical leaves a tuple with a parameter that is no DataType, or one with
// more parameters than a key holds, as its own key.
func TestCanonicalKeepsWhatItCannotName(t *testing.T) {
	tab := &Table{Name: "f"}
	u := mustApply(t, core.AnyTupleType, core.TypeUnion(core.Int64Type, core.AnyTupleType)).(*core.DataType)
	if tab.canonical(u) != u || len(tab.canon) != 0 {
		t.Error("a Union parameter")
	}
	ps := make([]any, maxKeyed+1)
	for i := range ps {
		ps[i] = core.Int64Type
	}
	long := mustApply(t, core.AnyTupleType, ps...).(*core.DataType)
	if tab.canonical(long) != long || len(tab.canon) != 0 {
		t.Error("a long tuple")
	}
}
