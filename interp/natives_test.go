package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// withNatives runs f with extra natives registered, and the registry as it was
// after.
func withNatives(t *testing.T, extra []nativeSpec, f func()) {
	t.Helper()
	old := natives
	natives = append(append([]nativeSpec(nil), old...), extra...)
	defer func() { natives = old }()
	f()
}

// A native is found by file, line, argument count and the hash of the lowered
// body, answers the cases it covers, and leaves the others to the julia body;
// an error it returns is the call's.
func TestNativeResolutionAndFallback(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	lamOf := func(name string) *lambda {
		f := s.Mod.own(expr.Symbol(name)).Value.(*Function)
		return f.Table.Methods(f.Table.World())[0].Def.(*lambda)
	}
	// Core only: the intrinsics are the arithmetic
	const add, sub, mul = "Core.Intrinsics.add_int", "Core.Intrinsics.sub_int", "Core.Intrinsics.mul_int"
	must(s.Eval("function nat_add(a, b)\n    " + add + "(a, b)\nend"))
	must(s.Eval("function nat_other(a, b)\n    " + sub + "(a, b)\nend"))
	lam, other := lamOf("nat_add"), lamOf("nat_other")
	if bodyHash(lam) == bodyHash(other) {
		t.Fatal("two different bodies have the same hash")
	}
	spec := func(l *lambda, fn nativeFn) nativeSpec {
		return nativeSpec{string(l.file), l.line, l.nargs, bodyHash(l), fn}
	}
	answer := func(vm *VM, args []any) (any, bool, error) { return int64(100), true, nil }
	decline := func(vm *VM, args []any) (any, bool, error) { return nil, false, nil }
	fail := func(vm *VM, args []any) (any, bool, error) { return nil, false, errors.New("native failed") }
	call := func(src string) string {
		v, err := s.Eval(src)
		if err != nil {
			return "!" + err.Error()
		}
		return showValue(v)
	}

	// no native: the julia body
	// (what nativeOf found for a lambda is cached, so each case has its own)
	must(s.Eval("function nat_none(a, b)\n    " + add + "(a, b)\nend"))
	if got := call("nat_none(1, 2)"); got != "3" {
		t.Fatalf("without a native: %s", got)
	}
	// a native that answers replaces the body
	withNatives(t, []nativeSpec{spec(lam, answer)}, func() {
		// twice: the second call finds the answer in the cache
		for i := 0; i < 2; i++ {
			if got := call("nat_add(1, 2)"); got != "100" {
				t.Errorf("a native that answers: %s", got)
			}
		}
		if got := call("nat_other(5, 3)"); got != "2" {
			t.Errorf("a method that no native stands for: %s", got)
		}
	})
	// one that declines leaves it to the body (a fresh lambda: the answer of the
	// first lookup is cached)
	must(s.Eval("function nat_decl(a, b)\n    " + mul + "(a, b)\nend"))
	withNatives(t, []nativeSpec{spec(lamOf("nat_decl"), decline)}, func() {
		if got := call("nat_decl(3, 4)"); got != "12" {
			t.Errorf("a native that declines: %s", got)
		}
	})
	must(s.Eval("function nat_err(a, b)\n    a ^ b\nend"))
	withNatives(t, []nativeSpec{spec(lamOf("nat_err"), fail)}, func() {
		if got := call("nat_err(2, 3)"); got != "!native failed" {
			t.Errorf("a native that fails: %s", got)
		}
	})
	// the hash, the line, the argument count and the file must all match
	for name, mutate := range map[string]func(*nativeSpec){
		"another body":   func(s *nativeSpec) { s.body = bodyHash(other) },
		"another line":   func(s *nativeSpec) { s.line++ },
		"another count":  func(s *nativeSpec) { s.nargs++ },
		"another file":   func(s *nativeSpec) { s.file = "nowhere.jl" },
		"an empty hash":  func(s *nativeSpec) { s.body = "" },
		"a longer file":  func(s *nativeSpec) { s.file = "x/" + s.file + "x" },
		"a file suffix":  nil,
		"the same, kept": nil,
	} {
		src := fmt.Sprintf("function nat_m%d(a, b)\n    %s(%s(a, b), %d)\nend", len(name), add, add, len(name))
		must(s.Eval(src))
		l := lamOf(fmt.Sprintf("nat_m%d", len(name)))
		sp := spec(l, answer)
		want := "100"
		if mutate != nil {
			mutate(&sp)
			want = fmt.Sprint(1 + 2 + len(name))
		}
		withNatives(t, []nativeSpec{sp}, func() {
			if got := call(fmt.Sprintf("nat_m%d(1, 2)", len(name))); got != want {
				t.Errorf("%s: %s, want %s", name, got, want)
			}
		})
	}
	// the cache holds a lambda weakly: the entry is there, and forgetNative (what
	// the cleanup of a collected lambda runs) removes it
	if _, ok := nativeCache.Load(weak.Make(lam)); !ok {
		t.Error("what nativeOf found is not cached")
	}
	forgetNative(weak.Make(lam))
	if _, ok := nativeCache.Load(weak.Make(lam)); ok {
		t.Error("forgetNative left the entry")
	}
	// natives off: the body, whatever the registry
	must(s.Eval("function nat_off(a, b)\n    " + add + "(a, b)\nend"))
	withNatives(t, []nativeSpec{spec(lamOf("nat_off"), answer)}, func() {
		nativesDisabled.Store(true)
		defer nativesDisabled.Store(false)
		if got := call("nat_off(1, 2)"); got != "3" {
			t.Errorf("natives disabled: %s", got)
		}
	})
}

