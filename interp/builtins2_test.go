package interp

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// A TypeName's singletonname, julia 1.12.6's for the same names (of
// Core._structtype(Main, Symbol(s), svec(), svec(), svec(), false, 0)).
func TestSingletonname(t *testing.T) {
	for name, want := range map[string]string{
		"f": "f", "#f": "f", "#f#3": "f", "#f##1#2": "#f##1", "#f##0": "#f##0",
		"#3": "#3", "#3#4": "#3", "##": "#", "#": "", "#f#": "f", "#f##": "f##",
		"#f##x#2": "f##x", "#f##1": "#f##1", "##f#2": "#f#2", "#f#a#b": "f",
		"#promote_typejoin_union": "promote_typejoin_union", "#ab##12": "#ab##12", "": "",
	} {
		if got := demangleTypename(name); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	vm := New()
	tn := core.NewIncompleteType("L", "#g#1", nil, false, false, nil).Name
	if v, err := biGetfield(vm, []any{tn, expr.Symbol("singletonname")}); err != nil || v != expr.Symbol("g") {
		t.Errorf("getfield singletonname: %v %v", v, err)
	}
}

// A TypeName's module is the module that defined the type: a struct, an
// abstract or primitive type, a function's type; Core for core's types.
func TestTypeNameModule(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	mod := func(src string) any {
		t.Helper()
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	for _, src := range []string{
		"struct TnS end\ngetfield(getfield(TnS, :name), :module)",
		"abstract type TnA end\ngetfield(getfield(TnA, :name), :module)",
		"primitive type TnP 8 end\ngetfield(getfield(TnP, :name), :module)",
		"tnf() = 1\ngetfield(getfield(typeof(tnf), :name), :module)",
	} {
		if got := mod(src); got != any(s.Mod) {
			t.Errorf("%s: %v, want the session's module", src, got)
		}
	}
	if got := mod("getfield(getfield(Int64, :name), :module)"); got != any(vm.Core) {
		t.Errorf("Int64's module: %v", got)
	}
	// a type whose module is neither recorded nor known by name
	tn := core.NewIncompleteType("Nowhere", "X", nil, false, false, nil).Name
	if _, err := biGetfield(vm, []any{tn, expr.Symbol("module")}); err == nil {
		t.Error("the module of a type of no known module")
	}
}

// jl_is_const, julia 1.12.6's answers for the same module: a const and a
// function's binding are constants, an explicit import and a using of a
// constant too, as is a name a module's usings bring; a global, a declared
// global and an unknown name are not.
func TestIsConst(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval("const c1 = 1\ng1 = 2\nglobal g2\nfn() = 1\nimport Core: Int64\nusing Core: Int32\nnothing"); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[expr.Symbol]int32{
		"c1": 1, "g1": 0, "g2": 0, "fn": 1, "Int64": 1, "Int32": 1, "Int8": 1, "nope": 0,
	} {
		v, err := vm.shims["jl_is_const"](vm, []any{s.Mod, name})
		if err != nil || v != want {
			t.Errorf("%s: %v %v, want %d", name, v, err, want)
		}
	}
	if _, err := vm.shims["jl_is_const"](vm, []any{int64(1), expr.Symbol("x")}); err == nil {
		t.Error("jl_is_const of a non-module")
	}
	// a constant from a later world on: a guard before it
	late := vm.Main.declare("late", 0)
	late.Const, late.Value, late.World = true, int64(1), vm.taskWorld+1
	if vm.isConst(vm.Main, "late", vm.taskWorld) || !vm.isConst(vm.Main, "late", vm.taskWorld+1) {
		t.Error("a constant of a later world")
	}
}

// rethrow() and rethrow(e) from a catch, as julia 1.12.6 runs the same
// program: the outer catch sees the exception, and the exception stack
// (current_exceptions) holds it once; a throw from a catch stacks both.
func TestRethrow(t *testing.T) {
	vm := New()
	// current_exceptions, read from the test
	vm.shims["test_excstack"] = func(vm *VM, a []any) (any, error) {
		return vm.newTuple(append([]any(nil), vm.excStack...)), nil
	}
	s := vm.NewSession()
	if _, err := s.Eval(`function g(kind)
    try
        try
            throw(1)
        catch
            kind === 1 && ccall(:jl_rethrow, Union{}, ())
            kind === 2 && ccall(:jl_rethrow_other, Union{}, (Any,), 2)
            throw(3)
        end
    catch e
        return (e, ccall(:test_excstack, Any, ()))
    end
end`); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		"g(1)": "(1, (1,))",
		"g(2)": "(2, (2,))",
		"g(3)": "(3, (1, 3))",
		"try ccall(:jl_rethrow, Union{}, ()) catch e; e end":              `ErrorException("rethrow() not allowed outside a catch block")`,
		"try ccall(:jl_rethrow_other, Union{}, (Any,), 5) catch e; e end": `ErrorException("rethrow(exc) not allowed outside a catch block")`,
	} {
		v, err := s.Eval(src)
		if err != nil || Repr(v) != want {
			t.Errorf("%s: %s %v, want %s", src, Repr(v), err, want)
		}
	}
}

// Base.deprecate and Base.isdeprecated, julia 1.12.6's answers for the same
// steps: a deprecation is seen from the next world on, in a partition of
// its own whose flags are 0x60 (with a warning) or 0x20, and is undone by
// flag 0.
func TestDeprecateBinding(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval("x = 1\ny = 2\nnothing"); err != nil {
		t.Fatal(err)
	}
	dep := func(name expr.Symbol, flag int32) {
		t.Helper()
		if _, err := vm.shims["jl_deprecate_binding"](vm, []any{s.Mod, name, flag}); err != nil {
			t.Fatal(err)
		}
	}
	isdep := func(name expr.Symbol) any {
		t.Helper()
		v, err := vm.shims["jl_is_binding_deprecated"](vm, []any{s.Mod, name})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	latest := func() { vm.taskWorld = vm.World() }
	latest()
	if isdep("x") != int32(0) || isdep("nope") != int32(0) {
		t.Fatal("deprecated before deprecate")
	}
	dep("x", 1)
	if isdep("x") != int32(0) {
		t.Error("deprecated in the world before")
	}
	latest()
	if isdep("x") != int32(1) {
		t.Error("not deprecated after")
	}
	p := vm.bindingPartition(s.Mod.own("x"), vm.taskWorld)
	next := p.Fields[3].(*Struct)
	if p.Fields[4].(uint64)&0x70 != 0x60 || p.Fields[2] != ^uint64(0) || next.Fields[4].(uint64)&0x70 != 0 || next.Fields[2].(uint64)+1 != p.Fields[1].(uint64) {
		t.Errorf("partitions %s then %s", showValue(p.Fields), showValue(next.Fields))
	}
	dep("y", 2)
	latest()
	if isdep("y") != int32(1) || vm.bindingPartition(s.Mod.own("y"), vm.taskWorld).Fields[4].(uint64)&0x70 != 0x20 {
		t.Error("deprecated without a warning")
	}
	dep("y", 0)
	latest()
	w := vm.World()
	if dep("y", 0); isdep("y") != int32(0) || vm.World() != w {
		t.Error("undeprecated, then again with no new world")
	}
	if _, err := vm.shims["jl_deprecate_binding"](vm, []any{s.Mod, expr.Symbol("y"), int64(1)}); err == nil {
		t.Error("a flag that is no Cint")
	}
	for _, f := range []expr.Symbol{"jl_deprecate_binding", "jl_is_binding_deprecated"} {
		if _, err := vm.shims[f](vm, []any{int64(1), expr.Symbol("y"), int32(0)}); err == nil {
			t.Errorf("%s of a non-module", f)
		}
	}
}

// fieldoffset, julia 1.12.6's answers for the same types: mpfr.jl's
// BigFloatLayout, padding, a pointer field, a tuple; BoundsError outside
// the fields and for a type with no layout.
func TestFieldOffset(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval(`struct BFL; prec::Int64; sign::Int32; exp::Int64; d::getfield(Core, :Ptr){UInt64}; p::UInt64; end
struct Pad; a::Int8; b::Int64; c::Int16; end
struct WithStr; a::Int8; s::String; end
struct Nest; a::Int8; w::WithStr; end
abstract type Ab end
struct Par{T}; a::Int8; x::T; end
nothing`); err != nil {
		t.Fatal(err)
	}
	ty := func(src string) any {
		t.Helper()
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		t    string
		i    int32
		want string
	}{
		{"BFL", 1, "0x0000000000000000"}, {"BFL", 2, "0x0000000000000008"}, {"BFL", 3, "0x0000000000000010"},
		{"BFL", 4, "0x0000000000000018"}, {"BFL", 5, "0x0000000000000020"},
		{"Pad", 2, "0x0000000000000008"}, {"Pad", 3, "0x0000000000000010"},
		{"WithStr", 2, "0x0000000000000008"},
		{"Tuple{Int8, Int64}", 2, "0x0000000000000008"},
		{"Pad", 0, "!BoundsError(Main.L.Pad, 0)"}, {"Pad", 4, "!BoundsError(Main.L.Pad, 4)"},
		{"Ab", 1, "!BoundsError(Main.L.Ab, 1)"},
		{"Par{Int64}", 2, "0x0000000000000008"},
		// Par's body, whose x is of the free T
		{"getfield(Par, :body)", 2, "!BoundsError(Main.L.Par{T}, 2)"},
		// julia stores the WithStr inline (testdata/layout.golden has more)
		{"Nest", 2, "0x0000000000000008"},
	} {
		v, err := vm.fieldOffset([]any{ty(c.t), c.i})
		check(t, c.t, v, err, c.want)
	}
	for _, a := range [][]any{{int64(1), int32(1)}, {core.Int64Type, int64(1)}} {
		if _, err := vm.fieldOffset(a); err == nil {
			t.Errorf("fieldoffset of %v", a)
		}
	}
	// through the ccall's shim
	v, err := vm.shims["jl_get_field_offset"](vm, []any{ty("Pad"), int32(2)})
	check(t, "the shim", v, err, "0x0000000000000008")
	// an isbits Union field, at its alignment
	if _, err := s.Eval("struct WithU; a::Int8; u::Union{Int8, Int16}; end"); err != nil {
		t.Fatal(err)
	}
	v, err = vm.fieldOffset([]any{ty("WithU"), int32(2)})
	check(t, "an isbits Union field", v, err, "0x0000000000000002")
}

// jl_fl_parse, Core._parse during Base's boot: the answers of julia 1.12.6
// for the same text (testdata/golden/parserule.jsonl), a syntax error's
// message as a String, and jl_fl_parse's own checks (ast.c:848-859).
func TestFlParse(t *testing.T) {
	vm := New()
	text := func(s string) *Pointer { return &Pointer{Type: vm.ptrType(core.UInt8Type), Target: s} }
	call := func(s string, offset uint64, rule string) (any, error) {
		return vm.shims["jl_fl_parse"](vm, []any{text(s), uint64(len(s)), "none", uint64(1), offset, expr.Symbol(rule)})
	}
	for _, c := range []struct {
		s      string
		offset uint64
		rule   string
		want   string
	}{
		{"$(n+1) is $(f(x)[2])", 1, "atom", `svec(Expr(:call, :+, :n, 1), 6)`},
		{"(α, β) = 3, 5", 10, "statement", `svec(Expr(:tuple, 3, 5), 15)`},
		{"   ", 0, "atom", `svec(nothing, 3)`},
		{"(α, β) = 3, 5", 16, "atom", `!BoundsError("(α, β) = 3, 5", 17)`},
		{"x", 0, "toplevel", `!jl_fl_parse: unrecognized parse options`},
		{"x\ny", 1, "all", "!Parse `all`: offset not supported"},
	} {
		v, err := call(c.s, c.offset, c.rule)
		check(t, c.rule+" of "+c.s, v, err, c.want)
	}
	// a syntax error's message is a String, flisp's form, where the parser
	// puts it: the expression, or the last form of a toplevel one
	for _, c := range []struct {
		s, rule string
		at      func(any) any
	}{
		{"x = ", "statement", func(v any) any { return v.(*expr.Expr).Args[0] }},
		{"a\n)\n", "all", func(v any) any { e := v.(*expr.Expr); return e.Args[len(e.Args)-1].(*expr.Expr).Args[0] }},
	} {
		v, err := call(c.s, 0, c.rule)
		if err != nil {
			t.Fatal(err)
		}
		if msg, ok := c.at(v.(SVec)[0]).(string); !ok || !strings.HasPrefix(msg, "ParseError:") {
			t.Errorf("%q: the error's argument is %#v", c.s, c.at(v.(SVec)[0]))
		}
	}
	// nested past the parser's depth limit: a syntax error, not a crash
	deep := strings.Repeat("(", 5000) + "x" + strings.Repeat(")", 5000)
	v, err := call(deep, 0, "atom")
	if err != nil || v.(SVec)[0].(*expr.Expr).Head != "error" || v.(SVec)[1] != int64(0) {
		t.Errorf("past the depth limit: %v %v", v, err)
	}
	// arguments jl_fl_parse cannot have been given
	for _, a := range [][]any{
		{int64(1), uint64(1), "none", uint64(1), uint64(0), expr.Symbol("atom")},
		{text("x"), int64(1), "none", uint64(1), uint64(0), expr.Symbol("atom")},
		{text("x"), uint64(9), "none", uint64(1), uint64(0), expr.Symbol("atom")},
		{text("x"), uint64(1), "none", uint64(1), uint64(0), int64(1)},
	} {
		if _, err := vm.shims["jl_fl_parse"](vm, a); err == nil {
			t.Errorf("jl_fl_parse of %v", a)
		}
	}
	// an Expr with no arguments is left as it is
	if e := expr.New("block"); parseErrorsAsStrings(e) != any(e) {
		t.Error("an empty Expr")
	}
}

// T{x} takes any isbits value of the runtime as it takes an Int
// (jl_valid_type_param, builtins.c:1543): an Int128, a UInt128, a Float16,
// a BFloat16, a pointer (julia 1.12.6: VP{C_NULL} and VP{Float16(1)} are
// types).
func TestValueTypeParams(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	vp, err := s.Eval("struct VP{D}; x::Int; end; VP")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []any{Int128{0, 1}, UInt128{0, 1}, Float16(0x3c00), BFloat16(0x3f80), &Pointer{Type: vm.ptrType(core.NothingType)}} {
		v, err := biApplyType(vm, []any{vp, p})
		if err != nil || v.(*core.DataType).Parameters[0] != p {
			t.Errorf("VP{%s}: %v %v", showValue(p), v, err)
		}
	}
}

// precompile's jl_compile_hint, julia 1.12.6's answers for the same
// methods: 1 where one method matches (the most specific covering a
// concrete query, the others pruned), 0 where none does or an ambiguity
// leaves none; several matches (an abstract or Union query) need
// jl_isa_compileable_sig, refused here where julia answers 0.
func TestCompileHint(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval(`f1(x::Int) = 1
g(x::Int) = 1
g(x::Real) = 2
h(x::Integer, y) = 1
h(x, y::Integer) = 2
k(x::Int8) = 1
k(x::Int16) = 2
nothing`); err != nil {
		t.Fatal(err)
	}
	for src, want := range map[string]string{
		"Tuple{typeof(f1), Int}":               "1",
		"Tuple{typeof(f1), String}":            "0",
		"Tuple{typeof(g), Int}":                "1",
		"Tuple{typeof(g), Float64}":            "1",
		"Tuple{typeof(h), Int, Int}":           "0",
		"Tuple{typeof(f1), T} where T":         "1",
		"Tuple{typeof(g), Real}":               "!not ported yet: jl_compile_hint of",
		"Tuple{typeof(k), Union{Int8, Int16}}": "!not ported yet: jl_compile_hint of",
	} {
		tt, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		v, err := vm.shims["jl_compile_hint"](vm, []any{tt})
		check(t, src, v, err, want)
	}
	// a type with free variables, a bare type variable's tuple; Union{};
	// a callee with no method table; a tuple with no callee
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	for _, c := range []struct {
		tt   any
		want string
	}{
		{must(core.ApplyType(core.AnyTupleType, T)), "0"},
		{core.Bottom, "0"},
		// calling an Int64, which has no method
		{must(core.ApplyType(core.AnyTupleType, core.Int64Type)), "0"},
		// an abstract callee's methods, in no one table here
		{must(core.ApplyType(core.AnyTupleType, core.RealType)), "!not ported yet: jl_compile_hint of the methods of Real"},
		{core.EmptyTupleType, "0"},
		{core.Int64Type, "0"},
	} {
		v, err := vm.shims["jl_compile_hint"](vm, []any{c.tt})
		check(t, showValue(c.tt), v, err, c.want)
	}
	// the type algebra's work bound, struck while matching, is an error
	gt, err := s.Eval("w(x::T, y::T) where {T<:Integer} = 1\nTuple{typeof(w), Int, Int}")
	if err != nil {
		t.Fatal(err)
	}
	saved := core.SetMaxSubtypeSteps(1)
	_, err = vm.shims["jl_compile_hint"](vm, []any{gt})
	core.SetMaxSubtypeSteps(saved)
	check(t, "under the work bound", nil, err, "!exceeded")
	// jl_has_concrete_subtype (gf.c:4932-4942): a Vararg answers for its
	// element type (Any when it has none); a non-DataType is true
	noConcrete := &core.DataType{Name: core.Int64Type.Name}
	for _, c := range []struct {
		t    any
		want bool
	}{
		{&core.Vararg{T: noConcrete}, false}, {&core.Vararg{}, true}, {&core.Vararg{T: core.Int64Type}, true},
		{core.TypeUnion(core.Int8Type, core.Int16Type), true}, {T, true},
	} {
		if got := hasConcreteSubtype(c.t); got != c.want {
			t.Errorf("hasConcreteSubtype(%s) = %v", showValue(c.t), got)
		}
	}
}

// jl_get_module_of_binding, Base.binding_module, julia 1.12.6's answers for
// the same modules: the module itself for its own binding and an unknown
// name, the defining module through an import, an implicit global and an
// implicit constant.
func TestModuleOfBinding(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	if _, err := s.Eval(`module A
export ax, ac
ax = 1
const ac = 2
end
module BM
using ..A
import Core: Int64
c1 = 1
fn() = 1
end`); err != nil {
		t.Fatal(err)
	}
	bm, a := s.Mod.own("BM").Value.(*Module), s.Mod.own("A").Value.(*Module)
	for name, want := range map[expr.Symbol]*Module{
		"c1": bm, "fn": bm, "Int64": vm.Core, "ax": a, "ac": a, "Int8": vm.Core, "nope": bm,
	} {
		p, err := vm.shims["jl_get_module_of_binding"](vm, []any{bm, name})
		if err != nil {
			t.Fatal(err)
		}
		// binding_module reads it back with unsafe_pointer_to_objref
		m, err := vm.shims["jl_value_ptr"](vm, []any{p})
		if err != nil || m != any(want) {
			t.Errorf("binding_module(BM, :%s) = %v %v, want %s", name, m, err, want.Name)
		}
	}
	if _, err := vm.shims["jl_get_module_of_binding"](vm, []any{int64(1), expr.Symbol("x")}); err == nil {
		t.Error("jl_get_module_of_binding of a non-module")
	}
}

// jl_macroexpand, as julia 1.12.6 expands the same macros (show_sexpr):
// what a macro introduces is resolved in its module, a GlobalRef; its local
// is renamed #<n>#t; a form with no macro comes back as it is; a macro's
// error is thrown.
func TestJlMacroexpand(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`module ME
macro m(x)
    quote
        local t = $(Expr(:escape, x))
        t + y
    end
end
macro e() :(error_here(1)) end
macro bad() throw(ErrorException("no")) end
end`); err != nil {
		t.Fatal(err)
	}
	me := s.Mod.own("ME").Value.(*Module)
	expand := func(src string) (any, error) {
		t.Helper()
		x, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return vm.shims["jl_macroexpand"](vm, []any{x, me})
	}
	// (:block, line, (:local, (:(=), #2#t, 1)), line, (:call, Main.ME.:+, #2#t, Main.ME.y))
	v, err := expand(":(ME.@m 1)")
	if err != nil {
		t.Fatal(err)
	}
	var stmts []any
	for _, a := range v.(*expr.Expr).Args {
		if _, line := a.(expr.LineNumberNode); !line {
			stmts = append(stmts, a)
		}
	}
	local := stmts[0].(*expr.Expr).Args[0].(*expr.Expr)
	tname, _ := local.Args[0].(expr.Symbol)
	call := stmts[1].(*expr.Expr)
	plus, y := call.Args[0].(expr.GlobalRef), call.Args[2].(expr.GlobalRef)
	if !strings.HasPrefix(string(tname), "#") || !strings.HasSuffix(string(tname), "#t") || local.Args[1] != int64(1) ||
		call.Args[1] != any(tname) || plus.Name != "+" || y.Name != "y" {
		t.Errorf("@m 1 expanded to %s", showValue(v))
	}
	v, err = expand(":(ME.@e())")
	if err != nil || v.(*expr.Expr).Args[0].(expr.GlobalRef).Name != "error_here" {
		t.Errorf("@e() expanded to %s %v", showValue(v), err)
	}
	v, err = expand(":(1 + 2)")
	check(t, "1 + 2", v, err, "Expr(:call, :+, 1, 2)")
	_, err = expand(":(ME.@bad())")
	check(t, "@bad()", nil, err, `!ErrorException("no")`)
	_, err = vm.shims["jl_macroexpand"](vm, []any{int64(1), int64(2)})
	check(t, "jl_macroexpand in a non-module", nil, err, "!TypeError(:ccall")
}

// A compressed value of a primitive type with no value of its own (a
// parametric primitive with free parameters, which IR never holds) is
// refused, not decoded.
func TestFromImageRefusesAnAbstractPrimitive(t *testing.T) {
	vm := New()
	llvm := core.UnwrapUnionAll(vm.Core.own("LLVMPtr").Value).(*core.DataType)
	d := &irDecoder{vm: vm, b: "\x00\x00\x00\x00\x00\x00\x00\x00", tables: vm.irTagTables()}
	v, err := d.fromImage(llvm)
	check(t, "LLVMPtr{T, AS}", v, err, "!not ported yet: jl_uncompress_ir of a")
}

// The builtins Base's first files call, each want julia 1.12.6's answer to
// the same call.
func TestBuiltins2(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval("f(x::Integer) = 1; f(x::Int) = 2; g(x::T) where {T} = T; struct P1; a::Int; end"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"Core.compilerbarrier(:type, 3)", "3"},
		{"Core._svec_len(Core.svec(1, 2))", "2"},
		{"Core._svec_ref(Core.svec(1, 2), 2)", "2"},
		{"isdefined(Core, :f)", "true"},
		{"isdefined(Core, :nope)", "false"},
		{"isdefined((1, 2), 2)", "true"},
		{"isdefined((1, 2), 3)", "false"},
		{"invoke(f, Tuple{Integer}, 1)", "1"},
		{"invoke(g, Tuple{Any}, 1)", "Int64"},
		{"applicable(f, 1)", "true"},
		{"applicable(f, 1.0)", "false"},
		{"applicable(tuple, 1)", "true"},
		{"(applicable(P1, 1), applicable(P1, :a), applicable(P1))", "(true, true, false)"},
		{"Core.current_scope()", "nothing"},
		{"try Core.compilerbarrier(:x, 1) catch e; e end", "ErrorException(\"The first argument of `compilerbarrier` must be either of `:type`, `:const` or `:conditional`.\")"},
		{"try Core._svec_ref(Core.svec(1), 2) catch e; e end", "BoundsError(svec(1), 2)"},
		{"try invoke(f, Tuple{Integer}, 1.0) catch e; e end", "!argument type error"},
		{"try invoke(f, Int, 1) catch e; e end", `TypeError(:invoke, "", Type{T} where T<:Tuple, Int64)`},
		{"try invoke(f, Tuple{AbstractFloat}, 1.0) catch e; e end", "!MethodError(f, Tuple{AbstractFloat}"},
		{"try isdefined(Core, :f, :not_atomic) catch e; e end", `ConcurrencyViolationError("isdefined: module binding cannot be accessed non-atomically")`},
		{"try isdefined((1,), 1, :monotonic) catch e; e end", `ConcurrencyViolationError("isdefined: non-atomic field cannot be accessed atomically")`},
		{"try Core.throw_methoderror(f, 1.0) catch e; e end", "!MethodError(f, (1.0,)"},
	} {
		v, err := s.Eval(c.src)
		if len(c.want) > 0 && c.want[0] == '!' {
			// julia's answer contains the world, which differs: compare a prefix
			check(t, c.src, v, err, "")
			if got := Repr(v); err == nil && !strings.Contains(got, c.want[1:]) {
				t.Errorf("%s: got %s, want it to contain %s", c.src, got, c.want[1:])
			}
			continue
		}
		check(t, c.src, v, err, c.want)
	}

	// the refusals and the cases julia reaches only through other paths
	st, _ := vm.newStruct(vm.tBounds, []any{int64(1)})
	for _, c := range []struct {
		name string
		f    func(*VM, []any) (any, error)
		args []any
		want string
	}{
		{"compilerbarrier arity", biCompilerbarrier, nil, "!compilerbarrier: too few arguments"},
		{"compilerbarrier of a non-symbol", biCompilerbarrier, []any{int64(1), int64(1)}, "!TypeError(:compilerbarrier"},
		{"_svec_len arity", biSvecLen, nil, "!_svec_len: too few arguments"},
		{"_svec_len of a non-svec", biSvecLen, []any{int64(1)}, "!TypeError(:_svec_len"},
		{"_svec_ref arity", biSvecRef, nil, "!_svec_ref: too few arguments"},
		{"_svec_ref of a non-svec", biSvecRef, []any{int64(1), int64(1)}, "!TypeError(:_svec_ref"},
		{"_svec_ref of a non-Int", biSvecRef, []any{SVec{}, "1"}, "!TypeError(:_svec_ref"},
		{"isdefined arity", biIsdefined, nil, "!isdefined: too few arguments"},
		{"isdefined of a non-symbol order", biIsdefined, []any{st, int64(1), int64(1)}, "!TypeError(:isdefined"},
		{"isdefined of a bad order", biIsdefined, []any{st, int64(1), expr.Symbol("bogus")}, "!invalid atomic ordering"},
		{"isdefined of a module's non-symbol", biIsdefined, []any{vm.Core, int64(1)}, "!TypeError(:isdefined"},
		{"isdefined of a set field", biIsdefined, []any{st, expr.Symbol("a")}, "true"},
		{"isdefined of an unset field", biIsdefined, []any{st, int64(2)}, "false"},
		{"isdefined of no field, ordered", biIsdefined, []any{st, int64(9), expr.Symbol("monotonic")}, "!atomic ordering cannot be specified for nonexistent field"},
		{"isdefined of a field by a bad key", biIsdefined, []any{st, 1.5}, "!TypeError(:isdefined"},
		{"isdefined of an AST field", biIsdefined, []any{expr.QuoteNode{Value: int64(1)}, expr.Symbol("value")}, "true"},
		{"isdefined of a field not_atomic", biIsdefined, []any{st, int64(1), expr.Symbol("not_atomic")}, "true"},
		{"throw_methoderror arity", biThrowMethoderror, nil, "!throw_methoderror: too few arguments"},
		{"current_scope arity", biCurrentScope, []any{int64(1)}, "!current_scope: too many arguments"},
		{"finalizer", biFinalizer, []any{int64(1), int64(2)}, "nothing"},
		{"finalizer arity", biFinalizer, nil, "!finalizer: too few arguments"},
		{"donotdelete", biDonotdelete, []any{int64(1)}, "nothing"},
		{"_call_in_world_total", biCallInWorldTotal, []any{uint64(1), coreValue(t, vm, "tuple"), int64(1)}, "(1,)"},
		{"_call_in_world_total arity", biCallInWorldTotal, nil, "!_call_in_world_total: too few arguments"},
		{"_call_in_world_total of a non-UInt", biCallInWorldTotal, []any{int64(1), int64(1)}, "!TypeError(:_call_in_world_total"},
		{"invoke arity", biInvoke, nil, "!invoke: too few arguments"},
		{"invoke of a non-function", biInvoke, []any{int64(1), core.EmptyTupleType}, "!not ported yet: invoke of a Int64"},
		{"applicable arity", biApplicable, nil, "!applicable: too few arguments"},
		{"applicable of a type", biApplicable, []any{vm.tBounds, int64(1)}, ""},
		{"applicable of an Int", biApplicable, []any{int64(1)}, "false"},
		{"applicable of an intrinsic", biApplicable, []any{intrinsic(t, vm, "add_int")}, "true"},
	} {
		v, err := c.f(vm, c.args)
		check(t, c.name, v, err, c.want)
	}

	// a dispatch past the subtype work bound is a Go error, never an answer:
	// swept so that some bound stops the lookup and some the static
	// parameters of the actual arguments, deeper than the declared tuple
	g := coreValue(t, vm, "g")
	anyT := must(core.Apply(core.AnyTupleType, core.AnyType))
	for n := 1; n < 40; n++ {
		old := core.SetMaxSubtypeSteps(n)
		_, err1 := biInvoke(vm, []any{g, anyT, int64(1)})
		_, err2 := biApplicable(vm, []any{g, int64(1)})
		core.SetMaxSubtypeSteps(old)
		var thrown *Thrown
		for _, e := range []error{err1, err2} {
			if e != nil && errors.As(e, &thrown) {
				t.Fatalf("bound %d: a Julia exception: %v", n, e)
			}
		}
	}
	// the static parameters of the actual arguments, past the bound
	m := g.(*Function).Table.Methods(vm.World())[0]
	old := core.SetMaxSubtypeSteps(1)
	_, err := vm.invokeMethod(m, g, []any{int64(1)})
	core.SetMaxSubtypeSteps(old)
	if err == nil {
		t.Error("invokeMethod past the subtype work bound")
	}
}
