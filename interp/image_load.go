package interp

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"weak"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
)

// LoadImage reads an image SaveImage wrote and returns the VM it describes: a
// fresh VM (New) whose boot objects are written over with the saved content,
// the process-static objects named by their index in core's table, and every
// other object made from its record; the types are interned again through
// core.Intern, so that a type the process already holds is that one; the Go
// funcs are the fresh VM's own, found by number, by name or made again from a
// name and a capture (gobodies.go); what the typemap derives is rebuilt when
// first needed.
//
// An image is read whole, within LoadOptions, and refused before anything is
// allocated for its objects when it is not this interpreter's (ErrImageKey),
// when its checksum fails (ErrImageChecksum), when it ends early
// (ErrImageTruncated), when a section declares more than the image holds or
// more than the limits allow (ErrImageLimit), or when a record does not fit
// the kinds it names (ErrImageCorrupt). The key and the checksum are not a
// signature: an image binds itself to an interpreter and detects damage.
//
// A load is atomic with respect to what the VMs of the process share (the type
// caches of core's names, their hints, the cached instances of their types):
// the image is decoded and validated against structures of its own, and every
// write to shared state is made in one step at the end (commit), after every
// conflict has been looked for. A refused image changes none of it
// (ErrImageConflict when a hint or an instance differs from the process's). A
// valid image from an untrusted source still adds its types to the caches and
// sets hints a process had not set: pin its digest (LoadOptions.Digest).
func LoadImage(r io.Reader, opts LoadOptions) (*VM, error) {
	// read and framed before any VM is made: what is refused there costs no VM
	fr, err := readImage(r, opts, &vmImageSpec)
	if err != nil {
		return nil, err
	}
	vm, err := decodeCommitted(fr, &vmImageSpec)
	if err != nil {
		return nil, err
	}
	return vm, nil
}

// LoadOptions bound a load. A zero field is its default (the same as
// SaveOptions').
type LoadOptions struct {
	// MaxBytes bounds the image read, MaxObjects the objects of its table.
	MaxBytes   int64
	MaxObjects int
	// Digest, when set, pins the SHA-256 of the whole image: the input is read
	// (within MaxBytes) and hashed, and any other bytes are refused with
	// ErrImageDigest before the key, the framing or any object is looked at.
	// A pointer, so that unset (nil, the image is not pinned) cannot be a
	// pinned all-zero digest by accident. An image from an untrusted source
	// must be loaded with its digest (FetchImage and FetchKernel do).
	Digest *[32]byte
}

var (
	// ErrImageKey is an image made by another interpreter: another format
	// version, schema, sources or module version.
	ErrImageKey = errors.New("interp: the image was made by another interpreter")
	// ErrImageChecksum is an image whose bytes are not the ones the trailer
	// hashed.
	ErrImageChecksum = errors.New("interp: the image checksum does not match")
	// ErrImageTruncated is an image that ends before its sections do.
	ErrImageTruncated = errors.New("interp: the image is truncated")
	// ErrImageLimit is an image over LoadOptions, or a section that declares
	// more than the image can hold.
	ErrImageLimit = errors.New("interp: the image is over the load limit")
	// ErrImageCorrupt is an image whose structure is wrong: a reference out of
	// range or of the wrong kind, a type this interpreter does not have.
	ErrImageCorrupt = errors.New("interp: the image is malformed")
	// ErrImageConflict is an image whose hints on core's shared types differ
	// from what this process already holds (another image was loaded).
	ErrImageConflict = errors.New("interp: the image conflicts with this process")
)

// maxImageDepth bounds the nesting of the values one record holds inline.
const maxImageDepth = 256

func (d *imgDec) failf(kind error, format string, a ...any) {
	panic(imgFail{fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, a...))})
}

// ----- the framing: bytes to sections -----

type imgFrame struct {
	types    []string
	kinds    []imgKind
	strs     []string
	recs     [][]byte
	roots    []byte
	hints    []byte
	interned []byte
}

// imgKind is the table entry of an object: the index of its Go type in the
// image's table, its tier, and for a static or boot object its number.
type imgKind struct {
	ti   int
	tier int
	idx  int
}

type frameReader struct{ b []byte }

func (r *frameReader) uv() uint64 {
	x, n := binary.Uvarint(r.b)
	if n <= 0 {
		panic(imgFail{fmt.Errorf("%w: a section ends in the middle of a number", ErrImageTruncated)})
	}
	r.b = r.b[n:]
	return x
}

