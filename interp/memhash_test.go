package interp

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// memhash_seed against julia 1.12.6's, over every tail length and three
// seeds (testdata/memhash.golden: the bytes, the seed, the hash).
func TestMemhashAgainstJulia(t *testing.T) {
	b, err := os.ReadFile("testdata/memhash.golden")
	if err != nil {
		t.Fatal(err)
	}
	vm := New()
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) != 123 {
		t.Fatalf("%d golden lines", len(lines))
	}
	for _, l := range lines {
		f := strings.Split(l, "\t")
		seed, _ := strconv.ParseUint(f[1], 10, 32)
		want, _ := strconv.ParseUint(f[2], 10, 64)
		if got := memhashSeed([]byte(f[0]), uint32(seed)); got != want {
			t.Errorf("memhash_seed(%q, %d) = %d, julia %d", f[0], seed, got, want)
		}
		v, err := vm.memhashShim([]any{vm.stringPtrOf(f[0]), uint64(len(f[0])), uint32(seed)})
		if err != nil || v != any(want) {
			t.Errorf("the shim of %q: %v, %v", f[0], v, err)
		}
	}
	_, err = vm.memhashShim([]any{vm.stringPtrOf("ab"), uint64(3), uint32(0)})
	check(t, "memhash_seed past the string", nil, err, "!not ported yet: memhash_seed")
	_, err = vm.memhashShim([]any{&Pointer{Type: vm.ptrType(core.UInt8Type)}, uint64(0), int64(0)})
	check(t, "memhash_seed of a non-UInt32 seed", nil, err, "!not ported yet: memhash_seed")
}
