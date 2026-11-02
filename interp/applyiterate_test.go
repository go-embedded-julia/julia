package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// _apply_iterate over memories and through an iterate function: what
// julia's builtins.c:661-875 spreads, and where it stops.
func TestApplyIterate(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	// a countdown iterator with intrinsics only, Base's < and - not being
	// there: it(3) gives 3, 2, 1
	if _, err := s.Eval(`it(n::Int) = Core.Intrinsics.slt_int(0, n) ? (n, Core.Intrinsics.sub_int(n, 1)) : nothing
it(n::Int, s::Int) = it(s)
one(x::Int) = 5
half(x::Int) = (x,)
boom(x::Int) = it(x)
boom(x::Int, s::Int) = throw(s)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"Core._apply_iterate(it, Core.tuple, 3)", "(3, 2, 1)"},
		{"Core._apply_iterate(it, Core.tuple, (0,), 2, Core.svec(9))", "(0, 2, 1, 9)"},
		{"Core._apply_iterate(it, Core.tuple, 0)", "()"},
		{"Core._apply_iterate(one, Core.tuple, 1)", "!BoundsError(5, 1)"},
		{"Core._apply_iterate(half, Core.tuple, 1)", "!BoundsError((1,), 2)"},
		{"Core._apply_iterate(boom, Core.tuple, 2)", "!julia exception: 1"},
		{"Core._apply_iterate(it, Core.tuple, Core.Memory{Any}(undef, 0))", "()"},
		{"Core._apply_iterate(it, Core.tuple, Core.Memory{Int}(undef, 2))", "(0, 0)"},
		{"Core._apply_iterate(it, Core.tuple, Core.Memory{Any}(undef, 1))", "!UndefRefError()"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	vm.MaxMemoryElems = 2
	for _, src := range []string{"Core._apply_iterate(it, Core.tuple, (1, 2, 3))", "Core._apply_iterate(it, Core.tuple, 3)"} {
		v, err := s.Eval(src)
		check(t, src, v, err, "!VM.MaxMemoryElems")
	}
}

// jl_genericmemory_copyto: a move within one memory, its refusals.
func TestMemoryCopyto(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	mem := func(src string) *Memory {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v.(*Memory)
	}
	m := mem("Core.Memory{Any}(undef, 4)")
	copy(m.Elems, []any{int64(1), int64(2), int64(3), int64(4)})
	at := func(m *Memory, i int) *Pointer { return vm.refPtrOrOffset(&MemoryRef{Mem: m, Off: i}) }
	cp := vm.shims["jl_genericmemory_copyto"]
	// overlapping, forwards: memmove's answer
	if _, err := cp(vm, []any{m, at(m, 1), m, at(m, 0), int64(3)}); err != nil {
		t.Fatal(err)
	}
	if got := showValue(vm.newTuple(m.Elems)); got != "(1, 1, 2, 3)" {
		t.Errorf("overlapping move: %s", got)
	}
	u := mem("Core.Memory{Union{Nothing, Int8}}(undef, 2)")
	ints := mem("Core.Memory{Int}(undef, 2)")
	other := mem("Core.Memory{Any}(undef, 4)")
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"an isbits Union memory, by index", []any{u, at(u, 1), u, at(u, 0), int64(1)}, "nothing"},
		{"memories of two types", []any{m, at(m, 0), ints, at(ints, 0), int64(1)}, "!ArgumentError(\"jl_genericmemory_copyto requires source and dest to have same type\")"},
		{"past the end", []any{m, at(m, 2), m, at(m, 0), int64(3)}, "!not ported yet: jl_genericmemory_copyto of 3 elements outside its memories"},
		{"a negative count", []any{m, at(m, 0), m, at(m, 0), int64(-1)}, "!outside its memories"},
		{"a pointer into another memory", []any{m, at(other, 0), m, at(m, 0), int64(1)}, "!outside its memories"},
		{"a misaligned pointer", []any{m, &Pointer{Target: m, Off: 3}, m, at(m, 0), int64(1)}, "!outside its memories"},
		{"a count that is not an Int", []any{m, at(m, 0), m, at(m, 0), int32(1)}, "!not ported yet: jl_genericmemory_copyto of"},
	} {
		v, err := cp(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
}

// c.jl's platform queries, answered as julia 1.12.6 on darwin/arm64 does
// (ccall(:jl_is_char_signed, Ref{Bool}, ()) is true there).
func TestPlatformShims(t *testing.T) {
	vm := New()
	for name, want := range map[string]string{
		"jl_is_char_signed":   "true",
		"jl_get_UNAME":        ":Darwin",
		"jl_get_ARCH":         ":aarch64",
		"jl_sizeof_mode_t":    "2",
		"jl_get_LLVM_VERSION": "0x0002bf8b", // 180107
		"jl_types_equal":      "1",
		"jl_ver_major":        "1",
		"jl_ver_minor":        "12",
		"jl_ver_patch":        "6",
		"jl_ver_is_release":   "1",
	} {
		v, err := vm.shims[expr.Symbol(name)](vm, []any{vm.Core, vm.Core})
		check(t, name, v, err, want)
	}
	if v, _ := vm.shims["jl_types_equal"](vm, []any{vm.Core, int64(1)}); v != int32(0) {
		t.Errorf("jl_types_equal of unequal values: %v", v)
	}
}
