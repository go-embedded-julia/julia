package interp

import (
	"fmt"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// utf8proc's entry points Base.Unicode calls (strings/unicode.jl), from
// unitab's tables, which are the pinned julia's own answers.

// utf8procCategories are utf8proc_category_string's abbreviations, in
// utf8proc_category_t's order (UTF8PROC_CATEGORY_CN = 0 … CO = 29).
var utf8procCategories = [...]string{"Cn", "Lu", "Ll", "Lt", "Lm", "Lo", "Mn", "Mc", "Me", "Nd", "Nl", "No",
	"Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Zs", "Zl", "Zp", "Cc", "Cf", "Cs", "Co"}

func (vm *VM) bootUtf8proc() {
	char := func(name string, a []any) (uint32, error) {
		c, ok := a[0].(uint32)
		if !ok {
			return 0, vm.typeError("ccall", "", core.UInt32Type, a[0])
		}
		return c, nil
	}
	vm.shims["utf8proc_category"] = func(vm *VM, a []any) (any, error) {
		c, err := char("utf8proc_category", a)
		if err != nil {
			return nil, err
		}
		return int32(unitab.Category(c)), nil
	}
	vm.shims["utf8proc_category_string"] = func(vm *VM, a []any) (any, error) {
		c, err := char("utf8proc_category_string", a)
		if err != nil {
			return nil, err
		}
		return vm.stringPtrOf(utf8procCategories[unitab.Category(c)]), nil
	}
	vm.shims["utf8proc_charwidth"] = func(vm *VM, a []any) (any, error) {
		c, err := char("utf8proc_charwidth", a)
		if err != nil {
			return nil, err
		}
		return int32(unitab.Width(c)), nil
	}
	// utf8proc_grapheme_break(c1, c2) is the stateful one from state 0
	vm.shims["utf8proc_grapheme_break"] = func(vm *VM, a []any) (any, error) {
		c1, ok1 := a[0].(uint32)
		c2, ok2 := a[1].(uint32)
		if !ok1 || !ok2 {
			return nil, fmt.Errorf("%w: utf8proc_grapheme_break of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		var st int32
		return unitab.GraphemeBreak(&st, c1, c2), nil
	}
	vm.shims["utf8proc_grapheme_break_stateful"] = func(vm *VM, a []any) (any, error) {
		c1, ok1 := a[0].(uint32)
		c2, ok2 := a[1].(uint32)
		p, ok3 := a[2].(*Pointer)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("%w: utf8proc_grapheme_break_stateful of %s", ErrUnsupported, showValue(vm.newTuple(a)))
		}
		sv, err := vm.ptrLoad("utf8proc_grapheme_break_stateful", p, 1)
		if err != nil {
			return nil, err
		}
		st, _ := sv.(int32)
		brk := unitab.GraphemeBreak(&st, c1, c2)
		if err := vm.ptrStore("utf8proc_grapheme_break_stateful", p, st, 1); err != nil {
			return nil, err
		}
		return brk, nil
	}
}
