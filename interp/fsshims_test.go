package interp

import (
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// the same bytes, as the shims keep them
func memBytes(m *Memory) []byte {
	b := make([]byte, len(m.Elems))
	for i, e := range m.Elems {
		b[i] = e.(uint8)
	}
	return b
}

func (f *pcreFixture) rawFD(fd int32) PrimValue {
	d := must(f.s.Eval("primitive type RawFDX 32 end\nRawFDX")).(*core.DataType)
	return PrimValue{d, u128{lo: uint64(uint32(fd))}}
}

const shaToml = stdlibAt + "/SHA/Project.toml"

// jl_stat, jl_lstat, jl_fstat and the getters of the buffer they fill.
// Julia's answers are the oracle's (a 5-byte 0644 file: size 5, mode 100644,
// nlink 1, blksize 4096, blocks 8; a missing path -2; mtime Float64).
func TestStatShims(t *testing.T) {
	f := newPCREFixture(t)
	v, err := f.sh("jl_sizeof_stat")
	check(t, "jl_sizeof_stat", v, err, "160")
	// julia 1.12.6 on darwin/arm64: ccall(:jl_uv_sizeof_interface_address, Int32, ()) == 96
	v, err = f.sh("jl_uv_sizeof_interface_address")
	check(t, "jl_uv_sizeof_interface_address", v, err, "96")
	for _, name := range []string{"jl_stat", "jl_lstat"} {
		buf, m := f.buf(160, 0xaa)
		v, err = f.sh(name, f.str(shaToml), buf)
		check(t, name, v, err, "0")
		for g, want := range map[string]string{
			"jl_stat_mode": "0x000081a4", "jl_stat_nlink": "0x00000001", "jl_stat_uid": "0x00000000",
			"jl_stat_gid": "0x00000000", "jl_stat_rdev": "0x00000000", "jl_stat_dev": "0x00000001",
			"jl_stat_size": "0x00000000000000f0", "jl_stat_blksize": "0x0000000000001000", "jl_stat_blocks": "0x0000000000000008",
			"jl_stat_mtime": "0.0", "jl_stat_ctime": "0.0",
		} {
			got, err := f.sh(g, buf)
			if err != nil || fmt.Sprint(showValue(got)) != want {
				t.Errorf("%s: %s %v, want %s", g, showValue(got), err, want)
			}
		}
		if ino, _ := f.sh("jl_stat_ino", buf); ino.(uint32) == 0 {
			t.Error("an inode of 0")
		}
		_ = m
		// a path that is absent leaves the buffer as it was
		buf, m = f.buf(160, 0xaa)
		v, err = f.sh(name, f.str(stdlibAt+"/Nope"), buf)
		check(t, name+" of nothing", v, err, "-2")
		if memBytes(m)[0] != 0xaa {
			t.Error("a failed stat wrote the buffer")
		}
		_, err = f.sh(name, int64(1), buf)
		check(t, name, nil, err, "!stat of")
		// a buffer too small
		small, _ := f.buf(10, 0)
		_, err = f.sh(name, f.str(shaToml), small)
		check(t, name+" into 10 bytes", nil, err, "!invalid pointer")
	}
	// the times: seconds and nanoseconds
	st := uvStat{mtim: [2]int64{1700000000, 500000000}, ctim: [2]int64{-1, 0}}
	buf, m := f.buf(160, 0)
	if err := f.vm.putBytes(buf, st.bytes()); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.sh("jl_stat_mtime", buf); got != any(1700000000.5) {
		t.Errorf("jl_stat_mtime: %v", got)
	}
	if got, _ := f.sh("jl_stat_ctime", buf); got != any(-1.0) {
		t.Errorf("jl_stat_ctime: %v", got)
	}
	_ = m
	for _, g := range []string{"jl_stat_mode", "jl_stat_size", "jl_stat_mtime"} {
		_, err = f.sh(g, int64(1))
		check(t, g, nil, err, "!a stat buffer")
		small, _ := f.buf(10, 0)
		_, err = f.sh(g, small)
		check(t, g, nil, err, "!invalid pointer")
	}
	// jl_fstat of an open file: the same answer as jl_stat
	fd, code := f.vm.fsys().open(shaToml, 0)
	if code != 0 {
		t.Fatal(code)
	}
	for _, arg := range []any{fd, f.rawFD(fd)} { // an Int32 or a RawFD
		buf, _ = f.buf(160, 0xaa)
		v, err = f.sh("jl_fstat", arg, buf)
		check(t, "jl_fstat", v, err, "0")
		if got, _ := f.sh("jl_stat_size", buf); got != any(uint64(240)) {
			t.Errorf("jl_fstat size: %v", got)
		}
	}
	v, err = f.sh("jl_fstat", int32(99), buf)
	check(t, "jl_fstat of a closed descriptor", v, err, "-9")
	_, err = f.sh("jl_fstat", f.str("x"), buf)
	check(t, "jl_fstat of a pointer", nil, err, "!jl_fstat of")
}

func TestAccessShim(t *testing.T) {
	f := newPCREFixture(t)
	for _, c := range []struct {
		p    string
		mode int32
		want string
	}{
		{shaToml, 0, "0"}, {shaToml, 4, "0"}, {shaToml, 2, "-30"}, {shaToml, 1, "-13"}, {stdlibAt + "/Nope", 0, "-2"},
	} {
		v, err := f.sh("jl_fs_access", f.str(c.p), c.mode)
		check(t, fmt.Sprint("access ", c.p, c.mode), v, err, c.want)
	}
	_, err := f.sh("jl_fs_access", int64(1), int32(0))
	check(t, "access of an Int", nil, err, "!jl_fs_access of")
	_, err = f.sh("jl_fs_access", f.str(shaToml), f.str("x"))
	check(t, "access with a pointer as mode", nil, err, "!jl_fs_access of")
}

// getattrlist's answers are julia 1.12.6's on darwin (a 4-byte floor,
// truncated to the buffer, the length aligned to 4).
func TestGetattrlist(t *testing.T) {
	f := newPCREFixture(t)
	dir := t.TempDir()
	vm := f.vm
	vm.Host = true
	for _, name := range []string{"Hello.txt", "abc", "abcd"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	null := &Pointer{Type: vm.ptrType(core.NothingType)}
	for _, c := range []struct {
		file string
		size int
		ret  string
		want []byte
	}{
		{"Hello.txt", 25, "0", []byte{0x18, 0, 0, 0, 8, 0, 0, 0, 0x0a, 0, 0, 0, 'H', 'e', 'l', 'l', 'o', '.', 't', 'x', 't', 0, 0, 0, 0xaa}},
		{"Hello.txt", 14, "0", []byte{0x18, 0, 0, 0, 8, 0, 0, 0, 0x0a, 0, 0, 0, 'H', 'e'}},
		{"Hello.txt", 4, "0", []byte{0x18, 0, 0, 0}},
		{"Hello.txt", 3, "-1", []byte{0xaa, 0xaa, 0xaa}},
		{"abc", 24, "0", []byte{0x10, 0, 0, 0, 8, 0, 0, 0, 4, 0, 0, 0, 'a', 'b', 'c', 0, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa, 0xaa}},
		{"abcd", 24, "0", []byte{0x14, 0, 0, 0, 8, 0, 0, 0, 5, 0, 0, 0, 'a', 'b', 'c', 'd', 0, 0, 0, 0, 0xaa, 0xaa, 0xaa, 0xaa}},
	} {
		buf, m := f.buf(c.size, 0xaa)
		v, err := f.sh("getattrlist", f.str(filepath.Join(dir, c.file)), null, buf, uint64(c.size), uint64(1))
		check(t, "getattrlist "+c.file, v, err, c.ret)
		if got := memBytes(m); string(got) != string(c.want) {
			t.Errorf("getattrlist %s into %d: % x, want % x", c.file, c.size, got, c.want)
		}
	}
	if e, _ := f.sh("jl_errno"); e != any(int32(34)) { // ERANGE, from the last failure
		t.Errorf("errno: %v", e)
	}
	buf, _ := f.buf(20, 0)
	v, err := f.sh("getattrlist", f.str(filepath.Join(dir, "nope")), null, buf, uint64(20), uint64(1))
	check(t, "getattrlist of nothing", v, err, "-1")
	if e, _ := f.sh("jl_errno"); e != any(int32(2)) { // ENOENT, as julia
		t.Errorf("errno: %v", e)
	}
	// the stored case of a name on a case-insensitive file system
	buf, m := f.buf(25, 0)
	f.sh("getattrlist", f.str(filepath.Join(dir, "Hello.txt")), null, buf, uint64(25), uint64(1))
	if string(memBytes(m)[12:21]) != "Hello.txt" {
		t.Errorf("name: %q", memBytes(m)[12:])
	}
	_, err = f.sh("getattrlist", int64(1), null, buf, uint64(25), uint64(1))
	check(t, "getattrlist of an Int", nil, err, "!getattrlist of")
	_, err = f.sh("getattrlist", f.str(shaToml), null, buf, int64(25), uint64(1))
	check(t, "getattrlist with an Int size", nil, err, "!getattrlist of")
	_, err = f.sh("getattrlist", f.str(shaToml), null, buf, uint64(1<<40), uint64(1))
	check(t, "getattrlist with a huge size", nil, err, "!getattrlist of")
	small, _ := f.buf(5, 0)
	_, err = f.sh("getattrlist", f.str(shaToml), null, small, uint64(25), uint64(1))
	check(t, "getattrlist into too little", nil, err, "!invalid pointer")
}

// malloc, free, errno and the uv_fs_t requests.
func TestRequestShims(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	p, err := f.sh("malloc", uint64(448))
	if err != nil {
		t.Fatal(err)
	}
	req := p.(*Pointer)
	if m, ok := req.Target.(*Memory); !ok || len(m.Elems) != 448 || req.Type != vm.ptrType(core.NothingType) {
		t.Fatalf("malloc: %v", showValue(p))
	}
	if q, _ := f.sh("malloc", uint64(0)); len(q.(*Pointer).Target.(*Memory).Elems) != 1 {
		t.Error("malloc of 0")
	}
	vm.MaxMemoryElems = 100
	if q, _ := f.sh("malloc", uint64(101)); q.(*Pointer).Target != nil {
		t.Error("malloc past the bound")
	}
	if e, _ := f.sh("jl_errno"); e != any(int32(12)) {
		t.Errorf("errno after malloc: %v", e)
	}
	vm.MaxMemoryElems = DefaultMaxMemoryElems
	_, err = f.sh("malloc", f.str("x"))
	check(t, "malloc of a pointer", nil, err, "!malloc of")
	if _, err = f.sh("jl_set_errno", int32(7)); err != nil {
		t.Fatal(err)
	}
	if e, _ := f.sh("jl_errno"); e != any(int32(7)) {
		t.Errorf("errno: %v", e)
	}
	_, err = f.sh("jl_set_errno", int64(7))
	check(t, "jl_set_errno of an Int", nil, err, "!TypeError")

	// uv_fs_realpath, then the path through jl_uv_fs_t_ptr
	null := &Pointer{Type: vm.ptrType(core.NothingType)}
	v, err := f.sh("jl_uv_fs_t_ptr", req)
	if err != nil || v.(*Pointer).Target != nil {
		t.Errorf("jl_uv_fs_t_ptr of a fresh request: %v %v", v, err)
	}
	v, err = f.sh("uv_fs_realpath", null, req, f.str("/julia/bin/../share/julia/stdlib/v1.12/SHA/src/.."), null)
	check(t, "uv_fs_realpath", v, err, "0")
	v, err = f.sh("jl_uv_fs_t_ptr", req)
	if err != nil || v.(*Pointer).Target != stdlibAt+"/SHA" {
		t.Errorf("jl_uv_fs_t_ptr: %v %v", v, err)
	}
	v, err = f.sh("uv_fs_realpath", null, req, f.str(stdlibAt+"/Nope"), null)
	check(t, "uv_fs_realpath of nothing", v, err, "-2")
	_, err = f.sh("uv_fs_realpath", null, int64(1), f.str("/"), null)
	check(t, "uv_fs_realpath of no request", nil, err, "!the request")
	_, err = f.sh("uv_fs_realpath", null, req, int64(1), null)
	check(t, "uv_fs_realpath of an Int", nil, err, "!uv_fs_realpath of")
	_, err = f.sh("jl_uv_fs_t_ptr", int64(1))
	check(t, "jl_uv_fs_t_ptr of no request", nil, err, "!the request")
	// a request pointer into the middle of its memory is not one
	_, err = f.sh("jl_uv_fs_t_ptr", &Pointer{Type: req.Type, Target: req.Target, Off: 8})
	check(t, "jl_uv_fs_t_ptr of an offset", nil, err, "!the request")

	// uv_fs_scandir and _next: julia's count and the entries, then UV_EOF
	if _, err := f.s.Eval("struct DirEnt; name::Ptr{UInt8}; typ::Int32; end\nmutable struct RefDirEnt; x::DirEnt; end"); err != nil {
		t.Fatal(err)
	}
	ent, st := f.ref("RefDirEnt", must(vm.newStruct(must(f.s.Eval("DirEnt")), []any{f.str(""), int32(0)})))
	v, err = f.sh("uv_fs_scandir", null, req, f.str(stdlibAt+"/SHA"), int32(0), null)
	check(t, "uv_fs_scandir", v, err, "3")
	var got []string
	for {
		v, err = f.sh("uv_fs_scandir_next", req, ent)
		if err != nil {
			t.Fatal(err)
		}
		if v == any(uvEOF) {
			break
		}
		d := st.Fields[0].(*Struct)
		got = append(got, fmt.Sprint(d.Fields[0].(*Pointer).Target, d.Fields[1]))
	}
	if fmt.Sprint(got) != "[LICENSE.md1 Project.toml1 src2]" { // julia: file, file, dir (UV_DIRENT_FILE 1, DIR 2)
		t.Errorf("entries: %v", got)
	}
	v, err = f.sh("uv_fs_scandir", null, req, f.str(stdlibAt+"/Nope"), int32(0), null)
	check(t, "uv_fs_scandir of nothing", v, err, "-2")
	v, err = f.sh("uv_fs_scandir", null, req, f.str(shaToml), int32(0), null)
	check(t, "uv_fs_scandir of a file", v, err, "-20")
	_, err = f.sh("uv_fs_scandir", null, int64(1), f.str("/"), int32(0), null)
	check(t, "uv_fs_scandir of no request", nil, err, "!the request")
	_, err = f.sh("uv_fs_scandir", null, req, int64(1), int32(0), null)
	check(t, "uv_fs_scandir of an Int", nil, err, "!uv_fs_scandir of")
	_, err = f.sh("uv_fs_scandir_next", int64(1), ent)
	check(t, "uv_fs_scandir_next of no request", nil, err, "!the request")
	f.sh("uv_fs_scandir", null, req, f.str(stdlibAt+"/SHA"), int32(0), null)
	_, err = f.sh("uv_fs_scandir_next", req, int64(1))
	check(t, "uv_fs_scandir_next into an Int", nil, err, "!uv_fs_scandir_next of")
	_, err = f.sh("uv_fs_scandir_next", req, &Pointer{Type: vm.ptrType(core.Int64Type), Target: st})
	check(t, "uv_fs_scandir_next into an Int64", nil, err, "!not ported yet")
	_, err = f.sh("uv_fs_scandir_next", req, &Pointer{Type: ent.Type, Target: "x"})
	check(t, "uv_fs_scandir_next into a string", nil, err, "!invalid pointer")
	_, err = f.sh("uv_fs_scandir_next", req, &Pointer{Type: vm.ptrType(core.AnyType), Target: st})
	check(t, "uv_fs_scandir_next into an Any", nil, err, "!TypeError")
	union := vm.ptrType(must(f.s.Eval("Union{Int8,Int16}")))
	_, err = f.sh("uv_fs_scandir_next", req, &Pointer{Type: union, Target: st})
	check(t, "uv_fs_scandir_next into a Union", nil, err, "!uv_fs_scandir_next of")
	// cleanup forgets the entries
	if _, err = f.sh("uv_fs_req_cleanup", req); err != nil {
		t.Fatal(err)
	}
	if v, _ = f.sh("uv_fs_scandir_next", req, ent); v != any(uvEOF) {
		t.Errorf("an entry after cleanup: %v", v)
	}
	_, err = f.sh("uv_fs_req_cleanup", int64(1))
	check(t, "uv_fs_req_cleanup of no request", nil, err, "!the request")
	// free forgets the request
	if _, err = f.sh("free", req); err != nil || len(vm.reqs) != 0 {
		t.Errorf("free: %v %d", err, len(vm.reqs))
	}
	if _, err = f.sh("free", null); err != nil {
		t.Error(err)
	}
	if _, err = f.sh("free", int64(0)); err != nil {
		t.Error(err)
	}
}

// jl_crc32c: julia 1.12.6's answers for Base._crc32c.
func TestCRC32C(t *testing.T) {
	f := newPCREFixture(t)
	crc := func(c uint32, p any, n uint64) string {
		v, err := f.sh("jl_crc32c", c, p, n)
		if err != nil {
			return "!" + err.Error()
		}
		return fmt.Sprintf("%x", v)
	}
	if got := crc(0, f.str("123456789"), 9); got != "e3069283" {
		t.Errorf("crc: %s", got)
	}
	c1, _ := f.sh("jl_crc32c", uint32(0), f.str("12345"), uint64(5))
	if got := crc(c1.(uint32), f.str("6789"), 4); got != "e3069283" {
		t.Errorf("chained: %s", got)
	}
	if got := crc(0, f.str(""), 0); got != "0" {
		t.Errorf("empty: %s", got)
	}
	m := &Memory{Type: memUInt8Type, Elems: []any{uint8(0x68), uint8(0x65), uint8(0x6c), uint8(0x6c), uint8(0x6f)}}
	if got := crc(0, &Pointer{Type: f.u8p, Target: m}, 5); got != "9a71bb4c" { // julia: open(_crc32c, file) of "hello"
		t.Errorf("a memory: %s", got)
	}
	// Ref{UIntN}: the word's little-endian bytes (julia: 5 as UInt64, UInt32, UInt8, UInt128)
	f.s.Eval("mutable struct RefU8; x::UInt8; end\nmutable struct RefU128; x::UInt128; end\nmutable struct RefU32b; x::UInt32; end")
	for _, c := range []struct {
		typ  string
		v    any
		n    uint64
		want string
	}{
		{"RefU64", uint64(5), 8, "e4094dc0"}, {"RefU32b", uint32(5), 4, "ee00d08c"}, {"RefU8", uint8(5), 1, "678c474d"},
		{"RefU128", UInt128{0, 5}, 16, "76783bff"},
	} {
		ref, _ := f.ref(c.typ, c.v)
		if got := crc(0, ref, c.n); got != c.want {
			t.Errorf("crc of %s: %s, want %s", c.typ, got, c.want)
		}
	}
	// fewer bytes than the word has: the low ones
	ref, _ := f.ref("RefU64", uint64(0x0102030405060708))
	want := fmt.Sprintf("%x", crc32.Checksum([]byte{8, 7, 6, 5}, castagnoli))
	if got := crc(0, ref, 4); got != want {
		t.Errorf("a short Ref: %s, want %s", got, want)
	}
	// more than the word holds is no word: refused with the rest
	if got := crc(0, ref, 9); got[0] != '!' {
		t.Errorf("a long Ref: %s", got)
	}
	for _, c := range []struct {
		name string
		args []any
	}{
		{"a crc that is not a UInt32", []any{int64(0), f.str("a"), uint64(1)}},
		{"a length that is not a Csize_t", []any{uint32(0), f.str("a"), int64(1)}},
		{"a non-pointer", []any{uint32(0), int64(1), uint64(1)}},
		{"too long", []any{uint32(0), f.str("a"), uint64(2)}},
	} {
		if _, err := f.sh("jl_crc32c", c.args...); err == nil {
			t.Errorf("%s: no error", c.name)
		}
	}
}

func TestIntArg(t *testing.T) {
	for _, c := range []struct {
		v    any
		want int64
		ok   bool
	}{
		{int8(-1), -1, true}, {int16(-2), -2, true}, {int32(-3), -3, true}, {int64(-4), -4, true},
		{uint8(5), 5, true}, {uint16(6), 6, true}, {uint32(7), 7, true}, {uint64(8), 8, true},
		{uint64(1 << 63), 0, false}, {"x", 0, false}, {PrimValue{core.Int32Type, u128{lo: 9}}, 9, true},
	} {
		if got, ok := intArg(c.v); got != c.want || ok != c.ok {
			t.Errorf("intArg(%v) = %d %v", c.v, got, ok)
		}
	}
}

// putBytes into the memory of bytes, a String allocString made, and what
// writeBytes handles.
func TestPutBytesAndRoom(t *testing.T) {
	f := newPCREFixture(t)
	vm := f.vm
	buf, m := f.buf(4, 0)
	if err := vm.putBytes(&Pointer{Type: f.u8p, Target: m, Off: 1}, []byte{1, 2}); err != nil || memBytes(m)[1] != 1 || memBytes(m)[2] != 2 {
		t.Errorf("putBytes: %v %v", err, memBytes(m))
	}
	for _, off := range []int64{-1, 5, 3} {
		if err := vm.putBytes(&Pointer{Type: f.u8p, Target: m, Off: off}, []byte{1, 2}); err == nil {
			t.Errorf("putBytes at %d: no error", off)
		}
	}
	if err := vm.putBytes(int64(1), []byte{1}); err == nil {
		t.Error("putBytes to an Int")
	}
	if n, ok := vm.room(buf); !ok || n != 4 {
		t.Errorf("room: %d %v", n, ok)
	}
	s, err := vm.allocString([]any{int64(6)})
	if err != nil {
		t.Fatal(err)
	}
	sp := vm.stringPtrOf(s.(string))
	sp.Off = 2
	if n, ok := vm.room(sp); !ok || n != 4 {
		t.Errorf("room of a String: %d %v", n, ok)
	}
	if err := vm.putBytes(sp, []byte("ab")); err != nil || s.(string)[2:4] != "ab" {
		t.Errorf("putBytes into a String: %v %q", err, s)
	}
	for _, p := range []any{int64(1), f.str("literal"), &Pointer{Type: f.u8p, Target: m, Off: 5}, &Pointer{Type: f.u8p, Target: m, Off: -1},
		&Pointer{Type: f.u8p, Target: &Memory{Type: memAnyType}}, &Pointer{Type: f.u8p}} {
		if n, ok := vm.room(p); ok {
			t.Errorf("room of %s: %d", showValue(p), n)
		}
	}
	sp.Off = 7
	if _, ok := vm.room(sp); ok {
		t.Error("room past the end of a String")
	}
}

// memmove of the bytes a Ref of an integer holds, as unsafe_write(io, Ref(x),
// sizeof(x)) copies them into an IOBuffer's data.
func TestMemmoveRef(t *testing.T) {
	f := newPCREFixture(t)
	f.s.Eval("mutable struct RefU128m; x::UInt128; end")
	ref, _ := f.ref("RefU128m", UInt128{0, 0x0102})
	src := &Pointer{Type: f.vm.ptrType(core.NothingType), Target: ref.Target}
	dst, m := f.buf(20, 0)
	dst.Off = 1
	v, err := f.sh("memmove", dst, src, uint64(16))
	if err != nil || v != any(dst) || memBytes(m)[1] != 2 || memBytes(m)[2] != 1 || memBytes(m)[3] != 0 || memBytes(m)[0] != 0 {
		t.Errorf("memmove of a Ref: %v %v", v, err)
	}
	for _, n := range []uint64{17, 0} {
		if _, err := f.sh("memmove", dst, src, n); (err == nil) == (n == 17) {
			t.Errorf("memmove of %d bytes of a Ref: %v", n, err)
		}
	}
	if _, err := f.sh("memmove", f.str("lit"), src, uint64(2)); err == nil {
		t.Error("memmove of a Ref into a String")
	}
}
