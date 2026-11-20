package interp

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// What the representation of a Memory costs the loops julia code runs through
// getindex and setindex!, on the Base image: the benchmark #81 asks for before
// any element type gets a typed store. JLGO_BASE_IMAGE names an image built by
// TestBuildImage (JLGO_IMAGE_VERSION=v0.17.0) by the same build of the tests.
// go test ./interp -run '^$' -bench MemoryLoops -benchmem
func BenchmarkMemoryLoops(b *testing.B) {
	path := os.Getenv("JLGO_BASE_IMAGE")
	if path == "" {
		b.Skip("set JLGO_BASE_IMAGE to a Base image built by this build of the tests")
	}
	img, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	fakeReleaseVersion(b, baseImageTag)
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 10 * time.Minute, MaxAlloc: -1, MaxOutput: 1 << 20}})
	if err != nil {
		b.Fatal(err)
	}
	defer k.Close()
	if r := k.Eval(context.Background(), "v = zeros(Float64, 10_000); w = zeros(UInt8, 10_000); s = 0.0; nothing"); r.Err != nil {
		b.Fatal(r.Err)
	}
	n := "10_000"
	if os.Getenv("JLGO_BENCH_BIG") != "" {
		n = "1_000_000" // 10^6: the sizes #81 asks about
	}
	if r := k.Eval(context.Background(), "v = fill(1.5, "+n+"); w = zeros(UInt8, "+n+"); str = repeat(\"a\", "+n+"); nothing"); r.Err != nil {
		b.Fatal(r.Err)
	}
	for _, c := range []struct{ name, src string }{
		{"field write loop (mutable struct)", "mutable struct Cnt; x::Int; end; c = Cnt(0); for i in 1:" + n + "; c.x += 1; end; c.x"},
		{"string bytes loop UInt8", "t = 0; for b in codeunits(str); t += b; end; t"},
		{"sum codeunits UInt8", "sum(codeunits(str))"},
		{"read loop Float64", "s = 0.0; for i in eachindex(v); s += v[i]; end; s"},
		{"write loop Float64", "for i in eachindex(v); v[i] = v[i] + 1.0; end; nothing"},
		{"sum Float64", "sum(v)"},
		{"broadcast Float64", "v .+ 1.0; nothing"},
		{"fill and copy UInt8", "fill!(w, 0x01); copy(w); nothing"},
		{"read loop UInt8", "t = 0; for i in eachindex(w); t += w[i]; end; t"},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				if r := k.Eval(context.Background(), c.src); r.Err != nil {
					b.Fatal(r.Err)
				}
			}
		})
	}
}
