package interp

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/core"
	"github.com/go-embedded-julia/julia/expr"
)

// mpfrFixture calls the libmpfr shims as Base's mpfr.jl does: a
// Ref{BigFloat} is a pointer to the BigFloat's Memory{UInt64}.
type mpfrFixture struct {
	*gmpFixture
	rndT *core.DataType
}

func newMPFRFixture(t *testing.T) *mpfrFixture {
	f := &mpfrFixture{gmpFixture: newGMPFixture(t)}
	if _, err := f.s.Eval("primitive type MPFRRoundingMode 32 end\nmutable struct GPS; p::Ptr{UInt8}; end\nmutable struct GRefI; x::Int64; end"); err != nil {
		t.Fatal(err)
	}
	f.rndT = must(f.s.Eval("MPFRRoundingMode")).(*core.DataType)
	// what Base's MPFR.__init__ does
	f.must("mpfr_set_emin", int64(mpfrEminMin))
	f.must("mpfr_set_emax", int64(mpfrEmaxMax))
	return f
}

func (f *mpfrFixture) call(name string, args ...any) (any, error) {
	f.t.Helper()
	sh := f.vm.libShims[mpfrLib][expr.Symbol(name)]
	if sh == nil {
		f.t.Fatalf("no shim %s", name)
	}
	return sh(f.vm, args)
}

func (f *mpfrFixture) must(name string, args ...any) any {
	f.t.Helper()
	v, err := f.call(name, args...)
	if err != nil {
		f.t.Fatalf("%s: %v", name, err)
	}
	return v
}

func (f *mpfrFixture) rnd(r int) PrimValue {
	return PrimValue{Type: f.rndT, Bits: from64(uint64(r))}
}

// newBF is BigFloat(precision=p): NaN, sign +1, as mpfr.jl makes one.
func (f *mpfrFixture) newBF(prec int64) *Pointer {
	m := f.vm.newLimbs(mpfrLimbWord + int((prec-1)/64+1))
	m.Elems[0], m.Elems[1] = uint64(prec), uint64(1)
	m.Elems[2] = expWord(math.MinInt64 + 2)
	return &Pointer{Type: f.u64p, Target: m}
}

func expWord(e int64) uint64 { return uint64(e) }

func bfMem(p *Pointer) *Memory { return p.Target.(*Memory) }

// bf decodes B:<prec>:<kind>:<sign>:<exp>:<limbs>.
func (f *mpfrFixture) bf(tok string) *Pointer {
	parts := strings.Split(tok, ":")
	if len(parts) != 6 || parts[0] != "B" {
		f.t.Fatalf("not a BigFloat: %q", tok)
	}
	prec := must(strconv.ParseInt(parts[1], 10, 64))
	p := f.newBF(prec)
	m := bfMem(p)
	sign := must(strconv.ParseInt(parts[3], 10, 32))
	m.Elems[1] = uint64(uint32(int32(sign)))
	switch parts[2] {
	case "n":
		m.Elems[2] = expWord(math.MinInt64 + 2)
	case "i":
		m.Elems[2] = expWord(math.MinInt64 + 3)
	case "z":
		m.Elems[2] = expWord(math.MinInt64 + 1)
	default:
		m.Elems[2] = uint64(must(strconv.ParseInt(parts[4], 10, 64)))
		for i, l := range strings.Split(parts[5], ",") {
			m.Elems[mpfrLimbWord+i] = must(strconv.ParseUint(l, 16, 64))
		}
	}
	return p
}

