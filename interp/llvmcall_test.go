package interp

import (
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// The IR templates of atomics.jl, on an Atomic{Int64}-like cell, give what
// Threads.Atomic does in julia 1.12.6 (atomic_add! returns the old value,
// atomic_cas! the value it found); other IR requires the compiler.
func TestLlvmcallAtomics(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	cell, err := s.Eval("mutable struct AtomicCell; v::Int64; end; AtomicCell(5)")
	if err != nil {
		t.Fatal(err)
	}
	st := cell.(*Struct)
	p := &Pointer{Type: vm.ptrType(core.Int64Type), Target: st}
	rt, at := core.Int64Type, core.AnyType
	call := func(ir string, xs ...any) (any, error) {
		return vm.llvmcall(append([]any{ir, rt, at, p}, xs...))
	}
	load := "%ptr = bitcast i8* %0 to i64*\n%rv = load atomic i64, i64* %ptr acquire, align 8\nret i64 %rv\n"
	store := "%ptr = bitcast i8* %0 to i64*\nstore atomic i64 %1, i64* %ptr release, align 8\nret void\n"
	cas := "%ptr = bitcast i8* %0 to i64*\n%rs = cmpxchg i64* %ptr, i64 %1, i64 %2 acq_rel acquire\n%rv = extractvalue { i64, i1 } %rs, 0\nret i64 %rv\n"
	rmw := func(op string) string {
		return "%ptr = bitcast i8* %0 to i64*\n%rv = atomicrmw " + op + " i64* %ptr, i64 %1 acq_rel\nret i64 %rv\n"
	}
	for _, c := range []struct {
		name string
		ir   string
		xs   []any
		want string
		cell string
	}{
		{"load", load, nil, "5", "5"},
		{"store", store, []any{int64(7)}, "nothing", "7"},
		{"cas that fails", cas, []any{int64(1), int64(9)}, "7", "7"},
		{"cas that succeeds", cas, []any{int64(7), int64(9)}, "7", "9"},
		{"add", rmw("add"), []any{int64(3)}, "9", "12"},
		{"sub", rmw("sub"), []any{int64(2)}, "12", "10"},
		{"and", rmw("and"), []any{int64(6)}, "10", "2"},
		{"or", rmw("or"), []any{int64(5)}, "2", "7"},
		{"xor", rmw("xor"), []any{int64(1)}, "7", "6"},
		{"nand", rmw("nand"), []any{int64(3)}, "6", "-3"},
		{"xchg", rmw("xchg"), []any{int64(4)}, "-3", "4"},
		{"max that keeps", rmw("max"), []any{int64(2)}, "4", "4"},
		{"max that takes", rmw("max"), []any{int64(8)}, "4", "8"},
		{"min that keeps", rmw("min"), []any{int64(9)}, "8", "8"},
		{"min that takes", rmw("min"), []any{int64(-1)}, "8", "-1"},
		// unsigned: -1 is the largest
		{"umax", rmw("umax"), []any{int64(3)}, "-1", "-1"},
		{"umin", rmw("umin"), []any{int64(3)}, "-1", "3"},
		{"an unknown operation", rmw("fadd"), []any{int64(1)}, "!`llvmcall` requires the compiler", "3"},
		{"a mismatched operand", rmw("add"), []any{int32(1)}, "!", "3"},
		{"other IR", "ret void", nil, "!`llvmcall` requires the compiler", "3"},
	} {
		v, err := call(c.ir, c.xs...)
		check(t, c.name, v, err, c.want)
		check(t, c.name+": the cell", st.Fields[0], nil, c.cell)
	}
	// atomic_fence, without arguments
	v, err := vm.llvmcall([]any{"fence seq_cst\nret void\n", core.NothingType, at})
	check(t, "fence", v, err, "nothing")
	// too few arguments, or no pointer
	_, err = vm.llvmcall([]any{load, rt, at})
	check(t, "no pointer argument", nil, err, "!`llvmcall` requires the compiler")
	_, err = vm.llvmcall([]any{load, rt, at, int64(0)})
	check(t, "an integer for the pointer", nil, err, "!`llvmcall` requires the compiler")
	// a pointer to nothing loads nothing
	null := &Pointer{Type: p.Type}
	for _, ir := range []string{cas, rmw("add")} {
		xs := []any{ir, rt, at, null, int64(1)}
		if ir == cas {
			xs = append(xs, int64(2))
		}
		_, err = vm.llvmcall(xs)
		check(t, "NULL", nil, err, "!llvmcall: invalid pointer")
	}
	// a cas whose store is refused
	_, err = vm.llvmcall([]any{cas, rt, at, p, int64(3), int32(1)})
	check(t, "a cas of another type", nil, err, "!TypeError(:llvmcall")
}
