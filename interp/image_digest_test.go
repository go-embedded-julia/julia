package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
)

// LoadOptions.Digest pins the whole image: any other bytes are refused with
// ErrImageDigest before the key, the framing or an object is looked at.

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("the disk went away") }

func TestLoadImageDigest(t *testing.T) {
	img := saveBytes(t, bootedVM(t))
	right := sha256.Sum256(img)
	wrong := right
	wrong[0] ^= 1
	var zero [32]byte

	// the right digest loads, and so does an image that is not pinned
	vm, err := LoadImage(bytes.NewReader(img), LoadOptions{Digest: &right})
	if err != nil || vm == nil {
		t.Fatalf("the right digest: %v", err)
	}
	if vm, err := LoadImage(bytes.NewReader(img), LoadOptions{}); err != nil || vm == nil {
		t.Fatalf("no digest, as before: %v", err)
	}
	// the wrong digest is refused, and no VM is made
	for name, d := range map[string]*[32]byte{"a wrong digest": &wrong, "an all-zero digest is a pin, not an unset one": &zero} {
		if vm, err := LoadImage(bytes.NewReader(img), LoadOptions{Digest: d}); !errors.Is(err, ErrImageDigest) || vm != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := LoadKernel(bytes.NewReader(img), LoadOptions{Digest: &wrong}, KernelOptions{}); !errors.Is(err, ErrImageDigest) {
		t.Errorf("LoadKernel, a wrong digest: %v", err)
	}
	if k, err := LoadKernel(bytes.NewReader(img), LoadOptions{Digest: &right}, KernelOptions{}); err != nil {
		t.Errorf("LoadKernel, the right digest: %v", err)
	} else {
		k.Close()
	}

	// the digest is read first: whatever else is wrong with the bytes, a wrong
	// digest is what is said, and nothing is decoded
	pinOf := func(b []byte) *[32]byte { d := sha256.Sum256(b); return &d }
	badKey := append([]byte(nil), img...)
	badKey[len(imageMagic)+4+3] ^= 1
	cut := img[:len(img)/2]
	junk := []byte("this is not an image at all, however long it goes on")
	for name, b := range map[string][]byte{"a foreign key": badKey, "a cut image": cut, "not an image": junk, "nothing": nil} {
		if _, err := LoadImage(bytes.NewReader(b), LoadOptions{Digest: &wrong}); !errors.Is(err, ErrImageDigest) {
			t.Errorf("%s, a wrong digest: %v", name, err)
		}
	}
	// and when the digest is right, the checks that follow still run
	for name, c := range map[string]struct {
		b    []byte
		want error
	}{"a foreign key": {badKey, ErrImageKey}, "a cut image": {cut, ErrImageTruncated}, "not an image": {junk, ErrImageCorrupt}} {
		if _, err := LoadImage(bytes.NewReader(c.b), LoadOptions{Digest: pinOf(c.b)}); !errors.Is(err, c.want) {
			t.Errorf("%s, its own digest: %v, want %v", name, err, c.want)
		}
	}
	// the size bound holds for a pinned image, and a reader that fails is an error
	if _, err := LoadImage(bytes.NewReader(img), LoadOptions{Digest: &right, MaxBytes: 1000}); !errors.Is(err, ErrImageLimit) {
		t.Errorf("a pinned image over MaxBytes: %v", err)
	}
	if _, err := LoadImage(failingReader{}, LoadOptions{Digest: &right}); err == nil || errors.Is(err, ErrImageDigest) {
		t.Errorf("a failing reader: %v", err)
	}
}

// FetchKernel passes its digest to the load, over the bytes the load is given.
func TestFetchKernelPinsTheLoad(t *testing.T) {
	skipWithoutNetwork(t)
	img := saveBytes(t, bootedVM(t))
	want := sha256.Sum256(img)
	srv, _ := imageServer(t, serve(img))
	other := want
	other[5] ^= 1
	// a digest of the caller's own is replaced by the pinned one
	k, err := FetchKernel(context.Background(), fetchVersion, want, fetchOpts(srv, t.TempDir()), LoadOptions{Digest: &other}, KernelOptions{})
	if err != nil {
		t.Fatal(err)
	}
	k.Close()
}
