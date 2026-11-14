package interp

import (
	"strings"
	"testing"
)

// memoryrefnew stepping back from an offset, as _growbeg! does, each want
// julia 1.12.6's (testdata/memoryrefback.jl).
func TestMemoryrefStepBack(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("m = Core.GenericMemory{:not_atomic, Any, Core.CPU}(undef, 4); r3 = Core.memoryrefnew(Core.memoryrefnew(m), 3, false)"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, src, want string }{
		{"3 then 0", "Core.memoryrefnew(r3, 0, false)", "2"},
		{"3 then -1", "Core.memoryrefnew(r3, -1, true)", "1"},
		{"3 then -2", "Core.memoryrefnew(r3, -2, true)", "!BoundsError"},
		{"3 then -3", "Core.memoryrefnew(r3, -3, false)", "!BoundsError"},
		{"3 then 2", "Core.memoryrefnew(r3, 2, true)", "4"},
		{"3 then 3", "Core.memoryrefnew(r3, 3, true)", "!BoundsError"},
		{"1 then 0", "Core.memoryrefnew(Core.memoryrefnew(m), 0, false)", "!BoundsError"},
		{"typemin", "Core.memoryrefnew(r3, Core.Intrinsics.sub_int(-9223372036854775807, 1), false)", "!BoundsError"},
	} {
		v, err := s.Eval("Core.memoryrefoffset(" + c.src + ")")
		if strings.HasPrefix(c.want, "!") {
			check(t, c.name, v, err, c.want)
			continue
		}
		check(t, c.name, v, err, c.want)
	}
}
