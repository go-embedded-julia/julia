package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// The cells run on a kernel over the image of a Boot()-only VM (core, no
// Base), so the cells use Core's names; the Base image is TestKernelBase.

func bootKernel(t testing.TB, o KernelOptions) *Kernel {
	t.Helper()
	k, err := LoadKernel(bytes.NewReader(saveBytes(t, bootedVM(t))), LoadOptions{}, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(k.Close)
	return k
}

func evalOK(t *testing.T, k *Kernel, src, want string) {
	t.Helper()
	r := k.Eval(context.Background(), src)
	if r.Err != nil || r.Value != want {
		t.Errorf("%s\n  value %q, error %v; want %q", src, r.Value, r.Err, want)
	}
}

// Cells that define and then use: a function, a struct, a redefined method.
func TestKernelDefinitions(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	def := func(src string) {
		t.Helper()
		if r := k.Eval(context.Background(), src); r.Err != nil {
			t.Errorf("%s: %v", src, r.Err)
		}
	}
	def("inc(x) = Core.Intrinsics.add_int(x, 1)")
	evalOK(t, k, "inc(41)", "42")
	def("struct Pt; x; y; end")
	evalOK(t, k, "p = Pt(1, 2); p.y", "2")
	evalOK(t, k, "p.x", "1")
	def("g(x) = 1")
	evalOK(t, k, "g(0)", "1")
	def("g(x) = 2")
	evalOK(t, k, "g(0)", "2")
	evalOK(t, k, "inc(g(0))", "3")
}

// A cell that throws is a value; the next cell runs.
func TestKernelErrorThenWorking(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	r := k.Eval(context.Background(), `throw(ErrorException("boom"))`)
	var je *JuliaError
	if !errors.As(r.Err, &je) || je.Type != "ErrorException" || je.Message != "boom" || r.Value != "" {
		t.Fatalf("a thrown error: %+v", r)
	}
	if je.Error() != "boom" || je.Value == nil {
		t.Errorf("JuliaError: %q, %v", je.Error(), je.Value)
	}
	r = k.Eval(context.Background(), `throw(1)`)
	if !errors.As(r.Err, &je) || je.Type != "Int64" || je.Message != "1" {
		t.Errorf("a thrown number: %+v", r)
	}
	r = k.Eval(context.Background(), `nothing_defined_here`)
	if !errors.As(r.Err, &je) || je.Type != "UndefVarError" {
		t.Errorf("an undefined name: %+v", r)
	}
	r = k.Eval(context.Background(), `1 +`)
	if r.Err == nil {
		t.Errorf("a syntax error: %+v", r)
	}
	evalOK(t, k, "Core.Intrinsics.add_int(1, 2)", "3")
	// a try block that failed in a cell leaves nothing behind
	evalOK(t, k, `try; throw(ErrorException("in")); catch e; e.msg; end`, `"in"`)
}

// Output is per cell: each cell gets its own stdout and stderr, and the next
// one does not see them.
func TestKernelOutputIsolation(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	var host bytes.Buffer
	k.VM().Stdout = &host
	r1 := k.Eval(context.Background(), `Core.println("one"); Core.println(Core.stderr, "err one"); 1`)
	r2 := k.Eval(context.Background(), `Core.println("two"); 2`)
	r3 := k.Eval(context.Background(), `3`)
	if r1.Stdout != "one\n" || r1.Stderr != "err one\n" || r1.Value != "1" {
		t.Errorf("cell 1: %+v", r1)
	}
	if r2.Stdout != "two\n" || r2.Stderr != "" || r2.Value != "2" {
		t.Errorf("cell 2: %+v", r2)
	}
	if r3.Stdout != "" || r3.Stderr != "" {
		t.Errorf("cell 3: %+v", r3)
	}
	// what a failing cell printed before it failed is still its own
	r := k.Eval(context.Background(), `Core.println("before"); throw(ErrorException("x"))`)
	if r.Stdout != "before\n" || r.Err == nil {
		t.Errorf("a failing cell: %+v", r)
	}
	// the VM's own writers are back between cells
	if k.VM().Stdout != &host {
		t.Error("the writers of the VM were not restored")
	}
	if host.Len() != 0 {
		t.Errorf("the host writer saw %q", host.String())
	}
	// past MaxOutput the rest is dropped and said
	r = k.EvalWith(context.Background(), `Core.println("abcdefgh")`, CellOptions{MaxOutput: 4})
	if r.Stdout != "abcd" || !r.Truncated || r.Err != nil {
		t.Errorf("a long output: %+v", r)
	}
	r = k.EvalWith(context.Background(), `Core.println("abcdefgh")`, CellOptions{MaxOutput: -1})
	if r.Stdout != "abcdefgh\n" || r.Truncated {
		t.Errorf("an unbounded output: %+v", r)
	}
}

const spin = `while true end`

// ctx interrupts a cell in a loop: InterruptException, even one the cell
// catches; the kernel runs the next cell.
func TestKernelCancellation(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	for _, src := range []string{
		spin,
		`while true; try; while true end; catch; end; end`, // the interrupt is not the cell's to swallow
		`function f(n); f(n); end; while true; try; throw(1); catch; end; end`,
	} {
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(50*time.Millisecond, cancel)
		r := k.Eval(ctx, src)
		if !errors.Is(r.Err, ErrCellCanceled) || !errors.Is(r.Err, context.Canceled) {
			t.Errorf("%s\n  %+v", src, r)
		}
		if r.Elapsed < 40*time.Millisecond || r.Elapsed > 5*time.Second {
			t.Errorf("%s\n  stopped after %v", src, r.Elapsed)
		}
		evalOK(t, k, "7", "7")
	}
	// a ctx that is done runs nothing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := k.Eval(ctx, `Core.println("never")`)
	if !errors.Is(r.Err, ErrCellCanceled) || r.Stdout != "" {
		t.Errorf("a cancelled ctx: %+v", r)
	}
	// a cell that throws an InterruptException itself is its own error
	r = k.Eval(context.Background(), `throw(InterruptException())`)
	var je *JuliaError
	if !errors.As(r.Err, &je) || je.Type != "InterruptException" || errors.Is(r.Err, ErrCellCanceled) {
		t.Errorf("a thrown InterruptException: %+v", r)
	}
}

// The budgets end a cell with typed errors, and the kernel goes on.
func TestKernelBounds(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	r := k.EvalWith(context.Background(), spin, CellOptions{Timeout: 50 * time.Millisecond})
	if !errors.Is(r.Err, ErrCellTimeout) || errors.Is(r.Err, ErrCellCanceled) {
		t.Errorf("a timeout: %+v", r)
	}
	r = k.EvalWith(context.Background(), spin, CellOptions{MaxSteps: 1000, Timeout: -1})
	if !errors.Is(r.Err, ErrStepLimit) || r.Steps <= 1000 {
		t.Errorf("a step budget: %+v", r)
	}
	alloc := `while true; Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 100); end`
	r = k.EvalWith(context.Background(), alloc, CellOptions{MaxAlloc: 1000})
	if !errors.Is(r.Err, ErrMemoryLimit) {
		t.Errorf("an allocation budget: %+v", r)
	}
	// one allocation over the VM's own bound
	r = k.Eval(context.Background(), `Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 1099511627776)`)
	if !errors.Is(r.Err, ErrMemoryLimit) {
		t.Errorf("a huge allocation: %+v", r)
	}
	// the budgets are per cell: each of these allocates 400 elements, within 1000
	for i := 0; i < 3; i++ {
		r = k.EvalWith(context.Background(), `Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 400); 1`, CellOptions{MaxAlloc: 1000})
		if r.Err != nil {
			t.Errorf("cell %d under its own allocation budget: %v", i, r.Err)
		}
	}
	// and the kernel's defaults can be set once
	k2 := bootKernel(t, KernelOptions{CellOptions: CellOptions{MaxSteps: 500, Timeout: -1}})
	if r := k2.Eval(context.Background(), spin); !errors.Is(r.Err, ErrStepLimit) {
		t.Errorf("a kernel step budget: %+v", r)
	}
	if r := k2.EvalWith(context.Background(), `7`, CellOptions{MaxSteps: -1}); r.Err != nil || r.Value != "7" {
		t.Errorf("a cell that lifts it: %+v", r)
	}
	evalOK(t, k, "8", "8")
}

// A running cell is not run twice at once; a closed or doubled kernel is refused.
func TestKernelRefusals(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	var first Result
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		first = k.Eval(ctx, spin)
	}()
	<-started
	var r Result
	for r = k.Eval(context.Background(), `1`); !errors.Is(r.Err, ErrKernelBusy); r = k.Eval(context.Background(), `1`) {
		if r.Err == nil {
			// the goroutine had not begun its cell: it is not busy yet
			time.Sleep(time.Millisecond)
			continue
		}
		t.Fatalf("while a cell runs: %+v", r)
	}
	cancel()
	wg.Wait()
	if !errors.Is(first.Err, ErrCellCanceled) {
		t.Errorf("the first cell: %+v", first)
	}

	if _, err := NewKernel(k.VM(), KernelOptions{}); !errors.Is(err, ErrKernelExists) {
		t.Errorf("a second kernel for a VM: %v", err)
	}
	if _, err := NewKernel(nil, KernelOptions{}); !errors.Is(err, ErrKernelState) {
		t.Errorf("no VM: %v", err)
	}
	gen := bootedVM(t)
	gen.GeneratingOutput = true
	if _, err := NewKernel(gen, KernelOptions{}); !errors.Is(err, ErrKernelState) {
		t.Errorf("a VM that generates an image: %v", err)
	}
	k.Close()
	k.Close()
	if r := k.Eval(context.Background(), `1`); !errors.Is(r.Err, ErrKernelClosed) {
		t.Errorf("a closed kernel: %+v", r)
	}
	if k.VM().kernel != nil {
		t.Error("a closed kernel is still its VM's")
	}
}

