package interp

import (
	"errors"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// Macro expansion, a port of jl_expand_macros and jl_invoke_julia_macro
// (ast.c:1104-1254): a macro call runs the macro's method in the
// interpreter, and its result is wrapped in Expr(:hygienic-scope, result,
// module, line) so that lowering's resolve-expansion-vars renames what the
// macro introduced (macroexpand.scm, already in package lower).

// macroCtx is struct macroctx_stack: the module a macro's result is hygienic
// in, and the enclosing one.
type macroCtx struct {
	m      *Module
	parent *macroCtx
}

// biExpr is jl_f__expr (builtins.c:1670-1685): Expr(head, args...).
func biExpr(vm *VM, args []any) (any, error) {
	if err := vm.nargs("Expr", args, 1, -1); err != nil {
		return nil, err
	}
	head, ok := args[0].(expr.Symbol)
	if err := vm.typechk("Expr", core.SymbolType, args[0], ok); err != nil {
		return nil, err
	}
	return &expr.Expr{Head: head, Args: append([]any{}, args[1:]...)}, nil
}

// macroexpand is jl_expand_macros over a copy of form, in the newest world
// (jl_expand_with_loc_warn, ast.c).
func (s *Session) macroexpand(form any) (any, error) {
	return s.expandMacros(copyAST(form), nil)
}

// isSelfQuoting is is_self_quoting_expr (ast.c:1076-1086).
func isSelfQuoting(e *expr.Expr) bool {
	switch e.Head {
	case "inert", "core", "line", "lineinfo", "meta", "boundscheck", "inline", "noinline":
		return true
	}
	return false
}

// needEsc is need_esc_node (ast.c:1090-1102): a value that can hold
// symbols or have effects, so that hygiene concerns it.
func needEsc(v any) bool {
	switch x := v.(type) {
	case expr.LineNumberNode, expr.SSAValue, expr.SlotNumber, expr.Argument, expr.QuoteNode:
		return false
	case *expr.Expr:
		return !isSelfQuoting(x)
	case expr.Symbol, expr.GlobalRef, expr.GotoNode, expr.GotoIfNot, expr.ReturnNode,
		expr.NewvarNode, *expr.CodeInfo:
		// jl_is_ast_node (ast.c:1055-1074)
		return true
	}
	return false
}

// expandMacros is jl_expand_macros (ast.c:1162-1254), onelevel false.
func (s *Session) expandMacros(v any, ctx *macroCtx) (any, error) {
	e, ok := v.(*expr.Expr)
	if !ok {
		return v, nil
	}
	switch e.Head {
	case "inert", "module", "toplevel", "meta":
		return v, nil
	case "quote":
		if len(e.Args) == 1 {
			// julia-bq-macro reports a malformed quote as an (error …) form
			// in its result; its Go error is a broken invariant only
			return s.expandMacros(must(s.low.Backquote(e.Args[0])), ctx)
		}
	case "hygienic-scope":
		if len(e.Args) >= 2 {
			m, err := s.VM.moduleOf(e.Args[1])
			if err != nil {
				return nil, err
			}
			a := e.Args[0]
			a2, err := s.expandMacros(a, &macroCtx{m: m, parent: ctx})
			if err != nil {
				return nil, err
			}
			if x, ok := a2.(*expr.Expr); ok && x.Head == "escape" && len(x.Args) > 0 && !needEsc(x.Args[0]) {
				return x.Args[0], nil
			}
			if !needEsc(a2) {
				return a2, nil
			}
			e.Args[0] = a2
			return e, nil
		}
	case "macrocall":
		return s.expandMacroCall(e, ctx)
	case "do":
		if len(e.Args) == 2 {
			if mc, ok := e.Args[0].(*expr.Expr); ok && mc.Head == "macrocall" && len(mc.Args) >= 2 {
				// @m(args) do x … end: the do-block is the first argument
				args := append([]any{mc.Args[0], mc.Args[1], e.Args[1]}, mc.Args[2:]...)
				return s.expandMacros(&expr.Expr{Head: "macrocall", Args: args}, ctx)
			}
		}
	case "escape":
		if ctx != nil {
			ctx = ctx.parent
		}
	}
	for i, a := range e.Args {
		a2, err := s.expandMacros(a, ctx)
		if err != nil {
			return nil, err
		}
		e.Args[i] = a2
	}
	return e, nil
}

// expandMacroCall is the macrocall branch of jl_expand_macros. A macro whose
// result calls it again would expand without end: the nesting counts as
// depth, like calls, and raises StackOverflowError past VM.MaxDepth.
func (s *Session) expandMacroCall(e *expr.Expr, ctx *macroCtx) (any, error) {
	vm := s.VM
	if vm.depth >= vm.MaxDepth {
		return nil, vm.exception(vm.tStackOverflow)
	}
	vm.depth++
	defer func() { vm.depth-- }()
	newctx := &macroCtx{m: s.Mod, parent: ctx}
	if ctx != nil {
		newctx.m = ctx.m
	}
	result, line, err := s.invokeMacro(e.Args, newctx)
	if err != nil {
		return nil, err
	}
	if !needEsc(result) {
		return result, nil
	}
	wrap := true
	if x, ok := result.(*expr.Expr); ok && x.Head == "escape" && len(x.Args) > 0 {
		result, wrap = x.Args[0], false
	}
	result = copyAST(result)
	inner := ctx
	if wrap {
		inner = newctx
	}
	if result, err = s.expandMacros(result, inner); err != nil {
		return nil, err
	}
	if wrap && needEsc(result) {
		if x, ok := result.(*expr.Expr); ok && x.Head == "escape" && len(x.Args) > 0 {
			return x.Args[0], nil
		}
		return expr.New("hygienic-scope", result, exprModule(newctx.m), line), nil
	}
	return result, nil
}

// invokeMacro is jl_invoke_julia_macro (ast.c:1104-1160): the macro's
// method called with __source__, __module__ and the arguments; ctx.m
// becomes the module of the method that ran.
func (s *Session) invokeMacro(args []any, ctx *macroCtx) (any, expr.LineNumberNode, error) {
	vm := s.VM
	if len(args) < 2 {
		// JL_NARGSV("macrocall", 3) over the macro, location and module
		return nil, expr.LineNumberNode{}, vm.tooFew("macrocall", 3)
	}
	line, ok := args[1].(expr.LineNumberNode)
	if !ok {
		line = expr.LineNumberNode{Line: 0, File: nil}
	}
	vm.taskWorld = vm.World()
	f, err := vm.evalMacroName(ctx.m, args[0])
	if err != nil {
		return nil, line, err
	}
	margs := append([]any{line, s.Mod}, args[2:]...)
	if fn, ok := f.(*Function); ok {
		if m, _, err := fn.Table.Lookup(vm.argTupleType(margs), vm.taskWorld); err == nil {
			if l, ok := m.Def.(*lambda); ok {
				ctx.m = l.mod
			}
		} else if !errors.Is(err, dispatch.ErrNoMethod) && !errors.Is(err, dispatch.ErrAmbiguous) {
			return nil, line, err
		}
	}
	res, err := vm.apply(f, margs)
	return res, line, err
}

// evalMacroName is jl_toplevel_eval of a macro call's name in module m: a
// symbol, a GlobalRef, or a qualified name A.@m (jl_eval_dot_expr,
// toplevel.c), which reads a module's global without Base.getproperty.
func (vm *VM) evalMacroName(m *Module, v any) (any, error) {
	if e, ok := v.(*expr.Expr); ok && e.Head == "." && len(e.Args) == 2 {
		if q, ok := e.Args[1].(expr.QuoteNode); ok {
			if name, ok := q.Value.(expr.Symbol); ok {
				lhs, err := vm.evalMacroName(m, e.Args[0])
				if err != nil {
					return nil, err
				}
				mod, ok := lhs.(*Module)
				if !ok {
					return nil, vm.typeError("getglobal", "", core.ModuleType, lhs)
				}
				return vm.globalValue(mod, name)
			}
		}
	}
	return vm.toplevelEval(m, v)
}

// moduleOf is the runtime module a hygienic-scope names.
func (vm *VM) moduleOf(v any) (*Module, error) {
	switch x := vm.constValue(v).(type) {
	case *Module:
		return x, nil
	}
	return nil, vm.typeError("hygienic-scope", "", core.ModuleType, v)
}

// exprModule is the value of module m inside syntax: its full name.
func exprModule(m *Module) expr.Module {
	var path []expr.Symbol
	for x := m; x != nil; x = x.Parent {
		path = append([]expr.Symbol{x.Name}, path...)
	}
	return expr.Module{Path: path}
}
