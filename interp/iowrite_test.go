package interp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// a writer that takes n bytes, then fails
type shortWriter struct {
	n   int
	err error
}

func (w *shortWriter) Write(p []byte) (int, error) {
	if len(p) <= w.n {
		w.n -= len(p)
		return len(p), nil
	}
	n := w.n
	w.n = 0
	return n, w.err
}

// a writer that writes nothing and says nothing
type mute struct{}

func (mute) Write([]byte) (int, error) { return 0, nil }

func (f *pcreFixture) fdStream(fd int32) *Pointer {
	f.t.Helper()
	s := f.newIOS()
	v, err := f.sh("ios_fd", s, int64(fd), int32(0), int32(0))
	if err != nil || v.(*Pointer).Target != s.Target {
		f.t.Fatalf("ios_fd %d: %v %v", fd, showValue(v), err)
	}
	return s
}

func (f *pcreFixture) write(s *Pointer, text string) uint64 {
	f.t.Helper()
	return f.i("ios_write", s, f.str(text), uint64(len(text))).(uint64)
}

// fdio(1) written as julia 1.12.6 writes it with its standard output
// redirected to a file (oracle: pkgx julia a.jl > out.txt; the file's size
// after each call): "abc" stays in the buffer, "d\ne" flushes up to its
// newline, putc of 'X' buffers and of '\n' flushes, flush(s), 40000 bytes go
// direct past a 32768-byte buffer, 30000 more are buffered, 20000 more flush
// those and are buffered, close flushes; nothing is written to a closed one.
func TestIOSWriteStdout(t *testing.T) {
	f := newPCREFixture(t)
	var out bytes.Buffer
	f.vm.Stdout = &out
	s := f.fdStream(1)
	if f.i("ios_isopen", s) != any(int32(1)) || f.i("ios_get_readable", s) != any(int32(1)) ||
		f.i("ios_get_writable", s) != any(int32(1)) || f.i("jl_ios_fd", s) != any(int64(1)) {
		t.Error("flags of fdio(1): julia says readable, writable, open, fd 1")
	}
	step := func(what string, want int) {
		t.Helper()
		if out.Len() != want {
			t.Errorf("%s: %d bytes written, julia 1.12.6 wrote %d", what, out.Len(), want)
		}
	}
	if f.write(s, "abc") != 3 {
		t.Error("write count")
	}
	step("abc", 0)
	f.write(s, "d\ne")
	step("d\\ne", 5)
	if out.String() != "abcd\n" {
		t.Errorf("content %q", out.String())
	}
	if f.i("ios_putc", int32('X'), s) != any(int32(1)) {
		t.Error("putc")
	}
	step("putc X", 5)
	f.i("ios_putc", int32('\n'), s)
	step("putc newline", 8)
	if out.String() != "abcd\neX\n" {
		t.Errorf("content %q", out.String())
	}
	f.write(s, "zz")
	if f.i("ios_flush", s) != any(int32(0)) {
		t.Error("flush")
	}
	step("flush", 10)
	f.write(s, strings.Repeat("q", 40000))
	step("40000 bytes", 40010)
	f.write(s, strings.Repeat("r", 30000))
	step("30000 bytes more", 40010)
	f.write(s, strings.Repeat("s", 20000))
	step("20000 bytes more", 70010)
	f.write(s, strings.Repeat("t", 100))
	step("100 bytes more", 70010)
	if f.i("ios_close", s) != any(int32(0)) || f.i("ios_isopen", s) != any(int32(0)) {
		t.Error("close")
	}
	step("close", 90110)
	if f.i("ios_putc", int32('X'), s) != any(int32(0)) || f.write(s, "ab") != 0 || f.i("ios_flush", s) != any(int32(0)) {
		t.Error("a closed stream writes nothing")
	}
	step("closed", 90110)
	if got := out.Bytes()[10:]; len(got) != 90100 || got[0] != 'q' || got[40000] != 'r' || got[70000] != 's' || got[90099] != 't' {
		t.Error("the bytes arrive in order")
	}
	// a stream of descriptor 3 is block buffered: a newline does not flush it
	// (the table has no descriptor 3 to take the bytes)
	b := f.fdStream(3)
	f.write(b, "x\n")
	if st, _ := f.vm.iosOf("t", b); st.ndirty != 2 || st.bm != bmBlock {
		t.Errorf("descriptor 3: %+v", st)
	}
}

