package interp

import (
	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// Staged functions: a method with a generator. Lowering turns a function
// whose body holds `if @generated` into a generator function and a
// `(meta generated stub)` statement, stub a Core.GeneratedFunctionStub
// (desugar.go makeGeneratorFunction, julia-syntax.scm:398), and the
// @generated macro (expr.jl:1178-1194) gives the plain form a fallback body
// of `return nothing` marked `(meta generated_only)`.
//
// julia sets Method.generator from the first at jl_method_set_source
// (method.c:936-944) and drops the method's source for the second
// (method.c:945-948, :969-971). A call of a method without a source runs
// jl_code_for_staged (method.c:695-810, interpreter.c:735-744): the generator
// is called with (world, method, static parameters…, argument types…), the
// stub of base/expr.jl:1692-1708 lowers the expression the generator returns
// into a CodeInfo, and that is the body, kept per method instance so that the
// generator runs once per signature.
//
// This VM keeps the same: methodGenerator reads the two statements, callStaged
// makes the body of a signature once (stagedBody) and runs it as a method.
// A method with a generator and a source (`if @generated … else … end`) runs
// its source, as julia's interpreter does (interpreter.c:721-734); julia
// requires the two branches to agree, so only the speed differs. Staging it
// too was measured: Base's own such methods (ntuple, multidimensional...)
// made the image build run past 15 minutes instead of 6.

// stagedKey names one method instance of a staged method: the method's body
// and the call's argument tuple, typeof(f) first.
type stagedKey struct {
	l  *lambda
	tt *core.DataType
}

// methodGenerator reads the `(meta generated stub)` and `(meta
// generated_only)` statements of a method's source (jl_method_set_source,
// method.c:936-948): the stub, evaluated in the method's module, and whether
// the method has no source of its own.
func (vm *VM) methodGenerator(m *Module, src *expr.CodeInfo) (gen any, only bool, err error) {
	for _, st := range src.Code {
		e, ok := st.(*expr.Expr)
		if !ok || e.Head != "meta" || len(e.Args) == 0 {
			continue
		}
		switch {
		case len(e.Args) == 2 && e.Args[0] == any(expr.Symbol("generated")):
			if gen != nil {
				return nil, false, vm.errorf("duplicate @generated function body")
			}
			if gen, err = vm.evalExprIn(m, e.Args[1], nil); err != nil {
				return nil, false, err
			}
		case len(e.Args) == 1 && e.Args[0] == any(expr.Symbol("generated_only")):
			only = true
		}
	}
	return gen, only && gen != nil, nil
}

// callStaged runs method m of a function without a source, a plain @generated
// one, on args: the body its generator makes for this signature.
func (vm *VM) callStaged(m *dispatch.Method, l *lambda, self any, args []any, env []any) (any, error) {
	tt := vm.argTupleType(append([]any{self}, args...))
	key := stagedKey{l, tt}
	vm.stagedMu.Lock()
	body := vm.staged[key]
	vm.stagedMu.Unlock()
	if body == nil {
		var err error
		if body, err = vm.stagedBody(m, l, self, tt, env); err != nil {
			return nil, err
		}
		body = vm.stagedStore(key, body)
	}
	return vm.callLambda(body, self, args, env)
}

// stagedStore keeps body for key unless another task kept one first, and gives
// the one kept: every task runs the same body of a method instance.
func (vm *VM) stagedStore(key stagedKey, body *lambda) *lambda {
	vm.stagedMu.Lock()
	defer vm.stagedMu.Unlock()
	if prev := vm.staged[key]; prev != nil {
		return prev
	}
	if len(vm.staged) >= stagedMax {
		return body // the table is full: this call's body is not kept
	}
	if vm.staged == nil {
		vm.staged = map[stagedKey]*lambda{}
	}
	vm.staged[key] = body
	return body
}

// stagedMax bounds the bodies kept, so that a program calling a staged method
// on ever new argument tuples (Type{a} for a million a) cannot grow the table
// without end; a call past it runs its generator again. A variable so a test
// can reach the bound.
var stagedMax = 1 << 16

// stagedBody is jl_code_for_staged (method.c:695-810) of one signature: the
// generator called as jl_call_staged does (method.c:649-665), its CodeInfo
// the body of a copy of l.
func (vm *VM) stagedBody(m *dispatch.Method, l *lambda, self any, tt *core.DataType, env []any) (*lambda, error) {
	tab, ft, name, ok := vm.methodTableOf(tt.Parameters[0])
	if !ok {
		return nil, vm.errorf("The generator method cannot run until it is added to a method table.")
	}
	gargs := []any{uint64(vm.taskWorld), vm.methodObject(tab, m, ft, name)}
	gargs = append(gargs, env...)
	fixed := l.nargs
	if l.isva {
		fixed--
	}
	gargs = append(gargs, tt.Parameters[:fixed]...)
	if l.isva {
		gargs = append(gargs, vm.newTuple(append([]any(nil), tt.Parameters[fixed:]...)))
	}
	ci, err := vm.apply(l.gen, gargs)
	if err != nil {
		return nil, err
	}
	js, ok := ci.(*Struct)
	if !ok || js.Type != vm.tCodeInfo {
		return nil, vm.errorf("As of Julia 1.12, generated functions must return `CodeInfo`. See `Base.generated_body_to_codeinfo`.")
	}
	return vm.stagedLambda(l, js, env)
}

// stagedLambda is a copy of l whose body is the CodeInfo js, resolved and
// prepared as methodDef does a method's source (eval.go:781).
func (vm *VM) stagedLambda(l *lambda, js *Struct, env []any) (*lambda, error) {
	src, err := vm.exprCodeInfo(js)
	if err != nil {
		return nil, err
	}
	if src, err = vm.definitionEffects(l.mod, src, env, true); err != nil {
		return nil, err
	}
	c, err := prepare(src, false)
	if err != nil {
		return nil, err
	}
	body := *l
	body.c, body.gen, body.genOnly = c, nil, false
	return &body, nil
}

// exprCodeInfo is a CodeInfo julia code holds as the form the evaluator runs,
// juliaCodeInfo's inverse.
func (vm *VM) exprCodeInfo(s *Struct) (*expr.CodeInfo, error) {
	f := append([]any(nil), s.Fields...)
	for _, i := range []int{0, 3, 4, 5} {
		_, _, elems, ok := arrayOf(f[i])
		if !ok {
			return nil, vm.errorf("malformed CodeInfo: field %d is not a vector", i+1)
		}
		f[i] = expr.Vector(elems)
	}
	f[1] = vm.exprDebugInfo(f[1])
	if e, ok := f[9].(SVec); ok {
		f[9] = expr.Vector(e)
	}
	ci, ok := expr.CodeInfoFrom(f)
	if !ok {
		return nil, vm.errorf("malformed CodeInfo")
	}
	return ci, nil
}

// exprDebugInfo is juliaDebugInfo's inverse.
func (vm *VM) exprDebugInfo(v any) any {
	s, ok := v.(*Struct)
	if !ok || len(s.Fields) != 4 {
		return v
	}
	edges, _ := s.Fields[2].(SVec)
	out := &expr.DebugInfo{Def: s.Fields[0], Linetable: vm.exprDebugInfo(s.Fields[1]), Edges: make(expr.Vector, len(edges))}
	for i, e := range edges {
		out.Edges[i] = vm.exprDebugInfo(e)
	}
	out.Codelocs, _ = s.Fields[3].(string)
	return out
}

// lambdaArgnames gives the argument list of a (lambda argnames body) as lowering
// reads a list, an expr.Vector, where julia code holds a Vector{Any}:
// julia_to_scm_ turns that first argument of a lambda into a list
// (ast.c:794-800). The lambda may be the first argument of the
// with-static-parameters of a generator stub (expr.jl:1705). It changes x in
// place.
func (vm *VM) lambdaArgnames(x any) {
	e, ok := x.(*expr.Expr)
	if !ok || len(e.Args) == 0 {
		return
	}
	switch e.Head {
	case "with-static-parameters":
		vm.lambdaArgnames(e.Args[0])
	case "lambda":
		if _, _, elems, ok := arrayOf(e.Args[0]); ok {
			e.Args[0] = expr.Vector(elems)
		}
	}
}

// jlExpand is jl_expand(x, m) (ast.c:1279-1303): x lowered as a top-level
// form of m, a CodeInfo as Base reads it (generated_body_to_codeinfo,
// expr.jl:1667) when the form is a lambda.
func (vm *VM) jlExpand(a []any) (any, error) {
	m, ok := a[1].(*Module)
	if !ok {
		return nil, vm.typeError("ccall", "", core.ModuleType, a[1])
	}
	s := vm.sessionFor(m)
	vm.lambdaArgnames(a[0])
	form, err := s.macroexpand(a[0])
	if err != nil {
		return nil, err
	}
	s.low.SetTop(vm.baseRelativeTo(m).Name)
	lowered, err := s.low.Lower(syntaxModules(form))
	if err != nil {
		return nil, err
	}
	lowered = vm.bindRefs(m, lowered)
	if ci, ok := lowered.(*expr.CodeInfo); ok {
		return vm.juliaCodeInfo(ci), nil
	}
	return vm.runtimeModules(lowered), nil
}
