package interp

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
)

// SaveImage writes a system image: the object graph of the VM, as julia's
// sys.so is the heap reachable from its tags (design of #40, section 4).
//
//	magic "JLGOIMG\0" | format version u32 | key [32]byte
//	TYPES    n, then n type descriptors (the Go type of each kind), sorted
//	KINDS    N, then per object: type<<2|tier [, index of a static or boot object]
//	STRINGS  m, then m strings, sorted: every string and symbol once
//	RECORDS  N records in id order, each its byte length and its fields
//	ROOTS    the VM's saved fields by name, in the order they are declared
//	trailer  SHA-256 of everything above
//
// An object is a pointer, a map or a slice; a value boxed in an interface is
// written inline after the number of its type. A reference is 0 (nil), 1 (the
// undef sentinel) or the id of the object plus 2. Ids are assigned by a
// breadth-first walk from the roots in a fixed order, so the same VM, or a VM
// that ran the same program, gives the same bytes. An object is
//
//   - static: core's (core.StaticIndex), named by its index, with no record;
//   - boot: made by New() (vm.bootIdx), named by its index in the enumeration
//     of the objects New made, and with its content, which a loader writes
//     over the fresh object;
//   - the root task: named, and with only its task-local storage;
//   - anything else: a full record.
//
// What is not saved is in the tables of image_spec.go. A Ptr in a mutable
// field, a Memory element or a binding is saved as C_NULL (except -1), as
// julia saves it (staticdata.c:676-683); a foreign value anywhere else, a Task
// other than the root, or a Go type, func or weak pointer no table knows,
// refuses the save, naming the path. Maps keyed by an object are written in
// the order of a canonical content key of the key; two keys with one content
// key are an error; a map keyed by a weak pointer keeps the entries whose key
// is reachable otherwise, in the order of the key's id.
func (vm *VM) SaveImage(w io.Writer, opts SaveOptions) error {
	return vm.saveImage(w, opts, &vmImageSpec)
}

// SaveOptions bound a save. A zero field is its default.
type SaveOptions struct {
	// MaxBytes bounds the image, MaxObjects the objects of its table.
	MaxBytes   int64
	MaxObjects int
}

// The defaults of SaveOptions.
const (
	DefaultMaxImageBytes   = 1 << 31
	DefaultMaxImageObjects = 1 << 24
)

const (
	imageMagic   = "JLGOIMG\x00"
	imageVersion = 2
)

// The tier of an object.
const (
	tierFull   = 0
	tierStatic = 1
	tierBoot   = 2
	tierTask   = 3
)

var (
	// ErrImageTooLarge is a save over SaveOptions' limits.
	ErrImageTooLarge = errors.New("interp: the image is over the size limit")
	// ErrImageUnsaveable is a VM that cannot be saved as it is, or a value the
	// format has no kind for.
	ErrImageUnsaveable = errors.New("interp: the VM cannot be saved as an image")
)

type imgKey struct {
	p uintptr
	t reflect.Type
	n int
}

type imgObj struct {
	t     reflect.Type
	v     reflect.Value
	key   imgKey
	owner string
	from  int32 // the object it was first reached from, or -1-root index
	tier  uint8
	tidx  int
	// nullElems: a Ptr among the elements of this slice is saved as C_NULL
	nullElems bool
}

type imgEntry struct {
	k, v reflect.Value
	sk   string
}

type imgPending struct {
	mapID uint32
	imgEntry
	keyWeak, valWeak bool
}

type encSeg struct {
	t     reflect.Type
	field int
	idx   int
	tag   byte
}

type encField struct {
	name     string
	skip     bool
	nullPtr  bool
	structFl bool // Struct.Fields, nulled when the struct is mutable
}

type imgFail struct{ err error }

type imgOut struct {
	w   io.Writer
	h   hash.Hash
	n   int64
	max int64
}

func (o *imgOut) write(b []byte) {
	o.n += int64(len(b))
	if o.n > o.max {
		panic(imgFail{fmt.Errorf("%w: more than %d bytes", ErrImageTooLarge, o.max)})
	}
	o.h.Write(b)
	if _, err := o.w.Write(b); err != nil {
		panic(imgFail{err})
	}
}

func (o *imgOut) uv(x uint64) {
	var b [binary.MaxVarintLen64]byte
	o.write(b[:binary.PutUvarint(b[:], x)])
}

func (o *imgOut) str(s string) {
	o.uv(uint64(len(s)))
	o.write([]byte(s))
}

