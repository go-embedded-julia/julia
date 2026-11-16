package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-embedded-julia/julia/core"
)

// A Kernel is the session a notebook keeps: one VM, one module (Main), and a
// cell at a time (#40 step 8). Eval runs the source of a cell and answers its
// value as julia's text/plain display, what the cell printed, and its error
// as a value; ctx interrupts a running cell, and per-cell budgets (time,
// statements, allocations, output) end a runaway one with a typed error.
//
// The sandbox is the default: the VM's filesystem holds julia's own tree only
// and its environment is empty (VM.Host, VM.Env). Host mode is an opt-in of
// KernelOptions, never a default and never inherited from a VM.
//
// One Kernel per VM, and one running cell per Kernel: NewKernel refuses a VM
// that has a kernel already, and a second Eval while a cell runs answers
// ErrKernelBusy. Kernels of different VMs share no VM state. What they do
// share, in one process, is core's tables: the types of core (Int64, Array,
// ...) and the hash-consing of every type application, which are guarded for
// concurrent use, and the hints an image sets on core's shared types, which
// LoadImage sets once and refuses to change (a second image whose hints
// differ is ErrImageConflict, #49). Two kernels over images of the same
// release therefore run side by side; kernels over different releases are
// refused at the second LoadImage rather than run on mixed state.

// Errors of a Kernel.
var (
	// ErrKernelBusy is an Eval while another cell of the kernel runs.
	ErrKernelBusy = errors.New("interp: the kernel is running a cell")
	// ErrKernelClosed is an Eval after Close.
	ErrKernelClosed = errors.New("interp: the kernel is closed")
	// ErrKernelExists is a second kernel for one VM.
	ErrKernelExists = errors.New("interp: the VM has a kernel already")
	// ErrKernelHost is a VM in host mode given to a kernel that did not ask for it.
	ErrKernelHost = errors.New("interp: the VM is in host mode, and the kernel was not given KernelOptions.Host")
	// ErrKernelState is a VM that cannot run cells: none, or still generating an image.
	ErrKernelState = errors.New("interp: the VM cannot run cells")
	// ErrCellCanceled is a cell stopped because its ctx was cancelled; the
	// cell saw an InterruptException. It also wraps ctx's error.
	ErrCellCanceled = errors.New("interp: the cell was cancelled")
	// ErrCellTimeout is a cell stopped for running past CellOptions.Timeout.
	ErrCellTimeout = errors.New("interp: the cell ran out of time")
)

// CellOptions are the budgets of a cell. A zero field is the kernel's value
// (which is DefaultCellOptions for a zero kernel field), a negative one means
// no bound. MaxSteps alone is unbounded when zero, as VM.MaxSteps.
type CellOptions struct {
	// Timeout bounds the wall time of the cell, its display included.
	Timeout time.Duration
	// MaxSteps bounds the statements the cell executes (ErrStepLimit).
	MaxSteps int64
	// MaxAlloc bounds the elements the cell's memory allocations (arrays,
	// dictionaries) take in total (ErrMemoryLimit); VM.MaxMemoryElems bounds
	// each one.
	MaxAlloc int64
	// MaxOutput bounds each of Stdout and Stderr in bytes; the rest is
	// dropped and Result.Truncated is set.
	MaxOutput int
	// HardScope runs the cell with the scope of a file (include_string)
	// instead of the REPL's soft scope, the default, as IJulia has it: in
	// soft scope a loop at the top of a cell assigns to a global that exists
	// (REPL.softscope, #54). The zero value is soft; a kernel's HardScope
	// holds for its cells.
	HardScope bool
}

// DefaultCellOptions are the budgets of a kernel that sets none.
var DefaultCellOptions = CellOptions{Timeout: 30 * time.Second, MaxAlloc: 1 << 25, MaxOutput: 1 << 20}

// KernelOptions configure NewKernel.
type KernelOptions struct {
	CellOptions
	// Host opts into the host filesystem (VM.Host). It is false by default
	// and a VM that is already in host mode is refused without it.
	Host bool
}

// over is the budgets o with the zero fields taken from d.
func (d CellOptions) over(o CellOptions) CellOptions {
	if o.Timeout == 0 {
		o.Timeout = d.Timeout
	}
	if o.MaxSteps == 0 {
		o.MaxSteps = d.MaxSteps
	}
	if o.MaxAlloc == 0 {
		o.MaxAlloc = d.MaxAlloc
	}
	if o.MaxOutput == 0 {
		o.MaxOutput = d.MaxOutput
	}
	o.HardScope = o.HardScope || d.HardScope
	return o
}

