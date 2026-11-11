package interp

import "testing"

// A module as a type parameter, as julia 1.12.6 answers inside a module L
// of Main (a session's module):
//
//	module Outer; module Inner end; end; struct V{T} end
//	repr(V{Outer.Inner})                                    # Main.L.V{Main.L.Outer.Inner}
//	(V{Outer.Inner} === V{Outer.Inner}, V{Outer} === V{Outer.Inner})  # (true, false)
//	repr(V{Outer.Inner}())                                  # Main.L.V{Main.L.Outer.Inner}()
//
// (Artifacts.jl:776 precompiles a signature holding Base.Val{Artifacts}.)
func TestModuleTypeParam(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("module Outer\nmodule Inner end\nend\nstruct V{T} end"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"V{Outer.Inner}", "Main.L.V{Main.L.Outer.Inner}"},
		{"(V{Outer.Inner} === V{Outer.Inner}, V{Outer} === V{Outer.Inner})", "(true, false)"},
		{"V{Outer.Inner}()", "Main.L.V{Main.L.Outer.Inner}()"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	// core shows the parameter by the same full name (core.Show's ModuleValue)
	inner, err := s.Eval("Outer.Inner")
	if err != nil {
		t.Fatal(err)
	}
	if got := inner.(*Module).ModuleName(); got != "Main.L.Outer.Inner" {
		t.Errorf("ModuleName: %s", got)
	}
}