// The sandbox is the default; host mode is an opt-in, and a VM already in host
// mode is not taken without it.
func TestKernelSandbox(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	if k.VM().Host {
		t.Error("a kernel is in host mode by default")
	}
	vm := bootedVM(t)
	vm.Host = true
	if _, err := NewKernel(vm, KernelOptions{}); !errors.Is(err, ErrKernelHost) {
		t.Errorf("a host VM without the option: %v", err)
	}
	hk, err := NewKernel(vm, KernelOptions{Host: true})
	if err != nil || !hk.VM().Host {
		t.Fatalf("host mode asked for: %v", err)
	}
	hk.Close()
	vm2 := bootedVM(t)
	hk2, err := NewKernel(vm2, KernelOptions{Host: true})
	if err != nil || !vm2.Host {
		t.Fatalf("the option sets host mode: %v", err)
	}
	hk2.Close()
	if len(k.VM().Env) != 0 {
		t.Errorf("the environment of a sandbox is not empty: %v", k.VM().Env)
	}
	// an image that does not load is not a kernel
	if _, err := LoadKernel(strings.NewReader("not an image"), LoadOptions{}, KernelOptions{}); !errors.Is(err, ErrImageTruncated) && !errors.Is(err, ErrImageCorrupt) {
		t.Errorf("not an image: %v", err)
	}
	// nor is an image whose VM the kernel refuses
	img := saveBytes(t, bootedVM(t))
	if _, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{}); err != nil {
		t.Fatal(err)
	}
}

