package interp

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The task programs of testdata/tasks.jl are answered by julia 1.12.6
// (JLGO_ORACLE=1 JLGO_UPDATE_TASKS=1) into testdata/tasks.golden and replayed
// here under the race detector: each program's last value, as julia shows it,
// must be what the Go interpreter computes.
//
// Both run the same text, which uses Core and ccall only, because the VM runs
// no Base in a unit test (the Base probe, base_test.go, runs the Base-level
// programs). Where julia's Base picks the next task when one finishes
// (task_done_hook) and queues the root task (schedule), the Go side defines
// the one thing each program needs, taskPrelude, in its Base: a queue of one.

const (
	tasksPath   = "testdata/tasks.jl"
	tasksGolden = "testdata/tasks.golden"
)

const taskHeader = `
mutable struct Log; v::Any; end
const LOG = Log(())
note(x) = (setfield!(LOG, :v, (getfield(LOG, :v)..., x)); nothing)
cur() = ccall(:jl_get_current_task, Ref{Core.Task}, ())
newtask(f) = ccall(:jl_new_task, Ref{Core.Task}, (Any, Any, Int), f, nothing, 0)
setnext(t) = ccall(:jl_set_next_task, Cvoid, (Any,), t)
getnext() = ccall(:jl_get_next_task, Ref{Core.Task}, ())
doswitch() = ccall(:jl_switch, Cvoid, ())
started(t) = ccall(:jl_is_task_started, Int32, (Any,), t)
tid(t) = ccall(:jl_get_task_tid, Int16, (Any,), t)
state(t) = getfield(t, :_state)
result(t) = getfield(t, :result)
i8(x) = Core.Intrinsics.trunc_int(Int8, x)
i16(x) = Core.Intrinsics.trunc_int(Int16, x)
i32(x) = Core.Intrinsics.trunc_int(Int32, x)
u64(x) = Core.Intrinsics.bitcast(UInt64, x)
root = cur()
# switch to t and come back when it finishes: the root task is queued, and
# what runs when a task ends is the queue's
function run(t)
    Base.schedule(root)
    setnext(t)
    doswitch()
end
`

const taskPrelude = `
# @cfunction (c.jl:64-79) for one argument type, which is all a libuv callback
# has; the Base one splats the argument types, which needs Base.iterate
macro cfunction(f, rt, at)
    first = Core.memoryrefget(getfield(getfield(at, :args), :ref), :not_atomic, false)
    Expr(:escape, Expr(:cfunction, Ptr{Cvoid}, QuoteNode(f), rt, Expr(:call, GlobalRef(Core, :svec), first), QuoteNode(:ccall)))
end
mutable struct TQ; t::Any; end
const QUEUE = TQ(nothing)
schedule(t) = (setfield!(QUEUE, :t, t); t)
function task_done_hook(t)
    nxt = getfield(QUEUE, :t)
    setfield!(QUEUE, :t, nothing)
    ccall(:jl_set_next_task, Cvoid, (Any,), nxt)
    ccall(:jl_switch, Cvoid, ())
end
`

// taskVM is a booted VM whose Base has taskPrelude.
func taskVM(t testing.TB) *VM {
	t.Helper()
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vm.baseModule = vm.Base
	if _, err := vm.sessionFor(vm.Base).Eval(taskPrelude); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(vm.Close)
	return vm
}

func loadTaskPrograms(t testing.TB) []string {
	t.Helper()
	b, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range strings.Split(string(b), "#---\n")[1:] {
		out = append(out, taskHeader+p)
	}
	return out
}

func TestTasksAgainstJulia(t *testing.T) {
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_TASKS") != "" {
		updateTasksGolden(t, loadTaskPrograms(t))
	}
	f, err := os.Open(tasksGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	for sc.Scan() {
		var c evalCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		n++
		v, err := taskVM(t).NewSession().Eval(c.Src)
		if err != nil {
			t.Errorf("program %d:\n%s\nGo error: %v\njulia: %s", n, c.Src, err, c.Want)
			continue
		}
		want := strings.ReplaceAll(c.Want, "Main.OracleEval", "Main.L")
		if got := Repr(v); got != want {
			t.Errorf("program %d:\n%s\nGo:    %s\njulia: %s", n, c.Src, got, want)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if want := len(loadTaskPrograms(t)); n != want {
		t.Fatalf("%d golden answers for %d programs: regenerate with JLGO_ORACLE=1 JLGO_UPDATE_TASKS=1", n, want)
	}
}

func updateTasksGolden(t *testing.T, progs []string) {
	t.Helper()
	p, err := oracle.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, err := os.Create(tasksGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	for i, src := range progs {
		ans, err := p.Call("eval", src)
		if err != nil {
			t.Fatalf("julia rejects program %d:\n%s\n%v", i+1, src, err)
		}
		_, shown, ok := strings.Cut(ans, "\n=> ")
		if !ok {
			t.Fatalf("program %d: julia answered %q", i+1, ans)
		}
		if err := enc.Encode(evalCase{Src: src, Want: shown}); err != nil {
			t.Fatal(err)
		}
	}
}
