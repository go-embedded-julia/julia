package interp

import (
	"embed"
	"errors"
	"hash/fnv"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The VM's filesystem: the one layer every file shim reaches (stat, the
// IOStream ios_* calls, libuv's uv_fs_* and jl_fs_*). It has two modes.
//
// The sandbox, the default, holds only julia's own tree: the Base sources
// (VM.Sources) and the stdlibs' Project.toml and src, mounted where julia
// installs them (<BinDir>/../share/julia/base and .../stdlib/v1.12). No path
// leaves it: a name is made absolute against VM.WorkDir and cleaned
// lexically, so `..` stops at the root, and the embedded tree holds no
// symbolic link to follow. Every other path is absent (UV_ENOENT) and the
// tree is read-only (UV_EROFS).
//
// The host (VM.Host) keeps that tree mounted at its paths and sends every
// other path to the real filesystem through package os.
//
// Errors are libuv's: a negative errno of the platform the VM models,
// darwin/arm64 (uv_constants.jl).

//go:embed stdlib
var stdlibFiles embed.FS

// stdlibSources is the stdlibs, julia's stdlib/v1.12 directory: one
// directory per package, its Project.toml and src (stdlib/README.md).
var stdlibSources = must(fs.Sub(stdlibFiles, "stdlib"))

// libuv's errors on darwin (uv_constants.jl), as negative errnos.
const (
	uvEPERM        int32 = -1
	uvENOENT       int32 = -2
	uvEIO          int32 = -5
	uvEBADF        int32 = -9
	uvENOMEM       int32 = -12
	uvEACCES       int32 = -13
	uvEEXIST       int32 = -17
	uvENOTDIR      int32 = -20
	uvEISDIR       int32 = -21
	uvEINVAL       int32 = -22
	uvEMFILE       int32 = -24
	uvEROFS        int32 = -30
	uvESPIPE       int32 = -29
	uvERANGE       int32 = -34
	uvENAMETOOLONG int32 = -63
	uvELOOP        int32 = -62
	uvENOTEMPTY    int32 = -66
	uvENOSYS       int32 = -78
	uvEOF          int32 = -4095
)

// maxOpenFiles bounds the descriptors a VM holds open.
const maxOpenFiles = 1024

// maxReadChunk bounds what one read of a file returns to a shim.
const maxReadChunk = 1 << 20

// access(2) modes (file_constants.jl)
const (
	fOK = 0
	xOK = 1
	wOK = 2
	rOK = 4
)

// uvStat is libuv's uv_stat_t (uv.h): twelve 64-bit integers, then four
// timespecs, 160 bytes on darwin/arm64 (jl_sizeof_stat).
type uvStat struct {
	dev, mode, nlink, uid, gid, rdev, ino, size, blksize, blocks, flags, gen uint64
	atim, mtim, ctim, birthtim                                               [2]int64 // sec, nsec
}

const uvStatSize = 160

// bytes is the stat buffer julia code reads, little-endian fields in
// declaration order.
func (s *uvStat) bytes() []byte {
	b := make([]byte, 0, uvStatSize)
	for _, v := range []uint64{s.dev, s.mode, s.nlink, s.uid, s.gid, s.rdev, s.ino, s.size, s.blksize, s.blocks, s.flags, s.gen} {
		b = le64(b, v)
	}
	for _, t := range [][2]int64{s.atim, s.mtim, s.ctim, s.birthtim} {
		b = le64(b, uint64(t[0]))
		b = le64(b, uint64(t[1]))
	}
	return b
}

func le64(b []byte, v uint64) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24), byte(v>>32), byte(v>>40), byte(v>>48), byte(v>>56))
}

// uv_dirent_type_t (uv.h)
const (
	direntUnknown = 0
	direntFile    = 1
	direntDir     = 2
	direntLink    = 3
	direntFIFO    = 4
	direntSocket  = 5
	direntChar    = 6
	direntBlock   = 7
)

type dirent struct {
	name string
	typ  int32
}

// mount is a tree served at a path: at is slash-separated, without a
// leading slash.
type mount struct {
	at   string
	fsys fs.FS
}

// vfile is an open file: a file of the julia tree or one of the host.
type vfile struct {
	f    fs.File
	h    *os.File
	path string // the cleaned absolute path, for fstat of a file of the tree
	// std is a descriptor 0, 1 or 2: the reader or writer of the embedder
	// (VM.Stdin, VM.Stdout, VM.Stderr), fetched at each call so that one set
	// after the table was made counts
	rd func() io.Reader
	wr func() io.Writer
}

