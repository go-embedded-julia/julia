package interp

import (
	"errors"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Module initializers. The VM builds Base when it starts, as a julia not
// generating output (jl_generating_output() false): a module's __init__
// runs once the outermost module being defined around it is done
// (toplevel.c:207-242), and Base's own once sysimg.jl has loaded it
// (jl_init_restored_module, module.c:2087-2095).

// isSubmodule is jl_is_submodule (module.c:2044-2053): child is parent or
// lies under it.
func isSubmodule(child, parent *Module) bool {
	for ; child != nil; child = child.Parent {
		if child == parent {
			return true
		}
	}
	return false
}

// moduleDefined is the end of jl_eval_module_expr (toplevel.c:200-242):
// newm joins the modules waiting for their __init__; unless its parent is
// still being defined, those of them under newm run theirs, in the order
// their definitions ended, and leave the list.
func (vm *VM) moduleDefined(newm *Module) error {
	vm.moduleInitOrder = append(vm.moduleInitOrder, newm)
	if vm.openModules[newm.Parent] > 0 {
		return nil
	}
	var run, keep []*Module
	for _, m := range vm.moduleInitOrder {
		if isSubmodule(m, newm) {
			run = append(run, m)
		} else {
			keep = append(keep, m)
		}
	}
	vm.moduleInitOrder = keep
	for _, m := range run {
		if err := vm.runInitializer(m); err != nil {
			return err
		}
	}
	return nil
}

// runInitializer is jl_module_run_initializer (toplevel.c:57-82): m's
// __init__, if it has one, called in the newest world; its error comes out
// as InitError(m's name, error) once Core has InitError.
func (vm *VM) runInitializer(m *Module) error {
	last := vm.taskWorld
	vm.taskWorld = vm.World()
	defer func() { vm.taskWorld = last }()
	b := m.lookup("__init__", vm.taskWorld)
	if b == nil || b.Value == undef {
		return nil
	}
	_, err := vm.apply(b.Value, nil)
	var thrown *Thrown
	if !errors.As(err, &thrown) {
		return err
	}
	ie := vm.Core.own("InitError")
	if ie == nil || ie.Value == undef {
		return err
	}
	// InitError(mod::Symbol, error) (boot.jl): a Symbol and any value fit
	v := must(vm.newStruct(ie.Value, []any{expr.Symbol(m.Name), thrown.Value}))
	return &Thrown{Value: v, Trace: thrown.Trace}
}

// initRestoredModule is jl_init_restored_module (module.c:2087-2095), as
// sysimg.jl:9 calls it for Base: its __init__ now, the VM not generating
// output.
func (vm *VM) initRestoredModule(a []any) (any, error) {
	m, ok := a[0].(*Module)
	if !ok {
		return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
	}
	return nil, vm.runInitializer(m)
}
