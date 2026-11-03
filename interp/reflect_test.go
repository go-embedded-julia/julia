package interp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_matching_methods where subtyping decides, each want julia 1.12.6's
// (testdata/matching.jl): the matches as (spec_types, sparams, the
// method's sig, fully_covers), session types shown under Main.L.
func TestMatchingMethods(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("ROOT() = nothing; g(x::Int) = 1; g(x::Integer) = 2; g(x) = 3; h(x::T, y::T) where {T} = 1; k(x::String) = 1; m2(x::Array{T, 1}) where {T<:Real} = T; amb(x::Int, y) = 1; amb(x, y::Int) = 2"); err != nil {
		t.Fatal(err)
	}
	ty := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(src, err)
		}
		return v
	}
	ref := func(t *core.DataType, v any) *Struct { return &Struct{Type: t, Fields: []any{v}} }
	refU := mutType("RefU", core.UInt64Type)
	refI := mutType("RefI", core.Int32Type)
	null := &Pointer{Type: vm.ptrType(core.Int32Type)}
	shim := vm.shims["jl_matching_methods"]
	show := func(v any) string {
		if v == nil {
			return "nothing"
		}
		_, _, elems, _ := arrayOf(v)
		parts := make([]string, len(elems))
		for i, e := range elems {
			m := e.(*Struct)
			parts[i] = fmt.Sprintf("(%s, %s, %s, %s)", showValue(m.Fields[0]), showValue(m.Fields[1]),
				showValue(m.Fields[2].(*Struct).Fields[7]), showValue(m.Fields[3]))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	}
	g := "Main.L.#g"
	for _, c := range []struct {
		name, tt string
		lim      int32
		want     string
	}{
		{"ROOT all", "Tuple{typeof(ROOT), Vararg{Any}}", -1, "[(Tuple{Main.L.#ROOT}, svec(), Tuple{Main.L.#ROOT}, false)]"},
		{"g all", "Tuple{typeof(g), Vararg{Any}}", -1, fmt.Sprintf("[(Tuple{%[1]s, Int64}, svec(), Tuple{%[1]s, Int64}, false), (Tuple{%[1]s, Integer}, svec(), Tuple{%[1]s, Integer}, false), (Tuple{%[1]s, Any}, svec(), Tuple{%[1]s, Any}, false)]", g)},
		{"g Int", "Tuple{typeof(g), Int}", -1, fmt.Sprintf("[(Tuple{%[1]s, Int64}, svec(), Tuple{%[1]s, Int64}, true)]", g)},
		{"g Int8", "Tuple{typeof(g), Int8}", -1, fmt.Sprintf("[(Tuple{%[1]s, Int8}, svec(), Tuple{%[1]s, Integer}, true)]", g)},
		{"g String", "Tuple{typeof(g), String}", -1, fmt.Sprintf("[(Tuple{%[1]s, String}, svec(), Tuple{%[1]s, Any}, true)]", g)},
		{"g Int lim 1", "Tuple{typeof(g), Int}", 1, fmt.Sprintf("[(Tuple{%[1]s, Int64}, svec(), Tuple{%[1]s, Int64}, true)]", g)},
		{"g all lim 1", "Tuple{typeof(g), Vararg{Any}}", 1, "nothing"},
		{"g all lim 0", "Tuple{typeof(g), Vararg{Any}}", 0, "nothing"},
		{"g all lim 3", "Tuple{typeof(g), Vararg{Any}}", 3, "[(Tuple{"},
		{"g none", "Tuple{typeof(g), Int, Int}", -1, "[]"},
		{"h all", "Tuple{typeof(h), Vararg{Any}}", -1, "[(Tuple{Main.L.#h, T, T} where T, svec(T), Tuple{Main.L.#h, T, T} where T, false)]"},
		{"h Int Int", "Tuple{typeof(h), Int, Int}", -1, "[(Tuple{Main.L.#h, Int64, Int64}, svec(Int64), Tuple{Main.L.#h, T, T} where T, true)]"},
		{"k Int", "Tuple{typeof(k), Int}", -1, "[]"},
		{"m2 all", "Tuple{typeof(m2), Vararg{Any}}", -1, "[(Tuple{Main.L.#m2, Vector{T}} where T<:Real, svec(T<:Real), Tuple{Main.L.#m2, Vector{T}} where T<:Real, false)]"},
		{"m2 Int", "Tuple{typeof(m2), Array{Int, 1}}", -1, "[(Tuple{Main.L.#m2, Vector{Int64}}, svec(Int64), Tuple{Main.L.#m2, Vector{T}} where T<:Real, true)]"},
		{"empty tuple", "Tuple{}", -1, "[]"},
		{"not a tuple", "Int", -1, "[]"},
	} {
		mn, mx := ref(refU, uint64(0)), ref(refU, ^uint64(0))
		pmn, _ := vm.valuePtr([]any{mn})
		pmx, _ := vm.valuePtr([]any{mx})
		w := vm.World()
		v, err := shim(vm, []any{ty(c.tt), nil, c.lim, false, w, pmn, pmx, null})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := show(v); !strings.HasPrefix(got, c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
		if mn.Fields[0].(uint64) > w || mx.Fields[0] != ^uint64(0) {
			t.Errorf("%s: valid %v..%v around %d", c.name, mn.Fields[0], mx.Fields[0], w)
		}
	}
	// the same Method object every time, and its fields
	v1, _ := shim(vm, []any{ty("Tuple{typeof(g), Int}"), nil, int32(-1), false, vm.World(), null, null, null})
	v2, _ := shim(vm, []any{ty("Tuple{typeof(g), Vararg{Any}}"), nil, int32(-1), false, vm.World(), null, null, null})
	_, _, e1, _ := arrayOf(v1)
	_, _, e2, _ := arrayOf(v2)
	meth := e1[0].(*Struct).Fields[2].(*Struct)
	if meth != e2[0].(*Struct).Fields[2] {
		t.Error("two Methods for one method")
	}
	for name, want := range map[expr.Symbol]string{"name": ":g", "module": "Main.L", "nargs": "2",
		"isva": "false", "slot_syms": `"#self#\0x\0"`, "line": "1", "file": ":none", "called": "255",
		"external_mt": "!UndefRefError", "invokes": "nothing", "max_varargs": "0xff"} {
		v, err := biGetfield(vm, []any{meth, name})
		check(t, string(name), v, err, want)
	}

	// what is refused: an intersection subtyping does not decide, two
	// matches neither more specific, a callee no table holds, a table of
	// its own, a future world
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"an undecided intersection", []any{ty("Tuple{typeof(h), Int, Any}"), nil, int32(-1), false, vm.World(), null, null, null}, "type intersection"},
		{"an ambiguity", []any{ty("Tuple{typeof(amb), Vararg{Any}}"), nil, int32(-1), false, vm.World(), null, null, null}, "neither more specific"},
		{"a callee of no table", []any{ty("Tuple{Any, Vararg{Any}}"), nil, int32(-1), false, vm.World(), null, null, null}, "the methods of Any"},
		{"a table of its own", []any{ty("Tuple{typeof(g)}"), int64(1), int32(-1), false, vm.World(), null, null, null}, "a method table of its own"},
		{"a lim not a Cint", []any{ty("Tuple{typeof(g)}"), nil, int64(-1), false, vm.World(), null, null, null}, "jl_matching_methods of"},
		{"a result through no pointer", []any{ty("Tuple{typeof(g)}"), nil, int32(-1), false, vm.World(), null, null, int64(1)}, "result through"},
		{"a min through no pointer", []any{ty("Tuple{typeof(g), Int}"), nil, int32(-1), false, vm.World(), int64(1), null, null}, "result through"},
		{"a max through no pointer", []any{ty("Tuple{typeof(g), Int}"), nil, int32(-1), false, vm.World(), null, int64(1), null}, "result through"},
	} {
		_, err := shim(vm, c.args)
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if v, err := shim(vm, []any{ty("Tuple{typeof(g), Int}"), nil, int32(-1), false, vm.World() + 1, null, null, null}); v != nil || err != nil {
		t.Errorf("a future world: %v %v", v, err)
	}
	// has_ambig is written 0
	amb := ref(refI, int32(7))
	pa, _ := vm.valuePtr([]any{amb})
	if _, err := shim(vm, []any{ty("Tuple{typeof(g), Int}"), nil, int32(-1), false, vm.World(), null, null, pa}); err != nil || amb.Fields[0] != int32(0) {
		t.Errorf("has_ambig: %v %v", amb.Fields[0], err)
	}

	// jl_specializations_get_linfo: one MethodInstance per argument types
	gl := vm.shims["jl_specializations_get_linfo"]
	tInt := ty("Tuple{typeof(g), Int}")
	mi1, err1 := gl(vm, []any{meth, tInt, SVec{}})
	mi2, err2 := gl(vm, []any{meth, ty("Tuple{typeof(g), Int}"), SVec{}})
	mi3, _ := gl(vm, []any{meth, ty("Tuple{typeof(g), Int8}"), SVec{}})
	if err1 != nil || err2 != nil || mi1 != mi2 || mi1 == mi3 {
		t.Errorf("MethodInstances: %v %v %v", err1, err2, mi1 == mi2)
	}
	for name, want := range map[expr.Symbol]string{"def": "", "specTypes": "Tuple{Main.L.#g, Int64}", "sparam_vals": "svec()",
		"backedges": "!UndefRefError", "cache_with_orig": "false", "dispatch_status": "0x00"} {
		v, err := biGetfield(vm, []any{mi1, name})
		if want == "" {
			if v != any(meth) {
				t.Errorf("def: %v", v)
			}
			continue
		}
		check(t, string(name), v, err, want)
	}
	for _, c := range []struct {
		args []any
		want string
	}{{[]any{int64(1), tInt, SVec{}}, "!TypeError(:ccall"}, {[]any{meth, tInt, int64(1)}, "!TypeError(:ccall"}} {
		v, err := gl(vm, c.args)
		check(t, "jl_specializations_get_linfo", v, err, c.want)
	}

	// methods defined most general first come out most specific first
	if _, err := s.Eval("q(x) = 1; q(x::Integer) = 2; q(x::Int) = 3"); err != nil {
		t.Fatal(err)
	}
	vq, err := shim(vm, []any{ty("Tuple{typeof(q), Vararg{Any}}"), nil, int32(-1), false, vm.World(), null, null, null})
	if got := show(vq); err != nil || !strings.HasPrefix(got, "[(Tuple{Main.L.#q, Int64}") || !strings.Contains(got, "Integer}, false), (Tuple{Main.L.#q, Any}") {
		t.Errorf("q all: %s %v", got, err)
	}
	// subtyping past its work bound is an error, not an answer
	hAll, hII, hTT := ty("Tuple{typeof(h), Vararg{Any}}"), ty("Tuple{typeof(h), Int, Int}"), ty("Tuple{typeof(h), T, T} where T")
	old := core.SetMaxSubtypeSteps(1)
	_, errMatch := shim(vm, []any{hAll, nil, int32(-1), false, vm.World(), null, null, null})
	_, errOrder := orderMatches([]match{{sig: hII}, {sig: hTT}})
	_, errOrder2 := orderMatches([]match{{sig: hTT}, {sig: hII}})
	core.SetMaxSubtypeSteps(old)
	if errMatch == nil || errOrder == nil || errOrder2 == nil {
		t.Errorf("past the work bound: %v %v %v", errMatch, errOrder, errOrder2)
	}

	// a method of a type's constructor table, and of a Go-built body
	v3, err := shim(vm, []any{ty("Tuple{Type{TypeVar}, Vararg{Any}}"), nil, int32(-1), false, vm.World(), null, null, null})
	if err != nil {
		t.Fatal(err)
	}
	_, _, e3, _ := arrayOf(v3)
	if len(e3) != 3 {
		t.Fatalf("TypeVar's constructors: %d", len(e3))
	}
	if n, _ := biGetfield(vm, []any{e3[0].(*Struct).Fields[2], expr.Symbol("name")}); n != expr.Symbol("TypeVar") {
		t.Errorf("a constructor's name: %v", n)
	}
}

