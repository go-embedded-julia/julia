package oracle

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// live enables the tests that need the reference julia: JLGO_ORACLE=1.
var live = os.Getenv("JLGO_ORACLE") != ""

// The fake oracle: when the test binary is started with JLGO_FAKE_ORACLE set,
// it speaks the protocol itself, so the client is tested without julia.
func TestMain(m *testing.M) {
	if mode := os.Getenv("JLGO_FAKE_ORACLE"); mode != "" {
		fakeOracle(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func enc(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func fakeOracle(mode string) {
	switch mode {
	case "silent":
		return
	case "badgreeting":
		fmt.Println("HELLO")
		return
	case "oldjulia":
		fmt.Println("READY\t" + enc("1.10.0"))
		return
	}
	fmt.Println("READY\t" + enc(Version))
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		parts := strings.Split(in.Text(), "\t")
		var args []string
		for _, a := range parts[1:] {
			b, _ := base64.StdEncoding.DecodeString(a)
			args = append(args, string(b))
		}
		switch parts[0] {
		case "echo", "parseall":
			fmt.Println("OK\t" + enc(strings.Join(args, "|")))
		case "fail":
			fmt.Println("ERR\t" + enc("boom"))
		case "badtag":
			fmt.Println("WHAT\t" + enc("x"))
		case "nosep":
			fmt.Println("OK")
		case "badb64":
			fmt.Println("OK\t!!!")
		case "die":
			os.Exit(3)
		}
	}
}

func withFake(t *testing.T, mode string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("JLGO_JULIA", exe)
	t.Setenv("JLGO_FAKE_ORACLE", mode)
}

func TestCommandDefaultsToPinnedPkgx(t *testing.T) {
	t.Setenv("JLGO_JULIA", "")
	got := strings.Join(Command(), " ")
	if got != "pkgx julia@"+Version {
		t.Fatalf("Command() = %q", got)
	}
	if !strings.Contains(Script(), "serialize_expr") {
		t.Fatal("embedded script is not the oracle")
	}
}

func TestCallRoundTrip(t *testing.T) {
	withFake(t, "ok")
	p, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	if p.JuliaVersion() != Version {
		t.Fatalf("version %q", p.JuliaVersion())
	}
	got, err := p.Call("echo", "a\tb\nc", "\xff")
	if err != nil || got != "a\tb\nc|\xff" {
		t.Fatalf("echo = %q, %v", got, err)
	}
	if got, err := p.ParseAll("x", "f.jl"); err != nil || got != "x|f.jl" {
		t.Fatalf("parseall = %q, %v", got, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestParseAllFraming(t *testing.T) {
	withFake(t, "ok")
	p, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Call("fail"); !errors.Is(err, ErrJulia) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("fail: %v", err)
	}
	if _, err := p.Call("badtag"); err == nil || !strings.Contains(err.Error(), "unexpected answer tag") {
		t.Fatalf("badtag: %v", err)
	}
	if _, err := p.Call("nosep"); err == nil || !strings.Contains(err.Error(), "malformed line") {
		t.Fatalf("nosep: %v", err)
	}
	if _, err := p.Call("badb64"); err == nil || !strings.Contains(err.Error(), "malformed payload") {
		t.Fatalf("badb64: %v", err)
	}
	if _, err := p.Call("die"); err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("die: %v", err)
	}
	if _, err := p.Call("echo"); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("after death: %v", err)
	}
}

func TestStartFailures(t *testing.T) {
	for _, tc := range []struct{ mode, want string }{
		{"silent", "no READY line"},
		{"badgreeting", "unexpected greeting"},
		{"oldjulia", "the pinned release is"},
	} {
		withFake(t, tc.mode)
		if _, err := Start(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.mode, err)
		}
	}
	t.Setenv("JLGO_JULIA", "/nonexistent/julia")
	if _, err := Start(); err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("missing binary: %v", err)
	}
}

// TestLive asks the real reference julia a few questions whose answers are
// known, which proves the oracle script and the serialiser framing.
func TestLive(t *testing.T) {
	if !live {
		t.Skip("needs JLGO_ORACLE=1")
	}
	p, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, err := p.ParseAll("x = 1 + 0x02\n'a'", "f.jl")
	if err != nil {
		t.Fatal(err)
	}
	want := `(:toplevel {L 1 :"f.jl"} (:"=" :x (:call :"+" 1 {UInt8 2})) {L 2 :"f.jl"} {Char 0x61000000})`
	if got != want {
		t.Fatalf("parseall:\n got %s\nwant %s", got, want)
	}
	v, err := p.Call("version")
	if err != nil || v != Version+" "+Commit {
		t.Fatalf("version = %q, %v", v, err)
	}
}
