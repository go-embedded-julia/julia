package interp

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/core"
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

// CoreSession returns a session in Core itself, whose top module is Core,
// as boot.jl is evaluated.
func (vm *VM) CoreSession() *Session { return vm.sessionFor(vm.Core) }

// Eval parses src as a file of top-level statements and evaluates them in
// order, returning the value of the last one (include_string). Macro
// expansion is not ported: a statement holding a macro call is an
// ErrUnsupported error.
func (s *Session) Eval(src string) (res any, err error) {
	// a broken invariant of the interpreter is a Go error for the embedder,
	// never a panic that takes the notebook server down with the cell
	defer func() {
		if r := recover(); r != nil {
			res, err = nil, fmt.Errorf("%w: %v", ErrInternal, r)
		}
	}()
	// ParseAll returns Expr(:toplevel, …), or Expr(:error, …) for an input
	// over its limits
	top := syntax.ParseAll([]byte(src), "none").(*expr.Expr)
	if top.Head != "toplevel" {
		return nil, s.VM.syntaxError(top)
	}
	for _, form := range top.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		v, err := s.evalForm(form)
		if err != nil {
			return nil, err
		}
		res = v
	}
	return res, nil
}

// evalForm expands the macros of one parsed top-level form, lowers it and
// evaluates it (jl_expand_with_loc_warn, then jl_toplevel_eval_flex).
func (s *Session) evalForm(form any) (any, error) {
	if e, ok := form.(*expr.Expr); ok && (e.Head == "error" || e.Head == "incomplete") {
		return nil, s.VM.syntaxError(e)
	}
	form, err := s.macroexpand(form)
	if err != nil {
		return nil, err
	}
	s.low.SetTop(s.VM.baseRelativeTo(s.Mod).Name)
	lowered, err := s.low.Lower(form)
	if err != nil {
		return nil, err
	}
	return s.VM.toplevelEval(s.Mod, lowered)
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
			b := m.own(name)
			if b == nil {
				// jl_module_public: the name only, its binding implicit
				b = m.declare(name, vm.newWorld())
				b.implicit = true
			}
			b.Exports = b.Exports || e.Head == "export"
		}
		return nil, nil
	case "global":
		// jl_declare_global(m, arg, NULL, 0) for each name
		// (toplevel.c:951-959): a weak declaration, a binding without a value
		for _, a := range e.Args {
			switch x := a.(type) {
			case expr.Symbol:
				if m.own(x) == nil {
					m.declare(x, vm.newWorld())
				}
			case expr.GlobalRef:
				if err := vm.declareGlobal(x, core.AnyType); err != nil {
					return nil, err
				}
			default:
				return nil, fmt.Errorf("%w: a global declaration of %v", ErrUnsupported, a)
			}
		}
		return nil, nil
	case "module":
		return vm.evalModule(m, e)
	case "thunk":
		// a lowered form inside another (jl_toplevel_eval_flex's thunk case)
		return vm.toplevelEval(m, e)
	case "toplevel":
		// each form expanded and lowered in turn, in the latest world
		// (toplevel.c:965-975)
		var res any
		s := vm.sessionFor(m)
		for _, a := range e.Args {
			vm.taskWorld = vm.World()
			v, err := s.evalForm(a)
			if err != nil {
				return nil, err
			}
			res = v
		}
		return res, nil
	}
	return nil, fmt.Errorf("%w: the top-level form %s", ErrUnsupported, e.Head)
}

// evalModule is jl_eval_module_expr (toplevel.c:114-198) for
// (module std_imports name (block forms…)): a submodule of parent bound in
// it as a constant, whose forms are expanded and evaluated in order. Every
// module uses Core; one with the standard imports also uses Base.
func (vm *VM) evalModule(parent *Module, e *expr.Expr) (any, error) {
	if len(e.Args) != 3 {
		return nil, vm.errorf("syntax: malformed module expression")
	}
	body, ok := e.Args[2].(*expr.Expr)
	if !ok {
		return nil, vm.errorf("syntax: malformed module expression")
	}
	if body.Head != "block" {
		return nil, vm.errorf("syntax: module expression third argument must be a block")
	}
	name, ok := e.Args[1].(expr.Symbol)
	if !ok {
		return nil, vm.typeError("module", "", core.SymbolType, e.Args[1])
	}
	newm := vm.addModule(name, parent)
	newm.usings = []*Module{vm.Core}
	// its own name, exported (jl_add_default_names, module.c:535-548)
	self := newm.declare(name, 0)
	self.Value, self.Const, self.Exports = newm, true, true
	// standard imports once a Base exists: using the top module above it
	// (jl_add_standard_imports, toplevel.c:39-46); Core stays used, which
	// julia gets through Base's exports
	if e.Args[0] == true && vm.baseModule != nil {
		base := vm.baseRelativeTo(newm)
		newm.usings = append(newm.usings, base)
		for _, std := range []*Module{vm.Core, base} {
			b := newm.declare(std.Name, 0)
			b.Value, b.Const = std, true
		}
	}
	// the Base of the bootstrap (toplevel.c:177-180)
	if parent == vm.Main && name == "Base" && vm.baseModule == nil {
		vm.baseModule = newm
		vm.replaceBase(newm)
	}
	b := parent.declare(name, vm.newWorld())
	b.Value, b.Const = newm, true
	s := vm.sessionFor(newm)
	for _, form := range body.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		vm.taskWorld = vm.World()
		if _, err := s.evalForm(form); err != nil {
			return nil, err
		}
	}
	return newm, nil
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
	args := path.Args
	if args[0] == any(expr.Symbol(".")) {
		// `.A.B`: the first dot is this module, each further one its
		// parent (toplevel.c:537-550)
		cur = m
		args = args[1:]
		for len(args) > 0 && args[0] == any(expr.Symbol(".")) {
			if cur.Parent != nil {
				cur = cur.Parent
			}
			args = args[1:]
		}
		if len(args) == 0 {
			return nil, vm.errorf("invalid module path")
		}
	}
	for i, a := range args {
		name, ok := a.(expr.Symbol)
		if !ok {
			return nil, vm.typeError(expr.Symbol(verb), "", core.SymbolType, a)
		}
		if name == "." {
			return nil, vm.errorf("invalid %s path: \".\" in identifier path", verb)
		}
		var v any
		if i == 0 && cur == nil {
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
			// jl_eval_global_var (toplevel.c:562): through usings too
			b := cur.lookup(name, vm.taskWorld)
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
