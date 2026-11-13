package interp

import "testing"

// jl_gc_init_finalizer_rng_state splits the current task's random state,
// advancing its counter as jl_rng_split does.
func TestGCInitFinalizerRNG(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	ct := vm.currentTask()
	before := append([]any(nil), ct.Fields[14:19]...)
	want := rngSplit(append([]any(nil), before...))
	if _, err := vm.shims["jl_gc_init_finalizer_rng_state"](vm, nil); err != nil {
		t.Fatal(err)
	}
	if vm.finalizerRNG != want {
		t.Errorf("finalizer state %x, want %x", vm.finalizerRNG, want)
	}
	if got, x := ct.Fields[18].(uint64), before[4].(uint64); got != x*0xd1342543de82ef95+1 {
		t.Errorf("task counter %x after %x", got, x)
	}
}
