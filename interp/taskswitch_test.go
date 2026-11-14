package interp

import (
	"reflect"
	"testing"
)

// TestTaskStateRoundTrip: what saveTaskState takes, loadTaskState gives back,
// whatever the VM held in between.
func TestTaskStateRoundTrip(t *testing.T) {
	vm := New()
	vm.taskWorld, vm.depth, vm.excStack = 7, 3, []any{"e1", "e2"}
	vm.typeinfTiming, vm.finalizersInhibited = true, 2
	s := vm.saveTaskState()
	vm.taskWorld, vm.depth, vm.excStack = 1, 0, nil
	vm.typeinfTiming, vm.finalizersInhibited = false, 0
	vm.loadTaskState(s)
	got := vm.saveTaskState()
	want := taskState{world: 7, depth: 3, excStack: []any{"e1", "e2"}, typeinfTiming: true, finalizersInhibited: 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("state after a round trip: %+v, want %+v", got, want)
	}
}
