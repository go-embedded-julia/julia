package interp

import (
	"io/fs"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// TestBoot evaluates boot.jl form by form in Core, as julia does after
// jl_init_types, and fails on the first form that raises.
func TestBoot(t *testing.T) {
	vm := New()
	src, err := fs.ReadFile(vm.Sources, "boot.jl")
	if err != nil {
		t.Fatal(err)
	}
	s := vm.CoreSession()
	top := syntax.ParseAll(src, "boot.jl").(*expr.Expr)
	n, line := 0, 0
	for _, form := range top.Args {
		if l, ok := form.(expr.LineNumberNode); ok {
			line = l.Line
			continue
		}
		if _, err := s.evalForm(form); err != nil {
			t.Fatalf("boot.jl:%d: form %d fails: %v", line, n+1, err)
		}
		n++
	}
	t.Logf("all %d forms of boot.jl evaluated", n)
}
