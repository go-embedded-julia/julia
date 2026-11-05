package dispatch

import (
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
	for k := range tab.leaf {
		if k.tt != call {
			t.Errorf("cached %s", core.Show(k.tt))
		}
	}
}
