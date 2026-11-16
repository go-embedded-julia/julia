package interp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// corpusBodies reads the committed corpus of FuzzLoadImage.
func corpusBodies(t testing.TB) [][]byte {
	files, err := filepath.Glob(filepath.Join("testdata", "fuzz", "FuzzLoadImage", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus: %v", err)
	}
	var out [][]byte
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.SplitN(string(text), "\n", 3)
		lit := strings.TrimSuffix(strings.TrimPrefix(lines[1], "[]byte("), ")")
		s, err := strconv.Unquote(lit)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		out = append(out, []byte(s))
	}
	return out
}

// Concurrent loads in one process: every answer is a VM or a typed error, and
// run under -race no access to the shared type caches, hints or instances is
// unsynchronized. (What a single refused load leaves unchanged cannot be
// asserted here: another load that succeeds legitimately changes it.)
func TestLoadImageConcurrent(t *testing.T) {
	bodies := append(corpusBodies(t), seedBody(t), []byte{}, failingLate(t, atomicImage(t)).body())
	for _, n := range []int{2, 4, 8} {
		var wg sync.WaitGroup
		for g := 0; g < n; g++ {
			for _, body := range bodies {
				wg.Add(1)
				go func() {
					defer wg.Done()
					vm, err := LoadImage(bytes.NewReader(sealImage(&vmImageSpec, body)), LoadOptions{MaxBytes: 1 << 22, MaxObjects: 1 << 16})
					if err == nil {
						if vm == nil {
							t.Error("no VM and no error")
						}
						return
					}
					for _, k := range imageErrors {
						if errors.Is(err, k) {
							return
						}
					}
					t.Errorf("an untyped error: %v", err)
				}()
			}
		}
		wg.Wait()
	}
}
