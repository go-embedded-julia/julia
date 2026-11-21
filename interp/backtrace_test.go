package interp

import (
	"context"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// TestGetBacktraceShim: jl_get_backtrace answers a Core.svec of two empty
// vectors, a Vector{Ptr{Cvoid}} and a Vector{Any} (stackwalk.c:324-330).
func TestGetBacktraceShim(t *testing.T) {
	vm := bootedVM(t)
	v, err := vm.shims["jl_get_backtrace"](vm, nil)
	if err != nil {
		t.Fatal(err)
	}
	sv, ok := v.(SVec)
	if !ok || len(sv) != 2 {
		t.Fatalf("jl_get_backtrace: %v, want a 2-element svec", showValue(v))
	}
	for i, want := range []string{"Vector{Ptr{Nothing}}", "Vector{Any}"} {
		s, ok := sv[i].(*Struct)
		if !ok {
			t.Fatalf("element %d: %v", i, showValue(sv[i]))
		}
		if got := showValue(s.Type); got != want {
			t.Errorf("element %d: type %s, want %s", i, got, want)
		}
		if n := showValue(s.Fields[1]); n != "(0,)" {
			t.Errorf("element %d: size %v, want (0,)", i, n)
		}
	}
}

// TestGetExcstackShim: jl_get_excstack of a Task is an empty Vector{Any}, of
// anything else the type error JL_TYPECHK raises.
func TestGetExcstackShim(t *testing.T) {
	vm := bootedVM(t)
	task := &Struct{Type: vm.coreType("Task").(*core.DataType)}
	v, err := vm.shims["jl_get_excstack"](vm, []any{task, int32(1), int32(1 << 30)})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := v.(*Struct); !ok || showValue(s.Type) != "Vector{Any}" || showValue(s.Fields[1]) != "(0,)" {
		t.Errorf("jl_get_excstack: %s, want an empty Vector{Any}", showValue(v))
	}
	_, err = vm.shims["jl_get_excstack"](vm, []any{int64(1), int32(1), int32(1)})
	check(t, "a non-Task", nil, err, "!TypeError")
}

// TestCatchBacktraceBase: on the Base image, catch_backtrace() is empty, the
// text showerror prints for it is the exception's alone, and a failed task is
// fetched, waited on and shown without the not-ported error (julia 1.12.6,
// base/error.jl:133-136, whose empty result is what julia gives when no
// backtrace was recorded; show_backtrace prints nothing for an empty one).
func TestCatchBacktraceBase(t *testing.T) {
	k := resetKernel(t, resetImage(t))
	for _, c := range []struct{ cell, want string }{
		{`try error("x") catch; length(catch_backtrace()) end`, "0"},
		{`try error("x") catch; typeof(catch_backtrace()) end`, "Vector{Union{Ptr{Nothing}, InterpreterIP}}"},
		{`try error("x") catch e; sprint(showerror, e, catch_backtrace()) end`, `"x"`},
		{`t = Task(() -> error("boom")); schedule(t); try wait(t) catch e; sprint(showerror, e) end`, `"TaskFailedException\n\n    nested task error: boom"`},
		{`try fetch(t) catch e; sprint(showerror, e) end`, `"TaskFailedException\n\n    nested task error: boom"`},
		{`istaskfailed(t)`, "true"},
		{`length(current_exceptions(t))`, "0"},
	} {
		r := k.Eval(context.Background(), c.cell)
		if r.Err != nil {
			t.Errorf("%s: %v", c.cell, r.Err)
			continue
		}
		if !strings.HasPrefix(r.Value, c.want) {
			t.Errorf("%s: got %q, want prefix %q", c.cell, r.Value, c.want)
		}
	}
}
