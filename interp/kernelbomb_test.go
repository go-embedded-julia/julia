package interp

import (
	"errors"
	"fmt"
	"math"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
)

// totalAlloc is the bytes the process has allocated so far: exact under load,
// unlike a time, and unaffected by when the collector runs.
func totalAlloc() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.TotalAlloc
}

// allocatedBy is the bytes f allocates.
func allocatedBy(f func()) uint64 {
	before := totalAlloc()
	f()
	return totalAlloc() - before
}

// A Set that is refused must cost a few words, not len(x) boxed elements: the
// size is checked before the slice is converted (#69). The slices are built
// before the measure; under the old order each refusal allocated about 24
// bytes per element (an interface value and its boxed float), here 1<<20
// elements would be 24 MiB, far over the memory limit set below.
func TestKernelSetBomb(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	k.VM().MaxMemoryElems = 1000
	const n = 1 << 20
	floats, ints := make([]float64, n), make([]int64, n)
	// a few KiB for the error and its formatting, nothing per element
	const budget = 16 << 10
	old := debug.SetMemoryLimit(int64(totalAliveHeap()) + 8<<20)
	defer debug.SetMemoryLimit(old)
	for name, v := range map[string]any{
		"[]float64":       floats,
		"[]int64":         ints,
		"Matrix[float64]": Matrix[float64]{Rows: 1024, Cols: 1024, Data: floats},
		"Matrix[int64]":   Matrix[int64]{Rows: n, Cols: 1, Data: ints},
	} {
		var err error
		got := allocatedBy(func() { err = k.Set("bomb", v) })
		if !errors.Is(err, ErrMemoryLimit) {
			t.Errorf("%s: %v, want ErrMemoryLimit", name, err)
		}
		if got > budget {
			t.Errorf("%s: a refused Set allocated %d bytes, want at most %d", name, got, budget)
		}
	}
	// nothing was bound
	if has, err := k.Has("bomb"); has || err != nil {
		t.Errorf("a refused Set bound the name: %v %v", has, err)
	}
	// dimensions that overflow, or do not match Data, are refused as a bad
	// shape before anything is allocated, and are not a memory-limit error
	for name, m := range map[string]Matrix[float64]{
		"overflow":   {Rows: math.MaxInt/2 + 1, Cols: 4, Data: floats},
		"mismatch":   {Rows: 3, Cols: 3, Data: floats},
		"negative":   {Rows: -1, Cols: -n, Data: floats},
		"zero cols":  {Rows: n, Cols: 0, Data: floats},
		"huge empty": {Rows: math.MaxInt, Cols: 2},
	} {
		var err error
		got := allocatedBy(func() { err = k.Set("bomb", m) })
		var ce *ConversionError
		if !errors.As(err, &ce) || ce.Why == "" || errors.Is(err, ErrMemoryLimit) {
			t.Errorf("%s: %v, want a ConversionError with a reason", name, err)
		}
		if got > budget {
			t.Errorf("%s: allocated %d bytes, want at most %d", name, got, budget)
		}
	}
	// and a slice within the bound still goes through
	k.VM().MaxMemoryElems = n
	if err := k.Set("fits", floats[:1000]); err != nil {
		t.Errorf("a slice within the bound: %v", err)
	}
}

// totalAliveHeap is the live heap after a collection.
func totalAliveHeap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// The errors state the table and the type they got, and do not contradict
// themselves: a Matrix with a bad shape is a type the table takes.
func TestKernelSetErrorMessages(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	type vec []float64
	for _, v := range []any{[]float32{1}, vec{1}, map[string]int{}, nil} {
		err := k.Set("x", v)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		if !errors.Is(err, ErrKernelType) || !strings.Contains(msg, acceptedGo) || !strings.Contains(msg, fmt.Sprintf("%T", v)) {
			t.Errorf("Set %T: %q", v, msg)
		}
	}
	err := k.Set("m", Matrix[float64]{Rows: 2, Cols: 3, Data: []float64{1}})
	msg := err.Error()
	if !errors.Is(err, ErrKernelType) || !strings.Contains(msg, "Matrix[float64]") || !strings.Contains(msg, "Rows 2 by Cols 3") ||
		!strings.Contains(msg, "1 elements") || strings.Contains(msg, "no julia value") || strings.Contains(msg, "does not convert") {
		t.Errorf("a bad shape: %q", msg)
	}
	// the Get side is unchanged
	cell(t, k, `t = (1, 2)`)
	_, _, err = k.Get("t")
	if err == nil || !strings.Contains(err.Error(), "Tuple{Int64, Int64}") {
		t.Errorf("Get of a tuple: %v", err)
	}
}

// Has and TypeOf answer without copying.
func TestKernelHasTypeOf(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	k.Set("n", int64(3))
	k.Set("v", make([]float64, 1<<20))
	k.Set("A", Matrix[int64]{Rows: 2, Cols: 2, Data: []int64{1, 2, 3, 4}})
	cell(t, k, `tup = (1, 2)`)
	cell(t, k, `global declared_only`)
	for name, want := range map[string]string{
		"n": "Int64", "v": "Vector{Float64}", "A": "Matrix{Int64}", "tup": "Tuple{Int64, Int64}",
	} {
		if has, err := k.Has(name); !has || err != nil {
			t.Errorf("Has(%s): %v %v", name, has, err)
		}
		if ty, ok, err := k.TypeOf(name); ty != want || !ok || err != nil {
			t.Errorf("TypeOf(%s): %q %v %v, want %q", name, ty, ok, err, want)
		}
	}
	// a type Get refuses, TypeOf still names
	if _, _, err := k.Get("tup"); !errors.Is(err, ErrKernelType) {
		t.Errorf("Get of a tuple: %v", err)
	}
	for _, name := range []string{"unbound_name", "declared_only", "not a name"} {
		if has, err := k.Has(name); has || err != nil {
			t.Errorf("Has(%q): %v %v", name, has, err)
		}
		if ty, ok, err := k.TypeOf(name); ty != "" || ok || err != nil {
			t.Errorf("TypeOf(%q): %q %v %v", name, ty, ok, err)
		}
	}
	// no copy of the 8 MiB vector: a few KiB at most
	const budget = 4 << 10
	if got := allocatedBy(func() { k.Has("v") }); got > budget {
		t.Errorf("Has allocated %d bytes", got)
	}
	if got := allocatedBy(func() { k.TypeOf("v") }); got > budget {
		t.Errorf("TypeOf allocated %d bytes", got)
	}
}