func (r *frameReader) take(n uint64) []byte {
	if n > uint64(len(r.b)) {
		panic(imgFail{fmt.Errorf("%w: %d bytes declared, %d left", ErrImageTruncated, n, len(r.b))})
	}
	s := r.b[:n]
	r.b = r.b[n:]
	return s
}

// count reads a number of items that each take at least one byte: more than
// the bytes left is a section that declares what it cannot hold.
func (r *frameReader) count() uint64 {
	n := r.uv()
	if n > uint64(len(r.b)) {
		panic(imgFail{fmt.Errorf("%w: %d items declared, %d bytes left", ErrImageTruncated, n, len(r.b))})
	}
	return n
}

// parseFrame splits the body of an image (between the header and the trailer)
// into its sections. With alloc false it only walks them: the check that an
// image that fails its checksum was cut short.
func parseFrame(body []byte, opts LoadOptions, alloc bool) (fr *imgFrame, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = imgRecover(r)
		}
	}()
	rd := &frameReader{b: body}
	fr = &imgFrame{}
	nt := rd.count()
	for i := uint64(0); i < nt; i++ {
		s := rd.take(rd.uv())
		if alloc {
			fr.types = append(fr.types, string(s))
		}
	}
	nk := rd.count()
	if nk > uint64(opts.MaxObjects) {
		return nil, fmt.Errorf("%w: %d objects, the limit is %d", ErrImageLimit, nk, opts.MaxObjects)
	}
	for i := uint64(0); i < nk; i++ {
		x := rd.uv()
		k := imgKind{tier: int(x & 3)}
		if k.tier == tierStatic || k.tier == tierBoot {
			k.idx = int(min(rd.uv(), math.MaxInt32))
		}
		if x>>2 >= nt {
			return nil, fmt.Errorf("%w: object %d has type %d of %d", ErrImageCorrupt, i, x>>2, nt)
		}
		if alloc {
			k.ti = int(x >> 2)
			fr.kinds = append(fr.kinds, k)
		}
	}
	ns := rd.count()
	for i := uint64(0); i < ns; i++ {
		s := rd.take(rd.uv())
		if alloc {
			fr.strs = append(fr.strs, string(s))
		}
	}
	for i := uint64(0); i < nk; i++ {
		s := rd.take(rd.uv())
		if alloc {
			fr.recs = append(fr.recs, s)
		}
	}
	fr.roots = rd.take(rd.uv())
	fr.hints = rd.take(rd.uv())
	fr.interned = rd.take(rd.uv())
	if len(rd.b) != 0 {
		return nil, fmt.Errorf("%w: %d bytes after the last section", ErrImageCorrupt, len(rd.b))
	}
	return fr, nil
}

// readImage reads the image within the limit and checks what can be checked
// before anything is allocated for its objects: the header, the key, the
// checksum, the sections.
func readImage(r io.Reader, opts LoadOptions, spec *imageSpec) (*imgFrame, error) {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxImageBytes
	}
	if opts.MaxObjects <= 0 {
		opts.MaxObjects = DefaultMaxImageObjects
	}
	// a pinned image is read whole and hashed before anything else is looked at
	if opts.Digest != nil {
		all, err := io.ReadAll(io.LimitReader(r, opts.MaxBytes+1))
		if err != nil {
			return nil, err
		}
		if int64(len(all)) > opts.MaxBytes {
			return nil, fmt.Errorf("%w: more than %d bytes", ErrImageLimit, opts.MaxBytes)
		}
		if got := sha256.Sum256(all); got != *opts.Digest {
			return nil, fmt.Errorf("%w: the image is %x, pinned %x", ErrImageDigest, got, *opts.Digest)
		}
		r = bytes.NewReader(all)
	}
	// the header first: a stream that is not this interpreter's image is
	// refused after 76 bytes, however long it goes on
	const header = len(imageMagic) + 4 + 32
	lr := io.LimitReader(r, opts.MaxBytes+1)
	buf := make([]byte, header)
	n, err := io.ReadFull(lr, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	if buf = buf[:n]; n < header {
		return nil, fmt.Errorf("%w: %d bytes, the header is %d", ErrImageTruncated, n, header)
	}
	if string(buf[:len(imageMagic)]) != imageMagic {
		return nil, fmt.Errorf("%w: not an image", ErrImageCorrupt)
	}
	if v := binary.LittleEndian.Uint32(buf[len(imageMagic):]); v != imageVersion {
		return nil, fmt.Errorf("%w: format version %d, this interpreter reads %d", ErrImageKey, v, imageVersion)
	}
	key := imageKey(spec.schemaHash())
	if !bytes.Equal(buf[len(imageMagic)+4:header], key[:]) {
		return nil, fmt.Errorf("%w: its key is not this interpreter's (schema, sources or version)", ErrImageKey)
	}
	rest, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if buf = append(buf, rest...); int64(len(buf)) > opts.MaxBytes {
		return nil, fmt.Errorf("%w: more than %d bytes", ErrImageLimit, opts.MaxBytes)
	}
	if len(buf) < header+32 {
		return nil, fmt.Errorf("%w: %d bytes, the header and the trailer are %d", ErrImageTruncated, len(buf), header+32)
	}
	body, trailer := buf[header:len(buf)-32], buf[len(buf)-32:]
	sum := sha256.Sum256(buf[:len(buf)-32])
	if !bytes.Equal(sum[:], trailer) {
		// a cut image fails its checksum too: say which it is
		if _, err := parseFrame(body, opts, false); errors.Is(err, ErrImageTruncated) {
			return nil, err
		}
		return nil, ErrImageChecksum
	}
	return parseFrame(body, opts, true)
}

