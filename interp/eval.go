package interp

import (
	"errors"
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// frame is interpreter_state (interpreter.c:17-27): one locals array, the
// slots then the SSA values, the static parameters and continue_at.
type frame struct {
	vm         *VM
	c          *code
	mod        *Module
	locals     []any
	sparams    []any
	continueAt int
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
		next := ip + 1
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
			v, err := f.evalBody(next)
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
			var val any = undef
			if s.val != nil {
				v, err := f.evalValue(s.val)
				if err != nil {
					return nil, err
				}
				val = v
			}
			if err := vm.declareGlobal(s.ref, val); err != nil {
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
		if tv, ok := sp.(*core.TypeVar); ok {
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
	b := m.own(name)
	if b == nil || b.World > vm.taskWorld {
		if ub := m.lookup(name, vm.taskWorld); ub != nil {
			return vm.errorf("cannot assign a value to imported variable %s.%s", m.Name, name)
		}
		return vm.errorf("Global %s.%s does not exist and cannot be assigned.\n"+
			"Note: Julia 1.9 and 1.10 inadvertently omitted this error check (#56933).\n"+
			"Hint: Declare it using `global %s` inside `%s` before attempting assignment.",
			m.Name, name, name, m.Name)
	}
	if b.Alias != nil {
		return vm.errorf("cannot assign a value to imported variable %s.%s", m.Name, name)
	}
	if b.Const {
		return vm.errorf("invalid assignment to constant %s.%s. This redefinition may be permitted using the `const` keyword.", m.Name, name)
	}
	b.Value = v
	return nil
}

// declareGlobal is jl_declare_global: a binding declared in a new world when
// the module does not have one.
func (vm *VM) declareGlobal(ref expr.GlobalRef, val any) error {
	m := vm.modules[ref.Mod]
	if m == nil {
		return fmt.Errorf("%w: a global of the unknown module %s", ErrUnsupported, ref.Mod)
	}
	if m.own(ref.Name) == nil {
		m.declare(ref.Name, vm.newWorld())
	}
	if val != undef {
		return vm.assignGlobal(m, ref.Name, val)
	}
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
	b.Const, b.Value = true, val
	return nil
}

// declareFunction is jl_declare_const_gf (method.c:1135-1160): the generic
// function name in m, created as a constant in a new world when absent.
func (vm *VM) declareFunction(m *Module, name expr.Symbol) (any, error) {
	if b := m.own(name); b != nil {
		if fn, ok := b.resolve().Value.(*Function); ok {
			return fn, nil
		}
		if b.Value != undef {
			return nil, vm.errorf("cannot define function %s; it already has a value", name)
		}
	}
	fn := vm.newFunction(m, name)
	vm.funcs[fn.Type] = fn
	b := m.declare(name, vm.newWorld())
	b.Const, b.Value = true, fn
	return fn, nil
}

// lambda is a method body defined by lowered code.
type lambda struct {
	c     *code
	nargs int
	isva  bool
	mod   *Module
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
	ft, _ := argtypes[0].(*core.DataType)
	fn := vm.funcs[ft]
	if fn == nil {
		return fmt.Errorf("%w: a method of %s, which is not a generic function of this VM", ErrUnsupported, core.Show(argtypes[0]))
	}
	src, ok := mv.(*expr.CodeInfo)
	if !ok {
		return vm.errorf("method: invalid declaration")
	}
	sig, err := core.Apply(core.AnyTupleType, argtypes[1:]...)
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
	c, err := prepare(src, false)
	if err != nil {
		return err
	}
	isva := false
	if last := argtypes[len(argtypes)-1]; last != nil {
		_, isva = last.(*core.Vararg)
	}
	_, err = fn.Table.Define(sig, vm.newWorld(), &lambda{c: c, nargs: len(argtypes), isva: isva, mod: f.mod})
	return err
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
		return vm.callGeneric(fn, args)
	}
	return nil, vm.methodErrorFor(fv, args)
}

func (vm *VM) methodErrorFor(f any, args []any) error {
	t, err := vm.newTuple(args)
	if err != nil {
		return err
	}
	return vm.exception(vm.tMethodError, f, t, vm.taskWorld)
}

func (vm *VM) callGeneric(fn *Function, args []any) (any, error) {
	types := make([]any, len(args))
	for i, a := range args {
		types[i] = vm.typeOf(a)
	}
	tt, err := core.Apply(core.AnyTupleType, types...)
	if err != nil {
		return nil, err
	}
	m, env, err := fn.Table.Lookup(tt, vm.taskWorld)
	if err != nil {
		if errors.Is(err, dispatch.ErrNoMethod) || errors.Is(err, dispatch.ErrAmbiguous) {
			return nil, vm.methodErrorFor(fn, args)
		}
		return nil, err
	}
	if vm.depth >= vm.MaxDepth {
		return nil, vm.exception(vm.tStackOverflow)
	}
	vm.depth++
	defer func() { vm.depth-- }()
	switch body := m.Def.(type) {
	case goMethod:
		return body(vm, args)
	case *lambda:
		return vm.callLambda(body, fn, args, env)
	}
	return nil, fmt.Errorf("%w: a method without a body", ErrUnsupported)
}

// callLambda runs a method body: slot 1 is the function, the next slots
// its arguments, the last a tuple of the rest when the method is vararg
// (jl_fptr_interpret_call).
func (vm *VM) callLambda(l *lambda, fn *Function, args []any, sparams []any) (any, error) {
	f := vm.newFrame(l.c, l.mod)
	f.sparams = sparams
	all := append([]any{fn}, args...)
	n := l.nargs
	if l.isva {
		rest, err := vm.newTuple(all[n-1:])
		if err != nil {
			return nil, err
		}
		copy(f.locals, all[:n-1])
		f.locals[n-1] = rest
	} else {
		copy(f.locals, all[:n])
	}
	return f.evalBody(0)
}

// newStruct is jl_new_structv (datatype.c:1627-1659) for the struct types
// of this VM. The count of fields a constructor must initialise
// (n_uninitialized) is not recorded yet, so any prefix of the fields is
// accepted.
func (vm *VM) newStruct(tv any, fields []any) (any, error) {
	t, ok := tv.(*core.DataType)
	if !ok || !t.IsConcrete {
		return nil, vm.typeError("new", "", core.DataTypeType, tv)
	}
	if core.IsTupleType(t) || t.Name.FieldNames == nil && len(t.Name.Fields) == 0 && !t.Name.Singleton {
		return nil, fmt.Errorf("%w: :new of %s", ErrUnsupported, core.Show(tv))
	}
	n := len(t.Name.FieldNames)
	if len(fields) > n {
		return nil, vm.errorf("invalid struct allocation")
	}
	for i, v := range fields {
		if ft := t.Name.Fields; ft != nil && !vm.isa(v, ft[i]) {
			return nil, vm.typeError("new", "", ft[i], v)
		}
	}
	vals := make([]any, n)
	for i := range vals {
		vals[i] = undef
	}
	copy(vals, fields)
	return &Struct{Type: t, Fields: vals}, nil
}
