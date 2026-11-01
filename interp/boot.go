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
	// julia's state after boot.jl: Core is the top module of every module
	// until Base claims it, and there is no Base yet
	vm.topModule, vm.baseModule = vm.Core, nil
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
	// in post_boot_hooks' order (jltypes.c:3939-3955)
	for _, f := range []struct {
		name  expr.Symbol
		field **core.DataType
	}{
		{"StackOverflowError", &vm.tStackOverflow},
		{"DivideError", &vm.tDivide},
		{"UndefRefError", &vm.tUndefRef},
		{"ErrorException", &vm.tError},
		{"UndefVarError", &vm.tUndefVar},
		{"ConcurrencyViolationError", &vm.tAtomicError},
		{"BoundsError", &vm.tBounds},
		{"TypeError", &vm.tTypeError},
		{"ArgumentError", &vm.tArgError},
		{"MethodError", &vm.tMethodError},
		{"LoadError", &vm.tLoadError},
	} {
		t, err := coreType(f.name)
		if err != nil {
			return err
		}
		*f.field = t
	}
	return nil
}
