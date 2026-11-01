package interp

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

func coreValue(t *testing.T, vm *VM, name expr.Symbol) any {
	t.Helper()
	b := vm.Core.own(name)
	if b == nil {
		t.Fatalf("Core.%s is not defined", name)
	}
	return b.Value
}

func intrinsic(t *testing.T, vm *VM, name expr.Symbol) any {
	t.Helper()
	return vm.modules["Intrinsics"].own(name).Value
}

// The builtins' argument checks, each answered as builtins.c answers.
func TestBuiltins(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	tup := vm.newTuple([]any{int64(1), int64(2)})
	ex, _ := vm.newStruct(vm.tError, []any{"m"})
	mut, _ := core.NewType("Main", "Mut", nil, false, true, []expr.Symbol{"x"}, []any{core.AnyType})
	m1, m2 := &Struct{Type: mut, Fields: []any{int64(1)}}, &Struct{Type: mut, Fields: []any{int64(1)}}
	partial, _ := vm.newStruct(vm.tBounds, []any{int64(1)})
	cases := []struct {
		name string
		f    expr.Symbol
		args []any
		want string
	}{
		{"=== arity", "===", []any{int64(1)}, "!===: too few arguments (expected 2)"},
		{"=== of distinct tuples", "===", []any{tup, vm.newTuple([]any{int64(1), uint8(2)})}, "false"},
		{"=== of tuple elements", "===", []any{tup, vm.newTuple([]any{int64(1), int64(3)})}, "false"},
		{"=== of immutable structs", "===", []any{ex, &Struct{Type: vm.tError, Fields: []any{"m"}}}, "true"},
		{"=== of immutable fields", "===", []any{ex, &Struct{Type: vm.tError, Fields: []any{"n"}}}, "false"},
		{"=== of a struct and a tuple", "===", []any{ex, tup}, "false"},
		{"=== of mutable structs", "===", []any{m1, m2}, "false"},
		{"=== of the same mutable", "===", []any{m1, m1}, "true"},
		{"=== of svecs", "===", []any{SVec{int64(1)}, SVec{int64(1)}}, "true"},
		{"=== of svec lengths", "===", []any{SVec{int64(1)}, SVec{}}, "false"},
		{"=== of svec elements", "===", []any{SVec{int64(1)}, SVec{int64(2)}}, "false"},
		{"=== of modules", "===", []any{vm.Core, vm.Core}, "true"},
		{"=== of an int and a module", "===", []any{int64(1), vm.Core}, "false"},
		{"typeof arity", "typeof", nil, "!typeof: too few arguments (expected 1)"},
		{"typeof of a builtin", "typeof", []any{coreValue(t, vm, "tuple")}, "#tuple"},
		{"typeof of a module", "typeof", []any{vm.Core}, "Module"},
		{"Typeof of a type", "Typeof", []any{core.Int64Type}, "Type{Int64}"},
		{"Typeof of a value", "Typeof", []any{int64(1)}, "Int64"},
		{"typeof too many", "typeof", []any{int64(1), int64(2)}, "!typeof: too many arguments (expected 1)"},
		{"isa arity", "isa", []any{int64(1)}, "!isa: too few arguments"},
		{"isa of a Go value", "isa", []any{float64(1), core.AbstractFloatType}, "true"},
		{"<: arity", "<:", []any{core.AnyType}, "!<:: too few arguments"},
		{"<: of a non-type", "<:", []any{int64(1), core.AnyType}, "!TypeError(:<:"},
		{"typeassert arity", "typeassert", []any{int64(1)}, "!typeassert: too few arguments"},
		{"typeassert of a non-type", "typeassert", []any{int64(1), int64(2)}, "!TypeError(:typeassert"},
		{"throw arity", "throw", nil, "!throw: too few arguments"},
		{"ifelse arity", "ifelse", []any{true}, "!ifelse: too few arguments"},
		{"ifelse true", "ifelse", []any{true, int64(1), int64(2)}, "1"},
		{"ifelse false", "ifelse", []any{false, int64(1), int64(2)}, "2"},
		{"ifelse of a non-Bool", "ifelse", []any{int64(1), int64(1), int64(2)}, "!TypeError(:ifelse, \"\", Bool, 1)"},
		{"getfield arity", "getfield", []any{tup}, "!getfield: too few arguments"},
		{"getfield of a module", "getfield", []any{vm.Core, expr.Symbol("Int")}, "Int64"},
		{"getfield of a struct by name", "getfield", []any{ex, expr.Symbol("msg")}, "\"m\""},
		{"getfield of a struct by index", "getfield", []any{ex, int64(1)}, "\"m\""},
		{"getfield of an unknown field", "getfield", []any{ex, expr.Symbol("nope")}, "!type ErrorException has no field nope"},
		{"getfield of an index out of range", "getfield", []any{ex, int64(2)}, "!BoundsError"},
		{"getfield of an undefined field", "getfield", []any{partial, int64(2)}, "!UndefRefError()"},
		{"getfield of an Int", "getfield", []any{int64(1), int64(1)}, "!BoundsError(1, 1)"},
		{"nfields arity", "nfields", nil, "!nfields: too few arguments"},
		{"nfields of a struct", "nfields", []any{ex}, "1"},
		{"nfields of an Int", "nfields", []any{int64(1)}, "0"},
		{"getglobal", "getglobal", []any{vm.Core, expr.Symbol("Int")}, "Int64"},
		{"getglobal arity", "getglobal", []any{vm.Core}, "!getglobal: too few arguments"},
		{"getglobal of a non-module", "getglobal", []any{int64(1), expr.Symbol("x")}, "!TypeError(:getglobal, \"\", Module, 1)"},
		{"getglobal of a non-symbol", "getglobal", []any{vm.Core, int64(1)}, "!TypeError(:getglobal, \"\", Symbol, 1)"},
		{"setglobal! arity", "setglobal!", []any{s.Mod}, "!setglobal!: too few arguments"},
		{"setglobal! of a non-module", "setglobal!", []any{int64(1), expr.Symbol("x"), int64(1)}, "!TypeError(:setglobal!"},
		{"setglobal! of an imported name", "setglobal!", []any{s.Mod, expr.Symbol("tuple"), int64(1)}, "!cannot assign a value to imported variable L.tuple"},
		{"setglobal! of an undeclared name", "setglobal!", []any{s.Mod, expr.Symbol("zz"), int64(1)}, "!Global L.zz does not exist and cannot be assigned."},
		{"isdefinedglobal", "isdefinedglobal", []any{vm.Core, expr.Symbol("Int")}, "true"},
		{"isdefinedglobal of a missing name", "isdefinedglobal", []any{vm.Core, expr.Symbol("nope")}, "false"},
		{"isdefinedglobal arity", "isdefinedglobal", []any{vm.Core}, "!isdefined: too few arguments"},
		{"isdefinedglobal of a non-module", "isdefinedglobal", []any{int64(1), expr.Symbol("x")}, "!TypeError(:isdefined"},
		{"get_binding_type", "get_binding_type", []any{vm.Core, expr.Symbol("Int")}, "Any"},
		{"get_binding_type arity", "get_binding_type", []any{vm.Core}, "!get_binding_type: too few arguments"},
		{"get_binding_type of a non-module", "get_binding_type", []any{int64(1), expr.Symbol("x")}, "!TypeError(:get_binding_type"},
		{"apply_type", "apply_type", []any{core.ValType, int64(1)}, ""},
		{"apply_type arity", "apply_type", nil, "!apply_type: too few arguments"},
		{"apply_type of a non-type", "apply_type", []any{int64(1), int64(2)}, "!ErrorException("},
	}
	for _, c := range cases {
		v, err := vm.apply(coreValue(t, vm, c.f), c.args)
		check(t, c.name, v, err, c.want)
	}
	if v, err := vm.apply(coreValue(t, vm, "svec"), []any{int64(1)}); err != nil || Repr(v) != "svec(1)" {
		t.Errorf("svec: %s, %v", Repr(v), err)
	}
}