// The native of _deleteend! drops the size and nothing else, for an element
// type that holds no pointer, and declines everything the body would not do
// the same way.
func TestNativeDeleteEnd(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vec := func(elem *core.DataType, n int, zero any) *Struct {
		elems := make([]any, n)
		for i := range elems {
			elems[i] = zero
		}
		return vm.newVector(elem, elems)
	}
	size := func(a *Struct) int64 { return a.Fields[1].(*Tuple).Elems[0].(int64) }
	// every isbits element type, sizes 0 and 1 and more, every shrink
	for name, c := range map[string]struct {
		t    *core.DataType
		zero any
	}{
		"UInt8": {core.UInt8Type, uint8(0)}, "Int8": {core.Int8Type, int8(0)}, "UInt16": {core.UInt16Type, uint16(0)},
		"Int32": {core.Int32Type, int32(0)}, "UInt64": {core.UInt64Type, uint64(0)}, "Int64": {core.Int64Type, int64(0)},
		"Float32": {core.Float32Type, float32(0)}, "Float64": {core.Float64Type, 0.0}, "Bool": {core.BoolType, false},
		"Char": {core.CharType, expr.Char(0)}, "UInt128": {core.UInt128Type, UInt128{}},
	} {
		for _, n := range []int{0, 1, 2, 5, 17} {
			for delta := 0; delta <= n; delta++ {
				a := vec(c.t, n, c.zero)
				mem := a.Fields[0]
				v, handled, err := nativeDeleteEnd(vm, []any{a, int64(delta)})
				if v != nil || !handled || err != nil || size(a) != int64(n-delta) || a.Fields[0] != mem {
					t.Errorf("%s n=%d delta=%d: %v %v %v size %d", name, n, delta, v, handled, err, size(a))
				}
			}
		}
	}
	// declined: the body does it, or throws
	bad := vec(core.UInt8Type, 3, uint8(0))
	anyVec := vm.newVector(core.AnyType, []any{int64(1), "x"})
	strVec := vm.newVector(core.StringType, []any{"a", "b"})
	matrix := &Struct{Type: must(core.Apply(core.ArrayType, core.Float64Type, int64(2))).(*core.DataType),
		Fields: []any{bad.Fields[0], vm.newTuple([]any{int64(1), int64(2)})}}
	notArray := &Struct{Type: must(core.Apply(core.GenericMemoryType, "not_atomic", core.UInt8Type, core.CPU)).(*core.DataType), Fields: []any{nil, nil}}
	for name, args := range map[string][]any{
		"delta past the end":      {vec(core.UInt8Type, 2, uint8(0)), int64(3)},
		"a negative delta":        {vec(core.UInt8Type, 2, uint8(0)), int64(-1)},
		"a delta that is no Int":  {bad, int32(1)},
		"a boxed element type":    {anyVec, int64(1)},
		"a String element type":   {strVec, int64(1)},
		"a matrix":                {matrix, int64(1)},
		"not an array":            {notArray, int64(1)},
		"not a struct":            {int64(1), int64(1)},
		"too few arguments":       {bad},
		"a size that is no Int":   {&Struct{Type: bad.Type, Fields: []any{bad.Fields[0], vm.newTuple([]any{"3"})}}, int64(1)},
		"a size that is no tuple": {&Struct{Type: bad.Type, Fields: []any{bad.Fields[0], int64(3)}}, int64(1)},
		"too few fields":          {&Struct{Type: bad.Type, Fields: []any{bad.Fields[0]}}, int64(1)},
	} {
		if v, handled, err := nativeDeleteEnd(vm, args); v != nil || handled || err != nil {
			t.Errorf("%s: %v %v %v", name, v, handled, err)
		}
	}
	if size(bad) != 3 {
		t.Errorf("a declined call changed the size: %d", size(bad))
	}
}

