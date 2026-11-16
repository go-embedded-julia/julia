package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/go-embedded-julia/julia/core"
)

// Kernel.Reset against a fresh kernel (#93): the oracle is a kernel just made
// from the same image, which a Reset kernel must answer as, cell for cell.

// resetCorpus is what a notebook does that Reset has to undo: define, redefine,
// overload a method of Base, make a struct and a method on it, import a stdlib,
// draw random numbers (unseeded first, then seeded), mutate a global of Base,
// delete a method of Base, and fail.
//
// An unseeded rand() differs from one fresh kernel to the next (the generator
// is seeded when the kernel starts), so the cells say whether it is in [0, 1);
// TestKernelResetRNG checks the generator's state, and the draws.
var resetCorpus = []string{
	`0 <= rand() < 1`,
	`x = 10; y = x + 1`,
	`f(x) = x + 1; f(2)`,
	`f(x) = x + 2; f(2)`,
	`struct P; a::Int; end; P(3)`,
	`Base.show(io::IO, ::MIME"text/plain", p::P) = print(io, "P!"); P(4)`,
	`Base.show(io::IO, x::Int8) = print(io, "int8!"); Int8(3)`,
	`using LinearAlgebra; norm([3.0, 4.0])`,
	`using Random; Random.seed!(42); rand(3)`,
	`0 <= rand() < 1`,
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
	`0 <= rand() < 1`,
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
	`size(rand(2))`,
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
	// equal errors would make the oracle agree on a cell neither runs
	for i, w := range append(append([]string(nil), wantProbes...), wantCorpus...) {
		if strings.Contains(w, "not ported yet") {
			t.Fatalf("the oracle kernel does not run cell %d: %s", i, w)
		}
	}

	k := resetKernel(t, img)
	resetSame(t, "first corpus", resetCorpus, resetRun(t, k, resetCorpus), wantCorpus)
	for round := 1; round <= 2; round++ {
		t0 := time.Now()
		info, err := k.Reset()
		d := time.Since(t0)
		if err != nil || info.Reloaded {
			t.Fatalf("Reset %d: %+v, %v", round, info, err)
		}
		// the corpus has to change something, or the test proves nothing
		if info.Undone < 100 || info.Tables < 3 {
			t.Errorf("the corpus changed too little to prove anything: %+v", info)
		}
		t.Logf("Reset %d: %v for %d writes and %d method tables", round, d, info.Undone, info.Tables)
		resetSame(t, "probes after a reset", resetProbes, resetRun(t, k, resetProbes), wantProbes)
		if _, err := k.Reset(); err != nil {
			t.Fatal(err)
		}
		resetSame(t, "the corpus after a reset", resetCorpus, resetRun(t, k, resetCorpus), wantCorpus)
	}
}

// TestKernelResetRNG: the generator of the task is where the kernel started
// it, whatever the cells drew or seeded, so that the draws after a Reset are the
// ones the kernel would have made first (an unseeded rand() cannot be compared
// with another kernel's, which was seeded by its own start).
func TestKernelResetRNG(t *testing.T) {
	img := resetImage(t)
	k := resetKernel(t, img)
	const state = "let t = current_task(); string(t.rngState0, ',', t.rngState1, ',', t.rngState2, ',', t.rngState3, ',', t.rngState4) end"
	const draw = "string(rand(3), rand(UInt8, 2), randn())"
	eval := func(src string) string {
		r := k.Eval(context.Background(), src)
		if r.Err != nil {
			t.Fatalf("%s: %v", src, r.Err)
		}
		return r.Value
	}
	s0 := eval(state)
	d0 := eval(draw)
	if s1 := eval(state); s1 == s0 {
		t.Fatalf("drawing did not move the state %s", s0)
	}
	eval(`using Random; Random.seed!(7); rand(3); rand()`)
	if _, err := k.Reset(); err != nil {
		t.Fatal(err)
	}
	if s := eval(state); s != s0 {
		t.Errorf("state after Reset %s, at the start %s", s, s0)
	}
	if d := eval(draw); d != d0 {
		t.Errorf("draws after Reset %s, the first draws of the kernel %s", d, d0)
	}
	// a seeded sequence is the same in every kernel, and after a Reset
	const seeded = "using Random; Random.seed!(42); string(rand(3), randn())"
	a := eval(seeded)
	if _, err := k.Reset(); err != nil {
		t.Fatal(err)
	}
	if b := eval(seeded); a != b {
		t.Errorf("seeded draws %s, then after a Reset %s", a, b)
	}
	if c := eval(seeded); a != c {
		t.Errorf("seeded draws %s, then again %s", a, c)
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
	// a kernel made from a VM has no image to load past the log's bound
	k.vm.undo.overflow = true
	if _, err := k.Reset(); !errors.Is(err, ErrResetOverflow) || k.busy.Load() {
		t.Errorf("Reset past the bound without an image: %v", err)
	}
	k.Close()
	if _, err := k.Reset(); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Reset after Close: %v", err)
	}
}

