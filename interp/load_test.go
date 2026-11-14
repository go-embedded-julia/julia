package interp

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_load_ reads VM.Sources only, evaluates each form in the module given,
// and answers missing files, bad arguments, syntax errors and a file that
// includes itself as julia does.
func TestLoad(t *testing.T) {
	vm := New()
	vm.Sources = fstest.MapFS{
		"two.jl":  {Data: []byte("x = 1\ny = (x, 2)\n")},
		"bad.jl":  {Data: []byte("x = (\n")},
		"self.jl": {Data: []byte("ccall(:jl_load_, Any, (Any, Any), Core, \"self.jl\")\n")},
		"none.jl": {Data: []byte("# nothing\n")},
		"deep.jl": {Data: []byte(strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000))},
	}
	m := vm.modules["Main"]
	cases := []struct {
		name string
		args []any
		want string
	}{
		{"the last value", []any{m, "two.jl"}, "(1, 2)"},
		{"an empty file", []any{m, "none.jl"}, "nothing"},
		{"a missing file", []any{m, "nope.jl"}, "!File \\\"nope.jl\\\" not found"},
		{"a host path", []any{m, "/etc/passwd"}, "!not found"},
		{"a syntax error", []any{m, "bad.jl"}, "!syntax:"},
		{"a file over the parser's limits", []any{m, "deep.jl"}, "!syntax:"},
		{"a non-module", []any{int64(1), "two.jl"}, "!TypeError(:include, \"\", Module, 1)"},
		{"a non-string", []any{m, int64(1)}, "!TypeError(:include, \"\", String, 1)"},
	}
	for _, c := range cases {
		v, err := vm.jlLoad(c.args)
		check(t, c.name, v, err, c.want)
	}
	vm.MaxDepth = 20
	v, err := vm.jlLoad([]any{vm.Core, "self.jl"})
	check(t, "a file that includes itself", v, err, "!StackOverflowError()")
	if vm.depth != 0 {
		t.Errorf("depth after the overflow: %d", vm.depth)
	}
	// julia's own sources are there by default
	for _, name := range []string{"boot.jl", "optimized_generics.jl"} {
		if _, err := fs.Stat(New().Sources, name); err != nil {
			t.Errorf("default sources: %v", err)
		}
	}
}