// The intrinsics on every integer width and on Float32, through their Go
// representations.
func TestIntrinsicsWidths(t *testing.T) {
	vm := New()
	cases := []struct {
		f    expr.Symbol
		args []any
		want string
	}{
		{"add_int", []any{int8(127), int8(1)}, "-128"},
		{"add_int", []any{int16(1), int16(2)}, "3"},
		{"add_int", []any{int32(1), int32(2)}, "3"},
		{"add_int", []any{uint16(0xffff), uint16(1)}, "0x0000"},
		{"add_int", []any{uint32(1), uint32(2)}, "0x00000003"},
		{"add_int", []any{uint64(1), uint64(2)}, "0x0000000000000003"},
		{"add_int", []any{true, true}, "false"},
		{"add_int", []any{false, true}, "true"},
		{"add_int", []any{expr.Char(1), expr.Char(2)}, ""},
		{"slt_int", []any{int8(-1), int8(1)}, "true"},
		{"slt_int", []any{int64(1), uint8(1)}, "!slt_int: types of a and b must match"},
		{"neg_int", []any{"s"}, "!neg_int: value is not a primitive type"},
		{"checked_sdiv_int", []any{int64(1), int8(1)}, "!checked_sdiv_int: types of a and b must match"},
		{"checked_udiv_int", []any{uint8(1), uint8(0)}, "!DivideError()"},
		{"checked_urem_int", []any{uint8(1), uint8(0)}, "!DivideError()"},
		{"checked_srem_int", []any{int64(1), int64(0)}, "!DivideError()"},
		{"sdiv_int", []any{int8(-128), int8(-1)}, "-128"},
		{"urem_int", []any{uint8(7), uint8(2)}, "0x01"},
		{"udiv_int", []any{uint8(7), uint8(2)}, "0x03"},
		{"add_float", []any{float32(1.5), float32(2.25)}, ""},
		{"add_float", []any{float32(1), float64(1)}, "!add_float: types of a and b must match"},
		{"add_float", []any{expr.Symbol("a"), expr.Symbol("b")}, "!add_float: values are not primitive types"},
		{"lt_float", []any{float32(1), float64(1)}, "!lt_float: types of a and b must match"},
		{"neg_float", []any{float32(1)}, ""},
		{"neg_float", []any{expr.Symbol("a")}, "!neg_float_withtype: value is not a primitive type"},
	}
	for _, c := range cases {
		v, err := vm.apply(intrinsic(t, vm, c.f), c.args)
		check(t, string(c.f), v, err, c.want)
	}
	if v, _ := vm.apply(intrinsic(t, vm, "add_float"), []any{float32(1.5), float32(2.25)}); v != float32(3.75) {
		t.Errorf("Float32 add: %v", v)
	}
	if fromBits(float64(0), 7) != uint64(7) {
		t.Error("fromBits of a non-integer kept the bits")
	}
}