// A kernel from a fetched image: the pinned digest is checked before the load.
func TestFetchKernel(t *testing.T) {
	skipWithoutNetwork(t)
	img := saveBytes(t, bootedVM(t))
	srv, _ := imageServer(t, serve(img))
	want := sha256.Sum256(img)
	k, err := FetchKernel(context.Background(), fetchVersion, want, fetchOpts(srv, t.TempDir()), LoadOptions{}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	evalOK(t, k, "Core.Intrinsics.add_int(20, 22)", "42")
	if _, err := FetchKernel(context.Background(), fetchVersion, [32]byte{1}, fetchOpts(srv, t.TempDir()), LoadOptions{}, KernelOptions{}); !errors.Is(err, ErrImageDigest) {
		t.Errorf("a wrong digest: %v", err)
	}
}

// Two kernels in one process, in two goroutines: nothing they share is
// unguarded (run under -race).
func TestKernelsInOneProcess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		k := bootKernel(t, KernelOptions{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, src := range []string{"struct Q; a; end", "Q(1).a", "h(x) = Core.Intrinsics.add_int(x, 1)", "h(h(1))"} {
				if r := k.Eval(context.Background(), src); r.Err != nil {
					t.Errorf("%s: %v", src, r.Err)
				}
			}
		}()
	}
	wg.Wait()
}

