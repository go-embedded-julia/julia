package interp

import (
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// afterWant is the element after a store, as julia prints it.
var afterWant = map[string]string{"set release": "7", "swap": "9", "any set": "1.5"}

// The atomic pointer intrinsics in julia's sequence, each want julia
// 1.12.6's (testdata/atomics.jl).
func TestAtomicPointers(t *testing.T) {
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
	m := mem("Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 2)")
	m.Elems[0], m.Elems[1] = int64(5), int64(6)
	p := &Pointer{Type: vm.ptrType(core.Int64Type), Target: m}
	ma := mem("Core.GenericMemory{:not_atomic, Any, Core.CPU}(undef, 1)")
	ma.Elems[0] = expr.Symbol("a")
	pa := &Pointer{Type: vm.ptrType(core.AnyType), Target: ma}
	nt := must(core.Apply(core.AnyTupleType, core.Int64Type, core.Int64Type, core.Int64Type))
	pt := &Pointer{Type: vm.ptrType(nt), Target: &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), nt, core.CPU)).(*core.DataType), Elems: []any{undef}}}
	str := &Pointer{Type: vm.ptrType(core.UInt8Type), Target: "abc"}
	uuT, err := s.Eval("struct UU; x::Union{Int8, UInt8}; end; UU")
	if err != nil {
		t.Fatal(err)
	}
	uu := &Pointer{Type: vm.ptrType(uuT), Target: m}
	plus, _ := s.Eval("Core.Intrinsics.add_int")
	float, _ := s.Eval("(a, b) -> 1.5")
	second, _ := s.Eval("(a, b) -> b")
	call := func(name string, args ...any) (any, error) {
		return vm.intrinsics[intrinsicIDs[expr.Symbol(name)]].Fn(vm, args)
	}
	sym := func(n string) expr.Symbol { return expr.Symbol(n) }
	sc, mono := sym("sequentially_consistent"), sym("monotonic")
	parts := func(v any) string {
		if st, ok := v.(*Struct); ok {
			fs := []string{showValue(st.Type)}
			for _, f := range st.Fields {
				fs = append(fs, showValue(f))
			}
			return "(" + strings.Join(fs, ", ") + ")"
		}
		return showValue(v)
	}
	for _, c := range []struct {
		name, f string
		args    []any
		want    string
		after   func() any
	}{
		{"ref sc", "atomic_pointerref", []any{p, sc}, "5", nil},
		{"ref not_atomic", "atomic_pointerref", []any{p, sym("not_atomic")}, "5", nil},
		{"ref release", "atomic_pointerref", []any{p, sym("release")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", nil},
		{"ref 1", "atomic_pointerref", []any{p, int64(1)}, "!TypeError(:atomic_pointerref, \"\", Symbol, 1)", nil},
		{"ref of no pointer", "atomic_pointerref", []any{int64(1), sc}, "!TypeError(:atomic_pointerref, \"\", Ptr, 1)", nil},
		{"set release", "atomic_pointerset", []any{p, int64(7), sym("release")}, "", func() any { return m.Elems[0] }},
		{"set acquire", "atomic_pointerset", []any{p, int64(7), sym("acquire")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", nil},
		{"set float", "atomic_pointerset", []any{p, 1.5, sym("release")}, "!TypeError(:atomic_pointerset, \"\", Int64, 1.5)", nil},
		{"swap", "atomic_pointerswap", []any{p, int64(9), sym("acquire_release")}, "7", func() any { return m.Elems[0] }},
		{"swap of no pointer", "atomic_pointerswap", []any{int64(1), int64(9), sc}, "!TypeError(:atomic_pointerswap", nil},
		{"swap float", "atomic_pointerswap", []any{p, 1.5, sc}, "!TypeError(:atomic_pointerswap, \"\", Int64, 1.5)", nil},
		{"modify", "atomic_pointermodify", []any{p, plus, int64(1), mono}, "(Pair{Int64, Int64}, 9, 10)", nil},
		{"modify float", "atomic_pointermodify", []any{p, float, int64(1), mono}, "!TypeError(:atomic_pointermodify, \"\", Int64, 1.5)", nil},
		{"modify of no pointer", "atomic_pointermodify", []any{int64(1), plus, int64(1), mono}, "!TypeError(:atomic_pointermodify", nil},
		{"modify throwing", "atomic_pointermodify", []any{p, plus, 1.5, mono}, "!", nil},
		{"replace hit", "atomic_pointerreplace", []any{p, int64(10), int64(11), sc, mono}, "(@NamedTuple{old::Int64, success::Bool}, 10, true)", nil},
		{"replace miss", "atomic_pointerreplace", []any{p, int64(3), int64(4), sc, mono}, "(@NamedTuple{old::Int64, success::Bool}, 11, false)", nil},
		{"replace order", "atomic_pointerreplace", []any{p, int64(3), int64(4), mono, sc}, "!ConcurrencyViolationError(\"atomic_pointerreplace: invalid atomic ordering\")", nil},
		{"replace float", "atomic_pointerreplace", []any{p, int64(3), 4.0, sc, mono}, "!TypeError(:atomic_pointerreplace, \"\", Int64, 4.0)", nil},
		{"replace sym", "atomic_pointerreplace", []any{p, int64(3), int64(4), int64(1), mono}, "!TypeError(:atomic_pointerreplace, \"\", Symbol, 1)", nil},
		{"replace acquire", "atomic_pointerreplace", []any{p, int64(3), int64(4), sym("acquire"), mono}, "(@NamedTuple{old::Int64, success::Bool}, 11, false)", nil},
		{"replace unordered", "atomic_pointerreplace", []any{p, int64(3), int64(4), sym("unordered"), mono}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", nil},
		{"replace bad failure", "atomic_pointerreplace", []any{p, int64(3), int64(4), sc, sym("release")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", nil},
		{"replace of no pointer", "atomic_pointerreplace", []any{int64(1), int64(3), int64(4), sc, mono}, "!TypeError(:atomic_pointerreplace", nil},
		{"replace past the memory", "atomic_pointerreplace", []any{&Pointer{Type: p.Type, Target: m, Off: 16}, int64(3), int64(4), sc, mono}, "!atomic_pointerreplace: invalid pointer", nil},
		{"any ref", "atomic_pointerref", []any{pa, mono}, ":a", nil},
		{"any set", "atomic_pointerset", []any{pa, 1.5, mono}, "", func() any { return ma.Elems[0] }},
		{"any replace", "atomic_pointerreplace", []any{pa, 1.5, "s", mono, mono}, "(@NamedTuple{old, success::Bool}, 1.5, true)", nil},
		{"any modify", "atomic_pointermodify", []any{pa, second, int64(2), mono}, "(Pair{Any, Any}, \"s\", 2)", nil},
		{"tuple ref", "atomic_pointerref", []any{pt, mono}, "!atomic_pointerref: invalid pointer for atomic operation", nil},
		{"abstract ref", "atomic_pointerref", []any{&Pointer{Type: vm.ptrType(core.IntegerType), Target: m}, mono}, "!atomic_pointerref: invalid pointer", nil},
		{"array ref", "atomic_pointerref", []any{&Pointer{Type: vm.ptrType(vecAnyType), Target: m}, mono}, "!atomic_pointerref: invalid pointer", nil},
		{"ref past the memory", "atomic_pointerref", []any{&Pointer{Type: p.Type, Target: m, Off: 16}, mono}, "!atomic_pointerref: invalid pointer", nil},
		{"swap past the memory", "atomic_pointerswap", []any{&Pointer{Type: p.Type, Target: m, Off: 16}, int64(1), mono}, "!atomic_pointerswap: invalid pointer", nil},
		{"modify past the memory", "atomic_pointermodify", []any{&Pointer{Type: p.Type, Target: m, Off: 16}, plus, int64(1), mono}, "!atomic_pointermodify: invalid pointer", nil},
		{"modify into no memory", "atomic_pointermodify", []any{&Pointer{Type: p.Type, Target: "abcdefgh"}, plus, int64(1), mono}, "!", nil},
		{"modify a tuple", "atomic_pointermodify", []any{pt, plus, int64(1), mono}, "!atomic_pointermodify: invalid pointer for atomic operation", nil},
		{"modify a String's byte", "atomic_pointermodify", []any{str, second, uint8('x'), mono}, "!atomic_pointermodify: invalid pointer", nil},
		{"replace a String's byte", "atomic_pointerreplace", []any{str, uint8('a'), uint8('x'), mono, mono}, "!atomic_pointerreplace: invalid pointer", nil},
		{"a layout not computed", "atomic_pointerref", []any{uu, mono}, "!not ported yet: the layout of an isbits Union field", nil},
		{"fence sc", "atomic_fence", []any{sc}, "nothing", nil},
		{"fence acquire", "atomic_fence", []any{sym("acquire")}, "nothing", nil},
		{"fence unordered", "atomic_fence", []any{sym("unordered")}, "!ConcurrencyViolationError(\"invalid atomic ordering\")", nil},
		{"fence 1", "atomic_fence", []any{int64(1)}, "!TypeError(:fence, \"\", Symbol, 1)", nil},
	} {
		v, err := call(c.f, c.args...)
		switch {
		case err != nil || strings.HasPrefix(c.want, "!"):
			check(t, c.name, v, err, c.want)
		case c.want != "" && parts(v) != c.want:
			t.Errorf("%s: got %s, want %s", c.name, parts(v), c.want)
		}
		if c.after != nil && err == nil {
			if got := showValue(c.after()); got != afterWant[c.name] {
				t.Errorf("%s: after %s, want %s", c.name, got, afterWant[c.name])
			}
		}
	}
	if m.Elems[0] != int64(11) || ma.Elems[0] != int64(2) {
		t.Errorf("after the sequence: %v %v", m.Elems[0], ma.Elems[0])
	}
}

// memmove between memories: whole elements of one inline type.
func TestMemmoveMemory(t *testing.T) {
	vm := New()
	ints := func(xs ...int64) *Memory {
		m := &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU)).(*core.DataType)}
		for _, x := range xs {
			m.Elems = append(m.Elems, x)
		}
		return m
	}
	at := func(m *Memory, off int64) *Pointer {
		return &Pointer{Type: vm.ptrType(core.NothingType), Target: m, Off: off}
	}
	a, b := ints(1, 2, 3, 4), ints(5, 6, 7)
	if _, err := vm.memmove([]any{at(a, 8), at(b, 0), uint64(24)}); err != nil || showValue(a) != "[1, 5, 6, 7]" {
		t.Errorf("memmove: %v %s", err, showValue(a))
	}
	// overlap
	if _, err := vm.memmove([]any{at(a, 0), at(a, 8), uint64(24)}); err != nil || showValue(a) != "[5, 6, 7, 7]" {
		t.Errorf("memmove overlapping: %v %s", err, showValue(a))
	}
	anys := &Memory{Type: memAnyType, Elems: []any{int64(1)}}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"part of an element", []any{at(a, 0), at(b, 0), uint64(4)}},
		{"a misaligned offset", []any{at(a, 4), at(b, 0), uint64(8)}},
		{"a negative offset", []any{at(a, -8), at(b, 0), uint64(8)}},
		{"past the destination", []any{at(a, 24), at(b, 0), uint64(16)}},
		{"past the source", []any{at(a, 0), at(b, 16), uint64(16)}},
		{"an offset past the end", []any{at(a, 40), at(b, 0), uint64(0)}},
		{"a huge size", []any{at(a, 0), at(b, 0), uint64(1) << 63}},
		{"boxed elements", []any{at(anys, 0), at(anys, 0), uint64(8)}},
		{"two element types", []any{at(anys, 0), at(a, 0), uint64(8)}},
	} {
		v, err := vm.memmove(c.args)
		check(t, c.name, v, err, "!not ported yet: memmove")
	}
}
