package interp

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// What Base wrote on core's shared type names is saved, and set at load when
// unset; set to the same value it is left; set to another value (another image
// was loaded in this process) it is refused, naming the type and the field.
func TestImageHints(t *testing.T) {
	tn := core.MissingType.Name
	reset := func() {
		tn.MaxArgs.Store(0)
		tn.CacheEntryCount.Store(0)
		tn.MaxMethods.Store(0)
		tn.ConstpropHeuristic.Store(0)
	}
	defer reset()
	reset()
	with := func(maxArgs int32, others uint32) []byte {
		tn.MaxArgs.Store(maxArgs)
		if others != 0 {
			tn.CacheEntryCount.Store(others)
			tn.MaxMethods.Store(others + 1)
			tn.ConstpropHeuristic.Store(others + 2)
		}
		b := saveBytes(t, bootedVM(t))
		reset()
		return b
	}
	imgA, imgC, imgNone := with(7, 3), with(9, 3), with(0, 0)

	loadBytes(t, imgA)
	if tn.MaxArgs.Load() != 7 || tn.CacheEntryCount.Load() != 3 || tn.MaxMethods.Load() != 4 || tn.ConstpropHeuristic.Load() != 5 {
		t.Fatalf("the hints of the image were not set: %d %d %d %d", tn.MaxArgs.Load(), tn.CacheEntryCount.Load(),
			tn.MaxMethods.Load(), tn.ConstpropHeuristic.Load())
	}
	// a second VM of the process, the same image: the same values, nothing to do
	loadBytes(t, imgA)
	// another image: refused, before anything is changed
	_, err := LoadImage(bytes.NewReader(imgC), LoadOptions{})
	if !errors.Is(err, ErrImageConflict) || !strings.Contains(err.Error(), "Missing.MaxArgs is 7 here and 9 in the image") {
		t.Errorf("a different image: %v", err)
	}
	if tn.MaxArgs.Load() != 7 {
		t.Error("a refused image changed a hint")
	}
	// an image that holds no hint loads where they are set
	loadBytes(t, imgNone)
	if tn.MaxArgs.Load() != 7 {
		t.Error("an image without a hint changed one")
	}
}

// A cached Type{Wrapper} of the process and the image's must be one type.
func TestImageTypeofWrapperHint(t *testing.T) {
	// the process holds Type{Int8}; the image says Type{Wrapper} of Int8 is Int64's
	core.Apply(core.TypeType, core.Int8Type)
	idx, _ := core.StaticIndex(core.Int8Type.Name)
	i64, _ := core.StaticIndex(core.Int64Type)
	c := newCraft()
	obj := c.static(tDType, i64)
	c.hints = new(rb).uv(1).uv(uint64(idx)).uv(c.str("typeofWrapper")).zz(int64(obj)).b
	wantLoad(t, "another Type{Wrapper}", c, ErrImageConflict, "typeofWrapper is another type")

	// the same one, held by a VM that is saved and loaded: nothing to do, and
	// the loaded VM holds the type the process holds
	vm := bootedVM(t)
	tw := must(core.Apply(core.TypeType, core.Int8Type)).(*core.DataType)
	vm.Main.declare("tw", vm.newWorld()).Value = tw
	l := loadBytes(t, saveBytes(t, vm))
	if got := l.Main.own("tw").Value; got != any(tw) || core.Int8Type.Name.TypeofWrapper() != tw {
		t.Errorf("Type{Int8} is not the process's after a load: %v", got)
	}
	// a numeric conflict made by hand
	tn := core.MissingType.Name
	tn.ConstpropHeuristic.Store(5)
	defer tn.ConstpropHeuristic.Store(0)
	mi, _ := core.StaticIndex(tn)
	c = newCraft()
	c.hints = new(rb).uv(1).uv(uint64(mi)).uv(c.str("ConstpropHeuristic")).zz(6).b
	wantLoad(t, "a numeric hint", c, ErrImageConflict, "Missing.ConstpropHeuristic is 5 here and 6")
}

