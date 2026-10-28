package syntax

import (
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// TestExportedWrappers: the exported helpers the lowering uses are the
// functions the parser's goldens check.
func TestExportedWrappers(t *testing.T) {
	for _, s := range []string{"", "a\"$b\n", "\x80"} {
		if ReprString(s) != reprString(s) {
			t.Errorf("ReprString(%q)", s)
		}
	}
	for _, c := range []expr.Char{expr.Char('c'), expr.Char(0)} {
		if ReprChar(c) != reprChar(c) {
			t.Errorf("ReprChar(%v)", c)
		}
	}
	for _, r := range []rune{'a', '′', '₁', 0x10ffff + 1} {
		if IsOpSuffixChar(r) != isOpSuffix(r) {
			t.Errorf("IsOpSuffixChar(%q)", r)
		}
	}
}