// Module initializers, as julia 1.12.6 runs them for the same modules
// (toplevel.c:207-242): [:B, :A, :O, :P], each module's __init__ waiting
// for the outermost module being defined around it; an __init__'s error is
// InitError(name, error); jl_init_restored_module runs one now.
func TestModuleInitializers(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	// rec(:x) records x after what came before: ((((), :B), :A), ...)
	v, err := s.Eval(`mutable struct Rec; v; end
const R = Rec(())
rec(x) = (setfield!(R, :v, (getfield(R, :v), x)); nothing)
module O
    module A
        __init__() = getfield(getfield(Main, :L), :rec)(:A)
        module B
            __init__() = getfield(getfield(Main, :L), :rec)(:B)
        end
    end
    __init__() = getfield(getfield(Main, :L), :rec)(:O)
    module C end
end
module P
    __init__() = getfield(getfield(Main, :L), :rec)(:P)
end
getfield(R, :v)`)
	check(t, "the order", v, err, "(((((), :B), :A), :O), :P)")
	_, err = s.Eval(`module Bad
    __init__() = throw(ErrorException("boom"))
end`)
	check(t, "an __init__'s error", nil, err, `!InitError(:Bad, ErrorException("boom"))`)
	// jl_init_restored_module: P's __init__ again, now
	p := s.Mod.own("P").Value.(*Module)
	if _, err := vm.shims["jl_init_restored_module"](vm, []any{p}); err != nil {
		t.Fatal(err)
	}
	v, err = s.Eval("getfield(R, :v)")
	check(t, "jl_init_restored_module", v, err, "((((((), :B), :A), :O), :P), :P)")
	_, err = vm.shims["jl_init_restored_module"](vm, []any{int64(1)})
	check(t, "jl_init_restored_module of 1", nil, err, "!TypeError(:ccall")
	// a module defined elsewhere stays waiting when another one's
	// initializers run
	other := newModule("Other", nil)
	vm.moduleInitOrder = []*Module{other}
	if err := vm.moduleDefined(newModule("Fresh", vm.Main)); err != nil || len(vm.moduleInitOrder) != 1 || vm.moduleInitOrder[0] != other {
		t.Errorf("the waiting module: %v %v", vm.moduleInitOrder, err)
	}
	// before Core has InitError (boot.jl), the error as it is
	bare := New()
	_, err = bare.NewSession().Eval("module Early\n__init__() = throw(1)\nend")
	check(t, "an __init__'s error before InitError", nil, err, "!julia exception: 1")

	// generating output, as the VM builds Base: the initializers wait, Base
	// itself joins them (jl_init_restored_module), and they run in order
	// afterwards; the first error stops them
	vm.moduleInitOrder = nil
	vm.GeneratingOutput = true
	if _, err := s.Eval(`setfield!(R, :v, ())
module Q
    __init__() = getfield(getfield(Main, :L), :rec)(:Q)
end`); err != nil {
		t.Fatal(err)
	}
	if _, err := vm.shims["jl_init_restored_module"](vm, []any{p}); err != nil {
		t.Fatal(err)
	}
	v, err = s.Eval("getfield(R, :v)")
	check(t, "none run while generating output", v, err, "()")
	vm.GeneratingOutput = false
	if err := vm.RunModuleInitializers(); err != nil {
		t.Fatal(err)
	}
	v, err = s.Eval("getfield(R, :v)")
	check(t, "run afterwards, in order", v, err, "(((), :Q), :P)")
	vm.moduleInitOrder = []*Module{s.Mod.own("Bad").Value.(*Module), p}
	err = vm.RunModuleInitializers()
	check(t, "the first error stops them", nil, err, `!InitError(:Bad, ErrorException("boom"))`)
}

// Base.jl:410-415's loop records the Compiler's sources from the embedded
// ones: (mod, abspath, size, CRC-32C, -1.0), julia's _crc32c of
// "123456789" being 0xe3069283; files of other modules are left out.
func TestCompilerDependencies(t *testing.T) {
	vm := New()
	vm.Sources = fstest.MapFS{
		"julia/Compiler/src/Compiler.jl": {Data: []byte("123456789")},
		"julia/Compiler/src/sub.jl":      {Data: []byte("")},
		"other.jl":                       {Data: []byte("x")},
		// as Base.jl:324-425: the loop inside `if is_primary_base_module`
		"loop.jl": {Data: []byte("if true\n" +
			"for i = 1:length(_included_files)\n" +
			"    _include_dependency!(_compiler_require_dependencies, true, i)\nend\n" +
			"end\n")},
	}
	base := newModule("Base", vm.Main)
	base.usings = []*Module{vm.Core}
	vm.baseModule = base
	vm.replaceBase(base)
	compiler := newModule("Compiler", base)
	sub := newModule("Sub", compiler)
	set := func(name expr.Symbol, v any) {
		b := base.declare(name, vm.newWorld())
		b.Value, b.Const = v, true
	}
	set("Compiler", compiler)
	set("_included_files", vm.newVector(core.AnyType, []any{
		vm.newTuple([]any{compiler, "julia/Compiler/src/Compiler.jl"}),
		vm.newTuple([]any{sub, "julia/Compiler/src/sub.jl"}),
		vm.newTuple([]any{base, "other.jl"}),
		undef,
	}))
	deps := vm.newVector(core.AnyType, nil)
	set("_compiler_require_dependencies", deps)
	if _, err := vm.jlLoad([]any{base, "loop.jl"}); err != nil {
		t.Fatal(err)
	}
	_, _, got, _ := arrayOf(deps)
	want := []string{
		`(Main.Base.Compiler, "/julia/home/julia/Compiler/src/Compiler.jl", 0x0000000000000009, 0xe3069283, -1.0)`,
		`(Main.Base.Compiler.Sub, "/julia/home/julia/Compiler/src/sub.jl", 0x0000000000000000, 0x00000000, -1.0)`,
	}
	if len(got) != len(want) {
		t.Fatalf("%d dependencies: %s", len(got), showValue(got))
	}
	for i := range want {
		if Repr(got[i]) != want[i] {
			t.Errorf("dependency %d: %s, want %s", i, Repr(got[i]), want[i])
		}
	}
	// a Compiler source the VM does not have, and a Base without the loop's globals
	set("_included_files", vm.newVector(core.AnyType, []any{vm.newTuple([]any{compiler, "julia/Compiler/src/gone.jl"})}))
	if _, err := vm.jlLoad([]any{base, "loop.jl"}); err == nil || !strings.Contains(err.Error(), "not among the VM's") {
		t.Errorf("a missing Compiler source: %v", err)
	}
	if err := vm.recordCompilerDependencies(newModule("Bare", vm.Main)); err == nil {
		t.Error("a Base without _included_files")
	}
	// Base.jl:418's loop over the dependencies, in the let after it,
	// rewrites their paths: it is not the one replaced
	relpathLoop := expr.New("for", expr.New("=", expr.Symbol("i"), expr.Symbol("_compiler_require_dependencies")),
		expr.New("call", expr.Symbol("relpath"), expr.Symbol("i")))
	for _, form := range []any{expr.New("for", expr.Symbol("other")), int64(1), relpathLoop} {
		if isCompilerDependencyLoop(form) {
			t.Errorf("%s taken for Base.jl:410's loop", showValue(form))
		}
	}
}