type imgEnc struct {
	vm      *VM
	spec    *imageSpec
	opts    SaveOptions
	boot    bool // enumerating what New made: classification does not refuse
	writing bool

	ids  map[imgKey]uint32
	objs []imgObj
	next int

	strs    map[string]struct{}
	strIdx  map[string]uint32
	typs    map[string]struct{}
	typIdx  map[string]uint32
	mapEnt  map[uint32][]imgEntry
	pending []imgPending

	buf  []byte
	cur  int32
	root string

	path       []encSeg
	fields     map[reflect.Type][]encField
	known      map[reflect.Type]bool
	nullPtr    bool
	curMutable bool
	rootNames  []string
}

var (
	typeNamePtrType = reflect.TypeFor[*core.TypeName]()
	structPtrType   = reflect.TypeFor[*Struct]()
	pointerPtrType  = reflect.TypeFor[*Pointer]()
	undefAddr       = reflect.ValueOf(undef).Pointer()
)

const nulledPointerType = "!nulled Ptr"

func newImgEnc(vm *VM, spec *imageSpec, opts SaveOptions) *imgEnc {
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = DefaultMaxImageBytes
	}
	if opts.MaxObjects <= 0 {
		opts.MaxObjects = DefaultMaxImageObjects
	}
	return &imgEnc{vm: vm, spec: spec, opts: opts, ids: map[imgKey]uint32{}, strs: map[string]struct{}{},
		typs: map[string]struct{}{nulledPointerType: {}}, mapEnt: map[uint32][]imgEntry{},
		fields: map[reflect.Type][]encField{}, known: map[reflect.Type]bool{}}
}

// imgRecover is the error of a panic the encoder raised; any other panic is a
// defect, and goes on.
func imgRecover(r any) error {
	f, ok := r.(imgFail)
	if !ok {
		panic(r)
	}
	return f.err
}

func (e *imgEnc) failf(format string, a ...any) {
	panic(imgFail{fmt.Errorf("%w: %s\n  at %s", ErrImageUnsaveable, fmt.Sprintf(format, a...), e.where())})
}

// where is the path to the value being walked: the chain of objects it was
// reached through (first and last steps), then the fields within the record.
func (e *imgEnc) where() string {
	var chain []string
	for i := e.cur; ; {
		if i < 0 {
			chain = append(chain, "VM."+e.rootAt(i))
			break
		}
		o := e.objs[i]
		chain = append(chain, o.t.String())
		i = o.from
	}
	// a path through a Base build is hundreds of objects deep: keep where it
	// started and the last steps before the value
	const head, tail = 4, 16
	var b strings.Builder
	for i := len(chain) - 1; i >= 0; i-- {
		if n, at := len(chain), len(chain)-1-i; n > head+tail && at >= head && at < n-tail {
			if at == head {
				fmt.Fprintf(&b, "... (%d objects) ... > ", n-head-tail)
			}
			continue
		}
		b.WriteString(chain[i])
		if i > 0 {
			b.WriteString(" > ")
		}
	}
	for _, s := range e.path {
		switch s.tag {
		case 'f':
			b.WriteString(" > " + s.t.String() + "." + s.t.Field(s.field).Name)
		case 'i':
			fmt.Fprintf(&b, "[%d]", s.idx)
		case 'k':
			b.WriteString(" > [map key]")
		case 'v':
			b.WriteString(" > [map value]")
		}
	}
	return b.String()
}

func (e *imgEnc) rootAt(i int32) string {
	n := int(-1 - i)
	if n >= 0 && n < len(e.rootNames) {
		return e.rootNames[n]
	}
	return e.root
}

// ----- output of values (the second pass) -----

func (e *imgEnc) uv(x uint64) {
	if e.writing {
		e.buf = binary.AppendUvarint(e.buf, x)
	}
}

func (e *imgEnc) zz(x int64) {
	if e.writing {
		e.buf = binary.AppendVarint(e.buf, x)
	}
}

func (e *imgEnc) str(s string) {
	if !e.writing {
		e.strs[s] = struct{}{}
		return
	}
	e.uv(uint64(e.strIdx[s]))
}

func (e *imgEnc) typeTag(name string) uint64 {
	if !e.writing {
		e.typs[name] = struct{}{}
		return 0
	}
	return uint64(e.typIdx[name]) + 1
}

// ----- the walk, which both passes make -----

func (e *imgEnc) push(s encSeg) { e.path = append(e.path, s) }
func (e *imgEnc) pop()          { e.path = e.path[:len(e.path)-1] }