// JuliaError is an exception a cell threw, as a value: its type, julia's
// showerror text (the Go repr on a VM without Base), and the exception itself.
type JuliaError struct {
	Type    string
	Message string
	Value   any
}

func (e *JuliaError) Error() string { return e.Message }

// Result is what a cell did.
type Result struct {
	// Value is the text/plain display of the cell's last value ("" after an error).
	Value string
	// Stdout and Stderr are what the cell printed, apart from every other cell's.
	Stdout, Stderr string
	// Truncated is set when output was dropped past CellOptions.MaxOutput.
	Truncated bool
	// Err is nil, a *JuliaError, or a typed Go error: ErrCellCanceled,
	// ErrCellTimeout, ErrStepLimit, ErrMemoryLimit, ErrKernelBusy,
	// ErrKernelClosed, ErrUnsupported or ErrInternal (test with errors.Is).
	Err error
	// Steps is the statements the cell executed, counted when a step budget is set.
	Steps int64
	// Elapsed is the wall time of the cell.
	Elapsed time.Duration
}

// Kernel is a VM that runs notebook cells.
type Kernel struct {
	vm     *VM
	sess   *Session
	opts   CellOptions
	busy   atomic.Bool
	closed atomic.Bool
	// show, errText and schedule are julia functions, nil on a VM without
	// Base: the display of a value, the text of an exception, and the
	// delivery of an exception to a task that waits
	show, errText, schedule any
	// snap is the state Reset returns to; img and lo the image the VM came
	// from, nil for a kernel made from a VM (Reset reloads it past the log's
	// bound), and ko the options the kernel was made with
	snap *kernelSnap
	// load makes a VM from the image again, nil for a kernel that was given a VM
	load func() (*VM, error)
	ko   KernelOptions
}

// NewKernel makes vm a kernel: a VM from LoadImage (or Boot, for a core-only
// one), whose module initializers run here if they have not. It takes the VM:
// Close closes it.
func NewKernel(vm *VM, opts KernelOptions) (*Kernel, error) {
	return newKernelOn(vm, opts)
}

func newKernelOn(vm *VM, opts KernelOptions) (*Kernel, error) {
	switch {
	case vm == nil || vm.GeneratingOutput:
		return nil, ErrKernelState
	case vm.kernel != nil:
		return nil, ErrKernelExists
	case vm.Host && !opts.Host:
		return nil, ErrKernelHost
	}
	k := &Kernel{vm: vm, opts: DefaultCellOptions.over(opts.CellOptions), ko: opts}
	vm.Host = opts.Host
	if vm.baseModule != nil {
		if err := vm.RunModuleInitializers(); err != nil {
			return nil, err
		}
		if err := k.installHelpers(showSource, errTextSource, scheduleSource); err != nil {
			return nil, err
		}
	}
	k.sess = vm.sessionFor(vm.Main)
	vm.kernel = k
	// from here on, a write to an object of the image is logged: the state
	// Reset returns to is this one, after the initializers and the helpers.
	// The world moves past the last one used, so that no method of this state
	// ends in the world Reset reverts to.
	vm.newWorld()
	k.snap = vm.takeSnap()
	vm.undo.on = true
	return k, nil
}

// The julia functions that print a value and an exception as the REPL does,
// defined in a module of their own so that Main holds only the cells' names.
var (
	showSource     = `v -> sprint(show, MIME("text/plain"), v; context=(:limit=>true, :displaysize=>(24, 80)))`
	errTextSource  = `e -> sprint(showerror, e)`
	scheduleSource = `(t, e) -> schedule(t, e; error=true)`
)

func (k *Kernel) installHelpers(show, errText, schedule string) (err error) {
	h := k.vm.NewSession()
	if k.show, err = h.Eval(show); err != nil {
		return err
	}
	if k.errText, err = h.Eval(errText); err != nil {
		return err
	}
	k.schedule, err = h.Eval(schedule)
	return err
}

// LoadKernel loads an image into a VM and makes it a kernel.
func LoadKernel(r io.Reader, lo LoadOptions, ko KernelOptions) (*Kernel, error) {
	// the bytes are kept (the image's size) so that Reset can load them again
	img, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	vm, err := LoadImage(bytes.NewReader(img), lo)
	if err != nil {
		return nil, err
	}
	k, err := NewKernel(vm, ko) // a VM that no kernel took has run nothing: nothing to close
	if k != nil {
		k.load = func() (*VM, error) { return LoadImage(bytes.NewReader(img), lo) }
	}
	return k, err
}

