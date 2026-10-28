package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
	"github.com/go-embedded-julia/julia/lower"
)

// When started with JLGO_FAKE_JULIA set, the test binary is a minimal
// oracle process: it greets with the pinned version and answers every
// request with "ok".
func TestMain(m *testing.M) {
	if os.Getenv("JLGO_FAKE_JULIA") != "" {
		enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		fmt.Println("READY\t" + enc(oracle.Version))
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			fmt.Println("OK\t" + enc("ok"))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// fakeOracle answers like the julia oracle, from canned data.
type fakeOracle struct{ fail string }

var errBoom = errors.New("boom")

func (f fakeOracle) Call(op string, args ...string) (string, error) {
	if op == f.fail {
		return "", errBoom
	}
	switch op {
	case "unicode":
		var b strings.Builder
		b.WriteString("unicode 16.0.0\n")
		for _, p := range []string{"idstart", "idchar", "cat", "space", "bound", "width"} {
			b.WriteString("prop " + p + "\n0 1114111 1\n")
		}
		return b.String(), nil
	case "normalize":
		return "181 956\n", nil
	case "graphemebreak":
		return strings.TrimSuffix(strings.Repeat("1,", strings.Count(args[0], ",")), ","), nil
	case "snippets":
		return base64.StdEncoding.EncodeToString([]byte("x")), nil
	case "corpus":
		return "base/a.jl", nil
	case "readfile":
		return "x = 1\n", nil
	// What the Go side computes, where it exists, so that parity is counted.
	case "tokens":
		if goSide.Tokens != nil {
			return goSide.Tokens([]byte(args[0])), nil
		}
	case "green":
		if goSide.Green != nil {
			return goSide.Green([]byte(args[0])), nil
		}
	case "parsefile":
		if goSide.Expr != nil {
			return goSide.Expr([]byte("x = 1\n"), args[0]), nil
		}
	case "benchparse":
		return "2000000 6", nil
	case "lowerforms", "lowerfile":
		enc := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
		rec := func(tag, in, out string) string { return tag + "\t" + enc(in) + "\t" + enc(out) + "\n" }
		return rec("F", ":x", ":x") + rec("F", "(:call :f)", "(:thunk)") +
			rec("E", ":y", "UndefVarError") + rec("L", ":z", "boom"), nil
	}
	return op, nil
}

func withFake(t *testing.T, f fakeOracle) {
	t.Helper()
	saved := startOracle
	t.Cleanup(func() { startOracle = saved })
	startOracle = func() (gen.Oracle, func() error, error) {
		return f, func() error { return nil }, nil
	}
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := RootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func scaffold(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "testdata", "programs"), 0o755)
	os.WriteFile(filepath.Join(root, "testdata", "snippets-extra.jl"), []byte("#----\ny\n"), 0o644)
	os.WriteFile(filepath.Join(root, "testdata", "lowering-snippets.jl"), []byte("x\n#----\nf()\n"), 0o644)
	os.WriteFile(filepath.Join(root, "testdata", "programs", "p.jl"), []byte("1 + 1\n"), 0o644)
	os.WriteFile(filepath.Join(root, "testdata", "programs", "README"), []byte("not a program"), 0o644)
	return root
}

func TestRegen(t *testing.T) {
	withFake(t, fakeOracle{})
	root := scaffold(t)
	out, err := run(t, "regen", "-C", root)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, f := range []string{"internal/unitab/tables_gen.go", "internal/unitab/testdata/normalize.txt",
		"internal/unitab/testdata/graphemebreak.txt", "testdata/golden/snippets.jsonl",
		"testdata/golden/corpus.jsonl", "testdata/golden/programs.jsonl",
		"testdata/golden/lowering-snippets.jsonl", "testdata/golden/lowering.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil {
			t.Errorf("%s not written: %v", f, err)
		}
	}
	// The fake answers what the Go side computes, at the levels it has.
	n := func(f any) int {
		if f == nil {
			return 0
		}
		return 1
	}
	var tok, green, ex any
	if goSide.Tokens != nil {
		tok = goSide.Tokens
	}
	if goSide.Green != nil {
		green = goSide.Green
	}
	if goSide.Expr != nil {
		ex = goSide.Expr
	}
	want := fmt.Sprintf("corpus parity: tokens %d/1, green trees %d/1, Expr %d/1 files", n(tok), n(green), n(ex))
	if !strings.Contains(out, want) {
		t.Errorf("output:\n%s\nwant %s", out, want)
	}
	// :x lowers to itself; the call is not ported yet.
	if want := "lowering parity: 0/1 files, 1/2 forms (1 forms not expandable, 1 lowering errors in julia)"; !strings.Contains(out, want) {
		t.Errorf("output:\n%s\nwant %s", out, want)
	}
}

