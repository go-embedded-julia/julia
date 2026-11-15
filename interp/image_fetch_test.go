package interp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// The fetch of a release image, against a local https server.

const fetchVersion = "v0.14.0"

func skipWithoutNetwork(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "js" || runtime.GOOS == "wasip1" {
		t.Skip("no sockets here")
	}
}

// imageServer serves body at the asset path of fetchVersion and counts the
// requests.
func imageServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.URL.Path != "/"+fetchVersion+"/"+ImageAssetName(fetchVersion) {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func serve(body []byte) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { w.Write(body) }
}

func fetchOpts(srv *httptest.Server, dir string) FetchOptions {
	return FetchOptions{Client: srv.Client(), BaseURL: srv.URL, CacheDir: dir}
}

func TestFetchImage(t *testing.T) {
	skipWithoutNetwork(t)
	body := bytes.Repeat([]byte("an image "), 1000)
	want := sha256.Sum256(body)
	srv, hits := imageServer(t, serve(body))
	dir := t.TempDir()
	ctx := context.Background()

	// the right digest: the bytes, and a copy in the cache directory
	got, err := FetchImage(ctx, fetchVersion, want, fetchOpts(srv, dir))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the right digest: %d bytes, %v", len(got), err)
	}
	files, _ := os.ReadDir(dir)
	if len(files) != 1 || !strings.HasPrefix(files[0].Name(), fetchVersion+"-") || strings.HasSuffix(files[0].Name(), ".tmp") {
		t.Errorf("cache directory: %v", files)
	}
	// a second fetch is the cache's, hashed again, and asks nobody
	if got, err := FetchImage(ctx, fetchVersion, want, fetchOpts(srv, dir)); err != nil || !bytes.Equal(got, body) || hits.Load() != 1 {
		t.Errorf("second fetch: %v, %d requests", err, hits.Load())
	}

	// a corrupted cache file is not trusted: it is removed and the image fetched
	cached := filepath.Join(dir, files[0].Name())
	bad := append([]byte(nil), body...)
	bad[len(bad)/2] ^= 1
	if err := os.WriteFile(cached, bad, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = FetchImage(ctx, fetchVersion, want, fetchOpts(srv, dir))
	if err != nil || !bytes.Equal(got, body) || hits.Load() != 2 {
		t.Errorf("a corrupted cache file: %v, %d requests", err, hits.Load())
	}
	if b, _ := os.ReadFile(cached); !bytes.Equal(b, body) {
		t.Error("the corrupted cache file was not replaced")
	}
	// a cache file over the limit is not read
	if _, err := FetchImage(ctx, fetchVersion, want, FetchOptions{Client: srv.Client(), BaseURL: srv.URL, CacheDir: dir, MaxBytes: 100}); !errors.Is(err, ErrImageLimit) {
		t.Errorf("a limit under the cached image: %v", err)
	}
	// a cache entry that is a directory is no image
	other := sha256.Sum256([]byte("another"))
	if err := os.MkdirAll(filepath.Join(dir, fmt.Sprintf("%s-%x.img", fetchVersion, other[:8])), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := FetchImage(ctx, fetchVersion, other, fetchOpts(srv, dir)); !errors.Is(err, ErrImageDigest) {
		t.Errorf("a directory in the cache: %v", err)
	}
}

func TestFetchImageRefusals(t *testing.T) {
	skipWithoutNetwork(t)
	body := bytes.Repeat([]byte("x"), 5000)
	want := sha256.Sum256(body)
	ctx := context.Background()
	check := func(name string, h func(w http.ResponseWriter, r *http.Request), opts func(*FetchOptions), kind error) {
		t.Helper()
		srv, _ := imageServer(t, h)
		dir := t.TempDir()
		o := fetchOpts(srv, dir)
		if opts != nil {
			opts(&o)
		}
		got, err := FetchImage(ctx, fetchVersion, want, o)
		if !errors.Is(err, kind) || got != nil {
			t.Errorf("%s: %d bytes, %v; want %v", name, len(got), err, kind)
		}
		if files, _ := os.ReadDir(dir); len(files) != 0 {
			t.Errorf("%s: a refused image was cached: %v", name, files)
		}
	}
	// another digest: refused, before anything is cached
	wrong := bytes.Repeat([]byte("y"), 5000)
	check("a wrong digest", serve(wrong), nil, ErrImageDigest)
	// one byte more, one byte less
	check("a byte too many", serve(append(append([]byte(nil), body...), 0)), nil, ErrImageDigest)
	check("a byte missing", serve(body[:len(body)-1]), nil, ErrImageDigest)
	// over the limit: declared (Content-Length) and not (chunked)
	check("an oversize body", serve(body), func(o *FetchOptions) { o.MaxBytes = 1000 }, ErrImageLimit)
	check("an oversize chunked body", func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush() // no Content-Length
		for i := 0; i < 5; i++ {
			w.Write(body[:1000])
			w.(http.Flusher).Flush()
		}
	}, func(o *FetchOptions) { o.MaxBytes = 2500 }, ErrImageLimit)
	// a body that ends before its Content-Length
	check("a truncated body", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		w.Write(body[:2000])
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler)
	}, nil, ErrImageTruncated)
	// the server's refusals
	check("a missing asset", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "gone", http.StatusGone) }, nil, ErrImageFetch)
	check("a server that serves nothing for this version", serve(body), func(o *FetchOptions) { o.BaseURL = o.BaseURL + "/nowhere" }, ErrImageFetch)

	// a request that cannot be made, one that fails, one cancelled
	srv, _ := imageServer(t, serve(body))
	for name, o := range map[string]FetchOptions{
		"a malformed URL":                        {BaseURL: "https://a b", CacheDir: t.TempDir()},
		"no https":                               {BaseURL: strings.Replace(srv.URL, "https", "http", 1), CacheDir: t.TempDir()},
		"a certificate the client does not know": {BaseURL: srv.URL, CacheDir: t.TempDir()}, // the default client
	} {
		if _, err := FetchImage(ctx, fetchVersion, want, o); !errors.Is(err, ErrImageFetch) {
			t.Errorf("%s: %v", name, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := FetchImage(cancelled, fetchVersion, want, fetchOpts(srv, t.TempDir())); !errors.Is(err, ErrImageFetch) {
		t.Errorf("a cancelled context: %v", err)
	}
	// a version that is no tag is refused before any request
	for _, v := range []string{"", "(devel)", "v0.14.1-0.20260101120000-0123456789ab", "v1.0.0+dirty", "../v1.0.0"} {
		if _, err := FetchImage(ctx, v, want, fetchOpts(srv, t.TempDir())); !errors.Is(err, ErrImageVersion) {
			t.Errorf("version %q: %v", v, err)
		}
	}
}

// brokenBody fails its read with an error that is not a short body.
type brokenBody struct{}

func (brokenBody) Read([]byte) (int, error) { return 0, errors.New("connection reset by a peer") }
func (brokenBody) Close() error             { return nil }

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A body that fails in the middle is a fetch error, not a short image.
func TestFetchImageBrokenBody(t *testing.T) {
	client := &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: brokenBody{}, ContentLength: -1, Request: r}, nil
	})}
	_, err := FetchImage(context.Background(), fetchVersion, [32]byte{}, FetchOptions{Client: client, BaseURL: "https://example.invalid", CacheDir: t.TempDir()})
	if !errors.Is(err, ErrImageFetch) || errors.Is(err, ErrImageTruncated) {
		t.Errorf("a failing body: %v", err)
	}
}