// FetchKernel fetches the image of a release by its pinned digest
// (FetchImage), loads it and makes it a kernel.
func FetchKernel(ctx context.Context, version string, digest [32]byte, fo FetchOptions, lo LoadOptions, ko KernelOptions) (*Kernel, error) {
	img, err := FetchImage(ctx, version, digest, fo)
	if err != nil {
		return nil, err
	}
	lo.Digest = &digest // the load checks it again over the bytes it is given
	return LoadKernel(bytes.NewReader(img), lo, ko)
}

// VM is the kernel's VM, for the embedder that sets its limits or reads
// its state between cells; not while one runs.
func (k *Kernel) VM() *VM { return k.vm }

// Close ends the kernel and closes its VM. A cell that runs is not stopped:
// cancel its ctx first.
func (k *Kernel) Close() {
	if k.closed.Swap(true) {
		return
	}
	k.vm.Close()
	k.vm.kernel = nil
}

// Eval runs one cell with the kernel's budgets.
func (k *Kernel) Eval(ctx context.Context, src string) Result {
	return k.EvalWith(ctx, src, CellOptions{})
}

// EvalWith runs one cell, its budgets o over the kernel's.
func (k *Kernel) EvalWith(ctx context.Context, src string, o CellOptions) (res Result) {
	if !k.busy.CompareAndSwap(false, true) {
		return Result{Err: ErrKernelBusy}
	}
	defer k.busy.Store(false)
	if k.closed.Load() {
		return Result{Err: ErrKernelClosed}
	}
	if err := ctx.Err(); err != nil {
		return Result{Err: fmt.Errorf("%w (%w)", ErrCellCanceled, err)}
	}
	vm := k.vm
	o = k.opts.over(o)
	start := time.Now()
	out, errb := &capBuf{max: o.MaxOutput}, &capBuf{max: o.MaxOutput}
	oldOut, oldErr := vm.Stdout, vm.Stderr
	depth, nexc := vm.depth, len(vm.excStack)
	vm.Stdout, vm.Stderr = out, errb
	k.sess.Soft = !o.HardScope
	vm.steps, vm.MaxSteps = 0, max(o.MaxSteps, 0)
	vm.allocs, vm.allocMax = 0, max(o.MaxAlloc, 0)

	cctx, cancel := ctx, context.CancelFunc(func() {})
	if o.Timeout > 0 {
		cctx, cancel = context.WithTimeout(ctx, o.Timeout)
	}
	// the interrupt is for this task: soft, once, and again every
	// interruptEvery, so that the cleanup of an interrupted call (a finally, a
	// lock, a sleep's wait list) runs in between; if the cell is still running
	// after softInterrupts of them (a catch that swallows it) it is hard, and
	// stays raised until the cell ends
	vm.intr.Store(false)
	vm.intrHard.Store(false)
	vm.intrSent = false
	vm.intrTask = vm.currentTask()
	vm.intrCh = make(chan struct{}, 1)
	var fired atomic.Bool
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-cctx.Done():
		case <-done:
			return
		}
		fired.Store(true)
		for i := 0; ; i++ {
			vm.intrHard.Store(i >= softInterrupts)
			vm.intr.Store(true)
			select {
			case vm.intrCh <- struct{}{}:
			default:
			}
			select {
			case <-done:
				return
			case <-time.After(interruptEvery):
			}
		}
	}()

	value, err := k.run(src)

	close(done)
	<-stopped
	cancel()
	vm.intr.Store(false)
	vm.intrHard.Store(false)
	vm.intrTask = nil
	if fired.Load() && err != nil {
		if ctx.Err() != nil {
			err = fmt.Errorf("%w (%w)", ErrCellCanceled, ctx.Err())
		} else {
			err = fmt.Errorf("%w: past %v", ErrCellTimeout, o.Timeout)
		}
	}
	// a cell that failed midway leaves the interpreter's counters where it
	// stopped; the next cell starts from where this one began
	vm.depth, vm.excStack = depth, vm.excStack[:min(nexc, len(vm.excStack))]
	vm.Stdout, vm.Stderr = oldOut, oldErr
	vm.MaxSteps, vm.allocMax, vm.intrCh = 0, 0, nil

	res = Result{Stdout: out.String(), Stderr: errb.String(), Truncated: out.trunc || errb.trunc, Steps: vm.steps, Elapsed: time.Since(start)}
	if err != nil {
		res.Err = err
	} else {
		res.Value = value
	}
	return res
}

// run evaluates the cell and displays its value, with the budgets live; the
// error is classified as a value.
func (k *Kernel) run(src string) (string, error) {
	v, err := k.sess.Eval(src)
	if err == nil {
		return k.display(v)
	}
	return "", k.asValue(err)
}

// asValue is a thrown exception as a *JuliaError; other errors stay as they are.
func (k *Kernel) asValue(err error) error {
	var thrown *Thrown
	if errors.As(err, &thrown) {
		return k.juliaError(thrown)
	}
	return err
}

