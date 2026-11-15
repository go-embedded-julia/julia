package interp

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"
	randv1 "math/rand"
	randv2 "math/rand/v2"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unsafe"
	"weak"

	"github.com/go-embedded-julia/julia/core"
)

// Images made by hand: the framing of a real image, with objects of types the
// test defines, sealed with a valid key and trailer so that the decoder and
// not the checksum refuses them. Each way a record, a section or a table can
// be wrong is one case.

// decHost has a field of each kind a record holds.
type decHost struct {
	B   bool
	I8  int8
	U8  uint8
	F32 float32
	F64 float64
	S   string
	P   *decHost
	A   any
	E   error
	Sl  []any
	M   map[string]any
	MA  map[any]int
	Ar  [2]uint8
	Fn  func()
	W   weak.Pointer[Memory]
	Ch  chan int
	Up  unsafe.Pointer
	At  atomic.Pointer[decHost]
	Mu  sync.Mutex
	Sk  int
}

type decNest struct{ A any }

type decCx struct{ C complex128 }

func decSpec() imageSpec {
	defs := append([]imageKindDef(nil), vmImageSpec.defs...)
	for _, t := range []reflect.Type{reflect.TypeFor[*decHost](), reflect.TypeFor[decNest](), reflect.TypeFor[*decCx]()} {
		defs = append(defs, imageKindDef{t, kindSaved})
	}
	s := newImageSpec(defs)
	s.skip = map[string]string{"interp.decHost.Sk": "test"}
	for k, v := range vmImageSpec.skip {
		s.skip[k] = v
	}
	s.roots = map[string]bool{"ghost": true} // saved, and no field of the VM
	for k, v := range vmImageSpec.roots {
		s.roots[k] = v
	}
	return s
}

// rec builds a record or a section.
type rb struct{ b []byte }

func (r *rb) uv(x uint64) *rb { r.b = binary.AppendUvarint(r.b, x); return r }
func (r *rb) zz(x int64) *rb  { r.b = binary.AppendVarint(r.b, x); return r }
func (r *rb) f64(f float64) *rb {
	r.b = binary.LittleEndian.AppendUint64(r.b, math.Float64bits(f))
	return r
}
func (r *rb) raw(b ...byte) *rb { r.b = append(r.b, b...); return r }

const (
	tHost   = "*interp.decHost"
	tSlice  = "[]interface {}"
	tMap    = "map[string]interface {}"
	tMapAny = "map[interface {}]int"
	tMem    = "*interp.Memory"
	tNest   = "interp.decNest"
	tCx     = "*interp.decCx"
	tTable  = "*dispatch.Table"
	tDType  = "*core.DataType"
)

type craftObj struct {
	typ  string
	ti   int // the index of the type in the table, or -1 for the one of typ
	tier int
	idx  int
	rec  []byte
}

type craft struct {
	spec     imageSpec
	types    []string
	strs     []string
	objs     []craftObj
	roots    []byte
	hints    []byte
	interned []byte
	trailing []byte
}

func newCraft() *craft {
	c := &craft{spec: decSpec(), roots: []byte{0}, hints: []byte{0}, interned: []byte{0}}
	c.str("")
	return c
}

func (c *craft) ti(name string) int {
	for i, t := range c.types {
		if t == name {
			return i
		}
	}
	c.types = append(c.types, name)
	return len(c.types) - 1
}

func (c *craft) str(s string) uint64 {
	for i, x := range c.strs {
		if x == s {
			return uint64(i)
		}
	}
	c.strs = append(c.strs, s)
	return uint64(len(c.strs) - 1)
}

// obj adds an object of the Go type named typ with its record: its id.
func (c *craft) obj(typ string, record []byte) int {
	c.ti(typ)
	c.objs = append(c.objs, craftObj{typ: typ, ti: -1, rec: record})
	return len(c.objs) - 1
}