// ----- the Go types of an image -----

var scalarTypes = []reflect.Type{
	reflect.TypeFor[bool](), reflect.TypeFor[int](), reflect.TypeFor[int8](), reflect.TypeFor[int16](),
	reflect.TypeFor[int32](), reflect.TypeFor[int64](), reflect.TypeFor[uint](), reflect.TypeFor[uint8](),
	reflect.TypeFor[uint16](), reflect.TypeFor[uint32](), reflect.TypeFor[uint64](), reflect.TypeFor[uintptr](),
	reflect.TypeFor[float32](), reflect.TypeFor[float64](), reflect.TypeFor[string](),
}

// imageRegistry is every Go type an image may name, by the string the encoder
// wrote: the kinds of the spec, the builtin scalars, and what they are made
// of (the types of their fields, the elements of their slices and maps).
// Two types with one name are ambiguous and refused.
type imageRegistry struct {
	byName    map[string]reflect.Type
	ambiguous map[string]bool
}

func newImageRegistry(defs []imageKindDef) *imageRegistry {
	r := &imageRegistry{byName: map[string]reflect.Type{}, ambiguous: map[string]bool{}}
	seen := map[reflect.Type]bool{}
	var add func(t reflect.Type)
	add = func(t reflect.Type) {
		// the sync types are never named by an image (their fields are skipped),
		// and sync.Mutex's own field is another sync.Mutex
		if seen[t] || strings.HasPrefix(t.PkgPath(), "sync") || strings.HasPrefix(t.PkgPath(), "internal/") {
			return
		}
		seen[t] = true
		name := t.String()
		if prev, ok := r.byName[name]; ok && prev != t {
			r.ambiguous[name] = true
		}
		r.byName[name] = t
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			add(t.Elem())
		case reflect.Map:
			add(t.Key())
			add(t.Elem())
		case reflect.Struct:
			for i := 0; i < t.NumField(); i++ {
				add(t.Field(i).Type)
			}
		}
	}
	for _, t := range scalarTypes {
		add(t)
	}
	// types an interface holds and no field names: the keys of the address table
	add(weakMemoryType)
	add(weakExprType)
	for _, d := range defs {
		add(d.t)
	}
	return r
}

var (
	vmRegistryOnce sync.Once
	vmRegistry     *imageRegistry
)

func (s *imageSpec) registry() *imageRegistry {
	if s == &vmImageSpec {
		vmRegistryOnce.Do(func() { vmRegistry = newImageRegistry(s.defs) })
		return vmRegistry
	}
	return newImageRegistry(s.defs)
}

// ----- the decoder -----