// The julia helpers of a kernel that has Base. Without Base they are stood in
// for by functions of Core, which take the same paths.
func TestKernelHelpers(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	// show: a string, a non-string, an error
	for _, c := range []struct{ fn, src, want, err string }{
		{`v -> "shown"`, `1`, "shown", ""},
		{`v -> 3`, `1`, "", ""},
		{`v -> throw(ErrorException("show failed"))`, `1`, "", "show failed"},
	} {
		f, err := k.sess.Eval(c.fn)
		if err != nil {
			t.Fatal(err)
		}
		k.show = f
		r := k.Eval(context.Background(), c.src)
		if c.err != "" {
			var je *JuliaError
			if !errors.As(r.Err, &je) || je.Message != c.err {
				t.Errorf("%s: %+v", c.fn, r)
			}
			continue
		}
		if r.Err != nil || r.Value != c.want {
			t.Errorf("%s: %+v", c.fn, r)
		}
	}
	k.show = nil
	// errText: a string, a non-string, an error
	for _, c := range []struct{ fn, want string }{
		{`e -> "text of it"`, "text of it"},
		{`e -> 3`, `ErrorException("boom")`},
		{`e -> throw(1)`, `ErrorException("boom")`},
	} {
		f, err := k.sess.Eval(c.fn)
		if err != nil {
			t.Fatal(err)
		}
		k.errText = f
		r := k.Eval(context.Background(), `throw(ErrorException("boom"))`)
		var je *JuliaError
		if !errors.As(r.Err, &je) || je.Message != c.want {
			t.Errorf("%s: %+v", c.fn, r)
		}
	}
	k.errText = nil
	// a kernel on a VM that has a Base: its initializers run, its helpers are made
	vm := bootedVM(t)
	vm.baseModule = vm.Main
	m := vm.NewModule("Z")
	if _, err := vm.sessionFor(m).Eval(`__init__() = throw(ErrorException("init failed"))`); err != nil {
		t.Fatal(err)
	}
	vm.moduleInitOrder = append(vm.moduleInitOrder, m)
	if _, err := NewKernel(vm, KernelOptions{}); err == nil {
		t.Error("an initializer that fails")
	}
	bk, err := NewKernel(vm, KernelOptions{})
	if err != nil || bk.show == nil || bk.errText == nil {
		t.Fatalf("helpers of a VM with Base: %v", err)
	}
	old := showSource
	showSource = "1 +"
	vm2 := bootedVM(t)
	vm2.baseModule = vm2.Main
	if _, err := NewKernel(vm2, KernelOptions{}); err == nil {
		t.Error("a helper that does not parse, in NewKernel")
	}
	showSource = old
	for i, srcs := range [][3]string{{"1 +", "e -> 1", "e -> 1"}, {"v -> 1", "1 +", "e -> 1"}, {"v -> 1", "e -> 1", "1 +"}} {
		if err := bk.installHelpers(srcs[0], srcs[1], srcs[2]); err == nil {
			t.Errorf("helper %d does not parse", i)
		}
	}
}

