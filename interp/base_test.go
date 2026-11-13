package interp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/internal/oracle"
)

// probeStdlibs runs, after sysimg.jl stopped, what Base.require of a stdlib
// does one step at a time, each in its own evaluation, so that one run of the
// 27-minute load reports every step that stops and not only the first.
func probeStdlibs(t *testing.T, vm *VM) {
	steps := []string{
		`Base.stat(joinpath(Sys.STDLIB, "SHA", "Project.toml")).size`,
		`(isdir(Sys.STDLIB), isfile(joinpath(Sys.STDLIB, "SHA", "Project.toml")), ispath(joinpath(Sys.STDLIB, "Nope")))`,
		`readdir(Sys.STDLIB)`,
		`realpath(joinpath(Sys.STDLIB, "SHA"))`,
		`Base.isfile_casesensitive(joinpath(Sys.STDLIB, "SHA", "Project.toml"))`,
		`sizeof(read(joinpath(Sys.STDLIB, "SHA", "Project.toml"), String))`,
		`open(Base._crc32c, joinpath(Sys.STDLIB, "SHA", "Project.toml"), "r")`,
		`Base.parsed_toml(joinpath(Sys.STDLIB, "SHA", "Project.toml"))["uuid"]`,
		`Base.identify_package(Base, "SHA")`,
		`Base.locate_package(Base.identify_package(Base, "SHA"))`,
		`Base.get_pkgversion_from_path(joinpath(Sys.STDLIB, "SHA"))`,
		`Base.find_all_in_cache_path(Base.identify_package(Base, "SHA"))`,
		`Base._require_search_from_serialized(Base.identify_package(Base, "SHA"), joinpath(Sys.STDLIB, "SHA", "src", "SHA.jl"), UInt128(0), true)`,
		`let io = IOBuffer(); write(io, UInt128(5)); take!(io) end`,
	}
	for _, name := range []string{"FileWatching", "Libdl", "Artifacts", "SHA", "Sockets", "LinearAlgebra", "Random"} {
		steps = append(steps, "Base.require(Base, :"+name+")")
	}
	for _, src := range steps {
		v, err := vm.sessionFor(vm.Main).Eval(src)
		t.Logf("probe %s\n  => %s %v", src, showValue(v), err)
		var th *Thrown
		if errors.As(err, &th) {
			t.Logf("  unwound:\n%s", strings.Join(th.Trace, "\n"))
		}
	}
}

// TestBaseProgress includes Base_compiler.jl in Main after Boot, as julia's
// build does, and reports where it stops. It runs for more than half an
// hour, so only when JLGO_BASE=1 asks for it.
func TestBaseProgress(t *testing.T) {
	if os.Getenv("JLGO_BASE") != "1" {
		t.Skip("set JLGO_BASE=1 to load julia's Base (over 30 minutes)")
	}
	vm := New()
	vm.Args = []string{"Base_compiler.jl"}
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	cb := vm.Core.own("Core")
	t.Logf("Core.Core = %+v; Main.usings = %v; world %d", cb, vm.Main.usings, vm.taskWorld)
	for _, src := range []string{"module X1\nCore\nend", "module X2\nusing .Core.Intrinsics\nend", "Core.Main"} {
		v, err := vm.sessionFor(vm.Main).Eval(src)
		t.Logf("%q => %v %v", src, v, err)
	}
	var out strings.Builder
	vm.Stdout = &out
	// julia's build generates its system image: initializers wait
	vm.GeneratingOutput = true
	_, err := vm.jlLoad([]any{vm.Main, "Base_compiler.jl"})
	t.Logf("Base_compiler.jl: %v\nprinted:\n%s", err, out.String())
	if err != nil {
		return
	}
	// the second stage (sysimage.mk:82): sysimg.jl in Main, which finishes
	// Base with Base.jl
	out.Reset()
	vm.Args = []string{"sysimg.jl"}
	_, err = vm.jlLoad([]any{vm.Main, "sysimg.jl"})
	t.Logf("sysimg.jl: %v\nprinted:\n%s", err, out.String())
	var th *Thrown
	if errors.As(err, &th) {
		t.Logf("unwound:\n%s", strings.Join(th.Trace, "\n"))
	}
	if err != nil {
		probeStdlibs(t, vm)
		return
	}
	// julia starting from its image: the modules' __init__, in order
	out.Reset()
	vm.GeneratingOutput = false
	err = vm.RunModuleInitializers()
	t.Logf("module initializers: %v\nprinted:\n%s", err, out.String())
	if errors.As(err, &th) {
		t.Logf("unwound:\n%s", strings.Join(th.Trace, "\n"))
	}
	if err == nil {
		probeBaseTasks(t, vm)
	}
}

const (
	tasksBasePath   = "testdata/tasks_base.jl"
	tasksBaseGolden = "testdata/tasks_base.golden"
)

func loadTasksBase(t testing.TB) []string {
	t.Helper()
	b, err := os.ReadFile(tasksBasePath)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(b), "#---\n")[1:]
}

// TestTasksBaseGolden has julia 1.12.6 answer the programs of
// testdata/tasks_base.jl (JLGO_ORACLE=1 JLGO_UPDATE_TASKS=1) and checks that
// every program has its answer; probeBaseTasks runs them on the VM that
// loaded Base.
func TestTasksBaseGolden(t *testing.T) {
	progs := loadTasksBase(t)
	if os.Getenv("JLGO_ORACLE") != "" && os.Getenv("JLGO_UPDATE_TASKS") != "" {
		p, err := oracle.Start()
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		out, err := os.Create(tasksBaseGolden)
		if err != nil {
			t.Fatal(err)
		}
		defer out.Close()
		enc := json.NewEncoder(out)
		enc.SetEscapeHTML(false)
		for i, src := range progs {
			ans, err := p.Call("eval", src)
			if err != nil {
				t.Fatalf("julia rejects program %d:\n%s\n%v", i+1, src, err)
			}
			_, shown, ok := strings.Cut(ans, "\n=> ")
			if !ok {
				t.Fatalf("program %d: julia answered %q", i+1, ans)
			}
			if err := enc.Encode(evalCase{Src: src, Want: shown}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := len(readTasksBaseGolden(t)); got != len(progs) {
		t.Fatalf("%d golden answers for %d programs: regenerate with JLGO_ORACLE=1 JLGO_UPDATE_TASKS=1", got, len(progs))
	}
}

func readTasksBaseGolden(t testing.TB) []evalCase {
	t.Helper()
	f, err := os.Open(tasksBaseGolden)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var cases []evalCase
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var c evalCase
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return cases
}

// probeBaseTasks runs julia's own Base through tasks, the scheduler and the
// event loop: each program of tasks_base.jl must give julia's answer. A
// program that waits for ever is refused with ErrDeadlock, which is reported
// (julia would hang).
func probeBaseTasks(t *testing.T, vm *VM) {
	defer vm.Close()
	s := vm.NewSession()
	for i, c := range readTasksBaseGolden(t) {
		start := time.Now()
		v, err := s.Eval(c.Src)
		got := "<error> " + fmt.Sprint(err)
		if err == nil {
			got = Repr(v)
		}
		verdict := "ok"
		if got != c.Want {
			verdict = "DIFFERS"
			t.Errorf("tasks program %d:\n%s\nGo:    %s\njulia: %s", i+1, c.Src, got, c.Want)
		}
		t.Logf("tasks program %d: %s in %v", i+1, verdict, time.Since(start))
	}
}
