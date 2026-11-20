package interp

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// Get and Set: the Julia-Go value table (#60). Cells here use Core's names
// (the kernel is over a Boot()-only image); TestKernelBase runs the table on
// julia's Base.

func cell(t *testing.T, k *Kernel, src string) string {
	t.Helper()
	r := k.Eval(context.Background(), src)
	if r.Err != nil {
		t.Fatalf("%s\n  %v", src, r.Err)
	}
	return r.Value
}

func mustGet(t *testing.T, k *Kernel, name string) any {
	t.Helper()
	v, ok, err := k.Get(name)
	if err != nil || !ok {
		t.Fatalf("Get(%q): %v %v %v", name, v, ok, err)
	}
	return v
}

func TestKernelScalars(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// Set -> a cell reads it -> a cell changes it -> Get
	if err := k.Set("n", int64(41)); err != nil {
		t.Fatal(err)
	}
	if got := cell(t, k, "Core.Intrinsics.add_int(n, 1)"); got != "42" {
		t.Errorf("a cell reads n: %s", got)
	}
	cell(t, k, "n = Core.Intrinsics.add_int(n, 1)")
	if got := mustGet(t, k, "n"); got != int64(42) {
		t.Errorf("Get n = %v (%T)", got, got)
	}
	// an int is an Int64
	if err := k.Set("m", 7); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, k, "m"); got != int64(7) {
		t.Errorf("Get m = %v (%T)", got, got)
	}
	// Float64
	k.Set("f", 2.5)
	if got := cell(t, k, "Core.Intrinsics.add_float(f, 1.0)"); got != "3.5" {
		t.Errorf("a cell reads f: %s", got)
	}
	cell(t, k, "f = Core.Intrinsics.mul_float(f, 2.0)")
	if got := mustGet(t, k, "f"); got != 5.0 {
		t.Errorf("Get f = %v", got)
	}
	// every bit of a Float64 is kept: a NaN payload, -0.0, and the infinities
	for _, bits := range []uint64{0x7ff8000000000123, 0xfff0000000000abc, 1 << 63, 0, 0x7ff0000000000000, 0xfff0000000000000, 1} {
		x := math.Float64frombits(bits)
		k.Set("x", x)
		cell(t, k, "y = x") // through a cell and back
		for _, name := range []string{"x", "y"} {
			if got := mustGet(t, k, name).(float64); math.Float64bits(got) != bits {
				t.Errorf("%s: %#x became %#x", name, bits, math.Float64bits(got))
			}
		}
	}
	// Bool and String
	k.Set("b", true)
	if got := cell(t, k, "Core.ifelse(b, 1, 2)"); got != "1" {
		t.Errorf("a cell reads b: %s", got)
	}
	cell(t, k, "b = false")
	if got := mustGet(t, k, "b"); got != false {
		t.Errorf("Get b = %v", got)
	}
	k.Set("s", "héllo\x00wörld")
	if got := cell(t, k, "s"); got != `"héllo\0wörld"` {
		t.Errorf("a cell reads s: %s", got)
	}
	cell(t, k, `s2 = "from a cell"`)
	if got := mustGet(t, k, "s2"); got != "from a cell" {
		t.Errorf("Get s2 = %v", got)
	}
	// a name replaced, with another type
	k.Set("n", "now a string")
	if got := mustGet(t, k, "n"); got != "now a string" {
		t.Errorf("a replaced binding: %v", got)
	}
}