// kindOK reports whether the type behind a pointer or an interface is in the
// kind table. Builtin scalars and unnamed composites are known: their parts
// are walked. The boot enumeration only needs identity and accepts all.
func (e *imgEnc) kindOK(t reflect.Type, always bool) bool {
	if e.boot {
		return true
	}
	if ok, done := e.known[t]; done {
		return ok
	}
	var ok bool
	switch {
	case !always && t.PkgPath() == "":
		ok = true
	case t.PkgPath() == "weak":
		ok = true // classified by the field that holds it (imageWeaks)
	default:
		_, ok = e.spec.kinds[t.String()]
	}
	e.known[t] = ok
	return ok
}

func (e *imgEnc) structFields(t reflect.Type) []encField {
	if f, ok := e.fields[t]; ok {
		return f
	}
	f := make([]encField, t.NumField())
	for i := range f {
		f[i].name = t.String() + "." + t.Field(i).Name
		_, f[i].skip = e.spec.skip[f[i].name]
		f[i].nullPtr = f[i].name == "interp.Memory.Elems" || f[i].name == "interp.Binding.Value"
		f[i].structFl = f[i].name == "interp.Struct.Fields"
	}
	e.fields[t] = f
	return f
}

// rw gives a value read from a struct field the access reflect denies it when
// the field is unexported: the same memory, as a value that may be read.
func rw(f reflect.Value) reflect.Value {
	if f.CanInterface() {
		return f
	}
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

func (e *imgEnc) value(v reflect.Value, owner string) {
	switch v.Kind() {
	case reflect.Bool:
		if v.Bool() {
			e.uv(1)
		} else {
			e.uv(0)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.zz(v.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		e.uv(v.Uint())
	case reflect.Float32, reflect.Float64:
		if e.writing {
			e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v.Float()))
		}
	case reflect.String:
		e.str(v.String())
	case reflect.Pointer, reflect.Map, reflect.Slice:
		e.ref(v, owner)
	case reflect.Interface:
		e.iface(v, owner)
	case reflect.Struct:
		e.structv(v, owner)
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			e.push(encSeg{idx: i, tag: 'i'})
			e.value(v.Index(i), owner)
			e.pop()
		}
	case reflect.Func:
		// the image holds no func: a loaded VM finds it in its own boot
		if v.IsNil() {
			e.uv(0)
			return
		}
		if _, ok := e.spec.funcs[owner]; !ok && !e.boot {
			e.failf("a Go func at %s no table classifies", owner)
		}
		e.uv(1)
	case reflect.Chan:
		if !v.IsNil() && !e.boot {
			e.failf("a chan at %s", owner)
		}
	case reflect.UnsafePointer:
		if v.Pointer() != 0 && !e.boot {
			e.failf("an unsafe.Pointer at %s", owner)
		}
	default:
		e.failf("a %s at %s", v.Kind(), owner)
	}
}

func (e *imgEnc) iface(v reflect.Value, owner string) {
	if v.IsNil() {
		e.uv(0)
		return
	}
	el := v.Elem()
	dt := el.Type()
	switch el.Kind() {
	case reflect.Pointer:
		if !e.kindOK(dt, true) {
			e.failf("the image format has no kind for %s", dt)
		}
	case reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.String, reflect.UnsafePointer:
		if dt.PkgPath() != "" && !e.kindOK(dt, true) {
			e.failf("the image format has no kind for %s", dt)
		}
	default:
		if !e.kindOK(dt, false) {
			e.failf("the image format has no kind for %s", dt)
		}
	}
	if e.nullPtr && dt == pointerPtrType {
		if p := el.Interface().(*Pointer); p.Off != -1 {
			// a Ptr in a mutable slot is C_NULL in an image, with its type
			e.uv(e.typeTag(nulledPointerType))
			e.ref(reflect.ValueOf(p.Type), owner)
			return
		}
	}
	e.uv(e.typeTag(dt.String()))
	switch el.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice:
		e.ref(el, owner)
	default:
		e.value(el, owner)
	}
}

