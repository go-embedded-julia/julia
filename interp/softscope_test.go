package interp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
)

// The soft scope of the REPL and IJulia (#54): REPL.softscope
// (stdlib/REPL/src/REPL.jl:145-161) and lowering's softscope, with
// defined-julia-global (ast.c:172-182). The cases are those of julia 1.12.6's
// test/syntax.jl:2113-2221 ("optional soft scope: #28789, #33864"), written
// with `while` and Core's intrinsics so that they run without Base (the same
// cases, with `for` and `+=`, run on the Base image: TestKernelBase). Every
// cell has a step budget and a timeout, so that a wrong lowering that makes a
// loop that never ends fails at once instead of hanging.

const softPre = `import Core.Intrinsics: add_int, slt_int, sub_int
`

func softKernel(t *testing.T, hard bool) *Kernel {
	t.Helper()
	k := bootKernel(t, KernelOptions{CellOptions: CellOptions{HardScope: hard, MaxSteps: 200000, Timeout: 20 * time.Second}})
	if r := k.Eval(context.Background(), softPre); r.Err != nil {
		t.Fatal(r.Err)
	}
	return k
}

// run is a cell, which must work.
func run(t *testing.T, k *Kernel, src string) string {
	t.Helper()
	r := k.Eval(context.Background(), src)
	if r.Err != nil {
		t.Fatalf("%s\n  %v", src, r.Err)
	}
	return r.Value
}

func undefVar(t *testing.T, k *Kernel, src string) {
	t.Helper()
	r := k.Eval(context.Background(), src)
	var je *JuliaError
	if !errors.As(r.Err, &je) || je.Type != "UndefVarError" {
		t.Errorf("%s\n  want an UndefVarError, got %+v", src, r)
	}
}

func TestSoftScopeCases(t *testing.T) {
	k := softKernel(t, false)

	// new global included in the same expression (x28789)
	if got := run(t, k, `begin
    x = 0
    i = 0
    while slt_int(i, 2)
        i = add_int(i, 1)
        x = add_int(x, i)
    end
    x
end`); got != "3" {
		t.Errorf("a new global in the same expression: %s, julia 3", got)
	}
	// a global defined in a separate input (y28789)
	run(t, k, `y = 1`)
	run(t, k, `n = 0
while slt_int(n, 10)
    n = add_int(n, 1)
    y = add_int(y, n)
end`)
	if got := run(t, k, `y`); got != "56" {
		t.Errorf("a global of a separate input: %s, julia 56", got)
	}
	// a new global assigned in a loop and after it, in the same soft scope (z28789)
	if got := run(t, k, `begin
    j = 0
    while slt_int(j, 1)
        j = add_int(j, 1)
        z = 10
    end
    seen = z
    z = 0
    seen
end`); got != "10" {
		t.Errorf("a global assigned in a loop: %s, julia 10", got)
	}
	if got := run(t, k, `z`); got != "0" {
		t.Errorf("z after the cell: %s, julia 0", got)
	}
	// let shadows, and is always a hard scope
	run(t, k, `let y = 0
    y = 1
end`)
	run(t, k, `let
    y = -8
end`)
	if got := run(t, k, `y`); got != "56" {
		t.Errorf("let: %s, julia 56", got)
	}
	// nested loops assign to the global (z28789 = 42)
	run(t, k, `begin
    a = 0
    while slt_int(a, 1)
        a = add_int(a, 1)
        b = 0
        while slt_int(b, 1)
            b = add_int(b, 1)
            c = 0
            while slt_int(c, 1)
                c = add_int(c, 1)
                z = 42
            end
        end
    end
end`)
	if got := run(t, k, `z`); got != "42" {
		t.Errorf("nested loops: %s, julia 42", got)
	}
	// a name assigned inside a let is not global (ww28789)
	run(t, k, `let x = 0
    ww = 88
    let y = 3
        ww = 89
    end
    ww
end`)
	undefVar(t, k, `ww`)
	// and in a loop that is inside a loop
	run(t, k, `begin
    p = 0
    while slt_int(p, 1)
        p = add_int(p, 1)
        ww = 88
        q = 0
        while slt_int(q, 1)
            q = add_int(q, 1)
            ww = 89
        end
        ww
    end
end`)
	undefVar(t, k, `ww`)
	// a function body is never soft (f28789)
	run(t, k, `function f28789()
    z = 43
end
f28789()`)
	if got := run(t, k, `z`); got != "42" {
		t.Errorf("a function: %s, julia 42", got)
	}
	// a struct is always a hard scope (issue 38650)
	run(t, k, `f38650() = 0`)
	run(t, k, `struct S38650
    f38650() = 1
end`)
	if got := run(t, k, `f38650()`); got != "0" {
		t.Errorf("struct: %s, julia 0", got)
	}
	// the cell of the issue: a loop assigning an existing global
	run(t, k, `s = 0
k1 = 0
while slt_int(k1, 10)
    k1 = add_int(k1, 1)
    s = add_int(s, k1)
end`)
	if got := run(t, k, `s`); got != "55" {
		t.Errorf("the cell of #54: %s, julia 55", got)
	}
	// local and global annotations
	run(t, k, `t = 5
m = 0
while slt_int(m, 2)
    m = add_int(m, 1)
    local t = 100
    t = add_int(t, 1)
end`)
	if got := run(t, k, `t`); got != "5" {
		t.Errorf("local: %s, julia 5", got)
	}
	run(t, k, `u = 5
m = 0
while slt_int(m, 2)
    m = add_int(m, 1)
    global u
    u = add_int(u, 1)
end`)
	if got := run(t, k, `u`); got != "7" {
		t.Errorf("global: %s, julia 7", got)
	}
}

