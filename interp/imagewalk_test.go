package interp

// imagewalk is the guard of the system image (#40, step 1): a walk over
// everything the image would hold, that is everything reachable from the saved
// roots of a *VM, which classifies every Go kind it meets and FAILS on one the
// image format does not know yet, naming it with the path that reached it. The
// encoder's kind table must cover the walk's table; the day the VM grows a
// field or a value of a new type, this test is the one that goes red.
//
// It is a reflect walk, in a test only: nothing at run time reads unexported
// fields this way. What it counts are the encoder's units: objects (pointers,
// maps and values boxed in an interface), references to them, scalars and
// strings.

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"unsafe"
	"weak"
)

// imageRoots classifies every field of VM: true when the image saves it,
// false when it is host state that New or __init__ rebuilds (design §1, §2).
// A field in neither list fails the walk.
var imageRoots = map[string]bool{
	"world": true, "Core": true, "Base": true, "Main": true, "modules": true,
	"topModule": true, "baseModule": true, "funcs": true, "ctors": true, "others": true,
	"sessions": true, "moduleInitOrder": true, "typeinfFunc": true, "typeinfWorld": true,
	"gsCtr": true, "idCtr": true, "moduleCtr": true, "tTask": true, "task": true, "mtBackedges": true, "emptyMemory": true,
	"singletons": true, "intrinsics": true, "reflected": true, "reflectedOf": true,
	"methodTable": true, "cglobals": true, "addrs": true, "argVectors": true,
	"builtinType": true, "intrinsicType": true, "typeofFn": true,
	"tMethod": true, "tMethodInstance": true, "tMethodMatch": true, "tCodeInstance": true,
	"tBinding": true, "tBindingPartition": true,
	"tError": true, "tUndefVar": true, "tTypeError": true, "tMethodError": true,
	"tStackOverflow": true, "tArgError": true, "tBounds": true, "tUndefRef": true,
	"tDivide": true, "tLoadError": true, "tAtomicError": true, "tFieldError": true,
	"tExpr": true, "tQuoteNode": true, "tSSAValue": true, "tSlotNumber": true,
	"tArgument": true, "tGotoNode": true, "tGotoIfNot": true, "tEnterNode": true,
	"tReturnNode": true, "tNewvarNode": true, "tCodeInfo": true,
	"tLineNumberNode": true, "tGlobalRef": true,

	// host fields, never saved
	"shims": false, "libShims": false, "libCglobals": false, "dynLibs": false, "lbt": false,
	"mpfr": false, "deferSignal": false, "finalizerRNG": false, "ptlsRng": false, "vfs": false,
	"ios": false, "reqs": false, "iolock": false, "errno": false, "Sources": false,
	"Stdlib": false, "Env": false, "Host": false, "HomeDir": false, "WorkDir": false,
	"BinDir": false, "Args": false, "Stdin": false, "Stdout": false, "Stderr": false,
	"MaxDepth": false, "MaxSteps": false, "MaxTasks": false, "MaxHandles": false,
	"MaxMemoryElems": false, "depth": false, "steps": false, "excStack": false,
	"taskWorld": false, "cur": false, "nextTask": false, "exitTo": false, "curRec": false,
	"rootRec": false, "running": false, "liveTasks": false, "taskSeq": false,
	"wakeup": false, "threaded": false, "peekCond": false, "loop": false, "taskMu": false,
	"taskRecs": false, "finalizersInhibited": false, "typeinfTiming": false,
	"strMu": false, "strBufs": false, "argMu": false, "irTags": false,
	"reservations": false, "openModules": false, "GeneratingOutput": false,
	"RunCompilerBootstrap": false,
}

// imageSkip lists the struct fields the walk does not enter: state that is
// rebuilt at load, with the reason (design §2).
var imageSkip = map[string]string{
	"interp.code.stmts":     "prepared statements, rebuilt lazily from src",
	"dispatch.Table.leaf":   "dispatch cache, refilled",
	"dispatch.Table.canon":  "dispatch cache, refilled",
	"dispatch.Table.tm":     "typemap, replays events on first query",
	"dispatch.Table.events": "typemap input, replayed",
	"dispatch.Table.clips":  "dispatch cache, refilled",
	"core.TypeName.hashed":  "weak type cache, re-inserted with cacheOrGet",
	"core.TypeName.linear":  "weak type cache, re-inserted with cacheOrGet",
	"interp.Session.VM":     "host back-pointer to the VM",
}

