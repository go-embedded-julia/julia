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
		// jl_set_istopmod (module.c): marks Core or Base as a top module;
		// the top module of each session's lowering is fixed when the
		// session is made
		"jl_set_istopmod": func(vm *VM, a []any) (any, error) { return nil, nil },
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
// state, made on first use (Core's top module is Core, every other's Base).
func (vm *VM) sessionFor(m *Module) *Session {
	if s := vm.sessions[m]; s != nil {
		return s
	}
	top := expr.Symbol("Base")
	if m == vm.Core {
		top = "Core"
	}
	s := &Session{VM: vm, Mod: m, low: lower.NewStateIn(exprModule(m), top)}
	vm.sessions[m] = s
	return s
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
		if atypes[i] != any(core.AnyType) {
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
