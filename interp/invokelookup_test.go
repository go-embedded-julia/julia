package interp

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_gf_invoke_lookup_worlds and jl_method_table_disable, each want julia
// 1.12.6's (testdata/invokelookup.jl).
func TestInvokeLookup(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval("g(x::Int) = 1; g(x::Integer) = 2; g(x) = 3; amb(x::Int, y) = 1; amb(x, y::Int) = 2"); err != nil {
		t.Fatal(err)
	}
	ty := func(src string) any {
		v, err := s.Eval(src)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	lookup := vm.shims["jl_gf_invoke_lookup_worlds"]
	show := func(v any) string {
		if v == nil {
			return "nothing"
		}
		m := v.(*Struct)
		return "(" + showValue(m.Fields[0]) + ", " + showValue(m.Fields[1]) + ", " + showValue(m.Fields[2].(*Struct).Fields[7]) + ", " + showValue(m.Fields[3]) + ")"
	}
	for _, c := range []struct{ name, tt, want string }{
		{"g Int", "Tuple{typeof(g), Int}", "(Tuple{Main.L.#g, Int64}, svec(), Tuple{Main.L.#g, Int64}, true)"},
		{"g Int8", "Tuple{typeof(g), Int8}", "(Tuple{Main.L.#g, Int8}, svec(), Tuple{Main.L.#g, Integer}, true)"},
		{"g Any", "Tuple{typeof(g), Any}", "(Tuple{Main.L.#g, Any}, svec(), Tuple{Main.L.#g, Any}, true)"},
		{"g all", "Tuple{typeof(g), Vararg{Any}}", "nothing"},
		{"g two", "Tuple{typeof(g), Int, Int}", "nothing"},
		{"amb Int Int", "Tuple{typeof(amb), Int, Int}", "nothing"},
		{"amb Int Any", "Tuple{typeof(amb), Int, Any}", "(Tuple{Main.L.#amb, Int64, Any}, svec(), Tuple{Main.L.#amb, Int64, Any}, true)"},
		{"not a tuple", "Int", "nothing"},
	} {
		v, err := lookup(vm, []any{ty(c.tt), nil, vm.World(), null, null})
		if err != nil || show(v) != c.want {
			t.Errorf("%s: %s %v, want %s", c.name, show(v), err, c.want)
		}
	}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"a world not a UInt", []any{ty("Tuple{typeof(g), Int}"), nil, int64(1), null, null}},
		{"a table of its own", []any{ty("Tuple{typeof(g), Int}"), int64(1), vm.World(), null, null}},
		{"a callee of no table", []any{ty("Tuple{Any, Int}"), nil, vm.World(), null, null}},
		{"a min through no pointer", []any{ty("Tuple{typeof(g), Int}"), nil, vm.World(), int64(1), null}},
		{"a max through no pointer", []any{ty("Tuple{typeof(g), Int}"), nil, vm.World(), null, int64(1)}},
	} {
		if _, err := lookup(vm, c.args); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	if v, err := lookup(vm, []any{ty("Tuple{typeof(g), Int}"), nil, vm.World() + 1, null, null}); v != nil || err != nil {
		t.Errorf("a future world: %v %v", v, err)
	}
	// jl_gf_invoke_lookup: the match's Method, or nothing; Core.methodtable
	// is every function's table, as nothing is
	method := vm.shims["jl_gf_invoke_lookup"]
	m8, err := method(vm, []any{ty("Tuple{typeof(g), Int8}"), ty("Core.methodtable"), vm.World()})
	if err != nil || m8 == nil || showValue(m8.(*Struct).Fields[7]) != "Tuple{Main.L.#g, Integer}" {
		t.Errorf("jl_gf_invoke_lookup of g(::Int8): %v %v", m8, err)
	}
	if v, err := method(vm, []any{ty("Tuple{typeof(g), Int, Int}"), nil, vm.World()}); v != nil || err != nil {
		t.Errorf("jl_gf_invoke_lookup of no method: %v %v", v, err)
	}
	if _, err := method(vm, []any{ty("Tuple{typeof(g), Int}"), nil, int64(1)}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("jl_gf_invoke_lookup of a world not a UInt: %v", err)
	}
	// past the work bound: an error from subtyping or from specificity
	if _, err := s.Eval("hp(x::T, y::Array{T, 1}) where {T<:Integer} = 1"); err != nil {
		t.Fatal(err)
	}
	hq, gq := ty("Tuple{typeof(hp), Int, Array{Int, 1}}"), ty("Tuple{typeof(g), Int8}")
	for _, q := range []any{hq, gq} {
		for steps := 1; steps < 64; steps++ {
			old := core.SetMaxSubtypeSteps(steps)
			_, err := lookup(vm, []any{q, nil, vm.World(), null, null})
			core.SetMaxSubtypeSteps(old)
			if err == nil {
				break
			}
		}
	}

	hAny := ty("Tuple{typeof(hp), T, Array{T, 1}} where T<:Integer")
	old := core.SetMaxSubtypeSteps(1)
	_, errMost := mostSpecific([]match{{sig: hq}, {sig: hAny}})
	core.SetMaxSubtypeSteps(old)
	if errMost == nil {
		t.Error("mostSpecific past the work bound")
	}
	// a callable struct's methods, in the table that keeps the callee's type
	cs, err := s.Eval("struct CS end; (c::CS)(x::Int) = x; CS")
	if err != nil {
		t.Fatal(err)
	}
	csq := must(core.Apply(core.AnyTupleType, cs, core.Int64Type))
	if v, err := lookup(vm, []any{csq, nil, vm.World(), null, null}); err != nil || v == nil {
		t.Errorf("a callable struct's method: %v %v", v, err)
	} else if n := v.(*Struct).Fields[2].(*Struct).Fields[0]; n != expr.Symbol("CS") {
		t.Errorf("a callable struct's method name: %v", n)
	}

	// delete_method: which(g, (Int8,)) disabled, then g(Int8) takes g(x)
	m, err := lookup(vm, []any{ty("Tuple{typeof(g), Int8}"), nil, vm.World(), null, null})
	if err != nil {
		t.Fatal(err)
	}
	meth := m.(*Struct).Fields[2]
	disable := vm.shims["jl_method_table_disable"]
	if _, err := disable(vm, []any{meth}); err != nil {
		t.Fatal(err)
	}
	vm.taskWorld = vm.World()
	v, err := s.Eval("g(Core.Intrinsics.trunc_int(Int8, 1))")
	check(t, "after delete", v, err, "3")
	_, err = disable(vm, []any{meth})
	check(t, "again", nil, err, "!Method of g already disabled")
	_, err = disable(vm, []any{int64(1)})
	check(t, "not a Method", nil, err, "!TypeError(:ccall")
}