// static adds the process-static object i of core's table, as the type says.
func (c *craft) static(typ string, i int) int {
	id := c.obj(typ, nil)
	c.objs[id].tier, c.objs[id].idx = tierStatic, i
	return id
}

func (c *craft) body() []byte {
	var b rb
	b.uv(uint64(len(c.types)))
	for _, t := range c.types {
		b.uv(uint64(len(t))).raw([]byte(t)...)
	}
	b.uv(uint64(len(c.objs)))
	for _, o := range c.objs {
		ti := o.ti
		if ti < 0 {
			ti = c.ti(o.typ)
		}
		b.uv(uint64(ti)<<2 | uint64(o.tier))
		if o.tier == tierStatic || o.tier == tierBoot {
			b.uv(uint64(o.idx))
		}
	}
	b.uv(uint64(len(c.strs)))
	for _, s := range c.strs {
		b.uv(uint64(len(s))).raw([]byte(s)...)
	}
	for _, o := range c.objs {
		b.uv(uint64(len(o.rec))).raw(o.rec...)
	}
	for _, sec := range [][]byte{c.roots, c.hints, c.interned} {
		b.uv(uint64(len(sec))).raw(sec...)
	}
	return append(b.b, c.trailing...)
}

func sealImage(spec *imageSpec, body []byte) []byte {
	var out bytes.Buffer
	out.WriteString(imageMagic)
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], imageVersion)
	out.Write(v[:])
	key := imageKey(spec.schemaHash())
	out.Write(key[:])
	out.Write(body)
	sum := sha256.Sum256(out.Bytes())
	out.Write(sum[:])
	return out.Bytes()
}

// hostFields are the fields of a decHost a record holds, in order.
var hostFields = []string{"B", "I8", "U8", "F32", "F64", "S", "P", "A", "E", "Sl", "M", "MA", "Ar", "Fn", "W", "At"}

// hostRecord is the record of a decHost: every field zero but those over
// holds, and ends after the field stop when it is not empty.
func hostRecord(over map[string]func(r *rb), stop string) []byte {
	var r rb
	for _, f := range hostFields {
		if w, ok := over[f]; ok {
			w(&r)
		} else {
			switch f {
			case "I8":
				r.zz(0)
			case "F32", "F64":
				r.f64(0)
			case "Ar":
				r.uv(0).uv(0)
			default:
				r.uv(0)
			}
		}
		if f == stop {
			break
		}
	}
	return r.b
}

func (c *craft) host(over map[string]func(r *rb)) int { return c.obj(tHost, hostRecord(over, "")) }

func loadCraft(c *craft) (*VM, error) {
	vm := New()
	return vm, vm.loadImage(bytes.NewReader(sealImage(&c.spec, c.body())), LoadOptions{}, &c.spec)
}

func wantLoad(t *testing.T, name string, c *craft, kind error, substr string) {
	t.Helper()
	_, err := loadCraft(c)
	if !errors.Is(err, kind) || !strings.Contains(err.Error(), substr) {
		t.Errorf("%s: %v\n  want %v mentioning %q", name, err, kind, substr)
	}
}

func hostCraft(field string, w func(r *rb, c *craft)) *craft {
	c := newCraft()
	c.host(map[string]func(r *rb){field: func(r *rb) { w(r, c) }})
	return c
}