// The same cell under hard scope (CellOptions.HardScope): the loop's name is a
// new local, as in a file, and the global it meant is not changed.
func TestHardScopeStillWorks(t *testing.T) {
	k := softKernel(t, true)
	run(t, k, `s = 0`)
	undefVar(t, k, `m = 0
while slt_int(m, 3)
    m = add_int(m, 1)
    s = add_int(s, m)
end`)
	// global says it, in either scope
	run(t, k, `m = 0
while slt_int(m, 3)
    global m, s
    m = add_int(m, 1)
    s = add_int(s, m)
end`)
	if got := run(t, k, `s`); got != "6" {
		t.Errorf("global in a hard scope: %s", got)
	}
	// a kernel soft by default can turn it off for one cell... (a per-cell
	// HardScope), and a kernel hard by default stays so for every cell
	soft := softKernel(t, false)
	run(t, soft, `s = 0`)
	r := soft.EvalWith(context.Background(), `m = 0
while slt_int(m, 3)
    m = add_int(m, 1)
    s = add_int(s, m)
end`, CellOptions{HardScope: true})
	var je *JuliaError
	if !errors.As(r.Err, &je) || je.Type != "UndefVarError" {
		t.Errorf("a hard cell on a soft kernel: %+v", r)
	}
	if got := run(t, soft, `m = 0
while slt_int(m, 3)
    m = add_int(m, 1)
    s = add_int(s, m)
end
s`); got != "6" {
		t.Errorf("the next cell is soft again: %s", got)
	}
}

// A loop that never ends under a soft scope with the wrong lowering must fail
// fast: the budget of the cell ends it.
func TestSoftScopeBudgetFailsFast(t *testing.T) {
	k := softKernel(t, false)
	r := k.EvalWith(context.Background(), `while true end`, CellOptions{MaxSteps: 5000})
	if !errors.Is(r.Err, ErrStepLimit) {
		t.Errorf("a loop that never ends: %+v", r)
	}
}

func TestSoftscopeRewrite(t *testing.T) {
	sym := expr.Symbol("x")
	call := expr.New("call", expr.Symbol("f"))
	wrapped := func(e any) any { return expr.New("block", expr.New("softscope", true), e) }
	same := func(name string, e any) {
		t.Helper()
		if got := softscope(e); got != e {
			t.Errorf("%s: rewritten to %v", name, got)
		}
	}
	// a non-Expr, and the forms that define no scope, are left
	same("a symbol", sym)
	same("a number", int64(1))
	for _, h := range []expr.Symbol{"meta", "import", "using", "export", "module", "error", "incomplete", "thunk"} {
		same(string(h), expr.New(h, sym))
	}
	same("global of symbols", expr.New("global", sym, expr.Symbol("y")))
	same("empty global", expr.New("global"))
	// anything else is wrapped, a global with an assignment too
	for name, e := range map[string]*expr.Expr{"call": call, "for": expr.New("for", sym), "global =": expr.New("global", expr.New("=", sym, int64(1))),
		"a symbol and an assignment": expr.New("global", sym, expr.New("=", sym, int64(1)))} {
		got, ok := softscope(e).(*expr.Expr)
		if !ok || got.Head != "block" || len(got.Args) != 2 || got.Args[1] != any(e) {
			t.Errorf("%s: %v", name, got)
			continue
		}
		if ss, ok := got.Args[0].(*expr.Expr); !ok || ss.Head != "softscope" || len(ss.Args) != 1 || ss.Args[0] != true {
			t.Errorf("%s: the marker is %v", name, got.Args[0])
		}
	}
	// :toplevel is mapped over, a new Expr that holds the results
	top := expr.New("toplevel", call, sym, expr.New("import", sym))
	got := softscope(top).(*expr.Expr)
	if got == top || got.Head != "toplevel" || len(got.Args) != 3 || got.Args[1] != any(sym) {
		t.Fatalf("toplevel: %v", got)
	}
	if b, ok := got.Args[0].(*expr.Expr); !ok || b.Head != "block" {
		t.Errorf("toplevel, a call: %v", got.Args[0])
	}
	_ = wrapped
}

// defined-julia-global: a global the module defines itself, in the world the
// form runs in; not an import, a constant, a name only export made, or a name
// that does not exist yet.
func TestDefinedGlobal(t *testing.T) {
	vm := bootedVM(t)
	m := vm.Main
	b := m.declare("g", 1)
	b.Value = int64(1)
	vm.taskWorld = 10
	if !vm.definedGlobal(m, "g") {
		t.Error("a global of the module")
	}
	if vm.definedGlobal(m, "nothing_here") {
		t.Error("a name that does not exist")
	}
	later := m.declare("later", 100)
	later.Value = int64(1)
	if vm.definedGlobal(m, "later") {
		t.Error("a global of a later world")
	}
	c := m.declare("c", 1)
	c.Const = true
	if vm.definedGlobal(m, "c") {
		t.Error("a constant")
	}
	imp := m.declare("imp", 1)
	imp.Alias = b
	if vm.definedGlobal(m, "imp") {
		t.Error("an import")
	}
	im := m.declare("im", 1)
	im.implicit = true
	if vm.definedGlobal(m, "im") {
		t.Error("an implicit (export-only) binding")
	}
}
