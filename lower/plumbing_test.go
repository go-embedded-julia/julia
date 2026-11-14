package lower

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// The plumbing around the passes: how Lower reports what the port cannot
// do, and the invariant checks no julia input reaches. The lowering
// results themselves are all compared with julia's (lisp_test.go,
// hygiene_test.go, lower_test.go).

// panicOf runs f and returns what it panicked with.
func panicOf(f func()) (r any) {
	defer func() { r = recover() }()
	f()
	return nil
}

func TestLowerErrorKinds(t *testing.T) {
	st := NewState()
	// a lambda in a toplevel-only form reaches the converter unexpanded
	lam := expr.New("toplevel", expr.New("lambda", int64(1)))
	if _, err := st.Lower(lam); !errors.Is(err, ErrInternal) {
		t.Errorf("malformed lambda: %v", err)
	}
	if _, err := st.Lower(expr.New("toplevel", expr.New("ssavalue", expr.Symbol("x")))); !errors.Is(err, ErrInternal) {
		t.Errorf("malformed ssavalue: %v", err)
	}
	// julia prints a Float64 label with jl_static_show, which is not ported
	gotoFloat := expr.New("function", expr.New("call", expr.Symbol("f")), expr.New("block", expr.New("symbolicgoto", 1.5)))
	if _, err := st.Lower(gotoFloat); !errors.Is(err, ErrUnsupported) {
		t.Errorf("float label: %v", err)
	}
	debugPanics = true
	defer func() { debugPanics = false }()
	if _, ok := panicOf(func() { _, _ = st.Lower(lam) }).(internalError); !ok {
		t.Error("debugPanics: no internal error panic")
	}
}

// ExpandMacroscope reports as Lower does (recoverLowering): a hygienic
// scope resolved; a form the converter refuses internal; an expression too
// deep a limit; with debugPanics, the panic.
func TestExpandMacroscopeErrorKinds(t *testing.T) {
	st := NewState()
	// (hygienic-scope (= x 1) m): x is renamed, the scope is gone
	scoped := expr.New("hygienic-scope", expr.New("=", expr.Symbol("x"), int64(1)), expr.Module{Path: []expr.Symbol{"Main", "M"}})
	out, err := st.ExpandMacroscope(scoped)
	if e, ok := out.(*expr.Expr); err != nil || !ok || e.Head != "=" {
		t.Errorf("hygienic scope: %v %v", out, err)
	}
	// a lambda left by itself reaches the converter unexpanded, as in Lower
	lam := expr.New("lambda", int64(1))
	if _, err := st.ExpandMacroscope(lam); !errors.Is(err, ErrInternal) {
		t.Errorf("malformed lambda: %v", err)
	}
	var deep any = int64(1)
	for range MaxDepth + 2 {
		deep = expr.New("block", deep)
	}
	if _, err := st.ExpandMacroscope(deep); !errors.Is(err, ErrLimit) {
		t.Errorf("too deep: %v", err)
	}
	debugPanics = true
	defer func() { debugPanics = false }()
	if panicOf(func() { _, _ = st.ExpandMacroscope(lam) }) == nil {
		t.Error("debugPanics: no panic")
	}
}

func TestTypevarNamesUnsupported(t *testing.T) {
	// typevar-names skips the names analyze-typevar rejects, but not a
	// failure of the port to print the rejected one
	if _, ok := panicOf(func() { typevarNames(list(&jlValue{1.5})) }).(unsupported); !ok {
		t.Error("unsupported repr not propagated")
	}
}

func TestInvariantChecks(t *testing.T) {
	c := NewState().converter()
	if _, ok := panicOf(func() { c.fromSx(true) }).(internalError); !ok {
		t.Error("boolean in lowered output accepted")
	}
	if _, ok := panicOf(func() { (&compiler{}).emit(nil) }).(internalError); !ok {
		t.Error("linearize: missing value accepted")
	}
	if _, ok := panicOf(func() { compactIR(list(sBlock, nil), sym("none"), int64(0)) }).(internalError); !ok {
		t.Error("compact-ir: missing value accepted")
	}
	if sp(nil) != nil {
		t.Error("sp(nil) is not the empty splice")
	}
}
