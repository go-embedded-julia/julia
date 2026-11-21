package interp

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"testing"
)

// TestBuildImage is the builder of the release asset: it builds Base to the
// cut where julia writes sys.so, saves the image to JLGO_IMAGE_OUT and prints
// its SHA-256 (the release workflow runs it at a tag, with JLGO_BASE=1,
// JLGO_IMAGE_VERSION the tag and JLGO_IMAGE_OUT the file). The image is keyed
// for that tag whatever this test binary's own build information says (a test
// binary is no main module with a version): the build information it presents
// to the key is the one of julia built as the main module at the tag, and
// the tag must be a release version.
// The clock Base keeps in the image is logical while an image is generated,
// so two builds do not differ by the time they took.
func TestHrtimeWhileGeneratingIsLogical(t *testing.T) {
	vm := New()
	vm.GeneratingOutput = true
	a, b := vm.hrtime(), vm.hrtime()
	if a != 1000 || b != 2000 {
		t.Errorf("logical clock: %d, %d", a, b)
	}
	if got, _ := vm.shims["jl_typeinf_timing_begin"](vm, nil); got != uint64(3000|1) {
		t.Errorf("typeinf timing reads the logical clock: %v", got)
	}
	vm.GeneratingOutput = false
	if vm.hrtime() == 4000 && vm.hrtime() == 5000 {
		t.Error("the wall clock is not the logical one")
	}
}

func TestBuildImage(t *testing.T) {
	out, tag := os.Getenv("JLGO_IMAGE_OUT"), os.Getenv("JLGO_IMAGE_VERSION")
	if os.Getenv("JLGO_BASE") != "1" || out == "" {
		t.Skip("set JLGO_BASE=1, JLGO_IMAGE_OUT and JLGO_IMAGE_VERSION (the release workflow does)")
	}
	img := buildBaseImage(t, tag)
	if err := os.WriteFile(out, img, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("image %s %s/%s: %d bytes, sha256 %x", tag, runtime.GOOS, runtime.GOARCH, len(img), sha256.Sum256(img))
	fmt.Printf("IMAGE_SHA256=%x\n", sha256.Sum256(img))
}

// fakeReleaseVersion makes this test binary present itself, for the key of an
// image, as julia built as the main module at the tag.
func fakeReleaseVersion(t testing.TB, tag string) {
	t.Helper()
	if err := CheckImageVersion(tag); err != nil {
		t.Fatal(err)
	}
	old := readBuildInfo
	t.Cleanup(func() { readBuildInfo = old })
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: tag}}, true
	}
}

// imageWarmup is what the builder runs before it saves: the REPL display of a
// Float64 matrix, as Kernel's show does it.
const imageWarmup = `sprint(show, MIME("text/plain"), [1.0 2.0; 3.0 4.0]; context=(:limit=>true, :displaysize=>(24, 80)))`

// buildBaseImage builds Base to the cut where julia writes sys.so, keyed for
// tag (see fakeReleaseVersion, which stays in force), and returns the image,
// which it has checked loads.
func buildBaseImage(t testing.TB, tag string) []byte {
	t.Helper()
	fakeReleaseVersion(t, tag)
	vm := New()
	vm.Args = []string{"Base_compiler.jl"}
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vm.GeneratingOutput = true
	if _, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"}); err != nil {
		t.Fatal(err)
	}
	vm.Args = []string{"sysimg.jl"}
	if _, err := vm.jlLoad([]any{vm.Main, "sysimg.jl"}); err != nil {
		t.Fatal(err)
	}
	// the inference the display of a matrix makes (join's default_eltype, the
	// column widths) is saved with the image: julia's sys.so carries it from its
	// precompile statements, and without it the first display costs about 69M
	// interpreter statements (#119). The cell is the kernel's own show.
	if _, err := vm.NewSession().Eval(imageWarmup); err != nil {
		t.Fatalf("image warm-up: %v", err)
	}
	var img bytes.Buffer
	if err := vm.SaveImage(&img, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	// the image must load, and be the one the key of a consumer asks for
	if _, err := LoadImage(bytes.NewReader(img.Bytes()), LoadOptions{}); err != nil {
		t.Fatalf("the image does not load: %v", err)
	}
	return img.Bytes()
}