// fdio(2) is unbuffered: every write is a write(2), standard input and a
// descriptor of the tree refuse writes
func TestIOSWriteStderrAndRefusals(t *testing.T) {
	f := newPCREFixture(t)
	var out, errb bytes.Buffer
	f.vm.Stdout, f.vm.Stderr = &out, &errb
	e := f.fdStream(2)
	f.write(e, "to stderr\n")
	f.i("ios_putc", int32('!'), e)
	if errb.String() != "to stderr\n!" || out.Len() != 0 {
		t.Errorf("stderr %q stdout %q", errb.String(), out.String())
	}
	// stdin is not a descriptor to write: the bytes are lost at the flush
	in := f.fdStream(0)
	if f.write(in, "no") != 2 || f.i("ios_flush", in) != any(uvEBADF) {
		t.Error("write to descriptor 0")
	}
	// a descriptor the table does not hold
	gone := f.fdStream(7)
	f.write(gone, "no")
	if f.i("ios_flush", gone) != any(uvEBADF) {
		t.Error("flush to a missing descriptor")
	}
	// nothing to write, and a stream not open for writing
	if f.write(e, "") != 0 || f.write(f.iosOpen(f.hostFile([]byte("x"))), "a") != 0 {
		t.Error("write of nothing or to a reader")
	}
	// a nil Stdout swallows
	f.vm.Stdout = nil
	o := f.fdStream(1)
	f.write(o, "gone\n")
	// a stream whose descriptor was closed keeps its bytes unwritten
	o2 := f.fdStream(1)
	f.write(o2, "x")
	st, _ := f.vm.iosOf("t", o2)
	st.fd = -1
	if f.i("ios_flush", o2) != any(int32(-1)) {
		t.Error("flush without a descriptor")
	}
	// bad arguments
	for _, c := range []struct {
		name string
		args []any
	}{
		{"ios_fd", []any{o, int64(-1), int32(0), int32(0)}},
		{"ios_fd", []any{o, int64(1) << 40, int32(0), int32(0)}},
		{"ios_fd", []any{o, "x", int32(0), int32(0)}},
		{"ios_fd", []any{o, int64(1), int32(0), "x"}},
		{"ios_fd", []any{int64(1), int64(1), int32(0), int32(0)}},
		{"ios_write", []any{o, int64(1), uint64(1)}},
		{"ios_write", []any{int64(1), f.str("a"), uint64(1)}},
		{"ios_putc", []any{"x", o}},
		{"ios_putc", []any{int32(1), int64(1)}},
		{"ios_flush", []any{int64(1)}},
	} {
		if _, err := f.sh(c.name, c.args...); err == nil {
			t.Errorf("%s of %v: no error", c.name, c.args)
		}
	}
}

// errors of the descriptor reach flush, and a writer that stops short
// leaves the stream knowing it took fewer bytes
func TestIOSWriteErrors(t *testing.T) {
	f := newPCREFixture(t)
	boom := errors.New("boom")
	f.vm.Stdout = &shortWriter{n: 3, err: boom}
	s := f.fdStream(1)
	f.write(s, "abcdef")
	if got := f.i("ios_flush", s); got != any(uvEIO) {
		t.Errorf("flush after a write error: %v", got)
	}
	// the unbuffered one reports what was written
	f.vm.Stderr = &shortWriter{n: 2, err: boom}
	e := f.fdStream(2)
	if f.write(e, "abcdef") != 2 {
		t.Error("stderr partial write")
	}
	// a writer that writes nothing and fails nothing is an I/O error
	f.vm.Stdout = mute{}
	m := f.fdStream(1)
	f.write(m, "x")
	if got := f.i("ios_flush", m); got != any(uvEIO) {
		t.Errorf("flush to a mute writer: %v", got)
	}
}

// a stream with a known position: the buffer's bytes count, as ios.c:534-552
// and 496-532 say
func TestIOSWritePositions(t *testing.T) {
	f := newPCREFixture(t)
	var out bytes.Buffer
	f.vm.Stdout = &out
	s := f.fdStream(1)
	if f.i("ios_pos", s) != any(int64(-1)) { // a descriptor that cannot seek
		t.Error("ios_pos of a pipe-like descriptor")
	}
	f.write(s, "abc")
	st, _ := f.vm.iosOf("t", s)
	st.fpos = 100
	if f.i("ios_pos", s) != any(int64(103)) {
		t.Error("ios_pos of a stream being written")
	}
	// a skip past the buffer flushes, then asks the descriptor
	if f.i("ios_skip", s, int64(50000)) != any(int64(-1)) || out.String() != "abc" {
		t.Errorf("ios_skip: %q", out.String())
	}
	f.write(s, "def")
	if f.i("ios_seek_end", s) != any(int64(-1)) || f.i("ios_seek", s, int64(0)) != any(int64(-1)) {
		t.Error("seeking a descriptor that cannot")
	}
	// the buffer moved back inside it: the flush asks where the stream was
	f.write(s, "xyz")
	st.bpos = 1
	f.i("ios_flush", s)
	if st.bpos != 0 || len(st.buf) != 0 || st.ndirty != 0 {
		t.Errorf("flush state %+v", st)
	}
}

// reading a stream written to: the dirty bytes go first (ios.c:269-272,
// 345-348); standard output cannot be read
func TestIOSWriteThenRead(t *testing.T) {
	f := newPCREFixture(t)
	var out bytes.Buffer
	f.vm.Stdout = &out
	s := f.fdStream(1)
	f.write(s, "ab")
	if f.i("ios_getc", s) != any(int32(-1)) || out.String() != "ab" {
		t.Errorf("getc after write: %q", out.String())
	}
	s = f.fdStream(1)
	f.write(s, "cd")
	if f.i("ios_peekc", s) != any(int32(-1)) || out.String() != "abcd" {
		t.Errorf("peekc after write: %q", out.String())
	}
	// and writing after reading seeks to where the reading stood
	r := f.fdStream(0)
	f.vm.Stdin = strings.NewReader("hello")
	if f.i("ios_getc", r) != any(int32('h')) {
		t.Error("getc")
	}
	f.write(r, "x") // ios_seek(ios_pos): the pipe-like descriptor cannot seek
	if st, _ := f.vm.iosOf("t", r); !st.writing || st.reading {
		t.Errorf("state after write: %+v", st)
	}
}

