package interp

import (
	"encoding/hex"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// jl_compress_ir of lowered code against julia 1.12.6's bytes and the roots
// it gives the method (testdata/ircode.jl), then jl_uncompress_ir back to
// the same statements.
func TestCompressIRAgainstJulia(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	s := vm.NewSession()
	if _, err := s.Eval(`f1(x) = x
f2(x, y) = tuple(x, y, 1.5, :sym, "str", 'c', 0x07, 300, 70000, 5000000000, nothing, true, -2)
f3(x) = x === nothing ? 1 : 2
f4(x) = try; x; catch; 0; end
f5(x::T) where {T} = T
f6() = :(a + b)
f7(x) = (y = x; while y === 1; y = 2; end; y)
f8(xs...) = xs
f9(x) = typeof(x)`); err != nil {
		t.Fatal(err)
	}
	null := &Pointer{Type: vm.ptrType(core.UInt64Type)}
	for _, c := range []struct{ f, args, hex, roots string }{
		{"f1", "Int", "20000000ffff0200000000080200000000000000050200380700bd4201", "Any[]"},
		{"f2", "Int, Int", "20000000ffff0300000000080804000000000000001f491f68090e440701050200050300361100000000000000f83f35110111023611030000006327072d2c0114701101002600f2052a010000000700412dfeff380700bf4200", `Any[Float64, :sym, "str", Char]`},
		{"f3", "Int", "20000000ffff02000000000806000000000000001f501f683007010502000700370700c138bc38bdc14201", "Any[]"},
		{"f4", "Int", "20000000ffff02000000000806000000000000003dc00805020009011e64070138070109011eaf070338bbc14201", "Any[]"},
		{"f5", "Int", "20000000ffff0200000000000200000000000000090172bc380700bd4201", "Any[]"},
		// julia shows the Expr root :(a + b), with Base's show
		{"f6", "", "20000000ffff0100000000020000000000000009011100351101380700bd4200", "Any[:copyast, Expr(:call, :+, :a, :b)]"},
		{"f7", "Int", "20000000ffff030000000008080a0000000000000005020009024c05030007001f500503003007010700bc370700c409024c050300bd34be050300380700c54201", "Any[]"},
		{"f8", "Int", "30000000ffff0200000000080200000000000000050200380700bd4201", "Any[]"},
		{"f9", "Int", "20000000ffff02000000000803000000000000001f912f0700050200380700be4201", "Any[]"},
	} {
		sep := ""
		if c.args != "" {
			sep = ", "
		}
		tt, err := s.Eval("Tuple{typeof(" + c.f + ")" + sep + c.args + "}")
		if err != nil {
			t.Fatal(err)
		}
		match, err := vm.shims["jl_gf_invoke_lookup_worlds"](vm, []any{tt, nil, vm.World(), null, null})
		if err != nil || match == nil {
			t.Fatalf("%s: no method: %v", c.f, err)
		}
		m := match.(*Struct).Fields[2].(*Struct)
		src := m.Fields[methodSource]
		v, err := vm.shims["jl_compress_ir"](vm, []any{m, src})
		if err != nil {
			t.Errorf("%s: %v", c.f, err)
			continue
		}
		if got := hex.EncodeToString([]byte(v.(string))); got != c.hex {
			t.Errorf("%s:\n go    %s\n julia %s", c.f, got, c.hex)
		}
		roots := "Any[]"
		if m.Fields[methodRoots] != undef {
			roots = showValue(m.Fields[methodRoots])
		}
		if roots != c.roots {
			t.Errorf("%s roots: go %s, julia %s", c.f, roots, c.roots)
		}
		// back to the same statements
		ci, err := vm.shims["jl_uncompress_ir"](vm, []any{m, nil, v})
		if err != nil {
			t.Errorf("%s: jl_uncompress_ir: %v", c.f, err)
			continue
		}
		if got, want := showValue(vm.ciField(ci.(*Struct), "code")), showValue(vm.ciField(src.(*Struct), "code")); got != want {
			t.Errorf("%s round trip:\n got  %s\n want %s", c.f, got, want)
		}
		for _, acc := range []expr.Symbol{"jl_ir_flag_inlining", "jl_ir_flag_has_fcall", "jl_ir_inlining_cost", "jl_ir_nslots"} {
			a, err1 := vm.shims[acc](vm, []any{v})
			b, err2 := vm.shims[acc](vm, []any{src})
			if err1 != nil || err2 != nil || a != b {
				t.Errorf("%s %s: compressed %v %v, CodeInfo %v %v", c.f, acc, a, err1, b, err2)
			}
		}
	}
}
