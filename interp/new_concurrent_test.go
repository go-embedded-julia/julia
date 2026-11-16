package interp

import (
	"fmt"
	"sync"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// staticFields is what New may have written to each of core's static objects
// that are types, as a string: the fields a VM's creation sets.
func staticFields() map[*core.TypeName]string {
	out := map[*core.TypeName]string{}
	for _, o := range core.StaticObjects() {
		if tn, ok := o.(*core.TypeName); ok {
			out[tn] = fmt.Sprint(tn.NUninitialized, tn.MayInlineAlloc, len(tn.Fields), tn.Wrapper != nil)
		}
	}
	return out
}

// Concurrent New calls in one process (issue #109): under -race no access to
// core's shared types is unsynchronized, and every VM sees the same shared
// types, with the same fields, as the first one made.
func TestNewConcurrent(t *testing.T) {
	New() // the first VM of the process
	want := staticFields()
	for _, n := range []int{2, 4, 8} {
		vms := make([]*VM, n)
		var wg sync.WaitGroup
		for i := range vms {
			wg.Add(1)
			go func() {
				defer wg.Done()
				vms[i] = New()
			}()
		}
		wg.Wait()
		got := staticFields()
		for tn, w := range want {
			if got[tn] != w {
				t.Errorf("%d concurrent New: %s has %s, it had %s", n, tn.Name, got[tn], w)
			}
		}
		for i, vm := range vms {
			if vm == nil || vm.tSSAValue == nil {
				t.Fatalf("VM %d is not made", i)
			}
		}
	}
}