func TestKernelVectors(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// elements through Core's memory builtins: element i of a vector v
	elem := func(name string, i int) string {
		return cell(t, k, "Core.memoryrefget(Core.memoryrefnew("+name+".ref, "+string(rune('0'+i))+", true), :not_atomic, true)")
	}
	set := func(name string, i int, v string) {
		cell(t, k, "Core.memoryrefset!(Core.memoryrefnew("+name+".ref, "+string(rune('0'+i))+", true), "+v+", :not_atomic, true)")
	}
	fs := []float64{1.5, math.Float64frombits(0x7ff8000000000123), math.Copysign(0, -1)}
	if err := k.Set("v", fs); err != nil {
		t.Fatal(err)
	}
	if elem("v", 1) != "1.5" || elem("v", 3) != "-0.0" {
		t.Errorf("a cell reads v: %s %s", elem("v", 1), elem("v", 3))
	}
	if got := cell(t, k, "v.size"); got != "(3,)" {
		t.Errorf("size: %s", got)
	}
	// the Go slice does not alias julia's memory, in either direction
	fs[0] = 99
	if elem("v", 1) != "1.5" {
		t.Error("a change of the Go slice reached julia")
	}
	set("v", 1, "7.25")
	got := mustGet(t, k, "v").([]float64)
	if got[0] != 7.25 || math.Float64bits(got[1]) != 0x7ff8000000000123 || math.Float64bits(got[2]) != 1<<63 {
		t.Errorf("Get v = %v", got)
	}
	got[0] = -1
	if again := mustGet(t, k, "v").([]float64); again[0] != 7.25 {
		t.Error("a change of what Get returned reached julia")
	}
	// Int64, and an empty vector
	k.Set("w", []int64{10, 20, 30})
	set("w", 2, "21")
	if got := mustGet(t, k, "w"); len(got.([]int64)) != 3 || got.([]int64)[1] != 21 {
		t.Errorf("Get w = %v", got)
	}
	k.Set("e", []float64{})
	if got := mustGet(t, k, "e").([]float64); len(got) != 0 {
		t.Errorf("an empty vector: %v", got)
	}
	// a nil slice is an empty vector
	k.Set("nl", []int64(nil))
	if got := mustGet(t, k, "nl").([]int64); len(got) != 0 {
		t.Errorf("a nil slice: %v", got)
	}
}

