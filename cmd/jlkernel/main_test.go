package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/interp"
)

// imageFile writes the image of a Boot()-only VM and returns its path and digest.
func imageFile(t *testing.T) (string, string) {
	t.Helper()
	vm := interp.New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	var img bytes.Buffer
	if err := vm.SaveImage(&img, interp.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "julia-image.bin")
	if err := os.WriteFile(path, img.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, fmt.Sprintf("%x", sha256.Sum256(img.Bytes()))
}

func execute(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errb bytes.Buffer
	cmd := RootCmd()
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	err = cmd.Execute()
	return out.String(), errb.String(), err
}

func TestRunsACell(t *testing.T) {
	image, digest := imageFile(t)
	out, errs, err := execute(t, "Core.println(\"hello\"); Core.Intrinsics.add_int(20, 22)", "--image", image, "--digest", digest)
	if err != nil || out != "hello\n42\n" || errs != "" {
		t.Errorf("a cell: %q %q %v", out, errs, err)
	}
	// stats go to standard error
	out, errs, err = execute(t, "7", "--image", image, "--digest", digest, "--stats")
	if err != nil || out != "7\n" || !strings.Contains(errs, "load ") || !strings.Contains(errs, "heap ") {
		t.Errorf("stats: %q %q %v", out, errs, err)
	}
	// what the cell printed to stderr stays there
	_, errs, err = execute(t, "Core.println(Core.stderr, \"warn\"); 1", "--image", image, "--digest", digest)
	if err != nil || errs != "warn\n" {
		t.Errorf("stderr: %q %v", errs, err)
	}
}

func TestRefusals(t *testing.T) {
	image, digest := imageFile(t)
	wrong := strings.Repeat("0", 64)
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"a wrong digest":     {[]string{"--image", image, "--digest", wrong}, "pinned"},
		"a digest not hex":   {[]string{"--image", image, "--digest", "xyz"}, "64 hexadecimal"},
		"no such image":      {[]string{"--image", filepath.Join(t.TempDir(), "none"), "--digest", digest}, "none"},
		"no image flag":      {[]string{"--digest", digest}, "required flag"},
		"a stray argument":   {[]string{"--image", image, "--digest", digest, "extra"}, "unknown command"},
		"a cell that throws": {[]string{"--image", image, "--digest", digest}, "boom"},
		"a cell out of time": {[]string{"--image", image, "--digest", digest, "--timeout", "50ms"}, "out of time"},
	} {
		src := "1"
		switch name {
		case "a cell that throws":
			src = `throw(ErrorException("boom"))`
		case "a cell out of time":
			src = "while true end"
		}
		_, _, err := execute(t, src, c.args...)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// the image is not an image
	bad := filepath.Join(t.TempDir(), "bad")
	os.WriteFile(bad, []byte("not an image"), 0o600)
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("not an image")))
	if _, _, err := execute(t, "1", "--image", bad, "--digest", sum); err == nil {
		t.Error("a file that is not an image")
	}
}

type failingIn struct{}

func (failingIn) Read([]byte) (int, error) { return 0, fmt.Errorf("stdin closed") }

func TestStdinError(t *testing.T) {
	image, digest := imageFile(t)
	cmd := RootCmd()
	cmd.SetArgs([]string{"--image", image, "--digest", digest})
	cmd.SetIn(failingIn{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "stdin closed") {
		t.Errorf("stdin: %v", err)
	}
}

func TestMain_(t *testing.T) {
	image, _ := imageFile(t)
	code := -1
	osExit = func(c int) { code = c }
	defer func() { osExit = os.Exit }()
	old := os.Args
	defer func() { os.Args = old }()
	os.Args = []string{"jlkernel", "--image", image, "--digest", strings.Repeat("0", 64)}
	main()
	if code != 1 {
		t.Errorf("exit status %d on a wrong digest", code)
	}
}