func (e *imgEnc) structv(v reflect.Value, owner string) {
	t := v.Type()
	switch {
	case t.PkgPath() == "weak":
		e.weak(v, owner)
		return
	case t.PkgPath() == "sync":
		return
	case t.PkgPath() == "sync/atomic" && strings.HasPrefix(t.Name(), "Pointer["):
		// atomic.Pointer[T]: a pointer to the T
		if e.boot && owner == typeInstanceField {
			// what running code caches on a type of core's process-wide
			// caches (typeInstance) is not something New made: a fresh VM and
			// one made after a long session must number the same objects
			return
		}
		p := v.FieldByName("v").UnsafePointer()
		if p == nil {
			e.uv(0)
			return
		}
		e.ref(reflect.NewAt(t.Field(0).Type.Elem().Elem(), p), t.String())
		return
	}
	if !v.CanAddr() {
		c := reflect.New(t).Elem()
		c.Set(v)
		v = c
	}
	fs := e.structFields(t)
	for i := range fs {
		if fs[i].skip {
			continue
		}
		e.push(encSeg{t: t, field: i, tag: 'f'})
		old := e.nullPtr
		if fs[i].nullPtr || fs[i].structFl && e.curMutable {
			e.nullPtr = true
		}
		e.value(rw(v.Field(i)), fs[i].name)
		e.nullPtr = old
		e.pop()
	}
}

// weakTarget is the pointer a weak.Pointer holds, or an invalid value when
// its object is gone.
func weakTarget(v reflect.Value) reflect.Value {
	if v.Kind() == reflect.Interface {
		v = v.Elem()
	}
	p := v.MethodByName("Value").Call(nil)[0]
	if p.IsNil() {
		return reflect.Value{}
	}
	return p
}

// foreignVal reports a pointer to a foreign kind (a compiled regex), held
// directly or in an interface: the address table keys such regions by it.
func (e *imgEnc) foreignVal(v reflect.Value) bool {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	return v.Kind() == reflect.Pointer && e.spec.kinds[v.Type().String()] == kindForeign
}

func isWeakVal(v reflect.Value) bool {
	if v.Kind() == reflect.Interface {
		if v.IsNil() {
			return false
		}
		v = v.Elem()
	}
	return v.Kind() == reflect.Struct && v.Type().PkgPath() == "weak"
}

// weak writes a weak pointer as a reference to its target when the target is
// in the image, else as nil: it keeps nothing alive.
func (e *imgEnc) weak(v reflect.Value, owner string) {
	if _, ok := e.spec.weaks[owner]; !ok && !e.boot {
		e.failf("a weak pointer at %s no table classifies", owner)
	}
	if !e.writing {
		return
	}
	if id, ok := e.idOf(weakTarget(v)); ok {
		e.uv(uint64(id) + 2)
		return
	}
	e.uv(0)
}

func (e *imgEnc) idOf(p reflect.Value) (uint32, bool) {
	if !p.IsValid() {
		return 0, false
	}
	id, ok := e.ids[imgKey{p: p.Pointer(), t: p.Type()}]
	return id, ok
}

// ref writes a reference to the pointer, map or slice v; in the first pass
// it makes the object when it is new.
func (e *imgEnc) ref(v reflect.Value, owner string) {
	if v.IsNil() {
		e.uv(0)
		return
	}
	var key imgKey
	switch v.Kind() {
	case reflect.Pointer:
		if v.Pointer() == undefAddr {
			e.uv(1)
			return
		}
		key = imgKey{p: v.Pointer(), t: v.Type()}
	case reflect.Slice:
		key = imgKey{p: v.Pointer(), t: v.Type(), n: v.Len()}
	default:
		key = imgKey{p: v.Pointer(), t: v.Type(), n: -1}
	}
	id, ok := e.ids[key]
	if !ok {
		if e.writing {
			e.failf("an object of %s was not found in the second pass", v.Type())
		}
		id = e.newObject(key, v, owner)
	}
	e.uv(uint64(id) + 2)
}

// newObject adds an object to the table and decides its tier.
func (e *imgEnc) newObject(key imgKey, v reflect.Value, owner string) uint32 {
	if len(e.objs) >= e.opts.MaxObjects {
		panic(imgFail{fmt.Errorf("%w: more than %d objects", ErrImageTooLarge, e.opts.MaxObjects)})
	}
	// the elements of a slice held by a mutable struct, a Memory or a binding
	// are in a mutable slot too
	o := imgObj{t: v.Type(), v: v, key: key, owner: owner, from: e.cur, nullElems: e.nullPtr}
	if v.Kind() == reflect.Pointer {
		if !e.kindOK(o.t, true) {
			e.failf("the image format has no kind for %s", o.t)
		}
		if !e.boot && e.spec.kinds[o.t.String()] == kindForeign {
			e.failf("a foreign value %s is saved only as C_NULL in a mutable field, memory element or binding", o.t)
		}
		if i, ok := core.StaticIndex(v.Interface()); ok {
			o.tier, o.tidx = tierStatic, i
		} else if o.t == structPtrType {
			x := v.Interface().(*Struct)
			switch {
			case x == e.vm.task && x != nil:
				o.tier = tierTask
			case e.vm.tTask != nil && x.Type == e.vm.tTask:
				e.failf("a Task other than the root cannot be serialized")
			}
		}
		if o.tier == tierFull {
			if i, ok := e.vm.bootIdx[key]; ok {
				o.tier, o.tidx = tierBoot, int(i)
			}
		}
	}
	id := uint32(len(e.objs))
	e.ids[key] = id
	e.objs = append(e.objs, o)
	e.typs[o.t.String()] = struct{}{}
	return id
}

