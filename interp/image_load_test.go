package interp

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/core/dispatch"
)

func loadBytes(t testing.TB, b []byte) *VM {
	t.Helper()
	vm, err := LoadImage(bytes.NewReader(b), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

// imageProgram is a VM with code of its own, saved after it has run it.
func imageProgram(t testing.TB) (*VM, *Session) {
	vm := bootedVM(t)
	s := vm.NewSession()
	for _, src := range []string{
		"mutable struct LM; x; end",
		"struct LI; a; b; end",
		"inc(x::Int) = Core.Intrinsics.add_int(x, 1)",
		"which_g(x::Int) = 1",
		"which_g(x::Number) = 2",
		"which_g(x::Int) = 3", // replaces the first
		"which_g(x::AbstractFloat) = 4",
		"const lm1 = LM(10)",
		"lt = (LI(1, :a), lm1, \"text\", 2.5)",
		"mem = Core.GenericMemory{:not_atomic, Int, Core.CPU}(undef, 3)",
		"ex = :(a + b)",
		"ex_args = ex.args", // the Expr's args vector: a weak entry, and a weak owner
	} {
		if _, err := s.Eval(src); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
	// a region of the address table whose memory the image holds
	mem, err := s.Eval("mem")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := vm.addressOf(&Pointer{Type: vm.ptrType(core.UInt8Type), Target: mem}); err != nil {
		t.Fatal(err)
	}
	return vm, s
}

// The same programs give the same answers, or the same errors, on the VM and on
// the VM loaded from its image.
func probe(t *testing.T, a, b *Session) {
	t.Helper()
	for _, src := range []string{
		"inc(41)", "which_g(1)", "which_g(1.5)", "which_g(2.5f0)", "which_g(\"s\")", "lm1.x", "lt", "typeof(lt)",
		"mem", "ex", "ex.args", "LM(5).x", "LI(1, 2).b", "(inc(1), which_g(3))", "x = 2; inc(x)",
		"lm1.x = 11; lm1.x", "lm1.x", "objectid(lm1) == objectid(lm1)", "Core.Intrinsics.mul_int(6, 7)",
		"inc", "LM", "LI", "typeof(inc)", "which_g(1) + 1",
	} {
		va, ea := a.Eval(src)
		vb, eb := b.Eval(src)
		sa, sb := fmt.Sprint(showValue(va), ea), fmt.Sprint(showValue(vb), eb)
		// a mutable struct prints with its address-free show; the answers agree
		if sa != sb {
			t.Errorf("%s:\n  original: %s\n  loaded:   %s", src, sa, sb)
		}
	}
}

func TestImageRoundTrip(t *testing.T) {
	vm, s := imageProgram(t)
	b1 := saveBytes(t, vm)
	l := loadBytes(t, b1)
	// save, load, save: the same bytes (before anything runs on the loaded VM)
	b2 := saveBytes(t, l)
	if !bytes.Equal(b1, b2) {
		p1, p2 := parseImage(t, b1), parseImage(t, b2)
		t.Fatalf("images differ: %d vs %d bytes; objects %d vs %d, strings %d vs %d, types %d vs %d",
			len(b1), len(b2), len(p1.kinds), len(p2.kinds), len(p1.strs), len(p2.strs), len(p1.types), len(p2.types))
	}
	ls := l.sessionFor(l.Main.own("L").Value.(*Module))
	if ls.VM != l {
		t.Fatalf("the session of Main.L is another VM's")
	}
	probe(t, s, ls)
	// and the loaded VM goes on defining
	for _, src := range []string{"inc2(x::Int) = inc(inc(x))", "inc2(1)"} {
		if _, err := ls.Eval(src); err != nil {
			t.Errorf("%s on the loaded VM: %v", src, err)
		}
	}
}

// Dispatch on the loaded VM answers as the saved one did over a fixed set of
// signatures and worlds, with a method replaced and a method deleted in later
// worlds.
func TestImageDispatchAfterLoad(t *testing.T) {
	vm, s := imageProgram(t)
	tab := func(vm *VM, s *Session) *dispatch.Table {
		return s.Mod.own("which_g").Value.(*Function).Table
	}
	// a method deleted in a later world
	ta := tab(vm, s)
	var float *dispatch.Method
	for _, m := range ta.Methods(ta.World()) {
		if strings.Contains(showValue(m.Sig), "AbstractFloat") {
			float = m
		}
	}
	if float == nil || !ta.Disable(float, vm.newWorld()) {
		t.Fatal("no method to delete")
	}
	vm.newWorld()
	// query the original first, so that its typemap has been built
	sigs := []any{}
	for _, ty := range []any{core.Int64Type, core.Float64Type, core.Float32Type, core.StringType, core.Int8Type} {
		sigs = append(sigs, must(core.Apply(core.AnyTupleType, ty)))
	}
	answers := func(tb *dispatch.Table) string {
		var b strings.Builder
		for w := uint64(0); w <= tb.World()+2; w++ {
			for _, sig := range sigs {
				m, _, err := tb.Lookup(sig, w)
				if err != nil {
					b.WriteString("-")
				} else {
					fmt.Fprintf(&b, "%d.%d", m.Index, m.PrimaryWorld)
				}
				b.WriteString(",")
			}
			b.WriteString("|")
		}
		return b.String()
	}
	want := answers(ta)
	if !strings.Contains(want, "-") {
		t.Fatalf("every call has a method: %s", want)
	}
	l := loadBytes(t, saveBytes(t, vm))
	ls := l.sessionFor(l.Main.own("L").Value.(*Module))
	if got := answers(tab(l, ls)); got != want {
		t.Errorf("dispatch on the loaded VM:\n got %s\nwant %s", got, want)
	}
}

// Refusals of a damaged or foreign image are typed errors.
func TestImageLoadRefusals(t *testing.T) {
	vm := bootedVM(t)
	good := saveBytes(t, vm)
	header := len(imageMagic) + 4 + 32
	flip := func(b []byte, i int) []byte {
		c := append([]byte(nil), b...)
		c[i] ^= 0x55
		return c
	}
	cases := []struct {
		name string
		img  []byte
		want error
	}{
		{"empty", nil, ErrImageTruncated},
		{"a header only", good[:header], ErrImageTruncated},
		{"a cut image", good[:len(good)/2], ErrImageTruncated},
		{"a cut trailer", good[:len(good)-10], ErrImageTruncated},
		{"no magic", flip(good, 0), ErrImageCorrupt},
		{"another version", flip(good, len(imageMagic)), ErrImageKey},
		{"another key", flip(good, len(imageMagic)+4+3), ErrImageKey},
		{"a damaged trailer", flip(good, len(good)-1), ErrImageChecksum},
	}
	// a damaged byte inside the image: found by the checksum, unless it lands
	// on a length the loader reads first (then the image is truncated or
	// corrupt, never accepted). Which offset is a length depends on the image's
	// layout, which every field added to the VM moves, so the offsets run on
	// until one is plain data.
	sawChecksum := false
	for k := 0; k < 64 && !sawChecksum; k++ {
		_, err := LoadImage(bytes.NewReader(flip(good, header+len(good)/3+k)), LoadOptions{})
		switch {
		case errors.Is(err, ErrImageChecksum):
			sawChecksum = true
		case errors.Is(err, ErrImageTruncated), errors.Is(err, ErrImageCorrupt):
		default:
			t.Errorf("a damaged byte at +%d: %v", k, err)
		}
	}
	if !sawChecksum {
		t.Error("no damaged byte in 64 reached the checksum")
	}
	for _, c := range cases {
		_, err := LoadImage(bytes.NewReader(c.img), LoadOptions{})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	// limits: bytes and objects
	if _, err := LoadImage(bytes.NewReader(good), LoadOptions{MaxBytes: 1000}); !errors.Is(err, ErrImageLimit) {
		t.Errorf("a byte limit: %v", err)
	}
	if _, err := LoadImage(bytes.NewReader(good), LoadOptions{MaxObjects: 10}); !errors.Is(err, ErrImageLimit) {
		t.Errorf("an object limit: %v", err)
	}
	// a reader that fails, in the header and after it
	if _, err := LoadImage(io.MultiReader(bytes.NewReader(good[:100]), errReader{}), LoadOptions{}); err == nil || errors.Is(err, ErrImageTruncated) {
		t.Errorf("a reader failing after the header: %v", err)
	}
	if _, err := LoadImage(errReader{}, LoadOptions{}); err == nil || errors.Is(err, ErrImageTruncated) {
		t.Errorf("a reader failing in the header: %v", err)
	}
}

// TestImageLoadFile loads the image JLGO_IMAGE_FILE names (TestImageLoadBase
// writes one and runs this in a fresh process), saves the loaded VM and
// requires the same bytes: save, load, save across processes. It is also the
// quick way to work on a difference the Base build would show after minutes.
func TestImageLoadFile(t *testing.T) {
	path := os.Getenv("JLGO_IMAGE_FILE")
	if path == "" {
		t.Skip("set JLGO_IMAGE_FILE to an image of the Base VM")
	}
	b1, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	l := loadBytes(t, b1)
	t.Logf("%d bytes loaded in %v", len(b1), time.Since(t0))
	var b2 bytes.Buffer
	if err := l.SaveImage(&b2, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, b2.Bytes()) {
		var more strings.Builder
		if from := os.Getenv("JLGO_IMAGE_DUMP"); from != "" {
			start, _ := strconv.Atoi(from)
			e := newImgEnc(l, &vmImageSpec, SaveOptions{})
			e.cur = -1
			e.discover()
			for i := start; i < min(start+60, len(e.objs)); i++ {
				o := e.objs[i]
				desc := o.t.String()
				if dt, ok := o.v.Interface().(*core.DataType); ok {
					desc += " " + showValue(dt)
				}
				e.cur = int32(i)
				fmt.Fprintf(&more, "%d %s tier %d | %s\n", i, desc, o.tier, e.where())
			}
		}
		t.Fatalf("save, load, save: %d and %d bytes that differ\n%s%s", len(b1), b2.Len(), describeImageDiff(t, b1, b2.Bytes()), more.String())
	}
}

// TestImageLoadBase takes the VM at the cut where julia writes sys.so, saves
// it, loads the image, requires save, load, save to give the same bytes in a
// fresh process; then the built VM and the loaded one go on from the cut as
// julia does from its image (the module initializers) and run the same Base
// programs. It builds Base: JLGO_BASE=1. The load time is printed; it is a
// measurement only on an idle machine (1-minute load at most 8).
func TestImageLoadBase(t *testing.T) {
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
	var b1 bytes.Buffer
	if err := vm.SaveImage(&b1, SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	l, err := LoadImage(bytes.NewReader(b1.Bytes()), LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d bytes loaded in %v (load-dependent: not a measurement unless the machine is idle)", b1.Len(), time.Since(t0))
	// save, load, save in a process that holds neither VM: the same bytes. (In
	// this process the loaded VM shares the types of the built one, and the
	// objects they are made of, so that its re-save need not be the same.)
	dir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "go-embedded-julia-image-test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "base.img")
	if err := os.WriteFile(file, b1.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestImageLoadFile$", "-test.v")
	cmd.Env = append(os.Environ(), "JLGO_IMAGE_FILE="+file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("save, load, save in a fresh process: %v\n%s", err, out)
	} else {
		t.Logf("in a fresh process: %s", out)
	}
	for _, v := range []*VM{vm, l} {
		v.GeneratingOutput = false
		if err := v.RunModuleInitializers(); err != nil {
			t.Fatal(err)
		}
	}
	a, b := vm.sessionFor(vm.Main), l.sessionFor(l.Main)
	for _, src := range []string{"sum(1:10)", "join([1, 2, 3], \",\")", "string(2^10)", "sort([3, 1, 2])",
		"Dict(:a => 1, :b => 2)[:b]", "length(methods(+)) > 10", "first(\"héllo\")", "map(x -> x * 2, 1:4)"} {
		va, ea := a.Eval(src)
		vb, eb := b.Eval(src)
		if sa, sb := fmt.Sprint(showValue(va), ea), fmt.Sprint(showValue(vb), eb); sa != sb {
			t.Errorf("%s:\n  built:  %s\n  loaded: %s", src, sa, sb)
		} else {
			t.Logf("%s => %s", src, sa)
		}
	}
}

// describeImageDiff says where two images that should be one differ: the first
// section, and the first object, that does.
func describeImageDiff(t testing.TB, a, b []byte) string {
	t.Helper()
	pa, pb := parseImage(t, a), parseImage(t, b)
	var out strings.Builder
	fmt.Fprintf(&out, "types %d/%d, objects %d/%d, strings %d/%d, roots %d/%d bytes, hints %d/%d, interned %d/%d bytes\n",
		len(pa.types), len(pb.types), len(pa.kinds), len(pb.kinds), len(pa.strs), len(pb.strs),
		len(pa.roots), len(pb.roots), len(pa.hints), len(pb.hints), len(pa.interned), len(pb.interned))
	for i := 0; i < min(len(pa.types), len(pb.types)); i++ {
		if pa.types[i] != pb.types[i] {
			fmt.Fprintf(&out, "first type that differs: %d %q vs %q\n", i, pa.types[i], pb.types[i])
			break
		}
	}
	for i := 0; i < min(len(pa.strs), len(pb.strs)); i++ {
		if pa.strs[i] != pb.strs[i] {
			fmt.Fprintf(&out, "first string that differs: %d %q vs %q\n", i, pa.strs[i], pb.strs[i])
			break
		}
	}
	shown := 0
	for i := 0; i < min(len(pa.kinds), len(pb.kinds)) && shown < 5; i++ {
		if pa.kinds[i] != pb.kinds[i] || !bytes.Equal(pa.recs[i], pb.recs[i]) {
			fmt.Fprintf(&out, "object %d differs: %+v (%d bytes) vs %+v (%d bytes)\n", i, pa.kinds[i], len(pa.recs[i]), pb.kinds[i], len(pb.recs[i]))
			shown++
		}
	}
	if !bytes.Equal(pa.roots, pb.roots) {
		out.WriteString("the roots differ\n")
	}
	if !bytes.Equal(pa.hints, pb.hints) {
		out.WriteString("the hints differ\n")
	}
	if !bytes.Equal(pa.interned, pb.interned) {
		out.WriteString("the interned types differ\n")
	}
	return out.String()
}

// describeGraphDiff walks two VMs in the order the encoder does and says where
// the first object that is not the other's counterpart is: its type, what it
// prints as, and the path the walk reached it by.
func describeGraphDiff(a, b *VM) string {
	walk := func(vm *VM) *imgEnc {
		e := newImgEnc(vm, &vmImageSpec, SaveOptions{})
		e.cur = -1
		e.discover()
		return e
	}
	ea, eb := walk(a), walk(b)
	desc := func(e *imgEnc, i int) string {
		o := e.objs[i]
		s := o.t.String()
		if dt, ok := o.v.Interface().(*core.DataType); ok {
			s += " " + showValue(dt)
		}
		e.cur = int32(i)
		return fmt.Sprintf("%d %s tier %d | %s", i, s, o.tier, e.where())
	}
	var out strings.Builder
	fmt.Fprintf(&out, "graphs of %d and %d objects\n", len(ea.objs), len(eb.objs))
	shown := 0
	for i := 0; i < min(len(ea.objs), len(eb.objs)) && shown < 6; i++ {
		if da, db := desc(ea, i), desc(eb, i); da != db {
			fmt.Fprintf(&out, "first difference:\n  original: %s\n  loaded:   %s\n", da, db)
			for j := 1; j <= 4 && i+j < min(len(ea.objs), len(eb.objs)); j++ {
				fmt.Fprintf(&out, "  original: %s\n  loaded:   %s\n", desc(ea, i+j), desc(eb, i+j))
			}
			break
		}
	}
	return out.String()
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }
