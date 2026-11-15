package lower

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// defined-julia-global (ast.c:172-182) decides what a soft scope does with an
// assignment in a loop at the top: to a global that exists it assigns it, else
// it makes a local. julia-syntax.scm:3233-3250 (resolve-scopes). Lowering
// alone, as the oracle lowers, has no global defined.
func TestDefinedGlobalDecidesSoftScope(t *testing.T) {
	s := expr.Symbol("s")
	loop := expr.New("while", true, expr.New("block", expr.New("=", s, int64(1))))
	soft := expr.New("block", expr.New("softscope", true), loop)
	slots := func(st *State, ex any) expr.Vector {
		t.Helper()
		out, err := st.Lower(ex)
		if err != nil {
			t.Fatal(err)
		}
		return out.(*expr.Expr).Args[0].(*expr.CodeInfo).SlotNames
	}
	has := func(names expr.Vector) bool {
		for _, n := range names {
			if n == any(s) {
				return true
			}
		}
		return false
	}
	// no hook: nothing is defined, the name is a new local
	if !has(slots(NewState(), soft)) {
		t.Error("without a hook, s is a local")
	}
	// the hook says s is a global the module defines: the soft scope assigns it
	st := NewState()
	var asked []expr.Symbol
	st.SetDefinedGlobal(func(n expr.Symbol) bool { asked = append(asked, n); return n == s })
	if has(slots(st, soft)) {
		t.Error("a soft scope over a defined global: s is a local")
	}
	if len(asked) == 0 || asked[0] != s {
		t.Errorf("the hook was asked about %v", asked)
	}
	// without the soft scope the loop's assignment is a local, defined or not
	if !has(slots(st, loop)) {
		t.Error("a hard scope: s is a local whatever the hook says")
	}
	// another name the hook denies stays local
	st = NewState()
	st.SetDefinedGlobal(func(expr.Symbol) bool { return false })
	if !has(slots(st, soft)) {
		t.Error("a name that is not defined is a local")
	}
}