// enc is the fields julia's code reads, as the golden prints them.
func (f *mpfrFixture) enc(p *Pointer) string {
	m := bfMem(p)
	prec := int64(m.Elems[0].(uint64))
	sign := int32(m.Elems[1].(uint64))
	exp := int64(m.Elems[2].(uint64))
	kind, limbs := "r", ""
	switch exp {
	case math.MinInt64 + 1:
		kind, exp = "z", 0
	case math.MinInt64 + 2:
		kind, exp = "n", 0
	case math.MinInt64 + 3:
		kind, exp = "i", 0
	default:
		var l []string
		for i := 0; i < int((prec-1)/64+1); i++ {
			l = append(l, strconv.FormatUint(m.Elems[mpfrLimbWord+i].(uint64), 16))
		}
		limbs = strings.Join(l, ",")
	}
	return fmt.Sprintf("B:%d:%s:%d:%d:%s", prec, kind, sign, exp, limbs)
}

func tsign(v any) string { return fmt.Sprintf("I:%d", min(max(int64(v.(int32)), -1), 1)) }

// flags are the six flags as digits.
func (f *mpfrFixture) flags() string {
	var b strings.Builder
	for _, n := range []string{"underflow_p", "overflow_p", "divby0_p", "nanflag_p", "inexflag_p", "erangeflag_p"} {
		b.WriteString(fmt.Sprint(f.must("mpfr_" + n).(int32)))
	}
	return b.String()
}

// arg decodes a golden input.
func (f *mpfrFixture) arg(tok string) any {
	switch tok[:2] {
	case "B:":
		return f.bf(tok)
	case "r:":
		return f.rnd(int(must(strconv.ParseInt(tok[2:], 10, 32))))
	}
	return f.token(tok)
}

func intTok(tok string) int64 { return must(strconv.ParseInt(tok[2:], 10, 64)) }

var mpfrRefusedOps = map[string]bool{
	"sech": true, "csch": true, "coth": true, "sec": true, "csc": true, "sinpi": true, "cospi": true, "tanpi": true,
	"sinu": true, "cosu": true, "tanu": true, "asinu": true, "acosu": true, "atanu": true, "atan2u": true,
	"const_catalan": true,
}