// ----- records -----

func (e *imgEnc) record(i int) {
	o := e.objs[i]
	e.cur = int32(i)
	e.path = e.path[:0]
	e.nullPtr, e.curMutable = false, false
	if o.tier == tierStatic {
		return
	}
	switch o.v.Kind() {
	case reflect.Pointer:
		rv := o.v.Elem()
		if o.t == structPtrType {
			x := o.v.Interface().(*Struct)
			if o.tier == tierTask {
				e.task(x)
				return
			}
			e.curMutable = x.Type.Name.Mutable
		}
		e.value(rv, "")
	case reflect.Slice:
		e.nullPtr = o.nullElems
		e.uv(uint64(o.v.Len()))
		for j := 0; j < o.v.Len(); j++ {
			e.push(encSeg{idx: j, tag: 'i'})
			e.value(o.v.Index(j), o.owner)
			e.pop()
		}
	case reflect.Map:
		e.mapRecord(uint32(i), o)
	}
}

// task writes the root task: only its task-local storage, which a loader sets
// on its own root task (staticdata.c:3220, 3403, 3910).
func (e *imgEnc) task(x *Struct) {
	for i, n := range e.vm.tTask.Name.FieldNames {
		if n == "storage" {
			e.push(encSeg{t: structPtrType.Elem(), field: 1, tag: 'f'})
			e.value(reflect.ValueOf(&x.Fields[i]).Elem(), "interp.Struct.Fields")
			e.pop()
			return
		}
	}
	e.failf("Core.Task has no storage field")
}

func (e *imgEnc) mapRecord(id uint32, o imgObj) {
	if e.writing {
		ents := e.mapEnt[id]
		e.uv(uint64(len(ents)))
		for _, en := range ents {
			e.push(encSeg{tag: 'k'})
			e.value(en.k, o.owner+"[key]")
			e.pop()
			e.push(encSeg{tag: 'v'})
			e.value(en.v, o.owner)
			e.pop()
		}
		return
	}
	it := o.v.MapRange()
	var strong []imgEntry
	for it.Next() {
		k, v := it.Key(), it.Value()
		if e.foreignVal(k) || e.foreignVal(v) {
			continue // a C handle is not in an image: nor is the entry that holds it
		}
		kw, vw := isWeakVal(k), isWeakVal(v)
		if kw || vw {
			e.pending = append(e.pending, imgPending{mapID: id, imgEntry: imgEntry{k: k, v: v}, keyWeak: kw, valWeak: vw})
			continue
		}
		strong = append(strong, imgEntry{k: k, v: v, sk: e.mapKey(k, o.owner)})
	}
	sort.Slice(strong, func(i, j int) bool { return strong[i].sk < strong[j].sk })
	for i := 1; i < len(strong); i++ {
		if strong[i].sk == strong[i-1].sk {
			e.failf("two keys of the map %s print alike (%q): no canonical order", o.t, strong[i].sk)
		}
	}
	e.mapEnt[id] = strong
	for _, en := range strong {
		e.entry(en, o.owner)
	}
}

func (e *imgEnc) entry(en imgEntry, owner string) {
	e.push(encSeg{tag: 'k'})
	e.value(en.k, owner+"[key]")
	e.pop()
	e.push(encSeg{tag: 'v'})
	e.value(en.v, owner)
	e.pop()
}

// mapKey is the canonical content key of a map key: what orders the entries,
// the same in every run and every process. Keys with no content of their own
// (an object nothing names) have none, and the map refuses the save.
func (e *imgEnc) mapKey(k reflect.Value, owner string) string {
	var b strings.Builder
	e.keyOf(&b, k, owner)
	return b.String()
}

