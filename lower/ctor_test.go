package lower

import (
	"errors"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// The default constructor bodies lower to a CodeInfo whose slots are the
// self argument and one per field, and whose last statement returns a :new.
func TestDefaultCtorBodies(t *testing.T) {
	st := NewState()
	inner, err := st.DefaultInnerCtorBody([]int64{-1, 0, 1}, "none", 3)
	if err != nil {
		t.Fatal(err)
	}
	ci := inner.(*expr.CodeInfo)
	if len(ci.SlotNames) < 4 || ci.SlotNames[0] != expr.Symbol("#ctor-self#") || ci.SlotNames[1] != expr.Symbol("_1") {
		t.Errorf("inner slots: %v", ci.SlotNames)
	}
	if !hasHead(ci.Code, "new") {
		t.Errorf("inner body has no :new: %v", ci.Code)
	}
	outer, err := st.DefaultOuterCtorBody(expr.Symbol("P"), 2, 1, "none", 3)
	if err != nil {
		t.Fatal(err)
	}
	if !hasHead(outer.(*expr.CodeInfo).Code, "new") {
		t.Error("outer body has no :new")
	}
	if _, err := st.DefaultOuterCtorBody(nil, 0, 0, "none", 1); err != nil {
		t.Errorf("no fields: %v", err)
	}
	// a broken builder is an internal error, not a panic
	if _, err := st.lowerBuilt(func(*lowerer) any { panic("x") }, "none", 1); !errors.Is(err, ErrInternal) {
		t.Errorf("panicking builder: %v", err)
	}
	if _, err := st.lowerBuilt(func(*lowerer) any { return int64(1) }, "none", 1); !errors.Is(err, ErrInternal) {
		t.Errorf("a builder that is not a lambda: %v", err)
	}
}

func hasHead(code expr.Vector, head expr.Symbol) bool {
	for _, s := range code {
		if e, ok := s.(*expr.Expr); ok {
			if e.Head == head {
				return true
			}
			if hasHead(expr.Vector(e.Args), head) {
				return true
			}
		}
	}
	return false
}
