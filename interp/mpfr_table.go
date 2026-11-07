package interp

import (
	"fmt"
	"math"
	"math/big"
)

// the table of libmpfr's functions (mpfr.jl's ccalls)

// mpfrOpFn is an operation of BigFloat arguments with one result: the
// arguments, the BigFloats among them read (those after the result), the
// rounding mode and the precision of the result.
type mpfrOpFn func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error)

// scalarKinds are the C types of the operations with a scalar operand: the
// kind letter of the argument, and its value as a number.
var scalarKinds = map[string]struct {
	kind byte
	num  func(v any) *mpfrNum
}{
	"ui": {'u', func(v any) *mpfrNum { return numOfUint(v.(uint64)) }},
	"si": {'i', func(v any) *mpfrNum { return numOfInt64(v.(int64)) }},
	"d":  {'d', func(v any) *mpfrNum { return numOfFloat(v.(float64)) }},
	"z":  {'z', func(v any) *mpfrNum { return numOfInt(v.(*big.Int)) }},
}

// refusedFns are the functions of mpfr.jl that neither math/big nor
// bigfloat computes: MPFR's value is not approximated, the call is refused.
var refusedFns = []struct{ name, sig, why string }{
	{"sech", "ffr", "the reciprocal of cosh"}, {"csch", "ffr", "the reciprocal of sinh"}, {"coth", "ffr", "the reciprocal of tanh"},
	{"sec", "ffr", "the reciprocal of cos"}, {"csc", "ffr", "the reciprocal of sin"},
	{"sinpi", "ffr", "sin(pi*x)"}, {"cospi", "ffr", "cos(pi*x)"}, {"tanpi", "ffr", "tan(pi*x)"},
	{"sinu", "ffur", "sin(2*pi*x/u)"}, {"cosu", "ffur", "cos(2*pi*x/u)"}, {"tanu", "ffur", "tan(2*pi*x/u)"},
	{"asinu", "ffur", "asin(x)*u/(2*pi)"}, {"acosu", "ffur", "acos(x)*u/(2*pi)"}, {"atanu", "ffur", "atan(x)*u/(2*pi)"},
	{"atan2u", "fffur", "atan2(y,x)*u/(2*pi)"},
}

// scalarNaN gives the NaN result of an operation with an integer operand c
// the sign MPFR's implementation leaves: it sets none, except that mpfr_mul_si
// and its relatives with a negative long call the unsigned version and change
// the sign of the result (flips says the operation is one). A double's, as
// the division of a BigFloat by one, is the generic operation's.
func scalarNaN(r *mpfrNum, tn string, c *mpfrNum, flips bool) {
	if r.kind != kNaN || tn == "d" {
		return
	}
	r.nsign = 0
	if flips && c.neg && (tn == "si" || tn == "z" && c.exp <= 63) {
		r.nsign = nsignFlip
	}
}

// mpfrArith are the four operations, as functions of two numbers.
var mpfrArith = map[string]func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int){
	"add": (*VM).mpfrAdd, "mul": (*VM).mpfrMul, "div": (*VM).mpfrDiv,
	"sub": func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		return vm.mpfrAdd(x, y.negated(), prec, rnd)
	},
}

// mpfrRints are the functions that round to an integer, and their direction.
var mpfrRints = map[string]int{"trunc": rndZ, "ceil": rndU, "floor": rndD, "round": rndNA, "roundeven": rndN}

