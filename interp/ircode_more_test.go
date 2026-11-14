package interp

import (
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// compressedOf is jl_compress_ir of the lowered source of f's method for
// the arguments' types, with the method.
func compressedOf(t *testing.T, vm *VM, s *Session, f, args string) (string, *Struct, *Struct) {
	t.Helper()
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	sep := ""
	if args != "" {
		sep = ", "
	}
	tt, err := s.Eval("Tuple{typeof(" + f + ")" + sep + args + "}")
	if err != nil {
		t.Fatal(err)
	}
	match, err := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
	if err != nil || match == nil {
		t.Fatalf("%s: no method: %v", f, err)
	}
	m := match.(*Struct).Fields[2].(*Struct)
	src := m.Fields[methodSource].(*Struct)
	v, err := vm.shims["jl_compress_ir"](vm, []any{m, src})
	if err != nil {
		t.Fatalf("%s: %v", f, err)
	}
	return v.(string), m, src
}

// jl_compress_ir against julia 1.12.6's bytes and roots for more lowered
// code (testdata/ircode2.jl), and the round trip back to the statements.
func TestCompressIRMoreAgainstJulia(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	golden, err := os.ReadFile("testdata/ircode2.golden")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(golden), "\n"), "\n") {
		f := strings.SplitN(line, " ", 3)
		want[f[0]] = [2]string{f[1], f[2]}
	}
	var long300, long600 []string
	for i := 1; i <= 300; i++ {
		long300 = append(long300, fmt.Sprint(i))
	}
	for i := 0; i < 600; i++ {
		long600 = append(long600, "hg()")
	}
	if _, err := s.Eval(`hg() = 1
h1(x) = tuple(x, 0x0001, 0x00000002, 0x0000000000000003, 1.5f0, -300, 32768, 2147483648, -2147483649, 'é', 0.1)
h2() = tuple(1, 2)
h6(x) = (Int, Float64, Vector, Tuple, Array, nothing, x)
h7(x) = (:a, :b, "s", "s", :a)
h8(x) = (x; tuple(x, 1.5, 1.5))
h9(x, y) = x === y ? (1, 2.5) : (y, :sym)
h10() = (Int8(1), 2)
h3() = tuple(` + strings.Join(long300, ", ") + `)
h4() = tuple(` + strings.Join(long600, ", ") + `)
h5(x) = (x = hg(); x = hg(); x = hg(); x)`); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ f, args string }{
		{"h1", "Int"}, {"h2", ""}, {"h3", ""}, {"h4", ""}, {"h5", "Int"}, {"h6", "Int"}, {"h7", "Int"},
		{"h8", "Int"}, {"h9", "Int, Int"}, {"h10", ""},
	} {
		got, m, src := compressedOf(t, vm, s, c.f, c.args)
		w := want[c.f]
		if h := hex.EncodeToString([]byte(got)); h != w[0] {
			t.Errorf("%s:\n go    %.300s\n julia %.300s", c.f, h, w[0])
		}
		roots := "Any[]"
		if m.Fields[methodRoots] != undef {
			roots = showValue(m.Fields[methodRoots])
		}
		if roots != w[1] {
			t.Errorf("%s roots: go %s, julia %s", c.f, roots, w[1])
		}
		ci, err := vm.shims["jl_uncompress_ir"](vm, []any{m, nil, got})
		if err != nil {
			t.Errorf("%s: jl_uncompress_ir: %v", c.f, err)
			continue
		}
		if g, e := showValue(vm.ciField(ci.(*Struct), "code")), showValue(vm.ciField(src, "code")); g != e {
			t.Errorf("%s round trip:\n got  %.300s\n want %.300s", c.f, g, e)
		}
	}
}
