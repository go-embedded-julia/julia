package syntax

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-embedded-julia/julia/internal/gen"
)

// maxPlatformFiles bounds how many corpus files may differ from the golden
// manifest because the installation that holds them was built for another
// platform (base/build_h.jl records the build machine, for instance).
const maxPlatformFiles = 8

// TestCorpus parses every file of the reference corpus — base, Compiler,
// the stdlibs and test of the pinned julia — and compares, at every level
// implemented, the digest of the Go parser's output with the one recorded
// from julia. $JLGO_CORPUS is the share/julia directory of a julia 1.12.6
// installation; the test is skipped without it unless $JLGO_REQUIRE_CORPUS
// is set, which CI does.
func TestCorpus(t *testing.T) {
	root := os.Getenv("JLGO_CORPUS")
	if root == "" {
		if os.Getenv("JLGO_REQUIRE_CORPUS") != "" {
			t.Fatal("JLGO_REQUIRE_CORPUS is set but JLGO_CORPUS is not")
		}
		t.Skip("set JLGO_CORPUS to the share/julia directory of julia 1.12.6")
	}
	entries := readGolden[gen.CorpusEntry](t, "corpus.jsonl")
	identical := make([]int, len(levels))
	platform, checked := 0, 0
	for _, e := range entries {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Errorf("%s: %v", e.Path, err)
			continue
		}
		if gen.Digest(string(src)) != e.SrcSHA256 {
			platform++
			t.Logf("%s: differs from the recorded corpus (platform-specific file), skipped", e.Path)
			continue
		}
		checked++
		for i, lv := range levels {
			want, recorded := lv.digest(e)
			switch {
			case gen.Digest(lv.run(t, src, e.Path)) == want:
				identical[i]++
				if !recorded {
					t.Logf("%s: %s now identical to julia; run `jloracle corpus` to record it", e.Path, lv.name)
				}
			case recorded:
				t.Errorf("%s: regression, the %s no longer match julia's", e.Path, lv.name)
			}
		}
	}
	if platform > maxPlatformFiles {
		t.Errorf("%d corpus files differ from the manifest: not the pinned julia?", platform)
	}
	t.Logf("corpus of %d files (%d platform-specific skipped)", checked, platform)
	for i, lv := range levels {
		t.Logf("  %s identical to julia: %d/%d files", lv.name, identical[i], checked)
	}

	// Control: the comparison must be able to see a difference. A planted
	// statement changes the digests of the first file at every level.
	first := entries[0]
	src, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.Path)))
	src = append(src, "\n:planted"...)
	for _, lv := range levels {
		if want, _ := lv.digest(first); gen.Digest(lv.run(t, src, first.Path)) == want {
			t.Fatalf("control: a planted statement did not change the %s digest", lv.name)
		}
	}
}
