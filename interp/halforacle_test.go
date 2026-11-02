package interp

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

// The 16-bit float conversions and the 128-bit integer ones over millions
// of inputs, hashed by chunks as testdata/halforacle.jl hashes julia
// 1.12.6's answers into testdata/half.golden (regenerated with
// JLGO_ORACLE=1): every Float16 shown and widened, Float32 and Float64
// rounded to Float16 and BFloat16 about their ties, the runtime's sitofp,
// uitofp, fptosi and fptoui on 128 bits.

const (
	halfScript = "testdata/halforacle.jl"
	halfGolden = "testdata/half.golden"
)

type hasher struct {
	b    strings.Builder
	name string
	h    uint64
	i, n int
}

func (s *hasher) fold(x uint64) { s.h = (s.h ^ x) * 0x00000100000001b3 }

func (s *hasher) foldString(v string) {
	for i := 0; i < len(v); i++ {
		s.fold(uint64(v[i]))
	}
	s.fold(0xff)
}

// next ends one input: a line per 65536, and one for the last.
func (s *hasher) next() {
	s.i++
	if s.i%65536 == 0 || s.i == s.n {
		fmt.Fprintf(&s.b, "%s %d %s\n", s.name, (s.i-1)/65536, strconv.FormatUint(s.h, 16))
		s.h = 0
	}
}

func (s *hasher) section(name string, n int) { s.name, s.i, s.n, s.h = name, 0, n, 0 }

func lcg(s uint64) uint64 { return s*0x5851f42d4c957f2d + 0x14057b7ef767814f }

func halfLines() string {
	var s hasher
	s.section("show", 1<<16)
	for i := 0; i < 1<<16; i++ {
		s.foldString(Repr(Float16(i)))
		s.next()
	}
	s.section("showelem", 1<<16)
	for i := 0; i < 1<<16; i++ {
		var b strings.Builder
		writeIn(&b, Float16(i), core.Float16Type)
		s.foldString(b.String())
		s.next()
	}
	s.section("widen", 1<<16)
	for i := 0; i < 1<<16; i++ {
		s.fold(uint64(math.Float32bits(halfToFloat(uint16(i)))))
		s.next()
	}
	var f32s []float32
	for k := uint32(0); k <= 0x7ffff; k++ {
		for _, p := range []uint32{0x0000, 0x0001, 0x0fff, 0x1000, 0x1001, 0x1fff} {
			f32s = append(f32s, math.Float32frombits(k<<13|p))
		}
	}
	// fptrunc's path: the value read as a double (runtime_intrinsics.c:1630)
	s.section("f32tohalf", len(f32s))
	for _, x := range f32s {
		s.fold(uint64(doubleToHalf(float64(x))))
		s.next()
	}
	s.section("f32tobf16", len(f32s))
	for _, x := range f32s {
		s.fold(uint64(doubleToBfloat(float64(x))))
		s.next()
	}
	var f64s []float64
	st := uint64(1)
	for i := 1; i <= 1_000_000; i++ {
		st = lcg(st)
		m := st & 0x000fffffffffffff
		st = lcg(st)
		e := uint64(1023-30) + (st>>33)%50
		neg := st >> 63 << 63
		var tie uint64
		if i%2 == 0 {
			tie = m&^(1<<42-1) | 1<<41 | st>>20&1
		} else {
			tie = m&^(1<<29-1) | 1<<28 | st>>20&1
		}
		f64s = append(f64s, math.Float64frombits(neg|e<<52|m), math.Float64frombits(neg|e<<52|tie))
	}
	s.section("f64tohalf", len(f64s))
	for _, x := range f64s {
		s.fold(uint64(doubleToHalf(x)))
		s.next()
	}
	s.section("f64tobf16", len(f64s))
	for _, x := range f64s {
		s.fold(uint64(doubleToBfloat(x)))
		s.next()
	}
	var ints []u128
	st = 7
	for i := 0; i < 400; i++ {
		st = lcg(st)
		hi := st
		st = lcg(st)
		lo := st
		var v u128
		if n := uint(st>>40) % 129; n != 0 {
			v = u128{hi, lo}.lshr(128 - n)
		}
		ints = append(ints, v)
	}
	for _, c := range []struct {
		name   string
		signed bool
		f32    bool
	}{{"sitofp", true, false}, {"uitofp", false, false}, {"uitofp32", false, true}} {
		s.section(c.name, len(ints))
		for _, x := range ints {
			v := roundToDouble(x, 128, c.signed)
			if c.f32 {
				s.fold(uint64(math.Float32bits(float32(v))))
			} else {
				s.fold(math.Float64bits(v))
			}
			s.next()
		}
	}
	var dbls []float64
	st = 11
	for i := 0; i < 400; i++ {
		st = lcg(st)
		dbls = append(dbls, math.Float64frombits(st&0x800fffffffffffff|(uint64(1023-4)+(st>>40)%135)<<52))
	}
	for _, c := range []struct {
		name   string
		signed bool
	}{{"fptosi", true}, {"fptoui", false}} {
		s.section(c.name, len(dbls))
		for _, x := range dbls {
			r := apfloatToInt(x, 128, c.signed)
			s.fold(r.hi)
			s.fold(r.lo)
			s.next()
		}
	}
	return s.b.String()
}

func TestHalfAgainstJulia(t *testing.T) {
	if testing.Short() {
		t.Skip("millions of conversions")
	}
	if os.Getenv("JLGO_ORACLE") != "" {
		args := append(oracle.Command(), "--startup-file=no", halfScript)
		out, err := exec.Command(args[0], args[1:]...).Output()
		if err != nil {
			t.Fatalf("julia: %v", err)
		}
		if err := os.WriteFile(halfGolden, out, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(halfGolden)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(halfLines(), "\n")
	for i, w := range strings.Split(string(want), "\n") {
		if i >= len(got) || got[i] != w {
			t.Errorf("line %d: Go %q, julia %q", i+1, got[min(i, len(got)-1)], w)
		}
	}
	if n := len(strings.Split(string(want), "\n")); len(got) != n {
		t.Errorf("%d lines, julia %d", len(got), n)
	}
}
