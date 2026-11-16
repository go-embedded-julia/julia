package interp

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unsafe"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-regexp/pcre2"
)

func saveBytes(t testing.TB, vm *VM) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := vm.SaveImage(&b, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// imageParts is an image read back as far as its framing goes.
type imageParts struct {
	version uint32
	key     [32]byte
	types   []string
	kinds   []imageKindEntry
	strs    []string
	recs    [][]byte
	roots   []byte

	hints, interned []byte
}

type imageKindEntry struct {
	typ  string
	tier int
	idx  int
}

type imageReader struct {
	t testing.TB
	b []byte
}

func (r *imageReader) uv() uint64 {
	x, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.t.Fatal("a truncated varint")
	}
	r.b = r.b[n:]
	return x
}

func (r *imageReader) take(n int) []byte {
	if n > len(r.b) {
		r.t.Fatalf("%d bytes wanted, %d left", n, len(r.b))
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s
}

func (r *imageReader) str() string { return string(r.take(int(r.uv()))) }

// parseImage reads the framing of an image and checks its magic, its version,
// the SHA-256 trailer, and that every section ends where the next begins.
func parseImage(t testing.TB, b []byte) *imageParts {
	t.Helper()
	if len(b) < len(imageMagic)+4+32+32 || string(b[:len(imageMagic)]) != imageMagic {
		t.Fatal("no magic")
	}
	sum := sha256.Sum256(b[:len(b)-32])
	if !bytes.Equal(sum[:], b[len(b)-32:]) {
		t.Fatal("the trailer is not the SHA-256 of the image")
	}
	r := &imageReader{t: t, b: b[len(imageMagic) : len(b)-32]}
	p := &imageParts{version: binary.LittleEndian.Uint32(r.take(4))}
	copy(p.key[:], r.take(32))
	for n := r.uv(); n > 0; n-- {
		p.types = append(p.types, r.str())
	}
	for n := r.uv(); n > 0; n-- {
		x := r.uv()
		k := imageKindEntry{typ: p.types[x>>2], tier: int(x & 3)}
		if k.tier == tierStatic || k.tier == tierBoot {
			k.idx = int(r.uv())
		}
		p.kinds = append(p.kinds, k)
	}
	for n := r.uv(); n > 0; n-- {
		p.strs = append(p.strs, r.str())
	}
	for range p.kinds {
		p.recs = append(p.recs, r.take(int(r.uv())))
	}
	p.roots = r.take(int(r.uv()))
	p.hints = r.take(int(r.uv()))
	p.interned = r.take(int(r.uv()))
	if len(r.b) != 0 {
		t.Fatalf("%d bytes after the roots", len(r.b))
	}
	return p
}

// The same VM saved twice, and two VMs booted one after the other, give the
// same bytes.
func TestImageDeterministic(t *testing.T) {
	a := bootedVM(t)
	first, second := saveBytes(t, a), saveBytes(t, a)
	if !bytes.Equal(first, second) {
		t.Error("one VM saved twice gives two images")
	}
	if third := saveBytes(t, bootedVM(t)); !bytes.Equal(first, third) {
		t.Errorf("two VMs booted one after the other give images of %d and %d bytes that differ", len(first), len(third))
	}
	// a program run in both is the same program: the ids it hands out, the
	// bindings it makes, are the same
	run := func() []byte {
		vm := bootedVM(t)
		s := vm.NewSession()
		for _, src := range []string{"mutable struct IM; x; end", "const im1 = IM(1)", "f_im(x) = x + 1", "im2 = (IM(2), :a)"} {
			if _, err := s.Eval(src); err != nil {
				t.Fatalf("%s: %v", src, err)
			}
		}
		return saveBytes(t, vm)
	}
	if x, y := run(), run(); !bytes.Equal(x, y) {
		t.Errorf("one program, two VMs, images of %d and %d bytes that differ", len(x), len(y))
	}
}

// The header carries the format version and the key, which is a function of
// the version, the schema, the sources and the module version; sections end
// where they say; the trailer is the SHA-256.
func TestImageHeader(t *testing.T) {
	vm := bootedVM(t)
	b := saveBytes(t, vm)
	p := parseImage(t, b)
	if p.version != imageVersion {
		t.Errorf("version %d, want %d", p.version, imageVersion)
	}
	if want := imageKey(vmImageSpec.schemaHash()); p.key != want {
		t.Error("the key is not the key of this interpreter")
	}
	if len(p.kinds) == 0 || len(p.strs) == 0 || len(p.types) == 0 || len(p.roots) == 0 {
		t.Errorf("empty sections: %d kinds, %d strings, %d types, %d bytes of roots", len(p.kinds), len(p.strs), len(p.types), len(p.roots))
	}
	for i := 1; i < len(p.strs); i++ {
		if p.strs[i-1] >= p.strs[i] {
			t.Fatalf("strings %q and %q are not in order", p.strs[i-1], p.strs[i])
		}
	}
	for i := 1; i < len(p.types); i++ {
		if p.types[i-1] >= p.types[i] {
			t.Fatalf("types %q and %q are not in order", p.types[i-1], p.types[i])
		}
	}
}

// A changed schema changes the hash, and so the key: a record layout, a field
// skipped or saved, a kind, a func or weak pointer classified differently.
func TestImageSchemaHash(t *testing.T) {
	base := vmImageSpec.schemaHash()
	if again := vmImageSpec.schemaHash(); again != base {
		t.Error("the hash is not stable")
	}
	copyMap := func(m map[string]string) map[string]string {
		c := map[string]string{}
		for k, v := range m {
			c[k] = v
		}
		return c
	}
	variants := map[string]func(s *imageSpec){
		"a skipped field":     func(s *imageSpec) { s.skip = copyMap(s.skip); s.skip["interp.Struct.Fields"] = "x" },
		"a field saved":       func(s *imageSpec) { s.skip = copyMap(s.skip); delete(s.skip, "interp.code.stmts") },
		"a func classified":   func(s *imageSpec) { s.funcs = copyMap(s.funcs); s.funcs["interp.X.f"] = "x" },
		"a func reclassified": func(s *imageSpec) { s.funcs = copyMap(s.funcs); s.funcs["interp.Builtin.Fn"] = "elsewise" },
		"a weak pointer":      func(s *imageSpec) { s.weaks = copyMap(s.weaks); delete(s.weaks, "interp.Struct.exprOwner") },
		"a root made host":    func(s *imageSpec) { s.roots = map[string]bool{"Core": false} },
		"a kind dropped":      func(s *imageSpec) { s.defs = s.defs[1:] },
		"a kind reclassified": func(s *imageSpec) { s.defs = append([]imageKindDef{{s.defs[0].t, kindStatic}}, s.defs[1:]...) },
	}
	seen := map[[32]byte]string{base: "the VM's"}
	for name, mod := range variants {
		s := vmImageSpec
		mod(&s)
		h := s.schemaHash()
		if other, dup := seen[h]; dup {
			t.Errorf("%s hashes as %s", name, other)
		}
		seen[h] = name
		if imageKey(h) == imageKey(base) {
			t.Errorf("%s does not change the key", name)
		}
	}
}

// The key's module version is the one string for the CI job that builds the
// image (julia is the main module, stamped from the tag, with a revision and a
// modified flag) and for a consumer (julia is a dependency, with neither): an
// image keyed by what only the first has would load nowhere.
func TestImageKeyIsTheSameForTheBuilderAndAConsumer(t *testing.T) {
	old := readBuildInfo
	defer func() { readBuildInfo = old }()
	asMain := &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: "v0.14.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "0123456789abcdef"}, {Key: "vcs.modified", Value: "false"}}}
	asDep := &debug.BuildInfo{Main: debug.Module{Path: "example.com/notebook", Version: "(devel)"}, Deps: []*debug.Module{
		{Path: "golang.org/x/text", Version: "v0.44.0"}, {Path: modulePathOfJulia, Version: "v0.14.0", Sum: "h1:abc"}}}
	asDirtyMain := &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: "v0.14.0"}, Settings: []debug.BuildSetting{
		{Key: "vcs.revision", Value: "fedcba9876543210"}, {Key: "vcs.modified", Value: "true"}}}
	key := func(bi *debug.BuildInfo) [32]byte {
		readBuildInfo = func() (*debug.BuildInfo, bool) { return bi, true }
		return imageKey(vmImageSpec.schemaHash())
	}
	if key(asMain) != key(asDep) || key(asMain) != key(asDirtyMain) {
		t.Error("the key of the builder and of a consumer differ")
	}
	for name, c := range map[string]struct {
		bi   *debug.BuildInfo
		want string
	}{
		"main":              {asMain, "v0.14.0"},
		"dependency":        {asDep, "v0.14.0"},
		"a test binary":     {&debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia}}, "(devel)"},
		"replaced by a dir": {&debug.BuildInfo{Main: debug.Module{Path: "x"}, Deps: []*debug.Module{{Path: modulePathOfJulia, Version: "v0.14.0", Replace: &debug.Module{Path: "../julia"}}}}, "(devel)"},
		"replaced by a tag": {&debug.BuildInfo{Main: debug.Module{Path: "x"}, Deps: []*debug.Module{{Path: modulePathOfJulia, Version: "v0.14.0", Replace: &debug.Module{Path: "fork", Version: "v0.14.1"}}}}, "v0.14.1"},
		"not there":         {&debug.BuildInfo{Main: debug.Module{Path: "x"}}, "unknown"},
	} {
		if got := versionIn(c.bi); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	// only a tag is a version of an image
	for _, v := range []string{"v0.14.0", "v1.0.0", "v2.10.3-rc.1"} {
		if err := CheckImageVersion(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, v := range []string{"", "(devel)", "unknown", "v0.14", "0.14.0", "v0.14.0+dirty", "v0.14.0+incompatible", "v0.14.1-0.20260101120000-0123456789ab",
		"v0.14.1-20260101120000-0123456789ab", "v0.14.0-pre.0.20260101120000-0123456789ab", "v01.2.3", "v1.2.3/../x", "v1.2.3 "} {
		if err := CheckImageVersion(v); !errors.Is(err, ErrImageVersion) {
			t.Errorf("%q: %v", v, err)
		}
	}
	readBuildInfo = func() (*debug.BuildInfo, bool) { return asMain, true }
	if v, err := ImageVersion(); v != "v0.14.0" || err != nil {
		t.Errorf("ImageVersion of a tag: %q, %v", v, err)
	}
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: "v0.14.1-0.20260101120000-0123456789ab+dirty"}}, true
	}
	if _, err := ImageVersion(); !errors.Is(err, ErrImageVersion) {
		t.Errorf("ImageVersion of a dirty pseudo-version: %v", err)
	}
}

