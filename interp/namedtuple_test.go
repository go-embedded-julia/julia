package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// stringBytes is total over any offset and count a program can give it,
// into a String or a Memory{UInt8} of the same bytes.
func FuzzStringBytes(f *testing.F) {
	f.Add("abc", int64(0), int64(3), uint64(0), false, false)
	f.Add("", int64(-1), int64(1)<<62, uint64(1)<<63, true, false)
	f.Add("hello", int64(1), int64(3), uint64(0), false, true)
	f.Add("ab", int64(0), int64(0), uint64(3), true, true)
	f.Fuzz(func(t *testing.T, s string, off, n int64, un uint64, unsigned, mem bool) {
		var count any = n
		if unsigned {
			count = un
		}
		var target any = s
		if mem {
			m := &Memory{Type: memUInt8Type, Elems: make([]any, len(s))}
			for i := range len(s) {
				m.Elems[i] = s[i]
			}
			target = m
		}
		got, ok := stringBytes(&Pointer{Target: target, Off: off}, count)
		if ok && (off < 0 || int(off)+len(got) > len(s) || s[off:int(off)+len(got)] != got) {
			t.Errorf("stringBytes(%q, %d, %v, memory %v) = %q", s, off, count, mem, got)
		}
	})
}

// A Memory of another element type than UInt8 is no byte buffer.
func TestStringBytesOfAMemoryOfInt32(t *testing.T) {
	m := &Memory{Type: memType(core.Int32Type), Elems: []any{int32(1)}}
	if _, ok := stringBytes(&Pointer{Target: m}, int64(1)); ok {
		t.Error("a Memory{Int32} read as bytes")
	}
}