func (e *imgEnc) keyOf(b *strings.Builder, k reflect.Value, owner string) {
	switch k.Kind() {
	case reflect.String:
		b.WriteString("s" + strconv.Itoa(len(k.String())) + ":" + k.String())
	case reflect.Bool:
		b.WriteString("b" + strconv.FormatBool(k.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(k.Int())^1<<63)
		b.WriteString("i")
		b.Write(n[:])
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], k.Uint())
		b.WriteString("u")
		b.Write(n[:])
	case reflect.Interface:
		if k.IsNil() {
			b.WriteString("n")
			return
		}
		b.WriteString("<" + k.Elem().Type().String() + ">")
		e.keyOf(b, k.Elem(), owner)
	case reflect.Struct:
		if !k.CanAddr() {
			c := reflect.New(k.Type()).Elem()
			c.Set(k)
			k = c
		}
		b.WriteString("{")
		for i := 0; i < k.NumField(); i++ {
			e.keyOf(b, rw(k.Field(i)), owner)
			b.WriteString(",")
		}
		b.WriteString("}")
	case reflect.Pointer:
		switch x := k.Interface().(type) {
		case *core.DataType:
			b.WriteString("T" + typeContentKey(x))
		case *core.TypeName:
			b.WriteString("N" + x.Module + "." + x.Name + "|" + moduleRefPath(x.ModuleRef))
		case *Module:
			b.WriteString("M" + modulePath(x))
		case *dispatch.Method:
			b.WriteString("m" + showValue(x.Sig) + "|" + strconv.FormatUint(x.PrimaryWorld, 10) + "|" + strconv.Itoa(x.Index))
		default:
			e.failf("the map %s is keyed by a %s, which has no canonical order", owner, k.Type())
		}
	default:
		e.failf("the map %s has keys of kind %s, which have no canonical order", owner, k.Kind())
	}
}

// typeContentKey is a type's full printed form and its defining module.
func typeContentKey(t *core.DataType) string {
	return showValue(t) + "|" + t.Name.Module + "|" + moduleRefPath(t.Name.ModuleRef)
}

func moduleRefPath(m any) string {
	if mod, ok := m.(*Module); ok {
		return modulePath(mod)
	}
	return ""
}

// modulePath is a module's name with its parents', outermost first.
func modulePath(m *Module) string {
	if m == nil {
		return ""
	}
	if m.Parent == nil {
		return string(m.Name)
	}
	return modulePath(m.Parent) + "." + string(m.Name)
}

// ----- discovery: the first pass -----

// discover walks the roots and then the object table, in order, assigning
// every object its id; then the entries of the maps that hold weak pointers
// whose targets turned out reachable, until none is left.
func (e *imgEnc) discover() {
	e.roots()
	e.hints()
	e.drain()
	for e.weakRound() {
		e.drain()
	}
}

func (e *imgEnc) drain() {
	for ; e.next < len(e.objs); e.next++ {
		e.record(e.next)
	}
}

// weakRound includes the pending map entries whose weak pointers' targets are
// in the table, in the order of their map and then of their key, and reports
// whether there were any.
func (e *imgEnc) weakRound() bool {
	var ready, rest []imgPending
	for _, p := range e.pending {
		live, ok := true, true
		for _, side := range []struct {
			weak bool
			v    reflect.Value
		}{{p.keyWeak, p.k}, {p.valWeak, p.v}} {
			if !side.weak {
				continue
			}
			t := weakTarget(side.v)
			if !t.IsValid() {
				live = false
				break
			}
			if _, found := e.idOf(t); !found {
				ok = false
			}
		}
		switch {
		case !live:
		case ok:
			ready = append(ready, p)
		default:
			rest = append(rest, p)
		}
	}
	e.pending = rest
	if len(ready) == 0 {
		return false
	}
	for i := range ready {
		p := &ready[i]
		e.cur = int32(p.mapID)
		if p.keyWeak {
			id, _ := e.idOf(weakTarget(p.k))
			p.sk = fmt.Sprintf("w%010d", id)
		} else {
			p.sk = e.mapKey(p.k, e.objs[p.mapID].owner)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].mapID != ready[j].mapID {
			return ready[i].mapID < ready[j].mapID
		}
		return ready[i].sk < ready[j].sk
	})
	for _, p := range ready {
		e.cur = int32(p.mapID)
		e.path = e.path[:0]
		e.mapEnt[p.mapID] = append(e.mapEnt[p.mapID], p.imgEntry)
		e.entry(p.imgEntry, e.objs[p.mapID].owner)
	}
	return true
}

