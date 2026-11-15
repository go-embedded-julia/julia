package interp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The Base oracle of the system image (#40, design section 5): Base is built to
// the cut where julia writes sys.so and saved; in a FRESH process the image is
// loaded, the module initializers run and the 16 task programs of
// testdata/tasks_base.jl are run. Their outputs must be the built VM's and
// julia's (tasks_base.golden), and save, load, save across fresh processes must
// give the same bytes.

// taskAnswers runs the task programs the way probeBaseTasks does and returns
// the answer of each, which is julia's text/plain display or "<error> ...".
func taskAnswers(t testing.TB, vm *VM) []string {
	t.Helper()
	s := vm.NewSession()
	show, err := s.Eval(`v -> repr(MIME("text/plain"), v)`)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range readTasksBaseGolden(t) {
		v, err := s.Eval(c.Src)
		if err == nil {
			v, err = vm.apply(show, []any{v})
		}
		got := "<error> " + fmt.Sprint(err)
		if str, ok := v.(string); err == nil && ok {
			got = str
		}
		out = append(out, got)
	}
	return out
}

// oracleChild is what the fresh process reports.
type oracleChild struct {
	LoadNs    int64
	Identical bool     // save, load, save: the bytes of the image
	Outputs   []string // the task programs, after the module initializers
}

func oracleCache(t testing.TB) string {
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "go-embedded-julia-image-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestImageBaseOracleChild is the fresh process: JLGO_ORACLE_IMAGE names the
// image, JLGO_ORACLE_OUT where the report goes, JLGO_ORACLE_MODE "load" only
// loads it (for the memory of a load alone).
func TestImageBaseOracleChild(t *testing.T) {
	path := os.Getenv("JLGO_ORACLE_IMAGE")
	if path == "" {
		t.Skip("run by TestImageBaseOracle")
	}
	img, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	vm := loadBytes(t, img)
	rep := oracleChild{LoadNs: time.Since(t0).Nanoseconds()}
	if os.Getenv("JLGO_ORACLE_MODE") == "load" {
		return
	}
	var again bytes.Buffer
	if err := vm.SaveImage(&again, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	rep.Identical = bytes.Equal(img, again.Bytes())
	vm.GeneratingOutput = false
	if err := vm.RunModuleInitializers(); err != nil {
		t.Fatal(err)
	}
	defer vm.Close()
	rep.Outputs = taskAnswers(t, vm)
	b, _ := json.Marshal(rep)
	if err := os.WriteFile(os.Getenv("JLGO_ORACLE_OUT"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// loadAverage is the 1-minute load average `uptime` reports, or -1.
func loadAverage() float64 {
	out, err := exec.Command("uptime").Output()
	if err != nil {
		return -1
	}
	s := string(out)
	i := strings.LastIndex(s, "load average")
	if i < 0 {
		return -1
	}
	f := strings.Fields(strings.NewReplacer(",", " ", ":", " ").Replace(s[i+len("load average"):]))
	for _, w := range f {
		if x, err := strconv.ParseFloat(strings.TrimPrefix(w, "s"), 64); err == nil {
			return x
		}
	}
	return -1
}

func TestImageBaseOracle(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 to build Base (several minutes)")
	}
	vm := New()
	vm.Args = []string{"Base_compiler.jl"}
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	vm.GeneratingOutput = true
	if _, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"}); err != nil {
		t.Fatal(err)
	}
	vm.Args = []string{"sysimg.jl"}
	if _, err := vm.jlLoad([]any{vm.Main, "sysimg.jl"}); err != nil {
		t.Fatal(err)
	}
	var img bytes.Buffer
	if err := vm.SaveImage(&img, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	dir := oracleCache(t)
	file := filepath.Join(dir, "oracle.img")
	if err := os.WriteFile(file, img.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(img.Bytes())

	// the built VM goes on from the cut: its outputs are julia's
	vm.GeneratingOutput = false
	if err := vm.RunModuleInitializers(); err != nil {
		t.Fatal(err)
	}
	built := taskAnswers(t, vm)
	vm.Close()
	golden := readTasksBaseGolden(t)
	if len(built) != len(golden) || len(built) != 16 {
		t.Fatalf("%d answers, %d in the golden, want 16", len(built), len(golden))
	}
	for i, c := range golden {
		if built[i] != c.Want {
			t.Errorf("built VM, tasks program %d:\n%s\nGo:    %s\njulia: %s", i+1, c.Src, built[i], c.Want)
		}
	}

	// a fresh process loads the image, saves it again, runs the same programs
	run := func(mode string) (oracleChild, *os.ProcessState) {
		out := filepath.Join(dir, "oracle-"+mode+".json")
		os.Remove(out)
		cmd := exec.Command(os.Args[0], "-test.run=^TestImageBaseOracleChild$", "-test.v", "-test.timeout=60m")
		cmd.Env = append(os.Environ(), "JLGO_ORACLE_IMAGE="+file, "JLGO_ORACLE_OUT="+out, "JLGO_ORACLE_MODE="+mode)
		if o, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("the fresh process (%s): %v\n%s", mode, err, o)
		}
		var rep oracleChild
		if mode != "load" {
			b, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(b, &rep); err != nil {
				t.Fatal(err)
			}
		}
		return rep, cmd.ProcessState
	}
	load1 := loadAverage()
	rep, _ := run("run")
	if !rep.Identical {
		t.Error("save, load, save in a fresh process gives other bytes than the image")
	}
	if len(rep.Outputs) != len(built) {
		t.Fatalf("the fresh process gave %d answers, want %d", len(rep.Outputs), len(built))
	}
	for i := range built {
		if rep.Outputs[i] != built[i] || rep.Outputs[i] != golden[i].Want {
			t.Errorf("tasks program %d in the fresh process:\n%s\nloaded: %s\nbuilt:  %s\njulia:  %s",
				i+1, golden[i].Src, rep.Outputs[i], built[i], golden[i].Want)
		}
	}
	t.Logf("image %d bytes, sha256 %x; 16 task programs: the loaded VM, the built VM and julia agree; save, load, save identical across processes: %v",
		img.Len(), sum, rep.Identical)

	// the measurements, on a quiet machine only
	load2 := loadAverage()
	if load1 < 0 || load2 < 0 || max(load1, load2) > 8 {
		t.Logf("measurements deferred: the 1-minute load average is %.1f then %.1f (at most 8 is needed)", load1, load2)
		return
	}
	_, ps := run("load")
	rss := peakRSS(ps)
	t.Logf("1-minute load %.1f then %.1f; load of the image in a fresh process: %v; peak RSS of a process that only loads it: %d MiB",
		load1, load2, time.Duration(rep.LoadNs), rss>>20)
	for _, lvl := range []string{"3", "19"} {
		cmd := exec.Command("zstd", "-q", "-"+lvl, "-c")
		cmd.Stdin = bytes.NewReader(img.Bytes())
		z, err := cmd.Output()
		if err != nil {
			t.Logf("zstd -%s: %v (no zstd to measure the compressed size)", lvl, err)
			break
		}
		t.Logf("zstd -%s: %d bytes (%.1f%% of %d)", lvl, len(z), 100*float64(len(z))/float64(img.Len()), img.Len())
	}
}