func TestImageKeyParts(t *testing.T) {
	if sourcesHash() == ([32]byte{}) || sourcesHash() != sourcesHash() {
		t.Error("the sources hash is empty or unstable")
	}
	old := readBuildInfo
	defer func() { readBuildInfo = old }()
	readBuildInfo = func() (*debug.BuildInfo, bool) { return nil, false }
	if v := moduleVersion(); v != "unknown" {
		t.Errorf("no build info: %q", v)
	}
	k0 := imageKey([32]byte{})
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Path: modulePathOfJulia, Version: "v1.2.3"}, Settings: []debug.BuildSetting{
			{Key: "GOOS", Value: "x"}, {Key: "vcs.revision", Value: "abc"}, {Key: "vcs.modified", Value: "true"}}}, true
	}
	if v := moduleVersion(); v != "v1.2.3" {
		t.Errorf("module version %q: the revision and the modified flag are not part of it", v)
	}
	if imageKey([32]byte{}) == k0 {
		t.Error("the module version is not in the key")
	}
	if imageKey([32]byte{1}) == imageKey([32]byte{}) {
		t.Error("the schema is not in the key")
	}
	// a source that cannot be read ends its hash, and the others are hashed
	h := sha256.New()
	if err := hashFS(h, failFS{}); err == nil {
		t.Error("a file system that fails")
	}
	h2 := sha256.New()
	if err := hashFS(h2, fstest.MapFS{"a/b": {Data: []byte("x")}, "c": {Data: []byte("y")}}); err != nil {
		t.Fatal(err)
	}
	h3 := sha256.New()
	_ = hashFS(h3, fstest.MapFS{"a/b": {Data: []byte("x")}, "c": {Data: []byte("z")}})
	if bytes.Equal(h2.Sum(nil), h3.Sum(nil)) {
		t.Error("a changed source does not change the hash")
	}
}

