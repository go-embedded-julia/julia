package interp

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"
)

// TestBaseSnippets builds julia's Base once (as TestBaseProgress does), then
// runs only the snippets of the file named by JLGO_SNIPPETS (separated by
// lines "#---", or "@N" to take program N of testdata/tasks_base.jl), timing
// each and writing a CPU profile per snippet to JLGO_PROFILE_DIR (default:
// the package directory) as snippet-<n>.pprof. Opt-in: JLGO_BASE=1.
func TestBaseSnippets(t *testing.T) {
	file := os.Getenv("JLGO_SNIPPETS")
	if os.Getenv("JLGO_BASE") != "1" || file == "" {
		t.Skip("set JLGO_BASE=1 JLGO_SNIPPETS=<file> to profile snippets on Base")
	}
	dir := os.Getenv("JLGO_PROFILE_DIR")
	if dir == "" {
		dir = "."
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	vm := New()
	vm.Args = []string{"Base_compiler.jl"}
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	vm.Stdout = &out
	vm.GeneratingOutput = true
	if _, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"}); err != nil {
		t.Fatal(err)
	}
	vm.Args = []string{"sysimg.jl"}
	if _, err := vm.jlLoad([]any{vm.Main, "sysimg.jl"}); err != nil {
		t.Fatal(err)
	}
	vm.GeneratingOutput = false
	if err := vm.RunModuleInitializers(); err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	s := vm.NewSession()
	progs := strings.Split(strings.TrimSpace(string(b)), "\n#---\n")
	for i, src := range progs {
		src = strings.TrimSpace(src)
		if n := 0; strings.HasPrefix(src, "@") {
			fmt.Sscan(src[1:], &n)
			src = strings.TrimSpace(loadTasksBase(t)[n-1])
		}
		f, err := os.Create(filepath.Join(dir, fmt.Sprintf("snippet-%d.pprof", i+1)))
		if err != nil {
			t.Fatal(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		// a snippet that runs for minutes needs its profile before it ends:
		// stop (and flush) it after JLGO_PROFILE_SECS (default 120)
		secs := 120
		fmt.Sscan(os.Getenv("JLGO_PROFILE_SECS"), &secs)
		timer := time.AfterFunc(time.Duration(secs)*time.Second, pprof.StopCPUProfile)
		v, err := s.Eval(src)
		timer.Stop()
		el := time.Since(start)
		pprof.StopCPUProfile()
		f.Close()
		t.Logf("snippet %d (%s) in %v: %s %v", i+1, strings.ReplaceAll(src, "\n", "; "), el, showValue(v), err)
	}
}