// imageKinds is the table of every Go type the walk may meet behind a pointer
// or an interface, and what the image does with it.
const (
	kindSaved   = "saved"   // a record of the image
	kindForeign = "foreign" // C handle: saved as C_NULL in a mutable slot, refused elsewhere
	kindStatic  = "static"  // process-wide, written as a Static(i) reference
)

var imageKinds = map[string]string{
	"*interp.Module": kindSaved, "*interp.Binding": kindSaved,
	"*core.TypeName": kindSaved, "*core.DataType": kindSaved, "*core.UnionAll": kindSaved,
	"*core.Union": kindSaved, "*core.TypeVar": kindSaved, "*core.Vararg": kindSaved,
	"*core.Instance": kindSaved, "*core.Tuple": kindSaved, "core.coreModule": kindStatic,
	"*interp.Function": kindSaved, "*interp.Builtin": kindSaved, "*interp.Intrinsic": kindSaved,
	"*dispatch.Table": kindSaved, "*dispatch.Method": kindSaved,
	"*interp.lambda": kindSaved, "*interp.code": kindSaved,
	"*expr.CodeInfo": kindSaved, "*expr.DebugInfo": kindSaved, "*expr.Expr": kindSaved,
	"expr.QuoteNode": kindSaved, "expr.LineNumberNode": kindSaved, "expr.GlobalRef": kindSaved,
	"expr.Module": kindSaved, "expr.GotoIfNot": kindSaved, "expr.ReturnNode": kindSaved,
	"expr.EnterNode": kindSaved, "expr.NewvarNode": kindSaved, "expr.SSAValue": kindSaved,
	"expr.SlotNumber": kindSaved, "expr.Argument": kindSaved, "expr.GotoNode": kindSaved,
	"expr.LineInfoNode": kindSaved, "expr.Tagged": kindSaved, "expr.Opaque": kindSaved,
	"expr.Vector": kindSaved, "expr.Symbol": kindSaved, "expr.Char": kindSaved,
	"*interp.Struct": kindSaved, "interp.SVec": kindSaved,
	"*interp.Memory": kindSaved, "*interp.MemoryRef": kindSaved,
	"*interp.Pointer": kindSaved, "*interp.ptrSlot": kindSaved, "*interp.outStream": kindSaved,
	"interp.PrimValue": kindSaved, "interp.Float16": kindSaved, "interp.BFloat16": kindSaved,
	"interp.Int128": kindSaved, "interp.UInt128": kindSaved,
	"*core.bottomType": kindStatic, "*core.instanceBox": kindSaved, "interp.goMethod": kindSaved, "*interp.goBody": kindSaved,
	"*struct { _ uint8 }": kindStatic, // undef, one per process (value.go:19)
	"*interp.Session":     kindSaved, "*lower.State": kindSaved,
	"interp.cfunc":      kindSaved,                               // a CodeInstance's fptr name (codeinst.go:41)
	"*interp.reflected": kindSaved, "interp.layoutOf": kindSaved, // VM.reflected, VM.addrs keys
	"*pcre2.Code": kindForeign, "*pcre2.JITStack": kindForeign, "*pcre2.MatchContext": kindForeign,
}

// imageFuncs are the Go funcs reachable from the saved roots, by the field
// that holds them, and how a loaded VM gets them back (design §2).
var imageFuncs = map[string]string{
	"interp.Intrinsic.Fn": "rebound by ID from the loading VM's New()",
	"interp.Builtin.Fn":   "rebound by name from the loading VM's New()",
	"dispatch.Method.Def": "a goMethod boot made: a boot object, comes with New()",
	"interp.goBody.fn":    "made again by newGoBody from the saved name and capture (gobodies.go)",
	"interp.outStream.w":  "host writer, rebound by New()",
}

// imageWeaks are the weak pointers reachable from the saved roots.
var imageWeaks = map[string]string{
	"interp.Struct.exprOwner":        "saved as a field of the Expr record",
	"interp.addrSpace.byTarget[key]": "a region whose target is saved keeps its id and next",
	"interp.addrSpace.byID":          "the same regions, by id",
	"interp.VM.argVectors[key]":      "an entry is saved only when its key is strongly reachable",
}