type failFS struct{}

func (failFS) Open(string) (fs.File, error) { return nil, errors.New("no") }

// Bounds: a VM over a limit is refused, before it is written, and the error is
// the sentinel.
func TestImageBounds(t *testing.T) {
	vm := bootedVM(t)
	var b bytes.Buffer
	if err := vm.SaveImage(&b, SaveOptions{MaxObjects: 100}); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("100 objects: %v", err)
	}
	b.Reset()
	if err := vm.SaveImage(&b, SaveOptions{MaxBytes: 10_000}); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("10 KB: %v", err)
	}
	if b.Len() > 10_000 {
		t.Errorf("%d bytes written past the limit", b.Len())
	}
	full := len(saveBytes(t, vm))
	b.Reset()
	if err := vm.SaveImage(&b, SaveOptions{MaxBytes: int64(full), MaxObjects: len(vm.bootIdx) * 100}); err != nil {
		t.Errorf("an exact limit: %v", err)
	}
	b.Reset()
	if err := vm.SaveImage(&b, SaveOptions{MaxBytes: int64(full) - 1}); !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("one byte short: %v", err)
	}
	// the writer's own error comes back
	boom := errors.New("disk full")
	for _, budget := range []int{0, full / 2, full - 5} {
		if err := vm.SaveImage(&errWriter{boom, budget}, SaveOptions{}); !errors.Is(err, boom) {
			t.Errorf("a writer failing after %d bytes: %v", budget, err)
		}
	}
}

// errWriter fails once it has taken its budget of bytes.
type errWriter struct {
	err    error
	budget int
}

func (w *errWriter) Write(p []byte) (int, error) {
	if len(p) > w.budget {
		return 0, w.err
	}
	w.budget -= len(p)
	return len(p), nil
}

