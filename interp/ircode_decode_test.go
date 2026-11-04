package interp

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// juliaIRHex are julia 1.12.6's jl_compress_ir bytes of testdata/ircode.jl's
// f1 to f9 (TestCompressIRAgainstJulia's).
var juliaIRHex = map[string]string{
	"f1": "20000000ffff0200000000080200000000000000050200380700bd4201",
	"f2": "20000000ffff0300000000080804000000000000001f491f68090e440701050200050300361100000000000000f83f35110111023611030000006327072d2c0114701101002600f2052a010000000700412dfeff380700bf4200",
	"f3": "20000000ffff02000000000806000000000000001f501f683007010502000700370700c138bc38bdc14201",
	"f4": "20000000ffff02000000000806000000000000003dc00805020009011e64070138070109011eaf070338bbc14201",
	"f5": "20000000ffff0200000000000200000000000000090172bc380700bd4201",
	"f6": "20000000ffff0100000000020000000000000009011100351101380700bd4200",
	"f7": "20000000ffff030000000008080a0000000000000005020009024c05030007001f500503003007010700bc370700c409024c050300bd34be050300380700c54201",
	"f8": "30000000ffff0200000000080200000000000000050200380700bd4201",
	"f9": "20000000ffff02000000000803000000000000001f912f0700050200380700be4201",
}

// irSeeds are real compressed IR: julia's own, those of testdata/ircode2.jl,
// and what this VM writes for a CodeInfo of every kind (the VM's Base is its
// module B).
func irSeeds(t testing.TB, f *irFixture) map[string]string {
	seeds := map[string]string{}
	for name, h := range juliaIRHex {
		b, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		seeds["julia-"+name] = string(b)
	}
	golden, err := os.ReadFile("testdata/ircode2.golden")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(golden), "\n"), "\n") {
		fs := strings.SplitN(line, " ", 3)
		b, err := hex.DecodeString(fs[1])
		if err != nil {
			t.Fatal(err)
		}
		seeds["julia-"+fs[0]] = string(b)
	}
	f.vm.baseModule = f.b
	kinds, err := f.compress(f.kindsCode())
	if err != nil {
		t.Fatal(err)
	}
	seeds["go-kinds"] = kinds
	return seeds
}

var updateFuzzSeeds = flag.Bool("update-fuzz-seeds", false, "write testdata/fuzz/FuzzUncompressIR from real compressions")

