package interp

import (
	"runtime"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
)

// An Expr's args is one Vector{Any} that shares the Expr's arguments: a
// write, a replacement of its memory or size, and setfield! of the Expr
// itself all reach what lowering reads. Each want is julia 1.12.6's answer
// to the same steps (through Base's indexing and push! there).
func TestExprArgs(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	if _, err := s.Eval(`
set!(a, i, x) = Core.memoryrefset!(Core.memoryrefnew(getfield(a, :ref), i, true), x, :not_atomic, true)
mset!(m, i, x) = Core.memoryrefset!(Core.memoryrefnew(Core.memoryrefnew(m), i, true), x, :not_atomic, true)
`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ src, want string }{
		{"getfield(:(f(1)), :args)", "Any[:f, 1]"},
		{"getfield(:(a + 1), :args)", "Any[:+, :a, 1]"},
		{"ex = :(f(1)); getfield(ex, :args) === getfield(ex, :args)", "true"},
		// julia shows these Exprs as :(f(2)), :(g(3, 4)), :((g, 3, 4)) …:
		// their head and args are compared, as the quoted form is not here
		{"ex = :(f(1)); set!(getfield(ex, :args), 2, 2); ex", "Expr(:call, :f, 2)"},
		{"ex = :(f(1)); a = Array{Any,1}(undef, 3); set!(a, 1, :g); set!(a, 2, 3); set!(a, 3, 4); setfield!(ex, :args, a); getfield(ex, :args)", "Any[:g, 3, 4]"},
		{"ex = :(g(3, 4)); setfield!(ex, :head, :tuple); ex", "Expr(:tuple, :g, 3, 4)"},
		{"ex = :(f(1)); try setfield!(ex, :head, 1) catch e; e end", `TypeError(:setfield!, "", Symbol, 1)`},
		{"ex = :(f(1)); try setfield!(ex, :args, 1) catch e; e end", `TypeError(:setfield!, "", Vector{Any}, 1)`},
		{"ex = :(f(1)); try setfield!(ex, :nope, 1) catch e; e end", `ErrorException("type Expr has no field nope")`},
		// what push! does: new memory, then the vector's ref and size
		{"ex = :(f(1)); a = getfield(ex, :args); m = Memory{Any}(undef, 3); mset!(m, 1, :f); mset!(m, 2, 1); mset!(m, 3, 5); setfield!(a, :ref, Core.memoryrefnew(m)); setfield!(a, :size, (3,)); ex", "Expr(:call, :f, 1, 5)"},
		{"ex = :(f(1, 2)); setfield!(getfield(ex, :args), :size, (1,)); ex", "Expr(:call, :f)"},
		{"ex = :(f(:x, $(Expr(:call, :g)))); ex", "Expr(:call, :f, QuoteNode(:x), Expr(:call, :g))"},
		// lowering reads the written arguments
		{"ex = :(Core.tuple(1)); set!(getfield(ex, :args), 2, 7); Core.eval(Core, ex)", "(7,)"},
	} {
		v, err := s.Eval(c.src)
		check(t, c.src, v, err, c.want)
	}

	// a slot never assigned reaches lowering as nothing, which has no undef
	v, err := s.Eval("ex = :(f(1)); a = getfield(ex, :args); m = Memory{Any}(undef, 3); mset!(m, 1, :f); setfield!(a, :ref, Core.memoryrefnew(m)); setfield!(a, :size, (3,)); ex")
	check(t, "an unassigned slot", v, err, "Expr(:call, :f, nothing, nothing)")

	// Go code that replaces Args gets a new vector on the next read
	ex := expr.New("call", expr.Symbol("f"))
	v1 := vm.exprArgs(ex)
	if vm.exprArgs(ex) != v1 {
		t.Error("a second read made a new vector")
	}
	ex.Args = []any{expr.Symbol("g"), int64(1)}
	if v2 := vm.exprArgs(ex); v2 == v1 || Repr(v2) != "Any[:g, 1]" {
		t.Errorf("after Args changed: %s", Repr(v2))
	}
	// a vector whose fields are broken leaves its Expr without arguments
	v1.Fields[0] = undef
	syncArgs(v1)
	if ex.Args != nil {
		t.Errorf("syncArgs of a broken vector: %v", ex.Args)
	}
	if sharesArgs(v1, ex.Args) {
		t.Error("sharesArgs of a broken vector")
	}
}

// The table of vectors does not keep an Expr alive: its entry goes with it.
func TestExprArgsCleanup(t *testing.T) {
	vm := New()
	func() {
		for i := 0; i < 10; i++ {
			vm.exprArgs(expr.New("call", expr.Symbol("f"), int64(i)))
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		runtime.GC()
		vm.argMu.Lock()
		n := len(vm.argVectors)
		vm.argMu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d vectors left after their Exprs were collected", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