// A Go value of a kind the image has no table entry for, put where the walk
// reaches it, refuses the save, naming the kind and the path.
func TestImageRefusals(t *testing.T) {
	x := 7
	// kinds the default tables do not know, put in a map of the VM
	cases := []struct {
		name string
		val  any
		want string
	}{
		{"an unknown pointer", &walkUnknownT{}, "no kind for *interp.walkUnknownT"},
		{"an unknown named value", walkNamedInt(1), "no kind for interp.walkNamedInt"},
		{"an unknown named slice", walkNamedSlice{1}, "no kind for interp.walkNamedSlice"},
		{"an unsafe pointer", unsafe.Pointer(&x), "no kind for unsafe.Pointer"},
		{"a foreign value", []any{new(pcre2.Code)}, "a foreign value *pcre2.Code"},
		{"a map keyed by an unknown pointer", map[*walkUnknownT]int{{}: 1}, "keyed by a *interp.walkUnknownT"},
		{"a map keyed by floats", map[float64]int{1: 1}, "keys of kind float64"},
	}
	for _, c := range cases {
		vm := bootedVM(t)
		vm.singletons[mustType("Main", "RefusalHost", core.AnyType, false, nil, nil)] = c.val
		err := vm.SaveImage(io.Discard, SaveOptions{})
		if !errors.Is(err, ErrImageUnsaveable) || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
			continue
		}
		if !strings.Contains(err.Error(), "VM.singletons") {
			t.Errorf("%s: the path is missing from %q", c.name, err)
		}
	}

	// kinds the tables know, with a field of a kind they refuse
	spec := vmImageSpec
	spec.kinds = map[string]string{"*interp.walkHost": kindSaved, "*interp.walkCx": kindSaved, "*interp.walkWk": kindSaved}
	for k, v := range vmImageSpec.kinds {
		spec.kinds[k] = v
	}
	hosts := []struct {
		name string
		host any
		want string
	}{
		{"a chan", &walkHost{Ch: make(chan int)}, "a chan at interp.walkHost.Ch"},
		{"an unsafe pointer", &walkHost{Raw: unsafe.Pointer(&x)}, "an unsafe.Pointer at interp.walkHost.Raw"},
		{"a func at no owner", &walkHost{Fn: func() {}}, "a Go func at interp.walkHost.Fn"},
		{"a complex number", &walkCx{}, "a complex128 at interp.walkCx.Cx"},
		{"a weak pointer at no owner", &walkWk{W: weak.Make(&x)}, "a weak pointer at interp.walkWk.W"},
	}
	for _, c := range hosts {
		vm := bootedVM(t)
		vm.singletons[mustType("Main", "WalkHostKey", core.AnyType, false, nil, nil)] = c.host
		err := vm.saveImage(io.Discard, SaveOptions{}, &spec)
		if !errors.Is(err, ErrImageUnsaveable) || err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
	// an empty host is saved: nil chan, func and pointers are nothing
	vm := bootedVM(t)
	vm.singletons[mustType("Main", "WalkHostKey", core.AnyType, false, nil, nil)] = &walkHost{}
	if err := vm.saveImage(io.Discard, SaveOptions{}, &spec); err != nil {
		t.Errorf("an empty host: %v", err)
	}

	// a VM field no table classifies
	vm = bootedVM(t)
	spec = vmImageSpec
	spec.roots = map[string]bool{}
	for k, v := range vmImageSpec.roots {
		if k != "gsCtr" {
			spec.roots[k] = v
		}
	}
	if err := vm.saveImage(io.Discard, SaveOptions{}, &spec); err == nil || !strings.Contains(err.Error(), "interp.VM.gsCtr is a field no table classifies") {
		t.Errorf("an unclassified VM field: %v", err)
	}

	// a Task other than the root
	vm = bootedVM(t)
	vm.rootTask()
	vm.singletons[mustType("Main", "OtherTask", core.AnyType, false, nil, nil)] = &Struct{Type: vm.tTask, Fields: newTaskFields(nil, nil, nil, [5]uint64{})}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "Task other than the root") {
		t.Errorf("a second Task: %v", err)
	}

	// two keys that print alike: a type made twice with one module and name
	vm = bootedVM(t)
	a, b := mustType("Main", "Twin", core.AnyType, false, nil, nil), mustType("Main", "Twin", core.AnyType, false, nil, nil)
	vm.ctors[a.Name], vm.ctors[b.Name] = nil, nil
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "print alike") {
		t.Errorf("two keys with one content key: %v", err)
	}
	// the boot enumeration makes the same check, and keeps its error
	vm.enumerateBoot()
	if vm.bootErr == nil || vm.bootIdx != nil && false {
		t.Error("the boot enumeration of a VM with twin keys")
	}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); !errors.Is(err, ErrImageUnsaveable) {
		t.Errorf("a VM whose boot enumeration failed: %v", err)
	}
}

type walkNamedSlice []int

// readFailFS lists a file it cannot read.
type readFailFS struct{ fstest.MapFS }

func (readFailFS) Open(name string) (fs.File, error) { return nil, errors.New("unreadable") }

func (readFailFS) ReadFile(name string) ([]byte, error) { return nil, errors.New("unreadable") }

func (r readFailFS) ReadDir(name string) ([]fs.DirEntry, error) { return r.MapFS.ReadDir(name) }

func (r readFailFS) Stat(name string) (fs.FileInfo, error) { return r.MapFS.Stat(name) }

