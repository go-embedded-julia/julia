package interp

import (
	"os"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// TestBootProgress evaluates boot.jl form by form in Core and reports how
// far it gets: the first form that fails, with its error.
func TestBootProgress(t *testing.T) {
	src, err := os.ReadFile("boot/boot.jl")
	if err != nil {
		t.Fatal(err)
	}
	vm := New()
	s := vm.CoreSession()
	top := syntax.ParseAll(src, "boot.jl").(*expr.Expr)
	n, line := 0, 0
	for _, form := range top.Args {
		if l, ok := form.(expr.LineNumberNode); ok {
			line = l.Line
			continue
		}
		if _, err := s.evalForm(form); err != nil {
			t.Logf("boot.jl:%d: form %d fails: %v", line, n+1, err)
			return
		}
		n++
	}
	t.Logf("all %d forms of boot.jl evaluated", n)
}
