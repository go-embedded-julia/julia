package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// What the cases cannot reach: the helpers on their own, a builtin method,
// a function whose max_args is below its methods', and the refusals.
func TestCompilationSigUnits(t *testing.T) {
	vm, s := compilesigVM(t)
	typ := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v
	}
	if varargT(&core.Vararg{}) != any(core.AnyType) || varargT(&core.Vararg{T: core.Int64Type}) != any(core.Int64Type) {
		t.Error("jl_unwrap_vararg")
	}
	if vaTupleKind(core.EmptyTupleType) != vaNone {
		t.Error("the Vararg kind of Tuple{}")
	}
	// a Vararg of a given length: Tuple types expand it, so one is made by hand
	withN := &core.DataType{Name: core.TupleTypeName, Parameters: []any{core.Int64Type, must(core.WrapVararg(core.Int64Type, int64(2)))}}
	if vaTupleKind(withN) != vaInt {
		t.Error("the Vararg kind of a Vararg{Int,2}")
	}

	// inst_varargp_in_env with a TypeVar among the static parameters
	v4 := vm.lookupMatch(t, typ("Tuple{typeof(v4), Int, Int}")).Fields[2].(*Struct)
	tv := typ("Core.TypeVar(:S)")
	va, ok := instVarargpInEnv(v4.Fields[methodSig], SVec{tv}).(*core.Vararg)
	if !ok || !core.TypesEqual(va.T, core.TypeUnionAll(tv.(*core.TypeVar), tv)) {
		t.Errorf("a Vararg over an unbound variable: %s", showValue(va))
	}
	v9 := vm.lookupMatch(t, typ("Tuple{typeof(v9), Int}")).Fields[2].(*Struct)
	va, ok = instVarargpInEnv(v9.Fields[methodSig], SVec{tv}).(*core.Vararg)
	if !ok || va.N != nil || va.T != any(core.Int64Type) {
		t.Errorf("a Vararg of a variable length: %s", showValue(va))
	}
	if got := instVarargpInEnv(v9.Fields[methodSig], SVec{}); got == nil {
		t.Error("no static parameters")
	}
	if got := instVarargpInEnv(v9.Fields[methodSig], SVec{core.Int64Type}); core.HasFreeTypeVars(got) {
		t.Errorf("a Vararg over Int: %s", showValue(got))
	}
	// a signature whose Vararg holds no variable: substituted as it is
	v1 := vm.lookupMatch(t, typ("Tuple{typeof(v1), Int}")).Fields[2].(*Struct)
	if got := instVarargpInEnv(v1.Fields[methodSig], SVec{tv}); core.HasFreeTypeVars(got) {
		t.Errorf("a Vararg{Any}: %s", showValue(got))
	}

	// get_max_varargs when the function's max_args is under what its method
	// takes (no insertion raised it): zero, the heuristic still deciding
	ty := vm.typeOf(typ("v1")).(*core.DataType)
	ty.Name.MaxArgs.Store(0)
	if n, may := vm.maxVarargs(v1); n != 0 || !may {
		t.Errorf("max_varargs of a function with max_args 0: %d %v", n, may)
	}
	v1.Fields[methodMaxVarargs] = uint8(3)
	if n, may := vm.maxVarargs(v1); n != 3 || may {
		t.Errorf("max_varargs of a set one: %d %v", n, may)
	}
	// update_max_args of what is no tuple type
	vm.updateMaxArgs(typ("Union{Tuple{Int}, Tuple{Int8}}"))
	vm.updateMaxArgs(typ("Tuple{typeof(v1), Int, Int, Int, Int, Int}"))
	if ty.Name.MaxArgs.Load() != 6 {
		t.Errorf("max_args of v1: %d", ty.Name.MaxArgs.Load())
	}

	// a builtin method: Tuple, with an unspecialized instance
	a1 := vm.lookupMatch(t, typ("Tuple{typeof(a1), Int}")).Fields[2].(*Struct)
	bm := &Struct{Type: a1.Type, Fields: append([]any(nil), a1.Fields...)}
	bm.Fields[methodSig] = core.AnyTupleType
	bm.Fields[methodUnspecialized] = true
	tt := typ("Tuple{typeof(a1), Int}").(*core.DataType)
	if got := vm.compilationSig(tt, SVec{}, bm, 0); len(got) != len(core.AnyTupleType.Parameters) {
		t.Errorf("the compilation signature of a builtin method: %v", got)
	}
	if !vm.isaCompileableSig(core.AnyTupleType, SVec{}, bm) || vm.isaCompileableSig(tt, SVec{}, bm) {
		t.Error("the compileable signature of a builtin method is Tuple, and only it")
	}
	v, err := vm.shims["jl_normalize_to_compilable_sig"](vm, []any{tt, SVec{}, bm, int32(0)})
	check(t, "normalize for a builtin method", v, err, "Tuple")

	// the shims' refusals
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_normalize_to_compilable_sig", []any{int64(1), SVec{}, a1, int32(0)}, "!TypeError(:ccall"},
		{"jl_normalize_to_compilable_sig", []any{tt, int64(1), a1, int32(0)}, "!TypeError(:ccall"},
		{"jl_normalize_to_compilable_sig", []any{tt, SVec{}, int64(1), int32(0)}, "!TypeError(:ccall"},
		{"jl_normalize_to_compilable_sig", []any{tt, SVec{}, tt, int32(0)}, "!TypeError(:ccall"},
		{"jl_normalize_to_compilable_sig", []any{tt, SVec{}, a1, int64(0)}, "!TypeError(:ccall"},
		{"jl_normalize_to_compilable_sig", []any{core.Int64Type, SVec{}, a1, int32(0)}, "!TypeError(:ccall"},
		{"jl_isa_compileable_sig", []any{tt, int64(1), a1}, "!not ported yet: jl_isa_compileable_sig"},
		{"jl_isa_compileable_sig", []any{tt, SVec{}, int64(1)}, "!not ported yet: jl_isa_compileable_sig"},
		{"jl_isa_compileable_sig", []any{tt, SVec{}, tt}, "!not ported yet: jl_isa_compileable_sig"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
}
