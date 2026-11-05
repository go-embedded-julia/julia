package interp

import (
	"errors"
	"fmt"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// frame is interpreter_state (interpreter.c:17-27): one locals array, the
// slots then the SSA values, the static parameters and continue_at.
type frame struct {
	vm      *VM
	c       *code
	mod     *Module
	locals  []any
	sparams []any
	spenv   any // the UnionAlls sparams are the values of, nil for none
	// preevaluation is interpreter_state's (interpreter.c:25): sparams may
	// be type variables, the static parameters of a method being defined
	preevaluation bool
	continueAt    int
	ip            int // the statement running, for Thrown.Trace
}

// leaveSignal carries a :leave up through the evalBody calls of the try
// blocks it exits; julia longjmps to the outermost of them
// (interpreter.c:607-622).
type leaveSignal struct{ n int }

func (*leaveSignal) Error() string { return "interp: :leave escaped its try blocks" }

func (vm *VM) newFrame(c *code, mod *Module) *frame {
	locals := make([]any, c.nslots+c.nssa)
	for i := range locals {
		locals[i] = undef
	}
	return &frame{vm: vm, c: c, mod: mod, locals: locals}
}

// evalBody is eval_body (interpreter.c:470-712).
func (f *frame) evalBody(ip int) (any, error) {
	vm := f.vm
	ns := len(f.c.stmts)
	for {
		if ip < 0 || ip >= ns {
			return nil, vm.errorf("`body` expression must terminate in `return`. Use `block` instead.")
		}
		if vm.MaxSteps > 0 {
			if vm.steps++; vm.steps > vm.MaxSteps {
				return nil, ErrStepLimit
			}
		}
		next := ip + 1
		f.ip = ip
		switch s := f.c.stmts[ip].(type) {
		case sGoto:
			next = s.to
		case sGotoIfNot:
			cond, err := f.evalValue(s.cond)
			if err != nil {
				return nil, err
			}
			switch cond {
			case false:
				next = s.to
			case true:
			default:
				return nil, vm.typeError("if", "", core.BoolType, cond)
			}
		case sReturn:
			return f.evalValue(s.v)
		case sEnter:
			// the exception-stack depth is the statement's value, consumed
			// by :pop_exception (interpreter.c:540)
			f.locals[f.c.nslots+ip] = uint64(len(vm.excStack))
			if s.hasScope {
				if _, err := f.evalValue(s.scope); err != nil {
					return nil, err
				}
			}
			// a try block is a nested evalBody, as in julia (a setjmp per
			// handler): IR that jumps back to an EnterNode without leaving
			// it nests without bound, so the nesting counts as depth
			if vm.depth >= vm.MaxDepth {
				return nil, vm.exception(vm.tStackOverflow)
			}
			vm.depth++
			v, err := f.evalBody(next)
			vm.depth--
			var leave *leaveSignal
			switch {
			case err == nil:
				return v, nil
			case errors.As(err, &leave):
				if leave.n--; leave.n > 0 {
					return nil, err
				}
				next, f.continueAt = f.continueAt, 0
			default:
				var thrown *Thrown
				if !errors.As(err, &thrown) || s.catch < 0 {
					return nil, err
				}
				vm.excStack = append(vm.excStack, thrown.Value)
				next = s.catch
			}
		case sAssign:
			v, err := f.evalValue(s.rhs)
			if err != nil {
				return nil, err
			}
			f.locals[s.slot] = v
		case sLeave:
			if s.n > 0 {
				f.continueAt = next
				return nil, &leaveSignal{s.n}
			}
		case sPopExc:
			st, err := f.evalValue(s.state)
			if err != nil {
				return nil, err
			}
			n, ok := st.(uint64)
			if !ok || n > uint64(len(vm.excStack)) {
				return nil, vm.errorf("Invalid IR: :pop_exception of a value that is not an exception-stack state")
			}
			vm.excStack = vm.excStack[:n]
		case sNewvar:
			f.locals[s.slot] = undef
		case sValue:
			v, err := f.evalValue(s.v)
			if err != nil {
				return nil, err
			}
			f.locals[f.c.nslots+ip] = v
		case sMethodDef:
			if err := f.methodDef(s); err != nil {
				return nil, err
			}
		case sGlobalDecl:
			var setType any = core.AnyType
			if s.val != nil {
				v, err := f.evalValue(s.val)
				if err != nil {
					return nil, err
				}
				setType = v
			}
			if err := vm.declareGlobal(s.ref, setType); err != nil {
				return nil, err
			}
			f.locals[f.c.nslots+ip] = nil
		case sConst:
			v, err := f.evalValue(s.val)
			if err != nil {
				return nil, err
			}
			if err := vm.declareConst(s.ref, v); err != nil {
				return nil, err
			}
			f.locals[f.c.nslots+ip] = nil
		case sLatestWorld:
			vm.taskWorld = vm.World()
		case sToplevel:
			v, err := vm.toplevelOnly(f.mod, s.e)
			if err != nil {
				return nil, err
			}
			f.locals[f.c.nslots+ip] = v
		case sNop:
		case sMeta:
			moduleMeta(f.mod, s.args)
		case sRaise:
			return nil, vm.errorf("%s", s.msg)
		}
		ip = next
	}
}

// evalValue is eval_value (interpreter.c:198-364).
func (f *frame) evalValue(o op) (any, error) {
	vm := f.vm
	switch x := o.(type) {
	case oSSA:
		return f.locals[f.c.nslots+int(x)], nil
	case oSlot:
		v := f.locals[x.i]
		if v == undef {
			return nil, vm.undefVar(x.name, vm.taskWorld, expr.Symbol("local"))
		}
		return v, nil
	case oConst:
		return vm.constValue(x.v), nil
	case oGlobal:
		m := vm.modules[x.mod]
		if m == nil {
			return nil, fmt.Errorf("%w: a GlobalRef to the unknown module %s", ErrUnsupported, x.mod)
		}
		return vm.globalValue(m, x.name)
	case oSymbol:
		return vm.globalValue(f.mod, x.name)
	case oCall:
		args := make([]any, len(x.args))
		for i, a := range x.args {
			v, err := f.evalValue(a)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		return vm.apply(args[0], args[1:])
	case oNew:
		args := make([]any, len(x.args))
		for i, a := range x.args {
			v, err := f.evalValue(a)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		return vm.newStruct(args[0], args[1:])
	case oSplatnew:
		t, err := f.evalValue(x.args[0])
		if err != nil {
			return nil, err
		}
		tup, err := f.evalValue(x.args[1])
		if err != nil {
			return nil, err
		}
		return vm.newStructT(t, tup)
	case oIsdefSlot:
		return f.locals[x.i] != undef, nil
	case oIsdefSparam:
		if x.n > len(f.sparams) {
			return nil, vm.errorf("could not determine static parameter value")
		}
		_, isvar := f.sparams[x.n-1].(*core.TypeVar)
		return !isvar, nil
	case oThrowUndefIfNot:
		cond, err := f.evalValue(x.cond)
		if err != nil {
			return nil, err
		}
		if cond == false {
			if x.name == "getfield_undefref" {
				return nil, vm.exception(vm.tUndefRef)
			}
			return nil, vm.undefVar(x.name, vm.taskWorld, expr.Symbol("local"))
		}
		return nil, nil
	case oSparam:
		if x.n > len(f.sparams) {
			return nil, vm.errorf("could not determine static parameter value")
		}
		sp := f.sparams[x.n-1]
		// a pre-evaluated ccall type keeps a type variable as it is
		// (interpreter.c:327)
		if tv, ok := sp.(*core.TypeVar); ok && !f.preevaluation {
			return nil, vm.undefVar(expr.Symbol(tv.Name), vm.taskWorld, expr.Symbol("static_parameter"))
		}
		return sp, nil
	case oTheException:
		if n := len(vm.excStack); n > 0 {
			return vm.excStack[n-1], nil
		}
		return nil, nil
	case oCopyast:
		v, err := f.evalValue(x.v)
		if err != nil {
			return nil, err
		}
		return copyAST(v), nil
	case oMethodDecl:
		m := f.mod
		if x.mod != "" {
			m = vm.modules[x.mod]
		}
		name, ok := x.name.(expr.Symbol)
		if m == nil || !ok {
			return nil, vm.errorf("method: invalid declaration")
		}
		return vm.declareFunction(m, name)
	case oForeign:
		return f.evalForeign(x)
	case oRaise:
		return nil, vm.errorf("%s", x.msg)
	case oUnsupported:
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, x.what)
	}
	panic(fmt.Sprintf("interp: unknown operand %T", o))
}

// constValue maps the Go values of package expr that name runtime objects
// to those objects: a module value to the module.
func (vm *VM) constValue(v any) any {
	if m, ok := v.(expr.Module); ok && len(m.Path) > 0 {
		if mod := vm.modules[m.Path[len(m.Path)-1]]; mod != nil {
			return mod
		}
	}
	return v
}

// copyAST is jl_copy_ast: a fresh copy of every Expr in v.
func copyAST(v any) any {
	e, ok := v.(*expr.Expr)
	if !ok {
		return v
	}
	args := make([]any, len(e.Args))
	for i, a := range e.Args {
		args[i] = copyAST(a)
	}
	return &expr.Expr{Head: e.Head, Args: args}
}

// globalValue is jl_eval_global_var: the value of name in m in the task's
// world, or an UndefVarError.
func (vm *VM) globalValue(m *Module, name expr.Symbol) (any, error) {
	b := m.lookup(name, vm.taskWorld)
	if b == nil || b.Value == undef {
		return nil, vm.undefVar(name, vm.taskWorld, m)
	}
	return b.Value, nil
}

// assignGlobal is jl_get_binding_wr plus jl_checked_assignment
// (module.c:820-857, 1901-1907).
func (vm *VM) assignGlobal(m *Module, name expr.Symbol, v any) error {
	b, err := vm.writableGlobal(m, name)
	if err != nil {
		return err
	}
	if err := vm.checkGlobalValue("setglobal!", m, name, b, v); err != nil {
		return err
	}
	b.Value = v
	return nil
}

// writableGlobal is jl_get_binding_wr: m's own binding of name, which must
// exist in the task's world and be neither an import nor a constant.
func (vm *VM) writableGlobal(m *Module, name expr.Symbol) (*Binding, error) {
	b := m.own(name)
	if b == nil || b.World > vm.taskWorld {
		if ub := m.lookup(name, vm.taskWorld); ub != nil {
			return nil, vm.errorf("cannot assign a value to imported variable %s.%s", m.Name, name)
		}
		return nil, vm.errorf("Global %s.%s does not exist and cannot be assigned.\n"+
			"Note: Julia 1.9 and 1.10 inadvertently omitted this error check (#56933).\n"+
			"Hint: Declare it using `global %s` inside `%s` before attempting assignment.",
			m.Name, name, name, m.Name)
	}
	if b.Alias != nil {
		// named after the module it came from (module.c:840-846)
		if b.From != nil && b.From != m {
			return nil, vm.errorf("cannot assign a value to imported variable %s.%s from module %s", b.From.Name, name, m.Name)
		}
		return nil, vm.errorf("cannot assign a value to imported variable %s.%s", m.Name, name)
	}
	if b.Const {
		return nil, vm.errorf("invalid assignment to constant %s.%s. This redefinition may be permitted using the `const` keyword.", m.Name, name)
	}
	return b, nil
}

// checkGlobalValue is jl_check_binding_assign_value (module.c:1886-1899):
// v must be of the global's declared type, a TypeError naming fname
// otherwise.
func (vm *VM) checkGlobalValue(fname expr.Symbol, m *Module, name expr.Symbol, b *Binding, v any) error {
	if b.Type != nil && !vm.isa(v, b.Type) {
		return vm.exception(vm.tTypeError, fname, expr.GlobalRef{Mod: m.Name, Name: name}, b.Type, v)
	}
	return nil
}

// declareGlobal is jl_declare_global (toplevel.c:270-331) for `global x` and
// `global x::T`: a binding declared in a new world when the module does not
// have one, with T as its type. A type is refused for a constant or an
// import, and for a global that has a value or another type.
func (vm *VM) declareGlobal(ref expr.GlobalRef, setType any) error {
	m := vm.modules[ref.Mod]
	if m == nil {
		return fmt.Errorf("%w: a global of the unknown module %s", ErrUnsupported, ref.Mod)
	}
	b := m.own(ref.Name)
	if b == nil {
		b = m.declare(ref.Name, vm.newWorld())
	}
	b.implicit = false
	if setType == any(core.AnyType) {
		return nil
	}
	switch {
	case b.Const:
		return vm.errorf("cannot set type for constant %s.%s.", m.Name, ref.Name)
	case b.Alias != nil:
		return vm.errorf("cannot set type for imported binding %s.%s.", m.Name, ref.Name)
	case b.Type != nil && core.Egal(b.Type, setType):
		return nil
	case b.Type != nil || b.Value != undef:
		return vm.errorf("cannot set type for global %s.%s. It already has a value or is already set to a different type.", m.Name, ref.Name)
	}
	b.Type = setType
	return nil
}

// declareConst is jl_eval_const_decl. julia gives a redefinition its own
// binding partition, so older worlds keep the old value; this version keeps
// one value per binding.
func (vm *VM) declareConst(ref expr.GlobalRef, val any) error {
	m := vm.modules[ref.Mod]
	if m == nil {
		return fmt.Errorf("%w: a constant of the unknown module %s", ErrUnsupported, ref.Mod)
	}
	w := vm.newWorld()
	b := m.declare(ref.Name, w)
	b.Const, b.Value, b.implicit = true, val, false // a definition, no longer only export's
	return nil
}

// declareFunction is jl_declare_const_gf (method.c:1135-1160): the generic
// function name in m, created as a constant in a new world when absent.
func (vm *VM) declareFunction(m *Module, name expr.Symbol) (any, error) {
	if b := m.own(name); b != nil {
		v := b.resolve().Value
		if v != undef {
			// jl_check_gf (method.c:1135-1139): an instance of a singleton
			// type (a function) or a type (a constructor) can take methods
			if t, ok := vm.typeOf(v).(*core.DataType); (ok && t.Name.Singleton) || core.IsType(v) {
				return v, nil
			}
			return nil, vm.errorf("cannot define function %s; it already has a value", name)
		}
	}
	fn := vm.newFunction(m, name)
	b := m.declare(name, vm.newWorld())
	b.Const, b.Value, b.implicit = true, fn, false
	return fn, nil
}

// lambda is a method body defined by lowered code.
type lambda struct {
	c     *code
	nargs int
	isva  bool
	mod   *Module
	// file and line are the definition's location (functionloc,
	// method.c:1229-1233), Method.file and .line
	file expr.Symbol
	line int32
	// sig is the method's signature, the UnionAlls of its static parameters
	sig any
}

// definitionEffects is jl_resolve_definition_effects_in_ir
// (method.c:81-256), as jl_method_set_source runs it with binding effects
// and the method's type variables (method.c:955) and a toplevel thunk with
// a ccall without them (toplevel.c:1017): a body's `global x` and
// `(globaldecl x)` declare their globals and become `nothing`; a ccall's
// return and argument types are evaluated; a Core.tuple of quoted values
// becomes the tuple. The body is copied when it changes.
func (vm *VM) definitionEffects(m *Module, src *expr.CodeInfo, sparams []any, bindingEffects bool) (*expr.CodeInfo, error) {
	var code []any
	set := func(i int, v any) {
		if code == nil {
			code = append([]any(nil), src.Code...)
		}
		code[i] = v
	}
	for i, st := range src.Code {
		v, changed, err := vm.resolveDefinitionEffects(st, m, sparams, bindingEffects)
		if err != nil {
			return nil, err
		}
		if changed {
			set(i, v)
		}
	}
	if code == nil {
		return src, nil
	}
	cp := *src
	cp.Code = code
	return &cp, nil
}

// resolveDefinitionEffects is resolve_definition_effects (method.c:81-246)
// of one statement: its replacement, and whether it changed.
func (vm *VM) resolveDefinitionEffects(st any, m *Module, sparams []any, bindingEffects bool) (any, bool, error) {
	e, ok := st.(*expr.Expr)
	if !ok {
		return st, false, nil
	}
	switch {
	case bindingEffects && (e.Head == "global" || e.Head == "globaldecl" && len(e.Args) == 1):
		for _, a := range e.Args {
			var ref expr.GlobalRef
			switch x := a.(type) {
			case expr.Symbol:
				ref = expr.GlobalRef{Mod: m.Name, Name: x}
			case expr.GlobalRef:
				ref = x
			default:
				return nil, false, vm.errorf("syntax: malformed \"%s\" statement", e.Head)
			}
			if err := vm.declareGlobal(ref, core.AnyType); err != nil {
				return nil, false, err
			}
		}
		return nil, true, nil
	case e.Head == "=" && len(e.Args) == 2:
		// not fully linearized: the right-hand side (method.c:106-108)
		rhs, changed, err := vm.resolveDefinitionEffects(e.Args[1], m, sparams, bindingEffects)
		if err != nil || !changed {
			return st, false, err
		}
		return expr.New("=", e.Args[0], rhs), true, nil
	case e.Head == "foreigncall" && len(e.Args) >= 5:
		// the return and argument types, evaluated now (method.c:175-217)
		args := append([]any(nil), e.Args...)
		changed := false
		if !core.IsType(args[1]) {
			rt, err := vm.evalExprIn(m, args[1], sparams)
			if err != nil {
				return nil, false, vm.couldNotEvaluate(err, "ccall return type")
			}
			args[1], changed = rt, true
		}
		if _, ok := args[2].(SVec); !ok {
			at, err := vm.evalExprIn(m, args[2], sparams)
			if err != nil {
				return nil, false, vm.couldNotEvaluate(err, "ccall argument type")
			}
			args[2], changed = at, true
		}
		if !changed {
			return st, false, nil
		}
		return expr.New(e.Head, args...), true, nil
	case e.Head == "call" && len(e.Args) > 0:
		// Core.tuple of quoted values, as the ccall callee (name, lib)
		// (method.c:218-244)
		g, ok := e.Args[0].(expr.GlobalRef)
		if !ok || !vm.isBuiltinTuple(g) {
			return st, false, nil
		}
		for _, a := range e.Args[1:] {
			if _, ok := a.(expr.QuoteNode); !ok {
				return st, false, nil
			}
		}
		v, err := vm.evalExprIn(m, e, sparams)
		if err != nil {
			return st, false, nil
		}
		return v, true, nil
	}
	return st, false, nil
}

// isBuiltinTuple is whether g names a constant binding holding Core.tuple.
func (vm *VM) isBuiltinTuple(g expr.GlobalRef) bool {
	mod := vm.modules[g.Mod]
	if mod == nil {
		return false
	}
	b := mod.lookup(g.Name, vm.World())
	return b != nil && b.Const && b.Value == vm.Core.own("tuple").Value
}

// couldNotEvaluate is resolve_definition_effects' error for a type that an
// ErrorException kept from being evaluated (method.c:184-189); any other
// exception is rethrown.
func (vm *VM) couldNotEvaluate(err error, what string) error {
	if t, ok := err.(*Thrown); ok {
		if s, ok := t.Value.(*Struct); ok && s.Type == vm.tError {
			return vm.errorf("could not evaluate %s (it might depend on a local variable)", what)
		}
	}
	return err
}

// evalExprIn is jl_interpret_toplevel_expr_in(m, ex, NULL, sparams)
// (interpreter.c): the value of one expression in m, with sparams as the
// static parameters.
func (vm *VM) evalExprIn(m *Module, ex any, sparams []any) (any, error) {
	c, err := prepare(&expr.CodeInfo{Code: []any{expr.ReturnNode{Val: ex}}, SSAValueTypes: int64(1)}, false)
	if err != nil {
		return nil, err
	}
	f := vm.newFrame(c, m)
	f.sparams, f.preevaluation = sparams, sparams != nil // interpreter.c:915
	return f.evalBody(0)
}

// methodDef is eval_methoddef's three-argument form and jl_method_def
// (method.c:1191-1305): the signature svec(svec(argtypes…), svec(tvars…),
// line). julia 1.12 keeps every method in one global table keyed by the
// whole signature, typeof(f) first (method.c:1163-1167); here each generic
// function has its own table without that first parameter, which answers
// the same for calls of a generic function.
func (f *frame) methodDef(s sMethodDef) error {
	vm := f.vm
	if _, err := f.evalValue(s.f); err != nil {
		return err
	}
	at, err := f.evalValue(s.atypes)
	if err != nil {
		return err
	}
	mv, err := f.evalValue(s.meth)
	if err != nil {
		return err
	}
	parts, ok := at.(SVec)
	if !ok || len(parts) < 2 {
		return vm.errorf("method: invalid declaration")
	}
	argtypes, ok1 := parts[0].(SVec)
	tvars, ok2 := parts[1].(SVec)
	if !ok1 || !ok2 || len(argtypes) == 0 {
		return vm.errorf("method: invalid declaration")
	}
	// a generic function's table leaves typeof(f) out of its signatures; a
	// type's constructor table keeps Type{…} in, because Q(x) and Q{Int}(x)
	// select different methods
	params := argtypes[1:]
	var tab *dispatch.Table
	if ft, ok := argtypes[0].(*core.DataType); ok && vm.funcs[ft] != nil {
		tab = vm.funcs[ft].Table
	} else if name := ctorTypeName(argtypes[0]); name != nil && vm.ctors[name] != nil {
		tab, params = vm.ctors[name], argtypes
	} else {
		// any other callee — Union{}, a callable struct — keeps its type
		// first, in the one table julia would put it in with the rest
		tab, params = vm.others, argtypes
	}
	src, ok := mv.(*expr.CodeInfo)
	if lam, isLam := mv.(*expr.Expr); isLam && lam.Head == "lambda" {
		// a closure's method added to a global function: its captured
		// values are in the lambda (method.c:1246-1251)
		src, ok = lambdaCodeInfo(lam)
	}
	if !ok {
		return vm.errorf("method: invalid declaration")
	}
	// jl_apply_tuple_type(atypes, 1): with the checks of apply_type, a
	// Vararg only last. core reports their TypeError as text, raised here as
	// an ErrorException.
	sig, err := core.ApplyType(core.AnyTupleType, params...)
	if err != nil {
		return vm.errorf("%s", err.Error())
	}
	for i := len(tvars) - 1; i >= 0; i-- {
		tv, ok := tvars[i].(*core.TypeVar)
		if !ok {
			return vm.typeError("method signature", "", core.TypeVarType, tvars[i])
		}
		sig = core.TypeUnionAll(tv, sig)
	}
	src, err = vm.definitionEffects(f.mod, src, []any(tvars), true)
	if err != nil {
		return err
	}
	c, err := prepare(src, false)
	if err != nil {
		return err
	}
	isva := false
	if last := argtypes[len(argtypes)-1]; last != nil {
		_, isva = last.(*core.Vararg)
	}
	lam := &lambda{c: c, nargs: len(argtypes), isva: isva, mod: f.mod, sig: sig}
	if len(parts) > 2 {
		lam.file, lam.line = functionLoc(parts[2])
	}
	if _, err = tab.Define(sig, vm.newWorld(), lam); err != nil {
		return err
	}
	// jl_method_table_insert's update_max_args, of the full signature
	// (gf.c:2624-2625)
	if full, err := core.ApplyType(core.AnyTupleType, argtypes...); err == nil {
		vm.updateMaxArgs(full)
	}
	return nil
}

// functionLoc is the file and line of a method's location node, the empty
// symbol for a file that is not a symbol (method.c:1229-1233).
func functionLoc(v any) (expr.Symbol, int32) {
	if q, ok := v.(expr.QuoteNode); ok {
		v = q.Value
	}
	ln, ok := v.(expr.LineNumberNode)
	if !ok {
		return "", 0
	}
	file, _ := ln.File.(expr.Symbol)
	return file, int32(ln.Line)
}

// apply is jl_apply: a builtin or intrinsic runs in Go; a generic function
// dispatches on the argument types in the task's world.
func (vm *VM) apply(fv any, args []any) (any, error) {
	switch fn := fv.(type) {
	case *Builtin:
		return fn.Fn(vm, args)
	case *Intrinsic:
		return fn.Fn(vm, args)
	case *Function:
		return vm.callTable(fn.Table, fn, args, false)
	case *core.DataType, *core.UnionAll:
		if d, ok := core.UnwrapUnionAll(fn).(*core.DataType); ok && vm.ctors[d.Name] != nil {
			return vm.callTable(vm.ctors[d.Name], fn, args, true)
		}
	}
	return vm.callTable(vm.others, fv, args, true)
}

// ctorTypeName is the type a constructor signature's first parameter
// Type{T} (or Type{T{…}}) constructs, nil when it is not one.
func ctorTypeName(ft any) *core.TypeName {
	d, ok := core.UnwrapUnionAll(ft).(*core.DataType)
	if !ok || !core.IsTypeType(d) {
		return nil
	}
	p := d.TParam0()
	// Type{S} where S<:X for a struct X: every S is an X{…}, so the method
	// is one of X's constructors (function (T::Type{<:X})() … end); under
	// an abstract X it is not
	bounded := false
	for {
		v, ok := p.(*core.TypeVar)
		if !ok {
			break
		}
		p, bounded = v.UB, true
	}
	t, ok := core.UnwrapUnionAll(p).(*core.DataType)
	if !ok || (bounded && t.Name.Abstract) {
		return nil
	}
	return t.Name
}

func (vm *VM) methodErrorFor(f any, args []any) error {
	return vm.exception(vm.tMethodError, f, vm.newTuple(args), vm.taskWorld)
}

// callTable dispatches a call of self — a generic function, or a type
// called as a constructor (ctor) — on its method table in the task's world,
// on the dispatch tuple of the arguments, self's Type{…} first for a
// constructor.
func (vm *VM) callTable(tab *dispatch.Table, self any, args []any, ctor bool) (any, error) {
	tt := vm.argTupleType(args)
	if ctor {
		tt = vm.argTupleType(append([]any{self}, args...))
	}
	m, env, err := tab.Lookup(tt, vm.taskWorld)
	if err != nil {
		if errors.Is(err, dispatch.ErrNoMethod) || errors.Is(err, dispatch.ErrAmbiguous) {
			return nil, vm.methodErrorFor(self, args)
		}
		return nil, err
	}
	return vm.callMethod(m, env, self, args)
}

// callMethod runs method m of self on args, with env its static
// parameters.
func (vm *VM) callMethod(m *dispatch.Method, env []any, self any, args []any) (any, error) {
	if vm.depth >= vm.MaxDepth {
		return nil, vm.exception(vm.tStackOverflow)
	}
	vm.depth++
	defer func() { vm.depth-- }()
	switch body := m.Def.(type) {
	case goMethod:
		return body(vm, args)
	case *lambda:
		return vm.callLambda(body, self, args, env)
	}
	return nil, fmt.Errorf("%w: a method without a body", ErrUnsupported)
}

// callLambda runs a method body: slot 1 is the function, the next slots
// its arguments, the last a tuple of the rest when the method is vararg
// (jl_fptr_interpret_call).
func (vm *VM) callLambda(l *lambda, self any, args []any, sparams []any) (any, error) {
	f := vm.newFrame(l.c, l.mod)
	f.sparams, f.spenv = sparams, l.sig
	all := append([]any{self}, args...)
	n := l.nargs
	if l.isva {
		copy(f.locals, all[:n-1])
		f.locals[n-1] = vm.newTuple(all[n-1:])
	} else {
		copy(f.locals, all[:n])
	}
	v, err := f.evalBody(0)
	if t, ok := err.(*Thrown); ok && len(t.Trace) < maxTrace {
		at := ""
		if code := l.c.src; code != nil && f.ip < len(code.Code) {
			if at = showValue(code.Code[f.ip]); len(at) > 160 {
				at = at[:160] + "…"
			}
		}
		t.Trace = append(t.Trace, fmt.Sprintf("%s:%d, statement %d: %s", l.file, l.line, f.ip+1, at))
	}
	return v, err
}

// newStruct is jl_new_structv (datatype.c:1627-1659) for the struct types
// of this VM: at least the fields n_uninitialized does not leave undefined.
func (vm *VM) newStruct(tv any, fields []any) (any, error) {
	t, ok := tv.(*core.DataType)
	if !ok || !t.IsConcrete {
		return nil, vm.typeError("new", "", core.DataTypeType, tv)
	}
	// a NamedTuple instance's names are its own (core's FieldNames)
	if core.IsTupleType(t) || t.FieldNames() == nil && len(t.Name.Fields) == 0 && !t.Name.Singleton {
		return nil, fmt.Errorf("%w: :new of %s", ErrUnsupported, core.Show(tv))
	}
	n := len(t.FieldNames())
	if n-int(t.Name.NUninitialized) > len(fields) || len(fields) > n {
		return nil, vm.errorf("invalid struct allocation")
	}
	// the field types of this instance: Q{Int64}'s in terms of Int64
	ft, _ := t.FieldTypes()
	for i, v := range fields {
		if ft != nil && !vm.isa(v, ft[i]) {
			return nil, vm.typeError("new", "", ft[i], v)
		}
	}
	if v, ok := vm.astNode(t, fields); ok {
		return v, nil
	}
	// a singleton type's instance (datatype.c:1641-1642)
	if !t.Name.Mutable {
		if v := vm.typeInstance(t); v != undef {
			return v, nil
		}
	}
	vals := make([]any, n)
	for i := range vals {
		vals[i] = undef
	}
	copy(vals, fields)
	return &Struct{Type: t, Fields: vals}, nil
}

// newStructT is jl_new_structt (datatype.c:1664-1720), :splatnew: a struct
// of type tv from the elements of a tuple, one per field.
func (vm *VM) newStructT(tv, tup any) (any, error) {
	tt, ok := tup.(*Tuple)
	if !ok {
		return nil, vm.typeError("new", "", core.AnyTupleType, tup)
	}
	t, ok := tv.(*core.DataType)
	if !ok || !t.IsConcrete {
		return nil, vm.typeError("new", "", core.DataTypeType, tv)
	}
	n := len(t.FieldNames())
	if err := vm.nargs("new", tt.Elems, n, n); err != nil {
		return nil, err
	}
	return vm.newStruct(t, tt.Elems)
}

// astNode is :new of an AST or IR node type as package expr's value, the
// one lowering, macro expansion and the evaluator read (boot.jl:519-539
// builds them with :new): QuoteNode(x) must be the QuoteNode lowering
// unquotes, not a struct that happens to have its type.
func (vm *VM) astNode(t *core.DataType, f []any) (any, bool) {
	n := len(f)
	idx := func(i int) int { return int(f[i].(int64)) }
	switch {
	case t == vm.tQuoteNode && n == 1:
		return expr.QuoteNode{Value: f[0]}, true
	case t == vm.tLineNumberNode && n == 2:
		return expr.LineNumberNode{Line: idx(0), File: f[1]}, true
	case t == vm.tSSAValue && n == 1:
		return expr.SSAValue(idx(0)), true
	case t == vm.tSlotNumber && n == 1:
		return expr.SlotNumber(idx(0)), true
	case t == vm.tArgument && n == 1:
		return expr.Argument(idx(0)), true
	case t == vm.tGotoNode && n == 1:
		return expr.GotoNode(idx(0)), true
	case t == vm.tGotoIfNot && n == 2:
		return expr.GotoIfNot{Cond: f[0], Dest: idx(1)}, true
	case t == vm.tReturnNode && n == 0:
		return expr.Unreachable{}, true
	case t == vm.tReturnNode && n == 1:
		return expr.ReturnNode{Val: f[0]}, true
	case t == vm.tNewvarNode && n == 1:
		return expr.NewvarNode{Slot: f[0]}, true
	case t == vm.tEnterNode && n == 1:
		return expr.EnterNode{Catch: idx(0)}, true
	case t == vm.tEnterNode && n == 2:
		return expr.EnterNode{Catch: idx(0), Scope: f[1], HasScope: true}, true
	case t == vm.tExpr && n == 2:
		// Base's exprarray (expr.jl:76): the Expr's arguments are the vector
		// given, which stays its args (e.args === arg)
		head, ok1 := f[0].(expr.Symbol)
		vec, ok2 := f[1].(*Struct)
		if _, _, elems, ok3 := arrayOf(vec); ok1 && ok2 && ok3 && vec.Type == vecAnyType {
			ex := &expr.Expr{Head: head, Args: elems}
			vec.exprOwner = weak.Make(ex)
			vm.cacheArgs(ex, vec)
			return ex, true
		}
	}
	return nil, false
}