// A type the process holds is the type of the loaded VM, and two VMs loading
// one image are two VMs.
func TestImageLoadUniquesTypes(t *testing.T) {
	vm, _ := imageProgram(t)
	img := saveBytes(t, vm)
	a, b := loadBytes(t, img), loadBytes(t, img)
	memT := must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU))
	for name, l := range map[string]*VM{"first": a, "second": b} {
		sess := l.sessionFor(l.Main.own("L").Value.(*Module))
		v, err := sess.Eval("mem")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v.(*Memory).Type != memT {
			t.Errorf("%s load: the type of a memory is not the one the process holds", name)
		}
	}
	if a.Main == b.Main || a.Core == b.Core {
		t.Error("two loads share a module")
	}
	if _, err := a.sessionFor(a.Main.own("L").Value.(*Module)).Eval("lm1.x = 99"); err != nil {
		t.Fatal(err)
	}
	v, _ := b.sessionFor(b.Main.own("L").Value.(*Module)).Eval("lm1.x")
	if v != int64(10) {
		t.Errorf("a write in one VM is seen in the other: %v", v)
	}
}

// The root task, the sessions, a Go body, and a method object come back.
func TestImageLoadRebuilds(t *testing.T) {
	vm, s := imageProgram(t)
	vm.rootTask()
	vm.task.Fields[2] = "task-local storage"
	vm.defineJuliaSyntax(vm.Base)
	mod := s.Mod
	vm.sessionFor(mod)
	g := mod.own("which_g").Value.(*Function)
	vm.methodObject(g.Table, g.Table.Methods(g.Table.World())[0], g.Type, "which_g")
	b1 := saveBytes(t, vm)
	l := loadBytes(t, b1)

	if l.task == nil || l.task.Type != l.tTask || l.cur != l.task || l.curRec == nil || l.rootRec != l.curRec {
		t.Fatalf("the root task: %+v", l.task)
	}
	if l.task.Fields[2] != "task-local storage" {
		t.Errorf("task-local storage is %v", l.task.Fields[2])
	}
	if !bytes.Equal(b1, saveBytes(t, l)) {
		t.Error("save, load, save differ with a root task, a session and a reflected method")
	}
	lm := l.Main.own("L").Value.(*Module)
	if ls := l.sessions[lm]; ls == nil || ls.VM != l {
		t.Error("the session came back without its VM")
	}
	if len(l.reflected) != 1 || len(l.reflectedOf) != 1 {
		t.Fatalf("%d reflected, %d by method object", len(l.reflected), len(l.reflectedOf))
	}
	for _, r := range l.reflected {
		if l.reflectedOf[r.method] != r {
			t.Error("reflectedOf is not reflected by the method object")
		}
	}
	// the Go body is made again, and runs
	v, err := l.sessionFor(lm).Eval("Base.JuliaSyntax.enable_in_core!()")
	if err != nil || v != nil {
		t.Errorf("enable_in_core! on the loaded VM: %v, %v", v, err)
	}
	// a program that runs with the root task: a try block
	if _, err := l.sessionFor(lm).Eval("try; error(\"x\"); catch e; 1; end"); err != nil {
		t.Logf("a try block: %v", err)
	}
}

// The Base module Base_compiler.jl defines replaces the empty one New made, and
// both have a function named iterate: two keys of `funcs` that print alike
// until the module they were defined in is told apart.
func TestImageTwoModulesOfOneName(t *testing.T) {
	vm := bootedVM(t)
	m := vm.newModule("Base", vm.Main)
	vm.replaceBase(m)
	vm.newFunction(m, "iterate")
	b1 := saveBytes(t, vm)
	l := loadBytes(t, b1)
	// the boot Base is not the Base of the image: the objects New made are
	// named by the pointer, not by the field of the VM they were reached through
	if any(l.Base) == l.bootList[1].Interface() || l.Base.Parent != l.Main || l.modules["Base"] != l.Base {
		t.Error("the Base of the image is the empty Base New made")
	}
	if b2 := saveBytes(t, l); !bytes.Equal(b1, b2) {
		t.Error("save, load, save differ with two modules of one name")
	}
}

// Code that has its source and no prepared statements (what an image holds) is
// prepared when a frame is first made for it; code that cannot be prepared
// stays as it is.
func TestNewFramePreparesLoadedCode(t *testing.T) {
	vm, s := imageProgram(t)
	f := s.Mod.own("inc").Value.(*Function)
	lam := f.Table.Methods(f.Table.World())[0].Def.(*lambda)
	loaded := &code{src: lam.c.src, toplevel: lam.c.toplevel} // as an image leaves it
	fr := vm.newFrame(loaded, s.Mod)
	if loaded.stmts == nil || fr.c != loaded || loaded.nslots != lam.c.nslots || len(loaded.stmts) != len(lam.c.stmts) {
		t.Errorf("code not prepared: %d statements, %d slots", len(loaded.stmts), loaded.nslots)
	}
	// code with no source, and source that cannot be prepared
	vm.newFrame(&code{}, s.Mod)
	bad := &code{src: &expr.CodeInfo{SlotFlags: []any{uint8(0)}, SSAValueTypes: int64(0)}}
	if vm.newFrame(bad, s.Mod); bad.stmts != nil {
		t.Error("code that cannot be prepared was prepared")
	}
}

