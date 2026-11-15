package interp

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// A load is atomic with respect to what the VMs of a process share: the type
// caches of core's names, their hints and the cached instances of their types.
// An image that is refused changes none of it; one that loads changes it in
// one step. These tests run once per process: an instance a load gives stays
// in the process (go test -count=2 skips them the second time).

// sharedState is what a load may write to and every VM of the process reads:
// for each of core's static type names its cache (the types, by pointer), its
// hints and its cached Type{Wrapper}, and for each static type its instance.
type sharedState struct {
	types   map[*core.TypeName][]*core.DataType
	hints   map[*core.TypeName]string
	wrapper map[*core.TypeName]*core.DataType
	inst    map[*core.DataType]any
	hasInst map[*core.DataType]bool
}

func snapshotShared() *sharedState {
	s := &sharedState{
		types: map[*core.TypeName][]*core.DataType{}, hints: map[*core.TypeName]string{},
		wrapper: map[*core.TypeName]*core.DataType{}, inst: map[*core.DataType]any{}, hasInst: map[*core.DataType]bool{},
	}
	for _, o := range core.StaticObjects() {
		switch x := o.(type) {
		case *core.TypeName:
			s.types[x] = x.CachedTypes()
			s.hints[x] = fmt.Sprint(x.MaxArgs.Load(), x.CacheEntryCount.Load(), x.MaxMethods.Load(), x.ConstpropHeuristic.Load())
			s.wrapper[x] = x.TypeofWrapper()
		case *core.DataType:
			s.inst[x], s.hasInst[x] = x.CachedInstance()
		}
	}
	return s
}

// vmGrowth is how much a VM's own creation grows the caches of core's names
// (the types New makes of what is private to the VM): a load makes a VM before
// it reads the image, and that is not what the image wrote.
func vmGrowth() map[*core.TypeName]int {
	New() // what a VM makes only the first time is made
	before := snapshotShared()
	New()
	after, g := snapshotShared(), map[*core.TypeName]int{}
	for tn, ts := range after.types {
		g[tn] = len(ts) - len(before.types[tn])
	}
	return g
}

// diff names the first thing that is not the same in b as in a, or "": the
// caches may have grown by what a VM's creation makes (growth), or by nothing,
// not by anything else.
func (a *sharedState) diff(b *sharedState, growth map[*core.TypeName]int) string {
	d := a.diffWith(b, nil)
	if d == "" {
		return ""
	}
	if a.diffWith(b, growth) == "" {
		return ""
	}
	return d
}

func (a *sharedState) diffWith(b *sharedState, growth map[*core.TypeName]int) string {
	for tn, ts := range a.types {
		us := b.types[tn]
		if len(ts)+growth[tn] != len(us) {
			return fmt.Sprintf("the cache of %s holds %d types, it held %d", tn.Name, len(us), len(ts))
		}
		in := map[*core.DataType]bool{}
		for _, u := range us {
			in[u] = true
		}
		for _, x := range ts {
			if !in[x] {
				return fmt.Sprintf("a type of the cache of %s is gone", tn.Name)
			}
		}
		if a.hints[tn] != b.hints[tn] {
			return fmt.Sprintf("the hints of %s are %s, they were %s", tn.Name, b.hints[tn], a.hints[tn])
		}
		if a.wrapper[tn] != b.wrapper[tn] {
			return fmt.Sprintf("the Type{Wrapper} of %s changed", tn.Name)
		}
	}
	for dt, v := range a.inst {
		if a.hasInst[dt] != b.hasInst[dt] || v != b.inst[dt] {
			return fmt.Sprintf("the instance of %v changed", dt)
		}
	}
	return ""
}

// craftOfImage is a real image taken apart into a craft, to which a test adds
// what makes a load fail or conflict, and sealed again with a valid key and
// trailer by sealCraft.
func craftOfImage(t testing.TB, img []byte) *craft {
	t.Helper()
	fr, err := readImage(bytes.NewReader(img), LoadOptions{}, &vmImageSpec)
	if err != nil {
		t.Fatal(err)
	}
	c := &craft{types: fr.types, strs: fr.strs, roots: fr.roots, hints: fr.hints, interned: fr.interned}
	for i, k := range fr.kinds {
		c.objs = append(c.objs, craftObj{typ: fr.types[k.ti], ti: k.ti, tier: k.tier, idx: k.idx, rec: fr.recs[i]})
	}
	return c
}

