package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The argument tuple of a call is cached by the identity of what it is made of
// (#71): the cached answer is the type the uncached path builds, a Union, a
// UnionAll or too many arguments are not cached, the cache is bounded, and a
// method defined after the first call is still found.
func TestArgTupleTypeCache(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	union := must(s.Eval("Union{Core.Int, Core.String}"))
	unionAll := must(s.Eval("Core.Array"))
	tupleVal := must(s.Eval("(1, 2.5)"))
	fn := must(s.Eval("function argkey_f(x) x end; argkey_f"))
	keyed := map[string][]any{
		"none":                 {},
		"an Int":               {int64(1)},
		"a Float and a String": {2.5, "a"},
		"a type":               {core.Int64Type},
		"Type and values":      {core.Float64Type, int64(1), "x", true},
		"a Tuple value":        {tupleVal},
		"a function":           {fn},
		"six arguments":        {int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)},
		"a Type{Int} type":     {must(core.Apply(core.TypeType, core.Int64Type))},
	}
	unkeyed := map[string][]any{
		"a Union":               {union, int64(1)},
		"a UnionAll":            {unionAll},
		"seven arguments":       {int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7)},
		"a Union after a value": {"x", union},
	}
	for name, args := range keyed {
		want := vm.buildArgTupleType(args)
		first := vm.argTupleType(args)
		second := vm.argTupleType(args)
		if !core.TypesEqual(first, want) {
			t.Errorf("%s: %s, want %s", name, core.Show(first), core.Show(want))
		}
		if first != second {
			t.Errorf("%s: the second call built another type", name)
		}
		if _, _, ok := vm.argKeyOf(args); !ok {
			t.Errorf("%s: not keyed", name)
		}
	}
	for name, args := range unkeyed {
		if _, _, ok := vm.argKeyOf(args); ok {
			t.Errorf("%s: keyed", name)
		}
		if got, want := vm.argTupleType(args), vm.buildArgTupleType(args); !core.TypesEqual(got, want) {
			t.Errorf("%s: %s, want %s", name, core.Show(got), core.Show(want))
		}
	}
	// the key is the identity of the types: two Int64 arguments are one entry,
	// an Int64 and a Float64 are two
	if vm.argTupleType([]any{int64(1)}) != vm.argTupleType([]any{int64(2)}) {
		t.Error("two values of one type have two tuples")
	}
	if vm.argTupleType([]any{int64(1)}) == vm.argTupleType([]any{1.0}) {
		t.Error("two types have one tuple")
	}
	// bounded: past the limit a new key is built each time and not stored
	argTupleMu.Lock()
	saved := argTupleCache
	full := make(map[argKey]argTupleEntry, argTupleCacheMax)
	for i := 0; i < argTupleCacheMax; i++ {
		full[argKey{uintptr(i) + 1000}] = argTupleEntry{}
	}
	argTupleCache = full
	argTupleMu.Unlock()
	defer func() {
		argTupleMu.Lock()
		argTupleCache = saved
		argTupleMu.Unlock()
	}()
	fresh := []any{"a", "b", "c", int64(1), 1.0, true}
	a, b := vm.argTupleType(fresh), vm.argTupleType(fresh)
	if !core.TypesEqual(a, b) {
		t.Error("past the limit the tuple differs")
	}
	k, _, ok := vm.argKeyOf(fresh)
	argTupleMu.RLock()
	_, stored := argTupleCache[k]
	n := len(argTupleCache)
	argTupleMu.RUnlock()
	if !ok || stored || n != argTupleCacheMax {
		t.Errorf("past the limit the cache grew: keyed %v, stored %v, %d entries", ok, stored, n)
	}
}

// A method defined after a call cached its argument tuple is found by the next
// call: the cache holds a type, not an answer of dispatch.
func TestArgTupleCacheAndTheWorldAge(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	for _, c := range []struct{ src, want string }{
		{"argk(x) = 1; argk(2)", "1"},
		{"argk(x::Core.Int) = 2; argk(2)", "2"},
		{"argk(x::Core.Int, y) = 3; argk(2, 2)", "3"},
		{"argk(2)", "2"},
		{"argk(::Type{Core.Int}) = 4; argk(Core.Int)", "4"},
		{"argk(Core.Float64)", "1"},
		{"argk(::Type{Core.Float64}) = 5; argk(Core.Float64)", "5"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}
}