type imageSpec struct {
	roots map[string]bool
	kinds map[string]string
	skip  map[string]string
	funcs map[string]string
	weaks map[string]string
}

var vmImageSpec = imageSpec{roots: imageRoots, kinds: imageKinds, skip: imageSkip, funcs: imageFuncs, weaks: imageWeaks}

// imageUnknown is a kind the image format does not know, with the first path
// that reached it.
type imageUnknown struct {
	What string
	Path string
}

type imageStats struct {
	Objects, Edges, Scalars, StringRefs, StringBytes int
	Strings                                          int
	Kinds                                            map[string]int // objects by Go type
	Funcs                                            map[string]int // by owner field
	Weaks                                            map[string]int
	Unknown                                          []imageUnknown
}

type imageSeg struct {
	t     reflect.Type // struct type of a field segment
	field int          // field index, or -1
	idx   int          // slice/array index, or -1
	tag   byte         // 'f' field, 'i' index, 'k' map key, 'v' map value, 'r' root
	name  string       // root name
}

type imageWalker struct {
	spec    imageSpec
	st      *imageStats
	seen    map[[3]uintptr]bool
	strs    map[string]struct{}
	path    []imageSeg
	known   map[reflect.Type]bool
	fields  map[reflect.Type][]imageField
	unknown map[string]bool
}

type imageField struct {
	name string // "pkg.Type.field"
	skip bool
}

// walkImage walks everything reachable from the saved fields of the struct
// root points to, and reports what it could not classify.
func walkImage(root any, spec imageSpec) *imageStats {
	w := &imageWalker{spec: spec, st: &imageStats{Kinds: map[string]int{}, Funcs: map[string]int{}, Weaks: map[string]int{}},
		seen: map[[3]uintptr]bool{}, strs: map[string]struct{}{}, known: map[reflect.Type]bool{},
		fields: map[reflect.Type][]imageField{}, unknown: map[string]bool{}}
	rp := reflect.ValueOf(root)
	rv := rp.Elem()
	rt := rv.Type()
	w.seen[w.ptrKey(rp)] = true
	label := rt.String()
	// the heap first, so that each object is attributed to its first root;
	// fields are visited in declaration order, deterministic
	for i := 0; i < rt.NumField(); i++ {
		name := rt.Field(i).Name
		saved, ok := spec.roots[name]
		switch {
		case !ok:
			w.fail(label + "." + name + " (a field of the root no table classifies)")
		case saved:
			w.path = append(w.path[:0], imageSeg{tag: 'r', name: label + "." + name})
			w.walk(rv.Field(i), label+"."+name)
		}
	}
	w.st.Strings = len(w.strs)
	return w.st
}

func (w *imageWalker) ptrKey(v reflect.Value) [3]uintptr {
	return [3]uintptr{v.Pointer(), uintptr(unsafe.Pointer(reflect.ValueOf(v.Type()).Pointer())), 0}
}

func (w *imageWalker) format() string {
	// a path through a Base build is hundreds of steps deep: keep where it
	// started and the last steps before the kind
	const head, tail = 4, 14
	var b strings.Builder
	for i, s := range w.path {
		if n := len(w.path); n > head+tail && i >= head && i < n-tail {
			if i == head {
				fmt.Fprintf(&b, " > ... (%d steps) ...", n-head-tail)
			}
			continue
		}
		if i > 0 {
			b.WriteString(" > ")
		}
		switch s.tag {
		case 'r':
			b.WriteString(s.name)
		case 'f':
			b.WriteString(s.t.String() + "." + s.t.Field(s.field).Name)
		case 'i':
			fmt.Fprintf(&b, "[%d]", s.idx)
		case 'k':
			b.WriteString("[map key]")
		case 'v':
			b.WriteString("[map value]")
		}
	}
	return b.String()
}

// fail records an unknown kind once per description, with the current path.
func (w *imageWalker) fail(what string) {
	if w.unknown[what] {
		return
	}
	w.unknown[what] = true
	w.st.Unknown = append(w.st.Unknown, imageUnknown{What: what, Path: w.format()})
}

func (w *imageWalker) obj(kind string) {
	w.st.Objects++
	w.st.Kinds[kind]++
}