// An interrupt that finds the event loop in another task than the cell's is
// scheduled into the cell's task, once.
func TestInterruptDeliveredToAWaitingTask(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	vm := k.VM()
	f, err := k.sess.Eval(`(t, e) -> Core.println("scheduled")`)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	vm.Stdout = &out
	if _, err := vm.apply(f, []any{vm.currentTask(), &Struct{Type: vm.interruptType()}}); err != nil || out.String() != "scheduled\n" {
		t.Fatalf("the helper itself: %q, %v", out.String(), err)
	}
	out.Reset()
	vm.deliverInterrupt() // nothing pending
	vm.intr.Store(true)
	vm.deliverInterrupt() // no helper
	k.schedule = f
	vm.deliverInterrupt() // no task to deliver to
	vm.intrTask = vm.currentTask()
	vm.deliverInterrupt()
	vm.deliverInterrupt() // once
	if out.String() != "scheduled\n" {
		t.Errorf("delivered: %q", out.String())
	}
	vm.intrSent = false
	old := vm.Core.own("InterruptException").Value
	vm.Core.own("InterruptException").Value = nil // a VM without the type delivers nothing
	vm.deliverInterrupt()
	vm.Core.own("InterruptException").Value = old
	if out.String() != "scheduled\n" {
		t.Errorf("delivered without a type: %q", out.String())
	}
	vm.intr.Store(false)
	vm.intrTask = nil
	// the shim julia asks about its pure context
	if got, err := vm.shims["jl_is_in_pure_context"](vm, nil); err != nil || got != cbool(false) {
		t.Errorf("jl_is_in_pure_context: %v, %v", got, err)
	}
}

// A loop that waits on a timer is woken by the interrupt and raises it.
func TestInterruptWakesTheLoop(t *testing.T) {
	vm := bootedVM(t)
	l := vm.uv()
	l.order = append(l.order, &uvHandle{kind: uvTimer, active: true, refd: true, due: l.now + 600000})
	vm.intrCh = make(chan struct{}, 1)
	vm.intrTask = vm.currentTask()
	time.AfterFunc(20*time.Millisecond, func() {
		vm.intr.Store(true)
		vm.intrCh <- struct{}{}
	})
	t0 := time.Now()
	err := vm.uvIterate(true)
	var thrown *Thrown
	if !errors.As(err, &thrown) || !vm.isInterrupt(thrown.Value) || time.Since(t0) > 10*time.Second {
		t.Errorf("a waiting loop: %v after %v", err, time.Since(t0))
	}
}

// An interrupt is for one task, and is taken once.
func TestTakeInterrupt(t *testing.T) {
	vm := bootedVM(t)
	if vm.takeInterrupt() {
		t.Error("nothing pending")
	}
	vm.intr.Store(true)
	vm.intrTask = &Struct{}
	if vm.takeInterrupt() || !vm.intr.Load() {
		t.Error("another task's interrupt was taken")
	}
	vm.intrTask = vm.currentTask()
	if !vm.takeInterrupt() || vm.intr.Load() || vm.takeInterrupt() {
		t.Error("the task's interrupt is taken once")
	}
	vm.intr.Store(true)
	vm.intrHard.Store(true)
	if !vm.takeInterrupt() || !vm.takeInterrupt() || !vm.intr.Load() {
		t.Error("a hard interrupt stays raised")
	}
}

// vm.interruptError without Core's InterruptException (a VM that did not boot).
func TestInterruptWithoutBoot(t *testing.T) {
	vm := New()
	if err := vm.interruptError(); !errors.Is(err, ErrInternal) {
		t.Errorf("no InterruptException: %v", err)
	}
	if vm.isInterrupt(1) || vm.isInterrupt(&Struct{}) {
		t.Error("a value that is no InterruptException")
	}
}

// A kernel's cell that panics inside the interpreter is an error, not a crash.
func TestKernelDefaults(t *testing.T) {
	o := DefaultCellOptions.over(CellOptions{})
	if o != DefaultCellOptions {
		t.Errorf("zero over the defaults: %+v", o)
	}
	o = DefaultCellOptions.over(CellOptions{Timeout: time.Second, MaxSteps: 5, MaxAlloc: 6, MaxOutput: 7})
	if o != (CellOptions{Timeout: time.Second, MaxSteps: 5, MaxAlloc: 6, MaxOutput: 7}) {
		t.Errorf("set fields kept: %+v", o)
	}
}