// A type whose parameter comes later in the list of interned types has the
// parameter interned first, and takes the type the process holds for it.
func TestImageInternParametersFirst(t *testing.T) {
	outer := must(core.NewType("Main", "InternOuter", core.AnyType, false, false, nil, nil))
	existing := must(core.Apply(core.RefType, core.Int8Type)).(*core.DataType)
	fresh := must(core.NewType("Main", "InternFresh", core.AnyType, false, false, nil, nil))
	cases := []struct {
		name     string
		param    *core.DataType // a type the image made
		redirect *core.DataType // what it becomes, nil if it stays
	}{
		{"one the process holds", &core.DataType{Name: existing.Name, Super: existing.Super, Parameters: append([]any(nil), existing.Parameters...)}, existing},
		{"a new one", &core.DataType{Name: existing.Name, Super: existing.Super, Parameters: []any{fresh}}, nil},
	}
	for _, c := range cases {
		a := &core.DataType{Name: outer.Name, Super: core.AnyType, Parameters: []any{c.param}}
		d := &imgDec{objs: []reflect.Value{reflect.ValueOf(c.param), reflect.ValueOf(a)}, redir: map[uint32]bool{}}
		st := &internState{done: map[uint32]bool{}, set: map[uint32]bool{0: true, 1: true},
			id: map[*core.DataType]uint32{c.param: 0, a: 1}, redirect: map[*core.DataType]*core.DataType{}}
		d.internOne(st, 1)
		d.internOne(st, 1) // interned once
		want := any(c.param)
		if c.redirect != nil {
			want = c.redirect
		}
		if a.Parameters[0] != want || d.redir[0] != (c.redirect != nil) || !st.done[0] || !st.done[1] {
			t.Errorf("%s: parameter %v, redirected %v", c.name, a.Parameters[0], d.redir)
		}
		if c.redirect != nil && d.objs[0].Interface() != any(c.redirect) {
			t.Errorf("%s: the object table still holds the image's type", c.name)
		}
	}
}

// The cached instance of a type the image made goes to the type the process
// holds in its place when that has none, and never replaces one it has.
func TestImageTransferInstances(t *testing.T) {
	mk := func(name string) *core.DataType {
		return must(core.NewType("Main", name, core.AnyType, false, false, nil, nil))
	}
	s1, c1 := mk("XferS1"), mk("XferC1") // the image's has an instance, the process's none
	s2, c2 := mk("XferS2"), mk("XferC2") // both have
	s3, c3 := mk("XferS3"), mk("XferC3") // the image's has none
	s1.SetCachedInstance("from the image")
	s2.SetCachedInstance("from the image")
	c2.SetCachedInstance("the process's")
	d := &imgDec{replaced: [][2]*core.DataType{{s1, c1}, {s2, c2}, {s3, c3}}}
	d.transferInstances()
	for name, c := range map[string]struct {
		dt   *core.DataType
		want any
		has  bool
	}{"given": {c1, "from the image", true}, "kept": {c2, "the process's", true}, "none to give": {c3, nil, false}} {
		got, has := c.dt.CachedInstance()
		if has != c.has || got != c.want {
			t.Errorf("%s: instance %v, %v", name, got, has)
		}
	}
}