func (f *vfile) read(p []byte) (int, error) {
	if f.rd != nil {
		r := f.rd()
		if r == nil {
			return 0, io.EOF
		}
		return r.Read(p)
	}
	if f.wr != nil {
		return 0, errBADF // a descriptor open for writing only
	}
	if f.h != nil {
		return f.h.Read(p)
	}
	return f.f.Read(p)
}

// write is write(2): the bytes go to the embedder's writer, and a file of
// the tree or the host (opened read-only here) refuses them.
func (f *vfile) write(p []byte) (int, error) {
	if f.wr == nil {
		return 0, errBADF
	}
	w := f.wr()
	if w == nil {
		return len(p), nil
	}
	return w.Write(p)
}

// stdInfo is what fstat answers of descriptors 0, 1 and 2, which julia sees
// as files: a regular file of mode 0644 and no size.
type stdInfo struct{ synthInfo }

func (stdInfo) Mode() fs.FileMode { return 0o644 }
func (stdInfo) IsDir() bool       { return false }

func (f *vfile) seek(off int64, whence int) (int64, error) {
	if f.rd != nil || f.wr != nil {
		return 0, errSPIPE
	}
	if f.h != nil {
		return f.h.Seek(off, whence)
	}
	if s, ok := f.f.(io.Seeker); ok {
		return s.Seek(off, whence)
	}
	return 0, errSPIPE
}

func (f *vfile) stat() (fs.FileInfo, error) {
	if f.rd != nil || f.wr != nil {
		return stdInfo{synthInfo{path.Base(f.path)}}, nil
	}
	if f.h != nil {
		return f.h.Stat()
	}
	return f.f.Stat()
}

func (f *vfile) close() error {
	if f.rd != nil || f.wr != nil {
		return nil // the embedder's own stream is not ours to close
	}
	if f.h != nil {
		return f.h.Close()
	}
	return f.f.Close()
}

// vfs is a VM's filesystem.
type vfs struct {
	host    func() bool
	workdir func() string
	mounts  []mount
	files   map[int32]*vfile
}

// fsys is vm's filesystem, made on first use from VM.Host, VM.WorkDir and
// VM.BinDir: an embedder sets them before the first file is touched.
func (vm *VM) fsys() *vfs {
	if vm.vfs != nil {
		return vm.vfs
	}
	share := path.Join(path.Dir(path.Clean(vm.BinDir)), "share", "julia")
	at := func(sub string) string { return strings.TrimPrefix(path.Join(share, sub), "/") }
	stdlib := stdlibSources
	if vm.Stdlib != nil {
		stdlib = vm.Stdlib
	}
	v := &vfs{host: func() bool { return vm.Host }, workdir: vm.workDir, files: map[int32]*vfile{}, mounts: []mount{
		{at("base"), vm.Sources},
		{at("stdlib/v1.12"), stdlib},
	}}
	if bin := strings.TrimPrefix(path.Clean("/"+vm.BinDir), "/"); bin != "" {
		v.mounts = append(v.mounts, mount{bin, emptyFS{}})
	}
	// standard input, output and error, in both modes: the host's own are
	// never touched
	v.files[0] = &vfile{path: "stdin", rd: func() io.Reader { return vm.Stdin }}
	v.files[1] = &vfile{path: "stdout", wr: func() io.Writer { return vm.Stdout }}
	v.files[2] = &vfile{path: "stderr", wr: func() io.Writer { return vm.Stderr }}
	vm.vfs = v
	return v
}

// workDir is the current directory (pwd()): VM.WorkDir, the host's own when
// the embedder gave none.
func (vm *VM) workDir() string {
	if vm.WorkDir == "" && vm.Host {
		w, _ := os.Getwd()
		return w
	}
	return vm.WorkDir
}

// emptyFS is a directory holding nothing: julia's bin directory, which has
// no executable here.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}
	return &synthDir{name: "."}, nil
}

// synthDir is a directory of the tree that no mounted file system holds: an
// ancestor of a mount point.
type synthDir struct {
	name    string
	entries []fs.DirEntry
}

type synthInfo struct{ name string }

func (i synthInfo) Name() string       { return i.name }
func (i synthInfo) Size() int64        { return 0 }
func (i synthInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o755 }
func (i synthInfo) ModTime() time.Time { return time.Time{} }
func (i synthInfo) IsDir() bool        { return true }
func (i synthInfo) Sys() any           { return nil }