// display is the text/plain display of v.
func (k *Kernel) display(v any) (string, error) {
	if k.show == nil {
		return Repr(v), nil
	}
	s, err := k.vm.apply(k.show, []any{v})
	if err != nil {
		return "", k.asValue(err)
	}
	text, _ := s.(string)
	return text, nil
}

// juliaError is the exception as a value, with julia's showerror text when
// Base is there to print it.
func (k *Kernel) juliaError(t *Thrown) error {
	e := &JuliaError{Type: Repr(k.vm.typeOf(t.Value)), Message: Repr(t.Value), Value: t.Value}
	if s, ok := t.Value.(*Struct); ok && k.errText == nil && len(s.Fields) == 1 {
		// without Base: ErrorException("m") and the like print their message
		if m, ok := s.Fields[0].(string); ok {
			e.Message = m
		}
	}
	if k.errText != nil {
		if s, err := k.vm.apply(k.errText, []any{t.Value}); err == nil {
			if text, ok := s.(string); ok {
				e.Message = text
			}
		}
	}
	return e
}

// yielder lets the other goroutines run: nothing where the runtime preempts a
// spinning goroutine, runtime.Gosched on WebAssembly (yield_wasm.go).
var yielder = func() {}

// yieldTick is on the path every statement takes (takeInterrupt): one in 1024
// calls yields.
func (vm *VM) yieldTick() {
	if vm.ticks++; vm.ticks&0x3ff == 0 {
		yielder()
	}
}

// interruptEvery is how often a running cell is interrupted again while its
// ctx is done.
var interruptEvery = 100 * time.Millisecond

// softInterrupts is how many interrupts are soft before the interrupt is hard.
const softInterrupts = 2

// takeInterrupt is whether the running task is the one an interrupt is for
// and one is pending; it takes it, unless the interrupt is hard (intrHard),
// which stays pending: nothing the cell does can swallow it.
func (vm *VM) takeInterrupt() bool {
	vm.yieldTick()
	if !vm.intr.Load() {
		return false
	}
	// a hard interrupt is for whatever task runs (a task the cell started
	// that never yields would otherwise outlive it); a soft one for the cell's
	hard := vm.intrHard.Load()
	if !hard && vm.currentTask() != vm.intrTask {
		return false
	}
	vm.intrSent = false
	return hard || vm.intr.Swap(false)
}

// deliverInterrupt is an interrupt pending while the event loop runs in a task
// that is not the cell's (the cell waits, and a task of Base's waits in the
// loop): the exception is scheduled into the cell's task, as julia does for a
// SIGINT, once until that task has taken it.
func (vm *VM) deliverInterrupt() {
	k := vm.kernel
	if !vm.intr.Load() || k == nil || k.schedule == nil || vm.intrSent || vm.intrTask == nil {
		return
	}
	vm.intrSent = true
	if t := vm.interruptType(); t != nil {
		// the helper is not itself interrupted
		hard := vm.intrHard.Swap(false)
		vm.intr.Store(false)
		// in the newest world: the task whose loop this is began in an older one
		last := vm.taskWorld
		vm.taskWorld = vm.World()
		vm.apply(k.schedule, []any{vm.intrTask, &Struct{Type: t}})
		vm.taskWorld = last
		// a soft interrupt is delivered by that exception, a hard one stays
		vm.intrHard.Store(hard)
		vm.intr.Store(hard)
	}
}

// interruptType is Core.InterruptException, nil on a VM that did not boot.
func (vm *VM) interruptType() *core.DataType {
	if b := vm.Core.own("InterruptException"); b != nil {
		t, _ := b.Value.(*core.DataType)
		return t
	}
	return nil
}

// interruptError is the InterruptException the interpreter raises while
// VM.intr is set.
func (vm *VM) interruptError() error {
	if t := vm.interruptType(); t != nil {
		return vm.exception(t)
	}
	return fmt.Errorf("%w: interrupted", ErrInternal)
}

// isInterrupt is whether v is an InterruptException.
func (vm *VM) isInterrupt(v any) bool {
	s, ok := v.(*Struct)
	t := vm.interruptType()
	return ok && t != nil && s.Type == t
}

// capBuf is an output buffer that keeps its first max bytes (all of them when
// max is negative) and counts as written what it dropped.
type capBuf struct {
	mu    sync.Mutex
	b     []byte
	max   int
	trunc bool
}

func (c *capBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	if c.max >= 0 && len(c.b)+len(p) > c.max {
		p = p[:max(c.max-len(c.b), 0)]
		c.trunc = true
	}
	c.b = append(c.b, p...)
	return n, nil
}

func (c *capBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.b)
}
