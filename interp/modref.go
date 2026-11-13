package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A reference to a module is the module object, as in julia (GlobalRef.mod is
// a Module), never its name: two modules can share one, Base.Libc.Libdl and
// the stdlib's Libdl among them. The front end writes a module by name, so a
// GlobalRef or a module value from lowering carries it in Obj once the runtime
// has bound it to the module it was lowered in (bindRefs). vm.modules is the
// registry of the root modules only, which julia keys by name too
// (Core, Base, Main).

// gref is the GlobalRef of name in m.
func gref(m *Module, name expr.Symbol) expr.GlobalRef {
	return expr.GlobalRef{Mod: m.Name, Name: name, Obj: m}
}

// refModule is the module g names: its object, else, for a name alone, the
// module ctx lowered in that bears it (ctx itself or an enclosing one), else
// the root module of that name. nil when none.
func (vm *VM) refModule(ctx *Module, g expr.GlobalRef) *Module {
	if m, ok := g.Obj.(*Module); ok {
		return m
	}
	for m := ctx; m != nil; m = m.Parent {
		if m.Name == g.Mod {
			return m
		}
	}
	return vm.modules[g.Mod]
}

// bindRef is g with Obj set to the module it names in ctx, g itself when none.
func (vm *VM) bindRef(ctx *Module, g expr.GlobalRef) expr.GlobalRef {
	if m := vm.refModule(ctx, g); m != nil {
		g.Obj = m
	}
	return g
}

// modulePath is the module of a fullname (Main.L): its root, then each
// submodule bound in the one before. nil when one is missing.
func (vm *VM) modulePath(path []expr.Symbol) *Module {
	m := vm.modules[path[0]]
	for _, name := range path[1:] {
		if m == nil {
			return nil
		}
		b := m.own(name)
		if b == nil {
			return nil
		}
		m, _ = b.Value.(*Module)
	}
	return m
}

// typeNameModule is the module a type was defined in.
func (vm *VM) typeNameModule(n *core.TypeName) *Module {
	if m, ok := n.ModuleRef.(*Module); ok {
		return m
	}
	return vm.modules[expr.Symbol(n.Module)]
}

// bindRefs binds the named GlobalRefs of lowered code to the modules they name
// in ctx, the module it was lowered in: the code's own module (or a parent) by
// object, so that a module of the same name elsewhere cannot take its place. A
// root module's name stays as it is. It changes the code in place and returns v,
// or its replacement when v is itself a node.
func (vm *VM) bindRefs(ctx *Module, v any) any {
	switch x := v.(type) {
	case expr.GlobalRef:
		if x.Obj == nil && vm.modules[x.Mod] == nil {
			return vm.bindRef(ctx, x)
		}
	case *expr.Expr:
		vm.bindAll(ctx, x.Args)
	case expr.Vector:
		vm.bindAll(ctx, x)
	case *expr.CodeInfo:
		vm.bindAll(ctx, x.Code)
	case expr.QuoteNode:
		x.Value = vm.bindRefs(ctx, x.Value)
		return x
	case expr.GotoIfNot:
		x.Cond = vm.bindRefs(ctx, x.Cond)
		return x
	case expr.ReturnNode:
		x.Val = vm.bindRefs(ctx, x.Val)
		return x
	case expr.NewvarNode:
		x.Slot = vm.bindRefs(ctx, x.Slot)
		return x
	case expr.EnterNode:
		x.Scope = vm.bindRefs(ctx, x.Scope)
		return x
	}
	return v
}

func (vm *VM) bindAll(ctx *Module, xs []any) {
	for i, a := range xs {
		xs[i] = vm.bindRefs(ctx, a)
	}
}