// roots writes the saved fields of the VM in the order they are declared.
func (e *imgEnc) roots() {
	rv := reflect.ValueOf(e.vm).Elem()
	rt := rv.Type()
	label := rt.String()
	var saved []int
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		isSaved, ok := e.spec.roots[name]
		if !ok && !e.boot {
			e.root = name
			e.cur = -1
			e.failf("%s.%s is a field no table classifies", label, name)
		}
		if isSaved {
			saved = append(saved, i)
		}
	}
	if !e.writing {
		e.rootNames = e.rootNames[:0]
		for _, i := range saved {
			e.rootNames = append(e.rootNames, rt.Field(i).Name)
		}
	}
	e.uv(uint64(len(saved)))
	for n, i := range saved {
		name := rt.Field(i).Name
		e.root, e.cur = name, int32(-1-n)
		e.path = e.path[:0]
		e.str(name)
		e.value(rw(rv.Field(i)), label+"."+name)
	}
}

// typeInstanceField names the field of core.DataType that holds the cached
// instance of a type (core.DataType.SetCachedInstance): the boot enumeration
// does not follow it.
const typeInstanceField = "core.DataType.instance"

// enumerateBoot numbers the objects New made, in the order of the walk the
// encoder makes: a pointer reached from the roots that is not process-static
// is boot object i of the VM, in every VM New makes, so that an image can
// name it and a loader find it in its own fresh VM (design section 3.1,
// tier b). Maps and slices are not named: a loader writes the saved content
// of the boot objects, which holds them.
func (vm *VM) enumerateBoot() {
	defer vm.lockWeakTables()()
	e := newImgEnc(vm, &vmImageSpec, SaveOptions{})
	e.boot = true
	e.cur = -1
	defer func() {
		if r := recover(); r != nil {
			vm.bootErr = imgRecover(r)
		}
	}()
	e.discover()
	idx := map[imgKey]int32{}
	var list []reflect.Value
	for _, o := range e.objs {
		if o.v.Kind() == reflect.Pointer && o.tier == tierFull {
			idx[o.key] = int32(len(idx))
			// the pointer itself: o.v may be the field of the VM it was reached
			// through, which reads as another object once the field is set
			list = append(list, reflect.ValueOf(o.v.Interface()))
		}
	}
	vm.bootIdx, vm.bootList = idx, list
}

// ----- the cut and the writer -----

// checkCut refuses a VM that is not at a point where an image can be taken:
// no module under definition, no engine lease, no task but the root running,
// no libuv handle alive.
func (vm *VM) checkCut() error {
	switch {
	case vm.bootErr != nil:
		return vm.bootErr
	case len(vm.openModules) != 0:
		return fmt.Errorf("%w: %d modules are open", ErrImageUnsaveable, len(vm.openModules))
	case len(vm.reservations) != 0:
		return fmt.Errorf("%w: %d engine reservations are live", ErrImageUnsaveable, len(vm.reservations))
	case vm.cur != nil && vm.cur != vm.task:
		return fmt.Errorf("%w: a task other than the root is running", ErrImageUnsaveable)
	case vm.loop != nil && (len(vm.loop.handles) != 0 || len(vm.loop.closing) != 0 || vm.loop.running):
		return fmt.Errorf("%w: libuv handles are alive", ErrImageUnsaveable)
	}
	return nil
}

// lockWeakTables holds the locks of the tables whose entries a cleanup
// deletes from another goroutine when their key's object is collected.
func (vm *VM) lockWeakTables() (unlock func()) {
	vm.argMu.Lock()
	vm.addrs.mu.Lock()
	return func() {
		vm.addrs.mu.Unlock()
		vm.argMu.Unlock()
	}
}

func (vm *VM) saveImage(w io.Writer, opts SaveOptions, spec *imageSpec) (err error) {
	if err := vm.checkCut(); err != nil {
		return err
	}
	defer vm.lockWeakTables()()
	e := newImgEnc(vm, spec, opts)
	defer func() {
		if r := recover(); r != nil {
			err = imgRecover(r)
		}
	}()
	e.cur = -1
	e.discover()
	e.write(w)
	return nil
}