// Maps are written in the order of their keys' content, whatever the key
// kind; a key kind with none refuses the save (TestImageRefusals).
func TestImageMapKeys(t *testing.T) {
	build := func(inject func(vm *VM) any) []byte {
		vm := bootedVM(t)
		vm.singletons[mustType("Main", "KeyHost", core.AnyType, false, nil, nil)] = inject(vm)
		return saveBytes(t, vm)
	}
	m := func(vm *VM) *dispatch.Method { return vm.typeofFn.Table.Methods(vm.World())[0] }
	maps := map[string]func(vm *VM) any{
		"bool":      func(*VM) any { return map[bool]int{true: 1, false: 2} },
		"int":       func(*VM) any { return map[int8]string{-3: "a", 4: "b", 0: "c"} },
		"uint":      func(*VM) any { return map[uint16]string{9: "a", 4: "b", 0: "c"} },
		"string":    func(*VM) any { return map[string]int{"b": 1, "a": 2, "": 3} },
		"interface": func(*VM) any { return map[any]int{nil: 1, "x": 2, int64(3): 3, expr.Symbol("s"): 4} },
		"struct":    func(vm *VM) any { return map[layoutOf]int{{core.Int64Type}: 1, {core.Int32Type}: 2} },
		"method":    func(vm *VM) any { return map[*dispatch.Method]int{m(vm): 1} },
		"module":    func(vm *VM) any { return map[*Module]int{vm.Core: 1, vm.Main: 2, nil: 3} },
		"typename":  func(vm *VM) any { return map[*core.TypeName]int{core.Int64Type.Name: 1, core.Int32Type.Name: 2} },
		"datatype":  func(vm *VM) any { return map[*core.DataType]int{core.Int64Type: 1, core.Int32Type: 2} },
		"floats":    func(*VM) any { return []any{float32(1.5), 2.5, -0.0} },
	}
	for name, inject := range maps {
		first := build(inject)
		for i := 0; i < 2; i++ {
			if !bytes.Equal(first, build(inject)) {
				t.Errorf("%s: two images of one map differ", name)
				break
			}
		}
	}
	// an unknown value in a key is reported with the key in its path
	vm := bootedVM(t)
	vm.singletons[mustType("Main", "KeyHost", core.AnyType, false, nil, nil)] = map[any]int{walkNamedInt(1): 1}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "[map key]") {
		t.Errorf("an unknown key: %v", err)
	}
	// a nil in a map value, and a pointer typed field to an unknown kind
	vm = bootedVM(t)
	vm.singletons[mustType("Main", "NilHost", core.AnyType, false, nil, nil)] = nil
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err != nil {
		t.Errorf("a nil value: %v", err)
	}
	spec := vmImageSpec
	spec.kinds = map[string]string{"*interp.walkPtrHost": kindSaved}
	for k, v := range vmImageSpec.kinds {
		spec.kinds[k] = v
	}
	vm.singletons[mustType("Main", "PtrHostKey", core.AnyType, false, nil, nil)] = &walkPtrHost{P: &walkUnknownT{}}
	if err := vm.saveImage(io.Discard, SaveOptions{}, &spec); err == nil || !strings.Contains(err.Error(), "no kind for *interp.walkUnknownT") {
		t.Errorf("a typed pointer to an unknown kind: %v", err)
	}
	// a source that is listed and cannot be read
	if err := hashFS(sha256.New(), readFailFS{fstest.MapFS{"a": {Data: []byte("x")}}}); err == nil {
		t.Error("an unreadable source")
	}
}

type walkPtrHost struct{ P *walkUnknownT }

// walkHost has a field of each kind the encoder refuses.
type walkHost struct {
	Ch  chan int
	Raw unsafe.Pointer
	Fn  func()
}

type walkCx struct{ Cx complex128 }

type walkWk struct{ W weak.Pointer[int] }

// A path through more than 64 objects is cut, and a Go panic that is not the
// encoder's is not swallowed.
func TestImageDeepPath(t *testing.T) {
	vm := bootedVM(t)
	tn := mustType("Main", "Link", core.AnyType, false, []expr.Symbol{"next"}, []any{core.AnyType})
	tn.Name.Mutable = true
	var chain any = &walkUnknownT{}
	for i := 0; i < 100; i++ {
		chain = &Struct{Type: tn, Fields: []any{chain}}
	}
	vm.singletons[mustType("Main", "DeepHost", core.AnyType, false, nil, nil)] = chain
	err := vm.SaveImage(io.Discard, SaveOptions{})
	if err == nil || !strings.Contains(err.Error(), "no kind for *interp.walkUnknownT") {
		t.Fatalf("%v", err)
	}
	// 4 objects from the start, 16 before the value: 10 Structs among them,
	// and the number of steps left out
	if n := strings.Count(err.Error(), "*interp.Struct"); n < 8 || n > 12 || !strings.Contains(err.Error(), "... (") {
		t.Errorf("%d Structs on a path cut to 20 objects: %v", n, err)
	}
	defer func() {
		if r := recover(); r != "not the encoder's" {
			t.Errorf("recovered %v", r)
		}
	}()
	imgRecover("not the encoder's")
}

// The encoder's own consistency check: a reference in the second pass to an
// object the first did not meet.
func TestImageSecondPassUnknownObject(t *testing.T) {
	vm := bootedVM(t)
	e := newImgEnc(vm, &vmImageSpec, SaveOptions{})
	e.writing = true
	defer func() {
		r := recover()
		f, ok := r.(imgFail)
		if !ok || !strings.Contains(f.err.Error(), "was not found in the second pass") {
			t.Errorf("recovered %v", r)
		}
	}()
	e.cur = -1
	e.ref(valueOf(&Memory{}), "")
}