type imgDec struct {
	vm   *VM
	spec *imageSpec
	fr   *imgFrame

	types  []reflect.Type // by index in the image's table; nil for the nulled Ptr
	nulled int
	objs   []reflect.Value
	// in holds the types the image made that the process does not: they are
	// put in core's caches by commit, when nothing can fail
	in    *core.Interner
	redir map[uint32]bool // objects replaced by the type the process already holds
	// replaced pairs each type of the image with the one the process holds
	replaced [][2]*core.DataType

	cur    []byte
	depth  int
	fields map[reflect.Type][]encField
}

var (
	tablePtrType    = reflect.TypeFor[*dispatch.Table]()
	sessionPtrType  = reflect.TypeFor[*Session]()
	goBodyPtrType   = reflect.TypeFor[*goBody]()
	dataTypePtrType = reflect.TypeFor[*core.DataType]()
	weakExprType    = reflect.TypeFor[weak.Pointer[expr.Expr]]()
	weakMemoryType  = reflect.TypeFor[weak.Pointer[Memory]]()
	undefValue      = reflect.ValueOf(undef)
)

func (vm *VM) loadImage(r io.Reader, opts LoadOptions, spec *imageSpec) error {
	fr, err := readImage(r, opts, spec)
	if err != nil {
		return err
	}
	return vm.decodeImage(fr, spec)
}

// decodeImage writes the image over a fresh VM.
func (vm *VM) decodeImage(fr *imgFrame, spec *imageSpec) (err error) {
	d := &imgDec{vm: vm, spec: spec, fr: fr, redir: map[uint32]bool{}, fields: map[reflect.Type][]encField{}, in: core.NewInterner()}
	defer func() {
		if r := recover(); r != nil {
			if f, ok := r.(imgFail); ok {
				err = f.err
				return
			}
			err = fmt.Errorf("%w: %v", ErrImageCorrupt, r)
		}
	}()
	d.resolveTypes()
	hints := d.parseHints()
	d.shells()
	// the first pass: every record into its object, the roots into the VM,
	// with references to the types as the image made them
	d.records()
	d.roots()
	// the types the process may already hold, interned, parameters first; the
	// objects that referred to a type it replaced are decoded again
	d.intern()
	d.records()
	d.roots()
	d.finishTask()
	d.rebuild()
	// the image is decoded and valid, against structures of its own; the
	// writes to what the process shares are made now, all or none
	imageDecoded()
	d.commit(hints)
	return nil
}

// imageDecoded is called when an image has been decoded whole and has not
// committed yet: the place a test makes another goroutine's change.
var imageDecoded = func() {}

// maxCommitTries is how many times a load decodes its image again when a type
// cache it read grew before it could commit.
const maxCommitTries = 4

// decodeCommitted decodes fr into a fresh VM, again into another when a
// cache another goroutine grew made the commit refuse.
func decodeCommitted(fr *imgFrame, spec *imageSpec) (vm *VM, err error) {
	for try := 0; try < maxCommitTries; try++ {
		vm = New()
		if err = vm.decodeImage(fr, spec); !core.IsInternChanged(err) {
			break
		}
	}
	return vm, err
}

func (d *imgDec) resolveTypes() {
	reg := d.spec.registry()
	d.nulled = -1
	d.types = make([]reflect.Type, len(d.fr.types))
	for i, name := range d.fr.types {
		if name == nulledPointerType {
			d.nulled = i
			continue
		}
		t, ok := reg.byName[name]
		if !ok || reg.ambiguous[name] {
			d.failf(ErrImageCorrupt, "the image names a Go type %q this interpreter does not have", name)
		}
		d.types[i] = t
	}
}

// ----- records -----

func (d *imgDec) uv() uint64 {
	x, n := binary.Uvarint(d.cur)
	if n <= 0 {
		d.failf(ErrImageCorrupt, "a record ends in the middle of a number")
	}
	d.cur = d.cur[n:]
	return x
}

func (d *imgDec) zz() int64 {
	x, n := binary.Varint(d.cur)
	if n <= 0 {
		d.failf(ErrImageCorrupt, "a record ends in the middle of a number")
	}
	d.cur = d.cur[n:]
	return x
}

func (d *imgDec) take(n int) []byte {
	if n > len(d.cur) {
		d.failf(ErrImageCorrupt, "a record is %d bytes short", n-len(d.cur))
	}
	s := d.cur[:n]
	d.cur = d.cur[n:]
	return s
}

