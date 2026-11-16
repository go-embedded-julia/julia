package interp

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"
)

// What a typed store of bytes costs and saves, on the Base image (#81): the
// cells #71 names (uppercase and normalize of a long string) and a read loop
// over a string's bytes, where boxing a uint8 costs nothing in Go. The image is
// built by the same build of the tests (JLGO_IMAGE_VERSION=v0.0.1).
//
//	JLGO_BASE_IMAGE=... go test ./interp -run '^$' -bench TypedUInt8 -benchmem
func BenchmarkTypedUInt8(b *testing.B) {
	path := os.Getenv("JLGO_BASE_IMAGE")
	if path == "" {
		b.Skip("set JLGO_BASE_IMAGE to a Base image built by this build of the tests")
	}
	img, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	fakeReleaseVersion(b, baseImageTag)
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 30 * time.Minute, MaxAlloc: -1, MaxOutput: 1 << 20}})
	if err != nil {
		b.Fatal(err)
	}
	defer k.Close()
	// JLGO_BENCH_CHARS is the length of the strings (default 100 000; #71 names
	// 660 000 for normalize)
	n := os.Getenv("JLGO_BENCH_CHARS")
	if n == "" {
		n = "100000"
	}
	if r := k.Eval(context.Background(), `s1 = repeat("a", `+n+`); s2 = repeat("aé", `+n+`÷2); nothing`); r.Err != nil {
		b.Fatal(r.Err)
	}
	for _, c := range []struct{ name, src string }{
		{"uppercase", `uppercase(s1); nothing`},
		{"normalize NFD", `Base.Unicode.normalize(s2, :NFD); nothing`},
		{"read loop over bytes", `t = 0; for b in codeunits(s1); t += b; end; t`},
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

// BenchmarkLoadKernel is the cost of a load of the Base image: the time and the
// allocations of reading it and making a kernel, and its size in bytes.
func BenchmarkLoadKernel(b *testing.B) {
	path := os.Getenv("JLGO_BASE_IMAGE")
	if path == "" {
		b.Skip("set JLGO_BASE_IMAGE to a Base image built by this build of the tests")
	}
	img, err := os.ReadFile(path)
	if err != nil {
		b.Fatal(err)
	}
	fakeReleaseVersion(b, baseImageTag)
	b.ReportMetric(float64(len(img)), "image-bytes")
	b.ReportAllocs()
	for b.Loop() {
		k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{})
		if err != nil {
			b.Fatal(err)
		}
		k.Close()
	}
}
