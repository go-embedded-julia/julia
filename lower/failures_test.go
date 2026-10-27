package lower

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// failureLog groups the corpus forms the Go lowering does not reproduce
// (JLGO_FAILDUMP=<file>): by error message, or by the text around the
// first difference, with one sample input per group.
type failureLog struct {
	counts  map[string]int
	samples map[string]string
}

var failures *failureLog

func (f *failureLog) record(input string, got any, err error, want string) {
	var key, sample string
	switch {
	case err != nil:
		key = "error: " + err.Error()
		if len(key) > 160 {
			key = key[:160]
		}
		sample = input
	default:
		g := expr.Serialize(Canonical(got))
		if g == want {
			return
		}
		k := 0
		for k < len(g) && k < len(want) && g[k] == want[k] {
			k++
		}
		ctx := func(s string) string { return s[max(0, k-40):min(len(s), k+60)] }
		key = "diff: got …" + ctx(g) + "\n      want …" + ctx(want)
		sample = input
	}
	f.counts[key]++
	if _, ok := f.samples[key]; !ok || len(sample) < len(f.samples[key]) {
		f.samples[key] = sample
	}
}

func (f *failureLog) write(t *testing.T, path string) {
	keys := make([]string, 0, len(f.counts))
	for k := range f.counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return f.counts[keys[i]] > f.counts[keys[j]] })
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%6d %s\n       sample: %s\n\n", f.counts[k], k, f.samples[k])
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Error(err)
	}
}
