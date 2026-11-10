package interp

import (
	"errors"
	"fmt"
	"strings"

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
	lowered, err := s.low.Lower(syntaxModules(form))
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

// addDefaultNames is jl_add_default_names (module.c:535-548): m uses Core,
// and holds its own name, exported so that `using M` shows M.
func (vm *VM) addDefaultNames(m *Module, usingCore, selfName bool) {
	if usingCore {
		m.usings = append(m.usings, vm.Core)
	}
	if selfName {
		self := m.declare(m.Name, 0)
		self.Value, self.Const, self.Exports = m, true, true
	}
}

// addStandardImports is jl_add_standard_imports (toplevel.c:39-46): m uses
// the top module above it, Base (Core before Base exists), and names Core
// and that module.
func (vm *VM) addStandardImports(m *Module) *Module {
	base := vm.baseRelativeTo(m)
	m.usings = append(m.usings, base)
	for _, std := range []*Module{vm.Core, base} {
		b := m.declare(std.Name, 0)
		b.Value, b.Const = std, true
	}
	return base
}

// newModuleShim is jl_f_new_module (module.c:662-673), Core.Module(name,
// std_imports, default_names): a module whose parent is Main, bound in no
// module.
func (vm *VM) newModuleShim(a []any) (any, error) {
	name, ok := a[0].(expr.Symbol)
	if !ok {
		return nil, vm.typeError("ccall", "", core.SymbolType, a[0])
	}
	std, _ := a[1].(bool)
	names, _ := a[2].(bool)
	m := vm.addModule(name, vm.Main)
	vm.addDefaultNames(m, names, names)
	if std {
		vm.addStandardImports(m)
	}
	return m, nil
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
	// a module defined in Base.__toplevel__ (what require includes a
	// package into) has no parent and is registered as a root module by
	// Base.register_root_module (toplevel.c:107-111, 136, 182-183)
	isTop := false
	if vm.baseModule != nil {
		if tb := vm.baseModule.lookup("__toplevel__", vm.taskWorld); tb != nil && tb.Value == any(parent) {
			isTop = true
		}
	}
	owner := parent
	if isTop {
		owner = nil
	}
	newm := vm.addModule(name, owner)
	// the package's UUID is its parent's (toplevel.c:143)
	newm.uuid = parent.uuid
	// Core stays used with the standard imports, which julia gets through
	// Base's exports
	vm.addDefaultNames(newm, true, true)
	if e.Args[0] == true && vm.baseModule != nil {
		base := vm.addStandardImports(newm)
		// its own include and eval, Base.IncludeInto and Core.EvalInto of
		// it, once they exist (toplevel.c:163-172)
		for _, d := range []struct {
			name expr.Symbol
			from *Module
			typ  expr.Symbol
		}{{"include", base, "IncludeInto"}, {"eval", vm.Core, "EvalInto"}} {
			tb := d.from.lookup(d.typ, vm.taskWorld)
			if tb == nil || tb.Value == undef {
				continue
			}
			form, err := vm.newStruct(tb.Value, []any{newm})
			if err != nil {
				return nil, err
			}
			b := newm.declare(d.name, 0)
			b.Value, b.Const = form, true
		}
	}
	// the Base of the bootstrap (toplevel.c:177-180)
	if parent == vm.Main && name == "Base" && vm.baseModule == nil {
		vm.baseModule = newm
		vm.replaceBase(newm)
	}
	if isTop {
		reg := vm.baseModule.lookup("register_root_module", vm.taskWorld)
		if reg == nil || reg.Value == undef {
			return nil, vm.errorf("Base.register_root_module is not defined")
		}
		if _, err := vm.apply(reg.Value, []any{newm}); err != nil {
			return nil, err
		}
	} else {
		b := parent.declare(name, vm.newWorld())
		b.Value, b.Const = newm, true
	}
	s := vm.sessionFor(newm)
	bootstrapBase := parent == vm.Main && name == "Base" && newm == vm.baseModule
	// open while its forms run (jl_current_modules, toplevel.c:182-205)
	vm.openModules[newm]++
	closed := false
	closeModule := func() {
		if !closed {
			closed = true
			if vm.openModules[newm]--; vm.openModules[newm] == 0 {
				delete(vm.openModules, newm)
			}
		}
	}
	defer closeModule()
	for _, form := range body.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		// the bootstrap Base's Compiler.bootstrap!() without its inference of
		// the compiler's entry points: only the activate_codegen!() it ends
		// with (bootstrap.jl:77), which records the inference world
		// (VM.RunCompilerBootstrap)
		if bootstrapBase && !vm.RunCompilerBootstrap && isCompilerBootstrap(form) {
			form = expr.New("call", expr.New(".", expr.Symbol("Compiler"), expr.QuoteNode{Value: expr.Symbol("activate_codegen!")}))
		}
		vm.taskWorld = vm.World()
		if _, err := s.evalForm(form); err != nil {
			return nil, err
		}
	}
	closeModule()
	// its __init__, and its submodules', once no module around it is open
	// (toplevel.c:207-242)
	if err := vm.moduleDefined(newm); err != nil {
		return nil, err
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
				a, asname, ok := importAs(a)
				if !ok {
					return malformed
				}
				path, ok := a.(*expr.Expr)
				if !ok || path.Head != "." || len(path.Args) != 1 {
					return fmt.Errorf("%w: the %s item %v", ErrUnsupported, verb, a)
				}
				name, ok := path.Args[0].(expr.Symbol)
				if !ok {
					return malformed
				}
				if asname == "" {
					asname = name
				} else if err := vm.checkMacroRename(name, asname, verb); err != nil {
					return err
				}
				vm.importName(m, from, name, asname, verb == "using")
			}
			return nil
		}
	}
	for _, a := range e.Args {
		a, asname, ok := importAs(a)
		if !ok || asname != "" && verb == "using" {
			// `using A as B` is not a form (toplevel.c:853)
			return malformed
		}
		path, ok := a.(*expr.Expr)
		if !ok || path.Head != "." {
			return fmt.Errorf("%w: the %s form %v", ErrUnsupported, verb, a)
		}
		if verb == "import" {
			mod, name, err := vm.importTarget(m, path, verb)
			if err != nil {
				return err
			}
			if name != "" {
				// `import A.b`, `import ..b`, `import A.b as c`
				if asname == "" {
					asname = name
				} else if err := vm.checkMacroRename(name, asname, verb); err != nil {
					return err
				}
				vm.importName(m, mod, name, asname, false)
				continue
			}
			if asname == "" {
				asname = mod.Name
			}
			// `import A`, `import A as B`
			b := m.declare(asname, vm.newWorld())
			if b.Value == undef {
				b.Value, b.Const = mod, true
			}
			continue
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
	if !ok || path.Head != "." {
		return nil, vm.errorf("syntax: malformed \"%s\" statement", verb)
	}
	if len(path.Args) == 0 {
		return nil, vm.errorf("malformed \"%s\" statement", verb) // toplevel.c:510-511
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
			// Core's name is Core, and the Base being defined is Base
			// before it is a root module (toplevel.c:525-530)
			if name == vm.Core.Name {
				cur = vm.Core
				continue
			}
			if vm.baseModule != nil && name == vm.baseModule.Name {
				cur = vm.baseModule
				continue
			}
			// once Base defines require, a package is what it finds and loads
			// (call_require, toplevel.c:475-501, 531-533)
			var req *Binding
			if vm.baseModule != nil {
				req = vm.baseModule.lookup("require", vm.taskWorld)
			}
			if req != nil && req.Value != undef {
				var err error
				if v, err = vm.apply(req.Value, []any{m, name}); err != nil {
					return nil, err
				}
			} else if mod := vm.modules[name]; mod != nil && mod.Parent == nil {
				cur = mod
				continue
			} else if b := m.lookup(name, vm.taskWorld); b != nil {
				v = b.Value
			} else {
				return nil, fmt.Errorf("%w: loading the package %s", ErrUnsupported, name)
			}
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

// importName is jl_module_import over module_import_ (module.c:1232-1311):
// name in to becomes an alias of from's binding, in a new world. A name from
// does not have yet is made a binding of it, undefined, with a warning; a
// name to already holds otherwise is kept, with a warning. A binding
// declared and not assigned counts as undefined here: one value per
// binding does not tell julia's guard from its declared kind. The binding
// in to is named asname (`import A: x as y`); the warnings name the
// binding imported, as julia's do. using marks `using A: x`, whose
// binding is of julia's explicit kind, not imported.
func (vm *VM) importName(to *Module, from *Module, name, asname expr.Symbol, using bool) {
	// jl_get_binding then jl_walk_binding_inplace: from's own binding, or
	// the one from reaches through its usings (Base's Pair is Core's);
	// else the name becomes a binding of from, undefined
	b := from.lookup(name, vm.taskWorld)
	if b == nil {
		b = from.declare(name, vm.taskWorld)
		b.implicit = true
	}
	if b.resolve().Value == undef {
		vm.warn("WARNING: Imported binding %s.%s was undeclared at import time during import to %s.\n", from.Name, name, to.Name)
	}
	bto := to.own(asname)
	switch {
	case bto == b:
		// importing a binding on top of itself: harmless
	case bto == nil || bto.implicit && bto.Value == undef && bto.Alias == nil:
		bto = to.declare(asname, vm.newWorld())
		bto.Alias, bto.From, bto.explicit = b, from, using
		bto.implicit = false // an export made it; the import defines it
	case bto.resolve() == b.resolve():
		// already imported
	case bto.Alias != nil:
		vm.warn("WARNING: ignoring conflicting import of %s.%s into %s\n", from.Name, name, to.Name)
	default:
		vm.warn("WARNING: import of %s.%s into %s conflicts with an existing identifier; ignored.\n", from.Name, name, to.Name)
	}
}

// importAs splits an import item (as path asname) into its path and new
// name, "" for an item without one; false for a malformed one
// (toplevel.c:853-866, 897-915).
func importAs(a any) (any, expr.Symbol, bool) {
	as, ok := a.(*expr.Expr)
	if !ok || as.Head != "as" {
		return a, "", true
	}
	if len(as.Args) != 2 {
		return a, "", false
	}
	path, ok1 := as.Args[0].(*expr.Expr)
	asname, ok2 := as.Args[1].(expr.Symbol)
	if !ok1 || path.Head != "." || !ok2 {
		return a, "", false
	}
	return path, asname, true
}

// checkMacroRename is check_macro_rename (toplevel.c:685-692): a macro
// renamed only to a macro.
func (vm *VM) checkMacroRename(from, to expr.Symbol, verb string) error {
	fm, tm := strings.HasPrefix(string(from), "@"), strings.HasPrefix(string(to), "@")
	switch {
	case fm && !tm:
		return vm.errorf("cannot rename macro \"%s\" to non-macro \"%s\" in \"%s\"", from, to, verb)
	case !fm && tm:
		return vm.errorf("cannot rename non-macro \"%s\" to macro \"%s\" in \"%s\"", from, to, verb)
	}
	return nil
}

// warn prints a runtime warning, as jl_printf(JL_STDERR, …) does.
func (vm *VM) warn(format string, args ...any) {
	if vm.Stderr != nil {
		fmt.Fprintf(vm.Stderr, format, args...)
	}
}

// importTarget is eval_import_path (toplevel.c:507-570) for `import`
// without a colon: a lone root names a module to import, any other path
// ends in the name of a binding of the module the rest reaches.
func (vm *VM) importTarget(m *Module, path *expr.Expr, verb string) (*Module, expr.Symbol, error) {
	args := path.Args
	if len(args) <= 1 && (len(args) == 0 || args[0] != any(expr.Symbol("."))) {
		mod, err := vm.importPath(m, path, verb)
		return mod, "", err
	}
	dots := 0
	for dots < len(args) && args[dots] == any(expr.Symbol(".")) {
		dots++
	}
	if dots == len(args) {
		return nil, "", vm.errorf("invalid module path")
	}
	var mod *Module
	if dots == len(args)-1 {
		// `.x`, `..x`: this module, then a parent per further dot
		mod = m
		for i := 1; i < dots && mod.Parent != nil; i++ {
			mod = mod.Parent
		}
	} else {
		var err error
		if mod, err = vm.importPath(m, &expr.Expr{Head: ".", Args: args[:len(args)-1]}, verb); err != nil {
			return nil, "", err
		}
	}
	name, ok := args[len(args)-1].(expr.Symbol)
	if !ok {
		return nil, "", vm.typeError(expr.Symbol(verb), "", core.SymbolType, args[len(args)-1])
	}
	if name == "." {
		return nil, "", vm.errorf("invalid %s path: \".\" in identifier path", verb)
	}
	return mod, name, nil
}