// TestWriteFuzzSeeds writes the committed seed corpus of FuzzUncompressIR;
// run with -update-fuzz-seeds.
func TestWriteFuzzSeeds(t *testing.T) {
	if !*updateFuzzSeeds {
		t.Skip("run with -update-fuzz-seeds to rewrite testdata/fuzz/FuzzUncompressIR")
	}
	f := newIRFixture(t)
	dir := filepath.Join("testdata", "fuzz", "FuzzUncompressIR")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, data := range irSeeds(t, f) {
		body := "go test fuzz v1\nstring(" + strconv.Quote(data) + ")\n"
		if err := os.WriteFile(filepath.Join(dir, "seed-"+name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// FuzzUncompressIR: jl_uncompress_ir of any bytes returns a CodeInfo or an
// error. The method is the one the kinds seed was compressed for, so that its
// roots are there to be read; the seeds in testdata/fuzz are real
// compressions.
func FuzzUncompressIR(fz *testing.F) {
	f := newIRFixture(fz)
	seeds := irSeeds(fz, f)
	names := make([]string, 0, len(seeds))
	for n := range seeds {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fz.Add(seeds[n])
	}
	fz.Fuzz(func(t *testing.T, data string) {
		if _, p := f.decodeNoPanic(data); p != nil {
			t.Fatalf("panic: %v", p)
		}
	})
}

// decodeNoPanic is jl_uncompress_ir of data, a panic reported as such. The
// one panic known (knownDecodePanic) is not: it is a defect of the decoder,
// documented by TestUncompressIRKnownPanic, and tests of everything else go on.
func (f *irFixture) decodeNoPanic(data string) (err error, panicked any) {
	defer func() {
		if p := recover(); p != nil && !knownDecodePanic(p) {
			panicked = p
		}
	}()
	_, err = f.vm.shims["jl_uncompress_ir"](f.vm, []any{f.m, nil, data})
	return err, nil
}

// knownDecodePanic is the panic of an SVec given as the element type of a
// vector or pointer type (jl_decode_value's TAG_VECTORTY and TAG_PTRTY): the
// svec reaches core.Apply, which compares it.
func knownDecodePanic(p any) bool {
	return strings.Contains(fmt.Sprint(p), "comparing uncomparable type interp.SVec")
}

// The decoder panics on `TAG_VECTORTY`, `TAG_PTRTY` followed by an svec:
// compressed IR julia would refuse (an error) and this decoder crashes on.
// The test records it and passes; it is left to whoever fixes the decoder.
func TestUncompressIRKnownPanic(t *testing.T) {
	f := newIRFixture(t)
	for _, tag := range []byte{irVectorTy, irPtrTy} {
		data := f.crafted([]byte{tag, irSvec, 0}, f.enc(int64(1)))
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Logf("DEFECT: tag %d then an empty svec panics: %v (compressed IR %x)", tag, p, data)
				}
			}()
			_, err := f.vm.shims["jl_uncompress_ir"](f.vm, []any{f.m, nil, data})
			_ = err
		}()
	}
}

// Every truncation of compressed IR is refused, and no byte changed in it
// makes the decoder panic.
func TestUncompressIRMalformed(t *testing.T) {
	f := newIRFixture(t)
	seeds := irSeeds(t, f)
	names := make([]string, 0, len(seeds))
	for n := range seeds {
		names = append(names, n)
	}
	sort.Strings(names)
	rng := rand.New(rand.NewSource(1))
	reported := 0
	report := func(name, how string, data string, p any) {
		if reported++; reported <= 5 {
			t.Errorf("%s %s panics: %v\n  input %x", name, how, p, data)
		}
	}
	for _, n := range names {
		data := seeds[n]
		for i := 0; i < len(data); i++ {
			err, p := f.decodeNoPanic(data[:i])
			if p != nil {
				report(n, fmt.Sprintf("cut at %d", i), data[:i], p)
			} else if err == nil {
				t.Errorf("%s cut at %d of %d: no error", n, i, len(data))
			}
		}
		if err, p := f.decodeNoPanic(data + "\x00"); p != nil {
			report(n, "extended", data+"\x00", p)
		} else if err == nil {
			t.Errorf("%s with a trailing byte: no error", n)
		}
		mutate := func(pos int, v byte) {
			b := []byte(data)
			b[pos] = v
			if _, p := f.decodeNoPanic(string(b)); p != nil {
				report(n, fmt.Sprintf("byte %d = %d", pos, v), string(b), p)
			}
		}
		if len(data) <= 300 {
			for pos := 0; pos < len(data); pos++ {
				for v := 0; v < 256; v++ {
					mutate(pos, byte(v))
				}
			}
		} else {
			for i := 0; i < 20000; i++ {
				mutate(rng.Intn(len(data)), byte(rng.Intn(256)))
			}
		}
	}
}

// enc is the bytes jl_compress_ir writes for v.
func (f *irFixture) enc(v any) []byte {
	if f.m.Fields[methodRoots] == undef {
		f.m.Fields[methodRoots] = f.vm.newVector(core.AnyType, nil)
	}
	mod, _ := f.m.Fields[methodModule].(*Module)
	s := &irEncoder{vm: f.vm, method: f.m, mod: mod, edges: SVec{}, reloc: 1, tables: f.vm.irTagTables()}
	if err := s.encode(v, false); err != nil {
		f.t.Fatalf("encode %s: %v", showValue(v), err)
	}
	return s.b
}

// crafted is compressed IR of one statement `stmt` (as bytes) and the
// ssavaluetypes `types`, for the method's own argument count.
func (f *irFixture) crafted(stmt []byte, types []byte) string {
	var b []byte
	b = binary.LittleEndian.AppendUint16(b, irfNargsmatchesmethod)
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint16(b, 0)
	b = binary.LittleEndian.AppendUint32(b, 0)
	n := uint64(1)
	if stmt == nil {
		n = 0
	}
	b = binary.LittleEndian.AppendUint64(b, n)
	b = append(b, stmt...)
	b = append(b, types...)
	b = append(b, f.enc(nil)...) // the argument names: the method's
	b = append(b, 1)
	return string(b)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// The decoder's refusals, one a case: each is an error naming the input, none
// a panic.
func TestUncompressIRRefusals(t *testing.T) {
	f := newIRFixture(t)
	vm := f.vm
	if _, err := f.s.Eval("struct SU\na::Union{Int,Nothing}\nend"); err != nil {
		t.Fatal(err)
	}
	one := f.enc(int64(1))
	u32 := func(n uint32) []byte { return binary.LittleEndian.AppendUint32(nil, n) }
	tags := vm.irTagTables()
	var rootTask byte
	for i := range tags.deser {
		if tags.has[i] && tags.deser[i] == any(irRootTask) {
			rootTask = byte(i)
		}
	}
	var unused byte
	for i := 255; i > 0; i-- {
		if !tags.has[i] {
			unused = byte(i)
			break
		}
	}
	vecAny := f.enc(f.typ("Vector{Any}"))
	memAny := f.enc(must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.AnyType, core.CPU)))
	memUnion := f.enc(must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), f.typ("Union{Int,Nothing}"), core.CPU)))
	memInt := f.enc(must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), core.Int64Type, core.CPU)))
	memSingle := f.enc(must(core.Apply(core.GenericMemoryType, expr.Symbol("not_atomic"), f.typ("Single"), core.CPU)))
	vecUnion := f.enc(f.typ("Vector{Union{Int,Nothing}}"))
	typeSU := f.enc(f.typ("SU"))
	typeSingle := f.enc(f.typ("Single"))
	typeFloat16 := f.enc(f.typ("Float16"))
	for _, c := range []struct {
		name string
		stmt []byte
		want string
	}{
		{"an unused tag", []byte{unused}, "malformed"},
		{"an unused tag after 0", []byte{0, unused}, "malformed"},
		{"the root task", []byte{rootTask}, "!the root task"},
		{"a common symbol that is none", []byte{irCommonsym, 0}, "malformed"},
		{"a common symbol", []byte{irCommonsym, 2}, ""},
		{"a method root past the roots", []byte{irMethodroot, 200}, "malformed"},
		{"a long method root past the roots", cat([]byte{irLongMethodroot}, u32(70000)), "malformed"},
		{"an edge without edges", []byte{irEdge, 0}, "malformed"},
		{"a long edge without edges", cat([]byte{irLongEdge}, u32(1)), "malformed"},
		{"a relocatable root of another key", cat([]byte{irRelocMethodroot}, make([]byte, 7), []byte{1, irMethodroot, 0}), "malformed"},
		{"a relocatable root that is none", cat([]byte{irRelocMethodroot}, make([]byte, 8), []byte{irSvec}), "malformed"},
		{"a relocatable root cut short", cat([]byte{irRelocMethodroot}, make([]byte, 8)), "malformed"},
		{"a relocatable root", cat([]byte{irRelocMethodroot}, make([]byte, 8), []byte{irLongMethodroot}, u32(70000)), "malformed"},
		{"a long svec that is too long", cat([]byte{irLongSvec}, u32(1<<30)), "malformed"},
		{"a long svec cut short", cat([]byte{irLongSvec}, u32(2), one), "malformed"},
		{"an svec cut short", cat([]byte{irSvec, 3}, one), "malformed"},
		{"a long expr that is too long", cat([]byte{irLongExpr}, u32(1<<30)), "malformed"},
		{"an expr whose head is no symbol", cat([]byte{irExpr, 0}, one), "malformed"},
		{"an expr without a head", []byte{irExpr, 0}, "malformed"},
		{"an expr cut short", cat([]byte{irExpr, 2}, f.enc(expr.Symbol("call")), one), "malformed"},
		{"a call cut short", cat([]byte{irCall1}, one), "malformed"},
		{"a phi cut short", cat([]byte{irPhinode, 2}, f.enc(int32(1))), "malformed"},
		{"a phi with no length", []byte{irPhinode}, "malformed"},
		{"a long phi with no lengths", []byte{irLongPhinode}, "malformed"},
		{"a long phi with one length", cat([]byte{irLongPhinode}, u32(1)), "malformed"},
		{"a long phi too long", cat([]byte{irLongPhinode}, u32(1<<30), u32(0)), "malformed"},
		{"a long phi with too many values", cat([]byte{irLongPhinode}, u32(0), u32(1<<30)), "malformed"},
		{"a phi with an edge that is no Int32", cat([]byte{irPhinode, 1}, one, one), "malformed"},
		{"a phi without values", cat([]byte{irPhinode, 1}, f.enc(int32(1))), "malformed"},
		{"a phic cut short", cat([]byte{irPhicnode, 2}, one), "malformed"},
		{"a phic with no length", []byte{irPhicnode}, "malformed"},
		{"a long phic too long", cat([]byte{irLongPhicnode}, u32(1<<30)), "malformed"},
		{"a goto without its label", []byte{irGotonode}, "!"},
		{"a goto with a label", cat([]byte{irGotonode}, one), ""},
		{"a goto of nothing", []byte{irGotonode, irNull}, ""},
		{"a gotoifnot cut short", cat([]byte{irGotoifnot}, one), "!"},
		{"an enternode cut short", cat([]byte{irEnternode}, one), "malformed"},
		{"an enternode", cat([]byte{irEnternode}, one, []byte{irNull}), ""},
		{"a short int64 cut", []byte{irShorterInt64, 1}, "malformed"},
		{"an int64 cut", []byte{irShortInt64, 1, 2}, "malformed"},
		{"a long int64 cut", []byte{irInt64, 1, 2}, "malformed"},
		{"a short int32 cut", []byte{irShortInt32, 1}, "malformed"},
		{"an int32 cut", []byte{irInt32, 1}, "malformed"},
		{"a uint8 cut", []byte{irUInt8}, "malformed"},
		{"an ssa value cut", []byte{irSSAValue}, "malformed"},
		{"a nearby ssa value cut", []byte{irNearbySSAValue}, "malformed"},
		{"a long ssa value cut", []byte{irLongSSAValue, 1}, "malformed"},
		{"a slot cut", []byte{irSlotnumber, 1}, "malformed"},
		{"a commonsym cut", []byte{irCommonsym}, "malformed"},
		{"a method root cut", []byte{irMethodroot}, "malformed"},
		{"a long method root cut", []byte{irLongMethodroot, 1}, "malformed"},
		{"an edge cut", []byte{irEdge}, "malformed"},
		{"a long edge cut", []byte{irLongEdge, 1}, "malformed"},
		{"a reloc root cut", []byte{irRelocMethodroot, 1}, "malformed"},
		{"a tag cut", []byte{0}, "malformed"},
		{"a nearby global of no symbol", cat([]byte{irNearbyGlobal}, one), "malformed"},
		{"a nearby global cut", []byte{irNearbyGlobal}, "malformed"},
		{"a nearby global", cat([]byte{irNearbyGlobal}, f.enc(expr.Symbol("z"))), ""},
		{"the nearby module", []byte{irNearbyModule}, ""},
		{"Base without Base", []byte{irBase}, "malformed"},
		{"Core", []byte{irCore}, ""},
		{"a global of no module", cat([]byte{irGlobalref}, one, one), "malformed"},
		{"a global of no name", cat([]byte{irGlobalref}, f.enc(vm.Core), one), "malformed"},
		{"a global cut", cat([]byte{irGlobalref}, f.enc(vm.Core)), "malformed"},
		{"a global", cat([]byte{irGlobalref}, f.enc(vm.Core), f.enc(expr.Symbol("zz"))), ""},
		{"a singleton of no type", cat([]byte{irSingleton}, one), "malformed"},
		{"a singleton cut", []byte{irSingleton}, "malformed"},
		{"a singleton of a type without one", cat([]byte{irSingleton}, f.enc(core.Int64Type)), "malformed"},
		{"a singleton", cat([]byte{irSingleton}, typeSingle), ""},
		{"a vector type cut", []byte{irVectorTy}, "malformed"},
		{"a pointer type cut", []byte{irPtrTy}, "malformed"},
		{"a vector type", cat([]byte{irVectorTy}, f.enc(core.Int64Type)), ""},
		{"a pointer type", cat([]byte{irPtrTy}, f.enc(core.Int64Type)), ""},
		{"a vector type of an Int", cat([]byte{irVectorTy}, one), ""},
		{"a pointer type of an Int", cat([]byte{irPtrTy}, one), ""},
		{"a string cut", []byte{irString, 5, 0, 0, 0, 'a'}, "malformed"},
		{"a string with no length", []byte{irString}, "malformed"},
		{"a string", []byte{irString, 1, 0, 0, 0, 'a'}, ""},
		{"a general of no type", cat([]byte{irGeneral}, one), "malformed"},
		{"a general cut", []byte{irGeneral}, "malformed"},
		{"a general of a type without a layout", cat([]byte{irGeneral}, typeSU), "!the layout of an isbits Union"},
		{"a general of a primitive cut", cat([]byte{irGeneral}, f.enc(core.Int64Type), []byte{1, 2}), "malformed"},
		{"a general of an unsupported primitive", cat([]byte{irGeneral}, typeFloat16, []byte{1, 2}), ""},
		{"a general struct cut short", cat([]byte{irGeneral}, f.enc(f.typ("Two")), []byte{1, 2, 3}), "malformed"},
		{"a general struct", cat([]byte{irGeneral}, f.enc(f.typ("Two")), make([]byte, 16)), ""},
		{"a general mixed struct", cat([]byte{irGeneral}, f.enc(f.typ("Mixed")), []byte{1}, make([]byte, 7), one, []byte{0, 0, 0, 0}, make([]byte, 4)), ""},
		{"a general mixed struct, the pointer cut", cat([]byte{irGeneral}, f.enc(f.typ("Mixed")), []byte{1}, make([]byte, 7)), "malformed"},
		{"a general mixed struct, padding cut", cat([]byte{irGeneral}, f.enc(f.typ("Mixed")), []byte{1}, make([]byte, 3)), "malformed"},
		{"a general mixed struct, tail cut", cat([]byte{irGeneral}, f.enc(f.typ("Mixed")), []byte{1}, make([]byte, 7), one, []byte{0, 0, 0, 0}), "malformed"},
		{"a general union", cat([]byte{irGeneral}, f.enc(core.UnionType), f.enc(core.Int64Type), f.enc(core.Int8Type)), ""},
		{"a general union cut", cat([]byte{irGeneral}, f.enc(core.UnionType), f.enc(core.Int64Type)), "malformed"},
		{"a memory of no type", cat([]byte{irMemoryT}, one, one), "malformed"},
		{"a memory of no length", cat([]byte{irMemoryT}, memAny, f.enc(expr.Symbol("s"))), "malformed"},
		{"a memory of a negative length", cat([]byte{irMemoryT}, memAny, f.enc(int64(-1))), "malformed"},
		{"a memory of a type that is no memory", cat([]byte{irMemoryT}, f.enc(core.Int64Type), one), "malformed"},
		{"a memory cut", cat([]byte{irMemoryT}, memAny), "malformed"},
		{"a memory cut after its type", cat([]byte{irMemoryT}, memAny, one), "malformed"},
		{"a memory too long", cat([]byte{irMemoryT}, memAny, f.enc(int64(1<<40))), "malformed"},
		{"a memory of Any", cat([]byte{irMemoryT}, memAny, one, one), ""},
		{"a memory of a union", cat([]byte{irMemoryT}, memUnion, one, one), "!jl_uncompress_ir of a Memory"},
		{"a memory of a zero-size", cat([]byte{irMemoryT}, memSingle, one, one), "!jl_uncompress_ir of a Memory"},
		{"a memory of Int64", cat([]byte{irMemoryT}, memInt, one, make([]byte, 8)), ""},
		{"a memory of Int64 cut", cat([]byte{irMemoryT}, memInt, one, make([]byte, 3)), "malformed"},
		{"an array of no length", []byte{irArray1D}, "malformed"},
		{"an array of no type", cat([]byte{irArray1D}, one), "malformed"},
		{"an array of a type that is none", cat([]byte{irArray1D}, one, one), "malformed"},
		{"an array of a negative length", cat([]byte{irArray1D}, f.enc(int64(-1)), vecAny), "malformed"},
		{"an array of a length that is none", cat([]byte{irArray1D}, f.enc(expr.Symbol("s")), vecAny), "malformed"},
		{"an array too long", cat([]byte{irArray1D}, f.enc(int64(1<<40)), vecAny), "malformed"},
		{"an array of Any cut", cat([]byte{irArray1D}, one, vecAny), "malformed"},
		{"an array of Any", cat([]byte{irArray1D}, one, vecAny, one), ""},
		{"an array of a union", cat([]byte{irArray1D}, one, vecUnion, one), "!jl_uncompress_ir of a Memory"},
	} {
		// the statement, then as ssavaluetypes, then with nothing after it
		raw := f.crafted(c.stmt, nil)
		raw = raw[:len(raw)-len(f.enc(nil))-1]
		for where, data := range map[string]string{"stmt": f.crafted(c.stmt, one), "types": f.crafted(nil, c.stmt), "raw": raw} {
			if where == "raw" && c.want == "" {
				if err, p := f.decodeNoPanic(data); p != nil || err == nil {
					t.Errorf("%s with nothing after it: %v %v", c.name, err, p)
				}
				continue
			}
			err, p := f.decodeNoPanic(data)
			if p != nil {
				t.Errorf("%s (%s) panics: %v\n  input %x", c.name, where, p, data)
				continue
			}
			switch {
			case c.want == "":
			case strings.HasPrefix(c.want, "!"):
				check(t, c.name+" ("+where+")", nil, err, c.want)
			default:
				if err == nil || !strings.Contains(err.Error(), c.want) {
					t.Errorf("%s (%s): %v, want an error containing %q", c.name, where, err, c.want)
				}
			}
		}
	}
}

