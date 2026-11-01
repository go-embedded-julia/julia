package interp

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Core's printing reaches VM.Stdout through pointers without addresses,
// and sizeof answers as jl_f_sizeof does.
func TestPointersAndOutput(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	vm.Stdout = &out
	s := vm.CoreSession()
	if _, err := s.Eval(`println("hi", 'é', 1); print(:a); show(2); write(stdout, 0x21)`); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "hié1\n:a2!" {
		t.Errorf("printed %q", got)
	}
	var errOut strings.Builder
	vm.Stderr = &errOut
	if _, err := s.Eval(`println(stderr, "e")`); err != nil || errOut.String() != "e\n" {
		t.Errorf("stderr: %q, %v", errOut.String(), err)
	}
	_, err := biSizeof(vm, nil)
	check(t, "sizeof arity", nil, err, "!sizeof: too few arguments")
	for _, c := range []struct{ src, want string }{
		{`sizeof("abc")`, "3"},
		{"sizeof(:ab)", "2"},
		{"sizeof(Core.svec(1))", "16"},
		{"sizeof(Int8)", "1"},
		{"sizeof(1)", "8"},
		{"sizeof(true)", "1"},
		{"sizeof('a')", "4"},
		{"sizeof(Memory{Int16}(undef, 3))", "6"},
		{"try sizeof(Integer) catch e; e end", `ErrorException("Abstract type Integer does not have a definite size.")`},
		{"try sizeof(Union{}) catch e; e end", `ErrorException("The empty type does not have a definite size since it does not have instances.")`},
		{"sizeof(Memory{Any}(undef, 1))", "!not ported yet: sizeof of a Memory{Any}"},
		{"sizeof(Tuple{Int64})", "!not ported yet: sizeof of the struct type Tuple"},
		{"sizeof(QuoteNode(1))", "!not ported yet: sizeof of a QuoteNode"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// the intrinsics and shims, each refusal
	u8 := vm.ptrType(core.UInt8Type)
	i64p := vm.ptrType(core.Int64Type)
	str := &Pointer{Type: u8, Target: "ab"}
	free := must(core.Apply(core.ArrayType, &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}, int64(1)))
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"cglobal of a non-type", biCglobal, []any{expr.Symbol("jl_uv_stdout"), int64(1)}, "!TypeError(:cglobal"},
		{"cglobal of a free type", biCglobal, []any{expr.Symbol("jl_uv_stdout"), free}, "!cglobal: type argument not concrete"},
		{"cglobal of a non-symbol", biCglobal, []any{int64(1), core.NothingType}, "!TypeError(:cglobal, \"\", Symbol, 1)"},
		{"cglobal of an unknown name", biCglobal, []any{expr.Symbol("environ"), core.NothingType}, "!not ported yet: cglobal(:environ)"},
		{"cglobal typed", biCglobal, []any{expr.Symbol("jl_uv_stderr"), core.Int64Type}, ""},
		{"pointerref of a non-pointer", biPointerref, []any{int64(1), int64(1), int64(1)}, "!TypeError(:pointerref"},
		{"pointerref of a non-Int index", biPointerref, []any{str, "1", int64(1)}, "!TypeError(:pointerref, \"\", Int64"},
		{"pointerref of a non-Int align", biPointerref, []any{str, int64(1), "1"}, "!TypeError(:pointerref, \"\", Int64"},
		{"pointerref of a string", biPointerref, []any{str, int64(2), int64(1)}, "0x62"},
		{"pointerref past a string", biPointerref, []any{str, int64(3), int64(1)}, "!pointerref: invalid pointer"},
		{"pointerref of a string as Int64", biPointerref, []any{&Pointer{Type: i64p, Target: "ab"}, int64(1), int64(1)}, "!not ported yet: pointerref of a String"},
		{"pointerref of a slot past it", biPointerref, []any{&Pointer{Type: u8, Target: vm.cglobals["jl_uv_stdout"]}, int64(2), int64(1)}, "!pointerref: invalid pointer"},
		{"pointerref of nothing", biPointerref, []any{&Pointer{Type: u8}, int64(1), int64(1)}, "!pointerref: invalid pointer"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	stream := vm.cglobals["jl_uv_stdout"].val
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"jl_string_ptr of a non-string", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_uv_puts to a non-stream", []any{int64(1), str, int64(1)}, "!ccall: not an output stream"},
		{"jl_uv_puts of a non-string pointer", []any{stream, &Pointer{Type: u8}, int64(1)}, "!not ported yet: jl_uv_puts"},
		{"jl_uv_puts past the string", []any{stream, str, int64(3)}, "!jl_uv_puts: 3 bytes past the string"},
		{"jl_uv_puts of a UInt count", []any{stream, str, uint64(1)}, "nothing"},
		{"jl_uv_putb to a non-stream", []any{int64(1), uint8(1)}, "!ccall: not an output stream"},
		{"jl_uv_putc to a non-stream", []any{int64(1), expr.Char(0)}, "!ccall: not an output stream"},
		{"jl_static_show to a non-stream", []any{int64(1), int64(1)}, "!ccall: not an output stream"},
		{"jl_set_istopmod of a non-module", []any{int64(1), true}, "!TypeError(:ccall"},
		{"jl_module_parent of a non-module", []any{int64(1)}, "!TypeError(:ccall"},
		{"jl_module_parent of Main", []any{vm.Main}, "Main"},
		{"jl_module_parent of Core.Intrinsics", []any{vm.modules["Intrinsics"]}, "Core"},
		{"jl_prepend_cwd", []any{"a.jl"}, `"a.jl"`},
		{"jl_uv_flush", []any{stream}, "nothing"},
	} {
		name := strings.Fields(c.name)[0]
		v, err := vm.shims[expr.Symbol(name)](vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	if !vm.egal(str, &Pointer{Type: u8, Target: "ab"}) || vm.egal(str, int64(1)) || vm.egal(int64(1), str) {
		t.Error("egal of pointers")
	}
	// a TypeName field other than an inference hint is not set
	_, err = biSetfield(vm, []any{core.Int64Type.Name, expr.Symbol("name"), expr.Symbol("x")})
	check(t, "setfield! of a TypeName's name", nil, err, "!not ported yet: setfield! of a TypeName's name")
}