func (vm *VM) mpfrFunctions() map[string]mpfrFn {
	fns := map[string]mpfrFn{}
	// op registers a function of BigFloat and other arguments, the first the
	// result, the last the rounding mode when the signature says so.
	op := func(name, sig string, f mpfrOpFn) {
		fns["mpfr_"+name] = mpfrFn{sig, 1, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			z := a[0].(*mpfrRef)
			rnd := rndN
			if sig[len(sig)-1] == 'r' {
				rnd = a[len(a)-1].(int)
			}
			r, t, err := f(vm, a, x, rnd, z.prec)
			if err != nil {
				return nil, err
			}
			return vm.mpfrRet(z, r, t)
		}}
	}
	plain := func(f func(vm *VM, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int)) mpfrOpFn {
		return func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			r, t := f(vm, x[1], prec, rnd)
			return r, t, nil
		}
	}
	two := func(f func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int)) mpfrOpFn {
		return func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			r, t := f(vm, x[1], x[2], prec, rnd)
			return r, t, nil
		}
	}
	twoErr := func(f func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error)) mpfrOpFn {
		return func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			return f(vm, x[1], x[2], prec, rnd)
		}
	}

	op("set", "ffr", plain(func(vm *VM, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		return vm.mpfrSetSign(x, x.neg, prec, rnd)
	}))
	op("neg", "ffr", plain(func(vm *VM, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		return vm.mpfrSetSign(x, !x.neg, prec, rnd)
	}))
	op("sqrt", "ffr", plain((*VM).mpfrSqrt))
	op("copysign", "fffr", two(func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		return vm.mpfrSetSign(x, y.neg, prec, rnd)
	}))
	for name, f := range mpfrArith {
		op(name, "fffr", two(f))
		for tn, sc := range scalarKinds {
			// x op c, and for - and / c op x
			op(name+"_"+tn, "ff"+string(sc.kind)+"r", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
				c := sc.num(a[2])
				r, t := f(vm, x[1], c, prec, rnd)
				scalarNaN(r, tn, c, name == "mul" || name == "div")
				return r, t, nil
			})
			if name != "sub" && name != "div" || tn == "z" && name == "div" {
				continue
			}
			op(tn+"_"+name, "f"+string(sc.kind)+"fr", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
				c := sc.num(a[1])
				if name == "sub" && c.kind == kZero && tn != "d" {
					// mpfr_ui_sub (and the others) of 0 is the opposite
					r, t := vm.mpfrSetSign(x[2], !x[2].neg, prec, rnd)
					return r, t, nil
				}
				r, t := f(vm, c, x[2], prec, rnd)
				scalarNaN(r, tn, c, true)
				return r, t, nil
			})
		}
	}
	op("fma", "ffffr", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
		r, t := vm.mpfrFMA(x[1], x[2], x[3], prec, rnd)
		return r, t, nil
	})
	op("mul_2si", "ffir", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
		r, t := vm.mpfrMul2(x[1], a[2].(int64), prec, rnd)
		return r, t, nil
	})
	op("mul_2ui", "ffur", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
		r, t := vm.mpfrMul2(x[1], int64(min(a[2].(uint64), math.MaxInt64)), prec, rnd)
		return r, t, nil
	})
	op("hypot", "fffr", twoErr((*VM).mpfrHypot))
	op("atan2", "fffr", twoErr((*VM).mpfrAtan2))
	op("fmod", "fffr", twoErr(func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
		return vm.mpfrFmod(false, x, y, prec, rnd)
	}))
	op("remainder", "fffr", twoErr(func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
		return vm.mpfrFmod(true, x, y, prec, rnd)
	}))
	op("pow", "fffr", twoErr(func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error) {
		return vm.mpfrPow("mpfr_pow", x, y, prec, rnd)
	}))
	for _, tn := range []string{"ui", "si", "z"} {
		sc := scalarKinds[tn]
		op("pow_"+tn, "ff"+string(sc.kind)+"r", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			return vm.mpfrPow("mpfr_pow_"+tn, x[1], sc.num(a[2]), prec, rnd)
		})
	}
	op("fac_ui", "fur", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
		return vm.mpfrFac(a[1].(uint64), prec, rnd)
	})
	op("const_log2", "fr", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
		return vm.mpfrLog2Const(prec, rnd)
	})
	for n, mode := range mpfrRints {
		op(n, "ff", func(vm *VM, a []any, x []*mpfrNum, _ int, prec int64) (*mpfrNum, int, error) {
			return vm.mpfrRint("mpfr_"+n, x[1], prec, mode)
		})
	}
	// the elementary functions
	for name, u := range bfUnaries {
		op(name, "ffr", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			return vm.elemValue("mpfr_"+name, u, prec, x[1], rnd)
		})
	}
	for _, r := range refusedFns {
		fns["mpfr_"+r.name] = mpfrFn{r.sig, 1, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			return nil, fmt.Errorf("%w: mpfr_%s, %s, is neither in math/big nor in bigfloat", ErrUnsupported, r.name, r.why)
		}}
	}
	fns["mpfr_sin_cos"] = mpfrFn{"fffr", 2, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		s, c, rnd := a[0].(*mpfrRef), a[1].(*mpfrRef), a[3].(int)
		sr, st, err := vm.elemValue("mpfr_sin", bfUnaries["sin"], s.prec, x[2], rnd)
		if err != nil {
			return nil, err
		}
		cr, ct, err := vm.elemValue("mpfr_cos", bfUnaries["cos"], c.prec, x[2], rnd)
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(s, sr)
		vm.mpfrWrite(c, cr)
		return inexPos(st) + 4*inexPos(ct), nil
	}}
	fns["mpfr_modf"] = mpfrFn{"fffr", 2, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		ipr, fpr := a[0].(*mpfrRef), a[1].(*mpfrRef)
		ip, fp, code, err := vm.mpfrModf(x[2], ipr.prec, fpr.prec, a[3].(int))
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(ipr, ip)
		vm.mpfrWrite(fpr, fp)
		return code, nil
	}}
	fns["mpfr_frexp"] = mpfrFn{"pffr", 2, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		z := a[1].(*mpfrRef)
		y, e, t := vm.mpfrFrexp(x[2], z.prec, a[3].(int))
		if err := vm.storeAs("mpfr_frexp", a[0], e); err != nil {
			return nil, err
		}
		return vm.mpfrRet(z, y, t)
	}}

	// conversions to a BigFloat
	for _, tn := range []string{"si", "ui", "d", "z"} {
		sc := scalarKinds[tn]
		op("set_"+tn, "f"+string(sc.kind)+"r", func(vm *VM, a []any, x []*mpfrNum, rnd int, prec int64) (*mpfrNum, int, error) {
			r, t := vm.mpfrSet(sc.num(a[1]), prec, rnd)
			return r, t, nil
		})
	}
	fns["mpfr_set_str"] = mpfrFn{"fscr", 1, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		z, base := a[0].(*mpfrRef), int(a[2].(int32))
		// MPFR asserts a base of 0 or 2 to 62
		if base < 0 || base == 1 || base > 62 {
			return nil, fmt.Errorf("%w: mpfr_set_str in base %d (MPFR aborts)", ErrUnsupported, base)
		}
		p, rest, ok := mpfrParse(a[1].(string), base)
		if !ok {
			return int32(-1), nil
		}
		// the number it starts with is what the string is read as, and what
		// sets the flags, whatever follows it
		r, _, err := vm.mpfrOfParsed(p, z.prec, a[3].(int))
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(z, r)
		if rest != "" {
			return int32(-1), nil
		}
		return int32(0), nil
	}}

	// conversions from a BigFloat
	getInt := func(name string, lo, hi *big.Int, mk func(*big.Int) any) {
		fns[name] = mpfrFn{"fr", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			n, err := vm.mpfrGetInt(name, x[0], a[1].(int), lo, hi)
			if err != nil {
				return nil, err
			}
			return mk(n), nil
		}}
	}
	getInt("__gmpfr_mpfr_get_sj", int64Min, int64Max, func(n *big.Int) any { return n.Int64() })
	getInt("__gmpfr_mpfr_get_uj", new(big.Int), uint64Max, func(n *big.Int) any { return n.Uint64() })
	fns["mpfr_get_z"] = mpfrFn{"wfr", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		n, t, err := vm.mpfrGetZ(x[1], a[2].(int))
		if err != nil {
			return nil, err
		}
		return int32(t), vm.putMpz(a[0].(*Struct).Fields[:3], n)
	}}

	// comparisons
	preds := map[string]func(c int) bool{
		"equal_p": func(c int) bool { return c == 0 }, "less_p": func(c int) bool { return c < 0 }, "greater_p": func(c int) bool { return c > 0 },
		"lessequal_p": func(c int) bool { return c <= 0 }, "greaterequal_p": func(c int) bool { return c >= 0 },
	}
	for name, ok := range preds {
		fns["mpfr_"+name] = mpfrFn{"ff", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			return mpfrPred(x[0], x[1], ok), nil
		}}
	}
	for tn, sc := range scalarKinds {
		fns["mpfr_cmp_"+tn] = mpfrFn{"f" + string(sc.kind), 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			return vm.mpfrCmp(x[0], sc.num(a[1])), nil
		}}
	}

	// queries and in-place functions
	fns["mpfr_get_prec"] = mpfrFn{"f", 1, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return a[0].(*mpfrRef).prec, nil }}
	fns["mpfr_get_exp"] = mpfrFn{"f", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return x[0].exp, nil }}
	fns["mpfr_integer_p"] = mpfrFn{"f", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return b2i(isInteger(x[0])), nil }}
	for name, up := range map[string]bool{"nextabove": true, "nextbelow": false} {
		fns["mpfr_"+name] = mpfrFn{"f", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			z := a[0].(*mpfrRef)
			if up {
				vm.mpfrWrite(z, vm.mpfrNextAbove(x[0], z.prec))
			} else {
				vm.mpfrWrite(z, vm.mpfrNextAbove(x[0].negated(), z.prec).negated())
			}
			return int32(0), nil
		}}
	}

	// the library, its exponent range, its flags
	fns["mpfr_get_version"] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return vm.stringPtrOf(mpfrVersion), nil }}
	fns["mpfr_get_patches"] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return vm.stringPtrOf(""), nil }}
	fns["mpfr_custom_get_size"] = mpfrFn{"i", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		// MPFR_PREC2LIMBS(prec) limbs, in C's arithmetic
		return uint64((a[0].(int64)-1)/64+1) * 8, nil
	}}
	fns["mpfr_get_emin"] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return vm.mpfr.emin, nil }}
	fns["mpfr_get_emax"] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return vm.mpfr.emax, nil }}
	for name, v := range map[string]int64{"emin_min": mpfrEminMin, "emin_max": mpfrEmaxMax, "emax_min": mpfrEminMin, "emax_max": mpfrEmaxMax} {
		fns["mpfr_get_"+name] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return v, nil }}
	}
	// set_emin and set_emax: 0, or 1 for a value outside the limits
	for name, set := range map[string]func(vm *VM, n int64){
		"emin": func(vm *VM, n int64) { vm.mpfr.emin = n }, "emax": func(vm *VM, n int64) { vm.mpfr.emax = n }} {
		fns["mpfr_set_"+name] = mpfrFn{"i", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
			if n := a[0].(int64); n >= mpfrEminMin && n <= mpfrEmaxMax {
				set(vm, n)
				return int32(0), nil
			}
			return int32(1), nil
		}}
	}
	fns["mpfr_clear_flags"] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { vm.mpfr.flags = 0; return nil, nil }}
	for name, f := range map[string]uint8{"underflow_p": flagUnderflow, "overflow_p": flagOverflow, "divby0_p": flagDivBy0,
		"nanflag_p": flagNaN, "inexflag_p": flagInexact, "erangeflag_p": flagErange} {
		fns["mpfr_"+name] = mpfrFn{"", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return b2i(vm.mpfr.flags&f != 0), nil }}
	}

	// printing: mpfr_asprintf(&str, "%Re", x), whose string mpfr_free_str frees
	fns["mpfr_asprintf"] = mpfrFn{"psf", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) {
		prec, ok := printfFormat(a[1].(string))
		if !ok {
			return nil, fmt.Errorf("%w: mpfr_asprintf with the format %q (mpfr.jl passes %%Re and %%.<k>Re)", ErrUnsupported, a[1])
		}
		text, err := vm.formatE(x[2], a[2].(*mpfrRef).prec, prec)
		if err != nil {
			return nil, err
		}
		s, err := vm.allocString([]any{int64(len(text) + 1)})
		if err != nil {
			return nil, err
		}
		b, _ := vm.writableBytes(s.(string))
		copy(b, text)
		if err := vm.storeAs("mpfr_asprintf", a[0], vm.stringPtrOf(s.(string))); err != nil {
			return nil, err
		}
		return int32(len(text)), nil
	}}
	fns["mpfr_free_str"] = mpfrFn{"p", 0, func(vm *VM, a []any, x []*mpfrNum) (any, error) { return nil, nil }}
	return fns
}