// A hand-made image loads, and each kind of field comes back as it was written.
func TestImageCraftedValid(t *testing.T) {
	c := newCraft()
	mem := c.obj(tMem, new(rb).uv(0).uv(0).uv(0).b)
	slice := c.obj(tSlice, new(rb).uv(2).uv(0).uv(c.str("x")+0).b)
	// the slice holds nil and an int8 boxed
	c.objs[slice].rec = new(rb).uv(2).uv(0).uv(uint64(c.ti("int8")) + 1).zz(-3).b
	m := c.obj(tMap, new(rb).uv(1).uv(c.str("k")).uv(uint64(c.ti("int8"))+1).zz(9).b)
	host := len(c.objs)
	c.host(map[string]func(r *rb){
		"B":   func(r *rb) { r.uv(1) },
		"I8":  func(r *rb) { r.zz(-5) },
		"U8":  func(r *rb) { r.uv(200) },
		"F32": func(r *rb) { r.f64(1.5) }, "F64": func(r *rb) { r.f64(math.Inf(1)) },
		"S":  func(r *rb) { r.uv(c.str("hello")) },
		"P":  func(r *rb) { r.uv(uint64(host) + 2) },
		"A":  func(r *rb) { r.uv(uint64(c.ti("*struct { _ uint8 }")) + 1).uv(1) }, // the undef sentinel
		"Sl": func(r *rb) { r.uv(uint64(slice) + 2) },
		"M":  func(r *rb) { r.uv(uint64(m) + 2) },
		"Ar": func(r *rb) { r.uv(3).uv(4) },
		"Fn": func(r *rb) { r.uv(1) },
		"W":  func(r *rb) { r.uv(uint64(mem) + 2) },
		"At": func(r *rb) { r.uv(uint64(host) + 2) },
	})
	c.roots = new(rb).uv(1).uv(c.str("gsCtr")).uv(77).b
	vm, err := loadCraft(c)
	if err != nil {
		t.Fatal(err)
	}
	if vm.gsCtr != 77 {
		t.Errorf("the root gsCtr is %d", vm.gsCtr)
	}
}

// Each way a record can be wrong is a refusal, of the kind that says why.
func TestImageCraftedRecords(t *testing.T) {
	cases := []struct {
		name, field string
		w           func(r *rb, c *craft)
		want        string
	}{
		{"a Bool of 2", "B", func(r *rb, c *craft) { r.uv(2) }, "a Bool of 2"},
		{"an int8 out of range", "I8", func(r *rb, c *craft) { r.zz(300) }, "does not fit a int8"},
		{"a uint8 out of range", "U8", func(r *rb, c *craft) { r.uv(300) }, "does not fit a uint8"},
		{"a float32 out of range", "F32", func(r *rb, c *craft) { r.f64(1e300) }, "does not fit a float32"},
		{"a string out of range", "S", func(r *rb, c *craft) { r.uv(99) }, "string 99 of"},
		{"a reference out of range", "P", func(r *rb, c *craft) { r.uv(99) }, "reference to object 97"},
		{"undef where a pointer is held", "P", func(r *rb, c *craft) { r.uv(1) }, "undef where a *interp.decHost"},
		{"a func flag of 2", "Fn", func(r *rb, c *craft) { r.uv(2) }, "a func flag of 2"},
		{"a type out of range in an interface", "A", func(r *rb, c *craft) { r.uv(999) }, "type 998 of"},
		{"a pointer in an error", "E", func(r *rb, c *craft) { r.uv(uint64(c.ti(tHost)) + 1).uv(0) }, "where a error is held"},
		{"a func the VM does not hold", "A", func(r *rb, c *craft) { r.uv(uint64(c.ti("interp.goMethod")) + 1).uv(1) }, "does not hold here"},
		{"a flag of 2 on a func in an interface", "A", func(r *rb, c *craft) { r.uv(uint64(c.ti("interp.goMethod")) + 1).uv(2) }, "a func flag of 2"},
		{"a weak pointer to undef", "W", func(r *rb, c *craft) { r.uv(1) }, "a weak pointer"},
		{"a weak pointer to the wrong type", "W", func(r *rb, c *craft) { r.uv(2) }, "a weak "},
		{"an atomic pointer to undef", "At", func(r *rb, c *craft) { r.uv(1) }, "undef where a *interp.decHost"},
		{"a nulled Ptr whose type is not a type", "A", func(r *rb, c *craft) { r.uv(uint64(c.ti(nulledPointerType)) + 1).uv(2) }, "where a *core.DataType is held"},
		{"values nested too deep", "A", func(r *rb, c *craft) {
			for i := 0; i < 300; i++ {
				r.uv(uint64(c.ti(tNest)) + 1)
			}
			r.uv(0)
		}, "nested more than"},
	}
	for _, k := range cases {
		c := hostCraft(k.field, k.w)
		wantLoad(t, k.name, c, ErrImageCorrupt, k.want)
	}
	// a nulled Ptr whose type is the static type of core: a Ptr is made
	c := newCraft()
	dt := c.static(tDType, 0)
	c.host(map[string]func(r *rb){"A": func(r *rb) { r.uv(uint64(c.ti(nulledPointerType)) + 1).uv(uint64(dt) + 2) }})
	if vm, err := loadCraft(c); err != nil {
		t.Errorf("a nulled Ptr: %v", err)
	} else {
		_ = vm
	}

	// a record cut in a number, short of its bytes, or with bytes left
	c = newCraft()
	c.obj(tHost, hostRecord(map[string]func(r *rb){"I8": func(r *rb) { r.raw(0x80) }}, "I8"))
	wantLoad(t, "a record cut in a number", c, ErrImageCorrupt, "ends in the middle of a number")
	c = newCraft()
	c.obj(tHost, append(hostRecord(map[string]func(r *rb){"F32": func(r *rb) { r.raw(1, 2, 3) }}, "F32"), 0)[:0])
	c.objs[0].rec = append(hostRecord(nil, "U8"), 1, 2, 3)
	wantLoad(t, "a record short of its bytes", c, ErrImageCorrupt, "bytes short")
	c = newCraft()
	c.obj(tHost, nil)
	wantLoad(t, "an empty record", c, ErrImageCorrupt, "ends in the middle of a number")
	c = newCraft()
	c.obj(tHost, append(hostRecord(nil, ""), 0))
	wantLoad(t, "a record with bytes left", c, ErrImageCorrupt, "bytes after its record")

	// a field of a kind with no record, an unhashable key, an unknown kind
	c = newCraft()
	c.obj(tCx, nil)
	wantLoad(t, "a complex number", c, ErrImageCorrupt, "a value of kind complex128")
	c = newCraft()
	sl := c.obj(tSlice, new(rb).uv(0).b)
	c.obj(tMapAny, new(rb).uv(1).uv(uint64(c.ti(tSlice))+1).uv(uint64(sl)+2).zz(1).b)
	wantLoad(t, "an unhashable key", c, ErrImageCorrupt, "unhashable")
}

