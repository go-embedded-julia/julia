package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// Kernel.Reset against a fresh kernel (#93): the oracle is a kernel just made
// from the same image, which a Reset kernel must answer as, cell for cell.

// resetCorpus is what a notebook does that Reset has to undo: define, redefine,
// overload a method of Base, make a struct and a method on it, import a stdlib,
// draw random numbers (unseeded first, then seeded), mutate a global of Base,
// delete a method of Base, and fail.
var resetCorpus = []string{
	`rand()`,
	`x = 10; y = x + 1`,
	`f(x) = x + 1; f(2)`,
	`f(x) = x + 2; f(2)`,
	`struct P; a::Int; end; P(3)`,
	`Base.show(io::IO, ::MIME"text/plain", p::P) = print(io, "P!"); P(4)`,
	`Base.show(io::IO, x::Int8) = print(io, "int8!"); Int8(3)`,
	`using LinearAlgebra; norm([3.0, 4.0])`,
	`using Random; Random.seed!(42); rand(3)`,
	`rand()`,
	`push!(Base.LOAD_PATH, "/zz"); length(Base.LOAD_PATH)`,
	`const K = 7; K`,
	`Base.delete_method(first(methods(iseven, (Int64,)))); try iseven(2) catch e; "gone" end`,
	`undefined_name + 1`,
	`error("boom")`,
	`module M; g() = 1; end; M.g()`,
	`Base.isnumeric(c::Char) = true; isnumeric('a')`,
	`println("printed"); 5`,
}

// resetProbes is run on a kernel that has been reset and on a fresh one.
var resetProbes = []string{
	`rand()`,
	`isdefined(Main, :x)`,
	`isdefined(Main, :P)`,
	`isdefined(Main, :M)`,
	`isdefined(Main, :K)`,
	`Int8(3)`,
	`isnumeric('a')`,
	`iseven(2)`,
	`length(Base.LOAD_PATH)`,
	`length(names(Main))`,
	`length(methods(show))`,
	`using LinearAlgebra; norm([3.0, 4.0])`,
	`f = 1; f`,
	`rand(2)`,
	`1 + 1`,
}

func resetText(r Result) string {
	s := r.Value + "|" + r.Stdout + "|" + r.Stderr
	if r.Err != nil {
		s += "|ERR " + r.Err.Error()
	}
	return s
}

func resetRun(t *testing.T, k *Kernel, cells []string) []string {
	t.Helper()
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = resetText(k.Eval(context.Background(), c))
	}
	return out
}

func resetSame(t *testing.T, what string, cells, got, want []string) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: cell %q\n   got %q\n  want %q", what, cells[i], got[i], want[i])
		}
	}
}

func resetImage(t *testing.T) []byte {
	t.Helper()
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 (and optionally JLGO_BASE_IMAGE, built at JLGO_IMAGE_VERSION=v0.0.1)")
	}
	if path := os.Getenv("JLGO_BASE_IMAGE"); path != "" {
		img, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		fakeReleaseVersion(t, baseImageTag)
		return img
	}
	return buildBaseImage(t, baseImageTag)
}

func resetKernel(t *testing.T, img []byte) *Kernel {
	t.Helper()
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 5 * time.Minute, MaxAlloc: -1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k
}

// TestKernelResetBase: a kernel after the corpus and a Reset answers the probes
// as a fresh kernel does (the unseeded rand() first of all), and runs the
// corpus again as a fresh kernel ran it; twice over.
func TestKernelResetBase(t *testing.T) {
	img := resetImage(t)
	fresh := resetKernel(t, img)
	wantProbes := resetRun(t, fresh, resetProbes)
	freshC := resetKernel(t, img)
	wantCorpus := resetRun(t, freshC, resetCorpus)

	k := resetKernel(t, img)
	resetSame(t, "first corpus", resetCorpus, resetRun(t, k, resetCorpus), wantCorpus)
	for round := 1; round <= 2; round++ {
		t0 := time.Now()
		info, err := k.Reset()
		d := time.Since(t0)
		if err != nil || info.Reloaded {
			t.Fatalf("Reset %d: %+v, %v", round, info, err)
		}
		t.Logf("Reset %d: %v for %d writes and %d method tables", round, d, info.Undone, info.Tables)
		resetSame(t, "probes after a reset", resetProbes, resetRun(t, k, resetProbes), wantProbes)
		if _, err := k.Reset(); err != nil {
			t.Fatal(err)
		}
		resetSame(t, "the corpus after a reset", resetCorpus, resetRun(t, k, resetCorpus), wantCorpus)
	}
	// the corpus has to change something, or the test proves nothing
	if info, _ := k.Reset(); info.Undone < 10 || info.Tables < 3 {
		t.Errorf("the corpus changed too little to prove anything: %+v", info)
	}
}

