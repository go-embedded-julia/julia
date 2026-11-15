package interp

import (
	"errors"
	"fmt"
	"unicode"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Get and Set of a Kernel: the Julia-Go value table (#60). A global of Main is
// read as a Go value, and a Go value is assigned to one, as a cell `name = v`
// would. Every conversion copies: a Go slice never keeps a julia Memory alive
// or sees its later mutations, and julia never sees a Go slice change.
//
//	julia                                 Go
//	Int64                                 int64
//	Float64                               float64   (every bit kept: NaN payloads, -0.0)
//	Bool                                  bool
//	String                                string
//	Vector{Float64}, Vector{Int64}        []float64, []int64
//	Matrix{Float64}, Matrix{Int64}        Matrix[float64], Matrix[int64]
//
// Set also takes an int as an Int64. Anything else is refused with a
// ConversionError (ErrKernelType) that names the type, never converted
// silently or answered as nil.

// Errors of Get and Set.
var (
	// ErrKernelType is a value of a type the table does not convert, in either
	// direction; the error is a *ConversionError that names it.
	ErrKernelType = errors.New("interp: a value of a type the kernel does not convert")
	// ErrKernelName is a name that is no identifier.
	ErrKernelName = errors.New("interp: not a name a global can have")
)

// ConversionError is a value the table does not convert: the julia type of a
// value Get found, or the Go type of a value Set was given.
type ConversionError struct {
	Julia string // the julia type, for a Get
	Go    string // the Go type, for a Set
}

func (e *ConversionError) Error() string {
	if e.Julia != "" {
		return fmt.Sprintf("interp: a value of the julia type %s has no Go value in the kernel's table", e.Julia)
	}
	return fmt.Sprintf("interp: a Go value of type %s has no julia value in the kernel's table", e.Go)
}

func (e *ConversionError) Is(target error) bool { return target == ErrKernelType }

// Matrix is a julia Matrix{Float64} or Matrix{Int64} as a Go value: Rows by
// Cols, in column-major order as julia keeps it, element (i, j) (0-based) at
// Data[i+j*Rows].
type Matrix[T float64 | int64] struct {
	Rows, Cols int
	Data       []T
}

// At is the element at row i, column j, both 0-based.
func (m Matrix[T]) At(i, j int) T { return m.Data[i+j*m.Rows] }

// Get is the value of the global name of Main as a Go value. ok is false for a
// name that is not bound (err nil); a value of a type the table does not
// convert is err, a *ConversionError (ErrKernelType). Like Eval, it is refused
// with ErrKernelBusy while a cell runs.
func (k *Kernel) Get(name string) (v any, ok bool, err error) {
	if err := k.enter(); err != nil {
		return nil, false, err
	}
	defer k.busy.Store(false)
	vm := k.vm
	vm.taskWorld = vm.World()
	b := vm.Main.lookup(expr.Symbol(name), vm.taskWorld)
	if b == nil || b.Value == undef {
		return nil, false, nil
	}
	v, err = vm.toGo(b.Value)
	if err != nil {
		return nil, false, err
	}
	return v, true, nil
}

// Set assigns the Go value v to the global name of Main, as the cell
// `name = v` would, replacing a binding that exists. A value of a type the
// table does not convert is a *ConversionError (ErrKernelType), a name that is
// no identifier ErrKernelName; an array over VM.MaxMemoryElems is
// ErrMemoryLimit. Like Eval, it is refused with ErrKernelBusy while a cell runs.
func (k *Kernel) Set(name string, v any) error {
	if !validName(name) {
		return fmt.Errorf("%w: %q", ErrKernelName, name)
	}
	if err := k.enter(); err != nil {
		return err
	}
	defer k.busy.Store(false)
	vm := k.vm
	jv, err := vm.fromGo(v)
	if err != nil {
		return err
	}
	if vm.Main.own(expr.Symbol(name)) == nil {
		vm.Main.declare(expr.Symbol(name), vm.newWorld())
	}
	vm.taskWorld = vm.World()
	return vm.assignGlobal(vm.Main, expr.Symbol(name), jv)
}

// enter takes the kernel for one call, as Eval does.
func (k *Kernel) enter() error {
	if !k.busy.CompareAndSwap(false, true) {
		return ErrKernelBusy
	}
	if k.closed.Load() {
		k.busy.Store(false)
		return ErrKernelClosed
	}
	return nil
}

// validName is an identifier: a letter or an underscore, then letters, digits,
// underscores and !.
func validName(s string) bool {
	for i, r := range s {
		if !(unicode.IsLetter(r) || r == '_' || i > 0 && (unicode.IsDigit(r) || r == '!')) {
			return false
		}
	}
	return s != ""
}

// toGo is a julia value as a Go value of the table.
func (vm *VM) toGo(v any) (any, error) {
	switch x := v.(type) {
	case int64, float64, bool, string:
		return x, nil
	case *Struct:
		if x.Type.Name.Module == "Core" && x.Type.Name.Name == "Array" {
			if g, ok := vm.arrayToGo(x); ok {
				return g, nil
			}
		}
	}
	return nil, &ConversionError{Julia: showValue(vm.typeOf(v))}
}

// arrayToGo is an Array{Float64 or Int64, 1 or 2} as its copy.
func (vm *VM) arrayToGo(a *Struct) (any, bool) {
	ref, ok1 := a.Fields[0].(*MemoryRef)
	size, ok2 := a.Fields[1].(*Tuple)
	nd, ok3 := a.Type.Parameters[1].(int64)
	if !ok1 || !ok2 || !ok3 || int64(len(size.Elems)) != nd || nd < 1 || nd > 2 {
		return nil, false
	}
	n := 1
	dims := make([]int, nd)
	for i, d := range size.Elems {
		di, ok := d.(int64)
		if !ok || di < 0 || di > 0 && int64(n) > int64(len(ref.Mem.Elems))/di {
			return nil, false // not a size, or more elements than the memory holds
		}
		dims[i] = int(di)
		n *= int(di)
	}
	if ref.Off+n > len(ref.Mem.Elems) {
		return nil, false
	}
	elems := ref.Mem.Elems[ref.Off : ref.Off+n]
	switch a.Type.Parameters[0] {
	case any(core.Float64Type):
		data, ok := copyElems[float64](elems)
		if !ok {
			return nil, false
		}
		if nd == 1 {
			return data, true
		}
		return Matrix[float64]{Rows: dims[0], Cols: dims[1], Data: data}, true
	case any(core.Int64Type):
		data, ok := copyElems[int64](elems)
		if !ok {
			return nil, false
		}
		if nd == 1 {
			return data, true
		}
		return Matrix[int64]{Rows: dims[0], Cols: dims[1], Data: data}, true
	}
	return nil, false
}

// copyElems is the elements of a julia memory as a Go slice; false when one is
// not a T (an undefined slot).
func copyElems[T any](elems []any) ([]T, bool) {
	out := make([]T, len(elems))
	for i, e := range elems {
		x, ok := e.(T)
		if !ok {
			return nil, false
		}
		out[i] = x
	}
	return out, true
}

// fromGo is a Go value of the table as a julia value.
func (vm *VM) fromGo(v any) (any, error) {
	switch x := v.(type) {
	case int:
		return int64(x), nil
	case int64, float64, bool, string:
		return x, nil
	case []float64:
		return vm.newVectorOf(core.Float64Type, toAny(x))
	case []int64:
		return vm.newVectorOf(core.Int64Type, toAny(x))
	case Matrix[float64]:
		return vm.newMatrixOf(core.Float64Type, toAny(x.Data), x.Rows, x.Cols)
	case Matrix[int64]:
		return vm.newMatrixOf(core.Int64Type, toAny(x.Data), x.Rows, x.Cols)
	}
	return nil, &ConversionError{Go: fmt.Sprintf("%T", v)}
}

func toAny[T any](xs []T) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// newVectorOf is a Vector{elem} over the elements, which are copies (toAny
// made them): the memory is the vector's own.
func (vm *VM) newVectorOf(elem *core.DataType, elems []any) (any, error) {
	if err := vm.checkArraySize(len(elems)); err != nil {
		return nil, err
	}
	return vm.newVector(elem, elems), nil
}

// checkArraySize is the bound of VM.MaxMemoryElems on one array.
func (vm *VM) checkArraySize(n int) error {
	if n > vm.MaxMemoryElems {
		return fmt.Errorf("%w: an array of %d elements (VM.MaxMemoryElems is %d)", ErrMemoryLimit, n, vm.MaxMemoryElems)
	}
	return nil
}

// newMatrixOf is a Matrix{elem} of rows by cols over the elements, column-major.
func (vm *VM) newMatrixOf(elem *core.DataType, elems []any, rows, cols int) (any, error) {
	n := len(elems)
	ok := rows >= 0 && cols >= 0
	switch {
	case !ok:
	case rows == 0 || cols == 0:
		ok = n == 0
	default:
		ok = n%rows == 0 && n/rows == cols // without rows*cols, which can overflow
	}
	if !ok {
		return nil, fmt.Errorf("%w: a matrix of %d by %d does not hold %d elements", ErrKernelType, rows, cols, n)
	}
	if err := vm.checkArraySize(n); err != nil {
		return nil, err
	}
	mt := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), elem, core.CPU)).(*core.DataType)
	rt := must(core.Apply(core.GenericMemoryRefType, expr.Symbol("not_atomic"), elem, core.CPU)).(*core.DataType)
	at := must(core.Apply(core.ArrayType, elem, int64(2))).(*core.DataType)
	return &Struct{Type: at, Fields: []any{
		&MemoryRef{Type: rt, Mem: &Memory{Type: mt, Elems: elems}},
		vm.newTuple([]any{int64(rows), int64(cols)}),
	}}, nil
}