// A declared length over the limit is refused before the body is read, and the
// default base URL is the release page of this module.
func TestFetchImageDeclaredLength(t *testing.T) {
	var asked string
	client := &http.Client{Transport: fakeTransport(func(r *http.Request) (*http.Response, error) {
		asked = r.URL.String()
		return &http.Response{StatusCode: 200, Status: "200 OK", Body: brokenBody{}, ContentLength: 5000, Request: r}, nil
	})}
	_, err := FetchImage(context.Background(), fetchVersion, [32]byte{}, FetchOptions{Client: client, MaxBytes: 1000, CacheDir: t.TempDir()})
	if !errors.Is(err, ErrImageLimit) {
		t.Errorf("a declared length over the limit: %v", err)
	}
	if want := DefaultImageBaseURL + "/" + fetchVersion + "/" + ImageAssetName(fetchVersion); asked != want {
		t.Errorf("asked %s, want %s", asked, want)
	}
}

// The cache is the user's: its default is the user cache directory, and a
// cache that cannot be written does not stop a good image.
func TestFetchImageCache(t *testing.T) {
	skipWithoutNetwork(t)
	body := []byte("a small image")
	want := sha256.Sum256(body)
	srv, _ := imageServer(t, serve(body))
	ctx := context.Background()

	// the default directory: under os.UserCacheDir
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", home)
	t.Setenv("LocalAppData", home)
	if got, err := FetchImage(ctx, fetchVersion, want, FetchOptions{Client: srv.Client(), BaseURL: srv.URL}); err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the default cache directory: %v", err)
	}
	if dir, _ := os.UserCacheDir(); !strings.HasPrefix(dir, home) {
		t.Logf("os.UserCacheDir is %s, not under %s on this system", dir, home)
	} else if _, err := os.Stat(filepath.Join(dir, "go-embedded-julia", "images")); err != nil {
		t.Errorf("no cache under the user cache directory: %v", err)
	}
	// no user cache directory at all: refused, not guessed
	t.Setenv("HOME", "")
	t.Setenv("XDG_CACHE_HOME", "")
	t.Setenv("LocalAppData", "")
	if _, err := os.UserCacheDir(); err == nil {
		t.Skip("this system has a cache directory without a home")
	}
	if _, err := FetchImage(ctx, fetchVersion, want, FetchOptions{Client: srv.Client(), BaseURL: srv.URL}); !errors.Is(err, ErrImageFetch) {
		t.Errorf("no cache directory: %v", err)
	}

	// a cache that cannot be written: the image is returned all the same
	root := t.TempDir()
	notADir := filepath.Join(root, "file")
	os.WriteFile(notADir, nil, 0o600)
	cases := map[string]string{"a directory under a file": filepath.Join(notADir, "cache")}
	blocked := filepath.Join(root, "blocked")
	os.MkdirAll(blocked, 0o700)
	name := fmt.Sprintf("%s-%x.img", fetchVersion, want[:8])
	os.MkdirAll(filepath.Join(blocked, name+fmt.Sprintf(".%d.tmp", os.Getpid())), 0o700) // the temporary name taken by a directory
	cases["a temporary name taken"] = blocked
	taken := filepath.Join(root, "taken")
	os.MkdirAll(filepath.Join(taken, name, "inside"), 0o700) // the final name, a directory with a file in it
	cases["a final name taken"] = taken
	for what, dir := range cases {
		if got, err := FetchImage(ctx, fetchVersion, want, fetchOpts(srv, dir)); err != nil || !bytes.Equal(got, body) {
			t.Errorf("%s: %v", what, err)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(taken, "*.tmp")); len(left) != 0 {
		t.Errorf("a temporary file was left: %v", left)
	}
}

