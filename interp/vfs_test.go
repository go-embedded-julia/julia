package interp

import (
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"
)

const stdlibAt = "/julia/share/julia/stdlib/v1.12"

// A file system for the cases the embedded tree never produces: a file
// that cannot be stat'ed, a directory that cannot be listed, one that is no
// ReadDirFile, a file that cannot seek.
type oddFS struct{}

type oddFile struct {
	name string
	dir  bool
	read bool // a ReadDirFile
	stat error
}

func (oddFS) Open(name string) (fs.File, error) {
	switch name {
	case ".":
		return &oddFile{name: ".", dir: true, read: true}, nil
	case "badstat":
		return &oddFile{name: name, stat: errors.New("stat failed")}, nil
	case "baddir":
		return &oddFile{name: name, dir: true, read: true}, nil
	case "plaindir":
		return &oddFile{name: name, dir: true}, nil
	case "stream":
		return &oddFile{name: name}, nil
	}
	return nil, fs.ErrNotExist
}

func (f *oddFile) Stat() (fs.FileInfo, error) {
	if f.stat != nil {
		return nil, f.stat
	}
	return oddInfo{f.name, f.dir}, nil
}
func (f *oddFile) Read(p []byte) (int, error) { return copy(p, "odd"), nil }
func (f *oddFile) Close() error               { return nil }

type oddReadDir struct{ *oddFile }

func (oddReadDir) ReadDir(int) ([]fs.DirEntry, error) { return nil, errors.New("list failed") }

type oddInfo struct {
	name string
	dir  bool
}

func (i oddInfo) Name() string { return i.name }
func (i oddInfo) Size() int64  { return 3 }
func (i oddInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i oddInfo) ModTime() time.Time { return time.Time{} }
func (i oddInfo) IsDir() bool        { return i.dir }
func (i oddInfo) Sys() any           { return nil }

// modeInfo is a FileInfo of any mode: a 2-byte file.
type modeInfo struct {
	mode fs.FileMode
	mt   time.Time
}

func (i modeInfo) Name() string       { return "f" }
func (i modeInfo) Size() int64        { return 2 }
func (i modeInfo) Mode() fs.FileMode  { return i.mode }
func (i modeInfo) ModTime() time.Time { return i.mt }
func (i modeInfo) IsDir() bool        { return i.mode.IsDir() }
func (i modeInfo) Sys() any           { return nil }

// oddDirFS opens "baddir" as a directory whose listing fails.
type oddDirFS struct{ oddFS }

func (o oddDirFS) Open(name string) (fs.File, error) {
	f, err := o.oddFS.Open(name)
	if err == nil && name == "baddir" {
		return oddReadDir{f.(*oddFile)}, nil
	}
	return f, err
}

func sandbox(t *testing.T) (*VM, *vfs) {
	t.Helper()
	vm := New()
	return vm, vm.fsys()
}

func hostVFS(t *testing.T) (*VM, *vfs) {
	t.Helper()
	vm := New()
	vm.Host = true
	return vm, vm.fsys()
}

// The sandbox serves julia's tree and nothing else, however a path is
// spelled: `..` stops at the root, a relative path is under the working
// directory. The answers are julia's: ENOENT -2 for what is absent, ENOTDIR
// -20 below a file, EROFS for a write.
func TestSandboxPaths(t *testing.T) {
	vm, v := sandbox(t)
	vm.WorkDir = stdlibAt + "/SHA"
	for _, c := range []struct {
		path string
		want int32
	}{
		{stdlibAt + "/SHA/Project.toml", 0},
		{"Project.toml", 0}, // relative to the working directory
		{"src/../Project.toml", 0},
		{stdlibAt + "/SHA/../SHA/Project.toml", 0},
		{"/julia/bin/../share/julia/stdlib/v1.12/SHA/Project.toml", 0}, // Sys.STDLIB as sysinfo.jl:57 spells it
		{"/julia/bin", 0},
		{"/", 0}, {"/julia", 0}, {"/julia/share/julia", 0}, {"/julia/share/julia/base/Base.jl", 0},
		{stdlibAt + "/Nope/Project.toml", uvENOENT},
		{"/etc/passwd", uvENOENT},
		{"/../../etc/passwd", uvENOENT},
		{stdlibAt + "/../../../../../../etc/passwd", uvENOENT},
		{"/julia/home", uvENOENT},
		{stdlibAt + "/SHA/Project.toml/x", uvENOTDIR}, // julia: -20
		{stdlibAt + "/Nope/x/y", uvENOENT},
	} {
		if _, code := v.stat(c.path, true); code != c.want {
			t.Errorf("stat %q: %d, want %d", c.path, code, c.want)
		}
	}
	// no WorkDir: the root
	vm.WorkDir = ""
	if _, code := v.stat("julia/share/julia/base/Base.jl", true); code != 0 {
		t.Errorf("a relative path with no working directory: %d", code)
	}
}

