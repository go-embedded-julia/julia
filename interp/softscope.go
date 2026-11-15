package interp

import "github.com/go-embedded-julia/julia/expr"

// Soft scope (#54). The REPL, and IJulia, evaluate top-level code with a rule a
// file does not have: a `for` or `while` loop, a `begin` or `if` block at the
// top assigns to a global that already exists instead of making a new local.
// Two pieces make it, both ported:
//
//   - REPL.softscope (stdlib/REPL/src/REPL.jl:145-161, julia 1.12.6): the
//     rewrite of the parsed code, which wraps every top-level expression in
//     Expr(:block, Expr(:softscope, true), ex), except the forms that have no
//     scope to soften. It is softscope below.
//   - lowering's handling of (softscope #t) (julia-syntax.scm:3193 and
//     3234-3250, resolve-scopes, lower/scope.go), which the lowering port had
//     already, and defined-julia-global (ast.c:172-182, fl_defined_julia_global):
//     whether a name is a global the module being lowered defines itself. A soft
//     scope assigns to such a global (the variable of the loop is not a new
//     local); lowering alone had it false, so it was State.SetDefinedGlobal
//     that was missing, and definedGlobal below that answers it.

// softscope is REPL.softscope: the code ex with the REPL's soft scope. A
// :toplevel is mapped over, the forms that define no scope are left, and any
// other expression is wrapped.
func softscope(ex any) any {
	e, ok := ex.(*expr.Expr)
	if !ok {
		return ex
	}
	switch e.Head {
	case "toplevel":
		out := &expr.Expr{Head: "toplevel", Args: make([]any, len(e.Args))}
		for i, a := range e.Args {
			out.Args[i] = softscope(a)
		}
		return out
	case "meta", "import", "using", "export", "module", "error", "incomplete", "thunk":
		return ex
	case "global":
		// `global x, y` of symbols only; `global x = 1` is wrapped
		all := true
		for _, a := range e.Args {
			if _, isSym := a.(expr.Symbol); !isSym {
				all = false
			}
		}
		if all {
			return ex
		}
	}
	return expr.New("block", expr.New("softscope", true), ex)
}

// definedGlobal is fl_defined_julia_global (ast.c:172-182): whether name is a
// global m itself defines, in the world the form runs in. That is the
// binding's global kind: declared in m (not imported, not an implicit export,
// not a constant) and visible in the task's world.
func (vm *VM) definedGlobal(m *Module, name expr.Symbol) bool {
	b := m.own(name)
	return b != nil && b.World <= vm.taskWorld && b.Alias == nil && !b.Const && !b.implicit
}
