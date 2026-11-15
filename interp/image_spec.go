package interp

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"reflect"
	"regexp"
	"runtime/debug"
	"sort"
	"sync"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/lower"
	"github.com/go-regexp/pcre2"
)

// What a system image holds, classified (design of #40, sections 1 and 2).
// The tables are the encoder's: it saves what imageRoots marks saved, skips
// what imageSkip lists, and refuses a Go type, func or weak pointer no table
// knows. imagewalk (imagewalk_test.go) walks a built VM with the same tables
// and fails on a kind the encoder would refuse.

// imageRoots classifies every field of VM: true when the image saves it,
// false when it is host state that New or __init__ rebuilds. A field in
// neither list fails the encoder and imagewalk.
var imageRoots = map[string]bool{
	"world": true, "Core": true, "Base": true, "Main": true, "modules": true,
	"topModule": true, "baseModule": true, "funcs": true, "ctors": true, "others": true,
	"sessions": true, "moduleInitOrder": true, "typeinfFunc": true, "typeinfWorld": true,
	"gsCtr": true, "idCtr": true, "moduleCtr": true, "tTask": true, "task": true, "mtBackedges": true, "emptyMemory": true,
	"singletons": true, "intrinsics": true, "reflected": true,
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

	// reflectedOf is reflected indexed by the method object, set with it at
	// reflect.go:160 and nowhere else: a loader rebuilds it from reflected
	"reflectedOf": false,

	// host fields, never saved; bootIdx is the boot enumeration (imageboot.go)
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
	"RunCompilerBootstrap": false, "bootIdx": false, "bootList": false, "bootErr": false,
}

// imageSkip lists the struct fields the encoder does not enter: state that is
// rebuilt at load, with the reason (design section 2).
var imageSkip = map[string]string{
	"interp.code.stmts":     "prepared statements, rebuilt lazily from src",
	"dispatch.Table.leaf":   "dispatch cache, refilled",
	"dispatch.Table.canon":  "dispatch cache, refilled",
	"dispatch.Table.tm":     "typemap, replays events on first query",
	"dispatch.Table.events": "typemap input, replayed",
	"dispatch.Table.clips":  "dispatch cache, refilled",
	"core.TypeName.hashed":  "weak type cache, re-inserted with cacheOrGet",
	"core.TypeName.linear":  "weak type cache, re-inserted with cacheOrGet",
	"interp.Session.VM":     "host back-pointer to the VM, set by the loader",

	// what the typemap derives from the methods: a loader rebuilds the events
	// from the methods (Table.ResetAfterLoad) and replaying them recomputes these
	"dispatch.Table.replayed":      "typemap position, replay starts again",
	"dispatch.Table.failed":        "typemap failure, none after a load",
	"dispatch.Table.changes":       "counts the leaf cache's invalidations, which starts empty",
	"dispatch.Method.full":         "recomputed when the method's event is replayed",
	"dispatch.Method.interference": "recomputed when the methods' events are replayed (replay appends to it)",
	"dispatch.Method.latestOnly":   "recomputed when the method's event is replayed",
}

// What the image does with a type reached behind a pointer or an interface.
const (
	kindSaved   = "saved"   // a record of the image
	kindForeign = "foreign" // C handle: saved as C_NULL in a mutable slot, refused elsewhere
	kindStatic  = "static"  // process-wide, written as a Static(i) reference
)

type imageKindDef struct {
	t    reflect.Type
	disp string
}

// cacheBoxType is *core.instanceBox, which core does not export: the type a
// DataType's instance field points to.
func cacheBoxType() reflect.Type {
	f, _ := reflect.TypeFor[core.DataType]().FieldByName("instance")
	return f.Type.Field(0).Type.Elem() // atomic.Pointer[T]'s [0]*T
}

