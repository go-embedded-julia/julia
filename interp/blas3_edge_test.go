package interp

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
)

// An illegal argument prints xerbla's line for the routine and writes nothing.
func TestBLASLevel3Xerbla(t *testing.T) {
	vm := New()
	var out bytes.Buffer
	vm.Stdout = &out
	run := func(name string, f func(a, c *Pointer) []any) {
		out.Reset()
		pa, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
		pc, mc := blasArray(vm, []float64{9, 9, 9, 9}, 0)
		if _, err := blasShim(vm, name)(vm, f(pa, pc)); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), " ** On entry to "+strings.ToUpper(name[:5])) {
			t.Errorf("%s: xerbla printed %q", name, out.String())
		}
		if got := blasElems[float64](mc); got[0] != 9 || got[3] != 9 {
			t.Errorf("%s: C written by a refused call: %v", name, got)
		}
	}
	two, one := int64(2), int64(1)
	run("dtrsm_64_", func(a, c *Pointer) []any {
		return level3Args(vm, uint8('L'), uint8('U'), uint8('N'), uint8('N'), two, two, 1.0, a, one, c, two)
	})
	run("dtrmm_64_", func(a, c *Pointer) []any {
		return level3Args(vm, uint8('X'), uint8('U'), uint8('N'), uint8('N'), two, two, 1.0, a, two, c, two)
	})
	run("dsyrk_64_", func(a, c *Pointer) []any {
		return level3Args(vm, uint8('U'), uint8('N'), two, two, 1.0, a, two, 0.0, c, one)
	})
	run("dsymm_64_", func(a, c *Pointer) []any {
		return level3Args(vm, uint8('L'), uint8('U'), two, two, 1.0, a, one, a, two, 0.0, c, two)
	})
	// the single precision routines refuse the same way
	out.Reset()
	f32, _ := blasArray(vm, []float32{1, 2, 3, 4}, 0)
	flag := func(c byte) *Pointer { return blasRef(vm, c) }
	s := blasRef(vm, float32(1))
	n2, n1 := blasRef(vm, two), blasRef(vm, one)
	for name, args := range map[string][]any{
		"strsm_64_": {flag('L'), flag('U'), flag('N'), flag('N'), n2, n2, s, f32, n1, f32, n2},
		"strmm_64_": {flag('L'), flag('U'), flag('N'), flag('N'), n2, n2, s, f32, n1, f32, n2},
		"ssyrk_64_": {flag('U'), flag('N'), n2, n2, s, f32, n1, s, f32, n1},
		"ssymm_64_": {flag('L'), flag('U'), n2, n2, s, f32, n1, f32, n2, s, f32, n2},
	} {
		if _, err := blasShim(vm, name)(vm, args); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if n := strings.Count(out.String(), " ** On entry to "); n != 4 {
		t.Errorf("%d xerbla lines for 4 refused calls: %q", n, out.String())
	}
}

// What a call cannot make sense of is refused as not ported.
func TestBLASLevel3Refusals(t *testing.T) {
	vm := New()
	d, _ := blasArray(vm, []float64{1, 2, 3, 4}, 0)
	str := vm.stringPtrOf("abcd")
	i, x := blasRef(vm, int64(2)), blasRef(vm, 1.0)
	fl := blasRef(vm, uint8('N'))
	bad := blasRef(vm, int64(1)) // a flag that is no byte
	for _, c := range []struct {
		name string
		args []any
	}{
		{"dtrsm_64_", []any{fl}},
		{"dtrmm_64_", []any{bad, fl, fl, fl, i, i, x, d, i, d, i}},
		{"dtrsm_64_", []any{fl, fl, fl, fl, i, i, x, str, i, d, i}}, // A
		{"dtrsm_64_", []any{fl, fl, fl, fl, i, i, x, d, i, str, i}}, // B
		{"dsyrk_64_", []any{fl}},
		{"dsyrk_64_", []any{bad, fl, i, i, x, d, i, x, d, i}},
		{"dsyrk_64_", []any{fl, fl, i, i, x, str, i, x, d, i}}, // A
		{"dsyrk_64_", []any{fl, fl, i, i, x, d, i, x, str, i}}, // C
		{"dsymm_64_", []any{fl}},
		{"dsymm_64_", []any{bad, fl, i, i, x, d, i, d, i, x, d, i}},
		{"dsymm_64_", []any{fl, fl, i, i, x, str, i, d, i, x, d, i}}, // A
		{"dsymm_64_", []any{fl, fl, i, i, x, d, i, str, i, x, d, i}}, // B
		{"dsymm_64_", []any{fl, fl, i, i, x, d, i, d, i, x, str, i}}, // C
	} {
		if _, err := blasShim(vm, c.name)(vm, c.args); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s %v: %v", c.name, c.args, err)
		}
	}
}

// The writing routines log the old values of what they write in place, so
// that Kernel.Reset restores them (#101), as gemm's shim does.
func TestBLASLevel3Undo(t *testing.T) {
	vm := New()
	l := vm.undo
	l.on = true
	mem := func(vals ...float64) (*Memory, *Pointer) {
		elems := make([]any, len(vals))
		for i, v := range vals {
			elems[i] = v
		}
		m := newMemory(memF64Type, elems)
		m.undo = l
		return m, &Pointer{Type: vm.ptrType(core.Float64Type), Target: m}
	}
	_, a := mem(2, 0, 1, 4)
	b, pb := mem(2, 8, 4, 12)
	c, pc := mem(1, 1, 1, 1)
	d, pd := mem(3, 3, 3, 3)
	e, pe := mem(5, 5, 5, 5)
	fl := func(ch byte) *Pointer { return blasRef(vm, ch) }
	two, one, zero := blasRef(vm, int64(2)), blasRef(vm, 1.0), blasRef(vm, 0.0)
	for _, call := range []struct {
		name string
		args []any
	}{
		{"dtrsm_64_", []any{fl('L'), fl('U'), fl('N'), fl('N'), two, two, one, a, two, pb, two}},
		{"dtrmm_64_", []any{fl('L'), fl('U'), fl('N'), fl('N'), two, two, one, a, two, pd, two}},
		{"dsyrk_64_", []any{fl('U'), fl('N'), two, two, one, a, two, zero, pc, two}},
		{"dsymm_64_", []any{fl('L'), fl('U'), two, two, one, a, two, pb, two, zero, pe, two}},
	} {
		if _, err := blasShim(vm, call.name)(vm, call.args); err != nil {
			t.Fatal(call.name, err)
		}
	}
	if b.Get(0) == any(2.0) || c.Get(0) == any(1.0) || d.Get(0) == any(3.0) || e.Get(0) == any(5.0) {
		t.Fatalf("the calls wrote nothing: %v %v %v %v", b.All(), c.All(), d.All(), e.All())
	}
	l.on = false
	for i := len(l.entries) - 1; i >= 0; i-- {
		l.entries[i].undo()
	}
	for _, x := range []struct {
		m    *Memory
		want []float64
	}{{b, []float64{2, 8, 4, 12}}, {c, []float64{1, 1, 1, 1}}, {d, []float64{3, 3, 3, 3}}, {e, []float64{5, 5, 5, 5}}} {
		got := x.m.All()
		for i, w := range x.want {
			if got[i] != any(w) {
				t.Errorf("after the undo: %v, want %v", got, x.want)
				break
			}
		}
	}
}