// Reset on the image of a Boot()-only VM (no Base): writes to an image memory
// and an image struct, a method defined, a module made and a registry entry
// added by a cell are undone; a log past its cap is dropped, not kept; and a
// log that overflowed is answered by a load of the image, whose failures leave
// the error to the caller.
func TestKernelResetCore(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	vm := k.vm
	// objects the image holds, as the loader marks them
	mem := newMemory(memAnyType, []any{"elem"})
	mem.undo = vm.undo
	st := &Struct{Type: vecAnyType, Fields: []any{"field"}, undo: vm.undo}
	oldElem, oldField := mem.Get(0), st.Fields[0]
	mem.Set(0, "written")
	st.logField(0)
	st.Fields[0] = "written"
	vm.modules["Zz"] = vm.Main
	if r := k.Eval(context.Background(), "f(a) = a; x = 3; f(x)"); r.Err != nil || r.Value != "3" {
		t.Fatalf("cell: %+v", r)
	}
	info, err := k.Reset()
	if err != nil || info.Reloaded || info.Undone < 2 || info.Tables < 1 {
		t.Fatalf("Reset: %+v, %v", info, err)
	}
	if mem.Get(0) != oldElem || st.Fields[0] != oldField {
		t.Errorf("the writes were not undone: %v, %v", mem.Get(0), st.Fields[0])
	}
	if _, ok := vm.modules["Zz"]; ok {
		t.Error("a module registered by a cell is still there")
	}
	if r := k.Eval(context.Background(), "isdefined(Main, :x)"); r.Err != nil || r.Value != "false" {
		t.Errorf("x after Reset: %+v", r)
	}
	// a log past its cap is dropped after the Reset, not kept
	for i := 0; i < 5000; i++ {
		mem.Set(0, i)
	}
	if info, err := k.Reset(); err != nil || info.Undone != 5000 || vm.undo.entries != nil || mem.Get(0) != oldElem {
		t.Errorf("Reset of a long log: %+v, %v, %d entries kept", info, err, len(vm.undo.entries))
	}
	// an overflow is answered by a load, and the new VM is the kernel's
	vm.undo.max = 1
	mem.Set(0, 1)
	mem.Set(0, 2)
	vm.Stdout = io.Discard
	info, err = k.Reset()
	if err != nil || !info.Reloaded || k.vm == vm || vm.kernel != nil || k.vm.kernel != k || !k.vm.undo.on {
		t.Fatalf("Reset past the cap: %+v, %v", info, err)
	}
	if r := k.Eval(context.Background(), "1"); r.Err != nil || r.Value != "1" {
		t.Errorf("a cell after the reload: %+v", r)
	}
	// the load fails, or makes a VM no kernel can take: the error, the kernel as it was
	boom := errors.New("boom")
	k.vm.undo.overflow = true
	k.load = func() (*VM, error) { return nil, boom }
	if _, err := k.Reset(); !errors.Is(err, boom) {
		t.Errorf("a load that fails: %v", err)
	}
	k.load = func() (*VM, error) { g := New(); g.GeneratingOutput = true; return g, nil }
	if _, err := k.Reset(); !errors.Is(err, ErrKernelState) {
		t.Errorf("a VM no kernel can take: %v", err)
	}
	if k.busy.Load() {
		t.Error("a refused Reset left the kernel busy")
	}
	// LoadKernel reads its image whole: a reader that fails fails the load
	if _, err := LoadKernel(iotest.ErrReader(boom), LoadOptions{}, KernelOptions{}); !errors.Is(err, boom) {
		t.Errorf("LoadKernel of a failing reader: %v", err)
	}
}