// shells makes the objects the records fill: a pointer's pointee, a slice and
// a map of the size their record states, the static and boot objects that
// exist already, the placeholder of the root task.
func (d *imgDec) shells() {
	statics := core.StaticObjects()
	d.objs = make([]reflect.Value, len(d.fr.kinds))
	for i, k := range d.fr.kinds {
		t := d.types[k.ti]
		if t == nil {
			d.failf(ErrImageCorrupt, "object %d has the type of a nulled Ptr", i)
		}
		rec := d.fr.recs[i]
		switch k.tier {
		case tierStatic:
			if k.idx >= len(statics) || len(rec) != 0 {
				d.failf(ErrImageCorrupt, "static object %d: index %d of %d, a record of %d bytes", i, k.idx, len(statics), len(rec))
			}
			d.objs[i] = reflect.ValueOf(statics[k.idx])
		case tierBoot:
			if k.idx >= len(d.vm.bootList) {
				d.failf(ErrImageCorrupt, "boot object %d: index %d of %d", i, k.idx, len(d.vm.bootList))
			}
			d.objs[i] = d.vm.bootList[k.idx]
		case tierTask:
			if t != structPtrType {
				d.failf(ErrImageCorrupt, "the root task is a %s", t)
			}
			d.objs[i] = reflect.ValueOf(&Struct{})
		default: // tierFull: the tier has two bits and the others are named above
			d.objs[i] = d.shell(i, t, rec)
		}
		if d.objs[i].Type() != t {
			d.failf(ErrImageCorrupt, "object %d is a %s, the image says %s", i, d.objs[i].Type(), t)
		}
	}
}

func (d *imgDec) shell(i int, t reflect.Type, rec []byte) reflect.Value {
	switch t.Kind() {
	case reflect.Pointer:
		return reflect.New(t.Elem())
	case reflect.Slice, reflect.Map:
		n, w := binary.Uvarint(rec)
		if w <= 0 || n > uint64(len(rec)-w) {
			d.failf(ErrImageCorrupt, "object %d: %d elements in a record of %d bytes", i, n, len(rec))
		}
		if t.Kind() == reflect.Slice {
			return reflect.MakeSlice(t, int(n), int(n))
		}
		return reflect.MakeMapWithSize(t, int(n))
	}
	d.failf(ErrImageCorrupt, "object %d of kind %s", i, t.Kind())
	return reflect.Value{}
}

// records fills every object from its record.
func (d *imgDec) records() {
	for i, k := range d.fr.kinds {
		if k.tier == tierStatic || k.tier == tierTask || d.redir[uint32(i)] {
			continue
		}
		d.record(i)
	}
}

func (d *imgDec) record(i int) {
	o := d.objs[i]
	d.cur, d.depth = d.fr.recs[i], 0
	switch o.Kind() {
	case reflect.Pointer:
		d.value(o.Elem())
	case reflect.Slice:
		d.uv() // the length: the shell was made of it
		for j := 0; j < o.Len(); j++ {
			d.value(o.Index(j))
		}
	case reflect.Map:
		n := d.uv()
		o.Clear()
		kt, vt := o.Type().Key(), o.Type().Elem()
		for j := uint64(0); j < n; j++ {
			k, v := reflect.New(kt).Elem(), reflect.New(vt).Elem()
			d.value(k)
			d.value(v)
			o.SetMapIndex(k, v)
		}
	}
	if len(d.cur) != 0 {
		d.failf(ErrImageCorrupt, "object %d: %d bytes after its record", i, len(d.cur))
	}
}

func (d *imgDec) structFields(t reflect.Type) []encField {
	if f, ok := d.fields[t]; ok {
		return f
	}
	f := make([]encField, t.NumField())
	for i := range f {
		f[i].name = t.String() + "." + t.Field(i).Name
		_, f[i].skip = d.spec.skip[f[i].name]
	}
	d.fields[t] = f
	return f
}

func (d *imgDec) value(v reflect.Value) {
	if d.depth++; d.depth > maxImageDepth {
		d.failf(ErrImageCorrupt, "values nested more than %d deep", maxImageDepth)
	}
	d.value1(v)
	d.depth--
}

