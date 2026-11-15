package interp

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_module_getloc: the file and line of a module's `module` form (the position
// of the last LineNumberNode of the forms it is in, as toplevel.c:156-158 has
// it); the empty file and line 0 for a module made otherwise.
func TestModuleGetloc(t *testing.T) {
	vm := bootedVM(t)
	s := vm.sessionFor(vm.Main) // the session a module form's position is read from
	src := "\n\nmodule Mloc\nmodule Inner\nend\nend\n"
	if _, err := s.Eval(src); err != nil {
		t.Fatal(err)
	}
	mloc := s.Mod.own("Mloc").Value.(*Module)
	inner := mloc.own("Inner").Value.(*Module)
	if mloc.file != "none" || mloc.line != 3 || inner.file != "none" || inner.line != 4 {
		t.Errorf("Mloc %s:%d, Inner %s:%d", mloc.file, mloc.line, inner.file, inner.line)
	}
	// a :toplevel built at run time carries its own line numbers
	if _, err := s.Eval("Core.eval(Core.Main, Core.Expr(:toplevel, Core.LineNumberNode(7, :nm), Core.Expr(:module, true, :Nested, Core.Expr(:block))))"); err != nil {
		t.Fatal(err)
	}
	if nested := vm.Main.own("Nested").Value.(*Module); nested.file != "nm" || nested.line != 7 {
		t.Errorf("Nested %s:%d", nested.file, nested.line)
	}
	// a LineNumberNode with no file keeps the last one
	s.at(expr.LineNumberNode{Line: 9})
	if s.file != "nm" || s.line != 9 {
		t.Errorf("a line with no file: %s:%d", s.file, s.line)
	}
	if _, err := s.Eval("mutable struct R32; x::Int32; end"); err != nil {
		t.Fatal(err)
	}
	ref, err := s.Eval("R32(Core.Intrinsics.trunc_int(Int32, 0))")
	if err != nil {
		t.Fatal(err)
	}
	ptr := &Pointer{Type: vm.ptrType(core.Int32Type), Target: ref}
	getloc := vm.shims["jl_module_getloc"]
	got, err := getloc(vm, []any{inner, ptr})
	if err != nil || got != expr.Symbol("none") || ref.(*Struct).Fields[0] != int32(4) {
		t.Errorf("jl_module_getloc: %v %v, line %v", got, err, ref.(*Struct).Fields[0])
	}
	// Core was not made by a form: the empty file, line 0
	if got, err := getloc(vm, []any{vm.Core, ptr}); err != nil || got != expr.Symbol("") || ref.(*Struct).Fields[0] != int32(0) {
		t.Errorf("Core: %v %v", got, err)
	}
	if _, err := getloc(vm, []any{int64(1), ptr}); err == nil {
		t.Error("a value that is no module")
	}
	if _, err := getloc(vm, []any{inner, int64(1)}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a line through no pointer: %v", err)
	}
}

// jl_type_morespecific is Base.morespecific.
func TestTypeMorespecific(t *testing.T) {
	vm := bootedVM(t)
	s := vm.NewSession()
	ev := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	more := vm.shims["jl_type_morespecific"]
	for _, c := range []struct {
		a, b string
		want int32
	}{
		{"Tuple{Int}", "Tuple{Integer}", 1},
		{"Tuple{Integer}", "Tuple{Int}", 0},
		{"Tuple{Int, Any}", "Tuple{Any, Int}", 0},
	} {
		if got, err := more(vm, []any{ev(c.a), ev(c.b)}); err != nil || got != c.want {
			t.Errorf("jl_type_morespecific(%s, %s) = %v, %v", c.a, c.b, got, err)
		}
	}
	// past the work bound: an error from subtyping, as the lookup tests have it
	a, b := ev("Tuple{Array{T, 1}, T} where T<:Integer"), ev("Tuple{Array{S, 1}, S} where S<:Number")
	failed := false
	for steps := 1; steps < 64 && !failed; steps++ {
		old := core.SetMaxSubtypeSteps(steps)
		_, err := more(vm, []any{a, b})
		core.SetMaxSubtypeSteps(old)
		failed = err != nil
	}
	if !failed {
		t.Error("no work bound made jl_type_morespecific fail")
	}
}

