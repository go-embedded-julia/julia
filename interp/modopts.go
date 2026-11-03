package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// moduleOpts are a module's compiler options (jl_module_t): -1 inherits
// the parent's, as jl_new_module_ sets them (module.c:519-523);
// nospecialize is 0 or -1 (all arguments).
type moduleOpts struct {
	nospecialize, optlevel, compile, infer, maxMethods int32
}

func newModuleOpts() moduleOpts { return moduleOpts{0, -1, -1, -1, -1} }

// The options' indices, the order of the getters and setters below.
const (
	optOptlevel = iota
	optCompile
	optInfer
	optMaxMethods
)

func (o *moduleOpts) at(i int) *int32 {
	return [...]*int32{&o.optlevel, &o.compile, &o.infer, &o.maxMethods}[i]
}

// moduleOpt is jl_get_module_optlevel, _compile, _infer and _max_methods
// (module.c:685-741): m's option, else the nearest enclosing module's that
// sets it, stopping at a root module and at Base.
func (vm *VM) moduleOpt(m *Module, i int) int32 {
	v := *m.opts.at(i)
	for v == -1 && m.Parent != nil && m != vm.baseModule {
		m = m.Parent
		v = *m.opts.at(i)
	}
	return v
}

// setModuleOpt is jl_set_module_optlevel, _compile, _infer and
// _max_methods (module.c:680-731); inference off turns nospecialize on.
func setModuleOpt(m *Module, i int, v int32) {
	*m.opts.at(i) = v
	if i == optInfer && v == 0 {
		m.opts.nospecialize = -1
	}
}

// moduleMeta is a top-level Expr(:meta, …) of module m (interpreter.c:
// 657-686): :nospecialize, :specialize, or an option and its Int value.
func moduleMeta(m *Module, args []any) {
	switch len(args) {
	case 1:
		switch args[0] {
		case expr.Symbol("nospecialize"):
			m.opts.nospecialize = -1
		case expr.Symbol("specialize"):
			m.opts.nospecialize = 0
		}
	case 2:
		n, ok := args[1].(int64)
		if !ok {
			return
		}
		for i, name := range []expr.Symbol{"optlevel", "compile", "infer", "max_methods"} {
			if args[0] == name {
				setModuleOpt(m, i, int32(n))
			}
		}
	}
}

// bootModuleOptShims are the options' ccalls.
func (vm *VM) bootModuleOptShims() {
	for i, name := range []string{"optlevel", "compile", "infer", "max_methods"} {
		vm.shims[expr.Symbol("jl_get_module_"+name)] = func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			return vm.moduleOpt(m, i), nil
		}
		vm.shims[expr.Symbol("jl_set_module_"+name)] = func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			v, ok2 := a[1].(int32)
			if !ok || !ok2 {
				return nil, fmt.Errorf("%w: jl_set_module_%s of %s", ErrUnsupported, name, showValue(vm.newTuple(a)))
			}
			setModuleOpt(m, i, v)
			return nil, nil
		}
	}
	// jl_set_module_nospecialize (module.c:675-678)
	vm.shims["jl_set_module_nospecialize"] = func(vm *VM, a []any) (any, error) {
		m, ok := a[0].(*Module)
		on, ok2 := a[1].(int32)
		if !ok || !ok2 {
			return nil, fmt.Errorf("%w: jl_set_module_nospecialize of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		m.opts.nospecialize = map[bool]int32{true: -1, false: 0}[on != 0]
		return nil, nil
	}
}