// The tree's stat: julia's installed files are regular, 0644, one 4096-byte
// block per 8 sectors (julia: size 240, blksize 4096, blocks 8, nlink 1); a
// directory is 0755 and counts its entries and the two dots.
func TestTreeStat(t *testing.T) {
	_, v := sandbox(t)
	st, code := v.stat(stdlibAt+"/SHA/Project.toml", true)
	if code != 0 || st.mode != 0o100644 || st.nlink != 1 || st.blksize != 4096 || st.blocks != 8 || st.size == 0 || st.dev == 0 || st.ino == 0 {
		t.Errorf("file: %+v %d", st, code)
	}
	if st2, _ := v.stat(stdlibAt+"/SHA/src/SHA.jl", true); st2.ino == st.ino {
		t.Error("two files share an inode")
	}
	if st3, _ := v.stat(stdlibAt+"/SHA/../SHA/Project.toml", false); st3 != st {
		t.Errorf("lstat of the same file: %+v", st3)
	}
	st, code = v.stat(stdlibAt+"/SHA", true)
	if code != 0 || st.mode != 0o040755 || st.nlink != 2+3 || st.size != 0 { // LICENSE.md, Project.toml, src
		t.Errorf("dir: %+v %d", st, code)
	}
	if len(st.bytes()) != uvStatSize {
		t.Errorf("a stat buffer of %d bytes", len(st.bytes()))
	}
	// the synthetic directories
	if st, code = v.stat("/julia", true); code != 0 || st.mode != 0o040755 {
		t.Errorf("/julia: %+v %d", st, code)
	}
	// a directory whose entries cannot be listed still stats
	if st := treeStat("d", &oddFile{dir: true}, oddInfo{"d", true}); st.nlink != 2 || st.mode != 0o040755 {
		t.Errorf("a plain directory: %+v", st)
	}
	// blocks round up
	if st := treeStat("f", &oddFile{}, oddInfo{"f", false}); st.blocks != 8 || st.size != 3 {
		t.Errorf("a 3-byte file: %+v", st)
	}
}

// What the embedded tree is made of: the Base sources at base/, the stdlibs
// at stdlib/v1.12, bin/ empty.
func TestTreeMounts(t *testing.T) {
	vm, v := sandbox(t)
	es, code := v.readdir("/julia/share/julia")
	if code != 0 || len(es) != 2 || es[0] != (dirent{"base", direntDir}) || es[1] != (dirent{"stdlib", direntDir}) {
		t.Errorf("share/julia: %v %d", es, code)
	}
	if es, _ = v.readdir("/"); len(es) != 1 || es[0].name != "julia" {
		t.Errorf("/: %v", es)
	}
	es, _ = v.readdir("/julia")
	if len(es) != 2 || es[0].name != "bin" || es[1].name != "share" {
		t.Errorf("/julia: %v", es)
	}
	if es, code = v.readdir("/julia/bin"); code != 0 || len(es) != 0 {
		t.Errorf("bin: %v %d", es, code)
	}
	if _, code = v.stat("/julia/bin/julia", true); code != uvENOENT {
		t.Errorf("bin/julia: %d", code)
	}
	es, code = v.readdir(stdlibAt)
	want := []string{"Artifacts", "CompilerSupportLibraries_jll", "FileWatching", "Libdl", "LinearAlgebra", "OpenBLAS_jll", "Random", "SHA", "Sockets", "libblastrampoline_jll"}
	if code != 0 || len(es) != len(want) {
		t.Fatalf("stdlib: %v %d", es, code)
	}
	for i, n := range want {
		if es[i] != (dirent{n, direntDir}) {
			t.Errorf("stdlib[%d] = %v, want %s", i, es[i], n)
		}
	}
	// julia lists SHA/src the same (oracle)
	es, _ = v.readdir(stdlibAt + "/SHA/src")
	var names []string
	for _, e := range es {
		names = append(names, e.name)
		if e.typ != direntFile {
			t.Errorf("%s is type %d", e.name, e.typ)
		}
	}
	if len(names) != 10 || names[0] != "SHA.jl" || names[9] != "types.jl" {
		t.Errorf("SHA/src: %v", names)
	}
	// a file is not a directory (julia: ENOTDIR), nor is there one that is absent
	if _, code = v.readdir(stdlibAt + "/SHA/Project.toml"); code != uvENOTDIR {
		t.Errorf("readdir of a file: %d", code)
	}
	if _, code = v.readdir(stdlibAt + "/Nope"); code != uvENOENT {
		t.Errorf("readdir of nothing: %d", code)
	}
	// a stdlib embedder's own tree, with no bin directory
	vm2 := New()
	vm2.BinDir = ""
	vm2.Stdlib = fstest.MapFS{"X/Project.toml": {Data: []byte("name = \"X\"\n")}}
	if es, _ = vm2.fsys().readdir("/share/julia/stdlib/v1.12"); len(es) != 1 || es[0].name != "X" {
		t.Errorf("a given stdlib tree: %v", es)
	}
	if vm.fsys() != v {
		t.Error("the file system is made once")
	}
}

