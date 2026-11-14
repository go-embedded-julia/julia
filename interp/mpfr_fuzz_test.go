package interp

import (
	"testing"
)

// FuzzMPFRParse feeds mpfr_set_str's parser, and the evaluation of what it
// reads, arbitrary strings and bases: neither may panic, and what is read is
// a number, NaN or infinity. The corpus is in testdata/fuzz/FuzzMPFRParse.
func FuzzMPFRParse(f *testing.F) {
	for _, s := range []string{"1.5e3", "0x1.8p-2", "nan(a)", "-Inf", "1e99999999999999999999", ".", "0b", "@", "1@-5", "zZ9"} {
		f.Add(s, uint8(0))
		f.Add(s, uint8(36))
	}
	vm := New()
	vm.MaxMemoryElems = 1 << 10
	f.Fuzz(func(t *testing.T, s string, b uint8) {
		base := int(b) % 63 // 0, and 2 to 62
		if base == 1 {
			base = 0
		}
		p, rest, ok := mpfrParse(s, base)
		if len(rest) > len(s) {
			t.Fatalf("rest %q longer than %q", rest, s)
		}
		if !ok {
			return
		}
		x, _, err := vm.mpfrOfParsed(p, 53, int(b)%5)
		if err == nil && x.kind == kReg && x.mant.BitLen() > 53 {
			t.Fatalf("%q read in %d bits", s, x.mant.BitLen())
		}
	})
}