func (d *imgDec) value1(v reflect.Value) {
	switch v.Kind() {
	case reflect.Bool:
		x := d.uv()
		if x > 1 {
			d.failf(ErrImageCorrupt, "a Bool of %d", x)
		}
		v.SetBool(x == 1)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		x := d.zz()
		if v.OverflowInt(x) {
			d.failf(ErrImageCorrupt, "%d does not fit a %s", x, v.Type())
		}
		v.SetInt(x)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		x := d.uv()
		if v.OverflowUint(x) {
			d.failf(ErrImageCorrupt, "%d does not fit a %s", x, v.Type())
		}
		v.SetUint(x)
	case reflect.Float32, reflect.Float64:
		f := math.Float64frombits(binary.LittleEndian.Uint64(d.take(8)))
		if v.OverflowFloat(f) {
			d.failf(ErrImageCorrupt, "%g does not fit a %s", f, v.Type())
		}
		v.SetFloat(f)
	case reflect.String:
		i := d.uv()
		if i >= uint64(len(d.fr.strs)) {
			d.failf(ErrImageCorrupt, "string %d of %d", i, len(d.fr.strs))
		}
		v.SetString(d.fr.strs[i])
	case reflect.Pointer, reflect.Map, reflect.Slice:
		v.Set(d.ref(v.Type()))
	case reflect.Interface:
		d.iface(v)
	case reflect.Struct:
		d.structv(v)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			d.value(v.Index(i))
		}
	case reflect.Func:
		// a func is the loading VM's own: the presence byte only says there was one
		if x := d.uv(); x > 1 {
			d.failf(ErrImageCorrupt, "a func flag of %d", x)
		}
	case reflect.Chan, reflect.UnsafePointer:
		// written as nothing
	default:
		d.failf(ErrImageCorrupt, "a value of kind %s", v.Kind())
	}
}

// ref reads a reference and returns the object it names, as a value of want,
// or the zero of want for nil; 1 is the undef sentinel.
func (d *imgDec) ref(want reflect.Type) reflect.Value {
	x := d.uv()
	switch x {
	case 0:
		return reflect.Zero(want)
	case 1:
		if !undefValue.Type().AssignableTo(want) {
			d.failf(ErrImageCorrupt, "undef where a %s is held", want)
		}
		return undefValue
	}
	if x-2 >= uint64(len(d.objs)) {
		d.failf(ErrImageCorrupt, "reference to object %d of %d", x-2, len(d.objs))
	}
	o := d.objs[x-2]
	if !o.Type().AssignableTo(want) {
		d.failf(ErrImageCorrupt, "a %s where a %s is held", o.Type(), want)
	}
	return o
}

func (d *imgDec) iface(v reflect.Value) {
	tag := d.uv()
	if tag == 0 {
		v.SetZero()
		return
	}
	if tag-1 >= uint64(len(d.types)) {
		d.failf(ErrImageCorrupt, "type %d of %d", tag-1, len(d.types))
	}
	if int(tag-1) == d.nulled {
		// a Ptr in a mutable slot is C_NULL in an image, with its type
		dt, _ := d.ref(dataTypePtrType).Interface().(*core.DataType)
		d.set(v, reflect.ValueOf(&Pointer{Type: dt}))
		return
	}
	t := d.types[tag-1]
	switch t.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		d.set(v, d.ref(t))
	case reflect.Func:
		if x := d.uv(); x > 1 {
			d.failf(ErrImageCorrupt, "a func flag of %d", x)
		}
		if v.IsNil() || v.Elem().Type() != t {
			d.failf(ErrImageCorrupt, "a Go func %s the loading VM does not hold here", t)
		}
	default:
		nv := reflect.New(t).Elem()
		d.value(nv)
		d.set(v, nv)
	}
}

func (d *imgDec) set(v, x reflect.Value) {
	if !x.Type().AssignableTo(v.Type()) {
		d.failf(ErrImageCorrupt, "a %s where a %s is held", x.Type(), v.Type())
	}
	v.Set(x)
}

func (d *imgDec) structv(v reflect.Value) {
	t := v.Type()
	switch {
	case t.PkgPath() == "weak":
		d.weak(v)
		return
	case t.PkgPath() == "sync":
		return
	case t.PkgPath() == "sync/atomic" && strings.HasPrefix(t.Name(), "Pointer["):
		pt := t.Field(0).Type.Elem() // atomic.Pointer[T]'s [0]*T: the *T
		v.Addr().MethodByName("Store").Call([]reflect.Value{d.ref(pt)})
		return
	}
	fs := d.structFields(t)
	for i := range fs {
		if !fs[i].skip {
			d.value(rw(v.Field(i)))
		}
	}
}
