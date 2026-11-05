package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The statements a method's definition resolves (resolve_definition_effects):
// a ccall's types evaluated, a Core.tuple of quoted values folded, a `=`
// around either.
func TestDefinitionEffects(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	m := f.s.Mod
	coreRef := func(n expr.Symbol) expr.GlobalRef { return expr.GlobalRef{Mod: "Core", Name: n} }
	call := func(fn any, args ...any) *expr.Expr { return expr.New("call", append([]any{fn}, args...)...) }
	throwErr := call(coreRef("throw"), call(coreRef("ErrorException"), "boom"))
	throwInt := call(coreRef("throw"), int64(1))
	fc := func(rt, at any) *expr.Expr {
		return expr.New("foreigncall", expr.QuoteNode{Value: expr.Symbol("jl_threadid")}, rt, at, int64(0), expr.QuoteNode{Value: expr.Symbol("ccall")})
	}
	run := func(st any, sparams []any) (any, error) {
		src := &expr.CodeInfo{Code: []any{st}}
		out, err := vm.definitionEffects(m, src, sparams, false)
		if err != nil {
			return nil, err
		}
		return out.Code[0], nil
	}
	svec0 := call(coreRef("svec"))
	// a return type and argument types given as expressions are evaluated
	got, err := run(fc(coreRef("Int64"), svec0), nil)
	if err != nil {
		t.Fatal(err)
	}
	e := got.(*expr.Expr)
	if e.Args[1] != any(core.Int64Type) {
		t.Errorf("the return type: %v", e.Args[1])
	}
	if sv, ok := e.Args[2].(SVec); !ok || len(sv) != 0 {
		t.Errorf("the argument types: %v", e.Args[2])
	}
	// types already there leave the statement as it is
	st := fc(core.Int64Type, SVec{})
	if got, _ := run(st, nil); got != any(st) {
		t.Error("a foreigncall of types was changed")
	}
	// inside an assignment, changed and not
	got, err = run(expr.New("=", expr.SlotNumber(1), fc(coreRef("Int64"), SVec{})), nil)
	if err != nil || got.(*expr.Expr).Head != "=" || got.(*expr.Expr).Args[1].(*expr.Expr).Args[1] != any(core.Int64Type) {
		t.Errorf("an assignment of a foreigncall: %v %v", got, err)
	}
	asg := expr.New("=", expr.SlotNumber(1), st)
	if got, _ := run(asg, nil); got != any(asg) {
		t.Error("an assignment of a settled foreigncall was changed")
	}
	// a type that cannot be evaluated: ErrorException names it, any other
	// exception comes out as it is
	_, err = run(fc(throwErr, svec0), nil)
	check(t, "a return type that fails", nil, err, "!could not evaluate ccall return type (it might depend on a local variable)")
	_, err = run(fc(core.Int64Type, throwErr), nil)
	check(t, "argument types that fail", nil, err, "!could not evaluate ccall argument type")
	_, err = run(fc(throwInt, svec0), nil)
	check(t, "a return type that throws an Int", nil, err, "!1")
	// static parameters make the evaluation a preevaluation
	if _, err := run(fc(coreRef("Int64"), svec0), []any{}); err != nil {
		t.Errorf("with static parameters: %v", err)
	}

	// Core.tuple of quoted values, the callee (name, lib)
	tup := call(coreRef("tuple"), expr.QuoteNode{Value: expr.Symbol("f")}, expr.QuoteNode{Value: "lib"})
	got, err = run(tup, nil)
	if tv, ok := got.(*Tuple); err != nil || !ok || len(tv.Elems) != 2 {
		t.Errorf("a quoted tuple: %v %v", got, err)
	}
	for name, c := range map[string]any{
		"an argument that is not quoted": call(coreRef("tuple"), expr.QuoteNode{Value: expr.Symbol("f")}, expr.SlotNumber(1)),
		"another function":               call(coreRef("svec"), expr.QuoteNode{Value: expr.Symbol("f")}),
		"a callee that is no GlobalRef":  call(expr.Symbol("tuple"), expr.QuoteNode{Value: expr.Symbol("f")}),
		"a module nobody has":            call(expr.GlobalRef{Mod: "NoSuchModule", Name: "tuple"}, expr.QuoteNode{Value: expr.Symbol("f")}),
		"a name that is no constant":     call(expr.GlobalRef{Mod: "Core", Name: "Int64"}, expr.QuoteNode{Value: expr.Symbol("f")}),
		"a call of nothing":              expr.New("call"),
	} {
		if got, err := run(c, nil); err != nil || got != c {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	// a Core.tuple that cannot be evaluated in the task's world stays
	if _, err := f.s.Eval("const mytuple = Core.tuple"); err != nil {
		t.Fatal(err)
	}
	late := call(expr.GlobalRef{Mod: m.Name, Name: "mytuple"}, expr.QuoteNode{Value: expr.Symbol("f")})
	if got, err := run(late, nil); err != nil {
		t.Fatal(err)
	} else if _, ok := got.(*Tuple); !ok {
		t.Errorf("a constant holding Core.tuple: %v", got)
	}
	saved := vm.taskWorld
	vm.taskWorld = 1
	if got, err := run(late, nil); err != nil || got != any(late) {
		t.Errorf("a tuple of an older world: %v %v", got, err)
	}
	vm.taskWorld = saved
	// global declarations with binding effects
	src := &expr.CodeInfo{Code: []any{expr.New("global", expr.Symbol("gx"))}}
	out, err := vm.definitionEffects(m, src, nil, true)
	if err != nil || out.Code[0] != nil {
		t.Errorf("global: %v %v", out, err)
	}
}

// The module Base as the bootstrap defines it: the VM's Base replaced, the
// form Compiler.bootstrap!() reduced to activate_codegen!(), `import Base`.
func TestBaseModuleDefinition(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.sessionFor(vm.Main)
	old := vm.Base
	if _, err := s.Eval("module Base\nend"); err != nil {
		t.Fatal(err)
	}
	if vm.Base == old || vm.baseModule == nil || vm.modules["Base"] != vm.Base {
		t.Error("the new Base did not replace the old")
	}
	found := false
	for _, u := range vm.Main.usings {
		found = found || u == vm.Base
	}
	if !found {
		t.Error("Main does not use the new Base")
	}
	// the bootstrap form, in a Base that is being defined
	vm2 := New()
	if err := vm2.Boot(); err != nil {
		t.Fatal(err)
	}
	s2 := vm2.sessionFor(vm2.Main)
	v, err := s2.Eval(`module Base
module Compiler
function activate_codegen!()
    global hits = 1
    nothing
end
function bootstrap!()
    global hits = 100
    nothing
end
end
Compiler.bootstrap!()
end
Base.Compiler.hits`)
	check(t, "Compiler.bootstrap!() as activate_codegen!()", v, err, "1")
	if _, err := s2.Eval("import Base"); err != nil {
		t.Errorf("import Base: %v", err)
	}
	// with the bootstrap asked for
	vm3 := New()
	if err := vm3.Boot(); err != nil {
		t.Fatal(err)
	}
	vm3.RunCompilerBootstrap = true
	v, err = vm3.sessionFor(vm3.Main).Eval(`module Base
module Compiler
function activate_codegen!()
    global hits = 1
    nothing
end
function bootstrap!()
    global hits = 100
    nothing
end
end
Compiler.bootstrap!()
end
Base.Compiler.hits`)
	check(t, "Compiler.bootstrap!() when asked for", v, err, "100")

	for _, c := range []struct {
		form any
		want bool
	}{
		{expr.New("call", expr.New(".", expr.Symbol("Compiler"), expr.QuoteNode{Value: expr.Symbol("bootstrap!")})), true},
		{expr.Symbol("x"), false},
		{expr.New("block"), false},
		{expr.New("call"), false},
		{expr.New("call", expr.Symbol("f")), false},
		{expr.New("call", expr.New(".", expr.Symbol("Compiler"))), false},
		{expr.New("call", expr.New(".", expr.Symbol("Other"), expr.QuoteNode{Value: expr.Symbol("bootstrap!")})), false},
		{expr.New("call", expr.New(".", expr.Symbol("Compiler"), expr.Symbol("bootstrap!"))), false},
		{expr.New("call", expr.New(".", expr.Symbol("Compiler"), expr.QuoteNode{Value: expr.Symbol("other")})), false},
	} {
		if got := isCompilerBootstrap(c.form); got != c.want {
			t.Errorf("isCompilerBootstrap(%v) = %v", c.form, got)
		}
	}
}

// A byte stored in a String the VM made, and refused in one it did not.
func TestStringByteStore(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	str := must(vm.allocString([]any{int64(3)})).(string)
	p := &Pointer{Type: f.u8p, Target: str}
	for _, c := range []struct {
		name string
		p    *Pointer
		x    any
		i    int64
		want string
	}{
		{"a byte", p, uint8('a'), 1, ""},
		{"the last", p, uint8('c'), 3, ""},
		{"past the end", p, uint8('x'), 4, "!invalid pointer"},
		{"before the start", p, uint8('x'), 0, "!invalid pointer"},
		{"an Int", p, int64(1), 2, "!TypeError(:pointerset"},
		{"as another type", &Pointer{Type: vm.ptrType(core.Int8Type), Target: str}, int8(1), 1, "!invalid pointer"},
		{"a String the VM did not make", &Pointer{Type: f.u8p, Target: "literal"}, uint8('x'), 1, "!invalid pointer"},
	} {
		_, err := biPointerset(vm, []any{c.p, c.x, c.i, int64(1)})
		check(t, c.name, nil, err, c.want)
	}
	if str[0] != 'a' || str[2] != 'c' {
		t.Errorf("the String: %q", str)
	}
}

// Small corners: flipsign_int of a negative, n_uninitialized, a function
// whose type is a constructor's, a Module that is no Base, hashes of types.
func TestSmallCorners(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	// julia: flipsign(5, -1) == -5
	v, err := vm.apply(intrinsic(t, vm, "flipsign_int"), []any{int64(5), int64(-1)})
	check(t, "flipsign_int", v, err, "-5")
	v, err = vm.apply(intrinsic(t, vm, "flipsign_int"), []any{int64(5), int64(1)})
	check(t, "flipsign_int", v, err, "5")
	// julia: Int.name.n_uninitialized == 0, RefValue's is 1, an Int32
	v, err = f.s.Eval("getfield(getfield(Int, :name), :n_uninitialized)")
	check(t, "n_uninitialized", v, err, "0")
	// the table of a constructor of a type with no constructor table
	if _, err := f.s.Eval("abstract type NoCtor end"); err != nil {
		t.Fatal(err)
	}
	if tab, _, _, ok := vm.methodTableOf(must(f.s.Eval("Type{NoCtor}"))); !ok || tab != vm.others {
		t.Errorf("the table of Type{NoCtor}: %v %v", tab, ok)
	}
	// a Module that is neither Core, Base nor the method's is a root of the method
	fx := newIRFixture(t)
	fx.vm.baseModule = nil
	data, err := fx.compress(fx.code([]any{fx.b, fx.vm.Core, fx.s.Mod}, int64(3)))
	if err != nil || data == "" {
		t.Fatal(err)
	}
	back, err := fx.uncompress(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, stmts, _ := arrayOf(fx.vm.ciField(back, "code"))
	if stmts[0] != any(fx.b) || stmts[1] != any(fx.vm.Core) || stmts[2] != any(fx.s.Mod) {
		t.Errorf("the modules: %v", stmts)
	}
}