// run is one golden line: the outputs of the shims the operation names.
func (f *mpfrFixture) run(op string, in []string) ([]string, error) {
	prec := func(tok string) int64 { return intTok(tok) }
	switch op {
	case "set", "neg", "sqrt", "exp", "exp2", "exp10", "expm1", "cosh", "sinh", "tanh", "cbrt", "log", "log2", "log10", "log1p",
		"sin", "cos", "tan", "acos", "asin", "atan", "acosh", "asinh", "atanh",
		"sech", "csch", "coth", "sec", "csc", "sinpi", "cospi", "tanpi":
		z := f.newBF(prec(in[1]))
		t, err := f.call("mpfr_"+op, z, f.bf(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "sinu", "cosu", "tanu", "asinu", "acosu", "atanu":
		z := f.newBF(prec(in[1]))
		t, err := f.call("mpfr_"+op, z, f.bf(in[0]), uint64(360), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "trunc", "roundeven", "ceil", "floor", "round":
		z := f.newBF(prec(in[1]))
		t, err := f.call("mpfr_"+op, z, f.bf(in[0]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "add", "sub", "mul", "div", "pow", "hypot", "atan2", "copysign", "fmod", "remainder":
		z := f.newBF(prec(in[2]))
		t, err := f.call("mpfr_"+op, z, f.bf(in[0]), f.bf(in[1]), f.arg(in[3]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "atan2u":
		z := f.newBF(prec(in[2]))
		t, err := f.call("mpfr_atan2u", z, f.bf(in[0]), f.bf(in[1]), uint64(360), f.arg(in[3]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "add_alias":
		z := f.bf(in[0])
		t, err := f.call("mpfr_add", z, z, f.bf(in[1]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "fma":
		z := f.newBF(prec(in[3]))
		t, err := f.call("mpfr_fma", z, f.bf(in[0]), f.bf(in[1]), f.bf(in[2]), f.arg(in[4]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "sin_cos":
		s, c := f.newBF(prec(in[1])), f.newBF(prec(in[1]))
		t, err := f.call("mpfr_sin_cos", s, c, f.bf(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(s), f.enc(c), fmt.Sprintf("I:%d", t)}, nil
	case "modf":
		zi, zf := f.newBF(prec(in[1])), f.newBF(prec(in[1]))
		t, err := f.call("mpfr_modf", zi, zf, f.bf(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(zi), f.enc(zf), fmt.Sprintf("I:%d", t)}, nil
	case "frexp":
		z := f.newBF(prec(in[1]))
		p, st := f.newStruct("GRefI")
		t, err := f.call("mpfr_frexp", p, z, f.bf(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), fmt.Sprintf("I:%d", st.Fields[0]), tsign(t)}, nil
	case "fac_ui":
		z := f.newBF(prec(in[1]))
		t, err := f.call("mpfr_fac_ui", z, f.token(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "const_log2", "const_pi", "const_euler", "const_catalan":
		z := f.newBF(prec(in[0]))
		t, err := f.call("mpfr_"+op, z, f.arg(in[1]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "equal_p", "lessequal_p", "greaterequal_p", "less_p", "greater_p":
		v, err := f.call("mpfr_"+op, f.bf(in[0]), f.bf(in[1]))
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("I:%d", v)}, nil
	case "integer_p", "get_prec", "get_exp":
		v, err := f.call("mpfr_"+op, f.bf(in[0]))
		if err != nil {
			return nil, err
		}
		return []string{fmt.Sprintf("I:%d", v)}, nil
	case "nextabove", "nextbelow":
		z := f.bf(in[0])
		if _, err := f.call("mpfr_"+op, z); err != nil {
			return nil, err
		}
		return []string{f.enc(z)}, nil
	case "cmp_z", "cmp_si", "cmp_ui", "cmp_d":
		v, err := f.call("mpfr_"+op, f.bf(in[0]), f.arg(in[1]))
		if err != nil {
			return nil, err
		}
		return []string{tsign(v)}, nil
	case "set_si", "set_ui", "set_d", "set_z":
		z := f.newBF(prec(in[1]))
		t, err := f.call("mpfr_"+op, z, f.arg(in[0]), f.arg(in[2]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	case "get_sj", "get_uj":
		v, err := f.call("__gmpfr_mpfr_"+op, f.bf(in[0]), f.arg(in[1]))
		if err != nil {
			return nil, err
		}
		if u, ok := v.(uint64); ok {
			return []string{fmt.Sprintf("U:%d", u)}, nil
		}
		return []string{fmt.Sprintf("I:%d", v)}, nil
	case "get_z":
		p, st := f.fresh()
		t, err := f.call("mpfr_get_z", p, f.bf(in[0]), f.arg(in[1]))
		if err != nil {
			return nil, err
		}
		return []string{f.raw(st.Fields[:3]), tsign(t)}, nil
	case "emin_emax", "emin_emax2":
		var outs []string
		names := []string{"emin", "emax"}
		if op == "emin_emax" {
			names = []string{"emin", "emax", "emin_min", "emax_max", "emin_max", "emax_min"}
		}
		for _, n := range names {
			outs = append(outs, fmt.Sprint("I:", f.must("mpfr_get_"+n)))
		}
		return outs, nil
	case "version":
		v := f.must("mpfr_get_version")
		s, _ := f.vm.cstringArg(v)
		pv := f.must("mpfr_get_patches")
		ps, _ := f.vm.cstringArg(pv)
		return []string{"S:" + s, fmt.Sprint("I:", len(strings.Split(ps, " ")))}, nil
	case "custom_get_size":
		return []string{fmt.Sprint("I:", f.must("mpfr_custom_get_size", f.arg(in[0])))}, nil
	case "set_emin", "set_emax":
		get := "mpfr_get_" + op[4:]
		old := f.must(get)
		r := f.must("mpfr_"+op, f.arg(in[0]))
		e := f.must(get)
		f.must("mpfr_"+op, old)
		return []string{fmt.Sprint("I:", r), fmt.Sprint("I:", e)}, nil
	case "set_str":
		z := f.newBF(prec(in[2]))
		r, err := f.call("mpfr_set_str", z, f.arg(in[0]), int32(intTok(in[1])), f.arg(in[3]))
		if err != nil {
			return nil, err
		}
		if r.(int32) != 0 {
			return []string{fmt.Sprint("I:", r)}, nil
		}
		return []string{"I:0", f.enc(z)}, nil
	case "asprintf":
		pc, st := f.newStruct("GPS")
		n, err := f.call("mpfr_asprintf", pc, f.arg(in[1]), f.bf(in[0]))
		if err != nil {
			return nil, err
		}
		s, _ := f.vm.cstringArg(st.Fields[0])
		f.must("mpfr_free_str", st.Fields[0])
		return []string{fmt.Sprint("I:", n), "S:" + s}, nil
	}
	// the operations with a scalar: x op c, c op x, and the powers of 2
	if i := strings.IndexByte(op, '_'); i > 0 {
		switch op[:i] {
		case "ui", "si", "d", "z":
			z := f.newBF(prec(in[2]))
			t, err := f.call("mpfr_"+op, z, f.arg(in[0]), f.bf(in[1]), f.arg(in[3]))
			if err != nil {
				return nil, err
			}
			return []string{f.enc(z), tsign(t)}, nil
		}
		z := f.newBF(prec(in[2]))
		t, err := f.call("mpfr_"+op, z, f.bf(in[0]), f.arg(in[1]), f.arg(in[3]))
		if err != nil {
			return nil, err
		}
		return []string{f.enc(z), tsign(t)}, nil
	}
	f.t.Fatalf("unknown golden operation %q", op)
	return nil, nil
}

// inputsOf is how many fields of a golden line are inputs, the others being
// results.
func inputsOf(op string) int {
	switch op {
	case "emin_emax", "emin_emax2", "version":
		return 0
	case "sech", "csch", "coth", "sec", "csc", "sinpi", "cospi", "tanpi", "sinu", "cosu", "tanu", "asinu", "acosu", "atanu",
		"sin_cos", "modf", "frexp", "fac_ui", "set_si", "set_ui", "set_d", "set_z", "set", "neg", "sqrt", "exp", "exp2", "exp10",
		"expm1", "cosh", "sinh", "tanh", "cbrt", "log", "log2", "log10", "log1p", "sin", "cos", "tan", "acos", "asin", "atan",
		"acosh", "asinh", "atanh", "add_alias":
		return 3
	case "trunc", "roundeven", "ceil", "floor", "round", "const_log2", "const_pi", "const_euler", "const_catalan", "equal_p", "lessequal_p", "greaterequal_p", "less_p",
		"greater_p", "cmp_z", "cmp_si", "cmp_ui", "cmp_d", "get_sj", "get_uj", "get_z", "asprintf":
		return 2
	case "integer_p", "get_prec", "get_exp", "nextabove", "nextbelow", "custom_get_size", "set_emin", "set_emax":
		return 1
	case "fma":
		return 5
	}
	return 4 // the binary operations, those with a scalar, the powers of two, set_str
}

// beyondInt32 is whether a B field has an exponent math/big cannot hold.
func beyondInt32(tok string) bool {
	parts := strings.Split(tok, ":")
	if len(parts) != 6 || parts[0] != "B" || parts[2] != "r" {
		return false
	}
	e, _ := strconv.ParseInt(parts[4], 10, 64)
	return e > math.MaxInt32 || e < math.MinInt32
}

// outOfScope is why MPFR's answer is one math/big cannot hold or the shim
// refuses on purpose, "" when it is neither: a function not ported ("op"),
// an argument or result whose exponent is beyond int32 ("int32"), or an
// infinity or zero from regular numbers ("special").
func outOfScope(op string, ins, outs []string) string {
	if mpfrRefusedOps[op] {
		return "op"
	}
	regularIn, specialOut := true, false
	for _, tok := range append(append([]string{}, ins...), outs...) {
		if beyondInt32(tok) {
			return "int32"
		}
	}
	for _, tok := range ins {
		if strings.HasPrefix(tok, "B:") && strings.Split(tok, ":")[2] != "r" {
			regularIn = false
		}
	}
	for _, tok := range outs {
		if k := strings.Split(tok, ":"); strings.HasPrefix(tok, "B:") && (k[2] == "i" || k[2] == "z") {
			specialOut = true
		}
	}
	if regularIn && specialOut {
		return "special"
	}
	return ""
}

func TestMPFRGolden(t *testing.T) {
	f := newMPFRFixture(t)
	file, err := os.Open("testdata/mpfr.golden")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	lines, refused, fails := 0, 0, map[string]int{}
	refusedBy := map[string]int{}
	refusedWhy, refusedNarrow, refusedSpecial := map[string]int{}, map[string]int{}, map[string]int{}
	shown := map[string]int{}
	for sc.Scan() {
		fields := strings.Split(sc.Text(), "\t")
		op, rest := fields[0], fields[1:]
		if op == "string" {
			continue // Base's own printing, over mpfr_asprintf
		}
		lines++
		flags := rest[len(rest)-1]
		rest = rest[:len(rest)-1]
		name := op
		f.must("mpfr_clear_flags")
		if strings.HasPrefix(op, "R") {
			// R<emin>,<emax>:<op>
			rng, o, _ := strings.Cut(op, ":")
			lo, hi, _ := strings.Cut(rng[1:], ",")
			f.must("mpfr_set_emin", must(strconv.ParseInt(lo, 10, 64)))
			f.must("mpfr_set_emax", must(strconv.ParseInt(hi, 10, 64)))
			name = o
			if name == "emin_emax" {
				name = "emin_emax2"
			}
		}
		nin := inputsOf(name)
		got, err := f.run(name, rest[:nin])
		haveFlags := f.flags()
		want := rest[nin:]
		if strings.HasPrefix(op, "R") {
			f.must("mpfr_set_emin", int64(mpfrEminMin))
			f.must("mpfr_set_emax", int64(mpfrEmaxMax))
		}
		if err != nil {
			if why := outOfScope(name, rest[:nin], want); why != "" && (errors.Is(err, ErrUnsupported) || errors.Is(err, ErrMemoryLimit)) {
				refused++
				refusedBy[name]++
				refusedWhy[why]++
				if why == "special" {
					refusedSpecial[name]++
					if strings.HasPrefix(op, "R") {
						// an overflow or underflow in a narrowed exponent
						// range, within what math/big holds
						refusedNarrow[name]++
					}
				}
				continue
			}
			fails[op]++
			if shown[op] < 3 {
				shown[op]++
				t.Errorf("%s: %v\n  line: %s", op, err, sc.Text())
			}
			continue
		}
		if strings.Join(got, "\t") != strings.Join(want, "\t") || flags != haveFlags {
			fails[op]++
			if shown[op] < 3 {
				shown[op]++
				t.Errorf("%s:\n  line: %s\n  got:  %s\t%s", op, sc.Text(), strings.Join(got, "\t"), haveFlags)
			}
		}
	}
	t.Logf("%d golden lines, %d refused as out of scope, %d failing operations; refused by operation: %v; by reason: %v; an infinity or zero from regular numbers: %v",
		lines, refused, len(fails), refusedBy, refusedWhy, refusedSpecial)
	// an overflow or underflow in a narrowed exponent range is within what
	// math/big holds: refusing one would hide a case the shims miss
	if len(refusedNarrow) > 0 {
		t.Errorf("refused in a narrowed exponent range: %v", refusedNarrow)
	}
}