func (e *imgEnc) write(w io.Writer) {
	out := &imgOut{w: w, h: sha256.New(), max: e.opts.MaxBytes}
	schema := e.spec.schemaHash()
	key := imageKey(schema)
	out.write([]byte(imageMagic))
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], imageVersion)
	out.write(v[:])
	out.write(key[:])

	// the tables, sorted
	e.typIdx, e.strIdx = map[string]uint32{}, map[string]uint32{}
	types := sortedIndex(e.typs, e.typIdx)
	strs := sortedIndex(e.strs, e.strIdx)
	out.uv(uint64(len(types)))
	for _, t := range types {
		out.str(t)
	}
	out.uv(uint64(len(e.objs)))
	for _, o := range e.objs {
		t := uint64(e.typIdx[o.t.String()])<<2 | uint64(o.tier)
		out.uv(t)
		if o.tier == tierStatic || o.tier == tierBoot {
			out.uv(uint64(o.tidx))
		}
	}
	out.uv(uint64(len(strs)))
	for _, s := range strs {
		out.str(s)
	}

	// the records, in id order
	e.writing = true
	for i := range e.objs {
		e.buf = e.buf[:0]
		e.record(i)
		out.uv(uint64(len(e.buf)))
		out.write(e.buf)
	}
	e.buf = e.buf[:0]
	e.cur = -1
	e.path = e.path[:0]
	e.roots()
	out.uv(uint64(len(e.buf)))
	out.write(e.buf)

	// what Base wrote on core's shared type names, then the interned types
	e.buf = e.buf[:0]
	e.hints()
	out.uv(uint64(len(e.buf)))
	out.write(e.buf)
	e.buf = e.buf[:0]
	ids := e.interned()
	e.uv(uint64(len(ids)))
	for _, id := range ids {
		e.uv(uint64(id))
	}
	out.uv(uint64(len(e.buf)))
	out.write(e.buf)

	out.write(out.h.Sum(nil)) // the trailer: the hash of all before it
}

// imageHintFields are the fields Base writes on core's type names, which every
// VM of the process shares: the hints of jl_typename_t Julia code sets, and
// the cached Type{Wrapper} (jltypes.c:1115-1120).
var imageHintFields = []string{"MaxArgs", "CacheEntryCount", "MaxMethods", "ConstpropHeuristic", "typeofWrapper"}

// hintValue is a hint of a type name: its value, whether it is set at all, and
// for typeofWrapper the type it holds.
func hintValue(tn *core.TypeName, field string) (v int64, dt *core.DataType, set bool) {
	switch field {
	case "MaxArgs":
		v = int64(tn.MaxArgs.Load())
	case "CacheEntryCount":
		v = int64(tn.CacheEntryCount.Load())
	case "MaxMethods":
		v = int64(tn.MaxMethods.Load())
	case "ConstpropHeuristic":
		v = int64(tn.ConstpropHeuristic.Load())
	default:
		dt = tn.TypeofWrapper()
		return 0, dt, dt != nil
	}
	return v, nil, v != 0
}

// hints writes the hints set on the static type names: (index in core's table,
// field, value). The first pass only collects the names of the fields; the
// second leaves out a cached type that is not in the image.
func (e *imgEnc) hints() {
	type entry struct {
		idx   int
		field string
		val   int64
	}
	var ents []entry
	for i, o := range core.StaticObjects() {
		tn, ok := o.(*core.TypeName)
		if !ok {
			continue
		}
		for _, f := range imageHintFields {
			v, dt, set := hintValue(tn, f)
			if !set {
				continue
			}
			if !e.writing {
				e.str(f) // the first pass only collects the names
				continue
			}
			if dt != nil {
				id, ok := e.idOf(reflect.ValueOf(dt))
				if !ok {
					continue
				}
				v = int64(id)
			}
			ents = append(ents, entry{i, f, v})
		}
	}
	e.uv(uint64(len(ents)))
	for _, h := range ents {
		e.uv(uint64(h.idx))
		e.str(h.field)
		e.zz(h.val)
	}
}

// interned lists the types of the image that were in the cache of their type
// name when it was taken, in the order of the caches: the static names, then
// the others in id order. A loader inserts them again, parameters first.
func (e *imgEnc) interned() []uint32 {
	var out []uint32
	visit := func(tn *core.TypeName) {
		for _, d := range tn.CachedTypes() {
			if id, ok := e.idOf(reflect.ValueOf(d)); ok && e.objs[id].tier == tierFull {
				out = append(out, id)
			}
		}
	}
	for _, o := range core.StaticObjects() {
		if tn, ok := o.(*core.TypeName); ok {
			visit(tn)
		}
	}
	for i := range e.objs {
		if e.objs[i].t == typeNamePtrType && e.objs[i].tier != tierStatic {
			visit(e.objs[i].v.Interface().(*core.TypeName))
		}
	}
	// in id order, not in the order of the caches: what a cache holds first
	// depends on what the process made before (a loader orders the insertions
	// by what depends on what)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// sortedIndex is the keys of set in order, and in idx the position of each.
func sortedIndex(set map[string]struct{}, idx map[string]uint32) []string {
	list := make([]string, 0, len(set))
	for s := range set {
		list = append(list, s)
	}
	sort.Strings(list)
	for i, s := range list {
		idx[s] = uint32(i)
	}
	return list
}