// NamedTuple types and values, each want julia 1.12.6's repr of the same
// expression (Base's own NamedTuple methods aside, not loaded here).
func TestNamedTuple(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{"(a=1, b=2.5)", "(a = 1, b = 2.5)"},
		{"typeof((a=1, b=2.5))", "@NamedTuple{a::Int64, b::Float64}"},
		{"(a=1,)", "(a = 1,)"},
		{"NamedTuple()", "NamedTuple()"},
		{"(;)", "NamedTuple()"},
		{"typeof(NamedTuple())", "@NamedTuple{}"},
		{"NamedTuple{(:a,)}", "NamedTuple{(:a,)}"},
		{"NamedTuple{(:a, :b), Tuple{Int, T}} where T", "@NamedTuple{a::Int64, b::T} where T"},
		{"NamedTuple{(:a,), Tuple{Array{Int, 1}}}", "@NamedTuple{a::Vector{Int64}}"},
		{"NamedTuple{(:a,), Union{}}", "NamedTuple{(:a,), Union{}}"},
		{"Core.apply_type(NamedTuple, 1)", "NamedTuple{1}"},
		{"Core.apply_type(NamedTuple, 1, Tuple{})", "!TypeError(:NamedTuple, \"names\", Tuple, 1)"},
		{"Core.apply_type(NamedTuple, (1,), Tuple{Int})", "!TypeError(:NamedTuple, \"name\", Symbol, 1)"},
		{"Core.apply_type(NamedTuple, (:a, :a), Tuple{Int, Int})", "!duplicate field name in NamedTuple: \\\"a\\\" is not unique"},
		{"Core.apply_type(NamedTuple, (:a,), Tuple{Int, Int})", "!NamedTuple names and field types must have matching lengths"},
		{"Core.apply_type(NamedTuple, (:a,), Tuple{Vararg{Int}})", "!NamedTuple names and field types must have matching lengths"},
		{"Core.apply_type(NamedTuple, (:a,), Int)", "!TypeError(:NamedTuple, \"T\", T<:Tuple, Int64)"},
		{"Core.apply_type(NamedTuple, (:a,), 1)", "!TypeError(:NamedTuple, \"T\", T<:Tuple, 1)"},
		{"getfield((a=1, b=2), :b)", "2"},
		{"getfield((a=1, b=2), 1)", "1"},
		{"getfield((a=1, b=2), :c)", "!FieldError(@NamedTuple{a::Int64, b::Int64}, :c)"},
		{"fieldtype(typeof((a=1, b=2.5)), :b)", "Float64"},
		{"fieldtype(typeof((a=1, b=2.5)), 2)", "Float64"},
		{"fieldtype(typeof((a=1, b=2.5)), :c)", "!FieldError(@NamedTuple{a::Int64, b::Float64}, :c)"},
		{"Core.sizeof((a=1, b=2.5))", "16"},
		{"nfields((a=1, b=2.5))", "2"},
		{"(a=1, b=2) === (a=1, b=2)", "true"},
		{"(a=1, b=2) === (b=1, a=2)", "false"},
		{"Core._apply_iterate(nothing, Core.tuple, (a=1, b=2))", "(1, 2)"},
		{"NamedTuple{(:a,)}((1,))", "(a = 1,)"},
		{"NamedTuple{(:a,), Tuple{Int}}((1,))", "(a = 1,)"},
		{"Core.apply_type(NamedTuple, (:a,))((1, 2))", "!NamedTuple names and field types must have matching lengths"},
		{"getfield(getfield(typeof((a=1,)), :name), :names)", "svec()"},
		{"getfield(typeof((a=1,)), :types)", "svec(Int64)"},
		{"setfield!((a=1,), :a, 2)", "!setfield!: immutable struct of type NamedTuple cannot be changed"},
		{"NamedTuple{(:a, Symbol(\"b c\"))}((1, 2))", "(a = 1, var\"b c\" = 2)"},
		{"typeof(NamedTuple{(:a, Symbol(\"b c\"))}((1, 2)))", "@NamedTuple{a::Int64, var\"b c\"::Int64}"},
		{"NamedTuple{(:a,), Tuple{Any}}((1,))", "@NamedTuple{a}((1,))"},
		{"NamedTuple{(:a, :b), Tuple{Any, Int}}((1, 2))", "@NamedTuple{a, b::Int64}((1, 2))"},
		{"NamedTuple{(:a,), Tuple{T}} where T", "@NamedTuple{a::T} where T"},
		{"NamedTuple{(:a,), T} where T<:Tuple{Int}", "NamedTuple{(:a,), T} where T<:Tuple{Int64}"},
		{"Core.apply_type(NamedTuple, (:a,), Union{Tuple{Int}, Tuple{Float64}})", "!NamedTuple field type must be a tuple datatype"},
		{"Core.apply_type(NamedTuple, (), Union{})", "!NamedTuple field type must be a tuple datatype"},
		{"Core.apply_type(NamedTuple, (:a,), Union{})", "NamedTuple{(:a,), Union{}}"},
		{"Symbol(\"a\\0b\")", "!ArgumentError(\"Symbol name may not contain \\\\0\")"},
		// julia's show throws on the non-Symbol name; shown generically here
		{"NamedTuple{(1,), Tuple{T}} where T", "NamedTuple{(1,), Tuple{T}} where T"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// :splatnew (interpreter.c:302-310 over jl_new_structt)
	nt := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	a := nt("NamedTuple{(:a,), Tuple{Int}}")
	for _, c := range []struct {
		name string
		e    *expr.Expr
		want string
	}{
		{"a tuple", expr.New("splatnew", a, vm.newTuple([]any{int64(1)})), "(a = 1,)"},
		{"a non-tuple", expr.New("splatnew", a, int64(1)), "!TypeError(:new, \"\", Tuple, 1)"},
		{"too many", expr.New("splatnew", a, vm.newTuple([]any{int64(1), int64(2)})), "!new: too many arguments (expected 1)"},
		{"an abstract type", expr.New("splatnew", nt("NamedTuple{(:a,)}"), vm.newTuple([]any{int64(1)})), "!TypeError(:new, \"\", DataType, NamedTuple{(:a,)})"},
		{"a value of another type", expr.New("splatnew", a, vm.newTuple([]any{1.5})), "!TypeError(:new, \"\", Int64, 1.5)"},
		{"a malformed one", expr.New("splatnew", a), "!Invalid IR: malformed :splatnew"},
		{"an undefined type", expr.New("splatnew", gr("Core", "nope"), vm.newTuple(nil)), "!UndefVarError(:nope,"},
		{"an undefined tuple", expr.New("splatnew", a, gr("Core", "nope")), "!UndefVarError(:nope,"},
	} {
		thunk := expr.New("thunk", codeInfo([]any{c.e, ret(expr.SSAValue(1))}))
		v, err := vm.toplevelEval(s.Mod, thunk)
		check(t, c.name, v, err, c.want)
	}

	// jl_symbol_n over what a pointer into a String reaches; anything else
	// is refused, C having no checks to refuse it with
	str := vm.stringPtrOf("abc")
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"two bytes", []any{str, int64(2)}, ":ab"},
		{"an unsigned count", []any{str, uint64(3)}, ":abc"},
		{"past the end", []any{str, int64(4)}, "!not ported yet: jl_symbol_n"},
		{"an unsigned count past the end", []any{str, uint64(1 << 63)}, "!not ported yet: jl_symbol_n"},
		{"a negative count", []any{str, int64(-1)}, "!not ported yet: jl_symbol_n"},
		{"a float count", []any{str, 1.0}, "!not ported yet: jl_symbol_n"},
		{"an offset past the end", []any{&Pointer{Type: str.Type, Target: "abc", Off: 4}, int64(0)}, "!not ported yet: jl_symbol_n"},
		{"a pointer into no string", []any{&Pointer{Type: str.Type}, int64(0)}, "!not ported yet: jl_symbol_n"},
		{"no pointer", []any{"abc", int64(1)}, "!not ported yet: jl_symbol_n"},
	} {
		v, err := vm.shims["jl_symbol_n"](vm, c.args)
		check(t, c.name, v, err, c.want)
	}

	// names a show_sym prints bare or as var"…" (show.jl:1565-1572)
	for name, want := range map[expr.Symbol]string{"a": "a", "x1!": "x1!", "true": "var\"true\"",
		"1a": "var\"1a\"", "b c": "var\"b c\"", "": "var\"\"", "\xff": "var\"\\xff\"", "αβ": "αβ"} {
		if got := showSym(name); got != want {
			t.Errorf("showSym(%q) = %s, want %s", name, got, want)
		}
	}
	// a free names parameter leaves the type without fields; a non-symbol
	// name in a shown type is shown as a value
	free := core.TypeUnionAll(&core.TypeVar{Name: "T", LB: core.Bottom, UB: core.AnyType},
		must(core.Apply(core.NamedTupleType, vm.newTuple([]any{int64(1)}))))
	if got := showValue(free); got != "NamedTuple{(1,)}" {
		t.Errorf("NamedTuple{(1,)}: %s", got)
	}
}