// checkKind fails on a type behind a pointer or an interface the kind table
// does not list. Builtin scalar types and unnamed composites are known.
func (w *imageWalker) checkKind(t reflect.Type, always bool) {
	ok, done := w.known[t]
	if done {
		if !ok {
			w.fail(t.String())
		}
		return
	}
	switch {
	case !always && t.PkgPath() == "":
		ok = true // a builtin scalar or an unnamed composite: its parts are walked
	case t.PkgPath() == "weak":
		ok = true // classified by the field that holds it (imageWeaks)
	default:
		_, ok = w.spec.kinds[t.String()]
	}
	w.known[t] = ok
	if !ok {
		w.fail(t.String())
	}
}

func (w *imageWalker) push(s imageSeg) { w.path = append(w.path, s) }
func (w *imageWalker) pop()            { w.path = w.path[:len(w.path)-1] }

func (w *imageWalker) structFields(t reflect.Type) []imageField {
	if f, ok := w.fields[t]; ok {
		return f
	}
	f := make([]imageField, t.NumField())
	for i := range f {
		f[i].name = t.String() + "." + t.Field(i).Name
		_, f[i].skip = w.spec.skip[f[i].name]
	}
	w.fields[t] = f
	return f
}

func (w *imageWalker) walk(v reflect.Value, owner string) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		w.st.Edges++
		key := w.ptrKey(v)
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		w.checkKind(v.Type(), true)
		w.obj(v.Type().String())
		w.walk(v.Elem(), v.Type().Elem().String())
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		e := v.Elem()
		switch e.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.String, reflect.UnsafePointer:
			if e.Type().PkgPath() != "" {
				w.checkKind(e.Type(), true) // a named slice, func or string
			}
		default:
			// a value boxed in an interface is an object of the image
			w.st.Edges++
			w.checkKind(e.Type(), false)
			w.obj("value " + e.Type().String())
		}
		w.walk(e, owner)
	case reflect.Struct:
		t := v.Type()
		switch {
		case t.PkgPath() == "weak":
			w.st.Weaks[owner]++
			if _, ok := w.spec.weaks[owner]; !ok {
				w.fail("weak pointer at " + owner)
			}
			return
		case t.PkgPath() == "sync":
			return
		case t.PkgPath() == "sync/atomic" && strings.HasPrefix(t.Name(), "Pointer["):
			if p := v.FieldByName("v").UnsafePointer(); p != nil {
				w.walk(reflect.NewAt(t.Field(0).Type.Elem().Elem(), p), t.String())
			}
			return
		}
		fs := w.structFields(t)
		for i := range fs {
			if fs[i].skip {
				continue
			}
			w.push(imageSeg{t: t, field: i, tag: 'f'})
			w.walk(v.Field(i), fs[i].name)
			w.pop()
		}
	case reflect.Slice:
		if v.IsNil() {
			return
		}
		key := [3]uintptr{v.Pointer(), uintptr(unsafe.Pointer(reflect.ValueOf(v.Type()).Pointer())), uintptr(v.Len())}
		if w.seen[key] {
			w.st.Edges++
			return
		}
		w.seen[key] = true
		w.elems(v, owner)
	case reflect.Array:
		w.elems(v, owner)
	case reflect.Map:
		if v.IsNil() {
			return
		}
		key := [3]uintptr{v.Pointer(), 1, 0}
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		w.obj(v.Type().String())
		ko := owner + "[key]"
		it := v.MapRange()
		for it.Next() {
			w.push(imageSeg{tag: 'k'})
			w.walk(it.Key(), ko)
			w.pop()
			w.push(imageSeg{tag: 'v'})
			w.walk(it.Value(), owner)
			w.pop()
		}
	case reflect.String:
		w.st.StringRefs++
		s := v.String()
		if _, ok := w.strs[s]; !ok {
			w.strs[s] = struct{}{}
			w.st.StringBytes += len(s)
		}
	case reflect.Func:
		if !v.IsNil() {
			w.st.Funcs[owner]++
			if _, ok := w.spec.funcs[owner]; !ok {
				w.fail("func at " + owner)
			}
		}
	case reflect.Chan:
		if !v.IsNil() {
			w.fail("chan at " + owner)
		}
	case reflect.UnsafePointer:
		if v.Pointer() != 0 {
			w.fail("unsafe.Pointer at " + owner)
		}
	default:
		w.st.Scalars++
	}
}

