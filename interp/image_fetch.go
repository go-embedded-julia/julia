package interp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Fetching the image of a release (#40 step 7, #49 point 1).
//
// The digest of an image cannot be written in the source of the release it
// belongs to: the image depends on that release's source. So, as with go.sum,
// the consumer pins it. A program that imports julia@vX holds the SHA-256 of
// vX's image (from the release notes, the .sha256 asset, or images.sum at the
// repository root), and FetchImage refuses any other bytes before they reach
// LoadImage: an image is not authenticated by its key or its trailer, and a
// load writes state the whole process shares.

// The release assets of a version: julia-image-vX.bin and julia-image-vX.sha256.
const (
	// DefaultImageBaseURL is where the releases of this module publish assets.
	DefaultImageBaseURL = "https://github.com/go-embedded-julia/julia/releases/download"
	// DefaultFetchMaxBytes bounds a download.
	DefaultFetchMaxBytes = 256 << 20
)

// ImageAssetName is the name of the asset that holds the image of a version.
func ImageAssetName(version string) string { return "julia-image-" + version + ".bin" }

var (
	// ErrImageDigest is bytes whose SHA-256 is not the one pinned.
	ErrImageDigest = errors.New("interp: the image is not the one pinned (SHA-256 differs)")
	// ErrImageFetch is a download that could not be made or was refused.
	ErrImageFetch = errors.New("interp: the image could not be fetched")
)

// FetchOptions configure FetchImage; a zero field is its default.
type FetchOptions struct {
	// Client makes the request; its default has a ten minute timeout and
	// follows redirects only to https.
	Client *http.Client
	// BaseURL is the https URL the version and the asset name are appended to.
	BaseURL string
	// MaxBytes bounds the download, in bytes.
	MaxBytes int64
	// CacheDir is where verified images are kept; the default is
	// os.UserCacheDir()/go-embedded-julia/images.
	CacheDir string
}

// ParseDigest reads the 64 hexadecimal digits of a SHA-256, as the .sha256
// asset and images.sum give it (a "  name" after the digits is allowed).
func ParseDigest(s string) ([32]byte, error) {
	var d [32]byte
	f := strings.Fields(s)
	if len(f) == 0 {
		return d, fmt.Errorf("%w: no digest in %q", ErrImageDigest, s)
	}
	b, err := hex.DecodeString(f[0])
	if err != nil || len(b) != 32 {
		return d, fmt.Errorf("%w: %q is not 64 hexadecimal digits", ErrImageDigest, f[0])
	}
	copy(d[:], b)
	return d, nil
}

// FetchImage returns the image of release version (a tag), whose SHA-256 is
// want. Nothing is returned that does not have that digest: a cached copy is
// hashed again, never trusted, and removed when it differs; a download is
// read within MaxBytes, hashed, and only then cached (in the user's cache
// directory, never the repository) and handed back. Errors are typed:
// ErrImageVersion, ErrImageFetch, ErrImageLimit (over MaxBytes),
// ErrImageTruncated (the body ended before it said it would) and
// ErrImageDigest. The bytes are for LoadImage.
func FetchImage(ctx context.Context, version string, want [32]byte, opts FetchOptions) ([]byte, error) {
	if err := CheckImageVersion(version); err != nil {
		return nil, err
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultFetchMaxBytes
	}
	if opts.BaseURL == "" {
		opts.BaseURL = DefaultImageBaseURL
	}
	if !strings.HasPrefix(opts.BaseURL, "https://") {
		return nil, fmt.Errorf("%w: %q is not an https URL", ErrImageFetch, opts.BaseURL)
	}
	dir := opts.CacheDir
	if dir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("%w: no cache directory: %v", ErrImageFetch, err)
		}
		dir = filepath.Join(base, "go-embedded-julia", "images")
	}
	cached := filepath.Join(dir, fmt.Sprintf("%s-%x.img", version, want[:8]))
	if b, err := readCachedImage(cached, want, opts.MaxBytes); err == nil {
		return b, nil
	}

	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 10 * time.Minute, CheckRedirect: refuseDowngrade}
	}
	url := strings.TrimSuffix(opts.BaseURL, "/") + "/" + version + "/" + ImageAssetName(version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageFetch, err)
	}
	resp, err := opts.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrImageFetch, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %s answered %s", ErrImageFetch, url, resp.Status)
	}
	if resp.ContentLength > opts.MaxBytes {
		return nil, fmt.Errorf("%w: %s is %d bytes, the limit is %d", ErrImageLimit, url, resp.ContentLength, opts.MaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, opts.MaxBytes+1))
	switch {
	case errors.Is(err, io.ErrUnexpectedEOF):
		return nil, fmt.Errorf("%w: the body of %s ended early", ErrImageTruncated, url)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrImageFetch, err)
	case int64(len(body)) > opts.MaxBytes:
		return nil, fmt.Errorf("%w: %s is over %d bytes", ErrImageLimit, url, opts.MaxBytes)
	}
	if got := sha256.Sum256(body); got != want {
		return nil, fmt.Errorf("%w: %s is %x, pinned %x", ErrImageDigest, url, got, want)
	}
	writeCachedImage(dir, cached, body) // best effort: the image is good whether or not it is kept
	return body, nil
}

// refuseDowngrade is the redirect policy: only to https, and not for ever.
func refuseDowngrade(req *http.Request, via []*http.Request) error {
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to %s: only https", req.URL.Scheme)
	}
	if len(via) >= 5 {
		return errors.New("stopped after 5 redirects")
	}
	return nil
}

// readCachedImage is the cached file when it has the digest; a file that has
// another, or is over the limit, is removed.
func readCachedImage(path string, want [32]byte, max int64) ([]byte, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Size() <= max {
		if b, err := os.ReadFile(path); err == nil && sha256.Sum256(b) == want {
			return b, nil
		}
	}
	os.Remove(path)
	return nil, ErrImageDigest
}

// writeCachedImage keeps the image under its name, whole or not at all.
func writeCachedImage(dir, path string, b []byte) {
	if os.MkdirAll(dir, 0o700) != nil {
		return
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		os.Remove(tmp)
	}
}
