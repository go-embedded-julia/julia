package interp

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRandSIMDOnBase: seeded rand of an array of 64 bytes or more (640
// elements for Bool) runs julia's XoshiroSimd, whose eight forked lanes make a
// stream the scalar generator does not (issue #120). On julia's Base image
// (JLGO_BASE=1; JLGO_KERNEL_IMAGE names an image, else Base is built) every
// case of testdata/randsimd.jl prints what julia 1.12.6 printed
// (testdata/randsimd.golden, regenerated with `pkgx julia@1.12.6`): a hash of
// the element bits, the first and the last element, for lengths just below, at
// and above the thresholds, for Float64, Float32, Float16, UInt64, UInt8, Int32
// and Bool, through Xoshiro(seed) and the task-local generator.
func TestRandSIMDOnBase(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 (and optionally JLGO_KERNEL_IMAGE, JLGO_IMAGE_VERSION)")
	}
	tag := os.Getenv("JLGO_IMAGE_VERSION")
	if tag == "" {
		tag = "v0.0.1"
	}
	var img []byte
	if file := os.Getenv("JLGO_KERNEL_IMAGE"); file != "" {
		fakeReleaseVersion(t, tag)
		var err error
		if img, err = os.ReadFile(file); err != nil {
			t.Fatal(err)
		}
	} else {
		img = buildBaseImage(t, tag)
	}
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	script, err := os.ReadFile("testdata/randsimd.jl")
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("testdata/randsimd.golden")
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, line := range strings.Split(string(script), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		r := k.Eval(context.Background(), line)
		if r.Err != nil {
			t.Fatalf("%.60s: %v", line, r.Err)
		}
		out.WriteString(r.Stdout)
	}
	got, want := strings.Split(out.String(), "\n"), strings.Split(string(golden), "\n")
	if len(got) != len(want) {
		t.Errorf("%d lines, julia printed %d", len(got), len(want))
	}
	for i := range want {
		if i < len(got) && got[i] != want[i] {
			t.Errorf("line %d\n--- ours\n%s\n--- julia 1.12.6\n%s", i+1, got[i], want[i])
		}
	}
}
