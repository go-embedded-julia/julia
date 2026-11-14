package interp

import (
	"runtime"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

func bootedVM(t testing.TB) *VM {
	t.Helper()
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	return vm
}

// A mutable value gets its id from the VM's counter on the first objectid, and
// keeps it: no address is in it.
func TestObjectIDAssigned(t *testing.T) {
	vm := bootedVM(t)
	vals := objectIDSample(t, vm)
	mutable := []any{vals[0], vals[1], vals[2], vals[3], vals[4]} // Struct, Memory, Module, Function, Expr
	seen := map[uint64]bool{}
	for i, v := range mutable {
		id := vm.objectID(v)
		if id == 0 || seen[id] {
			t.Errorf("value %d: id %x is zero or taken", i, id)
		}
		seen[id] = true
		if again := vm.objectID(v); again != id {
			t.Errorf("value %d: id %x then %x", i, id, again)
		}
	}
	// the ids are the counter, mixed: the first object asked for gets the
	// first number, whatever its address
	fresh := bootedVM(t)
	for n := uint64(1); n <= 3; n++ {
		if got, want := fresh.objectID(&Memory{Type: memAnyType}), int64hash(n); got != want {
			t.Errorf("object %d got id %x, want %x", n, got, want)
		}
	}
	// a Builtin and an Intrinsic too
	var b *Builtin
	for _, v := range vm.Core.bindings {
		if x, ok := v.Value.(*Builtin); ok {
			b = x
			break
		}
	}
	if b == nil || vm.objectID(b) == 0 || vm.objectID(vm.intrinsics[0]) == 0 {
		t.Error("a Builtin or an Intrinsic has no id")
	}
	if vm.objectID(vm.intrinsics[0]) == vm.objectID(vm.intrinsics[1]) {
		t.Error("two Intrinsics share an id")
	}
	// a MemoryRef is its memory's id and its offset
	mem := &Memory{Type: memAnyType}
	r0 := vm.objectID(&MemoryRef{Type: memRefAnyType, Mem: mem})
	r1 := vm.objectID(&MemoryRef{Type: memRefAnyType, Mem: mem, Off: 1})
	if r0 == r1 || r0 != bitmix(vm.objectID(mem), 0) {
		t.Errorf("MemoryRef ids %x %x of a memory with id %x", r0, r1, vm.objectID(mem))
	}
}

// A type is hashed by its name and parameters, and by its name alone when
// the parameters do not hash (a Union among them: jl_precompute_memoized_dt).
func TestTypeID(t *testing.T) {
	vm := bootedVM(t)
	s := vm.NewSession()
	ev := func(src string) *core.DataType {
		t.Helper()
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		return v.(*core.DataType)
	}
	plain, other, union := ev("Tuple{Int}"), ev("Tuple{String}"), ev("Tuple{Union{Int, String}}")
	if plain.Hash() == 0 || typeID(plain) == typeID(other) {
		t.Errorf("Tuple{Int} and Tuple{String} hash alike: %x %x", typeID(plain), typeID(other))
	}
	if union.Hash() != 0 {
		t.Fatalf("Tuple{Union{Int,String}} hashes (%x): the case this test is for is gone", union.Hash())
	}
	if want := int64hash(uint64(union.Name.Hash())); typeID(union) != want {
		t.Errorf("an unhashable type: %x, want its name's %x", typeID(union), want)
	}
	// values of that type hash by it
	if vm.objectID(&Tuple{Type: union}) != typeID(union) {
		t.Error("an empty tuple of an unhashable type")
	}
}

// objectIDRun is a program and the ids of what it evaluates to, in order.
func objectIDRun(t *testing.T, noise bool) []uint64 {
	vm := bootedVM(t)
	s := vm.NewSession()
	var ids []uint64
	for _, src := range []string{
		"mutable struct OM; x; end; OM(1)", "OM(2)", "struct OI; x; end; OI(1)", "OI(OM(3))",
		"f_oid() = 1; f_oid", "Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 2)",
		"module OMod end; OMod", ":(a + b)", "Int64", "(1, OM(4))", "OM", "Union{Int, String}",
		"Core.tuple", "Ptr{Int}(0)", "Main",
	} {
		if noise {
			// move the heap about: no id may follow
			junk := make([][]byte, 0, 64)
			for i := 0; i < 64; i++ {
				junk = append(junk, make([]byte, 4096*(i+1)))
			}
			runtime.GC()
			_ = junk
		}
		v, err := s.Eval(src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		ids = append(ids, vm.objectID(v))
	}
	return ids
}

// The same program gives the same ids in every VM, whatever the heap does
// and however many VMs the process made before.
func TestObjectIDDeterministic(t *testing.T) {
	a := objectIDRun(t, false)
	b := objectIDRun(t, true)
	if len(a) != len(b) {
		t.Fatalf("%d ids, then %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Errorf("value %d: id %x, then %x", i, a[i], b[i])
		}
	}
	// two mutable objects of one type differ
	if a[0] == a[1] {
		t.Errorf("OM(1) and OM(2) share the id %x", a[0])
	}
}

// A module's build id comes from its VM's own counter: another VM of the
// process cannot change it.
func TestModuleBuildIDPerVM(t *testing.T) {
	a := New()
	for i := 0; i < 5; i++ {
		a.newModule(expr.Symbol("Extra"), nil)
	}
	b := New()
	c := New()
	if b.Main.buildID != c.Main.buildID || b.Core.buildID != c.Core.buildID {
		t.Errorf("Main build ids %v and %v, Core %v and %v", b.Main.buildID, c.Main.buildID, b.Core.buildID, c.Core.buildID)
	}
	if a.Main.buildID != b.Main.buildID {
		t.Errorf("modules made by one VM moved another's build ids: %v vs %v", a.Main.buildID, b.Main.buildID)
	}
	if x := b.newModule("X", nil); x.buildID[1] != b.moduleCtr || x.buildID[0] != ^uint64(0) || b.moduleCtr == 0 {
		t.Errorf("build id %v with counter %d", x.buildID, b.moduleCtr)
	}
}
