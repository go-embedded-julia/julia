package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// @cfunction without a compiler (issue #35). julia compiles the callable into
// a C function and the macro's value is its address. Here the :cfunction form
// evaluates to a Ptr{Cvoid} whose target holds the callable and its
// signature, which the loop's shims (uvloop.go) call with apply: C code
// proper never sees it, because there is none.

// cfunction is what a Ptr{Cvoid} made by :cfunction points at: the julia
// callable, the declared return type and the declared argument types.
type cfunction struct {
	fn   any
	rt   any
	argt SVec
}

// oCfunction is a prepared :cfunction (type, func, rt, argtypes, cc).
type oCfunction struct {
	typ, fn, rt, at op
}

// prepCfunction prepares Expr(:cfunction, typ, fptr, rt, at, QuoteNode(:ccall)),
// which @cfunction builds (c.jl:64-79).
func (c *code) prepCfunction(x *expr.Expr) op {
	if len(x.Args) != 5 {
		return oRaise{"Invalid IR: malformed :cfunction"}
	}
	if cc, ok := x.Args[4].(expr.QuoteNode); !ok || cc.Value != any(expr.Symbol("ccall")) {
		return oUnsupported{"a :cfunction with a calling convention other than :ccall"}
	}
	return oCfunction{c.prepValue(x.Args[0]), c.prepValue(x.Args[1]), c.prepValue(x.Args[2]), c.prepValue(x.Args[3])}
}

// evalCfunction is the Ptr{Cvoid} form of jl_cfunction (codegen.cpp:6423-6427,
// emit_cfunction). The function is the quoted expression of the macro, which
// julia evaluates in the method's module when the method is defined
// (method.c:138-143): here when the form runs.
func (f *frame) evalCfunction(x oCfunction) (any, error) {
	vm := f.vm
	typ, err := f.evalValue(x.typ)
	if err != nil {
		return nil, err
	}
	if typ != any(vm.ptrType(core.NothingType)) {
		return nil, fmt.Errorf("%w: a cfunction of type %s", ErrUnsupported, showValue(typ))
	}
	fn, err := f.evalValue(x.fn)
	if err != nil {
		return nil, err
	}
	switch fn.(type) {
	case expr.Symbol, *expr.Expr:
		if fn, err = vm.sessionFor(f.mod).evalForm(fn); err != nil {
			return nil, err
		}
	}
	rt, err := f.evalValue(x.rt)
	if err != nil {
		return nil, err
	}
	at, err := f.evalValue(x.at)
	if err != nil {
		return nil, err
	}
	argt, ok := at.(SVec)
	if !ok {
		return nil, vm.errorf("cfunction: argument types must be a tuple of types")
	}
	return vm.voidPtr(&cfunction{fn: fn, rt: rt, argt: argt}), nil
}

// callCFunction calls the function a cfunction pointer holds, as C calling
// the address would: the arguments are those of the declared signature.
func (vm *VM) callCFunction(p any, args ...any) (any, error) {
	if ptr, ok := p.(*Pointer); ok {
		if cf, ok := ptr.Target.(*cfunction); ok && ptr.Off == 0 {
			if len(args) != len(cf.argt) {
				return nil, vm.errorf("cfunction: %d arguments for a signature of %d", len(args), len(cf.argt))
			}
			for i, a := range args {
				if !vm.isa(a, cf.argt[i]) {
					return nil, vm.typeError("cfunction", "", cf.argt[i], a)
				}
			}
			return vm.apply(cf.fn, args)
		}
	}
	return nil, fmt.Errorf("%w: a callback that is not a cfunction pointer: %s", ErrUnsupported, showValue(p))
}
