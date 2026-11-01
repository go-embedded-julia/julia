package interp

import "testing"

// TestBaseProgress includes Base_compiler.jl in Main after Boot, as julia's
// build does, and reports where it stops.
func TestBaseProgress(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	_, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"})
	t.Logf("Base_compiler.jl: %v", err)
}