// Values are shown as julia's repr shows them.
func TestShow(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	partial, _ := vm.newStruct(vm.tBounds, []any{int64(1)})
	fn, _ := vm.declareFunction(s.Mod, "g")
	vecT := core.ArrayType.Body.(*core.UnionAll).Body.(*core.DataType)
	nonWrapper := core.TypeUnionAll(core.ArrayType.Var, vecT)
	for v, want := range map[any]string{
		int8(-1): "-1", int16(2): "2", int32(3): "3", uint16(1): "0x0001", uint32(1): "0x00000001",
		uint64(1): "0x0000000000000001", math.Inf(-1): "-Inf", math.NaN(): "NaN", math.Inf(1): "Inf",
		1.0e6: "1.0e6", 1.0e-5: "1.0e-5", 100000.0: "100000.0", 2.0: "2.0",
		partial: "BoundsError(1, #undef)", fn: "g", coreValue(t, vm, "tuple"): "tuple",
		intrinsic(t, vm, "add_int"): "Core.Intrinsics.add_int", vm.modules["Intrinsics"]: "Core.Intrinsics",
		s.Mod: "Main.L", vm.Core: "Core", core.TypeType: "Type", struct{ x int }{1}: "{1}",
	} {
		if got := Repr(v); got != want {
			t.Errorf("Repr(%#v) = %s, want %s", v, got, want)
		}
	}
	if got := Repr(SVec{int64(1), int64(2)}); got != "svec(1, 2)" {
		t.Errorf("svec: %s", got)
	}
	if got := Repr(nonWrapper); !strings.Contains(got, "where") {
		t.Errorf("a UnionAll that is not a wrapper: %s", got)
	}
}

