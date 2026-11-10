package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

func TestMemmoveRefEdges(t *testing.T) {
	f := newPCREFixture(t)
	void := f.vm.ptrType(core.NothingType)
	u64, _ := f.ref("RefU64", uint64(1))
	any1, _ := f.ref("RefAny", "text")
	dst, _ := f.buf(20, 0)
	small, _ := f.buf(4, 0)
	for name, c := range map[string][]any{
		"a word shorter than the count":   {dst, &Pointer{Type: void, Target: u64.Target}, uint64(16)},
		"a value that is no primitive":    {dst, &Pointer{Type: void, Target: any1.Target}, uint64(2)},
		"a destination that is too small": {small, &Pointer{Type: void, Target: u64.Target}, uint64(8)},
	} {
		if _, err := f.sh("memmove", c...); err == nil {
			t.Errorf("memmove of %s: no error", name)
		}
	}
}
