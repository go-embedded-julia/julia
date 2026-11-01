package interp

import (
	"fmt"
	"io/fs"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// Boot evaluates boot.jl in Core, form by form, as julia's
// jl_init_primitives does after jl_init_types, then runs post_boot_hooks.
func (vm *VM) Boot() error {
	src, err := fs.ReadFile(vm.Sources, "boot.jl")
	if err != nil {
		return err
	}
	s := vm.CoreSession()
	top := syntax.ParseAll(src, "boot.jl").(*expr.Expr)
	line := 0
	for _, form := range top.Args {
		if l, ok := form.(expr.LineNumberNode); ok {
			line = l.Line
			continue
		}
		if _, err := s.evalForm(form); err != nil {
			return fmt.Errorf("boot.jl:%d: %w", line, err)
		}
	}
	return vm.postBootHooks()
}

// postBootHooks is post_boot_hooks (jltypes.c:3909-3968): the runtime's
// references to the types boot.jl defines are taken from Core's bindings,
// so that the exceptions the builtins raise are the ones Julia code catches
// and names. The re-parenting of Bool, the UInts, Int32 and Int64
// (:3931-3937) has nothing to do: boot.jl's abstract number types are
// equivalent to core's, which keeps its own, already their supertypes.
func (vm *VM) postBootHooks() error {
	coreType := func(name expr.Symbol) (*core.DataType, error) {
		b := vm.Core.own(name)
		if b == nil {
			return nil, fmt.Errorf("%w: post_boot_hooks: Core.%s is not defined", ErrInternal, name)
		}
		t, ok := b.Value.(*core.DataType)
		if !ok {
			return nil, fmt.Errorf("%w: post_boot_hooks: Core.%s is not a DataType", ErrInternal, name)
		}
		return t, nil
	}
	for name, field := range map[expr.Symbol]**core.DataType{
		"ErrorException":     &vm.tError,
		"UndefVarError":      &vm.tUndefVar,
		"TypeError":          &vm.tTypeError,
		"MethodError":        &vm.tMethodError,
		"StackOverflowError": &vm.tStackOverflow,
		"ArgumentError":      &vm.tArgError,
		"BoundsError":        &vm.tBounds,
		"UndefRefError":      &vm.tUndefRef,
		"DivideError":        &vm.tDivide,
		"LoadError":          &vm.tLoadError,
	} {
		t, err := coreType(name)
		if err != nil {
			return err
		}
		*field = t
	}
	return nil
}
