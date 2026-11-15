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
func TestBuildImage(t *testing.T) {
	out, tag := os.Getenv("JLGO_IMAGE_OUT"), os.Getenv("JLGO_IMAGE_VERSION")
	if os.Getenv("JLGO_BASE") != "1" || out == "" {
		t.Skip("set JLGO_BASE=1, JLGO_IMAGE_OUT and JLGO_IMAGE_VERSION (the release workflow does)")
	}
	if err := CheckImageVersion(tag); err != nil {
		t.Fatal(err)
	}
	old := readBuildInfo
	defer func() { readBuildInfo = old }()
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: tag}}, true
	}
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
	var img bytes.Buffer
	if err := vm.SaveImage(&img, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, img.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// the image must load, and be the one the key of a consumer asks for
	if _, err := LoadImage(bytes.NewReader(img.Bytes()), LoadOptions{}); err != nil {
		t.Fatalf("the image does not load: %v", err)
	}
	t.Logf("image %s %s/%s: %d bytes, sha256 %x", tag, runtime.GOOS, runtime.GOARCH, img.Len(), sha256.Sum256(img.Bytes()))
	fmt.Printf("IMAGE_SHA256=%x\n", sha256.Sum256(img.Bytes()))
}