// The synthetic directories, and the mount of nothing.
func TestSynthDirs(t *testing.T) {
	d := &synthDir{name: "a/b"}
	fi, err := d.Stat()
	if err != nil || fi.Name() != "b" || fi.Size() != 0 || fi.Mode() != fs.ModeDir|0o755 || !fi.ModTime().IsZero() || !fi.IsDir() || fi.Sys() != nil {
		t.Errorf("Stat: %v %v", fi, err)
	}
	if n, err := d.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Errorf("Read: %d %v", n, err)
	}
	if d.Close() != nil {
		t.Error("Close")
	}
	if _, err := d.ReadDir(1); !errors.Is(err, io.EOF) {
		t.Errorf("ReadDir(1) of nothing: %v", err)
	}
	d.entries = []fs.DirEntry{fs.FileInfoToDirEntry(synthInfo{"k"})}
	if es, err := d.ReadDir(1); err != nil || len(es) != 1 {
		t.Errorf("ReadDir(1): %v %v", es, err)
	}
	if es, err := d.ReadDir(-1); err != nil || len(es) != 0 {
		t.Errorf("ReadDir(-1) of nothing: %v %v", es, err)
	}
	if _, err := (emptyFS{}).Open("x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("emptyFS.Open: %v", err)
	}
	f, err := (emptyFS{}).Open(".")
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := f.Stat(); fi.Name() != "." {
		t.Errorf("emptyFS root: %v", fi.Name())
	}
}

// The files of the descriptor table: lowest free number first, EBADF for the
// rest, EMFILE at the bound, EROFS for a write in the tree.
func TestDescriptors(t *testing.T) {
	_, v := sandbox(t)
	p := stdlibAt + "/SHA/Project.toml"
	a, code := v.open(p, 0)
	b, _ := v.open(p, 0)
	if code != 0 || a != 3 || b != 4 {
		t.Fatalf("open: %d %d %d", a, b, code)
	}
	if v.close(a) != 0 {
		t.Error("close")
	}
	if c, _ := v.open(p, 0); c != 3 {
		t.Errorf("a freed descriptor is reused: %d", c)
	}
	if v.close(99) != uvEBADF || v.close(a+0) != 0 || v.close(a) != uvEBADF {
		t.Error("close of a descriptor that is not open")
	}
	if _, code := v.read(99, 1); code != uvEBADF {
		t.Errorf("read: %d", code)
	}
	if _, code := v.seek(99, 0, 0); code != uvEBADF {
		t.Errorf("seek: %d", code)
	}
	if _, code := v.fstat(99); code != uvEBADF {
		t.Errorf("fstat: %d", code)
	}
	for _, flags := range []int32{1, 2, 0x200, 0x400, 8} {
		if _, code := v.open(p, flags); code != uvEROFS {
			t.Errorf("open for writing (%#x): %d", flags, code)
		}
	}
	if _, code := v.open(stdlibAt+"/Nope", 0); code != uvENOENT {
		t.Errorf("open of nothing: %d", code)
	}
	// 1024 at most
	for len(v.files) < maxOpenFiles {
		if _, code := v.open(p, 0); code != 0 {
			t.Fatalf("open: %d", code)
		}
	}
	if fd, code := v.open(p, 0); code != uvEMFILE || fd != -1 {
		t.Errorf("the 1025th open: %d %d", fd, code)
	}
}