// canon rewrites the types inside unions, wrappers, bounds, varargs and tuples
// to the ones the process holds, once each, and leaves nil and other values.
func TestImageCanon(t *testing.T) {
	shell := &core.DataType{Name: core.Int64Type.Name, Parameters: []any{core.Int8Type}}
	canon := core.Int32Type
	tv := &core.TypeVar{Name: "T", LB: shell, UB: shell}
	st := &internState{done: map[uint32]bool{}, set: map[uint32]bool{}, id: map[*core.DataType]uint32{},
		redirect: map[*core.DataType]*core.DataType{shell: canon}}
	d := &imgDec{}
	vararg := &core.Vararg{T: shell, N: int64(2)}
	union := &core.Union{A: shell, B: vararg}
	ua := &core.UnionAll{Var: tv, Body: union}
	tup := &core.Tuple{Type: shell, Elems: []any{shell, int64(1), nil}}
	outer := &core.DataType{Name: core.Int64Type.Name, Parameters: []any{ua, tup, shell, "text", (*core.DataType)(nil)}}
	seen := map[any]bool{}
	if got := d.canon(st, outer, seen); got != outer {
		t.Fatalf("a data type that is not redirected is returned as it is")
	}
	if outer.Parameters[2] != any(canon) || union.A != any(canon) || vararg.T != any(canon) || tv.LB != any(canon) || tv.UB != any(canon) ||
		tup.Type != canon || tup.Elems[0] != any(canon) || tup.Elems[1] != int64(1) || tup.Elems[2] != nil {
		t.Errorf("the redirected type is still in the graph: %+v", outer.Parameters)
	}
	// a cycle ends: the variable's bound is the type that holds the variable
	cyc := &core.TypeVar{Name: "C", LB: core.Bottom}
	holder := &core.DataType{Name: core.Int64Type.Name, Parameters: []any{cyc}}
	cyc.UB = holder
	d.canon(st, holder, map[any]bool{})
	d.canon(st, cyc, map[any]bool{})
	// nil of each kind, and a seen object, are returned as they are
	for _, x := range []any{(*core.UnionAll)(nil), (*core.Union)(nil), (*core.TypeVar)(nil), (*core.Vararg)(nil), (*core.Tuple)(nil), 7, nil} {
		if got := d.canon(st, x, map[any]bool{}); got != x {
			t.Errorf("canon of %v is %v", x, got)
		}
	}
	for _, x := range []any{ua, union, tv, vararg, tup} {
		if got := d.canon(st, x, map[any]bool{x: true}); got != x {
			t.Errorf("a seen %T is rewritten", x)
		}
	}
}

// ----- bombs: images that declare more than they hold, under a memory limit -----

// bomb loads an image made of body, which must be refused with kind, with
// little allocated, under a memory limit.
func bomb(t *testing.T, name string, body []byte, opts LoadOptions, kind error) {
	t.Helper()
	prev := debug.SetMemoryLimit(256 << 20)
	defer debug.SetMemoryLimit(prev)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := LoadImage(bytes.NewReader(sealImage(&vmImageSpec, body)), opts)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, kind) {
		t.Errorf("%s: %v, want %v", name, err, kind)
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Errorf("%s: %d MB allocated for a refusal", name, grown>>20)
	}
}

func TestImageLoadBombs(t *testing.T) {
	huge := uint64(1) << 40
	str := func(s string) []byte { return new(rb).uv(uint64(len(s))).raw([]byte(s)...).b }
	cat := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }
	empty3 := []byte{0, 0, 0} // roots, hints, interned: empty sections
	zero := new(rb).uv(0).b
	// a table of 2^40 types, objects, strings
	bomb(t, "types", new(rb).uv(huge).b, LoadOptions{}, ErrImageTruncated)
	bomb(t, "objects", cat(zero, new(rb).uv(huge).b), LoadOptions{}, ErrImageTruncated)
	bomb(t, "strings", cat(zero, zero, new(rb).uv(huge).b), LoadOptions{}, ErrImageTruncated)
	// a string longer than the image
	bomb(t, "a long type", cat(new(rb).uv(1).uv(huge).b), LoadOptions{}, ErrImageTruncated)
	// 1000 objects when 10 are allowed
	var kinds rb
	kinds.uv(1).raw(str("*interp.Struct")...).uv(1000)
	for i := 0; i < 1000; i++ {
		kinds.uv(0)
	}
	bomb(t, "objects over the limit", kinds.b, LoadOptions{MaxObjects: 10}, ErrImageLimit)
	// a slice of 2^40 elements, a map of 2^40 entries, 2^40 roots, hints, interned types, in records of a few bytes
	for name, typ := range map[string]string{"a slice": "[]interface {}", "a map": "map[string]interface {}"} {
		var b rb
		b.uv(1).raw(str(typ)...).uv(1).uv(0).uv(0) // one object of the type, tier full
		b.uv(0)                                    // no strings
		b.uv(uint64(len(new(rb).uv(huge).b))).raw(new(rb).uv(huge).b...)
		bomb(t, name, cat(b.b, empty3), LoadOptions{}, ErrImageCorrupt)
	}
	for name, sec := range map[string]int{"roots": 0, "hints": 1, "interned types": 2} {
		var b rb
		b.uv(0).uv(0).uv(0) // no types, objects, strings
		for i := 0; i < 3; i++ {
			if i == sec {
				b.uv(uint64(len(new(rb).uv(huge).b))).raw(new(rb).uv(huge).b...)
			} else {
				b.uv(1).uv(0)
			}
		}
		bomb(t, name, b.b, LoadOptions{}, ErrImageCorrupt)
	}
	// a byte limit under the image
	bomb(t, "bytes over the limit", make([]byte, 5000), LoadOptions{MaxBytes: 1000}, ErrImageLimit)

	// a stream that never ends: refused at its header, or at the limit when
	// the header is the interpreter's
	prev := debug.SetMemoryLimit(256 << 20)
	defer debug.SetMemoryLimit(prev)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := LoadImage(zeros{}, LoadOptions{})
	if !errors.Is(err, ErrImageCorrupt) {
		t.Errorf("a stream of zeros: %v", err)
	}
	valid := sealImage(&vmImageSpec, nil)[:len(imageMagic)+4+32]
	_, err = LoadImage(io.MultiReader(bytes.NewReader(valid), zeros{}), LoadOptions{MaxBytes: 1 << 20})
	if !errors.Is(err, ErrImageLimit) {
		t.Errorf("a stream with a header and no end: %v", err)
	}
	runtime.ReadMemStats(&after)
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 64<<20 {
		t.Errorf("%d MB allocated for two endless streams", grown>>20)
	}
}