// Static parameters reach a method body through its frame.
func TestStaticParameters(t *testing.T) {
	vm := New()
	s := vm.NewSession()
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	for _, c := range []struct {
		name    string
		sparams []any
		code    []any
		want    string
	}{
		{"value", []any{core.Int64Type}, []any{expr.New("static_parameter", int64(1)), ret(expr.SSAValue(1))}, "Int64"},
		{"unbound", []any{T}, []any{expr.New("static_parameter", int64(1))}, "!UndefVarError(:T,"},
		{"isdefined of a value", []any{core.Int64Type}, []any{expr.New("isdefined", expr.New("static_parameter", int64(1))), ret(expr.SSAValue(1))}, "true"},
		{"isdefined of a variable", []any{T}, []any{expr.New("isdefined", expr.New("static_parameter", int64(1))), ret(expr.SSAValue(1))}, "false"},
	} {
		code, err := prepare(codeInfo(c.code), false)
		if err != nil {
			t.Fatal(err)
		}
		f := vm.newFrame(code, s.Mod)
		f.sparams = c.sparams
		v, err := f.evalBody(0)
		check(t, c.name, v, err, c.want)
	}
}

// The checks of jl_method_def and of the calls it enables.
func TestMethodDefinition(t *testing.T) {
	body := codeInfo([]any{ret(int64(1))}, "#self#")
	// %3 is typeof(f), %4 the signature, as lowering lays them out
	def := func(atypes any, meth any) []any {
		return []any{
			expr.New("method", gr("L", "f")),
			expr.New("latestworld"),
			call(gr("Core", "Typeof"), expr.SSAValue(1)),
			atypes,
			expr.New("method", gr("L", "f"), expr.SSAValue(4), meth),
			ret(nil),
		}
	}
	svec := func(a ...any) *expr.Expr { return call(append([]any{gr("Core", "svec")}, a...)...) }
	tvT := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	for _, c := range []struct {
		name string
		code []any
		want string
	}{
		{"atypes too short", def(svec(), body), "!method: invalid declaration"},
		{"argtypes not an svec", def(svec(int64(1), svec()), body), "!method: invalid declaration"},
		{"no argtypes", def(svec(svec(), svec()), body), "!method: invalid declaration"},
		{"not a function", def(svec(svec(gr("Core", "Int")), svec()), body), "!not ported"},
		{"body not a CodeInfo", def(svec(svec(expr.SSAValue(3)), svec()), int64(1)), "!method: invalid declaration"},
		{"Vararg not last", def(svec(svec(expr.SSAValue(3), gr("Core", "Vararg"), gr("Core", "Int")), svec()), body), "!ErrorException("},
		{"tvar not a TypeVar", def(svec(svec(expr.SSAValue(3)), svec(int64(1))), body), "!TypeError(:method signature"},
		{"body that does not prepare", def(svec(svec(expr.SSAValue(3)), svec()), &expr.CodeInfo{SSAValueTypes: "x"}), "!not ported"},
		{"defined", def(svec(svec(expr.SSAValue(3)), svec()), body), "nothing"},
		{"defined with a static parameter", def(svec(svec(expr.SSAValue(3), expr.QuoteNode{Value: tvT}),
			svec(expr.QuoteNode{Value: tvT})), codeInfo([]any{ret(int64(1))}, "#self#", "x")), "nothing"},
		{"function error", []any{expr.New("method", expr.SSAValue(9), int64(1), int64(1))}, "!access to invalid SSAValue"},
		{"atypes error", []any{expr.New("method", gr("Core", "Int"), expr.SSAValue(9), int64(1))}, "!access to invalid SSAValue"},
		{"body error", []any{expr.New("method", gr("Core", "Int"), int64(1), expr.SSAValue(9))}, "!access to invalid SSAValue"},
	} {
		s := New().NewSession()
		v, err := runIR(s, codeInfo(c.code), true)
		check(t, c.name, v, err, c.want)
	}

	// a binding that holds something else cannot become a function
	s := New().NewSession()
	if _, err := s.Eval("q = 1"); err != nil {
		t.Fatal(err)
	}
	_, err := s.VM.declareFunction(s.Mod, "q")
	check(t, "redefine a value", nil, err, "!cannot define function q; it already has a value")

	// calling a value that is not a function, and a method without a body
	_, err = s.VM.apply(int64(1), nil)
	check(t, "call an Int", nil, err, "!MethodError(1, ()")
	fv, _ := s.VM.declareFunction(s.Mod, "nb")
	fn := fv.(*Function)
	if _, err := fn.Table.Define(core.EmptyTupleType, s.VM.newWorld(), nil); err != nil {
		t.Fatal(err)
	}
	s.VM.taskWorld = s.VM.World()
	_, err = s.VM.apply(fn, nil)
	check(t, "method without a body", nil, err, "!not ported")

	// a dispatch that exceeds the subtype work bound is a Go error, not a
	// MethodError: Tuple{T} where T needs the full algorithm
	hv, _ := s.VM.declareFunction(s.Mod, "h")
	T := &core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType}
	sig, _ := core.Apply(core.AnyTupleType, T)
	if _, err := hv.(*Function).Table.Define(core.TypeUnionAll(T, sig), s.VM.newWorld(), nil); err != nil {
		t.Fatal(err)
	}
	s.VM.taskWorld = s.VM.World()
	old := core.SetMaxSubtypeSteps(1)
	defer core.SetMaxSubtypeSteps(old)
	_, err = s.VM.apply(hv, []any{int64(1)})
	var thrown *Thrown
	if err == nil || errors.As(err, &thrown) {
		t.Errorf("work bound: %v", err)
	}
}

