package interp

import (
	"testing"
)

// A module defined in Base.__toplevel__ (where require includes a package)
// has no parent, keeps its parent's UUID and is handed to
// Base.register_root_module instead of being bound in the parent
// (toplevel.c:132-136, 182-183); `using X` of a package calls Base.require
// once Base has it (toplevel.c:475-501, 531-533).
func TestCPUWake(t *testing.T) {
	f := newPCREFixture(t)
	if v, err := f.sh("jl_cpu_wake"); v != nil || err != nil {
		t.Errorf("jl_cpu_wake: %v %v", v, err)
	}
}

func TestRootModuleAndRequire(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	vm.baseModule = vm.Base
	bs := vm.sessionFor(vm.Base)
	if _, err := bs.Eval(`const __toplevel__ = Module(:__toplevel__)`); err != nil {
		t.Fatal(err)
	}
	top := vm.Base.own("__toplevel__").Value.(*Module)
	top.uuid = [2]uint64{7, 9}
	// no register_root_module yet
	_, err := vm.sessionFor(top).Eval(`module NoReg; end`)
	check(t, "a root module without register_root_module", nil, err, "!register_root_module is not defined")
	if _, err := bs.Eval("registered = nothing\nregister_root_module(m) = (global registered = m; nothing)"); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.sessionFor(top).Eval(`module Pkg; x = 1; end`); err != nil {
		t.Fatal(err)
	}
	m, _ := vm.Base.own("registered").Value.(*Module)
	if m == nil || m.Name != "Pkg" || m.Parent != nil || m.uuid != [2]uint64{7, 9} {
		t.Fatalf("the registered module: %v", m)
	}
	if top.own("Pkg") != nil {
		t.Error("a root module is bound in __toplevel__")
	}
	if _, err := bs.Eval(`register_root_module(m) = throw(ErrorException("refused"))`); err != nil {
		t.Fatal(err)
	}
	_, err = vm.sessionFor(top).Eval(`module Refused; end`)
	check(t, "a root module register_root_module refuses", nil, err, "!refused")
	if _, err := bs.Eval(`register_root_module(m) = (global registered = m; nothing)`); err != nil {
		t.Fatal(err)
	}
	// a submodule of it is an ordinary one
	if _, err := vm.sessionFor(m).Eval(`module Sub; end`); err != nil {
		t.Fatal(err)
	}
	if sub := m.own("Sub"); sub == nil || sub.Value.(*Module).Parent != m || sub.Value.(*Module).uuid != m.uuid {
		t.Error("a submodule of a root module")
	}
	// using of a package: Base.require(m, name)
	if _, err := bs.Eval("require(into, name) = Core.Main"); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.sessionFor(vm.Main).Eval(`using Nothing_Such_Pkg`); err != nil {
		t.Errorf("using through require: %v", err)
	}
	if _, err := bs.Eval("require(into, name) = throw(ErrorException(\"no such package\"))"); err != nil {
		t.Fatal(err)
	}
	_, err = vm.sessionFor(vm.Main).Eval(`using Nothing_Such_Pkg`)
	check(t, "using of a package require refuses", nil, err, "!no such package")
}