func (d *synthDir) Stat() (fs.FileInfo, error) { return synthInfo{path.Base(d.name)}, nil }
func (d *synthDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: errISDIR}
}
func (d *synthDir) Close() error { return nil }
func (d *synthDir) ReadDir(n int) ([]fs.DirEntry, error) {
	es := d.entries
	d.entries = nil
	if n > 0 && len(es) == 0 {
		return nil, io.EOF
	}
	return es, nil
}

// clean is name made absolute against the working directory and cleaned
// lexically: "/" is its own parent.
func (v *vfs) clean(name string) string {
	if !path.IsAbs(name) {
		w := v.workdir()
		if w == "" {
			w = "/"
		}
		name = path.Join(w, name)
	}
	return path.Clean(name)
}

// route is where a path is served: the tree's name for it, or (on the host,
// outside every mount point) the operating system's.
func (v *vfs) route(name string) (treeName, osName string, inTree bool) {
	abs := v.clean(name)
	rel := strings.TrimPrefix(abs, "/")
	if rel == "" {
		rel = "."
	}
	if !v.host() {
		return rel, "", true
	}
	for _, m := range v.mounts {
		if rel == m.at || strings.HasPrefix(rel, m.at+"/") {
			return rel, "", true
		}
	}
	// the operating system resolves an absolute name itself (`..` after a
	// link means what the kernel says); a relative one is made absolute
	// against the working directory
	// (on Windows too: `C:\x` and `\\?\C:\x` are absolute, `/x` is not)
	if path.IsAbs(name) || filepath.IsAbs(name) {
		return "", filepath.FromSlash(name), false
	}
	return "", filepath.FromSlash(abs), false
}

// treeOpen opens a name of the tree. A name that is absent is UV_ENOTDIR
// instead when what it would lie in is a file.
func (v *vfs) treeOpen(name string) (fs.File, error) {
	f, err := v.mountOpen(name)
	if errors.Is(err, fs.ErrNotExist) {
		for p := path.Dir(name); p != "."; p = path.Dir(p) {
			if pf, perr := v.mountOpen(p); perr == nil {
				fi, serr := pf.Stat()
				pf.Close()
				if serr == nil && !fi.IsDir() {
					return nil, errNOTDIR
				}
				break
			}
		}
	}
	return f, err
}