// imageKindDefs lists every Go type the encoder may meet behind a pointer or
// an interface.
func imageKindDefs() []imageKindDef {
	saved := []reflect.Type{
		reflect.TypeFor[*Module](), reflect.TypeFor[*Binding](),
		reflect.TypeFor[*core.TypeName](), reflect.TypeFor[*core.DataType](), reflect.TypeFor[*core.UnionAll](),
		reflect.TypeFor[*core.Union](), reflect.TypeFor[*core.TypeVar](), reflect.TypeFor[*core.Vararg](),
		reflect.TypeFor[*core.Instance](), reflect.TypeFor[*core.Tuple](),
		reflect.TypeFor[*Function](), reflect.TypeFor[*Builtin](), reflect.TypeFor[*Intrinsic](),
		reflect.TypeFor[*dispatch.Table](), reflect.TypeFor[*dispatch.Method](),
		reflect.TypeFor[*lambda](), reflect.TypeFor[*code](),
		reflect.TypeFor[*expr.CodeInfo](), reflect.TypeFor[*expr.DebugInfo](), reflect.TypeFor[*expr.Expr](),
		reflect.TypeFor[expr.QuoteNode](), reflect.TypeFor[expr.LineNumberNode](), reflect.TypeFor[expr.GlobalRef](),
		reflect.TypeFor[expr.Module](), reflect.TypeFor[expr.GotoIfNot](), reflect.TypeFor[expr.ReturnNode](),
		reflect.TypeFor[expr.EnterNode](), reflect.TypeFor[expr.NewvarNode](), reflect.TypeFor[expr.SSAValue](),
		reflect.TypeFor[expr.SlotNumber](), reflect.TypeFor[expr.Argument](), reflect.TypeFor[expr.GotoNode](),
		reflect.TypeFor[expr.LineInfoNode](), reflect.TypeFor[expr.Tagged](), reflect.TypeFor[expr.Opaque](),
		reflect.TypeFor[expr.Vector](), reflect.TypeFor[expr.Symbol](), reflect.TypeFor[expr.Char](),
		reflect.TypeFor[*Struct](), reflect.TypeFor[SVec](),
		reflect.TypeFor[*Memory](), reflect.TypeFor[*MemoryRef](),
		reflect.TypeFor[*Pointer](), reflect.TypeFor[*ptrSlot](), reflect.TypeFor[*outStream](),
		reflect.TypeFor[PrimValue](), reflect.TypeFor[Float16](), reflect.TypeFor[BFloat16](),
		reflect.TypeFor[Int128](), reflect.TypeFor[UInt128](),
		cacheBoxType(), reflect.TypeFor[goMethod](), reflect.TypeFor[*goBody](),
		reflect.TypeFor[*Session](), reflect.TypeFor[*lower.State](),
		reflect.TypeFor[cfunc](),      // a CodeInstance's fptr name (codeinst.go:41)
		reflect.TypeFor[*reflected](), // VM.reflected
		reflect.TypeFor[layoutOf](),   // a key of VM.addrs
	}
	defs := make([]imageKindDef, 0, len(saved)+8)
	for _, t := range saved {
		defs = append(defs, imageKindDef{t, kindSaved})
	}
	defs = append(defs,
		imageKindDef{reflect.TypeOf(core.CoreModule), kindStatic},
		imageKindDef{reflect.TypeOf(core.Bottom), kindStatic},
		imageKindDef{reflect.TypeOf(undef), kindStatic}, // one per process (value.go:19)
		imageKindDef{reflect.TypeFor[*pcre2.Code](), kindForeign},
		imageKindDef{reflect.TypeFor[*pcre2.JITStack](), kindForeign},
		imageKindDef{reflect.TypeFor[*pcre2.MatchContext](), kindForeign},
	)
	return defs
}

// imageFuncs are the Go funcs reachable from the saved roots, by the field
// that holds them, and how a loaded VM gets them back (design section 2).
var imageFuncs = map[string]string{
	"interp.Intrinsic.Fn": "rebound by ID from the loading VM's New()",
	"interp.Builtin.Fn":   "rebound by name from the loading VM's New()",
	"dispatch.Method.Def": "a goMethod boot made: a boot object, comes with New()",
	"interp.goBody.fn":    "made again by newGoBody from the saved name and capture (gobodies.go)",
	"interp.outStream.w":  "host writer, rebound by New()",
}

// imageWeaks are the weak pointers reachable from the saved roots.
var imageWeaks = map[string]string{
	"interp.Struct.exprOwner":        "saved as a reference to the Expr when it is saved, else nil",
	"interp.addrSpace.byTarget[key]": "a region whose target is saved keeps its id and next",
	"interp.addrSpace.byID":          "the same regions, by id",
	"interp.VM.argVectors[key]":      "an entry is saved only when its key is strongly reachable",
}

// imageSpec is the whole classification: the tables, and the types behind
// kinds, from which the schema hash is made.
type imageSpec struct {
	roots map[string]bool
	kinds map[string]string
	skip  map[string]string
	funcs map[string]string
	weaks map[string]string
	defs  []imageKindDef
}

func newImageSpec(defs []imageKindDef) imageSpec {
	kinds := make(map[string]string, len(defs))
	for _, d := range defs {
		kinds[d.t.String()] = d.disp
	}
	return imageSpec{roots: imageRoots, kinds: kinds, skip: imageSkip, funcs: imageFuncs, weaks: imageWeaks, defs: defs}
}

var vmImageSpec = newImageSpec(imageKindDefs())