func (w *imageWalker) elems(v reflect.Value, owner string) {
	for i := 0; i < v.Len(); i++ {
		w.push(imageSeg{tag: 'i', idx: i})
		w.walk(v.Index(i), owner)
		w.pop()
	}
}

func (st *imageStats) report(vm *VM) string {
	var b strings.Builder
	fmt.Fprintf(&b, "objects %d, edges %d, scalars %d, string refs %d, distinct strings %d (%d bytes), world %d, moduleInitOrder %d\n",
		st.Objects, st.Edges, st.Scalars, st.StringRefs, st.Strings, st.StringBytes, vm.World(), len(vm.moduleInitOrder))
	for _, m := range []struct {
		title string
		m     map[string]int
	}{{"objects by Go type", st.Kinds}, {"funcs by owner field", st.Funcs}, {"weak pointers by owner field", st.Weaks}} {
		type kv struct {
			k string
			v int
		}
		var all []kv
		for k, v := range m.m {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || all[i].v == all[j].v && all[i].k < all[j].k })
		fmt.Fprintf(&b, "== %s\n", m.title)
		for _, e := range all {
			fmt.Fprintf(&b, "%10d  %s\n", e.v, e.k)
		}
	}
	return b.String()
}

func requireClassified(t *testing.T, st *imageStats) {
	t.Helper()
	for _, u := range st.Unknown {
		t.Errorf("the image format has no kind for %s\n  reached as: %s", u.What, u.Path)
	}
}

// emptyHostState is what SaveImage requires before it runs: nothing open,
// nothing leased.
func requireCutState(t *testing.T, vm *VM) {
	t.Helper()
	if n := len(vm.openModules); n != 0 {
		t.Errorf("%d modules are open", n)
	}
	if n := len(vm.reservations); n != 0 {
		t.Errorf("%d engine reservations are live", n)
	}
}

// TestImageWalkBoot walks the VM after Boot() alone, in every CI run: the
// whole walk, every table, in seconds.
func TestImageWalkBoot(t *testing.T) {
	vm := New()
	if err := vm.Boot(); err != nil {
		t.Fatal(err)
	}
	st := walkImage(vm, vmImageSpec)
	requireClassified(t, st)
	requireCutState(t, vm)
	t.Log(st.report(vm))
	if st.Objects < 1000 || st.Kinds["*core.DataType"] == 0 || st.Kinds["*interp.Module"] < 3 {
		t.Errorf("the walk saw too little: %d objects, %d DataTypes, %d Modules",
			st.Objects, st.Kinds["*core.DataType"], st.Kinds["*interp.Module"])
	}
	for owner := range st.Funcs {
		if _, ok := imageFuncs[owner]; !ok {
			t.Errorf("func at %s is not classified", owner)
		}
	}
	// the walk is deterministic: the same VM gives the same counts
	again := walkImage(vm, vmImageSpec)
	if again.Objects != st.Objects || again.Edges != st.Edges || again.Scalars != st.Scalars || again.Strings != st.Strings {
		t.Errorf("two walks of one VM differ: %+v vs %+v", st, again)
	}
}

type walkUnknownT struct{ a int }
type walkNamedInt int

// walkToy has one field in each state the walk must tell apart.
type walkToy struct {
	Saved   any
	Host    any
	Gap     int // in no table
	Ch      chan int
	Fn      func()
	Raw     unsafe.Pointer
	Skipped *walkUnknownT
	W       weak.Pointer[int]
	Deep    any // a kind forty steps down: the path is shortened
}

type walkDeepT struct{ a int }