func TestLoweringParityCount(t *testing.T) {
	saved := loweringSide
	defer func() { loweringSide = saved }()
	loweringSide.Lower = func(forms []gen.LoweringForm) []bool {
		out := make([]bool, len(forms))
		for i := range out {
			out[i] = true
		}
		return out
	}
	withFake(t, fakeOracle{})
	out, err := run(t, "lowering-corpus", "-C", scaffold(t))
	if err != nil || !strings.Contains(out, "lowering parity: 1/1 files, 2/2 forms") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestLowerForms(t *testing.T) {
	got := lowerForms([]gen.LoweringForm{
		{Tag: "F", Input: ":x", Output: ":x"},
		{Tag: "F", Input: "(bad", Output: ":x"},
		{Tag: "F", Input: ":x", Output: "(bad"},
		{Tag: "F", Input: ":x", Output: ":y"},
		// beyond lower.MaxDepth: Lower refuses it
		{Tag: "F", Input: strings.Repeat("(:call :f ", lower.MaxDepth+2) + ":x" + strings.Repeat(")", lower.MaxDepth+2), Output: ":x"},
	})
	if fmt.Sprint(got) != "[true false false false false]" {
		t.Fatalf("lowerForms: %v", got)
	}
}

func TestStepFailures(t *testing.T) {
	for _, tc := range []struct{ step, fail string }{
		{"unicode", "unicode"}, {"unicode", "normalize"}, {"unicode", "graphemebreak"},
		{"snippets", "snippets"}, {"corpus", "corpus"}, {"programs", "lower"},
		{"lowering-snippets", "lowerforms"}, {"lowering-corpus", "lowerfile"},
	} {
		withFake(t, fakeOracle{fail: tc.fail})
		if _, err := run(t, tc.step, "-C", scaffold(t)); err == nil {
			t.Errorf("%s with %s failing succeeded", tc.step, tc.fail)
		}
	}
	// Unwritable outputs and missing inputs.
	withFake(t, fakeOracle{})
	ro := scaffold(t)
	os.WriteFile(filepath.Join(ro, "internal"), nil, 0o644) // a file where a directory must go
	if _, err := run(t, "unicode", "-C", ro); err == nil {
		t.Error("unicode into an unwritable tree succeeded")
	}
	ro2 := scaffold(t)
	os.MkdirAll(filepath.Join(ro2, "internal", "unitab", "testdata", "normalize.txt"), 0o755)
	if _, err := run(t, "unicode", "-C", ro2); err == nil {
		t.Error("normalize.txt over a directory succeeded")
	}
	ro3 := scaffold(t)
	os.MkdirAll(filepath.Join(ro3, "internal", "unitab", "testdata", "graphemebreak.txt"), 0o755)
	if _, err := run(t, "unicode", "-C", ro3); err == nil {
		t.Error("graphemebreak.txt over a directory succeeded")
	}
	empty := t.TempDir()
	if _, err := run(t, "snippets", "-C", empty); err == nil {
		t.Error("snippets without the extra file succeeded")
	}
	if _, err := run(t, "programs", "-C", empty); err == nil {
		t.Error("programs without the programs directory succeeded")
	}
	if _, err := run(t, "lowering-snippets", "-C", empty); err == nil {
		t.Error("lowering-snippets without the snippet file succeeded")
	}
	unreadable := scaffold(t)
	os.MkdirAll(filepath.Join(unreadable, "testdata", "programs", "dir.jl"), 0o755)
	if _, err := run(t, "programs", "-C", unreadable); err == nil {
		t.Error("a directory named .jl was read as a program")
	}
	saved := startOracle
	startOracle = func() (gen.Oracle, func() error, error) { return nil, nil, errBoom }
	defer func() { startOracle = saved }()
	if _, err := run(t, "regen"); !errors.Is(err, errBoom) {
		t.Errorf("oracle start failure: %v", err)
	}
}

func TestCorpusParityCount(t *testing.T) {
	saved := goSide
	defer func() { goSide = saved }()
	goSide = gen.GoSide{
		Tokens: func([]byte) string { return "t" },
		Green:  func([]byte) string { return "g" },
		Expr:   func([]byte, string) string { return "e" },
	}
	withFake(t, fakeOracle{})
	out, err := run(t, "corpus", "-C", scaffold(t))
	if err != nil || !strings.Contains(out, "tokens 1/1, green trees 1/1, Expr 1/1 files") {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestExtraCommands(t *testing.T) {
	saved := extraCommands
	defer func() { extraCommands = saved }()
	extraCommands = append(extraCommands, func() *cobra.Command { return &cobra.Command{Use: "probe"} })
	if c, _, err := RootCmd().Find([]string{"probe"}); err != nil || c.Use != "probe" {
		t.Fatalf("extra command not registered: %v", err)
	}
}

func TestDefaultStartOracle(t *testing.T) {
	exe, _ := os.Executable()
	t.Setenv("JLGO_JULIA", exe)
	t.Setenv("JLGO_FAKE_JULIA", "1")
	o, closer, err := startOracle()
	if err != nil {
		t.Fatal(err)
	}
	if v, err := o.Call("anything"); err != nil || v != "ok" {
		t.Fatalf("%q %v", v, err)
	}
	if err := closer(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JLGO_JULIA", "/nonexistent/julia")
	if _, _, err := startOracle(); err == nil {
		t.Fatal("missing julia accepted")
	}
}

func TestMainExits(t *testing.T) {
	saved := osExit
	defer func() { osExit = saved }()
	code := -1
	osExit = func(c int) { code = c }
	os.Args = []string{"jloracle", "no-such-command"}
	main()
	if code != 1 {
		t.Fatalf("exit code %d", code)
	}
	code = -1
	os.Args = []string{"jloracle", "--help"}
	main()
	if code != -1 {
		t.Fatalf("help exited with %d", code)
	}
}
