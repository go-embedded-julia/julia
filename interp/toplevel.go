package interp

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
	"github.com/go-embedded-julia/julia/syntax"
)

// Session is a module and the lowering state of its top-level forms, as a
// notebook keeps one per kernel.
type Session struct {
	VM  *VM
	Mod *Module
	low *lower.State
}

// NewSession returns a session in a fresh module Main.L, the module the
// lowering port names (lower.NewState).
func (vm *VM) NewSession() *Session {
	return &Session{VM: vm, Mod: vm.NewModule("L"), low: lower.NewState()}
}

// Eval parses src as a file of top-level statements and evaluates them in
// order, returning the value of the last one (include_string). Macro
// expansion is not ported: a statement holding a macro call is an
// ErrUnsupported error.
func (s *Session) Eval(src string) (any, error) {
	top, ok := syntax.ParseAll([]byte(src), "none").(*expr.Expr)
	if !ok || top.Head != "toplevel" {
		return nil, fmt.Errorf("%w: the parser returned %v", ErrUnsupported, top)
	}
	var res any
	for _, form := range top.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		if e, ok := form.(*expr.Expr); ok && (e.Head == "error" || e.Head == "incomplete") {
			return nil, s.VM.syntaxError(e)
		}
		if hasMacroCall(form) {
			return nil, fmt.Errorf("%w: macro expansion", ErrUnsupported)
		}
		lowered, err := s.low.Lower(form)
		if err != nil {
			return nil, err
		}
		res, err = s.VM.toplevelEval(s.Mod, lowered)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

func hasMacroCall(v any) bool {
	e, ok := v.(*expr.Expr)
	if !ok {
		return false
	}
	if e.Head == "macrocall" {
		return true
	}
	if e.Head == "quote" {
		return false
	}
	for _, a := range e.Args {
		if hasMacroCall(a) {
			return true
		}
	}
	return false
}

// syntaxError is the :error / :incomplete branch of jl_toplevel_eval_flex
// (toplevel.c:981-989).
func (vm *VM) syntaxError(e *expr.Expr) error {
	if len(e.Args) == 0 {
		return vm.errorf("malformed \"%s\" expression", e.Head)
	}
	switch m := e.Args[0].(type) {
	case string:
		return vm.errorf("syntax: %s", m)
	case *expr.ParseError:
		return vm.errorf("syntax: %s", m.Msg)
	}
	return &Thrown{Value: e.Args[0]}
}

// toplevelEval is jl_toplevel_eval_flex (toplevel.c:754-1005) for lowered
// input: every statement starts in the newest world.
func (vm *VM) toplevelEval(m *Module, e any) (any, error) {
	vm.taskWorld = vm.World()
	switch x := e.(type) {
	case expr.GlobalRef:
		mod := vm.modules[x.Mod]
		if mod == nil {
			return nil, fmt.Errorf("%w: a GlobalRef to the unknown module %s", ErrUnsupported, x.Mod)
		}
		return vm.globalValue(mod, x.Name)
	case expr.Symbol:
		return vm.globalValue(m, x)
	case expr.LineNumberNode:
		return nil, nil
	case *expr.Expr:
		switch x.Head {
		case "thunk":
			src, ok := firstArg(x).(*expr.CodeInfo)
			if !ok {
				return nil, vm.errorf("malformed \"thunk\" statement")
			}
			c, err := prepare(src, true)
			if err != nil {
				return nil, err
			}
			v, err := vm.newFrame(c, m).evalBody(0)
			var leave *leaveSignal
			if errors.As(err, &leave) {
				return nil, vm.errorf("Invalid IR: :leave outside a try block")
			}
			return v, err
		case "error", "incomplete":
			return nil, vm.syntaxError(x)
		}
		return vm.toplevelOnly(m, x)
	}
	return vm.constValue(e), nil
}

func firstArg(e *expr.Expr) any {
	if len(e.Args) == 0 {
		return nil
	}
	return e.Args[0]
}

// toplevelOnly evaluates the forms only the top level runs: import, using,
// export, public and nested toplevel blocks.
func (vm *VM) toplevelOnly(m *Module, e *expr.Expr) (any, error) {
	switch e.Head {
	case "import", "using":
		return nil, vm.evalImport(m, e)
	case "export", "public":
		for _, a := range e.Args {
			name, ok := a.(expr.Symbol)
			if !ok {
				return nil, vm.errorf("syntax: malformed \"%s\" statement", e.Head)
			}
			b := m.declare(name, vm.newWorld())
			b.Exports = b.Exports || e.Head == "export"
		}
		return nil, nil
	case "toplevel":
		var res any
		for _, a := range e.Args {
			v, err := vm.toplevelEval(m, a)
			if err != nil {
				return nil, err
			}
			res = v
		}
		return res, nil
	}
	return nil, fmt.Errorf("%w: the top-level form %s", ErrUnsupported, e.Head)
}

// evalImport handles `import A.B`, `import A.B: x, y`, `using A.B` and
// `using A.B: x` (toplevel.c:818-922).
func (vm *VM) evalImport(m *Module, e *expr.Expr) error {
	verb := string(e.Head)
	malformed := vm.errorf("syntax: malformed \"%s\" statement", verb)
	if len(e.Args) == 1 {
		if c, ok := e.Args[0].(*expr.Expr); ok && c.Head == ":" {
			if len(c.Args) < 2 {
				return malformed
			}
			from, err := vm.importPath(m, c.Args[0], verb)
			if err != nil {
				return err
			}
			for _, a := range c.Args[1:] {
				path, ok := a.(*expr.Expr)
				if !ok || path.Head != "." || len(path.Args) != 1 {
					return fmt.Errorf("%w: the %s item %v", ErrUnsupported, verb, a)
				}
				name, ok := path.Args[0].(expr.Symbol)
				if !ok {
					return malformed
				}
				if err := vm.importName(m, from, name); err != nil {
					return err
				}
			}
			return nil
		}
	}
	for _, a := range e.Args {
		path, ok := a.(*expr.Expr)
		if !ok || path.Head != "." {
			return fmt.Errorf("%w: the %s form %v", ErrUnsupported, verb, a)
		}
		mod, err := vm.importPath(m, path, verb)
		if err != nil {
			return err
		}
		if verb == "using" {
			m.mu.Lock()
			m.usings = append(m.usings, mod)
			m.mu.Unlock()
		}
		b := m.declare(mod.Name, vm.newWorld())
		if b.Value == undef {
			b.Value, b.Const = mod, true
		}
	}
	return nil
}

// importPath resolves (. A B …) to a module.
func (vm *VM) importPath(m *Module, v any, verb string) (*Module, error) {
	path, ok := v.(*expr.Expr)
	if !ok || path.Head != "." || len(path.Args) == 0 {
		return nil, vm.errorf("syntax: malformed \"%s\" statement", verb)
	}
	var cur *Module
	for i, a := range path.Args {
		name, ok := a.(expr.Symbol)
		if !ok || name == "." {
			return nil, fmt.Errorf("%w: a relative %s path", ErrUnsupported, verb)
		}
		var v any
		if i == 0 {
			if mod := vm.modules[name]; mod != nil && mod.Parent == nil {
				cur = mod
				continue
			}
			b := m.lookup(name, vm.taskWorld)
			if b == nil {
				return nil, fmt.Errorf("%w: loading the package %s", ErrUnsupported, name)
			}
			v = b.Value
		} else {
			b := cur.own(name)
			if b == nil {
				return nil, vm.undefVar(name, vm.taskWorld, cur)
			}
			v = b.resolve().Value
		}
		mod, ok := v.(*Module)
		if !ok {
			return nil, vm.errorf("invalid %s path: \"%s\" does not name a module", verb, name)
		}
		cur = mod
	}
	return cur, nil
}

// importName is jl_module_import: name in m becomes an alias of from's
// binding, in a new world.
func (vm *VM) importName(m *Module, from *Module, name expr.Symbol) error {
	src := from.own(name)
	if src == nil {
		return vm.undefVar(name, vm.taskWorld, from)
	}
	b := m.declare(name, vm.newWorld())
	if b != src {
		b.Alias = src.resolve()
	}
	return nil
}
