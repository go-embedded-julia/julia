package interp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// Staged functions (issue #126) without Base: Core has the stub type and the
// jl_expand shim; the stub's call, Base's expr.jl:1692-1708, is written here
// with Core's own functions (a body that is an Int64 stands for a generator
// that does not return a CodeInfo), and @generated (expr.jl:1178-1194) is two
// macros that make the `(generated)` expression and the `generated_only`
// meta. The same cases run on julia's Base in TestStagedOnBase.
const stagedPre = `
function (g::Core.GeneratedFunctionStub)(world::UInt, source::Core.Method, args...)
    body = g.gen(args...)
    Core.Intrinsics.ne_int(getfield(notci, :contents), 0) && return 5
    an = Core._expr(:argnames, g.argnames...)
    lam = Core._expr(:lambda, getfield(an, :args),
        Core._expr(:var"scope-block", Core._expr(:block, Core._expr(:return, body))))
    if Core.Intrinsics.ne_int(Core._svec_len(g.spnames), 0)
        lam = Core._expr(:var"with-static-parameters", lam, g.spnames...)
    end
    ci = ccall(:jl_expand, Any, (Any, Any), lam, getfield(source, :module))
    Core.setfield!(ci, :isva, getfield(source, :isva))
    ci
end
import Core.Intrinsics: add_int
macro generated() Core._expr(:generated) end
macro gonly() Core._expr(:meta, :generated_only) end
macro badstub() Core._expr(:meta, :generated, :undefined_stub_name) end
runs = Core.Box(0)
notci = Core.Box(0)
`

func stagedSession(t *testing.T) (*VM, *Session) {
	t.Helper()
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(stagedPre); err != nil {
		t.Fatal(err)
	}
	return vm, s
}

func TestStagedFunctions(t *testing.T) {
	_, s := stagedSession(t)
	for _, c := range []struct{ def, call, want string }{
		// the generator's expression is the body
		{`function f(x)
    if @generated
        :(add_int(x, 1))
    else
        @gonly
        return nothing
    end
end`, `f(1)`, "2"},
		{``, `f(40)`, "41"},
		// the generator sees the argument types, a vararg tail as a tuple
		{`function g(x, ys...)
    if @generated
        :(add_int(x, $(Core.nfields(ys))))
    else
        @gonly
        return nothing
    end
end`, `g(10, 1, 2, 3)`, "13"},
		{``, `g(10)`, "10"},
		// and the static parameters
		{`function h(x::T) where T
    if @generated
        :(T)
    else
        @gonly
        return nothing
    end
end`, `h(1) === Int64`, "true"},
		{``, `h(1.0) === Float64`, "true"},
		// a method with a source runs it, the generator never does
		{`function k(x)
    if @generated
        :(add_int(x, 100))
    else
        add_int(x, 1)
    end
end`, `k(1)`, "2"},
	} {
		if c.def != "" {
			if _, err := s.Eval(c.def); err != nil {
				t.Fatalf("%s: %v", c.def, err)
			}
		}
		v, err := s.Eval(c.call)
		if err != nil {
			t.Fatalf("%s: %v", c.call, err)
		}
		if got := showValue(v); got != c.want {
			t.Errorf("%s = %s, want %s", c.call, got, c.want)
		}
	}
}