// The native of reinterpret is bitcast for primitives, and declines what the
// body would route elsewhere.
func TestNativeReinterpret(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		out  *core.DataType
		x    any
		want any
	}{
		"Float64 to UInt64": {core.UInt64Type, 1.0, uint64(0x3ff0000000000000)},
		"UInt64 to Float64": {core.Float64Type, uint64(0x3ff0000000000000), 1.0},
		"Int64 to UInt64":   {core.UInt64Type, int64(-1), ^uint64(0)},
		"Int8 to UInt8":     {core.UInt8Type, int8(-1), uint8(255)},
		"Float32 to UInt32": {core.UInt32Type, float32(1), uint32(0x3f800000)},
		"UInt32 to Char":    {core.CharType, uint32(0x41000000), expr.Char(0x41000000)},
		"Char to UInt32":    {core.UInt32Type, expr.Char(0x41000000), uint32(0x41000000)},
		"Int128 to UInt128": {core.UInt128Type, Int128{^uint64(0), ^uint64(0)}, UInt128{^uint64(0), ^uint64(0)}},
	} {
		got, handled, err := nativeReinterpret(vm, []any{c.out, c.x})
		if !handled || err != nil || got != c.want {
			t.Errorf("%s: %v %v %v, want %v", name, got, handled, err, c.want)
		}
	}
	for name, args := range map[string][]any{
		"a size that does not match": {core.Int32Type, int64(1)},
		"x that is no primitive":     {core.Int64Type, "abc"},
		"Out that is no type":        {int64(1), int64(1)},
		"Out that is not a leaf":     {core.IntegerType, int64(1)},
		"too few arguments":          {core.Int64Type},
	} {
		if v, handled, err := nativeReinterpret(vm, args); v != nil || handled || err != nil {
			t.Errorf("%s: %v %v %v", name, v, handled, err)
		}
	}
}

// The natives against the julia bodies they stand for, on the Base image
// (JLGO_BASE=1; JLGO_KERNEL_IMAGE names an image): every cell runs with the
// natives on and off and must answer the same. Shrinking a vector of each
// isbits type to each length, and a vector that holds pointers; reinterpret
// between every pair of primitive types of equal size, and the errors it
// throws.
func TestNativesAgainstTheJuliaBodies(t *testing.T) {
	k := baseKernelForBench(t)
	ctx := context.Background()
	cells := []string{}
	for _, T := range []string{"UInt8", "Int8", "UInt16", "Int16", "UInt32", "Int32", "UInt64", "Int64", "Float32", "Float64", "Bool", "Char", "UInt128", "Int128"} {
		cells = append(cells, fmt.Sprintf(`let out = String[]
    for n in (0, 1, 2, 5, 17), keep in (0, 1, 2, 4, 16, 17)
        keep <= n || continue
        v = Vector{%s}(undef, n)
        for i in 1:n; v[i] = %s(i %% 2); end
        resize!(v, keep)
        push!(out, string(n, ">", keep, ":", length(v), ":", repr(v)))
    end
    join(out, "; ")
end`, T, T))
	}
	// vectors that hold pointers, and a union of isbits types: the body clears them
	cells = append(cells,
		`let v = Any[1, "a", 2.5, nothing]; resize!(v, 1); repr(v); end`,
		`let v = String["a", "b", "c"]; resize!(v, 0); repr(v); end`,
		`let v = Union{Int,Nothing}[1, nothing, 3]; resize!(v, 2); push!(v, 7); repr(v); end`,
		`let v = UInt8[1, 2, 3]; resize!(v, 1); resize!(v, 5); length(v); end`,
		`let v = UInt8[1, 2, 3]; try; resize!(v, -1); catch e; string(typeof(e)); end; end`,
		`let v = UInt8[1, 2, 3]; Base._deleteend!(v, 4); end`)
	groups := [][]string{{"Int8", "UInt8", "Bool"}, {"Int16", "UInt16", "Float16"}, {"Int32", "UInt32", "Float32", "Char"}, {"Int64", "UInt64", "Float64"}, {"Int128", "UInt128"}}
	sample := map[string]string{"Int8": "Int8(-5)", "UInt8": "0xfb", "Bool": "true", "Int16": "Int16(-5)", "UInt16": "0xfffb", "Float16": "Float16(1.5)",
		"Int32": "Int32(-5)", "UInt32": "0x41000001", "Float32": "1.5f0", "Char": "Char(0x41000000)", "Int64": "-5", "UInt64": "0xfffffffffffffffb", "Float64": "1.5",
		"Int128": "Int128(-5)", "UInt128": "UInt128(5)"}
	for _, g := range groups {
		for _, out := range g {
			for _, in := range g {
				cells = append(cells, fmt.Sprintf(`try; repr(reinterpret(%s, %s)); catch e; string(typeof(e)); end`, out, sample[in]))
			}
		}
	}
	cells = append(cells,
		`try; reinterpret(Int32, 1); catch e; sprint(showerror, e); end`,
		`try; reinterpret(Int64, "abc"); catch e; sprint(showerror, e); end`,
		`unsigned(-5), signed(0xfffffffffffffffb), 1 << 3, 1024 >> 2, -8 >> 1, 5 << -1`,
		`reinterpret(UInt64, [1.0, 2.0])`, `reinterpret(Int8, UInt8[200, 1])`)
	run := func(src string) string {
		r := k.Eval(ctx, src)
		if r.Err != nil {
			return "!" + r.Err.Error()
		}
		return r.Value
	}
	for _, src := range cells {
		nativesDisabled.Store(true)
		want := run(src)
		nativesDisabled.Store(false)
		got := run(src)
		if got != want {
			t.Errorf("%s\n  natives: %s\n  julia:   %s", strings.TrimSpace(src), got, want)
		}
	}
	nativesDisabled.Store(false)
}