// A save needs a point where the VM is at rest.
func TestImageCut(t *testing.T) {
	vm := bootedVM(t)
	m := vm.newModule("Open", nil)
	vm.openModules[m] = 1
	if err := vm.SaveImage(io.Discard, SaveOptions{}); !errors.Is(err, ErrImageUnsaveable) || !strings.Contains(err.Error(), "modules are open") {
		t.Errorf("an open module: %v", err)
	}
	delete(vm.openModules, m)
	vm.reservations = []reservation{{}}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "engine reservations") {
		t.Errorf("a reservation: %v", err)
	}
	vm.reservations = nil
	vm.cur = &Struct{}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "task other than the root") {
		t.Errorf("another task running: %v", err)
	}
	vm.cur = nil
	vm.loop = &uvLoop{handles: map[*Memory]*uvHandle{{}: {}}}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "libuv handles") {
		t.Errorf("a libuv handle: %v", err)
	}
	vm.loop = &uvLoop{running: true}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil {
		t.Error("a running loop")
	}
	vm.loop = &uvLoop{}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err != nil {
		t.Errorf("an idle loop: %v", err)
	}
	// the root task itself running is not another
	vm.rootTask()
	vm.cur = vm.task
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err != nil {
		t.Errorf("the root task running: %v", err)
	}
}

// Objects are static, boot or full records; the root task is its own tier
// and holds only its storage.
func TestImageTiers(t *testing.T) {
	vm := bootedVM(t)
	vm.rootTask()
	p := parseImage(t, saveBytes(t, vm))
	count := map[int]int{}
	for i, k := range p.kinds {
		count[k.tier]++
		if (k.tier == tierStatic) != (len(p.recs[i]) == 0) && k.tier != tierFull {
			t.Errorf("object %d (tier %d) has a record of %d bytes", i, k.tier, len(p.recs[i]))
		}
	}
	for tier, name := range map[int]string{tierFull: "full", tierStatic: "static", tierBoot: "boot", tierTask: "task"} {
		if count[tier] == 0 {
			t.Errorf("no %s object", name)
		}
	}
	if count[tierTask] != 1 {
		t.Errorf("%d root tasks", count[tierTask])
	}
	// every static object is named by an index of core's table; the boot
	// objects by an index below the number New made
	objs := core.StaticObjects()
	for _, k := range p.kinds {
		switch {
		case k.tier == tierStatic && (k.idx < 0 || k.idx >= len(objs)):
			t.Errorf("static index %d of %d", k.idx, len(objs))
		case k.tier == tierBoot && k.idx >= len(vm.bootIdx):
			t.Errorf("boot index %d of %d", k.idx, len(vm.bootIdx))
		}
	}
	// the task holds the storage and nothing else: its other fields can change
	// without changing the image
	before := saveBytes(t, vm)
	vm.task.Fields[0] = int64(12345)
	if !bytes.Equal(before, saveBytes(t, vm)) {
		t.Error("a field of the root task other than its storage is in the image")
	}
	vm.task.Fields[2] = "storage"
	if bytes.Equal(before, saveBytes(t, vm)) {
		t.Error("the root task's storage is not in the image")
	}
	// a Task type without storage cannot be saved
	vm = bootedVM(t)
	vm.tTask = mustType("Main", "NoStorage", core.AnyType, false, []expr.Symbol{"a"}, []any{core.AnyType})
	vm.task = &Struct{Type: vm.tTask, Fields: []any{nil}}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "no storage field") {
		t.Errorf("a Task without storage: %v", err)
	}
}

// The guard of core's Static table over a built VM, as imagewalk is the guard
// of the kinds: every type of core the VM reaches is the static one, never a
// second type of the same module and name that the table does not name (a
// core type made again where the variable should be used), and every object
// of the table the VM reaches is written as static.
func TestImageStaticGuard(t *testing.T) {
	vm := bootedVM(t)
	if _, err := vm.NewSession().Eval("struct SG; x::Int; end; SG(1)"); err != nil {
		t.Fatal(err)
	}
	e := newImgEnc(vm, &vmImageSpec, SaveOptions{})
	e.cur = -1
	e.discover()
	static := map[string]*core.TypeName{}
	for _, o := range core.StaticObjects() {
		if tn, ok := o.(*core.TypeName); ok {
			static[tn.Module+"."+tn.Name] = tn
		}
	}
	// boot.jl, evaluated by Boot(), defines these again as the VM's own
	// types (primitive type BFloat16, struct Pair): found by this guard, not
	// core's variables, saved as full records; an entry the VM no longer
	// duplicates must come out
	known := map[string]bool{"Core.BFloat16": true, "Core.Pair": true}
	seenDup := map[string]bool{}
	reached, names := 0, 0
	for _, o := range e.objs {
		if o.v.Kind() != reflect.Pointer {
			continue
		}
		_, isStatic := core.StaticIndex(o.v.Interface())
		if isStatic != (o.tier == tierStatic) {
			t.Errorf("%s: static in core is %v, in the image tier %d", o.t, isStatic, o.tier)
		}
		if tn, ok := o.v.Interface().(*core.TypeName); ok {
			names++
			if s := static[tn.Module+"."+tn.Name]; s != nil && s != tn {
				full := tn.Module + "." + tn.Name
				seenDup[full] = true
				if !known[full] {
					t.Errorf("%s is made again: a TypeName that is not the static one", full)
				}
			}
			if isStatic {
				reached++
			}
		}
	}
	for name := range known {
		if !seenDup[name] {
			t.Errorf("%s is no longer made again: take it out of the list", name)
		}
	}
	if reached < 10 || names <= reached {
		t.Errorf("%d static and %d other type names reached", reached, names-reached)
	}
	// a core type the VM holds is in the table: the variables the VM boots
	// from are exactly the ones named in core
	for _, v := range []any{vm.tExpr, vm.tTask, vm.builtinType} {
		if v == nil {
			continue
		}
		if _, ok := core.StaticIndex(v); ok {
			t.Errorf("%v made by the VM is in core's table", v)
		}
	}
	for _, s := range core.StaticVars() {
		if _, ok := core.StaticIndex(s.Value); !ok {
			t.Errorf("%s is not in the table", s.Name)
		}
	}
}

