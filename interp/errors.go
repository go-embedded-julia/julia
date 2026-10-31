package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Thrown is a Julia exception in flight: the value of `throw`. Evaluation
// returns it as a Go error, never as a panic, so that try/catch is the
// handler stack of eval.go rather than an unwinding of Go frames.
type Thrown struct {
	Value any
}

func (t *Thrown) Error() string { return "julia exception: " + showValue(t.Value) }

func (vm *VM) exception(t *core.DataType, fields ...any) error {
	return &Thrown{Value: &Struct{Type: t, Fields: fields}}
}

// errorf is jl_errorf: an ErrorException.
func (vm *VM) errorf(format string, args ...any) error {
	return vm.exception(vm.tError, fmt.Sprintf(format, args...))
}

// typeError is jl_type_error_rt (rtutils.c:113-122).
func (vm *VM) typeError(fname expr.Symbol, context string, expected, got any) error {
	return vm.exception(vm.tTypeError, fname, context, expected, got)
}

// undefVar is jl_undefined_var_error (rtutils.c:142): the variable, the
// task's world and the scope (a module, or :local / :static_parameter).
func (vm *VM) undefVar(name expr.Symbol, world uint64, scope any) error {
	return vm.exception(vm.tUndefVar, name, world, scope)
}

// tooFew and tooMany are jl_too_few_args and jl_too_many_args
// (rtutils.c:102-110).
func (vm *VM) tooFew(fname string, min int) error {
	return vm.exception(vm.tArgError, fmt.Sprintf("%s: too few arguments (expected %d)", fname, min))
}

func (vm *VM) tooMany(fname string, max int) error {
	return vm.exception(vm.tArgError, fmt.Sprintf("%s: too many arguments (expected %d)", fname, max))
}

// nargs is JL_NARGS(fname, min, max).
func (vm *VM) nargs(fname string, args []any, min, max int) error {
	if len(args) < min {
		return vm.tooFew(fname, min)
	}
	if max >= 0 && len(args) > max {
		return vm.tooMany(fname, max)
	}
	return nil
}