// schemaHash is a hash of the layout of every kind the spec names and of every
// table: a changed record layout, a field skipped or saved, a new kind, a
// func or weak pointer classified differently, all change it.
func (s *imageSpec) schemaHash() [32]byte {
	h := sha256.New()
	str := func(x string) {
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(len(x)))
		h.Write(n[:])
		h.Write([]byte(x))
	}
	table := func(name string, m map[string]string) {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		str(name)
		for _, k := range keys {
			str(k)
			str(m[k])
		}
	}
	roots := make(map[string]string, len(s.roots))
	for k, v := range s.roots {
		if v {
			roots[k] = "saved"
		} else {
			roots[k] = "host"
		}
	}
	table("roots", roots)
	table("skip", s.skip)
	table("funcs", s.funcs)
	table("weaks", s.weaks)
	defs := append([]imageKindDef(nil), s.defs...)
	sort.Slice(defs, func(i, j int) bool { return defs[i].t.String() < defs[j].t.String() })
	str("kinds")
	for _, d := range defs {
		str(d.t.String())
		str(d.disp)
		t := d.t
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		str(t.Kind().String())
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				str(f.Name)
				str(f.Type.String())
			}
		}
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

var (
	sourcesOnce sync.Once
	sourcesSum  [32]byte
)

// sourcesHash is a hash of the julia sources the VM embeds, interp/boot and
// interp/stdlib: an image built from other sources is another image.
func sourcesHash() [32]byte {
	sourcesOnce.Do(func() {
		h := sha256.New()
		for _, fsys := range []fs.FS{bootFiles, stdlibFiles} {
			_ = hashFS(h, fsys) // an embedded file system cannot fail to read
		}
		copy(sourcesSum[:], h.Sum(nil))
	})
	return sourcesSum
}

// hashFS feeds every file of fsys, its path and its bytes, in the order
// fs.WalkDir visits them (lexical).
func hashFS(h hash.Hash, fsys fs.FS) error {
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		var n [8]byte
		binary.LittleEndian.PutUint64(n[:], uint64(len(p)))
		h.Write(n[:])
		h.Write([]byte(p))
		binary.LittleEndian.PutUint64(n[:], uint64(len(b)))
		h.Write(n[:])
		h.Write(b)
		return nil
	})
}

// readBuildInfo is debug.ReadBuildInfo; tests replace it.
var readBuildInfo = debug.ReadBuildInfo

// modulePathOfJulia is this module's path, which build information names it by.
const modulePathOfJulia = "github.com/go-embedded-julia/julia"

// moduleVersion is the semver of this module as the binary records it: the
// main module's when julia is the program (the CI job that builds the image at
// a tag), the dependency's when it is a library (a consumer that imports
// julia@vX). The two are the one string, "vX": nothing else of the build
// goes in, not the VCS revision, not the modified flag, which a consumer's
// build does not have, and an image keyed by them would load nowhere. A build
// with no tag says "(devel)", a binary without build information "unknown".
func moduleVersion() string {
	bi, ok := readBuildInfo()
	if !ok {
		return "unknown"
	}
	return versionIn(bi)
}

func versionIn(bi *debug.BuildInfo) string {
	v := "unknown"
	if bi.Main.Path == modulePathOfJulia {
		v = bi.Main.Version
	}
	for _, d := range bi.Deps {
		if d.Path == modulePathOfJulia {
			v = d.Version
			if d.Replace != nil {
				v = d.Replace.Version // a replacement by a directory has none
			}
		}
	}
	if v == "" {
		return "(devel)"
	}
	return v
}

// ErrImageVersion is a version an image cannot be published for or fetched
// by: only a tag, vMAJOR.MINOR.PATCH with an optional prerelease, is a
// version of the image; "(devel)", a pseudo-version and a +dirty build are not.
var ErrImageVersion = errors.New("interp: not a release version")

var (
	cleanTag      = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z]+)*)?$`)
	pseudoVersion = regexp.MustCompile(`-([0-9A-Za-z]+\.)*0\.[0-9]{14}-[0-9a-f]{12}$|-[0-9]{14}-[0-9a-f]{12}$`)
)

// CheckImageVersion refuses a version that is not a tag.
func CheckImageVersion(v string) error {
	if !cleanTag.MatchString(v) || pseudoVersion.MatchString(v) {
		return fmt.Errorf("%w: %q (an image is made and fetched for a tag vMAJOR.MINOR.PATCH)", ErrImageVersion, v)
	}
	return nil
}

// ImageVersion is the version this binary's images are made for and fetched by
// (the semver of the julia module in its build information), or an
// ErrImageVersion when that is not a tag: a binary built from a working tree
// makes images for no release.
func ImageVersion() (string, error) {
	v := moduleVersion()
	return v, CheckImageVersion(v)
}

// imageKey binds an image to the interpreter that can load it: the format
// version, the schema, the embedded sources and the module version. A
// mismatch is refused before anything is allocated.
func imageKey(schema [32]byte) [32]byte {
	h := sha256.New()
	var v [4]byte
	binary.LittleEndian.PutUint32(v[:], imageVersion)
	h.Write(v[:])
	h.Write(schema[:])
	src := sourcesHash()
	h.Write(src[:])
	h.Write([]byte(moduleVersion()))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}
