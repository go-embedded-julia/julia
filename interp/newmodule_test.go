package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// Core.Module (jl_f_new_module), as julia 1.12.6 answers
// f(m) = (nameof(m), parentmodule(m), isdefined(m, nameof(m)), isdefined(m, :Int),
// isdefined(m, :Base), isdefined(Main, nameof(m))):
//
//	Core.Module()                 (:anonymous, Main, true, true, true, false)
//	Core.Module(:Q1, false, false) (:Q1, Main, false, false, false, false)
//	Core.Module(:Q2, false, true)  (:Q2, Main, true, true, false, false)
//	Core.Module(:Q3, true, false)  (:Q3, Main, false, true, true, false)
//
// and Core.eval(Core.Module(:Q4), :(x = 1; x)) is 1.
func TestNewModule(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	// before Base_compiler.jl the top module, which the standard imports
	// use, is Core (jl_base_relative_to)
	top := vm.baseRelativeTo(vm.Main)
	cases := []struct {
		src                      string
		name                     expr.Symbol
		self, usesCore, usesBase bool
	}{
		{"Core.Module()", "anonymous", true, true, true},
		{"Core.Module(:Q1, false, false)", "Q1", false, false, false},
		{"Core.Module(:Q2, false, true)", "Q2", true, true, false},
		{"Core.Module(:Q3, true, false)", "Q3", false, false, true},
	}
	uses := func(m, u *Module) int {
		n := 0
		for _, x := range m.usings {
			if x == u {
				n++
			}
		}
		return n
	}
	for _, c := range cases {
		v, err := s.Eval(c.src)
		if err != nil {
			t.Fatalf("%s: %v", c.src, err)
		}
		m := v.(*Module)
		self := m.own(c.name)
		want := 0
		for _, u := range []bool{c.usesCore, c.usesBase} {
			if u {
				want++
			}
		}
		if m.Name != c.name || m.Parent != vm.Main || (self != nil && self.Value == m && self.Exports) != c.self ||
			uses(m, top) != want {
			t.Errorf("%s: %s, self %v, usings %v", c.src, m.Name, self, m.usings)
		}
		if vm.Main.own(c.name) != nil {
			t.Errorf("%s: bound in Main", c.src)
		}
		// with the standard imports it names Core and the top module, as
		// Base's exports give it
		if c.usesBase && (m.own("Core").Value != vm.Core || m.own(top.Name).Value != top) {
			t.Errorf("%s: Core and %s not named", c.src, top.Name)
		}
	}
	v, err := s.Eval("Core.eval(Core.Module(:Q4), :(x = 1; x))")
	check(t, "eval in a new module", v, err, "1")
	_, err = vm.newModuleShim([]any{int64(1), true, true})
	check(t, "a name that is no Symbol", nil, err, "!TypeError(:ccall")
}
