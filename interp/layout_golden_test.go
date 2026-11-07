package interp

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

// Every type of testdata/layout_types.txt, defined by testdata/layout_defs.jl,
// laid out as julia 1.12.6 does (testdata/layout.jl, whose golden this
// replays): jl_stored_inline, sizeof, fieldoffset of every field, the layout
// record, the DataType and TypeName flags. A line is the answers, tab
// separated, or ERR for a type with no layout.
func TestLayoutGolden(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	defs, err := os.ReadFile("testdata/layout_defs.jl")
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range strings.Split(strings.TrimSpace(string(defs)), "\n\n") {
		evalDef(t, s, def)
	}
	dtl, err := s.Eval("DTL")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/layout.golden")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(golden)), "\n")
	if len(lines) < 180 {
		t.Fatalf("%d golden lines", len(lines))
	}
	for _, want := range lines {
		src, _, _ := strings.Cut(want, "\t")
		v, err := s.Eval(src)
		if err != nil {
			t.Errorf("%s: %v", src, err)
			continue
		}
		got, err := layoutLine(vm, src, v, dtl.(*core.DataType))
		if err != nil {
			t.Errorf("%s: %v", src, err)
		} else if got != want {
			t.Errorf("%s:\n got %q\nwant %q", src, got, want)
		}
	}
}

// layoutLine is what testdata/layout.jl prints of v.
func layoutLine(vm *VM, src string, v any, dtl *core.DataType) (string, error) {
	inl, err := storedInline(v)
	if err != nil {
		return "", err
	}
	line := fmt.Sprintf("%s\t%d\t", src, inl)
	d, ok := v.(*core.DataType)
	if !ok || !tryLayout(d) {
		return line + "ERR", nil
	}
	size, err := biSizeof(vm, []any{d})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			return "", err
		}
		return line + "ERR", nil // julia's sizeof throws
	}
	ft, _ := d.FieldTypes()
	offs := make([]string, len(ft))
	for i := range ft {
		o, err := vm.fieldOffset([]any{d, int32(i + 1)})
		if err != nil {
			return "", err
		}
		offs[i] = fmt.Sprint(o)
	}
	lv, err := vm.layoutStruct(d, dtl)
	if err != nil {
		return "", err
	}
	f := lv.(*Struct).Fields
	// jltypes.c makes these before it marks Symbol mutation- and identity-free
	// (jltypes.c:3883), so they never are: not modelled, the layout is
	flags := dataTypeFlags(d) & 0x3ff
	if d == core.UnwrapUnionAll(vm.coreType("LineNumberNode")) || d == core.UnwrapUnionAll(vm.coreType("LegacyLineInfoNode")) {
		flags &= 0xff
	}
	return line + fmt.Sprintf("%d\t%s\t%d,%d,%d,%d,%d,%d\t%d\t%d", size, strings.Join(offs, ","),
		f[0], f[1], f[2], f[3], f[4], f[5], flags, typeNameFlags(d.Name)), nil
}

// evalDef evaluates a definition, Base's @atomic (no macro expansion in this
// session) rewritten to the :atomic field attribute it expands to.
func evalDef(t *testing.T, s *Session, def string) {
	t.Helper()
	top := syntax.ParseAll([]byte(def), "none").(*expr.Expr)
	var rewrite func(v any) any
	rewrite = func(v any) any {
		e, ok := v.(*expr.Expr)
		if !ok {
			return v
		}
		if e.Head == "macrocall" && e.Args[0] == any(expr.Symbol("@atomic")) {
			return expr.New("atomic", rewrite(e.Args[2]))
		}
		args := make([]any, len(e.Args))
		for i, a := range e.Args {
			args[i] = rewrite(a)
		}
		return expr.New(e.Head, args...)
	}
	for _, form := range top.Args {
		if _, ok := form.(expr.LineNumberNode); ok {
			continue
		}
		if _, err := s.evalForm(rewrite(form)); err != nil {
			t.Fatalf("%s: %v", def, err)
		}
	}
}
