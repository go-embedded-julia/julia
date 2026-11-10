package interp

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// TestBaseProgress includes Base_compiler.jl in Main after Boot, as julia's
// build does, and reports where it stops. It runs for more than half an
// hour, so only when JLGO_BASE=1 asks for it.
func TestBaseProgress(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 to load julia's Base (over 30 minutes)")
	}
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
	// julia's build generates its system image: initializers wait
	vm.GeneratingOutput = true
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
	var th *Thrown
	if errors.As(err, &th) {
		t.Logf("unwound:\n%s", strings.Join(th.Trace, "\n"))
	}
	if err != nil {
		return
	}
	// julia starting from its image: the modules' __init__, in order
	out.Reset()
	vm.GeneratingOutput = false
	err = vm.RunModuleInitializers()
	t.Logf("module initializers: %v\nprinted:\n%s", err, out.String())
	if errors.As(err, &th) {
		t.Logf("unwound:\n%s", strings.Join(th.Trace, "\n"))
	}
}