func TestKernelMatrices(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// 2 by 3, column-major: columns (1,2) (3,4) (5,6)
	m := Matrix[float64]{Rows: 2, Cols: 3, Data: []float64{1, 2, 3, 4, 5, 6}}
	if err := k.Set("A", m); err != nil {
		t.Fatal(err)
	}
	if got := cell(t, k, "A.size"); got != "(2, 3)" {
		t.Errorf("size: %s", got)
	}
	// element (2, 3) is the 6th in memory order, (1, 2) the 3rd
	at := func(i int) string {
		return cell(t, k, "Core.memoryrefget(Core.memoryrefnew(A.ref, "+string(rune('0'+i))+", true), :not_atomic, true)")
	}
	if at(3) != "3.0" || at(6) != "6.0" {
		t.Errorf("column-major: %s %s", at(3), at(6))
	}
	m.Data[0] = 100 // no alias
	if at(1) != "1.0" {
		t.Error("a change of the Go matrix reached julia")
	}
	cell(t, k, "Core.memoryrefset!(Core.memoryrefnew(A.ref, 4, true), 40.0, :not_atomic, true)")
	got := mustGet(t, k, "A").(Matrix[float64])
	if got.Rows != 2 || got.Cols != 3 || got.At(1, 1) != 40 || got.At(0, 2) != 5 || got.At(1, 2) != 6 {
		t.Errorf("Get A = %+v", got)
	}
	// Int64, and an empty matrix
	k.Set("B", Matrix[int64]{Rows: 3, Cols: 1, Data: []int64{7, 8, 9}})
	if b := mustGet(t, k, "B").(Matrix[int64]); b.Rows != 3 || b.Cols != 1 || b.Data[2] != 9 {
		t.Errorf("Get B = %+v", b)
	}
	k.Set("Z", Matrix[float64]{Rows: 0, Cols: 4})
	if z := mustGet(t, k, "Z").(Matrix[float64]); z.Rows != 0 || z.Cols != 4 || len(z.Data) != 0 {
		t.Errorf("an empty matrix: %+v", z)
	}
	// dimensions that do not fit the data are refused, whatever they overflow to
	for name, bad := range map[string]Matrix[float64]{
		"too few": {Rows: 2, Cols: 3, Data: []float64{1}}, "negative": {Rows: -1, Cols: -1, Data: []float64{1}},
		"zero rows": {Rows: 0, Cols: 3, Data: []float64{1}}, "wrapping": {Rows: math.MaxInt/2 + 1, Cols: 4, Data: []float64{1, 2, 3, 4}},
	} {
		if err := k.Set("bad", bad); !errors.Is(err, ErrKernelType) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestKernelConversionErrors(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// Go values the table does not take: named, never converted silently
	for _, v := range []any{int32(1), uint8(2), float32(1), []string{"a"}, map[string]int{}, nil, struct{}{}, [2]float64{}, []float32{1}, [][]float64{{1}}} {
		err := k.Set("x", v)
		var ce *ConversionError
		if !errors.Is(err, ErrKernelType) || !errors.As(err, &ce) || ce.Go == "" || ce.Julia != "" || err.Error() == "" {
			t.Errorf("Set %T: %v", v, err)
		}
	}
	// julia values it does not return
	cell(t, k, `sym = :a`)
	cell(t, k, `tup = (1, 2)`)
	cell(t, k, `nothing_ = nothing`)
	cell(t, k, `i32 = Core.Intrinsics.trunc_int(Int32, 5)`)
	cell(t, k, `f32 = Core.Intrinsics.fptrunc(Float32, 1.5)`)
	cell(t, k, `anyvec = Core.Array{Any, 1}(undef, 0)`)
	cell(t, k, `f32vec = Core.Array{Float32, 1}(undef, 0)`)
	cell(t, k, `cube = Core.Array{Float64, 3}(undef, 0, 0, 0)`)
	for name, want := range map[string]string{"sym": "Symbol", "tup": "Tuple{Int64, Int64}", "nothing_": "Nothing", "i32": "Int32", "f32": "Float32",
		"anyvec": "Vector{Any}", "f32vec": "Vector{Float32}", "cube": "Array{Float64, 3}"} {
		v, ok, err := k.Get(name)
		var ce *ConversionError
		if v != nil || ok || !errors.Is(err, ErrKernelType) || !errors.As(err, &ce) || ce.Julia != want || ce.Go != "" || err.Error() == "" {
			t.Errorf("Get %s: %v %v %v, want the type %s", name, v, ok, err, want)
		}
	}
	// a name that is not bound is not an error
	if v, ok, err := k.Get("unbound_name"); v != nil || ok || err != nil {
		t.Errorf("an unbound name: %v %v %v", v, ok, err)
	}
	// a declared name with no value is not bound either
	cell(t, k, `global declared_only`)
	if v, ok, err := k.Get("declared_only"); v != nil || ok || err != nil {
		t.Errorf("a declared name: %v %v %v", v, ok, err)
	}
}

// Arrays that are not what julia makes (the interpreter's own guards).
func TestKernelMalformedArrays(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	vm := k.VM()
	mem := func(elem *core.DataType, elems ...any) *Memory {
		return newMemory(must(core.Apply(core.GenericMemoryType, "not_atomic", elem, core.CPU)).(*core.DataType), elems)
	}
	arr := func(elem *core.DataType, nd int64, ref any, size any) *Struct {
		return &Struct{Type: must(core.Apply(core.ArrayType, elem, nd)).(*core.DataType), Fields: []any{ref, size}}
	}
	ref := func(m *Memory, off int) *MemoryRef { return &MemoryRef{Mem: m, Off: off} }
	f64 := core.Float64Type
	good := mem(f64, 1.0, 2.0, 3.0)
	cases := map[string]*Struct{
		"fields of other types":    arr(f64, 1, int64(1), int64(2)),
		"a size that is no tuple":  arr(f64, 1, ref(good, 0), int64(3)),
		"a size of the wrong rank": arr(f64, 1, ref(good, 0), vm.newTuple([]any{int64(1), int64(3)})),
		"a rank of three":          arr(f64, 3, ref(good, 0), vm.newTuple([]any{int64(1), int64(1), int64(3)})),
		"a size that is no number": arr(f64, 1, ref(good, 0), vm.newTuple([]any{"3"})),
		"a negative size":          arr(f64, 1, ref(good, 0), vm.newTuple([]any{int64(-1)})),
		"more than the memory":     arr(f64, 1, ref(good, 0), vm.newTuple([]any{int64(4)})),
		"more than a matrix":       arr(f64, 2, ref(good, 0), vm.newTuple([]any{int64(2), int64(2)})),
		"past the offset":          arr(f64, 1, ref(good, 2), vm.newTuple([]any{int64(2)})),
		// (a Memory{Float64} is typed: a slot that is not a Float64, or undefined, cannot
		// be made, so the cases of the boxed Int64 kind are the ones left)
		"an Int64 slot that is not":        arr(core.Int64Type, 1, ref(mem(core.Int64Type, int64(1), 2.5), 0), vm.newTuple([]any{int64(2)})),
		"an Int64 matrix slot that is not": arr(core.Int64Type, 2, ref(mem(core.Int64Type, 2.5), 0), vm.newTuple([]any{int64(1), int64(1)})),
		"an undefined Int64 slot":          arr(core.Int64Type, 1, ref(mem(core.Int64Type, int64(1), undef), 0), vm.newTuple([]any{int64(2)})),
	}
	for name, a := range cases {
		if _, err := vm.toGo(a); !errors.Is(err, ErrKernelType) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// and the well-formed ones are read through their offset
	if got, err := vm.toGo(arr(f64, 1, ref(good, 1), vm.newTuple([]any{int64(2)}))); err != nil || len(got.([]float64)) != 2 || got.([]float64)[0] != 2 {
		t.Errorf("an offset: %v %v", got, err)
	}
	if got, err := vm.toGo(arr(core.Int64Type, 2, ref(mem(core.Int64Type, int64(1), int64(2)), 0), vm.newTuple([]any{int64(1), int64(2)}))); err != nil || got.(Matrix[int64]).At(0, 1) != 2 {
		t.Errorf("an Int64 matrix: %v %v", got, err)
	}
}

// An empty matrix is read whichever dimension is the zero (#76: the running
// product was compared with the memory before the 0 came, so 3x0 was refused),
// and a product that overflows is refused, not wrapped.
func TestKernelGetEmptyAndOverflowingMatrices(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	vm := k.VM()
	mem := func(elems ...any) *Memory {
		return newMemory(must(core.Apply(core.GenericMemoryType, "not_atomic", core.Float64Type, core.CPU)).(*core.DataType), elems)
	}
	mat := func(m *Memory, off int, dims ...int64) *Struct {
		size := make([]any, len(dims))
		for i, d := range dims {
			size[i] = d
		}
		return &Struct{Type: must(core.Apply(core.ArrayType, core.Float64Type, int64(len(dims)))).(*core.DataType),
			Fields: []any{&MemoryRef{Mem: m, Off: off}, vm.newTuple(size)}}
	}
	floats := func(n int) []any {
		out := make([]any, n)
		for i := range out {
			out[i] = float64(i)
		}
		return out
	}
	for name, c := range map[string]struct {
		a          *Struct
		rows, cols int
	}{
		"3x0":                 {mat(mem(), 0, 3, 0), 3, 0},
		"0x3":                 {mat(mem(), 0, 0, 3), 0, 3},
		"0x0":                 {mat(mem(), 0, 0, 0), 0, 0},
		"3x0 over a memory":   {mat(mem(floats(5)...), 5, 3, 0), 3, 0},
		"5x1":                 {mat(mem(floats(5)...), 0, 5, 1), 5, 1},
		"1x5":                 {mat(mem(floats(5)...), 0, 1, 5), 1, 5},
		"2x3 through offset":  {mat(mem(floats(8)...), 2, 2, 3), 2, 3},
		"the whole memory":    {mat(mem(floats(6)...), 0, 2, 3), 2, 3},
		"zero after a big 1d": {mat(mem(), 0, math.MaxInt/2+1, 0), math.MaxInt/2 + 1, 0},
	} {
		got, err := vm.toGo(c.a)
		m, ok := got.(Matrix[float64])
		if err != nil || !ok || m.Rows != c.rows || m.Cols != c.cols || len(m.Data) != c.rows*c.cols {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
	// the elements are the ones the offset names
	if got, _ := vm.toGo(mat(mem(floats(8)...), 2, 2, 3)); got.(Matrix[float64]).At(1, 2) != 7 {
		t.Errorf("through the offset: %v", got)
	}
	for name, a := range map[string]*Struct{
		"a product that wraps to 0":      mat(mem(), 0, math.MaxInt/2+1, 4),
		"a product that wraps to little": mat(mem(floats(4)...), 0, math.MaxInt/2+3, 4),
		"max by two":                     mat(mem(), 0, math.MaxInt, 2),
		"more than the memory":           mat(mem(floats(5)...), 0, 3, 2),
		"an offset past the memory":      mat(mem(floats(5)...), 6, 0, 0),
		"a negative offset":              mat(mem(floats(5)...), -1, 1, 1),
		"the offset leaves too few":      mat(mem(floats(5)...), 4, 1, 2),
	} {
		if got, err := vm.toGo(a); !errors.Is(err, ErrKernelType) {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
}

func TestKernelSetRefusals(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// names a global cannot have
	for _, name := range []string{"", "1x", "a b", "x-y", "!x", "a.b", "é́\x00"} {
		if err := k.Set(name, int64(1)); !errors.Is(err, ErrKernelName) {
			t.Errorf("Set(%q): %v", name, err)
		}
	}
	for _, name := range []string{"α", "_x", "x1", "x!", "Ωmega"} {
		if err := k.Set(name, int64(1)); err != nil {
			t.Errorf("Set(%q): %v", name, err)
		}
	}
	// a binding that cannot be assigned: a constant
	cell(t, k, "const c = 1")
	if err := k.Set("c", int64(2)); err == nil {
		t.Error("Set of a constant")
	}
	if got := mustGet(t, k, "c"); got != int64(1) {
		t.Errorf("the constant after the refusal: %v", got)
	}
	// an array over VM.MaxMemoryElems
	k.VM().MaxMemoryElems = 4
	if err := k.Set("big", make([]float64, 5)); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a big vector: %v", err)
	}
	if err := k.Set("big", Matrix[int64]{Rows: 5, Cols: 1, Data: make([]int64, 5)}); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("a big matrix: %v", err)
	}
	if err := k.Set("ok", make([]float64, 4)); err != nil {
		t.Errorf("a vector at the limit: %v", err)
	}
}

// TestKernelValuesBase is the table on julia's Base: a cell uses what Set gave
// it with Base's own functions, and Get reads what a cell made. It builds Base
// unless JLGO_KERNEL_IMAGE names an image (JLGO_BASE=1).
func TestKernelValuesBase(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 (and optionally JLGO_KERNEL_IMAGE, JLGO_IMAGE_VERSION)")
	}
	tag := os.Getenv("JLGO_IMAGE_VERSION")
	if tag == "" {
		tag = "v0.0.1"
	}
	var img []byte
	if file := os.Getenv("JLGO_KERNEL_IMAGE"); file != "" {
		fakeReleaseVersion(t, tag)
		var err error
		if img, err = os.ReadFile(file); err != nil {
			t.Fatal(err)
		}
	} else {
		img = buildBaseImage(t, tag)
	}
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	// Float64 and Int64 vectors: Set, a cell reads and changes, Get
	k.Set("v", []float64{1, 2, 3.5})
	if got := cell(t, k, "sum(v)"); got != "6.5" {
		t.Errorf("sum(v) = %s", got)
	}
	cell(t, k, "v[2] = 20.0; push!(v, 4.0)")
	if got := mustGet(t, k, "v").([]float64); len(got) != 4 || got[1] != 20 || got[3] != 4 {
		t.Errorf("Get v = %v", got)
	}
	k.Set("w", []int64{3, 1, 2})
	cell(t, k, "w2 = sort(w)")
	if got := mustGet(t, k, "w2").([]int64); got[0] != 1 || got[2] != 3 {
		t.Errorf("Get w2 = %v", got)
	}
	if got := mustGet(t, k, "w").([]int64); got[0] != 3 {
		t.Errorf("sort made a copy; w changed: %v", got)
	}
	// matrices: Set a 2 by 3, a cell multiplies, Get
	k.Set("A", Matrix[float64]{Rows: 2, Cols: 3, Data: []float64{1, 2, 3, 4, 5, 6}})
	if got := cell(t, k, "size(A)"); got != "(2, 3)" {
		t.Errorf("size(A) = %s", got)
	}
	if got := cell(t, k, "A[2, 3]"); got != "6.0" {
		t.Errorf("A[2, 3] = %s", got)
	}
	// no BLAS kernel computes yet (#33): no A * x
	cell(t, k, "B = A .* 2.0; C = A[:, 1] .+ A[:, 3]")
	if b := mustGet(t, k, "B").(Matrix[float64]); b.At(1, 2) != 12 || b.At(0, 1) != 6 {
		t.Errorf("Get B = %+v", b)
	}
	if c := mustGet(t, k, "C").([]float64); len(c) != 2 || c[0] != 6 || c[1] != 8 {
		t.Errorf("Get C = %v", c)
	}
	k.Set("I", Matrix[int64]{Rows: 2, Cols: 2, Data: []int64{1, 0, 0, 1}})
	cell(t, k, "J = I .+ 1")
	if j := mustGet(t, k, "J").(Matrix[int64]); j.At(0, 0) != 2 || j.At(0, 1) != 1 {
		t.Errorf("Get J = %+v", j)
	}
	// scalars and strings through Base
	k.Set("s", "héllo")
	if got := cell(t, k, "reverse(s)"); got != `"olléh"` { // not uppercase: utf8proc is M5
		t.Errorf("reverse(s) = %s", got)
	}
	cell(t, k, `r = string(s, " ", length(s))`)
	if got := mustGet(t, k, "r"); got != "héllo 5" {
		t.Errorf("Get r = %v", got)
	}
	// bits kept through Base's own arithmetic
	k.Set("nan", math.Float64frombits(0x7ff8000000000123))
	cell(t, k, "nan2 = nan")
	if got := mustGet(t, k, "nan2").(float64); math.Float64bits(got) != 0x7ff8000000000123 {
		t.Errorf("NaN payload: %#x", math.Float64bits(got))
	}
	// unsupported types, named as julia shows them
	cell(t, k, "d = Dict(1 => 2)")
	if _, _, err := k.Get("d"); !errors.Is(err, ErrKernelType) {
		t.Errorf("Get of a Dict: %v", err)
	} else if ce := err.(*ConversionError); ce.Julia != "Dict{Int64, Int64}" {
		t.Errorf("the type named: %s", ce.Julia)
	}
	cell(t, k, "u = UInt8[1, 2]")
	if _, _, err := k.Get("u"); !errors.Is(err, ErrKernelType) {
		t.Errorf("Get of a Vector{UInt8}: %v", err)
	}
}

// holdKernel starts a cell that calls a ccall the test holds, and returns once
// the cell is inside it: the kernel is busy from then until release is called,
// which lets the cell finish and returns its result. The overlap is made by two
// channels, not by sleeping or by asking until a call happens to land.
func holdKernel(k *Kernel) (release func() Result) {
	inside, free := make(chan struct{}), make(chan struct{})
	k.VM().shims["jlgo_hold"] = func(vm *VM, a []any) (any, error) {
		close(inside)
		<-free
		return int64(7), nil
	}
	result := make(chan Result, 1)
	go func() { result <- k.Eval(context.Background(), `hold() = ccall(:jlgo_hold, Int, ()); hold()`) }()
	<-inside
	return func() Result {
		close(free)
		return <-result
	}
}

// Get and Set are refused while a cell runs, as Eval is, and after Close.
func TestKernelGetSetBusyAndClosed(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	k.Set("n", int64(1))
	release := holdKernel(k)
	if _, _, err := k.Get("n"); !errors.Is(err, ErrKernelBusy) {
		t.Errorf("Get while a cell runs: %v", err)
	}
	if err := k.Set("n", int64(2)); !errors.Is(err, ErrKernelBusy) {
		t.Errorf("Set while a cell runs: %v", err)
	}
	// a refusal is an error, never a plain false that would read as "not bound"
	if has, err := k.Has("n"); has || !errors.Is(err, ErrKernelBusy) {
		t.Errorf("Has while a cell runs: %v %v", has, err)
	}
	if ty, ok, err := k.TypeOf("n"); ty != "" || ok || !errors.Is(err, ErrKernelBusy) {
		t.Errorf("TypeOf while a cell runs: %q %v %v", ty, ok, err)
	}
	if r := release(); r.Err != nil || r.Value != "7" {
		t.Fatalf("the held cell: %+v", r)
	}
	// the cell has returned: all four are answered, and the Set that was
	// refused changed nothing
	if got := mustGet(t, k, "n"); got != int64(1) {
		t.Errorf("n after the refused Set: %v", got)
	}
	if has, err := k.Has("n"); !has || err != nil {
		t.Errorf("Has after the cell: %v %v", has, err)
	}
	if ty, ok, err := k.TypeOf("n"); ty != "Int64" || !ok || err != nil {
		t.Errorf("TypeOf after the cell: %q %v %v", ty, ok, err)
	}
	if err := k.Set("n", int64(5)); err != nil || mustGet(t, k, "n") != int64(5) {
		t.Errorf("Set after the cell: %v", err)
	}
	k.Close()
	if _, _, err := k.Get("n"); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Get after Close: %v", err)
	}
	if err := k.Set("n", int64(3)); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Set after Close: %v", err)
	}
	if has, err := k.Has("n"); has || !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Has after Close: %v %v", has, err)
	}
	if _, _, err := k.TypeOf("n"); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("TypeOf after Close: %v", err)
	}
	// and the kernel is free again once a call has returned
	k2 := bootKernel(t, KernelOptions{})
	k2.Get("nothing")
	if r := k2.Eval(context.Background(), "1"); r.Err != nil {
		t.Errorf("Eval after Get: %v", r.Err)
	}
}
