package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A ccall((:f, lib), …) reaches the shim the VM has for f in lib, lib
// being a literal or a global; another library, or a name the library has
// no shim for, is refused when the call runs.
func TestLibraryQualifiedCcall(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vm.libShims = map[string]map[expr.Symbol]shim{"libtest": {
		"twice": func(vm *VM, a []any) (any, error) { return a[0].(int64) * 2, nil },
	}}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{`ccall((:twice, "libtest"), Int, (Int,), 21)`, "42"},
		{`const LIBT = "libtest"; ccall((:twice, LIBT), Int, (Int,), 4)`, "8"},
		{`ccall((:twice, "libother"), Int, (Int,), 1)`, `!not ported yet: ccall((:twice, "libother"))`},
		{`ccall((:thrice, "libtest"), Int, (Int,), 1)`, `!not ported yet: ccall((:thrice, "libtest"))`},
		{`f() = ccall((:twice, throw(ErrorException("no library"))), Int, (Int,), 1); f()`, "!no library"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}

// A Ref{T} argument is passed as the Ptr{T} lowering converted it to.
func TestRefArg(t *testing.T) {
	vm := New()
	refInt32 := must(core.Apply(core.RefType, core.Int32Type))
	for _, c := range []struct {
		at, v any
		want  bool
	}{
		{refInt32, &Pointer{Type: vm.ptrType(core.Int32Type)}, true},
		{refInt32, &Pointer{Type: vm.ptrType(core.Int64Type)}, false},
		{refInt32, int32(1), false},
		{vm.ptrType(core.Int32Type), &Pointer{Type: vm.ptrType(core.Int32Type)}, false},
		{core.AnyType, &Pointer{Type: vm.ptrType(core.Int32Type)}, false},
	} {
		if got := refArg(c.at, c.v); got != c.want {
			t.Errorf("refArg(%s, %s) = %v", showValue(c.at), showValue(c.v), got)
		}
	}
}

// Through pointer_from_objref of a mutable struct, a pointer of its first
// field's type reads and writes that field, as a C function does through a
// Ref; any other element, offset or type has no address.
func TestPointerIntoMutableStruct(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	v, err := s.Eval("mutable struct MR; x::Int32; y::Int; end; MR(Int32(5), 7)")
	if err != nil {
		t.Fatal(err)
	}
	st := v.(*Struct)
	p := &Pointer{Type: vm.ptrType(core.Int32Type), Target: st}
	v, err = vm.ptrLoad("pointerref", p, 1)
	check(t, "load the first field", v, err, "5")
	check(t, "store the first field", nil, vm.ptrStore("pointerset", p, int32(9), 1), "")
	v, err = vm.ptrLoad("pointerref", p, 1)
	check(t, "load it back", v, err, "9")
	_, err = vm.ptrLoad("pointerref", p, 2)
	check(t, "load the second element", nil, err, "!pointerref: invalid pointer")
	check(t, "store the second element", nil, vm.ptrStore("pointerset", p, int32(1), 2), "!pointerset: invalid pointer")
	check(t, "store another type", nil, vm.ptrStore("pointerset", p, int64(1), 1), "!TypeError(:pointerset")
	wide := &Pointer{Type: vm.ptrType(core.Int64Type), Target: st}
	_, err = vm.ptrLoad("pointerref", wide, 1)
	check(t, "load as another type", nil, err, "!pointerref: invalid pointer")
	off := &Pointer{Type: p.Type, Target: st, Off: 8}
	_, err = vm.ptrLoad("pointerref", off, 1)
	check(t, "load at an offset", nil, err, "!pointerref: invalid pointer")
	// an immutable struct has no address, and an unset field is undefined
	v, err = s.Eval("struct IR; x::Int32; end; IR(Int32(1))")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vm.ptrLoad("pointerref", &Pointer{Type: p.Type, Target: v.(*Struct)}, 1)
	check(t, "load from an immutable", nil, err, "!pointerref: invalid pointer")
	v, err = s.Eval("mutable struct MU; x::Any; MU() = new(); end; MU()")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vm.ptrLoad("pointerref", &Pointer{Type: vm.ptrType(core.AnyType), Target: v.(*Struct)}, 1)
	check(t, "load an unset field", nil, err, "!UndefRefError")
	v, err = s.Eval("mutable struct ME; end; ME()")
	if err != nil {
		t.Fatal(err)
	}
	_, err = vm.ptrLoad("pointerref", &Pointer{Type: p.Type, Target: v.(*Struct)}, 1)
	check(t, "load from a struct without fields", nil, err, "!pointerref: invalid pointer")
}