// Reading, seeking and fstat of an open file; a directory reads as an error
// and a seek of a file that cannot is ESPIPE.
func TestReadSeek(t *testing.T) {
	vm, v := sandbox(t)
	p := stdlibAt + "/SHA/Project.toml"
	fd, _ := v.open(p, 0)
	b, code := v.read(fd, 4)
	if code != 0 || string(b) != "name" {
		t.Errorf("read: %q %d", b, code)
	}
	if pos, code := v.seek(fd, 0, 1); pos != 4 || code != 0 {
		t.Errorf("seek: %d %d", pos, code)
	}
	if pos, _ := v.seek(fd, 0, 2); pos != 240 {
		t.Errorf("seek end: %d", pos)
	}
	if b, _ := v.read(fd, 10); len(b) != 0 {
		t.Errorf("read at the end: %q", b)
	}
	if _, code := v.seek(fd, -1, 0); code == 0 {
		t.Errorf("seek before the start: %d", code)
	}
	st, code := v.fstat(fd)
	if code != 0 || st.size != 240 || st.mode != 0o100644 {
		t.Errorf("fstat: %+v %d", st, code)
	}
	// a read is bounded
	if b, _ := v.read(fd, math.MaxInt); len(b) > maxReadChunk {
		t.Errorf("a read of %d bytes", len(b))
	}
	// a directory: open works, read fails
	dfd, code := v.open(stdlibAt+"/SHA", 0)
	if code != 0 {
		t.Fatal(code)
	}
	if _, code := v.read(dfd, 1); code == 0 {
		t.Error("a read of a directory")
	}
	// odd files: no seek, no stat
	vm.Stdlib = oddFS{}
	vm.vfs = nil
	v = vm.fsys()
	sfd, _ := v.open(stdlibAt+"/stream", 0)
	if _, code := v.seek(sfd, 0, 0); code != uvESPIPE {
		t.Errorf("seek of a stream: %d", code)
	}
	bfd, code := v.open(stdlibAt+"/badstat", 0)
	if code != 0 {
		t.Fatal(code)
	}
	if _, code := v.fstat(bfd); code != uvEIO {
		t.Errorf("fstat of a file that cannot: %d", code)
	}
	if _, code := v.stat(stdlibAt+"/badstat", true); code != uvEIO {
		t.Errorf("stat of a file that cannot: %d", code)
	}
	if code := v.access(stdlibAt+"/badstat", 0); code != uvEIO {
		t.Errorf("access of a file that cannot: %d", code)
	}
}

func TestReaddirErrors(t *testing.T) {
	vm, v := sandbox(t)
	vm.Stdlib = oddDirFS{}
	vm.vfs = nil
	v = vm.fsys()
	if _, code := v.readdir(stdlibAt + "/baddir"); code != uvEIO {
		t.Errorf("a listing that fails: %d", code)
	}
	if _, code := v.readdir(stdlibAt + "/plaindir"); code != uvENOTDIR {
		t.Errorf("a directory that cannot list: %d", code)
	}
}

// access: the tree is readable, searchable when a directory, never writable.
func TestTreeAccess(t *testing.T) {
	_, v := sandbox(t)
	p := stdlibAt + "/SHA/Project.toml"
	for _, c := range []struct {
		p    string
		mode int32
		want int32
	}{
		{p, fOK, 0}, {p, rOK, 0}, {p, wOK, uvEROFS}, {p, xOK, uvEACCES}, // julia: 0 0 0 -13 on a 0644 file it may write
		{stdlibAt + "/SHA", xOK, 0}, {stdlibAt + "/SHA", rOK | xOK, 0}, {stdlibAt + "/SHA", wOK, uvEROFS},
		{stdlibAt + "/Nope", fOK, uvENOENT}, // julia: -2
	} {
		if got := v.access(c.p, c.mode); got != c.want {
			t.Errorf("access(%s, %d) = %d, want %d", c.p, c.mode, got, c.want)
		}
	}
}