// The generator runs once per signature, not once per call (jl_code_for_staged
// keeps the body in the method instance, method.c:695-810).
func TestStagedGeneratorRunsOncePerSignature(t *testing.T) {
	_, s := stagedSession(t)
	if _, err := s.Eval(`function c(x)
    if @generated
        Core.setfield!(runs, :contents, add_int(getfield(runs, :contents), 1))
        :(x)
    else
        @gonly
        return nothing
    end
end`); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{`c(1)`, `c(2)`, `c(3)`, `c(1.0)`, `c(2.0)`, `c(1)`} {
		if _, err := s.Eval(call); err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.Eval(`getfield(runs, :contents)`)
	if err != nil || showValue(v) != "2" {
		t.Errorf("the generator ran %s times (%v), want 2: Int64 and Float64", showValue(v), err)
	}
}

// The table holds stagedMax bodies: a signature past the bound is refused a
// slot, its generator runs again at each call, and the kept ones are not
// disturbed. The bound is driven through the variable, one entry past it.
func TestStagedBoundRefusesTheOnePastIt(t *testing.T) {
	vm, s := stagedSession(t)
	defer func(n int) { stagedMax = n }(stagedMax)
	stagedMax = 2
	if _, err := s.Eval(`function d(x)
    if @generated
        Core.setfield!(runs, :contents, add_int(getfield(runs, :contents), 1))
        :(x)
    else
        @gonly
        return nothing
    end
end`); err != nil {
		t.Fatal(err)
	}
	runs := func() string {
		v, err := s.Eval(`getfield(runs, :contents)`)
		if err != nil {
			t.Fatal(err)
		}
		return showValue(v)
	}
	// stagedMax signatures, each called twice: each runs its generator once
	for _, call := range []string{`d(1)`, `d(1.0)`, `d(1)`, `d(1.0)`} {
		if _, err := s.Eval(call); err != nil {
			t.Fatal(err)
		}
	}
	if got := runs(); got != "2" || len(vm.staged) != stagedMax {
		t.Fatalf("%s runs, %d kept: want 2 runs and %d kept", got, len(vm.staged), stagedMax)
	}
	// one past the bound: kept nowhere, so each call runs the generator again
	for _, call := range []string{`d(true)`, `d(true)`, `d(1)`} {
		if _, err := s.Eval(call); err != nil {
			t.Fatal(err)
		}
	}
	if got := runs(); got != "4" || len(vm.staged) != stagedMax {
		t.Errorf("%s runs, %d kept: want 4 runs (the refused signature twice, the kept one not again) and %d kept", got, len(vm.staged), stagedMax)
	}
}

func TestStagedGeneratorErrors(t *testing.T) {
	_, s := stagedSession(t)
	if _, err := s.Eval(`function bad(x)
    if @generated
        :(x)
    else
        @gonly
        return nothing
    end
end`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Eval(`Core.setfield!(notci, :contents, 1)`); err != nil {
		t.Fatal(err)
	}
	_, err := s.Eval(`bad(1)`)
	if _, e := s.Eval(`Core.setfield!(notci, :contents, 0)`); e != nil {
		t.Fatal(e)
	}
	if err == nil || !strings.Contains(err.Error(), "generated functions must return `CodeInfo`") {
		t.Errorf("a generator that returns an Int64: %v", err)
	}
	// a stub that does not evaluate fails the definition
	if _, err := s.Eval(`function badstub(x)
    @badstub
    x
end`); err == nil || !strings.Contains(err.Error(), "undefined_stub_name") {
		t.Errorf("a method whose stub is undefined: %v", err)
	}
	if _, err := s.Eval(`function boom(x)
    if @generated
        add_int(1, 2, 3)
    else
        @gonly
        return nothing
    end
end`); err != nil {
		t.Fatal(err)
	}
	// the generator's own error reaches the caller, not a complaint about
	// what it returned
	if _, err := s.Eval(`boom(1)`); err == nil || strings.Contains(err.Error(), "CodeInfo") {
		t.Errorf("a generator that throws: %v", err)
	}
	// a body that is not a pure expression is the front end's error
	if _, err := s.Eval(`function impure(x)
    if @generated
        :(x -> x)
    else
        @gonly
        return nothing
    end
end`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Eval(`impure(1)`); err == nil {
		t.Error("a generator whose body holds a closure did not make the call throw")
	}
}

func TestMethodGenerator(t *testing.T) {
	vm, _ := stagedSession(t)
	m := vm.Main
	meta := func(args ...any) any { return expr.New("meta", args...) }
	gen := any(int64(5)) // a stub is any value the method's module evaluates
	ci := func(stmts ...any) *expr.CodeInfo { return &expr.CodeInfo{Code: expr.Vector(stmts)} }
	for _, c := range []struct {
		name     string
		src      *expr.CodeInfo
		wantGen  bool
		wantOnly bool
		wantErr  string
	}{
		{"no metas", ci(int64(1), expr.New("call"), expr.New("meta")), false, false, ""},
		{"inline only without a generator", ci(meta(expr.Symbol("generated_only"))), false, false, ""},
		{"generator and source", ci(meta(expr.Symbol("generated"), gen)), true, false, ""},
		{"generator without source", ci(meta(expr.Symbol("generated"), gen), meta(expr.Symbol("generated_only"))), true, true, ""},
		{"two generators", ci(meta(expr.Symbol("generated"), gen), meta(expr.Symbol("generated"), gen)), false, false, "duplicate"},
		{"a stub that fails", ci(meta(expr.Symbol("generated"), expr.Symbol("undefined_stub_name"))), false, false, "undefined_stub_name"},
	} {
		g, only, err := vm.methodGenerator(m, c.src)
		if c.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("%s: err %v, want %q", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil || (g != nil) != c.wantGen || only != c.wantOnly {
			t.Errorf("%s: gen %v only %v err %v, want gen %v only %v", c.name, g, only, err, c.wantGen, c.wantOnly)
		}
	}
}

func TestStagedBodyNeedsAMethodTable(t *testing.T) {
	vm, _ := stagedSession(t)
	tt := &core.DataType{Parameters: []any{core.AnyType}} // no table holds an abstract callee
	_, err := vm.stagedBody(nil, &lambda{}, int64(1), tt, nil)
	if err == nil || !strings.Contains(err.Error(), "method table") {
		t.Errorf("stagedBody on a value that is no function: %v", err)
	}
}

func TestStagedStoreKeepsTheFirstBody(t *testing.T) {
	vm := New()
	key := stagedKey{l: &lambda{}}
	first, second := &lambda{}, &lambda{}
	if got := vm.stagedStore(key, first); got != first {
		t.Error("the first body was not kept")
	}
	if got := vm.stagedStore(key, second); got != first {
		t.Error("a second body replaced the first: two tasks would run different bodies")
	}
	defer func(n int) { stagedMax = n }(stagedMax)
	stagedMax = 1
	other, third := stagedKey{l: &lambda{}}, &lambda{}
	if got := vm.stagedStore(other, third); got != third || len(vm.staged) != 1 {
		t.Errorf("a full table kept a body: %d kept", len(vm.staged))
	}
}

// jl_expand (ast.c:1279-1303) of the shapes Base hands it, and of what is not
// a lambda; CodeInfo conversion errors.
func TestJlExpandAndCodeInfo(t *testing.T) {
	vm, s := stagedSession(t)
	if _, err := vm.jlExpand([]any{expr.Symbol("x"), int64(1)}); err == nil {
		t.Error("jl_expand with a module that is not one did not fail")
	}
	v, err := s.Eval(`ccall(:jl_expand, Any, (Any, Any), Core._expr(:call, :f), Main)`)
	if e, ok := v.(*expr.Expr); err != nil || !ok || e.Head != "thunk" {
		t.Errorf("a call lowers to a thunk, got %v %v", v, err)
	}
	v, err = s.Eval(`ccall(:jl_expand, Any, (Any, Any), Core._expr(:lambda, getfield(Core._expr(:argnames, :x), :args),
        Core._expr(:var"scope-block", Core._expr(:block, Core._expr(:return, :x)))), Main)`)
	js, ok := v.(*Struct)
	if err != nil || !ok || js.Type != vm.tCodeInfo {
		t.Fatalf("a lambda lowers to a CodeInfo, got %v %v", v, err)
	}
	if ci, err := vm.exprCodeInfo(js); err != nil || ci == nil {
		t.Errorf("exprCodeInfo of a CodeInfo: %v", err)
	}
	if _, err := vm.exprCodeInfo(&Struct{Type: js.Type, Fields: append([]any{int64(1)}, js.Fields[1:6]...)}); err == nil {
		t.Error("a CodeInfo whose code is no vector converted")
	}
	if _, err := vm.exprCodeInfo(&Struct{Type: js.Type, Fields: js.Fields[:10]}); err == nil {
		t.Error("a CodeInfo of too few fields converted")
	}
	if vm.exprDebugInfo(int64(3)) != any(int64(3)) {
		t.Error("a debug info that is no struct changed")
	}
	// a debug info with a line table and edges survives juliaCodeInfo and back
	di := &expr.DebugInfo{Def: expr.Symbol("f"), Linetable: &expr.DebugInfo{Codelocs: "ab"}, Edges: expr.Vector{&expr.DebugInfo{Codelocs: "cd"}}, Codelocs: "ef"}
	back, ok := vm.exprDebugInfo(vm.juliaDebugInfo(di)).(*expr.DebugInfo)
	if !ok || back.Codelocs != "ef" || len(back.Edges) != 1 || back.Edges[0].(*expr.DebugInfo).Codelocs != "cd" || back.Linetable.(*expr.DebugInfo).Codelocs != "ab" {
		t.Errorf("debug info round trip: %#v", back)
	}
	// lambdaArgnames leaves what is no lambda alone
	vm.lambdaArgnames(int64(1))
	vm.lambdaArgnames(expr.New("lambda"))
	// a macro that does not exist, and an expression lowering refuses
	if _, err := s.Eval(`ccall(:jl_expand, Any, (Any, Any), Core._expr(:macrocall, Symbol("@nope"), nothing), Main)`); err == nil {
		t.Error("jl_expand of an undefined macro did not fail")
	}
	deep := any(expr.Symbol("x"))
	for i := 0; i < 100000; i++ {
		deep = expr.New("block", deep)
	}
	if _, err := vm.jlExpand([]any{deep, vm.Main}); err == nil {
		t.Error("jl_expand of an expression nested 100000 deep did not fail")
	}
	// the CodeInfo a generator returns is resolved and prepared like a method's
	l := &lambda{mod: vm.Main}
	if _, err := vm.stagedLambda(l, js, nil); err != nil {
		t.Errorf("stagedLambda of a CodeInfo: %v", err)
	}
	bad := vm.juliaCodeInfo(&expr.CodeInfo{Code: expr.Vector{expr.New("global", int64(1))}})
	if _, err := vm.stagedLambda(l, bad, nil); err == nil {
		t.Error("a body whose definitions do not resolve was accepted")
	}
	unprepared := vm.juliaCodeInfo(&expr.CodeInfo{SSAValueTypes: "x"})
	if _, err := vm.stagedLambda(l, unprepared, nil); err == nil {
		t.Error("a body that does not prepare was accepted")
	}
	if _, err := vm.stagedLambda(l, &Struct{Type: js.Type, Fields: js.Fields[:10]}, nil); err == nil {
		t.Error("a malformed CodeInfo was accepted")
	}
}

// TestStagedOnBase: @generated functions run on julia's Base image
// (JLGO_BASE=1; JLGO_KERNEL_IMAGE names an image, else Base is built), through
// the stub of expr.jl:1692-1708 and jl_expand, and what Base builds on them
// gives julia's results: SHA (the FIPS 180-2 vectors among them) and the
// seeds of Random, which hash a seed with SHA2-256 (issue #126, #120). Every
// cell of testdata/staged.jl prints what julia 1.12.6 printed
// (testdata/staged.golden, regenerated with `pkgx julia@1.12.6`).
func TestStagedOnBase(t *testing.T) {
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
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 10 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	script, err := os.ReadFile("testdata/staged.jl")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/staged.golden")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(script), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := k.Eval(context.Background(), line)
		if r.Err != nil {
			t.Fatalf("%.60s: %v", line, r.Err)
		}
		out.WriteString(r.Stdout)
	}
	got, want := strings.Split(out.String(), "\n"), strings.Split(string(golden), "\n")
	if len(got) != len(want) {
		t.Errorf("%d lines, julia printed %d", len(got), len(want))
	}
	for i := range want {
		if i < len(got) && got[i] != want[i] {
			t.Errorf("line %d\n--- ours\n%s\n--- julia 1.12.6\n%s", i+1, got[i], want[i])
		}
	}
}