// A struct is allocated as jl_new_structv allocates it.
func TestNewStruct(t *testing.T) {
	vm := New()
	for _, c := range []struct {
		name string
		t    any
		args []any
		want string
	}{
		{"not a type", int64(1), nil, "!TypeError(:new, \"\", DataType, 1)"},
		{"abstract", core.IntegerType, nil, "!TypeError(:new"},
		{"tuple type", core.EmptyTupleType, nil, "!not ported"},
		{"primitive type", core.Int64Type, nil, "!not ported"},
		{"too many fields", vm.tError, []any{"a", "b"}, "!invalid struct allocation"},
		{"field of the wrong type", vm.tError, []any{int64(1)}, "!TypeError(:new, \"\", AbstractString, 1)"},
		{"singleton", vm.tDivide, nil, "DivideError()"},
	} {
		v, err := vm.newStruct(c.t, c.args)
		check(t, c.name, v, err, c.want)
	}
	mut, _ := core.NewType("Main", "M2", nil, false, true, []expr.Symbol{"x"}, []any{core.Int64Type})
	_, err := vm.newStruct(mut, []any{"s"})
	check(t, "a mutable struct's field of the wrong type", nil, err, "!TypeError(:new, \"\", Int64, \"s\")")
}

// Global assignment and declaration as module.c answers them.
func TestGlobals(t *testing.T) {
	s := New().NewSession()
	for _, c := range []struct{ src, want string }{
		{"import Core.Intrinsics: add_int\nadd_int = 1", "!cannot assign a value to imported variable L.add_int"},
		{"const k = 1\nk = 2", "!invalid assignment to constant L.k."},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
	err := s.VM.declareConst(gr("Nope", "c"), int64(1))
	check(t, "const of an unknown module", nil, err, "!not ported")
}

// The top level: syntax errors, macros, nested toplevel blocks, import and
// using forms.
func TestToplevel(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"1 +", "!syntax:"},
		{"@m x", "!UndefVarError(:@m,"},
		{strings.Repeat("(", 5000) + "1" + strings.Repeat(")", 5000), "!syntax:"},
		{"import Core.Intrinsics\nIntrinsics", "Core.Intrinsics"},
		{"using Core.Intrinsics\nadd_int", "Core.Intrinsics.add_int"},
		{"import Core.Intrinsics\nimport Intrinsics: add_int\nadd_int", "Core.Intrinsics.add_int"},
		{"import Core.Intrinsics: nope", "!UndefVarError(:nope,"},
		{"import Core.Nope", "!UndefVarError(:Nope,"},
		{"import Core.Int", "!invalid import path: \\\"Int\\\" does not name a module"},
		{"import Pkg", "!not ported yet: loading the package Pkg"},
		{"import .Rel", "!not ported yet: a relative import path"},
		{"import Core.Intrinsics: add_int as plus", "!not ported yet: the import item"},
		{"import Core.Intrinsics as CI", "!not ported yet: the import form"},
		{"export q\nq", "!UndefVarError(:q,"},
		{"Core.Int", "!UndefVarError(:getproperty,"},
	} {
		v, err := New().NewSession().Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	vm := New()
	s := vm.NewSession()
	for _, c := range []struct {
		name string
		e    any
		want string
	}{
		{"GlobalRef", gr("Core", "Int"), "Int64"},
		{"GlobalRef of an unknown module", gr("Nope", "x"), "!not ported"},
		{"Symbol", expr.Symbol("nothing"), "nothing"},
		{"line number", expr.LineNumberNode{Line: 1}, "nothing"},
		{"literal", int64(3), "3"},
		{"malformed thunk", expr.New("thunk", int64(1)), "!malformed \\\"thunk\\\" statement"},
		{"thunk that does not prepare", expr.New("thunk", &expr.CodeInfo{SSAValueTypes: "x"}), "!not ported"},
		{"error form", expr.New("error", "bad"), "!syntax: bad"},
		{"error form without a message", expr.New("error"), "!malformed \\\"error\\\" expression"},
		{"error form of a ParseError", expr.New("incomplete", &expr.ParseError{Msg: "eof"}), "!syntax: eof"},
		{"error form of a value", expr.New("error", int64(5)), "!julia exception: 5"},
		{"nested toplevel", expr.New("toplevel", int64(1), int64(2)), "2"},
		{"nested toplevel error", expr.New("toplevel", gr("Nope", "x")), "!not ported"},
		{"using of a non-path", expr.New("using", int64(1)), "!not ported"},
		{"import with a short colon", expr.New("import", expr.New(":", expr.New(".", expr.Symbol("Core")))), "!syntax: malformed"},
		{"import from a bad path", expr.New("import", expr.New(":", int64(1), expr.New(".", expr.Symbol("x")))), "!syntax: malformed"},
		{"import of a non-symbol", expr.New("import", expr.New(":", expr.New(".", expr.Symbol("Core")), expr.New(".", int64(1)))), "!syntax: malformed"},
		{"import of an empty path", expr.New("import", expr.New(".")), "!syntax: malformed"},
		{"import from a bad first path", expr.New("import", expr.New(".", expr.Symbol("Core")), expr.New(".", expr.Symbol("Nope"))), "!not ported"},
	} {
		v, err := vm.toplevelEval(s.Mod, c.e)
		check(t, c.name, v, err, c.want)
	}

	// a lowering failure comes back as the lowering's error
	gotoFloat := expr.New("function", call(expr.Symbol("f")), expr.New("block", expr.New("symbolicgoto", 1.5)))
	if _, err := s.evalForm(gotoFloat); err == nil {
		t.Error("a form lowering rejects was evaluated")
	}
	if _, err := s.evalForm(expr.New("incomplete", "eof")); err == nil {
		t.Error("an incomplete form was evaluated")
	}
	if firstArg(expr.New("thunk")) != nil {
		t.Error("firstArg of no arguments")
	}
}

