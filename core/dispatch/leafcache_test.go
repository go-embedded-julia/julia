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
	if _, ok := tab.leaf[call]; ok {
		t.Fatal("the answer of worlds 6 on stayed")
	}
	if m, _, err := tab.Lookup(call, 10); err != nil || m != m2 {
		t.Fatalf("%v, %v", m, err)
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
