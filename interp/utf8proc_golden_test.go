package interp

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// The utf8proc shims against julia 1.12.6's answers (testdata/
// utf8proc.golden): category, its abbreviation and the width of code
// points of every category; grapheme breaks, stateless and stateful.
func TestUtf8procAgainstJulia(t *testing.T) {
	b, err := os.ReadFile("testdata/utf8proc.golden")
	if err != nil {
		t.Fatal(err)
	}
	vm := New()
	call := func(name string, a ...any) any {
		v, err := vm.shims[expr.Symbol(name)](vm, a)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return v
	}
	u32 := func(s string) uint32 {
		n, _ := strconv.ParseUint(s, 10, 32)
		return uint32(n)
	}
	state := &Memory{Type: memType(core.Int32Type), Elems: []any{int32(0)}}
	sp := &Pointer{Type: vm.ptrType(core.Int32Type), Target: state}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 45 {
		t.Fatalf("%d golden lines", len(lines))
	}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		var got string
		switch {
		case f[0] == "s":
			brk := call("utf8proc_grapheme_break_stateful", u32(f[1]), u32(f[2]), sp)
			got = fmt.Sprintf("s\t%s\t%s\t%v\t%d", f[1], f[2], brk, state.Elems[0])
		case len(f) == 3:
			got = fmt.Sprintf("%s\t%s\t%v", f[0], f[1], call("utf8proc_grapheme_break", u32(f[0]), u32(f[1])))
		default:
			c := u32(f[0])
			p := call("utf8proc_category_string", c).(*Pointer)
			got = fmt.Sprintf("%d\t%d\t%s\t%d", c, call("utf8proc_category", c), p.Target, call("utf8proc_charwidth", c))
		}
		if got != l {
			t.Errorf("got   %q\njulia %q", got, l)
		}
	}
	// the refusals
	for _, c := range []struct {
		name string
		args []any
	}{
		{"utf8proc_category", []any{int64(1)}},
		{"utf8proc_category_string", []any{int64(1)}},
		{"utf8proc_charwidth", []any{int64(1)}},
		{"utf8proc_grapheme_break", []any{uint32(1), int64(1)}},
		{"utf8proc_grapheme_break_stateful", []any{uint32(1), uint32(1), int64(1)}},
		{"utf8proc_grapheme_break_stateful", []any{uint32(1), uint32(1), &Pointer{Type: sp.Type}}},
		{"utf8proc_grapheme_break_stateful", []any{uint32(1), uint32(1), &Pointer{Type: sp.Type, Target: "ab"}}},
	} {
		if _, err := vm.shims[expr.Symbol(c.name)](vm, c.args); err == nil {
			t.Errorf("%s of %v: no error", c.name, c.args)
		}
	}
}
