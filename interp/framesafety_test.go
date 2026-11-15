package interp

import (
	"fmt"
	"runtime"
	"strconv"
	"testing"
)

// The cases where a julia frame, or the arguments of a call, could outlive it
// (#71). The two invariants they guard:
//
//   - FRAMES. callLambda gives its frame to the pool only when evalBody has
//     returned. By then nothing may hold the frame: a closure holds the VALUES
//     it captured (boxes and fields), not the frame; a Task that yields is
//     suspended INSIDE evalBody, so its frame has not been returned; an
//     exception unwinds through evalBody's return, which frees the frame after
//     the finally clauses ran; a generator or an iterator holds values copied
//     out of the locals. A frame is cleared when it is freed.
//   - ARGUMENTS. The argument slice of a generic call is the CALLER's frame's
//     buffer and is borrowed: a julia method copies it into its locals, a
//     vararg method's rest is a copy (newTuple), a Go method is given a copy.
//     So whatever a callee keeps (push!(G, args)) survives the buffer's reuse.
//
// Each case runs with the pool on and off and must answer what julia answers.
// Cells that need Base are in TestFrameSafetyOnBase (JLGO_BASE=1).

// churn calls a method many times, so that the pool hands the frames of
// earlier calls to later ones.
const churnSrc = `function churn(n)
    Core.Intrinsics.slt_int(n, 1) ? 0 : Core.Intrinsics.add_int(churn(Core.Intrinsics.sub_int(n, 1)), n)
end`

func TestFrameSafetyClosuresAndRecursion(t *testing.T) {
	for _, off := range []bool{false, true} {
		framePoolOff.Store(off)
		vm := New()
		if err := vm.Boot(); err != nil {
			t.Fatal(err)
		}
		s := vm.NewSession()
		must(s.Eval(churnSrc))
		// closures: one captures an argument and a local, one a local that is
		// changed after the capture (a box); both are called after the frame
		// that made them returned, with 200 calls in between that reuse frames
		must(s.Eval(`function mk(a, b)
    x = Core.Intrinsics.add_int(a, b)
    () -> Core.tuple(a, b, x)
end`))
		must(s.Eval(`function mkbox(a)
    n = a
    inc = () -> (n = Core.Intrinsics.add_int(n, 1))
    get = () -> n
    Core.tuple(inc, get)
end`))
		must(s.Eval("c1 = mk(1, 2); c2 = mk(10, 20); bx = mkbox(5)"))
		for i := 0; i < 4; i++ {
			must(s.Eval("churn(200)"))
		}
		for _, c := range []struct{ src, want string }{
			{"c1()", "(1, 2, 3)"}, {"c2()", "(10, 20, 30)"},
			{"Core.getfield(bx, 1)(); Core.getfield(bx, 1)(); Core.getfield(bx, 2)()", "7"},
		} {
			v, err := s.Eval(c.src)
			check(t, fmt.Sprintf("pool off=%v: %s", off, c.src), v, err, c.want)
		}
		// deep recursion: fib(25) makes 242 785 calls, each with a frame
		must(s.Eval(`function fib(n)
    Core.Intrinsics.slt_int(n, 2) ? n : Core.Intrinsics.add_int(fib(Core.Intrinsics.sub_int(n, 1)), fib(Core.Intrinsics.sub_int(n, 2)))
end`))
		v, err := s.Eval("fib(25)")
		check(t, fmt.Sprintf("pool off=%v: fib(25)", off), v, err, "75025")
	}
	framePoolOff.Store(false)

	// what the reuse saves, exactly: fib(25) allocates far less with the pool
	bytes := func(off bool) uint64 {
		framePoolOff.Store(off)
		defer framePoolOff.Store(false)
		vm := New()
		if err := vm.Boot(); err != nil {
			t.Fatal(err)
		}
		s := vm.NewSession()
		must(s.Eval(`function fib(n)
    Core.Intrinsics.slt_int(n, 2) ? n : Core.Intrinsics.add_int(fib(Core.Intrinsics.sub_int(n, 1)), fib(Core.Intrinsics.sub_int(n, 2)))
end`))
		must(s.Eval("fib(10)"))
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		v, err := s.Eval("fib(25)")
		runtime.ReadMemStats(&after)
		check(t, "fib(25)", v, err, "75025")
		return after.TotalAlloc - before.TotalAlloc
	}
	on, offB := bytes(false), bytes(true)
	t.Logf("fib(25) allocates %d bytes with the pool, %d without", on, offB)
	if on*10 > offB*9 {
		t.Errorf("the pool saves less than a tenth: %d bytes against %d", on, offB)
	}
}

