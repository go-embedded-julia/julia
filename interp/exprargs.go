package interp

import (
	"runtime"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// An Expr's args is a Vector{Any} (jltypes.c:3388-3392) that Julia code
// reads, writes and grows; the interpreter, lowering and macro expansion
// read the same arguments as the Go slice expr.Expr.Args. The two share
// storage: the vector's memory is the slice itself, so a write through the
// vector is a write to the slice, and replacing the vector's memory or size
// (as Base's growth does with setfield!) points the slice at the new
// elements. One vector per Expr, so that ex.args === ex.args.

var (
	vecAnyType    = must(core.Apply(core.ArrayType, core.AnyType, int64(1))).(*core.DataType)
	memAnyType    = must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.AnyType, core.CPU)).(*core.DataType)
	memRefAnyType = must(core.Apply(core.GenericMemoryRefType, expr.Symbol("not_atomic"), core.AnyType, core.CPU)).(*core.DataType)
)

// exprArgs is getfield(ex, :args): the vector of ex's arguments, made on
// first use and remade when Go code has replaced ex.Args since.
func (vm *VM) exprArgs(ex *expr.Expr) *Struct {
	vm.argMu.Lock()
	v := vm.argVectors[weak.Make(ex)]
	vm.argMu.Unlock()
	if v != nil && sharesArgs(v, ex.Args) {
		return v
	}
	mem := &Memory{Type: memAnyType, Elems: ex.Args}
	v = &Struct{Type: vecAnyType, exprOwner: weak.Make(ex), Fields: []any{
		&MemoryRef{Type: memRefAnyType, Mem: mem},
		vm.newTuple([]any{int64(len(ex.Args))}),
	}}
	vm.cacheArgs(ex, v)
	return v
}

// cacheArgs records v as ex's vector. The table holds ex weakly, and the
// entry goes when ex does (a cleanup, on another goroutine: hence the lock).
func (vm *VM) cacheArgs(ex *expr.Expr, v *Struct) {
	key := weak.Make(ex)
	vm.argMu.Lock()
	defer vm.argMu.Unlock()
	if _, had := vm.argVectors[key]; !had {
		runtime.AddCleanup(ex, func(k weak.Pointer[expr.Expr]) { vm.dropArgVector(k) }, key)
	}
	vm.argVectors[key] = v
}

func (vm *VM) dropArgVector(k weak.Pointer[expr.Expr]) {
	vm.argMu.Lock()
	defer vm.argMu.Unlock()
	delete(vm.argVectors, k)
}

// sharesArgs reports that vector v still views exactly args.
func sharesArgs(v *Struct, args []any) bool {
	_, dims, elems, ok := arrayOf(v)
	if !ok || len(dims) != 1 || len(elems) != len(args) || int64(len(args)) != dims[0] {
		return false
	}
	return len(args) == 0 || &elems[0] == &args[0]
}

// syncArgs points the owner of vector v at v's elements, after a
// setfield! of its ref or size. Elements never assigned read as nothing
// there: lowering has no undef.
func syncArgs(v *Struct) {
	ex := v.exprOwner.Value()
	if ex == nil {
		return // not an args vector, or its Expr is gone
	}
	_, _, elems, ok := arrayOf(v)
	if !ok {
		ex.Args = nil
		return
	}
	for i, e := range elems {
		if e == undef {
			elems[i] = nil
		}
	}
	ex.Args = elems
}

// setExprField is setfield! of an Expr, a mutable struct: head takes a
// Symbol, args a Vector{Any}, which becomes the Expr's arguments.
func (vm *VM) setExprField(ex *expr.Expr, field, v any) (any, error) {
	t := vm.typeOf(ex).(*core.DataType)
	i, err := vm.fieldIndex("setfield!", t, []expr.Symbol{"head", "args"}, 2, ex, field)
	if err != nil {
		return nil, err
	}
	if i == 0 {
		s, ok := v.(expr.Symbol)
		if !ok {
			return nil, vm.typeError("setfield!", "", core.SymbolType, v)
		}
		ex.Head = s
		return v, nil
	}
	vec, ok := v.(*Struct)
	if !ok || vec.Type != vecAnyType {
		return nil, vm.typeError("setfield!", "", vecAnyType, v)
	}
	vec.exprOwner = weak.Make(ex)
	syncArgs(vec)
	vm.cacheArgs(ex, vec)
	return v, nil
}