// Base.gensym's shims: ##N and ##name#N from one increasing counter.
func TestGensym(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	str := func(s string) *Pointer { return vm.stringPtrOf(s) }
	sym, _ := vm.shims["jl_symbol_name"](vm, []any{expr.Symbol("y")})
	var got []string
	for _, args := range [][]any{nil, {str("x"), uint64(1)}, {sym, ^uint64(0)}, {&Pointer{Type: str("").Type, Target: "ab\x00c"}, ^uint64(0)}} {
		v, err := vm.shims[map[bool]expr.Symbol{true: "jl_gensym", false: "jl_tagged_gensym"}[args == nil]](vm, args)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, string(v.(expr.Symbol)))
	}
	re := regexp.MustCompile(`^##(|x#|y#|ab#)(\d+)$`)
	last := -1
	for i, g := range got {
		m := re.FindStringSubmatch(g)
		if m == nil || []string{"", "x#", "y#", "ab#"}[i] != m[1] {
			t.Errorf("gensym %d: %q", i, g)
			continue
		}
		n := 0
		for _, d := range m[2] {
			n = n*10 + int(d-'0')
		}
		if n <= last {
			t.Errorf("gensym %d: %q not after %d", i, g, last)
		}
		last = n
	}
	for _, c := range []struct {
		args []any
		want string
	}{
		{[]any{str("a\x00b"), uint64(3)}, "!ArgumentError(\"Symbol name may not contain \\\\0\")"},
		{[]any{int64(1), uint64(1)}, "!not ported yet: jl_tagged_gensym"},
		{[]any{str("ab"), uint64(5)}, "!not ported yet: jl_tagged_gensym"},
		{[]any{int64(1), ^uint64(0)}, "!not ported yet: jl_tagged_gensym"},
	} {
		v, err := vm.shims["jl_tagged_gensym"](vm, c.args)
		check(t, "jl_tagged_gensym", v, err, c.want)
	}
	// refusals leave the counter where it was
	v, _ := vm.shims["jl_gensym"](vm, nil)
	if !strings.HasSuffix(string(v.(expr.Symbol)), "#"+itoa(last+1)) {
		t.Errorf("after refusals: %v, last %d", v, last)
	}
	_, err := vm.shims["jl_symbol_name"](vm, []any{int64(1)})
	check(t, "jl_symbol_name", nil, err, "!TypeError(:ccall")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// The inference world and the calls made in it; timing's reentrancy flag.
