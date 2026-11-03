package core

import (
	"sync"
	"testing"
)

// ObviouslyDisjoint answers true only for types without a common subtype.
func TestObviouslyDisjoint(t *testing.T) {
	if !ObviouslyDisjoint(Int64Type, StringType) || ObviouslyDisjoint(Int64Type, AnyType) {
		t.Error("ObviouslyDisjoint")
	}
}

// A type's cached instance: none until set, a set nothing told from none,
// and safe to set from the VMs of several goroutines at once.
func TestCachedInstance(t *testing.T) {
	d := mustApply(AnyTupleType, Int64Type, Int64Type, Int64Type, Int64Type).(*DataType)
	if v, ok := d.CachedInstance(); ok || v != nil {
		t.Fatalf("a fresh type has an instance: %v %v", v, ok)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d.SetCachedInstance(nil)
			d.CachedInstance()
		}()
	}
	wg.Wait()
	if v, ok := d.CachedInstance(); !ok || v != nil {
		t.Errorf("nothing recorded: %v %v", v, ok)
	}
}