// valueOfBytes: a primitive of exactly the bytes' size, a tuple of primitives
// that the bytes fill exactly; nothing else.
func TestValueOfBytes(t *testing.T) {
	vm := New()
	tup := func(ts ...any) *core.DataType { return must(core.ApplyType(core.AnyTupleType, ts...)).(*core.DataType) }
	for _, c := range []struct {
		name string
		t    any
		b    []byte
		want string // "" for a refusal
	}{
		{"an Int32", core.Int32Type, []byte{1, 0, 0, 0}, "1"},
		{"an Int64 of 4 bytes", core.Int64Type, []byte{1, 0, 0, 0}, ""},
		{"not a type", nil, []byte{1}, ""},
		{"a String", core.StringType, []byte{1}, ""},
		{"the empty tuple", core.EmptyTupleType, nil, ""},
		{"two bytes", tup(core.UInt8Type, core.UInt8Type), []byte{1, 2}, "(0x01, 0x02)"},
		{"a mixed tuple", tup(core.UInt8Type, core.Int32Type), []byte{1, 2, 0, 0, 0}, "(0x01, 2)"},
		{"a short tuple", tup(core.UInt8Type, core.UInt8Type), []byte{1}, ""},
		{"a long tuple", tup(core.UInt8Type), []byte{1, 2}, ""},
		{"a tuple with a String", tup(core.Int32Type, core.StringType), []byte{1, 0, 0, 0}, ""},
	} {
		v, ok := vm.valueOfBytes(c.t, c.b)
		if got := showValue(v); (c.want == "") == ok || ok && got != c.want {
			t.Errorf("%s: %s, %v; want %q", c.name, got, ok, c.want)
		}
	}
}

// BenchmarkMemorySet is what a write costs through Memory.Set: of a memory a
// cell made (one nil test), of an image memory with the log off, with it on (an
// entry appended; the log is emptied every 100k writes, as Reset empties it).
func BenchmarkMemorySet(b *testing.B) {
	for _, c := range []struct {
		name  string
		owned bool
		on    bool
	}{{"cell-owned", false, false}, {"image, log off", true, false}, {"image, log on", true, true}} {
		b.Run(c.name, func(b *testing.B) {
			l := &undoLog{max: 1 << 20}
			m := newMemory(memAnyType, make([]any, 1024))
			if c.owned {
				m.undo = l
			}
			l.on = c.on
			v := any(int64(1))
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				m.Set(i&1023, v)
				if i%100_000 == 99_999 {
					l.entries = l.entries[:0]
				}
			}
		})
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
	old := m.All()
	m.SetAll([]any{int64(9)})
	if len(l.entries) != 5 {
		t.Fatalf("%d entries", len(l.entries))
	}
	l.on = false // as Reset does while it replays
	for i := len(l.entries) - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	if got := strings.Join([]string{showValue(m.Get(0)), showValue(m.Get(1)), showValue(m.Get(2)), showValue(m.Get(3))}, ","); got != "1,2,3,4" || &m.All()[0] != &old[0] {
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