func TestTypeinfWorld(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if vm.typeinfWorld != 1 {
		t.Errorf("jl_typeinf_world starts at %d", vm.typeinfWorld)
	}
	if _, err := vm.shims["jl_set_typeinf_func"](vm, []any{int64(0)}); err != nil || vm.typeinfWorld != vm.taskWorld {
		t.Errorf("jl_set_typeinf_func: %v, world %d", err, vm.typeinfWorld)
	}
	// a function defined after the inference world is not callable in it
	later, err := s.Eval("later(x) = x; later")
	if err != nil {
		t.Fatal(err)
	}
	tuple := coreValue(t, vm, "tuple")
	at := func(off int64, xs ...any) *Pointer {
		return &Pointer{Type: vm.ptrType(core.AnyType), Target: &Memory{Type: memAnyType, Elems: xs}, Off: off}
	}
	call := vm.shims["jl_call_in_typeinf_world"]
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"Core.tuple(1, 2)", []any{at(0, tuple, int64(1), int64(2)), int32(3)}, "(1, 2)"},
		{"from an offset", []any{at(8, int64(9), tuple, int64(1)), int32(2)}, "(1,)"},
		{"a function newer than the world", []any{at(0, later, int64(1)), int32(2)}, "!MethodError"},
		{"no function", []any{at(0, tuple), int32(0)}, "!not ported yet: jl_call_in_typeinf_world"},
		{"past the memory", []any{at(0, tuple), int32(2)}, "!not ported yet: jl_call_in_typeinf_world"},
		{"a misaligned pointer", []any{at(4, tuple, int64(1)), int32(1)}, "!not ported yet: jl_call_in_typeinf_world"},
		{"a pointer past the end", []any{at(24, tuple), int32(1)}, "!not ported yet: jl_call_in_typeinf_world"},
		{"no pointer", []any{int64(1), int32(1)}, "!not ported yet: jl_call_in_typeinf_world"},
		{"an undefined argument", []any{at(0, tuple, undef), int32(2)}, "!UndefRefError()"},
	} {
		v, err := call(vm, c.args)
		check(t, c.name, v, err, c.want)
	}
	if vm.taskWorld == vm.typeinfWorld && vm.typeinfWorld != vm.World() {
		t.Error("the task's world was not restored")
	}
	ta := vm.shims["jl_typeinf_timing_begin"]
	te := vm.shims["jl_typeinf_timing_end"]
	start, _ := ta(vm, nil)
	nested, _ := ta(vm, nil)
	if start == uint64(0) || nested != uint64(0) {
		t.Errorf("timing: %v %v", start, nested)
	}
	te(vm, []any{nested})
	if again, _ := ta(vm, nil); again != uint64(0) {
		t.Error("a nested end cleared the flag")
	}
	te(vm, []any{start})
	if again, _ := ta(vm, nil); again == uint64(0) {
		t.Error("the end did not clear the flag")
	}
}