// The inference lattice's types and the Method types have julia's fields.
func TestReflectionTypes(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	// julia shows Core's unexported names qualified (Core.Const); the VM
	// shows Core's names bare (modulePrefix)
	for src, want := range map[string]string{
		"Core.Const(1)":                          "Const(1)",
		"getfield(Core.Const(:a), :val)":         ":a",
		"Core.InterConditional(1, Int, Bool)":    "InterConditional(1, Int64, Bool)",
		"fieldtype(Core.PartialOpaque, :parent)": "MethodInstance",
		"fieldtype(Core.PartialStruct, :fields)": "Vector{Any}",
		"fieldtype(Core.Method, :purity)":        "UInt16",
		"fieldtype(Core.MethodMatch, :method)":   "Method",
	} {
		v, err := s.Eval(src)
		check(t, src, v, err, want)
	}
	if !vm.tMethod.Name.Mutable || vm.tMethodMatch.Name.Mutable || !fieldMarked(vm.tMethod.Name.AtomicFields, 29) || !fieldMarked(vm.tMethodInstance.Name.AtomicFields, 7) {
		t.Error("the reflection types' flags")
	}
	if compressArgnames(expr.Vector{expr.Symbol("#self#"), expr.Symbol("x"), 1}) != "#self#\x00x\x00\x00" {
		t.Error("compressArgnames")
	}
	// a definition's location: a LineNumberNode, quoted or not, its file a
	// symbol or not
	for _, c := range []struct {
		v    any
		file expr.Symbol
		line int32
	}{
		{expr.QuoteNode{Value: expr.LineNumberNode{Line: 3, File: expr.Symbol("f.jl")}}, "f.jl", 3},
		{expr.LineNumberNode{Line: 4, File: nil}, "", 4},
		{int64(1), "", 0},
	} {
		if f, l := functionLoc(c.v); f != c.file || l != c.line {
			t.Errorf("functionLoc(%v) = %s, %d", c.v, f, l)
		}
	}
}

// jl_prepend_string, as EscapeAnalysis.jl:36 calls it.
func TestPrependString(t *testing.T) {
	vm := New()
	ps := vm.shims["jl_prepend_string"]
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{"ssair/", "x.jl"}, "\"ssair/x.jl\""},
		{[]any{"a\x00b", "c\x00d"}, "\"ac\""},
		{[]any{strings.Repeat("a", 1000), strings.Repeat("b", 24)}, "!use a bigger buffer for jl_fullpath"},
		{[]any{"a", int64(1)}, "!not ported yet: jl_prepend_string"},
	} {
		v, err := ps(vm, c.args)
		check(t, "jl_prepend_string", v, err, c.want)
	}
	if v, _ := vm.shims["jl_hrtime"](vm, nil); v.(uint64) == 0 {
		t.Error("jl_hrtime")
	}
	// an alias that names no origin keeps the short message
	m := vm.NewModule("AL")
	m.declare("a", 0).Alias = vm.Core.own("Int")
	_, err := vm.writableGlobal(m, "a")
	check(t, "an alias of no origin", nil, err, "!cannot assign a value to imported variable AL.a")
}
