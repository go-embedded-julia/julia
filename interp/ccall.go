package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
)

// foreigncall is where julia's interpreter gives up ("`ccall` requires the
// compiler", interpreter.c:356-358): julia never meets it because Base is
// compiled. Here Core and Base run in the interpreter, so every ccall they
// make must reach Go: a shim per C function, keyed by the literal name the
// call names (ast.md: args[1] of :foreigncall), with that function's
// semantics. A name without a shim is ErrUnsupported when the call runs,
// never earlier, so that methods holding calls the program does not make
// still load.

// shim is the Go side of one C function a ccall names.
type shim func(vm *VM, args []any) (any, error)

func (vm *VM) bootShims() {
	vm.shims = map[expr.Symbol]shim{
		// jl_toplevel_eval_in (toplevel.c): evaluate ex in module m
		"jl_toplevel_eval_in": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("eval", "", core.ModuleType, a[0])
			}
			return vm.sessionFor(m).evalForm(a[1])
		},
		// jl_load_ (toplevel.c:1194-1201), boot.jl's include
		"jl_load_": func(vm *VM, a []any) (any, error) { return vm.jlLoad(a) },
		// jl_set_istopmod (module.c:743-749): a top module, and for the
		// primary one the module every other falls back to; the primary
		// Base becomes the VM's Base
		"jl_set_istopmod": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			m.istopmod = true
			if primary, _ := a[1].(bool); primary {
				vm.topModule = m
				if m.Name == "Base" && m != vm.Base {
					vm.replaceBase(m)
				}
			}
			return nil, nil
		},
		// jl_prepend_cwd (init.c): a path made absolute against the working
		// directory; include's paths are VM.Sources', which has none
		"jl_prepend_cwd": func(vm *VM, a []any) (any, error) { return a[0], nil },
		// jl_uv_flush (jl_uv.c): output is written through as it comes
		"jl_uv_flush": func(vm *VM, a []any) (any, error) { return nil, nil },
		// jl_module_parent (module.c)
		"jl_module_parent": func(vm *VM, a []any) (any, error) {
			m, ok := a[0].(*Module)
			if !ok {
				return nil, vm.typeError("ccall", "", core.ModuleType, a[0])
			}
			if m.Parent == nil {
				return m, nil // a root module is its own parent
			}
			return m.Parent, nil
		},
		// jl_type_unionall (jltypes.c:907-948); boot.jl's UnionAll asserts
		// v::TypeVar before the call
		"jl_type_unionall": func(vm *VM, a []any) (any, error) {
			v, ok := a[0].(*core.TypeVar)
			if !ok {
				return nil, vm.typeError("typeassert", "", core.TypeVarType, a[0])
			}
			return vm.typeUnionAll(v, a[1])
		},
		// jl_has_free_typevars (jltypes.c)
		"jl_has_free_typevars": func(vm *VM, a []any) (any, error) { return core.HasFreeTypeVars(a[0]), nil },
		// jl_get_tls_world_age (task.c): the task's world, a UInt
		"jl_get_tls_world_age": func(vm *VM, a []any) (any, error) { return vm.taskWorld, nil },
	}
}

// sessionFor is the session that evaluates code in module m: its lowering
// state, made on first use. Its top module is asked again for every form
// (baseRelativeTo).
func (vm *VM) sessionFor(m *Module) *Session {
	if s := vm.sessions[m]; s != nil {
		return s
	}
	s := &Session{VM: vm, Mod: m, low: lower.NewStateIn(exprModule(m), vm.baseRelativeTo(m).Name)}
	vm.sessions[m] = s
	return s
}

// baseRelativeTo is jl_base_relative_to (toplevel.c:356-366): the nearest
// enclosing top module, else the primary one (Core until Base claims it).
func (vm *VM) baseRelativeTo(m *Module) *Module {
	for ; m != nil; m = m.Parent {
		if m.istopmod {
			return m
		}
	}
	return vm.topModule
}

// replaceBase makes m, the Base that Base_compiler.jl defines, the VM's
// Base in place of the empty one New made: the module Main and the
// standard imports name.
func (vm *VM) replaceBase(m *Module) {
	old := vm.Base
	vm.Base = m
	vm.modules["Base"] = m
	vm.Main.mu.Lock()
	for i, u := range vm.Main.usings {
		if u == old {
			vm.Main.usings[i] = m
		}
	}
	vm.Main.mu.Unlock()
}

// oForeign is a prepared :foreigncall: the callee's literal name, the
// return and argument types, and the arguments (julia's GC roots after the
// declared arguments are left out).
type oForeign struct {
	name   expr.Symbol
	ret    op
	atypes op
	args   []op
}

func (c *code) prepForeign(x *expr.Expr) op {
	// (foreigncall name rettype argtypes nreq cc args… roots…)
	if len(x.Args) < 5 {
		return oRaise{"Invalid IR: malformed :foreigncall"}
	}
	q, ok := x.Args[0].(expr.QuoteNode)
	name, ok2 := q.Value.(expr.Symbol)
	if !ok || !ok2 {
		return oUnsupported{"a ccall to a computed or library-qualified function"}
	}
	return oForeign{name: name, ret: c.prepValue(x.Args[1]), atypes: c.prepValue(x.Args[2]), args: c.prepArgs(x.Args[5:])}
}

func (f *frame) evalForeign(x oForeign) (any, error) {
	vm := f.vm
	if _, err := f.evalValue(x.ret); err != nil {
		return nil, err
	}
	at, err := f.evalValue(x.atypes)
	if err != nil {
		return nil, err
	}
	atypes, ok := at.(SVec)
	if !ok || len(atypes) > len(x.args) {
		return nil, vm.errorf("Invalid IR: malformed :foreigncall")
	}
	args := make([]any, len(atypes))
	for i := range args {
		v, err := f.evalValue(x.args[i])
		if err != nil {
			return nil, err
		}
		// an Any argument is passed boxed; a primitive one by value, which
		// lowering has already converted (cconvert/unsafe_convert) to it
		if atypes[i] != any(core.AnyType) && atypes[i] != any(vm.typeOf(v)) {
			return nil, fmt.Errorf("%w: a ccall argument of type %s", ErrUnsupported, showValue(atypes[i]))
		}
		args[i] = v
	}
	sh := vm.shims[x.name]
	if sh == nil {
		return nil, fmt.Errorf("%w: ccall(:%s)", ErrUnsupported, x.name)
	}
	return sh(vm, args)
}