// TestKernelBase runs the same cells on the Base image: julia's text/plain
// display and showerror text, cancellation of a sleep, and the cost of a cold
// start (load + first cell). It builds Base (several minutes) unless
// JLGO_KERNEL_IMAGE names an image built at JLGO_IMAGE_VERSION.
func TestKernelBase(t *testing.T) {
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
	t0 := time.Now()
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	tLoad := time.Since(t0)
	first := k.Eval(context.Background(), `1 + 1`)
	t.Logf("cold start: load + kernel %v, first cell %v (load %.1f)", tLoad, first.Elapsed, loadAverage())
	if first.Err != nil || first.Value != "2" {
		t.Fatalf("first cell: %+v", first)
	}
	ctx := context.Background()
	evalOK(t, k, "f(x) = x + 1", "f (generic function with 1 method)")
	evalOK(t, k, "f(41)", "42")
	evalOK(t, k, "struct P; a::Int; end; P(3)", "P(3)")
	evalOK(t, k, "f(x) = x + 2; f(40)", "42")
	evalOK(t, k, "[1, 2, 3]", "3-element Vector{Int64}:\n 1\n 2\n 3")
	evalOK(t, k, `"text"`, `"text"`)
	evalOK(t, k, `(1, "a", :b)`, `(1, "a", :b)`)
	for _, c := range []struct{ src, want string }{
		{`error("boom")`, "boom"},
		{`undefined_xyz`, "UndefVarError: `undefined_xyz` not defined in `Main`"},
		{`sqrt(-1)`, "DomainError with -1.0:\nsqrt was called with a negative real argument but will only return a complex result if called with a complex argument. Try sqrt(Complex(x))."},
	} {
		r := k.Eval(ctx, c.src)
		var je *JuliaError
		if !errors.As(r.Err, &je) || !strings.HasPrefix(je.Message, c.want) {
			t.Errorf("%s\n  %+v\n  want %q", c.src, r, c.want)
			if je != nil {
				_, err := k.vm.apply(k.errText, []any{je.Value})
				t.Logf("showerror of it: %v", err)
			}
		}
	}
	// showerror of a MethodError lists the candidates through
	// jl_methtable_lookup, which is not ported: the error is a value of the
	// right type, its message the Go repr (a stated limitation)
	var me *JuliaError
	if r := k.Eval(ctx, `1 + "a"`); !errors.As(r.Err, &me) || me.Type != "MethodError" || me.Message == "" {
		t.Errorf("a MethodError: %+v", r)
	}
	// the cell after an error works, and output is per cell
	r1 := k.Eval(ctx, `println("one"); print(stderr, "e1"); 1`)
	r2 := k.Eval(ctx, `println("two")`)
	if r1.Stdout != "one\n" || r1.Stderr != "e1" || r2.Stdout != "two\n" || r2.Stderr != "" {
		t.Errorf("output: %+v %+v", r1, r2)
	}
	// cancellation of a loop and of a sleep
	for _, src := range []string{`while true end`, `sleep(60)`} {
		cctx, cancel := context.WithCancel(ctx)
		time.AfterFunc(200*time.Millisecond, cancel)
		r := k.Eval(cctx, src)
		if !errors.Is(r.Err, ErrCellCanceled) || r.Elapsed > 10*time.Second {
			t.Errorf("%s: %+v", src, r)
		}
	}
	if r := k.EvalWith(ctx, `sleep(60)`, CellOptions{Timeout: 200 * time.Millisecond}); !errors.Is(r.Err, ErrCellTimeout) {
		t.Errorf("a sleep past its timeout: %+v", r)
	}
	if r := k.EvalWith(ctx, `global x = 0; while true; global x += 1; end`, CellOptions{MaxSteps: 10000, Timeout: -1}); !errors.Is(r.Err, ErrStepLimit) {
		t.Errorf("a step budget: %+v", r)
	}
	if r := k.EvalWith(ctx, `v = []; while true; push!(v, zeros(1000)); end`, CellOptions{MaxAlloc: 1 << 16, Timeout: -1}); !errors.Is(r.Err, ErrMemoryLimit) {
		t.Errorf("an allocation budget: %+v", r)
	}
	evalOK(t, k, "f(1)", "3")
}
