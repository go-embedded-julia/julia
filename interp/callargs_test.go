package interp

import (
	"reflect"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A generic-function call evaluates its arguments into a buffer of the calling
// frame, which the callee BORROWS (#71). Nothing may keep the slice: a method
// made in Go is given a copy, and a test that keeps what it was given finds the
// values of its own call after a later one reused the buffer. A call inside an
// argument, and a call of more arguments than the buffer holds, take slices of
// their own.
func TestBorrowedCallArguments(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	var kept [][]any
	fn := vm.newFunction(s.Mod, "argk_keep")
	any2, _ := core.Apply(core.AnyTupleType, core.AnyType, core.AnyType)
	m, err := fn.Table.Define(any2, vm.newWorld(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Def = goMethod(func(vm *VM, args []any) (any, error) {
		kept = append(kept, args) // the slice itself, as a careless method would
		return args[0], nil
	})
	anyN, _ := core.Apply(core.AnyTupleType, core.AnyType, core.AnyType, core.AnyType, core.AnyType, core.AnyType, core.AnyType)
	m6, _ := fn.Table.Define(anyN, vm.newWorld(), nil)
	m6.Def = m.Def
	vm.taskWorld = vm.World() // the methods are visible
	c := func(v any) op { return oConst{v} }
	call := func(args ...op) op { return oCall{append([]op{c(fn)}, args...)} }
	f := vm.newFrame(&code{}, s.Mod)

	// two calls in a row from one frame: the first one's arguments survive
	for _, args := range [][]int64{{1, 2}, {3, 4}} {
		if _, err := f.evalValue(call(c(args[0]), c(args[1]))); err != nil {
			t.Fatal(err)
		}
	}
	if len(kept) != 2 || !reflect.DeepEqual(kept[0], []any{int64(1), int64(2)}) || !reflect.DeepEqual(kept[1], []any{int64(3), int64(4)}) {
		t.Errorf("arguments kept by a Go method: %v", kept)
	}
	if f.argBusy {
		t.Error("the buffer is still busy after the calls")
	}
	for i, v := range f.argbuf {
		if v != nil {
			t.Errorf("argbuf[%d] = %v after the call: the buffer keeps what it lent", i, v)
		}
	}
	// a call inside an argument: the inner one does not overwrite the outer's
	kept = nil
	v, err := f.evalValue(call(call(c(int64(10)), c(int64(11))), c(int64(12))))
	if err != nil || v != int64(10) {
		t.Fatalf("nested: %v %v", v, err)
	}
	if len(kept) != 2 || !reflect.DeepEqual(kept[0], []any{int64(10), int64(11)}) || !reflect.DeepEqual(kept[1], []any{int64(10), int64(12)}) {
		t.Errorf("nested calls: %v", kept)
	}
	// more arguments than the buffer holds
	kept = nil
	six := []op{c(int64(1)), c(int64(2)), c(int64(3)), c(int64(4)), c(int64(5)), c(int64(6))}
	if v, err := f.evalValue(call(six...)); err != nil || v != int64(1) || len(kept) != 1 || len(kept[0]) != 6 {
		t.Errorf("six arguments: %v %v %v", v, err, kept)
	}
	// an argument that fails leaves the buffer free and clear
	if _, err := f.evalValue(call(c(int64(1)), oRaise{"no"})); err == nil {
		t.Error("an argument that raises did not")
	}
	if f.argBusy {
		t.Error("the buffer is busy after an argument raised")
	}
	// a callee that is not a generic function (a builtin: its arguments are a
	// slice of its own), whose argument raises
	if _, err := f.evalValue(oCall{[]op{c(vm.Core.own("tuple").Value), c(int64(1)), oRaise{"no"}}}); err == nil {
		t.Error("an argument of a builtin that raises did not")
	}
	if v, err := f.evalValue(oCall{[]op{c(vm.Core.own("tuple").Value), c(int64(1)), c(int64(2))}}); err != nil || showValue(v) != "(1, 2)" {
		t.Errorf("a builtin: %v %v", v, err)
	}
	// a method that is not defined: the error, with the arguments it was called on
	if _, err := f.evalValue(call(c(int64(1)))); err == nil {
		t.Error("a call with no method did not fail")
	}
	// a julia method: its locals hold the arguments, and the buffer is clear
	must(s.Eval("argk_id(x, y) = y"))
	g := s.Mod.own(expr.Symbol("argk_id")).Value.(*Function)
	got, err := f.evalValue(oCall{[]op{c(g), c(int64(7)), c(int64(8))}})
	if err != nil || got != int64(8) {
		t.Errorf("a julia method: %v %v", got, err)
	}
}
