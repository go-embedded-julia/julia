package interp

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// baseKernelForBench is a Kernel on the Base image (JLGO_BASE=1;
// JLGO_KERNEL_IMAGE names an image, else Base is built), with the string
// helpers of the benchmarks defined.
func baseKernelForBench(b testing.TB) *Kernel {
	b.Helper()
	if os.Getenv("JLGO_BASE") != "1" {
		b.Skip("set JLGO_BASE=1 (and optionally JLGO_KERNEL_IMAGE, JLGO_IMAGE_VERSION)")
	}
	tag := os.Getenv("JLGO_IMAGE_VERSION")
	if tag == "" {
		tag = "v0.0.1"
	}
	// JLGO_NATIVES=off runs the julia bodies (natives.go), to compare
	nativesDisabled.Store(os.Getenv("JLGO_NATIVES") == "off")
	var img []byte
	if file := os.Getenv("JLGO_KERNEL_IMAGE"); file != "" {
		fakeReleaseVersion(b, tag)
		var err error
		if img, err = os.ReadFile(file); err != nil {
			b.Fatal(err)
		}
	} else {
		img = buildBaseImage(b, tag)
	}
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 40 * time.Minute, MaxAlloc: -1}})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { k.Close() })
	if r := k.Eval(context.Background(), `const N = Base.Unicode; unit = "Héllo wörld ǆ ß Straße ﬁ é 각 ΑΣ "; nothing`); r.Err != nil {
		b.Fatal(r.Err)
	}
	return k
}

// BenchmarkKernelStrings times the calls of #71 in a cell: uppercase and
// lowercase of 2 000 characters, normalize of 20 000 and 660 000.
func BenchmarkKernelStrings(b *testing.B) {
	k := baseKernelForBench(b)
	ctx := context.Background()
	if r := k.Eval(ctx, `mid = repeat(unit, 60); big = repeat(unit, 600); huge = repeat(unit, 20000); nothing`); r.Err != nil {
		b.Fatal(r.Err)
	}
	for _, c := range []struct{ name, src string }{
		{"uppercase", `uppercase(mid); nothing`},
		{"lowercase", `lowercase(mid); nothing`},
		{"normalizeNFC", `N.normalize(big, :NFC); nothing`},
		{"normalizeNFD", `N.normalize(big, :NFD); nothing`},
		// plain julia loops: what every interpreted loop pays per iteration
		{"loopInt100k", `let s = 0; for i in 1:100000; s += i; end; s; end; nothing`},
		{"sumChars2000", `sum(Int(c) for c in mid); nothing`},
		{"normalizeNFC660k", `N.normalize(huge, :NFC); nothing`},
		{"normalizeNFD660k", `N.normalize(huge, :NFD); nothing`},
	} {
		b.Run(c.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if r := k.Eval(ctx, c.src); r.Err != nil {
					b.Fatal(r.Err)
				}
			}
		})
	}
}