func sealCraft(c *craft) *bytes.Reader { return bytes.NewReader(sealImage(&vmImageSpec, c.body())) }

// the image of a program that makes a type of its own and an Array of it: the
// types the image interns are new to the process, since the VM that made them
// is not the one that loads, and the names they hang on are core's.
func atomicImage(t testing.TB) []byte {
	vm := bootedVM(t)
	s := vm.NewSession()
	for _, src := range []string{"struct AtomA; x; end", "atomv = Core.GenericMemory{:not_atomic, AtomA, Core.CPU}(undef, 0)"} {
		if _, err := s.Eval(src); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
	return saveBytes(t, vm)
}

// failingLate is the image with one more object, a Go body no registry has:
// refused in rebuild, the last step of decoding, after every type is decoded.
func failingLate(t testing.TB, img []byte) *craft {
	c := craftOfImage(t, img)
	c.obj("*interp.goBody", new(rb).uv(c.str("no such body")).uv(0).uv(0).b)
	return c
}

// An image with a valid key and trailer that fails late, after types that
// would be interned have been decoded, changes nothing the process shares, and
// the VMs that were there answer as they did.
func TestImageLoadAtomicOnLateFailure(t *testing.T) {
	img := atomicImage(t)
	other := bootedVM(t)
	so := other.NewSession()
	answer := func() string {
		v, err := so.Eval("(Core.GenericMemory{:not_atomic, Int, Core.CPU}, 1)")
		if err != nil {
			t.Fatal(err)
		}
		return fmt.Sprint(v)
	}
	growth := vmGrowth()
	answered := answer() // what the question makes is made before the state is read
	before := snapshotShared()

	if _, err := LoadImage(sealCraft(failingLate(t, img)), LoadOptions{}); !errors.Is(err, ErrImageCorrupt) || !strings.Contains(err.Error(), "no Go body named") {
		t.Fatalf("the failing image: %v", err)
	}
	if d := before.diff(snapshotShared(), growth); d != "" {
		t.Errorf("a refused image changed what the process shares: %s", d)
	}
	if got := answer(); got != answered {
		t.Errorf("another VM answers %s, it answered %s", got, answered)
	}

	// the control: the image without the failure does write, so the assertion
	// above is about something
	if _, err := LoadImage(bytes.NewReader(img), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if before.diff(snapshotShared(), growth) == "" {
		t.Error("the valid image interned nothing: the test proves nothing")
	}
	if got := answer(); got != answered {
		t.Errorf("another VM answers %s after a valid load, it answered %s", got, answered)
	}
}

// dtype adds a *core.DataType of the image: its name (the id of a TypeName
// object), its parameters (ids of DataType objects), and the cached instance
// when inst is not nil (an int64).
func (c *craft) dtype(name int, params []int, inst *int64) int {
	ps := new(rb).uv(uint64(len(params)))
	for _, p := range params {
		ps.uv(uint64(c.ti(tDType)) + 1).uv(uint64(p) + 2)
	}
	slice := c.obj(tSlice, ps.b)
	box := 0
	if inst != nil {
		box = c.obj("*core.instanceBox", new(rb).uv(uint64(c.ti("int64"))+1).zz(*inst).b) + 2
	}
	// Name, Super, Parameters, hash, five flags, ntNames, ntTypes, instance
	r := new(rb).uv(uint64(name) + 2).uv(0).uv(uint64(slice) + 2).uv(0)
	r.uv(0).uv(0).uv(0).uv(0).uv(0).uv(0).uv(0).uv(uint64(box))
	return c.obj(tDType, r.b)
}

// staticOf adds the process-static object v, of Go type typ, to the table.
func (c *craft) staticOf(t testing.TB, v any, typ string) int {
	t.Helper()
	i, ok := core.StaticIndex(v)
	if !ok {
		t.Fatalf("%v is not static", v)
	}
	return c.static(typ, i)
}

// hintOf is a hint entry: (index of the static name, field, value).
func (c *craft) hintOf(t testing.TB, tn *core.TypeName, field string, val int64) []byte {
	t.Helper()
	i, ok := core.StaticIndex(tn)
	if !ok {
		t.Fatalf("%s is not static", tn.Name)
	}
	return new(rb).uv(uint64(i)).uv(c.str(field)).zz(val).b
}

func countedSection(n int, entries ...[]byte) []byte {
	b := new(rb).uv(uint64(n)).b
	for _, e := range entries {
		b = append(b, e...)
	}
	return b
}

// An image whose hint or whose instance transfer conflicts with what the
// process holds is refused before anything is written: not the hint that was
// fine, not the instance that was to be given.
func TestImageLoadConflictWritesNothing(t *testing.T) {
	refOf := func(p *core.DataType) *core.DataType { return must(core.Apply(core.RefType, p)).(*core.DataType) }
	given, kept, differs := refOf(core.UInt16Type), refOf(core.UInt64Type), refOf(core.UInt32Type)
	for _, d := range []*core.DataType{given, kept, differs} {
		if _, has := d.CachedInstance(); has {
			t.Skip("an instance left by a previous run in this process")
		}
	}
	kept.SetCachedInstance(int64(5))
	differs.SetCachedInstance(int64(7))
	missing := core.MissingType.Name
	missing.MaxArgs.Store(0)
	defer missing.MaxArgs.Store(0)
	core.Apply(core.TypeType, core.Int8Type) // the process holds Type{Int8}
	New()                                    // what a VM makes at its start is made before the state is read

	// the image holds Ref{UInt16} with an instance the process's has none of
	// (given), Ref{UInt64} with the instance the process's has (kept),
	// Ref{UInt32} with the instance it has, or another, and a hint that is
	// unset here
	mk := func(instOfDiffers int64, wrapper bool) *craft {
		c := newCraft()
		ref := c.staticOf(t, refName, "*core.TypeName")
		five := int64(5)
		ids := []int{
			c.dtype(ref, []int{c.staticOf(t, core.UInt16Type, tDType)}, &five),
			c.dtype(ref, []int{c.staticOf(t, core.UInt64Type, tDType)}, &five),
			c.dtype(ref, []int{c.staticOf(t, core.UInt32Type, tDType)}, &instOfDiffers),
		}
		c.interned = countedSection(len(ids), new(rb).uv(uint64(ids[0])).b, new(rb).uv(uint64(ids[1])).b, new(rb).uv(uint64(ids[2])).b)
		hints := [][]byte{c.hintOf(t, missing, "MaxArgs", 9)}
		if wrapper { // Type{Wrapper} of Int8 is Int64's here, and Type{Int8} in the process
			hints = append(hints, c.hintOf(t, core.Int8Type.Name, "typeofWrapper", int64(c.staticOf(t, core.Int64Type, tDType))))
		}
		c.hints = countedSection(len(hints), hints...)
		return c
	}
	growth := vmGrowth()
	for _, k := range []struct {
		name   string
		c      *craft
		substr string
	}{
		{"an instance another than the process's", mk(8, false), "the instance of Ref"},
		{"a Type{Wrapper} another than the process's", mk(7, true), "typeofWrapper is another type"},
	} {
		before := snapshotShared()
		if _, err := LoadImage(sealCraft(k.c), LoadOptions{}); !errors.Is(err, ErrImageConflict) || !strings.Contains(err.Error(), k.substr) {
			t.Errorf("%s: %v", k.name, err)
		}
		if d := before.diff(snapshotShared(), growth); d != "" {
			t.Errorf("%s: a refused image wrote: %s", k.name, d)
		}
		if _, has := given.CachedInstance(); has || missing.MaxArgs.Load() != 0 {
			t.Errorf("%s: the instance was given or the hint set", k.name)
		}
	}

	// the control: with the instance the process holds, the image loads, gives
	// what was missing, leaves what is equal, and sets the hint
	if _, err := LoadImage(sealCraft(mk(7, false)), LoadOptions{}); err != nil {
		t.Fatal(err)
	}
	if v, has := given.CachedInstance(); !has || !(&VM{}).egal(v, int64(5)) {
		t.Errorf("the instance was not given: %v, %v", v, has)
	}
	if v, _ := kept.CachedInstance(); v != int64(5) {
		t.Errorf("an equal instance was replaced: %v", v)
	}
	if missing.MaxArgs.Load() != 9 {
		t.Errorf("the hint was not set: %d", missing.MaxArgs.Load())
	}
}

var refName = core.UnwrapUnionAll(core.RefType).(*core.DataType).Name

// imageOfUnionRef is an image that interns Ref{Union{Int8, Int16}}: a type of a
// shared name whose key does not hash, so that it lives in the linear cache of
// Ref, which every Ref{Union{...}} another goroutine makes grows.
func imageOfUnionRef(t *testing.T) []byte {
	c := newCraft()
	i8, i16 := c.staticOf(t, core.Int8Type, tDType), c.staticOf(t, core.Int16Type, tDType)
	dtIface := func(id int) *rb { return new(rb).uv(uint64(c.ti(tDType)) + 1).uv(uint64(id) + 2) }
	un := c.obj("*core.Union", append(dtIface(i8).b, dtIface(i16).b...))
	params := c.obj(tSlice, new(rb).uv(1).uv(uint64(c.ti("*core.Union"))+1).uv(uint64(un)+2).b)
	name := c.staticOf(t, refName, "*core.TypeName")
	r := new(rb).uv(uint64(name) + 2).uv(0).uv(uint64(params) + 2).uv(0)
	r.uv(0).uv(0).uv(0).uv(0).uv(0).uv(0).uv(0).uv(0)
	id := c.obj(tDType, r.b)
	c.interned = countedSection(1, new(rb).uv(uint64(id)).b)
	return c.body()
}

// A load whose commit finds a cache grown since it read it decodes the image
// again, and is refused, having written nothing, when that keeps happening.
func TestImageLoadRetriesWhenACacheChanges(t *testing.T) {
	body := imageOfUnionRef(t)
	grown := 0
	// another goroutine makes a Ref{Union{...}} of its own: one more type in
	// the cache the image's type is in
	grow := func() {
		k := must(core.NewType("Main", fmt.Sprintf("AtomicGrow%d", grown), core.AnyType, false, false, nil, nil))
		must(core.Apply(core.RefType, core.TypeUnion(core.Int8Type, k)))
		grown++
	}
	New() // what a VM makes at its start is made before the cache is looked at
	defer func() { imageDecoded = func() {} }()

	// once: the second decoding commits
	calls := 0
	imageDecoded = func() {
		if calls++; calls == 1 {
			grow()
		}
	}
	if _, err := LoadImage(bytes.NewReader(sealImage(&vmImageSpec, body)), LoadOptions{}); err != nil {
		t.Fatalf("a cache that changed once: %v", err)
	}
	if calls != 2 {
		t.Errorf("the image was decoded %d times, want 2", calls)
	}

	// every time: refused, and the types of the image are not in the cache
	before := snapshotShared()
	calls, grown0 := 0, grown
	imageDecoded = func() { calls++; grow() }
	_, err := LoadImage(bytes.NewReader(sealImage(&vmImageSpec, body)), LoadOptions{})
	if !errors.Is(err, ErrImageConflict) || !core.IsInternChanged(err) {
		t.Fatalf("a cache that changes every time: %v", err)
	}
	if calls != maxCommitTries || grown-grown0 != maxCommitTries {
		t.Errorf("%d decodings, %d changes, want %d", calls, grown-grown0, maxCommitTries)
	}
	after := snapshotShared()
	if n := len(after.types[refName]) - len(before.types[refName]); n != maxCommitTries {
		t.Errorf("the cache of Ref grew by %d, by the %d types of the test only", n, maxCommitTries)
	}
}
