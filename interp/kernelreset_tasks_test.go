package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Kernel.Reset stops the tasks and timers cells started (#104). The scenarios
// below run on the Base image with stopping on, and again with it off
// (resetStopsTasks): each must have nothing to report the first time and
// something the second, or it proves nothing.

// ghostRun is a cell run of the scenarios, bounded in time: a kernel that a
// ghost task has broken must fail the test, not hang it.
func ghostRun(k *Kernel, src string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return k.Eval(ctx, src)
}

// runOK is a run that must work: no error, the value, and nothing printed.
func runOK(k *Kernel, name, src, want string) []string {
	r := ghostRun(k, src)
	var bad []string
	if r.Err != nil {
		bad = append(bad, fmt.Sprintf("%s: %v", name, r.Err))
	}
	if r.Value != want {
		bad = append(bad, fmt.Sprintf("%s: value %q, want %q", name, r.Value, want))
	}
	if r.Stdout != "" || r.Stderr != "" {
		bad = append(bad, fmt.Sprintf("%s printed %q %q", name, r.Stdout, r.Stderr))
	}
	return bad
}

func withStopping(t *testing.T, on bool) {
	old := resetStopsTasks
	resetStopsTasks = on
	t.Cleanup(func() { resetStopsTasks = old })
}

type stopScenario struct {
	name string
	// yieldMatters: the scenario also fails when the kernel does not yield
	// before the cut (kernelYieldsFirst)
	yieldMatters bool
	run          func(t *testing.T, k *Kernel) []string
}

var stopScenarios = []stopScenario{
	// go-notebook's reproduction: run 1 yields, which starts the tasks Base made
	// in its initializers (the profile listeners) and parks them in lists of
	// Base's own; the Reset rolls those lists back under them, and run 2 fails
	// (not ported yet: ccall(:jl_get_backtrace)) and so do the runs after it
	// ("val already in a list"). The task of the cell, parked on its timer, is
	// the other half: it must not run again. Runs 2, 3 and 4 must all work.
	{"a parked task and the runs after it", true, func(t *testing.T, k *Kernel) []string {
		r := ghostRun(k, `@async (sleep(0.5); println("ghost")); sleep(0.01)`)
		var bad []string
		if r.Err != nil {
			bad = append(bad, fmt.Sprintf("run 1: %v", r.Err))
		}
		if tasks, timers, err := k.Tasks(); err != nil || tasks != 1 || timers < 1 {
			bad = append(bad, fmt.Sprintf("before Reset: %d tasks, %d timers, %v; want 1 task and a timer", tasks, timers, err))
		}
		info, err := k.Reset()
		if err != nil || info.Tasks != 1 || info.Timers < 1 {
			bad = append(bad, fmt.Sprintf("Reset: %+v, %v; want 1 task and a timer stopped", info, err))
		}
		bad = append(bad, runOK(k, "run 2", `sleep(1); 2`, "2")...)
		bad = append(bad, runOK(k, "run 3", `x = 3; x`, "3")...)
		bad = append(bad, runOK(k, "run 4", `sleep(0.1); 4`, "4")...)
		return bad
	}},
	// a periodic timer keeps firing in every later run
	{"a periodic timer", false, func(t *testing.T, k *Kernel) []string {
		r := ghostRun(k, `tm = Timer(t -> println("tick"), 0; interval=0.05); sleep(0.12)`)
		var bad []string
		if r.Err != nil || !strings.Contains(r.Stdout, "tick") {
			bad = append(bad, fmt.Sprintf("run 1: %v, printed %q: the timer does not fire", r.Err, r.Stdout))
		}
		info, err := k.Reset()
		if err != nil || info.Timers < 1 {
			bad = append(bad, fmt.Sprintf("Reset: %+v, %v; want the timer closed", info, err))
		}
		bad = append(bad, runOK(k, "run 2", `sleep(0.3); 2`, "2")...)
		bad = append(bad, runOK(k, "run 3", `3`, "3")...)
		return bad
	}},
	// tasks blocked on a Channel, on a Condition and in sleep
	{"tasks blocked on a Channel, a Condition and sleep", false, func(t *testing.T, k *Kernel) []string {
		r := ghostRun(k, `c = Channel(1); @async take!(c); cond = Condition(); @async wait(cond); @async sleep(60); yield()`)
		var bad []string
		if r.Err != nil {
			bad = append(bad, fmt.Sprintf("run 1: %v", r.Err))
		}
		if tasks, _, _ := k.Tasks(); tasks != 3 {
			bad = append(bad, fmt.Sprintf("before Reset: %d tasks, want 3", tasks))
		}
		info, err := k.Reset()
		if err != nil || info.Tasks != 3 {
			bad = append(bad, fmt.Sprintf("Reset: %+v, %v; want 3 tasks stopped", info, err))
		}
		if tasks, timers, _ := k.Tasks(); tasks != 0 || timers != 0 {
			bad = append(bad, fmt.Sprintf("after Reset: %d tasks and %d timers are alive", tasks, timers))
		}
		bad = append(bad, runOK(k, "run 2", `sleep(0.1); 2`, "2")...)
		return bad
	}},
	// a task that never ran holds nothing, and a task that finished is not
	// alive: of the three tasks of the cell, only the parked one is stopped
	{"a task that never ran, one that finished, one parked", false, func(t *testing.T, k *Kernel) []string {
		var bad []string
		bad = append(bad, runOK(k, "run 1", `t1 = Task(() -> println("never")); t2 = @async 1; wait(t2); @async sleep(60); yield(); istaskdone(t2)`, "true")...)
		if tasks, _, _ := k.Tasks(); tasks != 1 {
			bad = append(bad, fmt.Sprintf("%d tasks alive, want the parked one", tasks))
		}
		if info, err := k.Reset(); err != nil || info.Tasks != 1 {
			bad = append(bad, fmt.Sprintf("Reset: %+v, %v; want 1 task stopped", info, err))
		}
		return bad
	}},
	// Base's own tasks are untouched: the same ones are alive after a Reset
	// that stopped a cell's
	{"Base's own tasks stay", false, func(t *testing.T, k *Kernel) []string {
		before := map[*taskRec]bool{}
		for rec := range k.vm.running {
			before[rec] = true
		}
		var bad []string
		if tasks, timers, _ := k.Tasks(); tasks != 0 || timers != 0 {
			bad = append(bad, fmt.Sprintf("a fresh kernel has %d tasks and %d timers of cells", tasks, timers))
		}
		ghostRun(k, `@async sleep(60); yield()`)
		if info, err := k.Reset(); err != nil || info.Tasks != 1 {
			bad = append(bad, fmt.Sprintf("Reset: %+v, %v", info, err))
		}
		if len(k.vm.running) != len(before) {
			bad = append(bad, fmt.Sprintf("%d tasks alive after Reset, %d before the cell", len(k.vm.running), len(before)))
		}
		for rec := range k.vm.running {
			if !before[rec] {
				bad = append(bad, "a task that Base did not have is alive after Reset")
			}
		}
		for rec := range before {
			if _, ok := k.vm.running[rec]; !ok {
				bad = append(bad, "one of Base's own tasks was stopped")
			}
		}
		return bad
	}},
}

func TestKernelResetStopsTasks(t *testing.T) {
	img := resetImage(t)
	for _, sc := range stopScenarios {
		// on, with stopping off, and (where it matters) with no first yield
		for _, mode := range []struct {
			name         string
			stops, yield bool
		}{{"on", true, true}, {"stopping off", false, true}, {"no first yield", true, false}} {
			if mode.name == "no first yield" && !sc.yieldMatters {
				continue
			}
			t.Run(sc.name+", "+mode.name, func(t *testing.T) {
				withStopping(t, mode.stops)
				old := kernelYieldsFirst
				kernelYieldsFirst = mode.yield
				t.Cleanup(func() { kernelYieldsFirst = old })
				k := resetKernel(t, img)
				bad := sc.run(t, k)
				switch {
				case mode.name == "on" && len(bad) > 0:
					t.Errorf("%s", strings.Join(bad, "; "))
				case mode.name != "on" && len(bad) == 0:
					t.Errorf("passes with %s: the scenario proves nothing", mode.name)
				case mode.name != "on":
					t.Logf("with %s: %s", mode.name, strings.Join(bad, "; "))
				}
			})
		}
	}
}

// A kernel on a VM whose Base has a yield calls it once, before the cut: a
// function Base lacks is not an error, and one that fails is.
func TestKernelFirstYield(t *testing.T) {
	for _, c := range []struct {
		name, src string
		fails     bool
	}{{"no yield", "", false}, {"a yield", `yield() = Core.println("yielded")`, false}, {"a yield that fails", `yield() = throw(ErrorException("no"))`, true}} {
		vm := bootedVM(t)
		vm.baseModule = vm.Main
		if c.src != "" {
			if _, err := vm.sessionFor(vm.Main).Eval(c.src); err != nil {
				t.Fatal(err)
			}
		}
		var out strings.Builder
		vm.Stdout = &out
		k, err := NewKernel(vm, KernelOptions{})
		if (err != nil) != c.fails {
			t.Errorf("%s: %v", c.name, err)
		}
		if c.name == "a yield" && (err != nil || out.String() != "yielded\n") {
			t.Errorf("the yield was not called once: %q, %v", out.String(), err)
		}
		if k != nil {
			k.Close()
		}
	}
}

// fakeTask is a started task that waits for a baton, as the goroutine of a
// parked task does, without Base: the kill ends it as runTask's does.
func fakeTask(vm *VM) (*Struct, *taskRec) {
	t := &Struct{Type: vm.tTask, Fields: make([]any, 19)}
	rec := vm.newTaskRec(-1, 0)
	vm.addTaskRec(t, rec)
	rec.started = true
	if vm.running == nil {
		vm.running = map[*taskRec]struct{}{}
	}
	vm.running[rec] = struct{}{}
	vm.liveTasks++
	go func() {
		if b := <-rec.baton; b.kill {
			vm.liveTasks--
			rec.exited = true
			delete(vm.running, rec)
			close(rec.done)
		}
	}()
	return t, rec
}

// fakeTimer is an armed libuv timer a cell opened.
func fakeTimer(t *testing.T, vm *VM, active bool) *uvHandle {
	t.Helper()
	m := newMemoryN(memUInt8Type, 8)
	h, err := vm.uvHandle("test", vm.voidPtr(m), true)
	if err != nil {
		t.Fatal(err)
	}
	h.kind, h.active = uvTimer, active
	return h
}

// The order and the bookkeeping of the stop, without Base: the tasks and timers
// are gone when the log is about to be replayed, the count says how many, a task
// is marked done, and what was alive at the cut is left.
func TestKernelResetStopsBeforeReplay(t *testing.T) {
	for _, stops := range []bool{true, false} {
		t.Run(fmt.Sprintf("stopping %v", stops), func(t *testing.T) {
			withStopping(t, stops)
			k := bootKernel(t, KernelOptions{})
			vm := k.vm
			// a task and a timer of Base's, alive at the cut
			baseTask, baseRec := fakeTask(vm)
			baseTimer := fakeTimer(t, vm, true)
			k.snap = vm.takeSnap()
			// a cell's: two tasks, an armed timer, a timer not armed, and a handle
			// that is closing
			cellTask, cellRec := fakeTask(vm)
			_, _ = fakeTask(vm)
			armed := fakeTimer(t, vm, true)
			idle := fakeTimer(t, vm, false)
			closing := fakeTimer(t, vm, true)
			vm.loop.closing = append(vm.loop.closing, closing, baseTimer)
			if tasks, timers, err := k.Tasks(); err != nil || tasks != 2 || timers != 2 {
				t.Fatalf("before: %d tasks, %d timers, %v", tasks, timers, err)
			}
			var atReplay [2]int
			var steps []string
			resetTrace = func(step string) {
				steps = append(steps, step)
				atReplay[0], atReplay[1] = k.counts()
			}
			defer func() { resetTrace = nil }()
			mem := newMemory(memAnyType, []any{"old"})
			mem.undo = vm.undo
			mem.Set(0, "new") // what the replay restores
			info, err := k.Reset()
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) != 1 || steps[0] != "replay" || mem.Get(0) != any("old") {
				t.Errorf("the replay: steps %v, memory %v", steps, mem.Get(0))
			}
			if stops {
				if info.Tasks != 2 || info.Timers != 2 || atReplay != [2]int{0, 0} {
					t.Errorf("stopped: %+v, alive when the log was replayed: %v; want 2 tasks and 2 timers stopped, none alive at the replay", info, atReplay)
				}
				if cellTask.Fields[7] != any(taskStateDone) || !cellRec.exited {
					t.Errorf("the cell's task: state %v, exited %v", cellTask.Fields[7], cellRec.exited)
				}
				if armed.active || idle.active || vm.loop.handles[armed.mem] != nil || len(vm.loop.closing) != 1 || vm.loop.closing[0] != baseTimer {
					t.Error("a cell's handles are still in the loop")
				}
			} else if info.Tasks != 0 || info.Timers != 0 || atReplay[0] != 2 {
				t.Errorf("with stopping off: %+v, alive at the replay %v", info, atReplay)
			}
			// what was alive at the cut is untouched
			if _, alive := vm.running[baseRec]; !alive || baseTask.Fields[7] == any(taskStateDone) || !baseTimer.active || vm.loop.handles[baseTimer.mem] == nil {
				t.Error("Base's task or timer was stopped")
			}
			// nothing is left to stop, in either mode
			vm.Close()
		})
	}
}

// A reload counts what the old VM's close ended, and Tasks is refused as Get is.
func TestKernelResetCountsOnReload(t *testing.T) {
	k := bootKernel(t, KernelOptions{})
	vm := k.vm
	fakeTask(vm)
	fakeTimer(t, vm, true)
	vm.undo.max = 1
	mem := newMemory(memAnyType, []any{"x"})
	mem.undo = vm.undo
	mem.Set(0, 1)
	mem.Set(0, 2)
	info, err := k.Reset()
	if err != nil || !info.Reloaded || info.Tasks != 1 || info.Timers != 1 {
		t.Fatalf("Reset past the cap: %+v, %v", info, err)
	}
	k.busy.Store(true)
	if _, _, err := k.Tasks(); !errors.Is(err, ErrKernelBusy) {
		t.Errorf("Tasks while a cell runs: %v", err)
	}
	k.busy.Store(false)
	if tasks, timers, err := k.Tasks(); err != nil || tasks != 0 || timers != 0 {
		t.Errorf("Tasks of a reloaded kernel: %d %d %v", tasks, timers, err)
	}
	k.Close()
	if _, _, err := k.Tasks(); !errors.Is(err, ErrKernelClosed) {
		t.Errorf("Tasks after Close: %v", err)
	}
}