// Two New() enumerate the same boot objects in the same order.
func TestImageBootEnumeration(t *testing.T) {
	order := func(vm *VM) []string {
		out := make([]string, len(vm.bootIdx))
		for k, i := range vm.bootIdx {
			out[i] = k.t.String()
		}
		return out
	}
	a, b := order(New()), order(New())
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("%d and %d boot objects", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("boot object %d is a %s, then a %s", i, a[i], b[i])
		}
	}
	if len(order(bootedVM(t))) != len(a) {
		t.Error("Boot() changed what New made")
	}
	// each boot object is named once, by a number below the count
	seen := map[int32]bool{}
	for _, i := range New().bootIdx {
		if seen[i] || int(i) >= len(a) {
			t.Fatalf("boot index %d", i)
		}
		seen[i] = true
	}
}

// A Ptr in a mutable field, a Memory element or a binding is C_NULL in the
// image, with its type: where it points does not change the bytes. Elsewhere
// it is saved as it is, and a Ptr(-1) too.
func TestImageNullPointers(t *testing.T) {
	build := func(off int64, target any, immutable bool) []byte {
		vm := bootedVM(t)
		ptrT := vm.ptrType(core.Int64Type)
		p := &Pointer{Type: ptrT, Target: target, Off: off}
		mut := mustType("Main", "NullHost", core.AnyType, false, []expr.Symbol{"p"}, []any{core.AnyType})
		mut.Name.Mutable = !immutable
		vm.singletons[mustType("Main", "NullKey", core.AnyType, false, nil, nil)] = &Struct{Type: mut, Fields: []any{p}}
		mem := newMemory(memAnyType, []any{p})
		vm.singletons[mustType("Main", "NullMem", core.AnyType, false, nil, nil)] = mem
		vm.Main.declare("nullptr", vm.newWorld()).Value = p
		return saveBytes(t, vm)
	}
	m1, m2 := newMemory(memAnyType, []any{int64(1)}), newMemory(memAnyType, []any{int64(2), int64(3)})
	_ = m1
	_ = m2
	// mutable struct, Memory element and binding: the pointer is nulled, so
	// neither where it points nor its offset is in the image
	if !bytes.Equal(build(8, "a", false), build(16, int64(1), false)) {
		t.Error("a pointer in a mutable slot keeps what it pointed at")
	}
	// -1 is kept
	if bytes.Equal(build(-1, "a", false), build(8, "a", false)) {
		t.Error("Ptr(-1) was nulled")
	}
	// in an immutable struct a pointer is saved as it is... in the Memory and
	// the binding it is still nulled, so only the immutable field differs
	if bytes.Equal(build(8, "a", true), build(16, "a", true)) {
		t.Error("a pointer in an immutable field was nulled")
	}
	// a foreign handle in a nulled slot is saved; elsewhere it is refused
	vm := bootedVM(t)
	p := &Pointer{Type: vm.ptrType(core.NothingType), Target: new(pcre2.Code)}
	mut := mustType("Main", "RegexLike", core.AnyType, false, []expr.Symbol{"regex"}, []any{core.AnyType})
	mut.Name.Mutable = true
	vm.singletons[mustType("Main", "RegexKey", core.AnyType, false, nil, nil)] = &Struct{Type: mut, Fields: []any{p}}
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err != nil {
		t.Errorf("a compiled regex in a mutable field: %v", err)
	}
	vm.singletons[mustType("Main", "TupleKey", core.AnyType, false, nil, nil)] = vm.newTuple([]any{p})
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err == nil || !strings.Contains(err.Error(), "a foreign value") {
		t.Errorf("a compiled regex in a tuple: %v", err)
	}
}

