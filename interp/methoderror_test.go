package interp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestMethodErrorText: the text showerror prints for a MethodError on julia's
// Base image (JLGO_BASE=1; JLGO_KERNEL_IMAGE names an image, else Base is
// built), against julia 1.12.6's, case by case (testdata/methoderror.jl,
// which regenerates testdata/methoderror.golden with `pkgx julia@1.12.6`).
//
// How the texts are compared: character for character, after one
// normalisation of both sides: a file in a candidate's `@ Module path:line` is
// reduced to its base name (`@ Module name.jl:line`), because the directories
// are the host's (julia's own tree under ~/.pkgx for the reference, the
// embedded sources here) and not part of the error; the line stays, and is
// exact, since the sources are julia's own. The cells a case defines say
// `none:LINE` on both sides, the file name the Kernel parses with.
func TestMethodErrorText(t *testing.T) {
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
	k, err := LoadKernel(bytes.NewReader(img), LoadOptions{}, KernelOptions{CellOptions: CellOptions{Timeout: time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()

	golden, err := os.ReadFile("testdata/methoderror.golden")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, line := range strings.Split(strings.TrimSpace(string(golden)), "\n") {
		var c struct {
			Name  string
			Cells []string
			Want  string
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		n++
		var last Result
		for _, cell := range c.Cells {
			last = k.Eval(context.Background(), cell)
		}
		var je *JuliaError
		if !errors.As(last.Err, &je) || je.Type != "MethodError" {
			t.Errorf("%s: the last cell did not throw a MethodError: %+v", c.Name, last)
			continue
		}
		if got, want := normalizeCandidatePaths(je.Message), normalizeCandidatePaths(c.Want); got != want {
			t.Errorf("%s:\n--- ours\n%s\n--- julia 1.12.6\n%s", c.Name, got, want)
			// why showerror did not print it: the error it raised
			k.vm.taskWorld = k.vm.World()
			if _, err := k.vm.apply(k.errText, []any{je.Value}); err != nil {
				t.Logf("%s: showerror raised: %v", c.Name, err)
			}
		}
	}
	if n < 10 {
		t.Errorf("%d cases in the golden", n)
	}
}

var (
	candidatePath = regexp.MustCompile(`(@ \S+ )(?:\S*/)?([^/\s]+:\d+)`)
	worldNumbers  = regexp.MustCompile(`world age \d+, while current world is \d+`)
)

// normalizeCandidatePaths reduces the file of each `@ Module path:line` to its
// base name, and the two world ages of a "may be too new" hint to a word: a
// world counter is the process's, and differs from one julia to the next.
func normalizeCandidatePaths(s string) string {
	s = worldNumbers.ReplaceAllString(s, "world age N, while current world is M")
	return candidatePath.ReplaceAllString(s, "$1$2")
}

func TestNormalizeCandidatePaths(t *testing.T) {
	in := "  +(::Number, !Matched::LinearAlgebra.UniformScaling)\n   @ LinearAlgebra ~/.pkgx/julialang.org/v1.12.6/share/julia/stdlib/v1.12/LinearAlgebra/src/uniformscaling.jl:148\n   @ Base operators.jl:642\n   @ Main none:1\n"
	want := "  +(::Number, !Matched::LinearAlgebra.UniformScaling)\n   @ LinearAlgebra uniformscaling.jl:148\n   @ Base operators.jl:642\n   @ Main none:1\n"
	if got := normalizeCandidatePaths(in); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
