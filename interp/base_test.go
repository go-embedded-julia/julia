package interp

import (
	"strings"
	"testing"
)

// TestBaseProgress includes Base_compiler.jl in Main after Boot, as julia's
// build does, and reports where it stops.
func TestBaseProgress(t *testing.T) {
	vm := New()
	vm.Args = []string{"Base_compiler.jl"}
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
	if err != nil {
		return
	}
	// the second stage (sysimage.mk:82): sysimg.jl in Main, which finishes
	// Base with Base.jl
	out.Reset()
	vm.Args = []string{"sysimg.jl"}
	_, err = vm.jlLoad([]any{vm.Main, "sysimg.jl"})
	t.Logf("sysimg.jl: %v\nprinted:\n%s", err, out.String())
}
