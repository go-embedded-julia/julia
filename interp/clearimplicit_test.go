package interp

import "testing"

// jl_clear_implicit_imports: a name read through a using reads the same
// after it, and later definitions of the used module are seen, as julia
// resolves the cleared bindings again; a non-module is refused.
func TestClearImplicitImports(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("module A\nexport x, y\nx = 1\nend\nmodule B\nusing ..A\nend"); err != nil {
		t.Fatal(err)
	}
	v, err := s.Eval("B.x")
	check(t, "before", v, err, "1")
	v, err = s.Eval("ccall(:jl_clear_implicit_imports, Cvoid, (Any,), B)")
	check(t, "the call", v, err, "nothing")
	v, err = s.Eval("B.x")
	check(t, "after", v, err, "1")
	if _, err := s.Eval("Core.eval(A, :(y = 2))"); err != nil {
		t.Fatal(err)
	}
	v, err = s.Eval("B.y")
	check(t, "a later definition", v, err, "2")
	_, err = s.Eval("ccall(:jl_clear_implicit_imports, Cvoid, (Any,), 1)")
	check(t, "of 1", nil, err, "!TypeError(:ccall")
}
