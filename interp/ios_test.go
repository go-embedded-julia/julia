package interp

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// an ios_t as Base allocates it: sizeof(ios_t) zero bytes
func (f *pcreFixture) newIOS() *Pointer {
	m := &Memory{Type: memUInt8Type, Elems: make([]any, 184)}
	for i := range m.Elems {
		m.Elems[i] = uint8(0)
	}
	return &Pointer{Type: f.u8p, Target: m}
}

func (f *pcreFixture) iosOpen(name string) *Pointer {
	f.t.Helper()
	s := f.newIOS()
	v, err := f.sh("ios_file", s, f.str(name), int32(1), int32(0), int32(0), int32(0))
	if err != nil || v.(*Pointer).Target == nil {
		f.t.Fatalf("ios_file %s: %v %v", name, showValue(v), err)
	}
	return s
}

func (f *pcreFixture) i(name string, args ...any) any {
	f.t.Helper()
	v, err := f.sh(name, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (f *pcreFixture) hostFile(content []byte) string {
	f.t.Helper()
	f.vm.Host = true
	p := filepath.Join(f.t.TempDir(), "f.txt")
	if err := os.WriteFile(p, content, 0o644); err != nil {
		f.t.Fatal(err)
	}
	return p
}

// A 5-byte file read as julia 1.12.6 reads it (oracle: pos 0, size 5, eof
// false, 5 bytes available once eof looked; getc 104 then pos 1 and 4
// available, peek 101; seek 2 then getc 108; skip -2 from 3 to 1; seekend
// then eof and pos 5; closed).
func TestIOSSmallFile(t *testing.T) {
	f := newPCREFixture(t)
	s := f.iosOpen(f.hostFile([]byte("hello")))
	if f.i("ios_isopen", s) != any(int32(1)) || f.i("ios_get_readable", s) != any(int32(1)) || f.i("ios_get_writable", s) != any(int32(0)) {
		t.Error("flags")
	}
	if f.i("ios_pos", s) != any(int64(0)) || f.i("ios_filesize", s) != any(int64(5)) {
		t.Error("pos and size")
	}
	if f.i("ios_eof_blocking", s) != any(int32(0)) || f.i("jl_nb_available", s) != any(int32(5)) {
		t.Error("eof then available")
	}
	if f.i("ios_getc", s) != any(int32(104)) || f.i("ios_pos", s) != any(int64(1)) || f.i("jl_nb_available", s) != any(int32(4)) || f.i("ios_peekc", s) != any(int32(101)) {
		t.Error("getc, pos, available, peek")
	}
	if f.i("ios_seek", s, int64(2)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(2)) || f.i("ios_getc", s) != any(int32(108)) {
		t.Error("seek 2")
	}
	if f.i("ios_pos", s) != any(int64(3)) || f.i("ios_skip", s, int64(-2)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(1)) || f.i("ios_getc", s) != any(int32(101)) {
		t.Error("skip -2")
	}
	if f.i("ios_seek_end", s) != any(int64(0)) || f.i("ios_eof_blocking", s) != any(int32(1)) || f.i("ios_pos", s) != any(int64(5)) {
		t.Error("seek end")
	}
	if f.i("ios_getc", s) != any(int32(-1)) || f.i("ios_peekc", s) != any(int32(-1)) {
		t.Error("getc and peekc at the end")
	}
	if f.i("ios_close", s) != any(int32(0)) || f.i("ios_isopen", s) != any(int32(0)) {
		t.Error("close")
	}
	if f.i("ios_eof_blocking", s) != any(int32(1)) || f.i("ios_getc", s) != any(int32(-1)) || f.i("ios_peekc", s) != any(int32(-1)) {
		t.Error("a closed stream is at its end")
	}
	// and again: closing a closed stream is not an error
	if f.i("ios_close", s) != any(int32(0)) {
		t.Error("close of a closed stream")
	}
}

// read(io) of a file: ios_fillbuf answers the size when the file is wholly
// buffered, -1 when there may be more; then ios_readall.
func TestIOSFillbufAndRead(t *testing.T) {
	f := newPCREFixture(t)
	s := f.iosOpen(f.hostFile([]byte("hello")))
	if f.i("ios_fillbuf", s) != any(int64(5)) {
		t.Error("fillbuf of a small file")
	}
	dst, m := f.buf(5, 0)
	if f.i("ios_readall", s, dst, uint64(5)) != any(uint64(5)) || string(memBytes(m)) != "hello" {
		t.Errorf("readall: %q", memBytes(m))
	}
	// 100000 bytes: the buffer fills (-1), then a read goes through it and
	// direct, all of them
	data := bytes.Repeat([]byte("0123456789"), 10000)
	s = f.iosOpen(f.hostFile(data))
	if f.i("ios_fillbuf", s) != any(int64(-1)) || f.i("jl_nb_available", s) != any(int32(32768)) {
		t.Error("fillbuf of a big file")
	}
	dst, m = f.buf(100000, 0)
	if f.i("ios_readall", s, dst, uint64(100000)) != any(uint64(100000)) || !bytes.Equal(memBytes(m), data) {
		t.Error("readall of a big file")
	}
	if f.i("ios_eof_blocking", s) != any(int32(1)) {
		t.Error("eof after it")
	}
	dst, _ = f.buf(40000, 0)
	if f.i("ios_readall", s, dst, uint64(40000)) != any(uint64(0)) { // a direct read at the end
		t.Error("readall at the end")
	}
	// a stream written into a String allocString made
	str, _ := f.vm.allocString([]any{int64(5)})
	sp := f.vm.stringPtrOf(str.(string))
	s = f.iosOpen(f.hostFile([]byte("world")))
	if f.i("ios_readall", s, sp, uint64(5)) != any(uint64(5)) || str != "world" {
		t.Errorf("readall into a String: %q", str)
	}
}

// ios_read reads once, ios_readall until the end: 50 bytes asked 100 of.
func TestIOSReadVersusReadall(t *testing.T) {
	f := newPCREFixture(t)
	p := f.hostFile(bytes.Repeat([]byte("x"), 50))
	for name, want := range map[string]uint64{"ios_read": 50, "ios_readall": 50} {
		s := f.iosOpen(p)
		dst, _ := f.buf(100, 0)
		if got := f.i(name, s, dst, uint64(100)); got != any(want) {
			t.Errorf("%s: %v", name, got)
		}
		if f.i("ios_eof_blocking", s) != any(int32(1)) {
			t.Errorf("%s: not at the end", name)
		}
	}
	// ios_read of a small count refills the buffer first
	s := f.iosOpen(p)
	dst, _ := f.buf(10, 0)
	if f.i("ios_read", s, dst, uint64(10)) != any(uint64(10)) || f.i("jl_nb_available", s) != any(int32(40)) {
		t.Error("ios_read of 10")
	}
	// ios_read of a big count goes direct and reads once
	data := bytes.Repeat([]byte("y"), 40000)
	s = f.iosOpen(f.hostFile(data))
	dst, _ = f.buf(40000, 0)
	if f.i("ios_read", s, dst, uint64(40000)) != any(uint64(40000)) {
		t.Error("a direct ios_read")
	}
	// the room: a count past the destination is refused, as are a count and a
	// destination that are no numbers and no pointer
	dst, _ = f.buf(4, 0)
	for _, args := range [][]any{{s, dst, uint64(5)}, {s, dst, f.str("x")}, {s, int64(1), uint64(1)}} {
		if _, err := f.sh("ios_read", args...); err == nil {
			t.Errorf("ios_read of %v: no error", args)
		}
	}
	// a descriptor that went away under the stream: nothing to read
	s = f.iosOpen(p)
	st, _ := f.vm.iosOf("ios_read", s)
	f.vm.fsys().close(st.fd)
	dst, _ = f.buf(100000, 0)
	if f.i("ios_read", s, dst, uint64(100000)) != any(uint64(0)) || f.i("ios_eof_blocking", s) != any(int32(1)) {
		t.Error("a stream whose descriptor is gone")
	}
	dst, _ = f.buf(4, 0)
	if f.i("ios_read", s, dst, uint64(4)) != any(uint64(0)) {
		t.Error("a small read of it")
	}
}

// The buffer moves back when a read would pass its end.
func TestIOSBufferMovesBack(t *testing.T) {
	f := newPCREFixture(t)
	s := f.iosOpen(f.hostFile(bytes.Repeat([]byte("z"), 40000)))
	f.i("ios_fillbuf", s)
	dst, _ := f.buf(32768, 0)
	f.i("ios_readall", s, dst, uint64(32768))
	if f.i("ios_eof_blocking", s) != any(int32(0)) || f.i("jl_nb_available", s) != any(int32(7232)) {
		t.Error("the rest of the file")
	}
}

// Opening: the errors are julia's (oracle: ENOENT 2), writing is refused.
func TestIOSOpen(t *testing.T) {
	f := newPCREFixture(t)
	null := func(v any) bool { return v.(*Pointer).Target == nil }
	s := f.newIOS()
	v := f.i("ios_file", s, f.str(stdlibAt+"/Nope"), int32(1), int32(0), int32(0), int32(0))
	if !null(v) || f.i("jl_errno") != any(int32(2)) || f.i("ios_isopen", s) != any(int32(0)) {
		t.Errorf("a file that is absent: %v", showValue(v))
	}
	v = f.i("ios_file", s, f.str(shaToml), int32(0), int32(0), int32(0), int32(0))
	if !null(v) || f.i("jl_errno") != any(int32(22)) {
		t.Errorf("neither read nor write: %v", showValue(v))
	}
	for _, flags := range [][4]int32{{1, 1, 0, 0}, {0, 1, 0, 0}, {1, 0, 1, 0}, {1, 0, 0, 1}} {
		v = f.i("ios_file", s, f.str(shaToml), flags[0], flags[1], flags[2], flags[3])
		if !null(v) || f.i("jl_errno") != any(int32(30)) { // EROFS
			t.Errorf("writing the tree (%v): %v", flags, showValue(v))
		}
	}
	f.vm.Host = true
	_, err := f.sh("ios_file", s, f.str(filepath.Join(t.TempDir(), "w")), int32(0), int32(1), int32(1), int32(0))
	check(t, "writing a host file", nil, err, "!for writing on the host")
	// the file in the tree
	f.vm.Host = false
	s = f.iosOpen(shaToml)
	dst, m := f.buf(4, 0)
	f.i("ios_readall", s, dst, uint64(4))
	if string(memBytes(m)) != "name" {
		t.Errorf("SHA/Project.toml starts with %q", memBytes(m))
	}
	if f.i("ios_filesize", s) != any(int64(240)) {
		t.Error("filesize of a tree file")
	}
	// a directory opens, and reads as nothing (julia: UInt8[])
	s = f.iosOpen(stdlibAt + "/SHA")
	if f.i("ios_fillbuf", s) != any(int64(0)) {
		t.Error("fillbuf of a directory")
	}
	// the arguments
	for _, c := range []struct {
		name string
		args []any
	}{
		{"a name that is no Cstring", []any{s, int64(1), int32(1), int32(0), int32(0), int32(0)}},
		{"a flag that is no integer", []any{s, f.str(shaToml), f.str("x"), int32(0), int32(0), int32(0)}},
		{"a stream that is no memory", []any{int64(1), f.str(shaToml), int32(1), int32(0), int32(0), int32(0)}},
	} {
		if _, err := f.sh("ios_file", c.args...); err == nil {
			t.Errorf("ios_file with %s: no error", c.name)
		}
	}
	// the stream at an offset into its memory is no stream
	if _, err := f.sh("ios_pos", &Pointer{Type: f.u8p, Target: s.Target, Off: 8}); err == nil {
		t.Error("a stream at an offset")
	}
}

// The calls on a stream that is none, and on one whose descriptor is gone.
func TestIOSErrors(t *testing.T) {
	f := newPCREFixture(t)
	for _, name := range []string{"ios_close", "ios_isopen", "ios_get_readable", "ios_get_writable", "jl_nb_available", "jl_ios_fd",
		"ios_fillbuf", "ios_eof_blocking", "ios_getc", "ios_peekc", "ios_seek_end", "ios_pos", "ios_filesize"} {
		if _, err := f.sh(name, int64(1)); err == nil {
			t.Errorf("%s of an Int: no error", name)
		}
	}
	for _, name := range []string{"ios_seek", "ios_skip", "ios_read", "ios_readall"} {
		if _, err := f.sh(name, int64(1), int64(1), uint64(1)); err == nil {
			t.Errorf("%s of an Int: no error", name)
		}
	}
	s := f.iosOpen(f.hostFile([]byte("hello")))
	for _, name := range []string{"ios_seek", "ios_skip"} {
		if _, err := f.sh(name, s, f.str("x")); err == nil {
			t.Errorf("%s of a pointer: no error", name)
		}
	}
	if f.i("jl_ios_fd", s).(int64) < 3 {
		t.Error("jl_ios_fd")
	}
	f.i("ios_getc", s) // the descriptor's position is no longer known
	// the descriptor closed behind the stream: each call that reaches it fails
	// with -1 and EBADF
	st, _ := f.vm.iosOf("x", s)
	f.vm.fsys().close(st.fd)
	for _, name := range []string{"ios_seek_end", "ios_pos", "ios_filesize"} {
		if got := f.i(name, s); got != any(int64(-1)) {
			t.Errorf("%s: %v", name, got)
		}
	}
	if f.i("ios_seek", s, int64(0)) != any(int64(-1)) || f.i("ios_skip", s, int64(10)) != any(int64(-1)) {
		t.Error("seek and skip")
	}
	if f.i("jl_errno") != any(int32(9)) { // EBADF
		t.Errorf("errno: %v", f.i("jl_errno"))
	}
	// ios_close reports the descriptor it could not close
	if f.i("ios_close", s) != any(int32(1)) {
		t.Error("close of a descriptor that is gone")
	}
}

// ios_skip's three ways: inside the buffer, back inside it, and past it by a
// seek of the descriptor.
func TestIOSSkip(t *testing.T) {
	f := newPCREFixture(t)
	s := f.iosOpen(f.hostFile(bytes.Repeat([]byte("abcdefghij"), 10000)))
	f.i("ios_getc", s) // buffers 32768 bytes
	if f.i("ios_skip", s, int64(0)) != any(int64(0)) || f.i("ios_skip", s, int64(9)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(10)) {
		t.Error("skip inside the buffer")
	}
	if f.i("ios_skip", s, int64(-5)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(5)) {
		t.Error("skip back inside the buffer")
	}
	if f.i("ios_skip", s, int64(50000)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(50005)) || f.i("ios_getc", s) != any(int32('f')) {
		t.Error("skip past the buffer")
	}
	if f.i("ios_skip", s, int64(-50005)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(1)) || f.i("ios_getc", s) != any(int32('b')) {
		t.Error("skip back past the buffer")
	}
	// the position a seek leaves is known without asking the descriptor
	if f.i("ios_seek", s, int64(7)) != any(int64(0)) || f.i("ios_pos", s) != any(int64(7)) {
		t.Error("seek")
	}
	// a position unknown (after a read) is asked for
	st, _ := f.vm.iosOf("x", s)
	f.i("ios_getc", s)
	if st.fpos != -1 && st.fpos != 32768 {
		t.Errorf("fpos %d", st.fpos)
	}
}

// The ends: a read of nothing, a stream closed before it is read, a byte
// after the last that was buffered.
func TestIOSEnds(t *testing.T) {
	f := newPCREFixture(t)
	p := f.hostFile([]byte("hello"))
	s := f.iosOpen(p)
	dst, _ := f.buf(5, 0)
	if f.i("ios_read", s, dst, uint64(0)) != any(uint64(0)) {
		t.Error("a read of 0 bytes")
	}
	if f.i("ios_getc", s) != any(int32('h')) || f.i("ios_eof_blocking", s) != any(int32(0)) { // buffered data: not at the end
		t.Error("eof with data buffered")
	}
	if f.i("ios_readall", s, dst, uint64(4)) != any(uint64(4)) || f.i("ios_getc", s) != any(int32(-1)) || f.i("ios_peekc", s) != any(int32(-1)) {
		t.Error("getc after the buffered bytes")
	}
	s = f.iosOpen(p)
	f.i("ios_close", s)
	if f.i("ios_fillbuf", s) != any(int64(-1)) { // ios.c:377-384: 0 bytes wanted, 0 got
		t.Error("fillbuf of a closed stream")
	}
	if f.i("ios_getc", s) != any(int32(-1)) {
		t.Error("getc of a closed stream")
	}
	s = f.iosOpen(p)
	f.i("ios_close", s)
	if f.i("ios_read", s, dst, uint64(5)) != any(uint64(0)) {
		t.Error("a read of a closed stream")
	}
	if _, err := f.sh("ios_peekc", int64(1)); err == nil {
		t.Error("ios_peekc of an Int")
	}
	s = f.iosOpen(p)
	f.i("ios_getc", s)
	f.i("ios_readall", s, dst, uint64(4))
	f.i("ios_getc", s) // at the end
	f.i("ios_close", s)
	if len(f.vm.ios) == 0 {
		t.Error("the streams are tracked")
	}
}