// zeros is a reader that never ends.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) { clear(p); return len(p), nil }

// ----- the fuzz target -----

var imageErrors = []error{ErrImageKey, ErrImageChecksum, ErrImageTruncated, ErrImageLimit, ErrImageCorrupt, ErrImageConflict}

// seedBody is the body of the image of a VM that has not booted: the smallest
// real image.
func seedBody(t testing.TB) []byte {
	var b bytes.Buffer
	if err := New().SaveImage(&b, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()[len(imageMagic)+4+32 : b.Len()-32]
}

// FuzzLoadImage seals the bytes it is given as the body of an image, so that
// they reach the decoder and not the checksum, and loads them under limits:
// whatever they are, the answer is a VM or a typed error, never a crash or a
// hang. The corpus in testdata/fuzz holds the image of a fresh VM, cut at
// section boundaries.
func FuzzLoadImage(f *testing.F) {
	f.Add(seedBody(f))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0})
	f.Fuzz(func(t *testing.T, body []byte) {
		prev := debug.SetMemoryLimit(512 << 20)
		defer debug.SetMemoryLimit(prev)
		type result struct {
			vm  *VM
			err error
		}
		done := make(chan result, 1)
		go func() {
			vm, err := LoadImage(bytes.NewReader(sealImage(&vmImageSpec, body)), LoadOptions{MaxBytes: 1 << 22, MaxObjects: 1 << 16})
			done <- result{vm, err}
		}()
		var r result
		select {
		case r = <-done:
		case <-time.After(20 * time.Second):
			t.Fatal("a load took more than 20 seconds: a hang")
		}
		vm, err := r.vm, r.err
		if err == nil {
			if vm == nil {
				t.Fatal("no VM and no error")
			}
			return
		}
		for _, k := range imageErrors {
			if errors.Is(err, k) {
				return
			}
		}
		t.Fatalf("an untyped error: %v", err)
	})
}

// TestWriteImageFuzzSeeds regenerates the corpus: JLGO_FUZZ_SEEDS=1.
func TestWriteImageFuzzSeeds(t *testing.T) {
	if os.Getenv("JLGO_FUZZ_SEEDS") != "1" {
		t.Skip("set JLGO_FUZZ_SEEDS=1 to rewrite testdata/fuzz/FuzzLoadImage")
	}
	dir := filepath.Join("testdata", "fuzz", "FuzzLoadImage")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := seedBody(t)
	for name, b := range map[string][]byte{
		"fresh-vm":              body,
		"fresh-vm-half":         body[:len(body)/2],
		"fresh-vm-cut-near-end": body[:len(body)-len(body)/20],
	} {
		text := "go test fuzz v1\n[]byte(" + strconv.Quote(string(b)) + ")\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