func TestRealpathAndCase(t *testing.T) {
	_, v := sandbox(t)
	if r, code := v.realpath("/julia/bin/../share/julia/stdlib/v1.12/SHA/./src/.."); r != stdlibAt+"/SHA" || code != 0 {
		t.Errorf("realpath: %q %d", r, code)
	}
	if _, code := v.realpath(stdlibAt + "/Nope"); code != uvENOENT { // julia: ENOENT
		t.Errorf("realpath of nothing: %d", code)
	}
	// the stored case of a name: its own when it matches, the stored one
	// when only the case differs, the name when there is none
	for _, c := range []struct{ in, want string }{
		{stdlibAt + "/SHA/Project.toml", "Project.toml"},
		{stdlibAt + "/SHA/project.toml", "Project.toml"},
		{stdlibAt + "/SHA/nothing.toml", "nothing.toml"},
		{stdlibAt + "/Nope/x.toml", "x.toml"},
		{stdlibAt + "/SHA/Project.toml/x", "x"},
	} {
		if got := v.caseName(c.in); got != c.want {
			t.Errorf("caseName(%s) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestUvErr(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int32
	}{
		{fs.ErrNotExist, uvENOENT}, {fs.ErrPermission, uvEACCES}, {fs.ErrExist, uvEEXIST}, {fs.ErrInvalid, uvEINVAL},
		{io.EOF, uvEOF}, {errors.New("other"), uvEIO},
		{&fs.PathError{Err: os.ErrNotExist}, uvENOENT},
	} {
		if got := uvErr(c.err); got != c.want {
			t.Errorf("uvErr(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestDirentTypes(t *testing.T) {
	for _, c := range []struct {
		m    fs.FileMode
		want int32
	}{
		{0, direntFile}, {fs.ModeDir, direntDir}, {fs.ModeSymlink, direntLink}, {fs.ModeNamedPipe, direntFIFO},
		{fs.ModeSocket, direntSocket}, {fs.ModeCharDevice | fs.ModeDevice, direntChar}, {fs.ModeDevice, direntBlock},
		{fs.ModeIrregular, direntUnknown},
	} {
		if got := direntType(c.m); got != c.want {
			t.Errorf("direntType(%v) = %d, want %d", c.m, got, c.want)
		}
	}
}

func TestGenericStat(t *testing.T) {
	mt := time.Unix(1700000000, 5)
	for _, c := range []struct {
		m    fs.FileMode
		want uint64
	}{
		{0o640, 0o100640}, {fs.ModeDir | 0o755, 0o040755}, {fs.ModeSymlink | 0o777, 0o120777},
		{fs.ModeNamedPipe | 0o600, 0o010600}, {fs.ModeSocket | 0o600, 0o140600},
		{fs.ModeDevice | fs.ModeCharDevice | 0o600, 0o020600 | 0o060000}, {fs.ModeDevice | 0o600, 0o060600},
	} {
		st := genericStat(modeInfo{c.m, mt})
		if st.mode != c.want || st.size != 2 || st.blocks != 8 || st.nlink != 1 || st.mtim != [2]int64{1700000000, 5} {
			t.Errorf("genericStat(%v) = %+v, want mode %o", c.m, st, c.want)
		}
	}
}

// The host keeps julia's tree at its paths and the real files everywhere
// else, resolved by the system.
func TestHostPaths(t *testing.T) {
	vm, v := hostVFS(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Hello.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Hello.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	// the tree is still mounted
	if _, code := v.stat(stdlibAt+"/SHA/Project.toml", true); code != 0 {
		t.Errorf("tree on the host: %d", code)
	}
	// the host's files
	st, code := v.stat(filepath.Join(dir, "Hello.txt"), true)
	if code != 0 || st.size != 5 || st.mode != 0o100644 || st.nlink != 1 || st.blksize != 4096 || st.blocks != 8 { // julia: 5 100644 1 4096 8
		t.Errorf("stat: %+v %d", st, code)
	}
	if st, code = v.stat(dir, true); code != 0 || st.mode&0o170000 != 0o040000 {
		t.Errorf("stat of a directory: %+v %d", st, code)
	}
	if st, _ = v.stat(filepath.Join(dir, "link"), false); st.mode&0o170000 != 0o120000 {
		t.Errorf("lstat of a link: %o", st.mode)
	}
	if st, _ = v.stat(filepath.Join(dir, "link"), true); st.mode&0o170000 != 0o100000 {
		t.Errorf("stat of a link: %o", st.mode)
	}
	if _, code = v.stat(filepath.Join(dir, "nope"), true); code != uvENOENT {
		t.Errorf("stat of nothing: %d", code)
	}
	if _, code = v.stat(filepath.Join(dir, "Hello.txt", "x"), true); code != uvENOTDIR {
		t.Errorf("below a file: %d", code)
	}
	// relative to the working directory
	vm.WorkDir = dir
	if _, code = v.stat("Hello.txt", true); code != 0 {
		t.Errorf("relative: %d", code)
	}
	vm.WorkDir = ""
	cwd, _ := os.Getwd()
	if vm.workDir() != cwd {
		t.Errorf("the host's own directory: %q, not %q", vm.workDir(), cwd)
	}
	if _, code = v.stat("vfs_test.go", true); code != 0 { // the package's directory, where tests run
		t.Errorf("relative to the process: %d", code)
	}
	// descriptors
	fd, code := v.open(filepath.Join(dir, "Hello.txt"), 0)
	if code != 0 {
		t.Fatal(code)
	}
	if b, _ := v.read(fd, 100); string(b) != "hello" {
		t.Errorf("read: %q", b)
	}
	if pos, _ := v.seek(fd, 1, 0); pos != 1 {
		t.Errorf("seek: %d", pos)
	}
	if st, code = v.fstat(fd); code != 0 || st.size != 5 {
		t.Errorf("fstat: %+v %d", st, code)
	}
	v.files[fd].h.Close() // a descriptor closed behind the table's back
	if _, code = v.fstat(fd); code == 0 {
		t.Error("fstat of a closed file")
	}
	if _, code = v.read(fd, 1); code == 0 {
		t.Error("read of a closed file")
	}
	v.close(fd)
	if _, code = v.open(filepath.Join(dir, "nope"), 0); code != uvENOENT {
		t.Errorf("open of nothing: %d", code)
	}
	if _, code = v.open(filepath.Join(dir, "x"), 0x200); code != uvENOSYS {
		t.Errorf("host write: %d", code)
	}
	// readdir, realpath, access
	es, code := v.readdir(dir)
	if code != 0 || len(es) != 3 || es[0] != (dirent{"Hello.txt", direntFile}) || es[1] != (dirent{"link", direntLink}) || es[2] != (dirent{"sub", direntDir}) {
		t.Errorf("readdir: %v %d", es, code)
	}
	if _, code = v.readdir(filepath.Join(dir, "nope")); code != uvENOENT {
		t.Errorf("readdir of nothing: %d", code)
	}
	want, _ := filepath.EvalSymlinks(filepath.Join(dir, "Hello.txt"))
	if r, code := v.realpath(filepath.Join(dir, "link")); r != want || code != 0 {
		t.Errorf("realpath: %q %d, want %q", r, code, want)
	}
	if _, code = v.realpath(filepath.Join(dir, "nope")); code != uvENOENT {
		t.Errorf("realpath of nothing: %d", code)
	}
	if v.access(filepath.Join(dir, "Hello.txt"), rOK|wOK) != 0 || v.access(filepath.Join(dir, "Hello.txt"), xOK) != uvEACCES || v.access(filepath.Join(dir, "nope"), fOK) != uvENOENT {
		t.Error("access") // julia: 0 for r and w, -13 for x, -2
	}
	// the stored case of a name
	if got := v.caseName(filepath.Join(dir, "hello.txt")); got != "Hello.txt" && got != "hello.txt" {
		t.Errorf("caseName: %q", got)
	}
	if got := v.caseName(filepath.Join(dir, "Hello.txt")); got != "Hello.txt" {
		t.Errorf("caseName: %q", got)
	}
	if got := v.caseName(filepath.Join(dir, "nope", "x")); got != "x" {
		t.Errorf("caseName: %q", got)
	}
}

func TestHostStatGeneric(t *testing.T) {
	// a FileInfo that is no darwin stat(2): what package os knows of it
	st := hostStat(oddInfo{"f", false})
	if st.mode != 0o100644 || st.size != 3 {
		t.Errorf("hostStat of a plain FileInfo: %+v", st)
	}
}
