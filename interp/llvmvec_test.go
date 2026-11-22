package interp

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// vecTypes are the NTuple{n,VecElement{UInt64}} types of the three widths
// XoshiroSimd.jl covers.
func vecTypes(t *testing.T, vm *VM) map[int]*core.DataType {
	s := vm.NewSession()
	m := map[int]*core.DataType{}
	for _, n := range []int{4, 8, 16} {
		v, err := s.Eval(fmt.Sprintf("NTuple{%d,VecElement{UInt64}}", n))
		if err != nil {
			t.Fatal(err)
		}
		m[n] = v.(*core.DataType)
	}
	return m
}

func lanesOf(t *testing.T, v any, n int) []uint64 {
	t.Helper()
	l, ok := vecLanes(v, n)
	if !ok {
		t.Fatalf("not a vector of %d lanes: %v", n, v)
	}
	return l
}

// Each template of XoshiroSimd.jl, for every width, computes per lane what
// the IR says, checked here against an independent formula.
func TestLlvmVectorTemplates(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	types := vecTypes(t, vm)
	in := []uint64{0, 1, 0xFFFFFFFFFFFFFFFF, 0x0123456789ABCDEF, 0x8000000000000001, 0xDEADBEEFCAFEF00D, 1 << 63, 12345}
	other := []uint64{7, 0xF0F0F0F0F0F0F0F0, 3, 0xFFFFFFFFFFFFFFFF, 1 << 40, 0x0F0F0F0F0F0F0F0F, 5, 99}
	seen := 0
	for ir, tp := range llvmVecTemplates {
		n := tp.lanes
		x, y := make([]uint64, n), make([]uint64, n)
		for i := range x {
			x[i], y[i] = in[i%len(in)], other[i%len(other)]
		}
		xv, _ := vecValue(types[n], x)
		yv, _ := vecValue(types[n], y)
		args := []any{ir, types[n], core.AnyType, xv}
		var want func(i int) uint64
		switch {
		case strings.Contains(ir, "= add "):
			args, want = append(args, yv), func(i int) uint64 { return x[i] + y[i] }
		case strings.Contains(ir, "= xor "):
			args, want = append(args, yv), func(i int) uint64 { return x[i] ^ y[i] }
		case strings.Contains(ir, "= and "):
			args, want = append(args, yv), func(i int) uint64 { return x[i] & y[i] }
		case strings.Contains(ir, "= or "):
			args, want = append(args, yv), func(i int) uint64 { return x[i] | y[i] }
		case strings.Contains(ir, "= shl "):
			want = func(i int) uint64 { return x[i] << 17 }
		case strings.Contains(ir, "%shift = "):
			args, want = append(args, int64(45)), func(i int) uint64 { return x[i] >> 45 }
		case strings.Contains(ir, "x half"):
			want = func(i int) uint64 {
				var r uint64
				for j := uint(0); j < 4; j++ {
					h := uint16(x[i] >> (16 * j))
					r |= uint64(floatToHalf(float32(h>>5)/2048)) << (16 * j)
				}
				return r
			}
		case strings.Contains(ir, "x float>"):
			want = func(i int) uint64 {
				lo, hi := uint32(x[i])>>8, uint32(x[i]>>32)>>8
				return uint64(math.Float32bits(float32(lo)/(1<<24))) | uint64(math.Float32bits(float32(hi)/(1<<24)))<<32
			}
		case strings.Contains(ir, "x double>"):
			want = func(i int) uint64 { return math.Float64bits(float64(x[i]>>11) / (1 << 53)) }
		default:
			t.Fatalf("unclassified template %q", ir)
		}
		got, err := vm.llvmcall(args)
		if err != nil {
			t.Fatalf("%q: %v", ir, err)
		}
		for i, g := range lanesOf(t, got, n) {
			if g != want(i) {
				t.Errorf("%q lane %d: %#x, want %#x", ir, i, g, want(i))
			}
		}
		seen++
	}
	if seen != 3*9 {
		t.Errorf("%d templates, want 27", seen)
	}
	// the scalar lshr of the Bool path
	v, err := vm.llvmcall([]any{llvmScalarLshr + "\n", core.UInt64Type, core.AnyType, uint64(1 << 40), int64(38)})
	check(t, "scalar lshr", v, err, "0x0000000000000004")
}