// The shells: what each object of the table must be.
func TestImageCraftedObjects(t *testing.T) {
	cases := []struct {
		name string
		mk   func(c *craft)
		want string
	}{
		{"a type of the table out of range", func(c *craft) { c.host(nil); c.objs[0].ti = 99 }, "has type 99 of"},
		{"a root task that is no Struct", func(c *craft) { c.host(nil); c.objs[0].tier = tierTask }, "the root task is a *interp.decHost"},
		{"a static index out of range", func(c *craft) { c.static(tDType, 1<<20) }, "static object"},
		{"a static with a record", func(c *craft) { c.static(tDType, 0); c.objs[0].rec = []byte{0} }, "static object"},
		{"a static of another type", func(c *craft) { c.static(tHost, 0) }, "the image says *interp.decHost"},
		{"a boot index out of range", func(c *craft) { c.host(nil); c.objs[0].tier, c.objs[0].idx = tierBoot, 1<<20 }, "boot object"},
		{"an object that is no pointer, slice or map", func(c *craft) { c.obj("int8", nil) }, "of kind int8"},
		{"an object of the nulled Ptr", func(c *craft) { c.obj(nulledPointerType, nil) }, "type of a nulled Ptr"},
		{"a slice with no record", func(c *craft) { c.obj(tSlice, nil) }, "elements in a record of 0 bytes"},
		{"a slice longer than its record", func(c *craft) { c.obj(tSlice, new(rb).uv(1000).b) }, "1000 elements in a record"},
		{"a map larger than its record", func(c *craft) { c.obj(tMap, new(rb).uv(1000).b) }, "1000 elements in a record"},
		{"a type no interpreter has", func(c *craft) { c.obj("*nowhere.Nothing", nil) }, "does not have"},
		{"an object table longer than its records", func(c *craft) { c.host(nil); c.trailing = []byte{1} }, "after the last section"},
	}
	for _, k := range cases {
		c := newCraft()
		k.mk(c)
		wantLoad(t, k.name, c, ErrImageCorrupt, k.want)
	}
}

