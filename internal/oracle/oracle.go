// Package oracle drives the reference Julia as a differential oracle.
//
// One julia process is started per session and kept alive; requests and
// answers are single lines (see oracle.jl for the framing). Everything the
// test suite and the golden files know about "what Julia does" comes through
// here, from the release pinned in [Version].
package oracle

import (
	"bufio"
	_ "embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Version is the pinned reference release. It is a stable release (the plan
// asks for one), matching the JuliaSyntax sources this parser is a port of.
const Version = "1.12.6"

// Commit is the git commit of the pinned release, as reported by
// Base.GIT_VERSION_INFO.commit.
const Commit = "15346901f0039751c5488744f1f62de7d87510a8"

//go:embed oracle.jl
var script string

// Script returns the Julia source of the oracle.
func Script() string { return script }

// Command returns the program and arguments that run the reference julia.
// $JLGO_JULIA, when set, is the julia executable; otherwise the pinned
// release is run through pkgx.
func Command() []string {
	if j := os.Getenv("JLGO_JULIA"); j != "" {
		return []string{j}
	}
	return []string{"pkgx", "julia@" + Version}
}

// Process is a running oracle.
type Process struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     *bufio.Reader
	version string
}

// ErrJulia is wrapped by every error that julia itself reported in answer to
// a request (as opposed to a failure of the process or the protocol).
var ErrJulia = errors.New("julia error")

// Start launches the oracle and checks that it is the pinned release.
func Start() (*Process, error) {
	argv := append(Command(), "--startup-file=no", "--history-file=no", "--color=no", "-e", script)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stderr = os.Stderr
	// StdinPipe and StdoutPipe fail only when the stream is already set or
	// the process has started, neither of which is the case here.
	in, _ := cmd.StdinPipe()
	out, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("oracle: starting %q: %w", argv[0], err)
	}
	p := &Process{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}
	line, err := p.out.ReadString('\n')
	if err != nil {
		p.Close()
		return nil, fmt.Errorf("oracle: no READY line: %w", err)
	}
	tag, payload, err := split(line)
	if err != nil || tag != "READY" {
		p.Close()
		return nil, fmt.Errorf("oracle: unexpected greeting %q", line)
	}
	p.version = payload
	if payload != Version {
		p.Close()
		return nil, fmt.Errorf("oracle: julia %s is running, the pinned release is %s", payload, Version)
	}
	return p, nil
}

// JuliaVersion is the VERSION the running julia reported.
func (p *Process) JuliaVersion() string { return p.version }

func split(line string) (tag, payload string, err error) {
	line = strings.TrimRight(line, "\r\n")
	tag, b64, ok := strings.Cut(line, "\t")
	if !ok {
		return "", "", fmt.Errorf("oracle: malformed line %q", line)
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", "", fmt.Errorf("oracle: malformed payload: %w", err)
	}
	return tag, string(raw), nil
}

// Call sends one request. Each argument is base64-encoded on the wire.
func (p *Process) Call(op string, args ...string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var b strings.Builder
	b.WriteString(op)
	for _, a := range args {
		b.WriteByte('\t')
		b.WriteString(base64.StdEncoding.EncodeToString([]byte(a)))
	}
	b.WriteByte('\n')
	if _, err := io.WriteString(p.in, b.String()); err != nil {
		return "", fmt.Errorf("oracle: write: %w", err)
	}
	line, err := p.out.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("oracle: read: %w", err)
	}
	tag, payload, err := split(line)
	if err != nil {
		return "", err
	}
	switch tag {
	case "OK":
		return payload, nil
	case "ERR":
		return "", fmt.Errorf("%w: %s", ErrJulia, payload)
	}
	return "", fmt.Errorf("oracle: unexpected answer tag %q", tag)
}

// Close stops the oracle.
func (p *Process) Close() error {
	p.in.Close()
	return p.cmd.Wait()
}

// ParseAll answers Meta.parseall(text; filename) serialised with
// expr.Serialize's format.
func (p *Process) ParseAll(text, filename string) (string, error) {
	return p.Call("parseall", text, filename)
}