// Base.jl:317's include of JuliaSyntax's Julia source gives Base a native
// JuliaSyntax (juliasyntax.go), whose enable_in_core!() Base's __init__
// calls; the same form outside Base, or another file, is evaluated as is.
func TestJuliaSyntaxInBase(t *testing.T) {
	vm := New()
	const inc = `include(@__MODULE__, string(BUILDROOT, "JuliaSyntax/src/JuliaSyntax.jl"))`
	vm.Sources = fstest.MapFS{
		"base.jl":  {Data: []byte(inc + "\ngetfield(JuliaSyntax, :enable_in_core!)()\n")},
		"other.jl": {Data: []byte("include(@__MODULE__, string(BUILDROOT, \"x.jl\"))\n")},
	}
	// the bootstrap's Base, as `module Base` in Main makes it (toplevel.go)
	base := newModule("Base", vm.Main)
	base.usings = []*Module{vm.Core}
	vm.baseModule = base
	vm.replaceBase(base)
	v, err := vm.jlLoad([]any{base, "base.jl"})
	check(t, "enable_in_core!()", v, err, "nothing")
	js, ok := base.own("JuliaSyntax").Value.(*Module)
	if !ok || js.Parent != base || !base.own("JuliaSyntax").Const {
		t.Fatalf("Base.JuliaSyntax: %v", base.own("JuliaSyntax").Value)
	}
	// outside Base the form is evaluated: here no include is defined
	if _, err := vm.jlLoad([]any{vm.Main, "base.jl"}); err == nil {
		t.Error("the include evaluated outside Base")
	}
	if _, err := vm.jlLoad([]any{base, "other.jl"}); err == nil {
		t.Error("another file's include taken for JuliaSyntax's")
	}
	for _, form := range []any{
		int64(1),
		expr.New("call", expr.Symbol("f"), int64(1), int64(2)),
		expr.New("call", expr.Symbol("include"), int64(1), "JuliaSyntax/src/JuliaSyntax.jl"),
	} {
		if isJuliaSyntaxInclude(form) {
			t.Errorf("%s taken for JuliaSyntax's include", showValue(form))
		}
	}
}
