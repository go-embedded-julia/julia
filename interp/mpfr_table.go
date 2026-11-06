package interp

import (
	"fmt"
	"math/big"
)

// the table of libmpfr's functions (mpfr.jl's ccalls)

// mpfrOpFn is an operation of BigFloat arguments with one result.
type mpfrOpFn func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error)

// scalarKinds are the C types of the operations with a scalar operand: the
// kind letter of the argument, and its value as a number.
var scalarKinds = map[string]struct {
	kind byte
	num  func(vm *VM, v any) (*mpfrNum, error)
}{
	"ui": {'u', func(vm *VM, v any) (*mpfrNum, error) { return numOfUint(v.(uint64)), nil }},
	"si": {'i', func(vm *VM, v any) (*mpfrNum, error) { return numOfInt64(v.(int64)), nil }},
	"d":  {'d', func(vm *VM, v any) (*mpfrNum, error) { return numOfFloat(v.(float64)), nil }},
	"z": {'z', func(vm *VM, v any) (*mpfrNum, error) {
		n, err := vm.getMpz(v.(*Struct).Fields[:3])
		return numOfInt(n), err
	}},
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

func (vm *VM) mpfrFunctions() map[string]mpfrFn {
	fns := map[string]mpfrFn{}
	// op registers a function of BigFloat and other arguments, the first
	// the result, the last the rounding mode when ternary says so.
	op := func(name, sig string, f mpfrOpFn) {
		fns["mpfr_"+name] = mpfrFn{sig, func(vm *VM, a []any) (any, error) {
			z := a[0].(*mpfrRef)
			rnd := rndN
			if sig[len(sig)-1] == 'r' {
				var err error
				if rnd, err = vm.roundingArg("mpfr_"+name, a[len(a)-1]); err != nil {
					return nil, err
				}
			}
			x, t, err := f(vm, a, rnd, z.prec)
			if err != nil {
				return nil, err
			}
			return vm.mpfrRet(z, x, t)
		}}
	}
	// the operands are read before the result is written, which may be one
	read := func(vm *VM, v any) (*mpfrNum, error) { return vm.mpfrRead(v.(*mpfrRef)) }
	plain := func(f func(vm *VM, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int)) mpfrOpFn {
		return func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			r, t := f(vm, x, prec, rnd)
			return r, t, nil
		}
	}
	two := func(f func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int)) mpfrOpFn {
		return func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			y, err := read(vm, a[2])
			if err != nil {
				return nil, 0, err
			}
			r, t := f(vm, x, y, prec, rnd)
			return r, t, nil
		}
	}
	twoErr := func(f func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int, error)) mpfrOpFn {
		return func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			y, err := read(vm, a[2])
			if err != nil {
				return nil, 0, err
			}
			return f(vm, x, y, prec, rnd)
		}
	}

	op("set", "ffr", plain((*VM).mpfrSet))
	op("neg", "ffr", plain(func(vm *VM, x *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		return vm.mpfrSet(x.negated(), prec, rnd)
	}))
	op("sqrt", "ffr", plain((*VM).mpfrSqrt))
	op("copysign", "fffr", two(func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
		z := *x
		z.neg = y.neg
		if x.kind == kNaN {
			z.neg = x.neg
		}
		return vm.mpfrSet(&z, prec, rnd)
	}))
	arith := map[string]func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int){
		"add": (*VM).mpfrAdd, "mul": (*VM).mpfrMul, "div": (*VM).mpfrDiv,
		"sub": func(vm *VM, x, y *mpfrNum, prec int64, rnd int) (*mpfrNum, int) {
			return vm.mpfrAdd(x, y.negated(), prec, rnd)
		},
	}
	for name, f := range arith {
		op(name, "fffr", two(f))
		for tn, sc := range scalarKinds {
			// x op c, and for - and / c op x
			op(name+"_"+tn, "ff"+string(sc.kind)+"r", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
				x, err := read(vm, a[1])
				if err != nil {
					return nil, 0, err
				}
				c, err := sc.num(vm, a[2])
				if err != nil {
					return nil, 0, err
				}
				r, t := f(vm, x, c, prec, rnd)
				return r, t, nil
			})
			if name != "sub" && name != "div" || tn == "z" && name == "div" {
				continue
			}
			op(tn+"_"+name, "f"+string(sc.kind)+"fr", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
				c, err := sc.num(vm, a[1])
				if err != nil {
					return nil, 0, err
				}
				x, err := read(vm, a[2])
				if err != nil {
					return nil, 0, err
				}
				r, t := f(vm, c, x, prec, rnd)
				return r, t, nil
			})
		}
	}
	op("fma", "ffffr", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
		var v [3]*mpfrNum
		for i := range v {
			var err error
			if v[i], err = read(vm, a[i+1]); err != nil {
				return nil, 0, err
			}
		}
		r, t := vm.mpfrFMA(v[0], v[1], v[2], prec, rnd)
		return r, t, nil
	})
	op("mul_2si", "ffir", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
		x, err := read(vm, a[1])
		if err != nil {
			return nil, 0, err
		}
		r, t := vm.mpfrMul2(x, a[2].(int64), prec, rnd)
		return r, t, nil
	})
	op("mul_2ui", "ffur", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
		x, err := read(vm, a[1])
		if err != nil {
			return nil, 0, err
		}
		r, t := vm.mpfrMul2(x, int64(min(a[2].(uint64), 1<<62)), prec, rnd)
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
	for tn, sc := range scalarKinds {
		if tn == "d" {
			continue
		}
		op("pow_"+tn, "ff"+string(sc.kind)+"r", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			y, err := sc.num(vm, a[2])
			if err != nil {
				return nil, 0, err
			}
			return vm.mpfrPow("mpfr_pow_"+tn, x, y, prec, rnd)
		})
	}
	op("fac_ui", "fur", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
		return vm.mpfrFac(a[1].(uint64), prec, rnd)
	})
	op("const_log2", "fr", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
		return vm.mpfrLog2Const(prec, rnd)
	})
	for _, n := range []string{"trunc", "ceil", "floor", "round", "roundeven"} {
		mode := map[string]int{"trunc": rndZ, "ceil": rndU, "floor": rndD, "round": rndNA, "roundeven": rndN}[n]
		op(n, "ff", func(vm *VM, a []any, _ int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			return vm.mpfrRint("mpfr_"+n, x, prec, mode)
		})
	}
	// the elementary functions
	for name, u := range bfUnaries {
		op(name, "ffr", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			x, err := read(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			return vm.elemValue("mpfr_"+name, u, prec, x, rnd)
		})
	}
	for _, r := range refusedFns {
		fns["mpfr_"+r.name] = mpfrFn{r.sig, func(vm *VM, a []any) (any, error) {
			return nil, fmt.Errorf("%w: mpfr_%s, %s, is neither in math/big nor in bigfloat", ErrUnsupported, r.name, r.why)
		}}
	}
	fns["mpfr_sin_cos"] = mpfrFn{"fffr", func(vm *VM, a []any) (any, error) {
		s, c := a[0].(*mpfrRef), a[1].(*mpfrRef)
		rnd, err := vm.roundingArg("mpfr_sin_cos", a[3])
		if err != nil {
			return nil, err
		}
		x, err := read(vm, a[2])
		if err != nil {
			return nil, err
		}
		sr, st, err := vm.elemValue("mpfr_sin", bfUnaries["sin"], s.prec, x, rnd)
		if err != nil {
			return nil, err
		}
		cr, ct, err := vm.elemValue("mpfr_cos", bfUnaries["cos"], c.prec, x, rnd)
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(s, sr)
		vm.mpfrWrite(c, cr)
		return inexPos(st) + 4*inexPos(ct), nil
	}}
	fns["mpfr_modf"] = mpfrFn{"fffr", func(vm *VM, a []any) (any, error) {
		ipr, fpr := a[0].(*mpfrRef), a[1].(*mpfrRef)
		rnd, err := vm.roundingArg("mpfr_modf", a[3])
		if err != nil {
			return nil, err
		}
		x, err := read(vm, a[2])
		if err != nil {
			return nil, err
		}
		ip, fp, code, err := vm.mpfrModf(x, ipr.prec, fpr.prec, rnd)
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(ipr, ip)
		vm.mpfrWrite(fpr, fp)
		return code, nil
	}}
	fns["mpfr_frexp"] = mpfrFn{"pffr", func(vm *VM, a []any) (any, error) {
		z := a[1].(*mpfrRef)
		rnd, err := vm.roundingArg("mpfr_frexp", a[3])
		if err != nil {
			return nil, err
		}
		x, err := read(vm, a[2])
		if err != nil {
			return nil, err
		}
		y, e, t := vm.mpfrFrexp(x, z.prec, rnd)
		if err := vm.storeAs("mpfr_frexp", a[0], e); err != nil {
			return nil, err
		}
		return vm.mpfrRet(z, y, t)
	}}

	// conversions to a BigFloat
	for tn, sc := range map[string]struct {
		kind byte
		num  func(vm *VM, v any) (*mpfrNum, error)
	}{"si": scalarKinds["si"], "ui": scalarKinds["ui"], "d": scalarKinds["d"], "z": scalarKinds["z"]} {
		op("set_"+tn, "f"+string(sc.kind)+"r", func(vm *VM, a []any, rnd int, prec int64) (*mpfrNum, int, error) {
			c, err := sc.num(vm, a[1])
			if err != nil {
				return nil, 0, err
			}
			r, t := vm.mpfrSet(c, prec, rnd)
			return r, t, nil
		})
	}
	fns["mpfr_set_str"] = mpfrFn{"fscr", func(vm *VM, a []any) (any, error) {
		z, base := a[0].(*mpfrRef), int(a[2].(int32))
		rnd, err := vm.roundingArg("mpfr_set_str", a[3])
		if err != nil {
			return nil, err
		}
		// MPFR asserts a base of 0 or 2 to 62
		if base < 0 || base == 1 || base > 62 {
			return nil, fmt.Errorf("%w: mpfr_set_str in base %d (MPFR aborts)", ErrUnsupported, base)
		}
		p, ok := mpfrParse(a[1].(string), base)
		if !ok {
			return int32(-1), nil
		}
		x, _, err := vm.mpfrOfParsed(p, z.prec, rnd)
		if err != nil {
			return nil, err
		}
		vm.mpfrWrite(z, x)
		return int32(0), nil
	}}

	// conversions from a BigFloat
	getInt := func(name string, lo, hi *big.Int, mk func(*big.Int) any) {
		fns[name] = mpfrFn{"fr", func(vm *VM, a []any) (any, error) {
			rnd, err := vm.roundingArg(name, a[1])
			if err != nil {
				return nil, err
			}
			x, err := read(vm, a[0])
			if err != nil {
				return nil, err
			}
			n, err := vm.mpfrGetInt(name, x, rnd, lo, hi)
			if err != nil {
				return nil, err
			}
			return mk(n), nil
		}}
	}
	getInt("__gmpfr_mpfr_get_sj", int64Min, int64Max, func(n *big.Int) any { return n.Int64() })
	getInt("__gmpfr_mpfr_get_uj", new(big.Int), uint64Max, func(n *big.Int) any { return n.Uint64() })
	fns["mpfr_get_z"] = mpfrFn{"zfr", func(vm *VM, a []any) (any, error) {
		rnd, err := vm.roundingArg("mpfr_get_z", a[2])
		if err != nil {
			return nil, err
		}
		x, err := read(vm, a[1])
		if err != nil {
			return nil, err
		}
		n, t, err := vm.mpfrGetZ(x, rnd)
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
		fns["mpfr_"+name] = mpfrFn{"ff", func(vm *VM, a []any) (any, error) {
			x, err := read(vm, a[0])
			if err != nil {
				return nil, err
			}
			y, err := read(vm, a[1])
			if err != nil {
				return nil, err
			}
			return mpfrPred(x, y, ok), nil
		}}
	}
	for tn, sc := range scalarKinds {
		fns["mpfr_cmp_"+tn] = mpfrFn{"f" + string(sc.kind), func(vm *VM, a []any) (any, error) {
			x, err := read(vm, a[0])
			if err != nil {
				return nil, err
			}
			c, err := sc.num(vm, a[1])
			if err != nil {
				return nil, err
			}
			return vm.mpfrCmp(x, c), nil
		}}
	}

	// queries and in-place functions
	fns["mpfr_get_prec"] = mpfrFn{"f", func(vm *VM, a []any) (any, error) { return a[0].(*mpfrRef).prec, nil }}
	fns["mpfr_get_exp"] = mpfrFn{"f", func(vm *VM, a []any) (any, error) {
		x, err := read(vm, a[0])
		if err != nil {
			return nil, err
		}
		return x.exp, nil
	}}
	fns["mpfr_integer_p"] = mpfrFn{"f", func(vm *VM, a []any) (any, error) {
		x, err := read(vm, a[0])
		if err != nil {
			return nil, err
		}
		return b2i(isInteger(x)), nil
	}}
	for name, up := range map[string]bool{"nextabove": true, "nextbelow": false} {
		fns["mpfr_"+name] = mpfrFn{"f", func(vm *VM, a []any) (any, error) {
			z := a[0].(*mpfrRef)
			x, err := read(vm, z)
			if err != nil {
				return nil, err
			}
			if up {
				vm.mpfrWrite(z, vm.mpfrNextAbove(x, z.prec))
			} else {
				vm.mpfrWrite(z, vm.mpfrNextAbove(x.negated(), z.prec).negated())
			}
			return int32(0), nil
		}}
	}

	// the library, its exponent range, its flags
	fns["mpfr_get_version"] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return vm.stringPtrOf(mpfrVersion), nil }}
	fns["mpfr_get_patches"] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return vm.stringPtrOf(""), nil }}
	fns["mpfr_custom_get_size"] = mpfrFn{"i", func(vm *VM, a []any) (any, error) {
		// MPFR_PREC2LIMBS(prec) limbs, in C's arithmetic
		return uint64((a[0].(int64)-1)/64+1) * 8, nil
	}}
	fns["mpfr_get_emin"] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return vm.mpfr.emin, nil }}
	fns["mpfr_get_emax"] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return vm.mpfr.emax, nil }}
	for name, v := range map[string]int64{"emin_min": mpfrEminMin, "emin_max": mpfrEmaxMax, "emax_min": mpfrEminMin, "emax_max": mpfrEmaxMax} {
		fns["mpfr_get_"+name] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return v, nil }}
	}
	// set_emin and set_emax: 0, or 1 for a value outside the limits
	fns["mpfr_set_emin"] = mpfrFn{"i", func(vm *VM, a []any) (any, error) {
		if n := a[0].(int64); n >= mpfrEminMin && n <= mpfrEmaxMax {
			vm.mpfr.emin = n
			return int32(0), nil
		}
		return int32(1), nil
	}}
	fns["mpfr_set_emax"] = mpfrFn{"i", func(vm *VM, a []any) (any, error) {
		if n := a[0].(int64); n >= mpfrEminMin && n <= mpfrEmaxMax {
			vm.mpfr.emax = n
			return int32(0), nil
		}
		return int32(1), nil
	}}
	fns["mpfr_clear_flags"] = mpfrFn{"", func(vm *VM, a []any) (any, error) { vm.mpfr.flags = 0; return nil, nil }}
	for name, f := range map[string]uint8{"underflow_p": flagUnderflow, "overflow_p": flagOverflow, "divby0_p": flagDivBy0,
		"nanflag_p": flagNaN, "inexflag_p": flagInexact, "erangeflag_p": flagErange} {
		fns["mpfr_"+name] = mpfrFn{"", func(vm *VM, a []any) (any, error) { return b2i(vm.mpfr.flags&f != 0), nil }}
	}

	// printing: mpfr_asprintf(&str, "%Re", x), whose string mpfr_free_str frees
	fns["mpfr_asprintf"] = mpfrFn{"psf", func(vm *VM, a []any) (any, error) {
		prec, ok := printfFormat(a[1].(string))
		if !ok {
			return nil, fmt.Errorf("%w: mpfr_asprintf with the format %q (mpfr.jl passes %%Re and %%.<k>Re)", ErrUnsupported, a[1])
		}
		r := a[2].(*mpfrRef)
		x, err := read(vm, r)
		if err != nil {
			return nil, err
		}
		text, err := vm.formatE(x, r.prec, prec)
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
	fns["mpfr_free_str"] = mpfrFn{"p", func(vm *VM, a []any) (any, error) { return nil, nil }}
	return fns
}