// jl_types_egal and jl_has_typevar.
func TestTypesEgalAndHasTypevar(t *testing.T) {
	vm := bootedVM(t)
	s := vm.NewSession()
	ev := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	egal := vm.shims["jl_types_egal"]
	for _, c := range []struct {
		a, b string
		want int32
	}{{"Int", "Int", 1}, {"Int", "Int8", 0}, {"Array{Int, 1}", "Array{Int, 1}", 1}, {"Union{Int, Int8}", "Union{Int8, Int}", 1}} {
		if got, err := egal(vm, []any{ev(c.a), ev(c.b)}); err != nil || got != c.want {
			t.Errorf("jl_types_egal(%s, %s) = %v, %v", c.a, c.b, got, err)
		}
	}
	has := vm.shims["jl_has_typevar"]
	tv := ev("T = Core.TypeVar(:T); T")
	for _, c := range []struct {
		t    any
		want int32
	}{{ev("Array{T, 1}"), 1}, {ev("Array{Int, 1}"), 0}, {tv, 1}, {ev("Int"), 0}} {
		if got, err := has(vm, []any{c.t, tv}); err != nil || got != c.want {
			t.Errorf("jl_has_typevar(%s) = %v, %v", showValue(c.t), got, err)
		}
	}
	if _, err := has(vm, []any{ev("Int"), int64(1)}); err == nil {
		t.Error("a second argument that is no TypeVar")
	}
}

// jl_methtable_lookup(type, world) (gf.c:289-299): the method whose signature
// is exactly type, visible in world.
func TestMethtableLookup(t *testing.T) {
	vm := bootedVM(t)
	s := vm.NewSession()
	if _, err := s.Eval("g(x::Int) = 1; g(x::Integer) = 2; g(x) = 3"); err != nil {
		t.Fatal(err)
	}
	ty := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	lookup := vm.shims["jl_methtable_lookup"]
	sigOf := func(v any) string {
		if v == nil {
			return "nothing"
		}
		return showValue(v.(*Struct).Fields[7])
	}
	world := vm.World()
	for _, c := range []struct{ name, tt, want string }{
		{"the exact signature", "Tuple{typeof(g), Int}", "Tuple{Main.L.#g, Int64}"},
		{"the one a method has", "Tuple{typeof(g), Integer}", "Tuple{Main.L.#g, Integer}"},
		{"Any", "Tuple{typeof(g), Any}", "Tuple{Main.L.#g, Any}"},
		{"a signature no method has (a subtype of one)", "Tuple{typeof(g), Int8}", "nothing"},
		{"two arguments", "Tuple{typeof(g), Int, Int}", "nothing"},
		{"not a tuple", "Int", "nothing"},
		{"the empty tuple", "Tuple{}", "nothing"},
		{"a union of signatures", "Union{Tuple{typeof(g), Int}, Tuple{typeof(g), String}}", "nothing"},
	} {
		v, err := lookup(vm, []any{ty(c.tt), world})
		if err != nil || sigOf(v) != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, sigOf(v), err, c.want)
		}
	}
	// the method is the one the table holds: its name and function, as a Method
	v, _ := lookup(vm, []any{ty("Tuple{typeof(g), Int}"), world})
	if m := v.(*Struct); m.Type != vm.tMethod || showValue(m.Fields[0]) != ":g" {
		t.Errorf("the method object: %v", showValue(v))
	}
	// a world before the method: nothing, and after a later one it is there
	if v, err := lookup(vm, []any{ty("Tuple{typeof(g), Int}"), uint64(0)}); v != nil || err != nil {
		t.Errorf("world 0: %v %v", v, err)
	}
	// refusals: a world that is no UInt, a callee no table holds
	if _, err := lookup(vm, []any{ty("Tuple{typeof(g), Int}"), int64(1)}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a world that is no UInt: %v", err)
	}
	if _, err := lookup(vm, []any{ty("Tuple{Any, Int}"), world}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("a callee of no table: %v", err)
	}
}