// Two Go types printed alike cannot be named by an image.
func TestImageRegistryAmbiguous(t *testing.T) {
	defs := []imageKindDef{{reflect.TypeFor[*randv1.Rand](), kindSaved}, {reflect.TypeFor[*randv2.Rand](), kindSaved}}
	reg := newImageRegistry(defs)
	if !reg.ambiguous["*rand.Rand"] {
		t.Fatalf("*rand.Rand is not ambiguous: %v", reg.ambiguous)
	}
	c := newCraft()
	c.spec = newImageSpec(append(append([]imageKindDef(nil), c.spec.defs...), defs...))
	c.obj("*rand.Rand", nil)
	wantLoad(t, "an ambiguous type", c, ErrImageCorrupt, "does not have")
	if len(vmImageSpec.registry().ambiguous) != 0 {
		t.Errorf("the interpreter's own types are ambiguous: %v", vmImageSpec.registry().ambiguous)
	}
}

// Roots, hints and interned types of an image that disagrees with the VM.
func TestImageCraftedSections(t *testing.T) {
	root := func(name string, extra ...byte) *craft {
		c := newCraft()
		c.roots = append(new(rb).uv(1).uv(c.str(name)).b, extra...)
		return c
	}
	cases := []struct {
		name string
		mk   func() *craft
		kind error
		want string
	}{
		{"more roots than bytes", func() *craft { c := newCraft(); c.roots = new(rb).uv(1000).b; return c }, ErrImageCorrupt, "roots in"},
		{"a root name out of range", func() *craft { c := newCraft(); c.roots = new(rb).uv(1).uv(99).b; return c }, ErrImageCorrupt, "string 99 of"},
		{"a root that is no field", func() *craft { return root("nosuch") }, ErrImageCorrupt, "not a saved field"},
		{"a host root", func() *craft { return root("shims") }, ErrImageCorrupt, "not a saved field"},
		{"a root twice", func() *craft {
			c := newCraft()
			c.roots = new(rb).uv(2).uv(c.str("gsCtr")).uv(1).uv(c.str("gsCtr")).uv(2).b
			return c
		}, ErrImageCorrupt, "or is there twice"},
		{"a root the VM has no field for", func() *craft { return root("ghost") }, ErrImageCorrupt, "has no field"},
		{"bytes after the roots", func() *craft { c := newCraft(); c.roots = []byte{0, 0}; return c }, ErrImageCorrupt, "after the roots"},
		{"more hints than bytes", func() *craft { c := newCraft(); c.hints = new(rb).uv(1000).b; return c }, ErrImageCorrupt, "hints in"},
		{"a hint of a static out of range", func() *craft {
			c := newCraft()
			c.hints = new(rb).uv(1).uv(1 << 20).uv(c.str("MaxArgs")).zz(1).b
			return c
		}, ErrImageCorrupt, "a hint of static object"},
		{"a hint with a string out of range", func() *craft {
			c := newCraft()
			c.hints = new(rb).uv(1).uv(1).uv(99).zz(1).b
			return c
		}, ErrImageCorrupt, "a hint of static object"},
		{"a hint no field has", func() *craft {
			c := newCraft()
			i, _ := core.StaticIndex(core.Int8Type.Name)
			c.hints = new(rb).uv(1).uv(uint64(i)).uv(c.str("bogus")).zz(1).b
			return c
		}, ErrImageCorrupt, `a hint "bogus"`},
		{"a hint of a static that is no name", func() *craft {
			c := newCraft()
			c.hints = new(rb).uv(1).uv(0).uv(c.str("MaxArgs")).zz(1).b
			return c
		}, ErrImageCorrupt, `a hint "MaxArgs"`},
		{"a cached type out of range", func() *craft {
			c := newCraft()
			i, _ := core.StaticIndex(core.Int8Type.Name)
			c.hints = new(rb).uv(1).uv(uint64(i)).uv(c.str("typeofWrapper")).zz(1000).b
			return c
		}, ErrImageCorrupt, "is object 1000 of"},
		{"a cached type that is no type", func() *craft {
			c := newCraft()
			h := c.host(nil)
			i, _ := core.StaticIndex(core.Int8Type.Name)
			c.hints = new(rb).uv(1).uv(uint64(i)).uv(c.str("typeofWrapper")).zz(int64(h)).b
			return c
		}, ErrImageCorrupt, "is not a type"},
		{"bytes after the hints", func() *craft { c := newCraft(); c.hints = []byte{0, 0}; return c }, ErrImageCorrupt, "after the hints"},
		{"more interned types than bytes", func() *craft { c := newCraft(); c.interned = new(rb).uv(1000).b; return c }, ErrImageCorrupt, "interned types in"},
		{"an interned object out of range", func() *craft { c := newCraft(); c.interned = new(rb).uv(1).uv(99).b; return c }, ErrImageCorrupt, "is not a type of the image"},
		{"an interned static", func() *craft {
			c := newCraft()
			id := c.static(tDType, 0)
			c.interned = new(rb).uv(1).uv(uint64(id)).b
			return c
		}, ErrImageCorrupt, "is not a type of the image"},
		{"an interned object of another type", func() *craft {
			c := newCraft()
			id := c.host(nil)
			c.interned = new(rb).uv(1).uv(uint64(id)).b
			return c
		}, ErrImageCorrupt, "is not a type of the image"},
		{"an interned type with no name", func() *craft {
			c := newCraft()
			id := c.obj(tDType, make([]byte, 12)) // the 12 fields of a DataType, each nil or zero
			c.interned = new(rb).uv(1).uv(uint64(id)).b
			return c
		}, ErrImageCorrupt, "has no name"},
		{"bytes after the interned types", func() *craft { c := newCraft(); c.interned = []byte{0, 0}; return c }, ErrImageCorrupt, "after the interned"},
		{"a Task object that is not the VM's", func() *craft {
			c := newCraft()
			id := c.obj("*interp.Struct", new(rb).uv(0).uv(0).uv(0).b)
			c.objs[id].tier = tierTask
			return c
		}, ErrImageCorrupt, "not the VM's task"},
		{"a root task whose type has no storage", func() *craft {
			c := newCraft()
			i64, _ := core.StaticIndex(core.Int64Type)
			dt := c.static(tDType, i64)
			task := c.obj("*interp.Struct", new(rb).uv(0).b)
			c.objs[task].tier = tierTask
			c.roots = new(rb).uv(2).uv(c.str("tTask")).uv(uint64(dt) + 2).uv(c.str("task")).uv(uint64(task) + 2).b
			return c
		}, ErrImageCorrupt, "no storage field"},
		{"bytes after the root task", func() *craft {
			c := newCraft()
			task := c.obj("*interp.Struct", new(rb).uv(0).uv(0).b)
			c.objs[task].tier = tierTask
			c.roots = new(rb).uv(1).uv(c.str("task")).uv(uint64(task) + 2).b
			return c
		}, ErrImageCorrupt, "after the root task"},
		{"a Go body no registry has", func() *craft {
			c := newCraft()
			c.obj("*interp.goBody", new(rb).uv(c.str("nope")).uv(0).uv(0).b)
			return c
		}, ErrImageCorrupt, "no Go body named"},
	}
	for _, k := range cases {
		wantLoad(t, k.name, k.mk(), k.kind, k.want)
	}
}
