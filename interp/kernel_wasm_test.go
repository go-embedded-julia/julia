package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"runtime"
	"testing"
	"time"
)

// TestWasmKernelFromBytes is what go-notebook's playground does: the image is
// a static asset, its bytes are passed in with the digest that was pinned for
// them, and cells run on the Kernel it makes. It runs on every platform; the
// WebAssembly lane of CI (GOOS=js under node) runs it, and the kernel tests,
// as a binary of the browser's platform.
func TestWasmKernelFromBytes(t *testing.T) {
	img := saveBytes(t, bootedVM(t))
	digest := sha256.Sum256(img)

	t0 := time.Now()
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{Digest: &digest}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	t.Logf("%s/%s: %d bytes loaded in %v", runtime.GOOS, runtime.GOARCH, len(img), time.Since(t0))

	// a value
	evalOK(t, k, "Core.Intrinsics.add_int(20, 22)", "42")
	// stdout, and stderr apart from it
	r := k.Eval(context.Background(), `Core.println("hello"); Core.println(Core.stderr, "warn"); 1`)
	if r.Stdout != "hello\n" || r.Stderr != "warn\n" || r.Err != nil {
		t.Errorf("output: %+v", r)
	}
	// an error is a value, and the next cell runs
	r = k.Eval(context.Background(), `throw(ErrorException("boom"))`)
	var je *JuliaError
	if !errors.As(r.Err, &je) || je.Message != "boom" {
		t.Errorf("an error: %+v", r)
	}
	evalOK(t, k, "1", "1")
	// cancellation of a loop that never yields: on WebAssembly one thread runs
	// the cell and the goroutine that watches its context (yield_wasm.go)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	r = k.Eval(ctx, `while true end`)
	if !errors.Is(r.Err, ErrCellCanceled) {
		t.Errorf("cancellation: %+v", r)
	}
	r = k.EvalWith(context.Background(), `while true end`, CellOptions{Timeout: 50 * time.Millisecond})
	if !errors.Is(r.Err, ErrCellTimeout) {
		t.Errorf("a timeout: %+v", r)
	}
	evalOK(t, k, "2", "2")

	// the bytes are refused when they are not the ones pinned
	other := digest
	other[0] ^= 1
	if _, err := LoadKernel(bytes.NewReader(img), LoadOptions{Digest: &other}, KernelOptions{}); !errors.Is(err, ErrImageDigest) {
		t.Errorf("a wrong digest: %v", err)
	}
	// FetchImage needs the network and a cache directory, which a browser has
	// not: a version that is no tag, or an address that is not https, is a
	// typed error before either is asked for
	if _, err := FetchImage(context.Background(), "(devel)", digest, FetchOptions{}); !errors.Is(err, ErrImageVersion) {
		t.Errorf("a version that is no tag: %v", err)
	}
	if _, err := FetchImage(context.Background(), fetchVersion, digest, FetchOptions{BaseURL: "http://example.invalid"}); !errors.Is(err, ErrImageFetch) {
		t.Errorf("a plain http address: %v", err)
	}
}
