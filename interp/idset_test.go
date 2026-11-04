package interp

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// goIdSet is Base.IdSet (base/idset.jl) written over the shims.
type goIdSet struct {
	t     *testing.T
	vm    *VM
	ref   *Pointer
	list  *Memory
	idxs  *Memory
	count int
	max   int
}

func newGoIdSet(t *testing.T, vm *VM, s *Session) *goIdSet {
	rv, err := s.Eval("mutable struct IdRV\nx::Int\nend; IdRV(0)")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := vm.newEqTable(0)
	return &goIdSet{t: t, vm: vm, list: list, idxs: &Memory{Type: memUInt8Type},
		ref: &Pointer{Type: vm.ptrType(core.Int64Type), Target: rv}}
}

func (s *goIdSet) call(name string, args ...any) any {
	s.t.Helper()
	v, err := s.vm.shims[expr.Symbol(name)](s.vm, args)
	if err != nil {
		s.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (s *goIdSet) has(k any) bool { return s.call("jl_idset_peek_bp", s.list, s.idxs, k).(int64) != -1 }

func (s *goIdSet) push(x any) {
	idx := s.call("jl_idset_peek_bp", s.list, s.idxs, x).(int64)
	if idx >= 0 {
		s.list.Elems[idx] = x
		return
	}
	if s.max < len(s.list.Elems) {
		idx = int64(s.max)
		s.list.Elems[idx] = x
		s.max = int(idx) + 1
	} else {
		s.list = s.call("jl_idset_put_key", s.list, x, s.ref).(*Memory)
		idx = s.ref.Target.(*Struct).Fields[0].(int64)
		if idx < 0 {
			s.max = int(-idx)
		} else {
			s.max = int(idx) + 1
		}
	}
	s.idxs = s.call("jl_idset_put_idx", s.list, s.idxs, idx).(*Memory)
	s.count++
}

func (s *goIdSet) pop(x any) {
	if s.call("jl_idset_pop", s.list, s.idxs, x).(int64) != -1 {
		s.count--
		for s.max > 0 && s.list.Elems[s.max-1] == undef {
			s.max--
		}
	}
}

func idKeyString(k any) string {
	switch x := k.(type) {
	case float64:
		return strconv.FormatFloat(x, 'f', 1, 64)
	case string:
		return x
	case expr.Symbol:
		return string(x)
	}
	return fmt.Sprint(k)
}

// dump is testdata/idset.jl's dump.
func (s *goIdSet) dump(name string) string {
	n := len(s.list.Elems)
	ks := 0
	var parts []string
	for i, k := range s.list.Elems {
		if k == undef {
			parts = append(parts, "#")
			continue
		}
		if x, ok := k.(int64); ok {
			ks += (i + 1) * int(x)
		}
		parts = append(parts, idKeyString(k))
	}
	keys := strings.Join(parts, ",")
	if n > 64 {
		keys = fmt.Sprintf("n=%d/%d", n, ks)
	}
	w := map[*core.DataType]string{memUInt8Type: "UInt8", memUInt16Type: "UInt16", memUInt32Type: "UInt32"}[s.idxs.Type]
	return fmt.Sprintf("%s %s %s %d %d", name, keys, w, s.count, s.max)
}

// want compares a dump with julia's line, without the index's length and
// checksum: they follow the keys' object ids, which objectid.go does not
// reproduce (its comment says why). The index must hold exactly the keys
// instead.
func (s *goIdSet) want(got, want string) {
	s.t.Helper()
	f := strings.Fields(want)
	want = strings.Join(append(append([]string(nil), f[:3]...), f[5:]...), " ")
	for _, k := range s.list.Elems {
		if k != undef && !s.has(k) {
			s.t.Errorf("%s: key %v is not found in the index", f[0], k)
		}
	}
	if got != want {
		s.t.Errorf("got  %s\nwant %s", got, want)
	}
}

// IdSet against julia 1.12.6's Base.IdSet (testdata/idset.jl): the index
// outgrowing UInt8 and UInt16, tombstones, compaction and the full rehash.
func TestIdSetAgainstJulia(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	sess := vm.NewSession()
	s := newGoIdSet(t, vm, sess)
	for i := int64(1); i <= 300; i++ {
		s.push(i)
	}
	s.want(s.dump("grow"), "grow n=316/9045050 UInt16 512 12150221 300 300")

	s = newGoIdSet(t, vm, sess)
	for i := int64(1); i <= 40; i++ {
		s.push(i)
	}
	for i := int64(5); i <= 40; i += 5 {
		s.pop(i)
	}
	if s.has(int64(5)) || !s.has(int64(6)) || s.has(int64(5000)) {
		t.Error("membership after pops")
	}
	s.want(s.dump("popped"), "popped 1,2,3,4,#,6,7,8,9,#,11,12,13,14,#,16,17,18,19,#,21,22,23,24,#,26,27,28,29,#,31,32,33,34,#,36,37,38,39,#,#,# UInt8 128 167196 32 39")
	s.push(int64(5))
	s.want(s.dump("reinserted"), "reinserted 1,2,3,4,#,6,7,8,9,#,11,12,13,14,#,16,17,18,19,#,21,22,23,24,#,26,27,28,29,#,31,32,33,34,#,36,37,38,39,5,#,# UInt8 128 166121 33 40")
	for i := int64(41); i <= 60; i++ {
		s.push(i)
	}
	s.want(s.dump("compacted"), "compacted 1,2,3,4,6,7,8,9,11,12,13,14,16,17,18,19,21,22,23,24,26,27,28,29,31,32,33,34,36,37,38,39,5,41,42,43,44,45,46,47,48,49,50,51,52,53,54,55,56,57,58,59,60,#,#,#,#,#,#,#,#,#,# UInt8 128 92631 53 53")

	s = newGoIdSet(t, vm, sess)
	for i := int64(1); i <= 8; i++ {
		s.push(i)
	}
	s.pop(int64(8))
	s.pop(int64(7))
	s.pop(int64(7)) // already gone
	s.want(s.dump("tail"), "tail 1,2,3,4,5,6,#,#,# UInt8 32 6494 6 6")
	s.push(int64(70))
	s.want(s.dump("tailpush"), "tailpush 1,2,3,4,5,6,70,#,# UInt8 32 6641 7 7")
	s.push(int64(70))
	s.want(s.dump("again"), "again 1,2,3,4,5,6,70,#,# UInt8 32 6641 7 7")

	s = newGoIdSet(t, vm, sess)
	for _, k := range []any{int64(1), float64(1), true, "a", expr.Symbol("a")} {
		s.push(k)
	}
	if !s.has(int64(1)) || !s.has(float64(1)) || !s.has(true) || s.has(int64(2)) || s.has(float32(1)) {
		t.Error("egal membership")
	}
	s.want(s.dump("mixed"), "mixed 1,1.0,true,a,a,# UInt8 32 214 5 5")

	s = newGoIdSet(t, vm, sess)
	for i := int64(1); i <= 70000; i++ {
		s.push(i)
	}
	s.want(s.dump("wide"), "wide n=92170/114335783345000 UInt32 131072 160407818974476 70000 70000")
}

// The IdSet entry points' refusals, and the rehash and the smallintset
// helpers they stand on.
func TestIdSetShims(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	sess := vm.NewSession()
	s := newGoIdSet(t, vm, sess)
	for i := int64(1); i <= 10; i++ {
		s.push(i)
	}
	null := &Pointer{Type: vm.ptrType(core.Int64Type)}
	for _, c := range []struct {
		f    string
		args []any
		want string
	}{
		{"jl_idset_peek_bp", []any{int64(1), s.idxs, int64(1)}, "!not ported yet: jl_idset_peek_bp of 1"},
		{"jl_idset_peek_bp", []any{s.list, &Memory{Type: memAnyType}, int64(1)}, "-1"},
		{"jl_idset_peek_bp", []any{s.list, &Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU)).(*core.DataType)}, int64(1)}, "!not ported yet: jl_idset_peek_bp"},
		{"jl_idset_peek_bp", []any{s.idxs, s.idxs, int64(1)}, "!not ported yet: jl_idset_peek_bp"},
		{"jl_idset_peek_bp", []any{s.list, s.list, int64(3)}, "-1"},
		{"jl_idset_pop", []any{int64(1), s.idxs, int64(1)}, "!not ported yet: jl_idset_pop"},
		{"jl_idset_put_key", []any{int64(1), int64(1), null}, "!not ported yet: jl_idset_put_key of 1"},
		{"jl_idset_put_key", []any{s.idxs, int64(1), null}, "!not ported yet: jl_idset_put_key"},
		{"jl_idset_put_key", []any{s.list, int64(1), int64(5)}, "!not ported yet: jl_idset_put_key's result through 5"},
		{"jl_idset_put_idx", []any{int64(1), s.idxs, int64(1)}, "!not ported yet: jl_idset_put_idx"},
		{"jl_idset_put_idx", []any{s.list, s.idxs, "x"}, "!TypeError(:ccall"},
		{"jl_idset_rehash", []any{int64(1), s.idxs, uint64(1)}, "!not ported yet: jl_idset_rehash"},
		{"jl_idset_rehash", []any{s.list, s.idxs, int64(1)}, "!TypeError(:ccall"},
	} {
		v, err := vm.shims[expr.Symbol(c.f)](vm, c.args)
		check(t, c.f, v, err, c.want)
	}
	// a rehash of zero entries is the index itself; of n, a power of two
	v, err := vm.shims["jl_idset_rehash"](vm, []any{s.list, s.idxs, uint64(0)})
	if err != nil || v != any(s.idxs) {
		t.Errorf("rehash 0: %v", err)
	}
	v, err = vm.shims["jl_idset_rehash"](vm, []any{s.list, s.idxs, uint64(100)})
	if err != nil || len(v.(*Memory).Elems) != 128 {
		t.Errorf("rehash 100: %v %v", v, err)
	}
	// a full rehash (a negative index) after removals
	s.pop(int64(3))
	idx, err := vm.shims["jl_idset_put_idx"](vm, []any{s.list, s.idxs, int64(-10)})
	if err != nil || s.call("jl_idset_peek_bp", s.list, idx, int64(3)).(int64) != -1 ||
		s.call("jl_idset_peek_bp", s.list, idx, int64(4)).(int64) != 3 {
		t.Errorf("put_idx -10: %v", err)
	}
	// the index of a memory limit
	vm2 := New()
	vm2.MaxMemoryElems = 4
	if _, err := vm2.allocInt1d(1, 8); err == nil || !strings.Contains(err.Error(), "MaxMemoryElems") {
		t.Errorf("allocInt1d past the limit: %v", err)
	}
	vm2.MaxMemoryElems = 4
	if _, err := vm2.smallintsetInsert(&Memory{Type: memUInt8Type}, func(int) uint64 { return 0 }, 0); err == nil {
		t.Error("smallintsetInsert past the limit")
	}
	if _, _, err := vm2.idsetPutKey(&Memory{Type: memAnyType, Elems: []any{int64(1), int64(2), int64(3), int64(4)}}, int64(5)); err == nil {
		t.Error("idsetPutKey past the limit")
	}
	// the same limit met inside each shim, and by an index whose width must grow
	if _, err := vm2.smallintsetInsert(&Memory{Type: memUInt8Type, Elems: make([]any, 8)}, func(int) uint64 { return 0 }, 254); err == nil {
		t.Error("smallintsetInsert of a wider value past the limit")
	}
	old := vm.MaxMemoryElems
	vm.MaxMemoryElems = 1
	_, err = vm.shims["jl_idset_put_key"](vm, []any{&Memory{Type: memAnyType, Elems: []any{int64(1), int64(2)}}, int64(99), null})
	check(t, "put_key past the limit", nil, err, "!MaxMemoryElems")
	_, err = vm.shims["jl_idset_put_idx"](vm, []any{s.list, &Memory{Type: memUInt8Type}, int64(-3)})
	check(t, "put_idx past the limit", nil, err, "!MaxMemoryElems")
	vm.MaxMemoryElems = old
	// an index that is no Memory is refused, not dereferenced
	_, err = vm.shims["jl_idset_peek_bp"](vm, []any{s.list, int64(1), int64(5)})
	check(t, "peek_bp of a non-Memory index", nil, err, "!not ported")
	if h := vm.idsetHash(&Memory{Type: memAnyType, Elems: []any{undef}})(0); h != 0 {
		t.Errorf("the hash of a removed key: %d", h)
	}
	// a UInt32 index, and the widths' tombstones
	m := &Memory{Type: memUInt32Type, Elems: []any{uint32(0), uint32(0xFFFFFFFF), uint32(7)}}
	if intref(m, 1) != 0 || intrefAcquire(m, 1) != -1 || intrefAcquire(m, 2) != 7 {
		t.Error("UInt32 tombstone")
	}
	intsetRelease(m, 0, 9)
	if m.Elems[0] != uint32(9) {
		t.Error("UInt32 release")
	}
	m16 := &Memory{Type: memUInt16Type, Elems: []any{uint16(0)}}
	intsetRelease(m16, 0, -1)
	if m16.Elems[0] != uint16(0xFFFF) {
		t.Error("UInt16 tombstone")
	}
	if _, ok := intWidth(&Memory{Type: memUInt16Type}); !ok {
		t.Error("UInt16 width")
	}
	if w, ok := intWidth(&Memory{Type: memAnyType}); !ok || w != 0 {
		t.Error("Any width")
	}
	if _, ok := intWidth(&Memory{Type: memUInt8Type}); !ok {
		t.Error("UInt8 width")
	}
	if _, ok := intWidth(&Memory{Type: must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU)).(*core.DataType)}); ok {
		t.Error("another type has a width")
	}
	// an empty table finds nothing; a full one with no match stops
	if smallintsetLookup(&Memory{Type: memUInt8Type}, func(int) bool { return true }, 1, false) != -1 {
		t.Error("lookup in an empty table")
	}
	full := &Memory{Type: memUInt8Type, Elems: []any{uint8(1), uint8(2), uint8(3), uint8(4)}}
	if smallintsetLookup(full, func(int) bool { return false }, 1, false) != -1 {
		t.Error("lookup in a full table")
	}
	if smallintsetInsertAt(&Memory{Type: memUInt8Type, Elems: []any{uint8(0)}}, 0, 1) {
		t.Error("a table of one entry takes none")
	}
	if smallintsetInsertAt(full, 0, 5) {
		t.Error("a full table takes none")
	}
	// a rehash that does not fit: one slot short, so the table doubles
	small := &Memory{Type: memUInt8Type, Elems: make([]any, 5)}
	smallintsetEmpty(small)
	for i, v := range []uint8{1, 2, 3, 4, 5} {
		small.Elems[i] = v
	}
	newa, err := vm.smallintsetRehash(small, func(int) uint64 { return 0 }, 4, 0)
	if err != nil || len(newa.Elems) <= 4 {
		t.Errorf("rehash of colliding keys: %v", err)
	}
}