// TestImageWalkFailsOnUnknown shows the guard goes red, each time with the
// kind and the path that reached it, for every way a kind can be unknown.
func TestImageWalkFailsOnUnknown(t *testing.T) {
	x := 7
	var deep any = &walkDeepT{}
	for i := 0; i < 40; i++ {
		deep = []any{deep}
	}
	toy := &walkToy{
		Saved:   []any{&walkUnknownT{}, walkNamedInt(3), map[string]any{"k": &x}, int64(1), "s"},
		Host:    &walkUnknownT{},
		Ch:      make(chan int),
		Fn:      func() {},
		Raw:     unsafe.Pointer(&x),
		Skipped: &walkUnknownT{},
		W:       weak.Make(&x),
		Deep:    deep,
	}
	spec := imageSpec{
		roots: map[string]bool{"Saved": true, "Host": false, "Ch": true, "Fn": true, "Raw": true, "Skipped": true, "W": true, "Deep": true},
		kinds: map[string]string{},
		skip:  map[string]string{"interp.walkToy.Skipped": "test"},
	}
	st := walkImage(toy, spec)
	want := map[string]string{
		"*interp.walkUnknownT":                 "interp.walkToy.Saved > [0]",
		"interp.walkNamedInt":                  "interp.walkToy.Saved > [1]",
		"*int":                                 "[map value]",
		"chan at interp.walkToy.Ch":            "interp.walkToy.Ch",
		"func at interp.walkToy.Fn":            "interp.walkToy.Fn",
		"unsafe.Pointer at interp.walkToy.Raw": "interp.walkToy.Raw",
		"weak pointer at interp.walkToy.W":     "interp.walkToy.W",
		"*interp.walkDeepT":                    "... (23 steps) ... > [0]",
		"interp.walkToy.Gap (a field of the root no table classifies)": "",
	}
	if len(st.Unknown) != len(want) {
		t.Errorf("%d unknown kinds, want %d: %+v", len(st.Unknown), len(want), st.Unknown)
	}
	for _, u := range st.Unknown {
		p, ok := want[u.What]
		if !ok {
			t.Errorf("unexpected unknown %q", u.What)
		} else if !strings.Contains(u.Path, p) {
			t.Errorf("%s reached as %q, want it to contain %q", u.What, u.Path, p)
		}
	}
}

// inRange reports a count outside its band.
func inRange(t *testing.T, name string, got, want int, pct int) {
	t.Helper()
	lo, hi := want-want*pct/100, want+want*pct/100
	if got < lo || got > hi {
		t.Errorf("%s = %d, outside %d..%d (the design measured %d)", name, got, lo, hi, want)
	}
}

// TestImageWalkBase is the walk after Base is built, at the cut where julia
// writes sys.so (design §0): sysimg.jl loaded, module initializers waiting.
// It checks the design's measured counts as ranges, and that every kind the
// build leaves is classified. Over ten minutes: JLGO_BASE=1.
func TestImageWalkBase(t *testing.T) {
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
	requireCutState(t, vm)
	st := walkImage(vm, vmImageSpec)
	requireClassified(t, st)
	t.Log(st.report(vm))
	// design §1, measured on v0.13.0 + #39 (84 modules, world 38766)
	inRange(t, "objects", st.Objects, 3268688, 15)
	inRange(t, "edges", st.Edges, 4247336, 15)
	inRange(t, "scalars", st.Scalars, 3037684, 15)
	inRange(t, "distinct strings", st.Strings, 54870, 15)
	inRange(t, "string bytes", st.StringBytes, 3030370, 15)
	inRange(t, "*core.DataType", st.Kinds["*core.DataType"], 61136, 15)
	inRange(t, "*expr.Expr", st.Kinds["*expr.Expr"], 328183, 15)
	inRange(t, "*interp.Struct", st.Kinds["*interp.Struct"], 35551, 15)
	inRange(t, "*interp.Memory", st.Kinds["*interp.Memory"], 25519, 15)
	inRange(t, "*dispatch.Method", st.Kinds["*dispatch.Method"], 23282, 15)
	inRange(t, "*interp.Module", st.Kinds["*interp.Module"], 84, 15)
	inRange(t, "moduleInitOrder", len(vm.moduleInitOrder), 75, 15)
	inRange(t, "Go funcs", st.Funcs["interp.Intrinsic.Fn"]+st.Funcs["interp.Builtin.Fn"]+
		st.Funcs["dispatch.Method.Def"]+st.Funcs["interp.goBody.fn"]+st.Funcs["interp.outStream.w"], 165, 15)
	// the two bodies made while Base is built are goBodies, the nine boot
	// made are goMethods
	inRange(t, "goBody.fn", st.Funcs["interp.goBody.fn"], 2, 0)
	inRange(t, "goMethod", st.Funcs["dispatch.Method.Def"], 9, 0)
	inRange(t, "Struct.exprOwner", st.Weaks["interp.Struct.exprOwner"], 35551, 15)
	inRange(t, "argVectors keys", st.Weaks["interp.VM.argVectors[key]"], 5277, 15)
}
