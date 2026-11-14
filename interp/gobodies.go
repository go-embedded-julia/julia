package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/expr"
)

// Go funcs a system image refers to by name (design of #40, section 2).
//
// A saved image holds no Go func. What a loaded VM calls instead is found in
// that VM's own boot, by the name the image holds:
//
//   - an Intrinsic by its number, vm.intrinsics[ID] (intrinsicFn);
//   - a Builtin by its name, the binding of Core it is (builtinFn);
//   - a goMethod that boot made (the constructors of bootTypeCtors, Typeof)
//     comes with New(), as the boot object it is;
//   - a goMethod made while Base is built is a goBody: a name of goBodies and
//     the value it captured, both saved, the func made again by newGoBody.

// goBody is the body of a method made in Go while Base is built. Name is a
// key of goBodies; Capture is what the body closes over, a value the image
// saves like any other.
type goBody struct {
	Name    string
	Capture any
	fn      goMethod
}

// goBodies makes each named body from its capture.
var goBodies = map[string]func(capture any) (goMethod, error){
	// Base.jl:410-415's loop, called as a function (compilerdeps.go); it
	// closes over Base
	"record_compiler_dependencies": func(capture any) (goMethod, error) {
		base, ok := capture.(*Module)
		if !ok {
			return nil, fmt.Errorf("%w: record_compiler_dependencies captures a Module, not %T", ErrUnsupported, capture)
		}
		return func(vm *VM, args []any) (any, error) { return nil, vm.recordCompilerDependencies(base) }, nil
	},
	// Base.JuliaSyntax.enable_in_core!, which leaves Core's parser as it is
	// (juliasyntax.go); it closes over nothing
	"enable_in_core!": func(capture any) (goMethod, error) {
		if capture != nil {
			return nil, fmt.Errorf("%w: enable_in_core! captures nothing, not %T", ErrUnsupported, capture)
		}
		return func(vm *VM, args []any) (any, error) { return nil, nil }, nil
	},
}

// newGoBody is the body goBodies names, closing over capture. A name no body
// has, or a capture of the wrong kind, is an error: an image is input.
func newGoBody(name string, capture any) (*goBody, error) {
	mk, ok := goBodies[name]
	if !ok {
		return nil, fmt.Errorf("%w: no Go body named %q", ErrUnsupported, name)
	}
	fn, err := mk(capture)
	if err != nil {
		return nil, err
	}
	return &goBody{Name: name, Capture: capture, fn: fn}, nil
}

// mustGoBody is newGoBody for a name and a capture the VM itself chose.
func mustGoBody(name string, capture any) *goBody { return must(newGoBody(name, capture)) }

// intrinsicFn is the func of the intrinsic numbered id in this VM's boot.
func (vm *VM) intrinsicFn(id int32) (func(*VM, []any) (any, error), error) {
	if id < 0 || int(id) >= len(vm.intrinsics) || vm.intrinsics[id] == nil {
		return nil, fmt.Errorf("%w: no intrinsic numbered %d", ErrUnsupported, id)
	}
	return vm.intrinsics[id].Fn, nil
}

// builtinFn is the func of the Builtin named name in this VM's boot: the one
// Core binds to that name.
func (vm *VM) builtinFn(name expr.Symbol) (func(*VM, []any) (any, error), error) {
	if b := vm.Core.own(name); b != nil {
		if x, ok := b.Value.(*Builtin); ok {
			return x.Fn, nil
		}
	}
	return nil, fmt.Errorf("%w: no builtin named %s", ErrUnsupported, name)
}