// A weak pointer keeps nothing alive: an Expr's args vector is in the image
// when the Expr is, and an entry whose key is reachable only through another
// entry comes in a later round.
func TestImageWeak(t *testing.T) {
	// an Expr reachable from the VM: its vector, and the weak owner field
	vm := bootedVM(t)
	ex1, ex2 := expr.New("call", expr.Symbol("f")), expr.New("call", expr.Symbol("g"))
	empty := saveBytes(t, vm)
	v1 := vm.exprArgs(ex1)
	v2 := vm.exprArgs(ex2)
	// neither Expr is held by the VM: both entries are dropped
	if !bytes.Equal(empty, saveBytes(t, vm)) {
		t.Error("an entry whose key nothing in the VM holds was saved")
	}
	// ex1 held by a binding: its entry is in; ex2 is the key of an entry whose
	// value is held by ex1's vector, so it comes in the second round
	vm.Main.declare("ex1", vm.newWorld()).Value = ex1
	// two more entries ready in the same round, of the same map and of the
	// address table, which come in the order of their maps and then keys
	ex3, held := expr.New("call", expr.Symbol("k")), newMemory(memUInt8Type, make([]any, 2))
	vm.exprArgs(ex3)
	vm.Main.declare("ex3", vm.newWorld()).Value = ex3
	if _, err := vm.addressOf(&Pointer{Type: vm.ptrType(core.UInt8Type), Target: held}); err != nil {
		t.Fatal(err)
	}
	vm.Main.declare("held", vm.newWorld()).Value = held
	with1 := saveBytes(t, vm)
	runtime.KeepAlive(ex3)
	if bytes.Equal(empty, with1) {
		t.Error("the entry of a reachable Expr is not in the image")
	}
	v1.Fields = append(v1.Fields, ex2)
	with2 := saveBytes(t, vm)
	if bytes.Equal(with1, with2) || len(with2) <= len(with1) {
		t.Error("the entry reached in a later round is not in the image")
	}
	// the owner of a vector is saved as a reference to its Expr when the Expr
	// is in the image, else as nil: here only ex1 (and so v1) is
	v1.Fields = v1.Fields[:2]
	vm.Main.declare("v2", vm.newWorld()).Value = v2
	if bytes.Equal(with1, saveBytes(t, vm)) {
		t.Error("a vector the VM holds is not in the image")
	}
	runtime.KeepAlive(ex1)
	runtime.KeepAlive(ex2)

	// an Expr that is gone: its entry cannot be saved, and the save does not
	// fail on it
	vm = bootedVM(t)
	gone := expr.New("call", expr.Symbol("h"))
	keep := vm.exprArgs(gone)
	w := weak.Make(gone)
	gone = nil
	for i := 0; i < 10 && w.Value() != nil; i++ {
		runtime.GC()
	}
	if w.Value() != nil {
		t.Skip("the Expr was not collected")
	}
	vm.argMu.Lock()
	vm.argVectors[w] = keep // the cleanup may have run
	vm.argMu.Unlock()
	if err := vm.SaveImage(io.Discard, SaveOptions{}); err != nil {
		t.Errorf("a weak key whose object is gone: %v", err)
	}
}

// The address table is weak too: a region whose memory is in the image keeps
// its number, one whose memory is not is dropped.
func TestImageAddressRegions(t *testing.T) {
	vm := bootedVM(t)
	held, loose := newMemory(memUInt8Type, make([]any, 4)), newMemory(memUInt8Type, make([]any, 4))
	for _, m := range []*Memory{held, loose} {
		if _, err := vm.addressOf(&Pointer{Type: vm.ptrType(core.UInt8Type), Target: m}); err != nil {
			t.Fatal(err)
		}
	}
	// a region of a target that is not a memory is held as it is
	if _, err := vm.addressOf(&Pointer{Type: vm.ptrType(core.UInt8Type), Target: "text"}); err != nil {
		t.Fatal(err)
	}
	before := saveBytes(t, vm)
	vm.singletons[mustType("Main", "RegionHost", core.AnyType, false, nil, nil)] = held
	after := saveBytes(t, vm)
	if len(after) <= len(before) {
		t.Errorf("held memory: %d bytes, then %d", len(before), len(after))
	}
	runtime.KeepAlive(loose)

	// a region whose target is a C handle (a compiled regex) is left out of
	// the image, with the handle: two handles, one image
	region := func() []byte {
		vm := bootedVM(t)
		if _, err := vm.addressOf(&Pointer{Type: vm.ptrType(core.NothingType), Target: new(pcre2.Code)}); err != nil {
			t.Fatal(err)
		}
		return saveBytes(t, vm)
	}
	if !bytes.Equal(region(), region()) {
		t.Error("the identity of a C handle is in the image")
	}
}

func valueOf(p any) reflect.Value { return reflect.ValueOf(p) }

// TestImageSaveBase saves the VM at the cut where julia writes sys.so (after
// sysimg.jl, before the module initializers): the whole of Base. It takes the
// build's several minutes and as much memory as the walk of imagewalk:
// JLGO_BASE=1.
func TestImageSaveBase(t *testing.T) {
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
	var b bytes.Buffer
	t0 := time.Now()
	if err := vm.SaveImage(&b, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	took := time.Since(t0)
	p := parseImage(t, b.Bytes())
	tiers := map[int]int{}
	for _, k := range p.kinds {
		tiers[k.tier]++
	}
	t.Logf("%d bytes, %d objects (%d static, %d boot, %d task), %d strings, %d types, saved in %v (load-dependent)",
		b.Len(), len(p.kinds), tiers[tierStatic], tiers[tierBoot], tiers[tierTask], len(p.strs), len(p.types), took)
	// the second save is the same bytes
	var again bytes.Buffer
	if err := vm.SaveImage(&again, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b.Bytes(), again.Bytes()) {
		t.Error("two saves of the Base VM differ")
	}
}