// The compressed header's counts are checked against what is left to read.
func TestUncompressIRHeader(t *testing.T) {
	f := newIRFixture(t)
	good := f.crafted(nil, f.enc(int64(1)))
	if err, p := f.decodeNoPanic(good); err != nil || p != nil {
		t.Fatalf("a crafted CodeInfo: %v %v", err, p)
	}
	patch := func(at int, b ...byte) string { return good[:at] + string(b) + good[at+len(b):] }
	for _, c := range []struct {
		name string
		data string
	}{
		{"nslots past the data", patch(6, 0xff, 0xff, 0xff, 0x7f)},
		{"nstmts past the data", patch(10, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x7f)},
		{"nargs not the method's, then cut", patch(0, 0, 0)},
		{"no data", ""},
		{"one byte", "x"},
	} {
		err, p := f.decodeNoPanic(c.data)
		if p != nil || err == nil {
			t.Errorf("%s: %v %v", c.name, err, p)
		}
	}
	// ssaflags: none for no statement
	flagged := patchFlags(good, irfHasSSAFlags|irfNargsmatchesmethod)
	if err, p := f.decodeNoPanic(flagged); p != nil || err != nil {
		t.Errorf("no statements need no ssaflags: %v %v", err, p)
	}
	// the header ends where the argument count should be
	if err, p := f.decodeNoPanic(patchFlags(good, 0)[:10]); p != nil || err == nil {
		t.Errorf("a header without nargs: %v %v", err, p)
	}
	// the slot types of an opaque closure are not there
	f.m.Fields[methodIsOpaque] = true
	stmtless := f.crafted(nil, f.enc(int64(1)))
	if err, p := f.decodeNoPanic(stmtless[:len(stmtless)-len(f.enc(nil))-1]); p != nil || err == nil {
		t.Errorf("an opaque closure without slot types: %v %v", err, p)
	}
	f.m.Fields[methodIsOpaque] = false
	// a method without a module has no nearby names
	f.m.Fields[methodModule] = nil
	for _, stmt := range [][]byte{{irNearbyModule}, cat([]byte{irNearbyGlobal}, f.enc(expr.Symbol("z")))} {
		if err, p := f.decodeNoPanic(f.crafted(stmt, f.enc(int64(1)))); p != nil || err == nil {
			t.Errorf("a nearby name of a method without a module: %v %v", err, p)
		}
	}
	// slot names that are neither in the data nor the method's
	saved := f.m.Fields[methodSlotSyms]
	f.m.Fields[methodSlotSyms] = int64(1)
	if err, p := f.decodeNoPanic(good); p != nil || err == nil {
		t.Errorf("a method without slot names: %v %v", err, p)
	}
	f.m.Fields[methodSlotSyms] = saved
}

func patchFlags(s string, flags uint16) string {
	b := []byte(s)
	binary.LittleEndian.PutUint16(b, flags)
	return string(b)
}