// treeOpenStat opens a name of the tree and stats it: the file, its info
// or a libuv error.
func (v *vfs) treeOpenStat(name string) (fs.File, fs.FileInfo, int32) {
	f, err := v.treeOpen(name)
	if err != nil {
		return nil, nil, uvErr(err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, uvErr(err)
	}
	return f, fi, 0
}

// mountOpen is the longest mount holding a name, or a directory made of the
// mount points below it.
func (v *vfs) mountOpen(name string) (fs.File, error) {
	for _, m := range v.mounts {
		if name == m.at {
			return m.fsys.Open(".")
		}
		if strings.HasPrefix(name, m.at+"/") {
			return m.fsys.Open(strings.TrimPrefix(name, m.at+"/"))
		}
	}
	// a proper ancestor of a mount point
	prefix := name + "/"
	if name == "." {
		prefix = ""
	}
	kids := map[string]bool{}
	for _, m := range v.mounts {
		if strings.HasPrefix(m.at, prefix) {
			kids[strings.SplitN(strings.TrimPrefix(m.at, prefix), "/", 2)[0]] = true
		}
	}
	if len(kids) == 0 {
		return nil, fs.ErrNotExist
	}
	names := make([]string, 0, len(kids))
	for k := range kids {
		names = append(names, k)
	}
	sort.Strings(names)
	d := &synthDir{name: name}
	for _, k := range names {
		d.entries = append(d.entries, fs.FileInfoToDirEntry(synthInfo{k}))
	}
	return d, nil
}

// genericStat is what package os knows of a file: its type and permission
// bits, size and modification time.
func genericStat(fi fs.FileInfo) uvStat {
	m := fi.Mode()
	s := uvStat{nlink: 1, size: uint64(fi.Size()), blksize: 4096, mode: uint64(m.Perm())}
	s.blocks = (s.size + 4095) / 4096 * 8
	for mode, bits := range map[fs.FileMode]uint64{fs.ModeDir: 0o040000, fs.ModeSymlink: 0o120000,
		fs.ModeNamedPipe: 0o010000, fs.ModeSocket: 0o140000, fs.ModeCharDevice: 0o020000, fs.ModeDevice: 0o060000} {
		if m&mode != 0 {
			s.mode |= bits
		}
	}
	if m.IsRegular() {
		s.mode |= 0o100000
	}
	t := fi.ModTime()
	s.atim, s.mtim, s.ctim = [2]int64{t.Unix(), int64(t.Nanosecond())}, [2]int64{t.Unix(), int64(t.Nanosecond())}, [2]int64{t.Unix(), int64(t.Nanosecond())}
	return s
}

// errnoCode is an error and the libuv code it maps to.
type errnoCode struct {
	target error
	code   int32
}

// portableErrnos are the errors every platform has (package fs, io).
var portableErrnos = []errnoCode{
	{fs.ErrNotExist, uvENOENT}, {fs.ErrPermission, uvEACCES}, {fs.ErrExist, uvEEXIST},
	{fs.ErrInvalid, uvEINVAL}, {io.EOF, uvEOF},
}

// uvErr is err as a libuv error code: the host's errno values first
// (errno_posix.go, errno_plan9.go), then the portable errors.
func uvErr(err error) int32 {
	for _, table := range [][]errnoCode{hostErrnos, portableErrnos} {
		for _, e := range table {
			if errors.Is(err, e.target) {
				return e.code
			}
		}
	}
	return uvEIO
}

// treeStat is the stat of a name of the julia tree. The tree is
// not on any device: it reports what julia's installed files have (mode
// 0644 for a file, 0755 for a directory, one block of 4096 bytes per 4096
// of size), a device of its own and, as inode, a hash of the path, so that
// samefile tells two files apart; its files carry no time.
func treeStat(name string, f fs.File, fi fs.FileInfo) uvStat {
	h := fnv.New32a()
	h.Write([]byte(name))
	s := uvStat{dev: 1, ino: uint64(h.Sum32()) | 1, blksize: 4096, nlink: 1}
	if fi.IsDir() {
		s.mode = 0o040755
		s.nlink = 2
		if rd, ok := f.(fs.ReadDirFile); ok {
			es, _ := rd.ReadDir(-1)
			s.nlink += uint64(len(es))
		}
		return s
	}
	s.mode = 0o100644
	s.size = uint64(fi.Size())
	s.blocks = (s.size + 4095) / 4096 * 8
	return s
}

// stat is uv_fs_stat (follow) or uv_fs_lstat.
func (v *vfs) stat(name string, follow bool) (uvStat, int32) {
	tn, on, inTree := v.route(name)
	if inTree {
		f, fi, code := v.treeOpenStat(tn)
		if code != 0 {
			return uvStat{}, code
		}
		defer f.Close()
		return treeStat(tn, f, fi), 0
	}
	var fi fs.FileInfo
	var err error
	if follow {
		fi, err = os.Stat(on)
	} else {
		fi, err = os.Lstat(on)
	}
	if err != nil {
		return uvStat{}, uvErr(err)
	}
	return hostStat(fi), 0
}

// fstat is uv_fs_fstat.
func (v *vfs) fstat(fd int32) (uvStat, int32) {
	f := v.files[fd]
	if f == nil {
		return uvStat{}, uvEBADF
	}
	fi, err := f.stat()
	if err != nil {
		return uvStat{}, uvErr(err)
	}
	if f.h != nil {
		return hostStat(fi), 0
	}
	return treeStat(f.path, f.f, fi), 0
}

// access is uv_fs_access: the tree is readable, searchable (a directory) and
// never writable.
func (v *vfs) access(name string, mode int32) int32 {
	tn, on, inTree := v.route(name)
	if !inTree {
		return hostAccess(on, mode)
	}
	f, fi, code := v.treeOpenStat(tn)
	if code != 0 {
		return code
	}
	defer f.Close()
	switch {
	case mode&wOK != 0:
		return uvEROFS
	case mode&xOK != 0 && !fi.IsDir():
		return uvEACCES
	}
	return 0
}

// open is open(2) of a name for reading; writing a file is refused in the
// tree and not ported on the host.
func (v *vfs) open(name string, flags int32) (int32, int32) {
	tn, on, inTree := v.route(name)
	if flags&3 != 0 || flags&(0x200|0x400|8) != 0 { // O_WRONLY|O_RDWR, O_CREAT|O_TRUNC|O_APPEND
		if inTree {
			return -1, uvEROFS
		}
		return -1, uvENOSYS
	}
	if len(v.files) >= maxOpenFiles {
		return -1, uvEMFILE
	}
	f := &vfile{}
	if inTree {
		t, err := v.treeOpen(tn)
		if err != nil {
			return -1, uvErr(err)
		}
		f.f, f.path = t, tn
	} else {
		h, err := os.Open(on)
		if err != nil {
			return -1, uvErr(err)
		}
		f.h = h
	}
	fd := int32(3)
	for v.files[fd] != nil {
		fd++
	}
	v.files[fd] = f
	return fd, 0
}

// read is read(2): up to n bytes, none at the end of the file.
func (v *vfs) read(fd int32, n int) ([]byte, int32) {
	f := v.files[fd]
	if f == nil {
		return nil, uvEBADF
	}
	b := make([]byte, min(n, maxReadChunk))
	got, err := f.read(b)
	if got == 0 && err != nil && err != io.EOF {
		return nil, uvErr(err)
	}
	return b[:got], 0
}

// write is write(2): the bytes written, or a libuv error. A writer that
// takes fewer bytes than it was given owes an error, which comes with them.
func (v *vfs) write(fd int32, b []byte) (int, int32) {
	f := v.files[fd]
	if f == nil {
		return 0, uvEBADF
	}
	n, err := f.write(b)
	n = max(0, min(n, len(b)))
	if err != nil {
		return n, uvErr(err)
	}
	if n == 0 && len(b) > 0 {
		return 0, uvEIO // no byte and no error: a writer that cannot be waited on
	}
	return n, 0
}

// seek is lseek(2).
func (v *vfs) seek(fd int32, off int64, whence int32) (int64, int32) {
	f := v.files[fd]
	if f == nil {
		return -1, uvEBADF
	}
	pos, err := f.seek(off, int(whence))
	if err != nil {
		return -1, uvErr(err)
	}
	return pos, 0
}

// close is close(2).
func (v *vfs) close(fd int32) int32 {
	f := v.files[fd]
	if f == nil {
		return uvEBADF
	}
	delete(v.files, fd)
	f.close()
	return 0
}

// readdir is uv_fs_scandir: the entries of a directory with their types.
func (v *vfs) readdir(name string) ([]dirent, int32) {
	tn, on, inTree := v.route(name)
	var es []fs.DirEntry
	if inTree {
		f, err := v.treeOpen(tn)
		if err != nil {
			return nil, uvErr(err)
		}
		defer f.Close()
		d, ok := f.(fs.ReadDirFile)
		if !ok {
			return nil, uvENOTDIR
		}
		if es, err = d.ReadDir(-1); err != nil {
			return nil, uvErr(err)
		}
	} else {
		var err error
		if es, err = os.ReadDir(on); err != nil {
			return nil, uvErr(err)
		}
	}
	out := make([]dirent, len(es))
	for i, e := range es {
		out[i] = dirent{e.Name(), direntType(e.Type())}
	}
	return out, 0
}

func direntType(m fs.FileMode) int32 {
	switch {
	case m&fs.ModeSymlink != 0:
		return direntLink
	case m.IsDir():
		return direntDir
	case m&fs.ModeNamedPipe != 0:
		return direntFIFO
	case m&fs.ModeSocket != 0:
		return direntSocket
	case m&fs.ModeCharDevice != 0:
		return direntChar
	case m&fs.ModeDevice != 0:
		return direntBlock
	case m.IsRegular():
		return direntFile
	}
	return direntUnknown
}

// realpath is uv_fs_realpath: the cleaned name in the tree (it holds no
// link), the host's resolution elsewhere.
func (v *vfs) realpath(name string) (string, int32) {
	tn, on, inTree := v.route(name)
	if inTree {
		f, err := v.treeOpen(tn)
		if err != nil {
			return "", uvErr(err)
		}
		f.Close()
		return v.clean(name), 0
	}
	r, err := filepath.EvalSymlinks(on)
	if err != nil {
		return "", uvErr(err)
	}
	// as libuv answers: the system's spelling (backslashes on Windows)
	return r, 0
}

// caseName is the name a path is stored under in its directory: on a
// case-insensitive file system the stored case of what was asked for
// (getattrlist's ATTR_CMN_NAME).
func (v *vfs) caseName(name string) string {
	base := path.Base(v.clean(name))
	tn, on, inTree := v.route(name)
	if !inTree {
		base = filepath.Base(filepath.Clean(on)) // the system's separators
	}
	var es []fs.DirEntry
	if inTree {
		d, err := v.treeOpen(path.Dir(tn))
		if err != nil {
			return base
		}
		defer d.Close()
		if rd, ok := d.(fs.ReadDirFile); ok {
			es, _ = rd.ReadDir(-1)
		}
	} else {
		es, _ = os.ReadDir(filepath.Dir(on))
	}
	for _, e := range es {
		if e.Name() == base {
			return base
		}
	}
	for _, e := range es {
		if strings.EqualFold(e.Name(), base) {
			return e.Name()
		}
	}
	return base
}