func TestParseDigest(t *testing.T) {
	d := sha256.Sum256([]byte("x"))
	hexd := fmt.Sprintf("%x", d)
	for in, ok := range map[string]bool{hexd: true, hexd + "  julia-image-v1.0.0.bin\n": true, "  " + hexd: true,
		"": false, "abc": false, hexd[:62]: false, strings.Repeat("g", 64): false, hexd + "00": false} {
		got, err := ParseDigest(in)
		if (err == nil) != ok || err != nil && !errors.Is(err, ErrImageDigest) || ok && got != d {
			t.Errorf("%q: %x, %v", in, got, err)
		}
	}
}

func TestRefuseDowngrade(t *testing.T) {
	https, _ := url.Parse("https://example.com/a")
	http1, _ := url.Parse("http://example.com/a")
	if err := refuseDowngrade(&http.Request{URL: https}, nil); err != nil {
		t.Errorf("a redirect to https: %v", err)
	}
	if err := refuseDowngrade(&http.Request{URL: http1}, nil); err == nil {
		t.Error("a redirect to http")
	}
	if err := refuseDowngrade(&http.Request{URL: https}, make([]*http.Request, 5)); err == nil {
		t.Error("an endless redirect")
	}
}

// images.sum at the root of the repository lists a digest per released
// version; each line must be a tag and 64 hexadecimal digits.
func TestImagesSum(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "images.sum"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Errorf("line %d: %q is not \"version digest\"", i+1, line)
			continue
		}
		if err := CheckImageVersion(f[0]); err != nil {
			t.Errorf("line %d: %v", i+1, err)
		}
		if _, err := ParseDigest(f[1]); err != nil {
			t.Errorf("line %d: %v", i+1, err)
		}
		if seen[f[0]] {
			t.Errorf("line %d: %s twice", i+1, f[0])
		}
		seen[f[0]] = true
	}
	_ = io.Discard
}
