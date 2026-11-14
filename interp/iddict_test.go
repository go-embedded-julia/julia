package interp

import (
	"errors"
	"math"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// eqTable drives the jl_eqtable_* shims as iddict.jl does.
type eqTableT struct {
	t   *testing.T
	vm  *VM
	ht  any
	ref *Struct // RefValue{Cint}
}

// mutType is a mutable struct type with one field x of type ft.
func mutType(name string, ft any) *core.DataType {
	return must(core.NewType("Main", name, core.AnyType, false, true, []expr.Symbol{"x"}, []any{ft}))
}

func newEqTableT(t *testing.T, vm *VM) *eqTableT {
	// a mutable struct with one Int32 field, as RefValue{Cint} is
	ref := &Struct{Type: mutType("RefCint", core.Int32Type), Fields: []any{int32(0)}}
	ht, err := vm.newEqTable(htNInline)
	if err != nil {
		t.Fatal(err)
	}
	return &eqTableT{t, vm, ht, ref}
}

func (e *eqTableT) call(name string, args ...any) any {
	e.t.Helper()
	v, err := e.vm.shims[expr.Symbol(name)](e.vm, args)
	if err != nil {
		e.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (e *eqTableT) ptr() any {
	p, _ := e.vm.valuePtr([]any{e.ref})
	return p
}

func (e *eqTableT) put(k, v any) bool {
	e.ht = e.call("jl_eqtable_put", e.ht, k, v, e.ptr())
	return e.ref.Fields[0] == int32(1)
}

func (e *eqTableT) get(k any) any { return e.call("jl_eqtable_get", e.ht, k, undef) }

func (e *eqTableT) pop(k any) (any, bool) {
	v := e.call("jl_eqtable_pop", e.ht, k, undef, e.ptr())
	return v, e.ref.Fields[0] == int32(1)
}

func (e *eqTableT) len() int {
	n := 0
	for i := uint64(0); ; i += 2 {
		i = e.call("jl_eqtable_nextind", e.ht, i).(uint64)
		if i == ^uint64(0) {
			return n
		}
		n++
	}
}

func TestEqTable(t *testing.T) {
	vm := New()
	e := newEqTableT(t, vm)
	mut := &Struct{Type: mutType("M", core.AnyType), Fields: []any{int64(1)}}
	mut2 := &Struct{Type: mut.Type, Fields: []any{int64(1)}}
	tup := vm.newTuple([]any{int64(1), "a"})
	for _, k := range []any{int64(1), 1.0, "a", nil, tup, mut} {
		if !e.put(k, k) {
			t.Errorf("put %s: not inserted", showValue(k))
		}
	}
	if e.put(int64(1), "one") {
		t.Error("an existing key was inserted again")
	}
	for k, want := range map[any]any{int64(1): "one", 1.0: 1.0, "a": "a", nil: nil, mut: mut} {
		if got := e.get(k); got != want {
			t.Errorf("get %s = %s", showValue(k), showValue(got))
		}
	}
	// keys are egal: an equal tuple finds the entry, an equal mutable does not
	if e.get(vm.newTuple([]any{int64(1), "a"})) != any(tup) || e.get(mut2) != undef || e.get(int64(2)) != undef {
		t.Error("egal keys")
	}
	if e.len() != 6 {
		t.Errorf("%d entries", e.len())
	}
	// pop leaves nothing as the key: nothing itself is still found after
	if v, found := e.pop(int64(1)); v != "one" || !found {
		t.Errorf("pop 1: %v %v", v, found)
	}
	if _, found := e.pop(int64(1)); found {
		t.Error("popped twice")
	}
	if e.get(nil) != nil || e.len() != 5 {
		t.Errorf("after pop: %s, %d entries", showValue(e.get(nil)), e.len())
	}
	if !e.put(int64(1), "again") || e.get(int64(1)) != "again" {
		t.Error("put after pop")
	}
	// growth: a thousand keys all found, through rehashes
	for i := int64(100); i < 1100; i++ {
		e.put(i, i*2)
	}
	for i := int64(100); i < 1100; i++ {
		if e.get(i) != i*2 {
			t.Fatalf("get %d after growth = %v", i, e.get(i))
		}
	}
	if e.len() != 1006 {
		t.Errorf("%d entries after growth", e.len())
	}
	re := e.call("jl_idtable_rehash", e.ht, uint64(4096)).(*Memory)
	if len(re.Elems) != 4096 {
		t.Errorf("rehash to %d slots", len(re.Elems))
	}
	// C_NULL for the result pointer: none wanted
	e.call("jl_eqtable_put", e.ht, int64(5000), int64(1), &Pointer{Type: vm.ptrType(core.NothingType)})
	if e.get(int64(5000)) != int64(1) {
		t.Error("put through C_NULL")
	}
	// an empty table grows on the first put; nextind past the end and on an
	// odd-length table
	empty := &Memory{Type: memAnyType}
	if m := e.call("jl_eqtable_put", empty, int64(1), int64(2), e.ptr()).(*Memory); len(m.Elems) != htNInline {
		t.Errorf("an empty table grew to %d", len(m.Elems))
	}
	if e.call("jl_eqtable_get", empty, int64(1), "d") != "d" {
		t.Error("get from an empty table")
	}
	odd := &Memory{Type: memAnyType, Elems: []any{int64(1), int64(2), int64(3)}}
	if e.call("jl_eqtable_nextind", odd, uint64(1)) != ^uint64(0) || e.call("jl_eqtable_nextind", odd, uint64(0)) != uint64(0) {
		t.Error("nextind of an odd-length table")
	}
	if maxProbe(2048) != 32 || maxProbe(64) != 16 {
		t.Error("maxProbe")
	}

	// nothing as a key after its own pop: the deleted slot is reused
	n := newEqTableT(t, vm)
	n.put(nil, int64(1))
	n.pop(nil)
	if !n.put(nil, int64(2)) || n.get(nil) != int64(2) || n.len() != 1 {
		t.Error("nothing put again after its pop")
	}
	// a table shorter than HT_N_INLINE grows to it when full
	small := &Memory{Type: memAnyType, Elems: []any{undef, undef}}
	for i := int64(0); i < 3; i++ {
		var err error
		if small, _, err = vm.tableAssign(small, i, i); err != nil {
			t.Fatal(err)
		}
	}
	if len(small.Elems) != htNInline {
		t.Errorf("a full 2-slot table grew to %d", len(small.Elems))
	}
	// a key whose value is undef is not found (an insertion C would be
	// finishing on another thread); nothing's is passed over, to the end
	half := &Memory{Type: memAnyType, Elems: []any{int64(1), undef, undef, undef}}
	if vm.tablePeek(half, int64(1)) != -1 {
		t.Error("a half-inserted key was found")
	}
	deleted := &Memory{Type: memAnyType, Elems: []any{nil, undef, nil, undef}}
	if vm.tablePeek(deleted, nil) != -1 {
		t.Error("nothing found in a table of deleted slots")
	}
	full := &Memory{Type: memAnyType, Elems: []any{int64(7), int64(7), int64(9), int64(9)}}
	if vm.tablePeek(full, int64(11)) != -1 {
		t.Error("a missing key found in a full table")
	}
}

// objectID agrees with egal: equal values hash alike.
func TestObjectID(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	vals := []string{"(1, \"a\")", "Core.svec(1, :a)", "1.5", "-0.0", "true", "false", "nothing",
		"\"str\"", ":sym", "Int64", "Core.tuple", "Core.Intrinsics.add_int", "Main", "Int8(1)",
		"Core.GenericMemory{:not_atomic, Int, Core.CPU}", "Union{Int, String}", "struct OI; x; end; OI(OI(1))",
		"mutable struct OM; x; end; OM(1)", "f_oi() = 1; f_oi", ":(a + b)", "QuoteNode(1)", "Ptr{Int}(0)",
		"Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 2)", "struct OU; x; OU() = new(); end; OU()"}
	for _, src := range vals {
		a, err1 := s.Eval(src)
		b, err2 := s.Eval(src)
		if err1 != nil || err2 != nil {
			t.Fatalf("%s: %v %v", src, err1, err2)
		}
		if vm.egal(a, b) && vm.objectID(a) != vm.objectID(b) {
			t.Errorf("%s: egal values with ids %x and %x", src, vm.objectID(a), vm.objectID(b))
		}
		if vm.objectID(a) != vm.objectID(a) {
			t.Errorf("%s: unstable id", src)
		}
	}
	mem, _ := s.Eval("Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 2)")
	ref := &MemoryRef{Type: memRefAnyType, Mem: mem.(*Memory)}
	if vm.objectID(ref) != vm.objectID(&MemoryRef{Type: memRefAnyType, Mem: mem.(*Memory)}) {
		t.Error("egal MemoryRefs")
	}
	if vm.objectID(0.0) == vm.objectID(math.Copysign(0, -1)) || vm.objectID(int64(1)) == vm.objectID(int64(2)) {
		t.Error("ids of values that are not egal collide where they need not")
	}
}

func TestEqTableErrors(t *testing.T) {
	vm := New()
	vm.MaxMemoryElems = 64
	e := newEqTableT(t, vm)
	sh := func(name string, args ...any) error {
		_, err := vm.shims[expr.Symbol(name)](vm, args)
		return err
	}
	ht := e.ht
	notMem := vm.newTuple(nil)
	for _, c := range []struct {
		name string
		args []any
		want string
	}{
		{"jl_idtable_rehash", []any{notMem, uint64(1)}, "not ported yet: jl_idtable_rehash of ()"},
		{"jl_idtable_rehash", []any{ht, int64(1)}, "TypeError(:ccall"},
		{"jl_idtable_rehash", []any{ht, uint64(65)}, "memory limit exceeded"},
		{"jl_eqtable_put", []any{notMem, int64(1), int64(1), e.ptr()}, "not ported yet: jl_eqtable_put"},
		{"jl_eqtable_put", []any{ht, int64(1), int64(1), int64(0)}, "not ported yet: jl_eqtable_put's result through 0"},
		{"jl_eqtable_put", []any{ht, int64(1), int64(1), &Pointer{Type: vm.ptrType(core.NothingType), Target: notMem}}, "result through"},
		{"jl_eqtable_put", []any{ht, int64(1), int64(1), &Pointer{Type: vm.ptrType(core.NothingType), Target: e.ref, Off: 4}}, "result through"},
		{"jl_eqtable_put", []any{ht, int64(1), int64(1), &Pointer{Type: vm.ptrType(core.NothingType), Target: &Struct{Type: e.ref.Type}}}, "result through"},
		{"jl_eqtable_put", []any{ht, int64(1), int64(1), &Pointer{Type: vm.ptrType(core.NothingType), Target: &Struct{Type: mutType("R64", core.Int64Type), Fields: []any{int64(0)}}}}, "result through"},
		{"jl_eqtable_get", []any{notMem, int64(1), nil}, "not ported yet: jl_eqtable_get"},
		{"jl_eqtable_pop", []any{notMem, int64(1), nil, e.ptr()}, "not ported yet: jl_eqtable_pop"},
		{"jl_eqtable_pop", []any{ht, int64(1), nil, int64(0)}, "result through"},
		{"jl_eqtable_nextind", []any{notMem, uint64(0)}, "not ported yet: jl_eqtable_nextind"},
		{"jl_eqtable_nextind", []any{ht, int64(0)}, "TypeError(:ccall"},
	} {
		err := sh(c.name, c.args...)
		check(t, c.name, nil, err, "!"+c.want)
	}
	// a table that must grow past the limit refuses
	for i, grown := int64(0), ht.(*Memory); ; i++ {
		var err error
		if grown, _, err = vm.tableAssign(grown, i, i); err != nil {
			if !errors.Is(err, ErrMemoryLimit) {
				t.Fatal(err)
			}
			break
		}
		if i > 1000 {
			t.Fatal("the table grew past the limit")
		}
	}
	if _, err := vm.newEqTable(65); !errors.Is(err, ErrMemoryLimit) {
		t.Error(err)
	}
	// a rehash into fewer slots than its keys must grow, past the limit
	big, _ := vm.newEqTable(64)
	for i := int64(0); i < 20; i++ {
		big, _, _ = vm.tableAssign(big, i, i)
	}
	vm.MaxMemoryElems = 32
	if _, err := vm.idtableRehash(big, 2); !errors.Is(err, ErrMemoryLimit) {
		t.Error("a rehash that grows past the limit:", err)
	}
	if err := sh("jl_eqtable_put", big, int64(1000), int64(1), e.ptr()); err != nil {
		t.Error("a put into room:", err)
	}
	full, _ := vm.newEqTable(32)
	for i := int64(0); i < 16; i++ {
		full, _, _ = vm.tableAssign(full, i, i)
	}
	if err := sh("jl_eqtable_put", full, int64(1000), int64(1), e.ptr()); !errors.Is(err, ErrMemoryLimit) {
		t.Error("a put that grows past the limit:", err)
	}
	vm.MaxMemoryElems = 64
	vm.MaxMemoryElems = 16
	if _, _, err := vm.tableAssign(&Memory{Type: memAnyType}, int64(1), int64(1)); !errors.Is(err, ErrMemoryLimit) {
		t.Error("a first table past the limit", err)
	}
}

// The table against a Go map, over any sequence of puts and pops of small
// integer keys.
func FuzzEqTable(f *testing.F) {
	f.Add([]byte{1, 2, 3, 0x81, 2, 3})
	f.Add([]byte{0, 0, 0x80, 0})
	f.Fuzz(func(t *testing.T, ops []byte) {
		vm := New()
		e := newEqTableT(t, vm)
		ref := map[int64]int64{}
		for i, op := range ops {
			k := int64(op & 0x3f)
			if op&0x80 != 0 {
				v, found := e.pop(k)
				want, had := ref[k]
				if found != had || had && v != want {
					t.Fatalf("op %d pop %d: %v %v, want %v %v", i, k, v, found, want, had)
				}
				delete(ref, k)
				continue
			}
			_, had := ref[k]
			if e.put(k, int64(i)) == had {
				t.Fatalf("op %d put %d: inserted when present %v", i, k, had)
			}
			ref[k] = int64(i)
		}
		for k, v := range ref {
			if e.get(k) != v {
				t.Fatalf("get %d = %v, want %d", k, e.get(k), v)
			}
		}
		if e.len() != len(ref) {
			t.Fatalf("%d entries, want %d", e.len(), len(ref))
		}
	})
}