// fdio(0) reads VM.Stdin; nil is an empty input
func TestIOSReadStdin(t *testing.T) {
	f := newPCREFixture(t)
	f.vm.Stdin = strings.NewReader("hi\nyo")
	s := f.fdStream(0)
	dst, m := f.buf(5, 0)
	if f.i("ios_readall", s, dst, uint64(5)) != any(uint64(5)) || string(memBytes(m)) != "hi\nyo" {
		t.Error("read of standard input")
	}
	if f.i("ios_getc", s) != any(int32(-1)) || f.i("ios_eof_blocking", s) != any(int32(1)) {
		t.Error("end of standard input")
	}
	f.vm.Stdin = nil
	e := f.fdStream(0)
	if f.i("ios_getc", e) != any(int32(-1)) {
		t.Error("empty standard input")
	}
	// reading a descriptor of output is EBADF: end of input
	o := f.fdStream(1)
	if f.i("ios_getc", o) != any(int32(-1)) {
		t.Error("read of standard output")
	}
}

// jl_stdin_stream, jl_stdout_stream and jl_stderr_stream answer handles that
// are files of descriptors 0, 1, 2 (julia 1.12.6, streams redirected to
// files: ccall(:jl_uv_handle_type, Int32, (Ptr{Cvoid},), h) == Base.UV_FILE ==
// 17, fd(stdout) == RawFD(1))
func TestStdHandles(t *testing.T) {
	f := newPCREFixture(t)
	for fd, name := range []string{"jl_stdin_stream", "jl_stdout_stream", "jl_stderr_stream"} {
		h := f.i(name)
		if f.i("jl_uv_handle_type", h) != any(int32(17)) || f.i("jl_uv_file_handle", h) != any(int32(fd)) {
			t.Errorf("%s: handle type or descriptor", name)
		}
		if f.i(name) != h {
			t.Errorf("%s: a new handle each time", name)
		}
	}
	for _, name := range []string{"jl_uv_handle_type", "jl_uv_file_handle"} {
		for _, bad := range []any{int64(1), f.null, f.i("jl_stdout_stream").(*Pointer).Target, &Pointer{Type: f.null.Type, Target: f.vm.cglobals["jl_uv_stdout"]}} {
			if _, err := f.sh(name, bad); err == nil {
				t.Errorf("%s of %v: no error", name, bad)
			}
		}
	}
	h := f.i("jl_stdout_stream").(*Pointer)
	if _, err := f.sh("jl_uv_file_handle", &Pointer{Type: h.Type, Target: h.Target, Off: 8}); err == nil {
		t.Error("jl_uv_file_handle of an offset")
	}
}

// the VM's descriptors 0, 1 and 2
func TestVFSStdio(t *testing.T) {
	vm := New()
	var out bytes.Buffer
	vm.Stdout = &out
	v := vm.fsys()
	if n, code := v.write(1, []byte("ab")); n != 2 || code != 0 || out.String() != "ab" {
		t.Errorf("write to 1: %d %d %q", n, code, out.String())
	}
	if n, code := v.write(0, []byte("ab")); n != 0 || code != uvEBADF {
		t.Errorf("write to 0: %d %d", n, code)
	}
	if n, code := v.write(9, []byte("ab")); n != 0 || code != uvEBADF {
		t.Errorf("write to a closed descriptor: %d %d", n, code)
	}
	if b, code := v.read(1, 4); len(b) != 0 || code != uvEBADF {
		t.Errorf("read of 1: %q %d", b, code)
	}
	vm.Stdin = strings.NewReader("in")
	if b, code := v.read(0, 4); string(b) != "in" || code != 0 {
		t.Errorf("read of 0: %q %d", b, code)
	}
	if b, code := v.read(0, 4); len(b) != 0 || code != 0 {
		t.Errorf("read of 0 at the end: %q %d", b, code)
	}
	if _, code := v.seek(2, 0, 0); code != uvESPIPE {
		t.Errorf("seek of 2: %d", code)
	}
	for fd, name := range []string{"stdin", "stdout", "stderr"} {
		st, code := v.fstat(int32(fd))
		if code != 0 || st.mode != 0o100644 || st.size != 0 {
			t.Errorf("fstat of %s: %o %d %d", name, st.mode, st.size, code)
		}
	}
	if fi, _ := v.files[1].stat(); fi.Mode() != 0o644 || fi.IsDir() {
		t.Error("stat of 1")
	}
	// a descriptor opened later is 3, and the standard ones can be closed
	if code := v.close(2); code != 0 || v.files[2] != nil {
		t.Error("close of 2")
	}
	if code := v.close(2); code != uvEBADF {
		t.Error("close of 2 twice")
	}
}
