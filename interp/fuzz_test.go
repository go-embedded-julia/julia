package interp

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// An untrusted cell cannot run forever or exhaust the Go stack: a loop stops
// at MaxSteps, and try blocks re-entered without leaving them count as
// depth.
func TestResourceBounds(t *testing.T) {
	s := New().NewSession()
	s.VM.MaxSteps = 1000
	if _, err := s.Eval("while true\nend"); !errors.Is(err, ErrStepLimit) {
		t.Errorf("an endless loop: %v", err)
	}
	s = New().NewSession()
	v, err := runIR(s, codeInfo([]any{expr.EnterNode{Catch: 0}, expr.GotoNode(1)}), false)
	check(t, "re-entered try", v, err, "!StackOverflowError()")
}

// A panic inside the interpreter comes out of Session.Eval as ErrInternal.
func TestEvalRecoversPanics(t *testing.T) {
	s := New().NewSession()
	b := s.VM.Core.declare("boom", 0)
	b.Value, b.Exports = &Builtin{Name: "boom", Type: s.VM.builtinType, Fn: func(*VM, []any) (any, error) {
		panic("broken invariant")
	}}, true
	if _, err := s.Eval("boom()"); !errors.Is(err, ErrInternal) {
		t.Errorf("a panicking builtin: %v", err)
	}
	if _, err := s.Eval("getfield(:(a + 1), :head)"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("getfield of an Expr: %v", err)
	}
}

// Arbitrary source never makes the interpreter panic: every outcome is a
// value, a julia exception, or a Go error.
func FuzzEval(f *testing.F) {
	for _, src := range loadSnippets(f) {
		f.Add(src)
	}
	f.Add("while true\nend")
	f.Add("f(x) = f(x)\nf(1)")
	f.Add("macro r()\n    :(@r())\nend\n@r()")
	f.Add("macro m(x)\n    :(($x, $x))\nend\n@m(@m(1))")
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<12 {
			return
		}
		s := New().NewSession()
		s.VM.MaxSteps = 1 << 16
		s.VM.MaxDepth = 200
		v, err := s.Eval(src)
		if err == nil {
			_ = Repr(v)
		}
	})
}