// A VM refuses what it cannot represent instead of guessing.
func TestVMRefuses(t *testing.T) {
	vm := New()
	for name, v := range map[string]any{"int8": int8(1), "int16": int16(1), "int32": int32(1), "uint16": uint16(1),
		"uint32": uint32(1), "uint64": uint64(1), "float32": float32(1), "char": expr.Char(1),
		"svec": SVec{}, "intrinsic": intrinsic(t, vm, "add_int"),
		"line": expr.LineNumberNode{Line: 1}, "globalref": gr("Core", "Int")} {
		if vm.typeOf(v) == nil {
			t.Errorf("typeOf(%s)", name)
		}
	}
	if !panics(func() { vm.typeOf(struct{}{}) }) {
		t.Error("typeOf of a Go value without a Julia type did not panic")
	}
	if !panics(func() { mustType("Core", "X", core.Int64Type, false, nil, nil) }) {
		t.Error("mustType of an invalid supertype did not panic")
	}
	if !panics(func() { (&frame{vm: vm}).evalValue(struct{}{}) }) {
		t.Error("an unknown operand did not panic")
	}
	if copyAST(int64(1)) != int64(1) {
		t.Error("copyAST of a non-Expr")
	}
	if b := vm.Core.declare("Int", 9); b.World != 0 {
		t.Error("declare replaced an existing binding")
	}
	if slotNumber(int64(1)) != 0 {
		t.Error("slotNumber of a non-slot")
	}
	if (&Thrown{Value: int64(1)}).Error() != "julia exception: 1" {
		t.Error("Thrown.Error")
	}
}

func panics(f func()) (p bool) {
	defer func() { p = recover() != nil }()
	f()
	return false
}