// Anything that is not exactly a template is not this file's to answer.
func TestLlvmVectorRefusals(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	types := vecTypes(t, vm)
	var add, shl, lshr, f64 string
	for ir, tp := range llvmVecTemplates {
		if tp.lanes != 4 {
			continue
		}
		switch {
		case strings.Contains(ir, "= add "):
			add = ir
		case strings.Contains(ir, "= shl "):
			shl = ir
		case strings.Contains(ir, "%shift = "):
			lshr = ir
		case strings.Contains(ir, "x double>"):
			f64 = ir
		}
	}
	good, _ := vecValue(types[4], []uint64{1, 2, 3, 4})
	short, _ := vecValue(types[8], make([]uint64, 8))
	rt, at := any(types[4]), any(core.AnyType)
	elem := good.(*Tuple).Elems[0].(*Struct)
	badLanes := func(e any) any { return &Tuple{Type: types[4], Elems: []any{e, e, e, e}} }
	for _, c := range []struct {
		name string
		ir   string
		args []any
	}{
		{"unknown IR", "ret void", []any{"ret void", rt, at, good}},
		{"scalar lshr with a wrong type", llvmScalarLshr, []any{llvmScalarLshr, core.Int64Type, at, uint64(1), int64(1)}},
		{"scalar lshr with a wrong operand", llvmScalarLshr, []any{llvmScalarLshr, core.UInt64Type, at, int64(1), int64(1)}},
		{"scalar lshr with a wrong count", llvmScalarLshr, []any{llvmScalarLshr, core.UInt64Type, at, uint64(1), uint64(1)}},
		{"scalar lshr with too many operands", llvmScalarLshr, []any{llvmScalarLshr, core.UInt64Type, at, uint64(1), int64(1), 0}},
		{"no operand", f64, []any{f64, rt, at}},
		{"a scalar for a vector", f64, []any{f64, rt, at, int64(3)}},
		{"a vector of another width", f64, []any{f64, rt, at, short}},
		{"a lane that is not a struct", f64, []any{f64, rt, at, badLanes(uint64(1))}},
		{"a lane struct of two fields", f64, []any{f64, rt, at, badLanes(&Struct{Type: elem.Type, Fields: []any{uint64(1), uint64(2)}})}},
		{"a lane that is not a UInt64", f64, []any{f64, rt, at, badLanes(&Struct{Type: elem.Type, Fields: []any{int64(1)}})}},
		{"a unary template with an operand too many", f64, []any{f64, rt, at, good, good}},
		{"a binary template with one operand", add, []any{add, rt, at, good}},
		{"a binary template with a scalar", add, []any{add, rt, at, good, int64(1)}},
		{"a shift with a vector count", lshr, []any{lshr, rt, at, good, good}},
		{"a shift without count", lshr, []any{lshr, rt, at, good}},
		{"a unary template given a count", shl, []any{shl, rt, at, good, int64(1)}},
		{"a return type that is not a type", f64, []any{f64, 1, at, good}},
		{"a return type of another width", f64, []any{f64, types[8], at, good}},
		{"a return type of other elements", f64, []any{f64, core.AnyType, at, good}},
		{"a return type whose elements are not types", f64, []any{f64, &core.DataType{Name: types[4].Name, Parameters: []any{1, 2, 3, 4}}, at, good}},
	} {
		v, err := vm.llvmcall(c.args)
		check(t, c.name, v, err, "!`llvmcall` requires the compiler")
	}
}