// Cases that need Base: a Task that yields in the middle of a frame, a
// generator, an exception through several frames with finally clauses and a
// rethrow, and a method that keeps its argument tuple in a global.
func TestFrameSafetyOnBase(t *testing.T) {
	k := baseKernelForBench(t)
	run := func(src string) string {
		r := k.Eval(t.Context(), src)
		if r.Err != nil {
			return "!" + r.Err.Error()
		}
		return r.Value
	}
	// 150 nested calls whose frames the pool hands to later calls
	const churn = `churn(150)`
	if r := k.Eval(t.Context(), `churn(n) = n < 1 ? 0 : churn(n - 1) + n; churn(3)`); r.Err != nil {
		t.Fatal(r.Err)
	}
	cases := []struct{ name, src, want string }{
		// TASK: yields inside work(), whose frame (locals l, arguments a and b)
		// must stay intact while other calls reuse the pool; the frame is not
		// freed until work returns, in the task, after the yield.
		{"a task that yields mid-frame", `let results = Int[]
    function work(a, b)
        l = a * 2
        yield()
        push!(results, l + b + a)
    end
    ts = [@async work(i, 100) for i in 1:5]
    ` + churn + `
    foreach(wait, ts)
    sort(results)
end`, "[103, 106, 109, 112, 115]"},
		// TASK through a Channel: the producer's frame is suspended in put!
		{"a task suspended on a channel", `let ch = Channel{Int}(1)
    function produce(base, n)
        local acc = base
        for i in 1:n
            acc += i
            put!(ch, acc)
        end
    end
    t = @async produce(100, 4)
    got = Int[]
    for _ in 1:4
        ` + churn + `
        push!(got, take!(ch))
    end
    wait(t)
    got
end`, "[101, 103, 106, 110]"},
		// GENERATOR / ITERATOR: hold the values of a frame that returned
		{"a generator made by a returned frame", `let
    mkgen(k) = (i * k for i in 1:3)
    g1 = mkgen(10); g2 = mkgen(7)
    ` + churn + `
    (collect(g1), collect(g2))
end`, "([10, 20, 30], [7, 14, 21])"},
		{"an iterator state", `let
    mkit(v) = (state = 0; () -> (state += 1; v[state]))
    it = mkit([5, 6, 7])
    ` + churn + `
    (it(), it(), it())
end`, "(5, 6, 7)"},
		// EXCEPTIONS: through three frames, finally reads the locals of its frame,
		// the catch runs other calls then rethrows; the frames are freed as the
		// exception unwinds, after their finally clauses ran
		{"an exception through frames, finally and rethrow", `let log = String[]
    function inner(a)
        l = a + 1
        throw(ErrorException(string("boom", l)))
    end
    function mid(a)
        l2 = a * 3
        try
            inner(a)
        finally
            push!(log, string("fin", a, l2))
        end
    end
    function outer(a)
        try
            mid(a)
        catch e
            ` + churn + `
            push!(log, sprint(showerror, e))
            rethrow()
        end
    end
    r = try; outer(7); "no"; catch e2; sprint(showerror, e2); end
    (log, r)
end`, `(["fin721", "boom8"], "boom8")`},
		// ARGUMENTS KEPT: a vararg method stores its argument tuple in a global,
		// and a method stores its arguments one by one; the caller's buffer is
		// reused by the next calls
		{"a callee that keeps its arguments", `let G = Any[]
    keep(args...) = (push!(G, args); nothing)
    keep2(a, b) = (push!(G, (a, b)); nothing)
    function drive()
        keep(1, 2, 3); keep(4, 5, 6); keep2(7, 8); keep(9); keep2(10, 11)
    end
    drive(); drive()
    G
end`, "Any[(1, 2, 3), (4, 5, 6), (7, 8), (9,), (10, 11), (1, 2, 3), (4, 5, 6), (7, 8), (9,), (10, 11)]"},
	}
	for _, off := range []bool{false, true} {
		framePoolOff.Store(off)
		for _, c := range cases {
			got := run("repr(" + c.src + ")")
			if u, err := strconv.Unquote(got); err == nil {
				got = u
			}
			if got != c.want {
				t.Errorf("%s (pool off=%v):\n  got  %s\n  want %s", c.name, off, got, c.want)
			}
		}
	}
	framePoolOff.Store(false)
}