// TestKernelResetReload: past the bound of the log a Reset loads the image
// again, and says so; the kernel then answers as a fresh one.
func TestKernelResetReload(t *testing.T) {
	img := resetImage(t)
	wantProbes := resetRun(t, resetKernel(t, img), resetProbes)

	k := resetKernel(t, img)
	k.vm.undo.max = 10
	resetRun(t, k, resetCorpus)
	if !k.vm.undo.overflow {
		t.Fatal("the corpus did not overflow a log of 10")
	}
	old := k.vm
	t0 := time.Now()
	info, err := k.Reset()
	t.Logf("Reset by reload: %v (load %.1f)", time.Since(t0), loadAverage())
	if err != nil || !info.Reloaded || k.vm == old {
		t.Fatalf("Reset past the bound: %+v, %v", info, err)
	}
	resetSame(t, "probes after a reload", resetProbes, resetRun(t, k, resetProbes), wantProbes)
	// and the new VM logs again, within the default bound
	resetRun(t, k, resetCorpus)
	if info, err := k.Reset(); err != nil || info.Reloaded || info.Undone == 0 {
		t.Errorf("the second Reset: %+v, %v", info, err)
	}

	// a kernel made from a VM has no image to reload
	vm, err := LoadImage(bytes.NewReader(img), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	k2, err := NewKernel(vm, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k2.Close()
	k2.vm.undo.max = 10
	resetRun(t, k2, resetCorpus)
	if _, err := k2.Reset(); !errors.Is(err, ErrResetOverflow) {
		t.Errorf("Reset of a kernel with no image past the bound: %v", err)
	}
	if k2.busy.Load() {
		t.Error("a refused Reset left the kernel busy")
	}
}

// TestKernelResetRefusals: Reset is refused while a cell runs and after Close.
func TestKernelResetRefusals(t *testing.T) {
	k, err := NewKernel(New(), KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	k.busy.Store(true)
	if _, err := k.Reset(); !errors.Is(err, ErrKernelBusy) {
		t.Errorf("Reset while a cell runs: %v", err)
	}
	k.busy.Store(false)
	if info, err := k.Reset(); err != nil || info.Reloaded {
		t.Errorf("Reset of a core-only kernel: %+v, %v", info, err)
	}
	k.Close()
	if _, err := k.Reset(); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Reset after Close: %v", err)
	}
}

// the log and its coverage, on the objects the image holds, without Base: a
// write of every kind is undone, in reverse, and the range writers log too.
func TestUndoLogKinds(t *testing.T) {
	vm := New()
	l := vm.undo
	m := newMemory(memAnyType, []any{int64(1), int64(2), int64(3), int64(4)})
	m.undo = l
	s := &Struct{Type: vecAnyType, Fields: []any{"a", "b"}, undo: l}
	l.on = true
	m.Set(0, int64(10))
	m.Set(0, int64(11)) // twice: the first old value must come back
	copy(m.Written(1, 3), []any{int64(20), int64(30)})
	s.logField(1)
	s.Fields[1] = "B"
	old := m.elems
	m.SetAll([]any{int64(9)})
	if len(l.entries) != 5 {
		t.Fatalf("%d entries", len(l.entries))
	}
	for i := len(l.entries) - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	if got := strings.Join([]string{showValue(m.elems[0]), showValue(m.elems[1]), showValue(m.elems[2]), showValue(m.elems[3])}, ","); got != "1,2,3,4" || &m.elems[0] != &old[0] {
		t.Errorf("memory after the undo: %s", got)
	}
	if s.Fields[1] != "b" {
		t.Errorf("field after the undo: %v", s.Fields[1])
	}
	// an object a cell made has no log, and an off log records nothing
	n := newMemory(memAnyType, []any{int64(1)})
	n.Set(0, int64(2))
	l.on = false
	m.Set(0, int64(5))
	if len(l.entries) != 5 {
		t.Errorf("entries after unlogged writes: %d", len(l.entries))
	}
	// the cap: past it nothing is recorded and the overflow is noted
	l.entries, l.max, l.on = nil, 2, true
	for i := 0; i < 5; i++ {
		m.Set(0, int64(i))
	}
	if len(l.entries) != 2 || !l.overflow {
		t.Errorf("the cap: %d entries, overflow %v", len(l.entries), l.overflow)
	}
}
