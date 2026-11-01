package interp

import (
	"strings"
	"testing"
)

// TestBaseProgress includes Base_compiler.jl in Main after Boot, as julia's
// build does, and reports where it stops.
func TestBaseProgress(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	cb := vm.Core.own("Core")
	t.Logf("Core.Core = %+v; Main.usings = %v; world %d", cb, vm.Main.usings, vm.taskWorld)
	for _, src := range []string{"module X1\nCore\nend", "module X2\nusing .Core.Intrinsics\nend", "Core.Main"} {
		v, err := vm.sessionFor(vm.Main).Eval(src)
		t.Logf("%q => %v %v", src, v, err)
	}
	var out strings.Builder
	vm.Stdout = &out
	_, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"})
	t.Logf("Base_compiler.jl: %v\nprinted:\n%s", err, out.String())
}