// llvm.fshl.vNi64 rotates when both halves are the same vector.
func TestLlvmFshl(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	types := vecTypes(t, vm)
	for _, n := range []int{4, 8, 16} {
		x, s := make([]uint64, n), make([]uint64, n)
		for i := range x {
			x[i], s[i] = 0x0123456789ABCDEF*uint64(i+1), uint64([]int{45, 23, 0, 64, 1, 63, 128, 100}[i%8])
		}
		xv, _ := vecValue(types[n], x)
		sv, _ := vecValue(types[n], s)
		v, err := llvmFshl(n)(vm, []any{xv, xv, sv})
		if err != nil {
			t.Fatal(err)
		}
		for i, g := range lanesOf(t, v, n) {
			k := s[i] % 64
			want := x[i]<<k | x[i]>>((64-k)%64)
			if g != want {
				t.Errorf("n=%d lane %d: %#x, want %#x", n, i, g, want)
			}
		}
		// a funnel shift, two different halves
		y := make([]uint64, n)
		for i := range y {
			y[i] = 0xFFFF000000000000
		}
		yv, _ := vecValue(types[n], y)
		v, _ = llvmFshl(n)(vm, []any{xv, yv, sv})
		if got, k := lanesOf(t, v, n)[1], s[1]%64; got != x[1]<<k|y[1]>>(64-k) {
			t.Errorf("funnel: %#x", got)
		}
	}
	good, _ := vecValue(types[4], []uint64{1, 2, 3, 4})
	short, _ := vecValue(types[8], make([]uint64, 8))
	for _, c := range []struct {
		name string
		a    []any
	}{
		{"two operands", []any{good, good}},
		{"four operands", []any{good, good, good, good}},
		{"a scalar", []any{good, good, int64(1)}},
		{"another width", []any{good, good, short}},
	} {
		v, err := llvmFshl(4)(vm, c.a)
		check(t, c.name, v, err, "!not ported yet")
	}
	// the result takes the type of the operand: one that is not a vector type
	bad := &Tuple{Type: core.AnyType, Elems: good.(*Tuple).Elems}
	v, err := llvmFshl(4)(vm, []any{bad, bad, good})
	check(t, "a result type that is no tuple", v, err, "!not ported yet")
}

// A vector stored through a Ptr of its type lands lane after lane in the
// bytes of the memory, as julia's store does.
func TestVectorStoreIntoMemory(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	types := vecTypes(t, vm)
	s := vm.NewSession()
	mv, err := s.Eval("Memory{Float64}(undef, 16)")
	if err != nil {
		t.Fatal(err)
	}
	mem := mv.(*Memory)
	vec, _ := vecValue(types[4], []uint64{math.Float64bits(1.5), math.Float64bits(2.5), math.Float64bits(-3), math.Float64bits(4)})
	p := &Pointer{Type: vm.ptrType(types[4]), Target: mem}
	if err := vm.ptrStore("pointerset", p, vec, 2); err != nil {
		t.Fatal(err)
	}
	for i, w := range []float64{1.5, 2.5, -3, 4} {
		got := mem.Get(4 + i)
		if got != any(w) {
			t.Errorf("element %d: %v, want %v", 4+i, got, w)
		}
	}
	// an offset pointer
	q := &Pointer{Type: p.Type, Target: mem, Off: 8}
	if err := vm.ptrStore("pointerset", q, vec, 1); err != nil {
		t.Fatal(err)
	}
	if got := mem.Get(1); got != any(1.5) {
		t.Errorf("offset store: %v", got)
	}
	// past the end of the memory
	if err := vm.ptrStore("pointerset", p, vec, 5); err == nil {
		t.Error("a store past the end succeeded")
	}
	// lanes that are not primitive
	elem := vec.(*Tuple).Elems[0].(*Struct)
	nested := &Tuple{Type: types[4], Elems: []any{elem, elem, elem, &Struct{Type: elem.Type, Fields: []any{"x"}}}}
	err = vm.ptrStore("pointerset", p, nested, 1)
	check(t, "a lane that is no number", nil, err, "!not ported yet")
	flat := &Tuple{Type: types[4], Elems: []any{elem, elem, elem, int64(1)}}
	err = vm.ptrStore("pointerset", p, flat, 1)
	check(t, "a lane that is no struct", nil, err, "!not ported yet")
	two := &Tuple{Type: types[4], Elems: []any{elem, elem, elem, &Struct{Type: elem.Type, Fields: []any{uint64(1), uint64(2)}}}}
	err = vm.ptrStore("pointerset", p, two, 1)
	check(t, "a lane of two fields", nil, err, "!not ported yet")
}
