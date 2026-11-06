# julia 1.12.6's libmpfr calls as Base's mpfr.jl makes them, for
# interp/mpfr_test.go: each call's results, one line each, tab-separated: the
# function, its inputs, its results, then the six flags (underflow, overflow,
# divby0, nan, inexact, erange) as 0/1 digits. A BigFloat is
#   B:<prec>:<kind>:<sign>:<exp>:<limbs in hex, least significant first>
# kind n (NaN), i (Inf), z (zero) or r (regular), exactly the fields julia's
# code reads (prec, sign, exp and d). Run with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/mpfr.jl > interp/testdata/mpfr.golden
using Random
const R = Base.MPFR
const L = R.libmpfr
const BF = BigFloat
const BI = BigInt

rm(r::Integer) = R.MPFRRoundingMode(r)
const MODES = 0:4

function benc(x::BF)
    p = x.prec
    k = isnan(x) ? "n" : isinf(x) ? "i" : iszero(x) ? "z" : "r"
    limbs = k == "r" ? join((string(x.d[i], base=16) for i in 1:cld(p, 64)), ",") : ""
    string("B:", p, ":", k, ":", x.sign, ":", k == "r" ? x.exp : 0, ":", limbs)
end
zin(x::BI) = string("z:", x)
raw(x::BI) = string("Z:", x.size, ";", join((string(unsafe_load(x.d, i), base=16) for i in 1:abs(x.size)), ","))
uin(x) = string("u:", x)
iin(x) = string("i:", x)
fin(x::Float64) = string("f:", string(reinterpret(UInt64, x), base=16))
rin(r) = string("r:", r)
sin_(s) = string("s:", escape_string(s))
iout(x) = string("I:", x)
fout(x::Float64) = string("F:", string(reinterpret(UInt64, x), base=16))
sout(s) = string("S:", escape_string(s))

flags() = string(Int(R.had_underflow()), Int(R.had_overflow()), Int(R.had_divbyzero()),
                 Int(R.had_nan()), Int(R.had_inexact_exception()), Int(R.had_range_exception()))

PFX = ""
function emit(op, ins, f)
    R.clear_flags()
    outs = try
        f()
    catch e
        ["E:" * string(nameof(typeof(e)))]
    end
    fl = flags()
    println(join(vcat(PFX * op, ins, outs, fl), '\t'))
    flush(stdout)
end

newf(p) = BF(precision=p)

# unary functions with a rounding mode: z, x, rnd
UN = (:set, :neg, :sqrt, :exp, :exp2, :exp10, :expm1, :cosh, :sinh, :tanh, :sech, :csch, :coth, :cbrt,
      :log, :log2, :log10, :log1p, :sin, :cos, :tan, :sec, :csc, :acos, :asin, :atan, :acosh, :asinh,
      :atanh, :sinpi, :cospi, :tanpi)
for f in UN
    @eval $(Symbol(:un_, f))(x, p, r) = (z = newf(p); t = ccall(($(QuoteNode(Symbol(:mpfr_, f))), L), Int32,
        (Ref{BF}, Ref{BF}, R.MPFRRoundingMode), z, x, rm(r)); (z, t))
end
UNU = (:sinu, :cosu, :tanu, :asinu, :acosu, :atanu)
for f in UNU
    @eval $(Symbol(:un_, f))(x, p, r) = (z = newf(p); t = ccall(($(QuoteNode(Symbol(:mpfr_, f))), :libmpfr), Int32,
        (Ref{BF}, Ref{BF}, Culong, R.MPFRRoundingMode), z, x, 360, rm(r)); (z, t))
end
# no rounding mode: z, x
for f in (:trunc, :roundeven, :ceil, :floor, :round)
    @eval $(Symbol(:rd_, f))(x, p) = (z = newf(p); t = ccall(($(QuoteNode(Symbol(:mpfr_, f))), L), Int32,
        (Ref{BF}, Ref{BF}), z, x); (z, t))
end
# binary: z, x, y, rnd
for f in (:add, :sub, :mul, :div, :pow, :hypot, :atan2, :copysign, :fmod, :remainder)
    @eval $(Symbol(:bi_, f))(x, y, p, r) = (z = newf(p); t = ccall(($(QuoteNode(Symbol(:mpfr_, f))), L), Int32,
        (Ref{BF}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), z, x, y, rm(r)); (z, t))
end
bi_atan2u(x, y, p, r) = (z = newf(p); t = ccall((:mpfr_atan2u, :libmpfr), Int32,
    (Ref{BF}, Ref{BF}, Ref{BF}, Culong, R.MPFRRoundingMode), z, x, y, 360, rm(r)); (z, t))
# x op scalar, with the scalar of each kind
for (f, ty, tn) in ((:add, :Culong, :ui), (:add, :Clong, :si), (:add, :Cdouble, :d), (:add, BI, :z),
                    (:mul, :Culong, :ui), (:mul, :Clong, :si), (:mul, :Cdouble, :d), (:mul, BI, :z),
                    (:sub, :Culong, :ui), (:sub, :Clong, :si), (:sub, :Cdouble, :d), (:sub, BI, :z),
                    (:div, :Culong, :ui), (:div, :Clong, :si), (:div, :Cdouble, :d), (:div, BI, :z),
                    (:pow, :Culong, :ui), (:pow, :Clong, :si), (:pow, BI, :z))
    rt = ty === BI ? :(Ref{BI}) : ty
    @eval $(Symbol(:sc_, f, :_, tn))(x, c, p, r) = (z = newf(p); t = ccall(($(QuoteNode(Symbol(:mpfr_, f, :_, tn))), L), Int32,
        (Ref{BF}, Ref{BF}, $rt, R.MPFRRoundingMode), z, x, c, rm(r)); (z, t))
end
# scalar op x
for (f, ty, tn) in ((:sub, :Culong, :ui), (:sub, :Clong, :si), (:sub, :Cdouble, :d), (:sub, BI, :z),
                    (:div, :Culong, :ui), (:div, :Clong, :si), (:div, :Cdouble, :d))
    rt = ty === BI ? :(Ref{BI}) : ty
    nm = Symbol(:mpfr_, tn, :_, f)
    @eval $(Symbol(:rs_, f, :_, tn))(c, x, p, r) = (z = newf(p); t = ccall(($(QuoteNode(nm)), L), Int32,
        (Ref{BF}, $rt, Ref{BF}, R.MPFRRoundingMode), z, c, x, rm(r)); (z, t))
end
mul2si(x, n, p, r) = (z = newf(p); t = ccall((:mpfr_mul_2si, L), Int32, (Ref{BF}, Ref{BF}, Clong, R.MPFRRoundingMode), z, x, n, rm(r)); (z, t))
mul2ui(x, n, p, r) = (z = newf(p); t = ccall((:mpfr_mul_2ui, L), Int32, (Ref{BF}, Ref{BF}, Culong, R.MPFRRoundingMode), z, x, n, rm(r)); (z, t))
fma_(x, y, w, p, r) = (z = newf(p); t = ccall((:mpfr_fma, L), Int32, (Ref{BF}, Ref{BF}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), z, x, y, w, rm(r)); (z, t))
sincos_(x, p, r) = (s = newf(p); c = newf(p); t = ccall((:mpfr_sin_cos, L), Int32, (Ref{BF}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), s, c, x, rm(r)); (s, c, t))
modf_(x, p, r) = (zi = newf(p); zf = newf(p); t = ccall((:mpfr_modf, L), Int32, (Ref{BF}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), zi, zf, x, rm(r)); (zi, zf, t))
frexp_(x, p, r) = (z = newf(p); c = Ref{Clong}(); t = ccall((:mpfr_frexp, L), Int32, (Ptr{Clong}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), c, z, x, rm(r)); (z, c[], t))
fac_(n, p, r) = (z = newf(p); t = ccall((:mpfr_fac_ui, L), Int32, (Ref{BF}, Culong, R.MPFRRoundingMode), z, n, rm(r)); (z, t))
log2c(p, r) = (z = newf(p); t = ccall((:mpfr_const_log2, L), Cint, (Ref{BF}, R.MPFRRoundingMode), z, rm(r)); (z, t))
nextf(::Val{:mpfr_nextabove}, x) = (z = R._duplicate(x); ccall((:mpfr_nextabove, L), Int32, (Ref{BF},), z); z)
nextf(::Val{:mpfr_nextbelow}, x) = (z = R._duplicate(x); ccall((:mpfr_nextbelow, L), Int32, (Ref{BF},), z); z)

# inputs
const P = 53
b53(x) = BF(x; precision=P)
function at(p, f)
    setprecision(BF, p) do
        f()
    end
end
function ld(x::BF, n::Integer)  # x * 2^n at x's precision
    z = newf(x.prec)
    ccall((:mpfr_mul_2si, L), Int32, (Ref{BF}, Ref{BF}, Clong, R.MPFRRoundingMode), z, x, n, rm(0))
    z
end

NANF = BF(NaN; precision=P)
PINF = BF(Inf; precision=P)
NINF = BF(-Inf; precision=P)
PZERO = BF(0; precision=P)
NZERO = BF(-0.0; precision=P)
third = at(130, () -> BF(1) / 3)
pi130 = at(130, () -> BF(pi))
e130 = at(130, () -> exp(BF(1)))
seven10 = BF("0.7"; precision=130)
V = BF[NANF, PINF, NINF, PZERO, NZERO,
       b53(1), b53(-1), b53(0.5), b53(1.5), b53(-2.5), b53(3), b53(10), b53(2.0^-20), b53(123456.789), b53(1e300), b53(1e-300),
       third, -pi130, e130, seven10, BF(1; precision=2), BF(-1.5; precision=2), BF(3; precision=64), b53(2.0^100),
       BF(nextfloat(1.0); precision=53), BF(prevfloat(1.0); precision=53), BF(1 - 2.0^-60; precision=64), BF(35.5; precision=53),
       BF(2.0^70 + 2.0^20; precision=100), BF(-1000.25; precision=53)]
# exponents far beyond Int32: argument reduction of these takes MPFR forever
VH = BF[ld(BF(1.5; precision=53), 1 << 40), ld(BF(-1.5; precision=53), -(1 << 40)),
        ld(BF(1.5; precision=53), (1 << 62) - 2), ld(BF(-1; precision=53), -(1 << 62))]
NOTRIG = (:set, :neg, :sqrt, :exp, :exp2, :exp10, :expm1, :cosh, :sinh, :tanh, :sech, :csch, :coth, :cbrt,
          :log, :log2, :log10, :log1p, :atan, :acosh, :asinh, :atanh)
# a narrower list for binary functions
VB = BF[NANF, PINF, NINF, PZERO, NZERO, b53(1), b53(-2.5), b53(2.0^-20), third, -pi130, b53(3), b53(0.5),
        b53(-1)]
VBH = vcat(VB, VH)
PS = (2, 53, 130)
PB = (2, 53, 130)

# deterministic thinning of the large products
keep(i, k) = (i * 2654435761) % 4294967296 % k == 0

for f in UN
    g = getfield(Main, Symbol(:un_, f))
    for (i, x) in enumerate(f in NOTRIG ? vcat(V, VH) : V), p in PS, r in MODES
        keep(i + p + 3r, 2) || continue
        emit(string(f), [benc(x), uin(p), rin(r)], () -> ((z, t) = g(x, p, r); [benc(z), iout(sign(t))]))
    end
end
for f in UNU
    g = getfield(Main, Symbol(:un_, f))
    for x in VB, p in PS, r in MODES
        emit(string(f), [benc(x), uin(p), rin(r)], () -> ((z, t) = g(x, p, r); [benc(z), iout(sign(t))]))
    end
end
for f in (:trunc, :roundeven, :ceil, :floor, :round)
    g = getfield(Main, Symbol(:rd_, f))
    for x in V, p in PS
        emit(string(f), [benc(x), uin(p)], () -> ((z, t) = g(x, p); [benc(z), iout(sign(t))]))
    end
end
# rounding to integers of values with fractions
for f in (:trunc, :roundeven, :ceil, :floor, :round)
    g = getfield(Main, Symbol(:rd_, f))
    for v in (0.5, 1.5, 2.5, -0.5, -1.5, -2.5, 0.3, -0.7, 1e10 + 0.5, 4503599627370496.5, 1.0, 7.0), p in (2, 53, 130)
        x = b53(v)
        emit(string(f), [benc(x), uin(p)], () -> ((z, t) = g(x, p); [benc(z), iout(sign(t))]))
    end
end
for f in (:add, :sub, :mul, :div, :pow, :hypot, :atan2, :copysign, :fmod, :remainder, :atan2u)
    g = getfield(Main, Symbol(:bi_, f))
    W = f in (:add, :sub, :mul, :div, :copysign, :hypot) ? VBH : VB
    for (i, (x, y)) in enumerate(Iterators.product(W, W)), p in PB, r in MODES
        keep(i + p + r, f in (:add, :sub, :mul, :div, :copysign, :hypot) ? 6 : 4) || continue
        emit(string(f), [benc(x), benc(y), uin(p), rin(r)], () -> ((z, t) = g(x, y, p, r); [benc(z), iout(sign(t))]))
    end
end
# the destination aliasing an operand: mpfr_add(z, z, x) as sum does
for x in VB[1:13], y in VB[1:13], r in MODES
    emit("add_alias", [benc(x), benc(y), rin(r)], () -> begin
        z = R._duplicate(x)
        t = ccall((:mpfr_add, L), Int32, (Ref{BF}, Ref{BF}, Ref{BF}, R.MPFRRoundingMode), z, z, y, rm(r))
        [benc(z), iout(sign(t))]
    end)
end

SU = Culong[0, 1, 3, 1 << 53 + 1, typemax(Culong)]
SS = Clong[typemin(Clong), -3, -1, 0, 2, typemax(Clong)]
SD = Float64[0.0, -0.0, 0.1, -3.5, 1e300, 5e-324, Inf, -Inf, NaN]
SZ = BI[0, 1, -7, big(2)^64 + 1, -big(10)^30, big(2)^200]
SCAL = Dict(:ui => SU, :si => SS, :d => SD, :z => SZ)
sin2(c::Culong) = uin(c)
sin2(c::Clong) = iin(c)
sin2(c::Float64) = fin(c)
sin2(c::BI) = zin(c)
for f in (:add, :mul, :sub, :div), tn in (:ui, :si, :d, :z)
    g = getfield(Main, Symbol(:sc_, f, :_, tn))
    for (i, x) in enumerate(VBH), c in SCAL[tn], p in (2, 130), r in MODES
        keep(i + r, 4) || continue
        emit(string(f, "_", tn), [benc(x), sin2(c), uin(p), rin(r)], () -> ((z, t) = g(x, c, p, r); [benc(z), iout(sign(t))]))
    end
end
for (f, tn) in ((:pow, :ui), (:pow, :si), (:pow, :z))
    g = getfield(Main, Symbol(:sc_, f, :_, tn))
    for (i, x) in enumerate(VB), c in SCAL[tn], p in (2, 53, 130), r in MODES
        keep(i + r + p, 3) || continue
        emit(string(f, "_", tn), [benc(x), sin2(c), uin(p), rin(r)], () -> ((z, t) = g(x, c, p, r); [benc(z), iout(sign(t))]))
    end
end
for (f, tn) in ((:sub, :ui), (:sub, :si), (:sub, :d), (:sub, :z), (:div, :ui), (:div, :si), (:div, :d))
    g = getfield(Main, Symbol(:rs_, f, :_, tn))
    for (i, x) in enumerate(VBH), c in SCAL[tn], p in (2, 130), r in MODES
        keep(i + r, 4) || continue
        emit(string(tn, "_", f), [sin2(c), benc(x), uin(p), rin(r)], () -> ((z, t) = g(c, x, p, r); [benc(z), iout(sign(t))]))
    end
end
for x in VBH, n in Clong[0, 1, -1, 5, -70, 1 << 40, -(1 << 40), (1 << 62), -(1 << 62), typemax(Clong), typemin(Clong)], p in (2, 130), r in MODES
    keep(hash(x.exp) % 99 + n % 17 + p + r, 3) || continue
    emit("mul_2si", [benc(x), iin(n), uin(p), rin(r)], () -> ((z, t) = mul2si(x, n, p, r); [benc(z), iout(sign(t))]))
end
for x in VBH, n in Culong[0, 1, 5, 70, 1 << 40, (1 << 62), typemax(Culong)], p in (2, 130), r in MODES
    keep(hash(x.exp) % 99 + n % 17 + p + r, 3) || continue
    emit("mul_2ui", [benc(x), uin(n), uin(p), rin(r)], () -> ((z, t) = mul2ui(x, n, p, r); [benc(z), iout(sign(t))]))
end
VF = BF[NANF, PINF, NINF, PZERO, NZERO, b53(1), b53(-2.5), b53(2.0^-20), third, b53(3), ld(BF(1.5; precision=53), 1 << 40), ld(BF(1; precision=53), -(1 << 62))]
for (i, (x, y, w)) in enumerate(Iterators.product(VF, VF, VF)), p in (2, 53, 130), r in MODES
    keep(i + p + r, 25) || continue
    emit("fma", [benc(x), benc(y), benc(w), uin(p), rin(r)], () -> ((z, t) = fma_(x, y, w, p, r); [benc(z), iout(sign(t))]))
end
for x in V, p in PS, r in MODES
    emit("sin_cos", [benc(x), uin(p), rin(r)], () -> ((s, c, t) = sincos_(x, p, r); [benc(s), benc(c), iout(t)]))
    emit("modf", [benc(x), uin(p), rin(r)], () -> ((zi, zf, t) = modf_(x, p, r); [benc(zi), benc(zf), iout(t)]))
    emit("frexp", [benc(x), uin(p), rin(r)], () -> ((z, e, t) = frexp_(x, p, r); [benc(z), iout(e), iout(sign(t))]))
end
for n in Culong[0, 1, 2, 5, 20, 21, 25, 100, 1000], p in (2, 53, 130, 1100), r in MODES
    emit("fac_ui", [uin(n), uin(p), rin(r)], () -> ((z, t) = fac_(n, p, r); [benc(z), iout(sign(t))]))
end
for p in (1, 2, 53, 64, 65, 130, 400), r in MODES
    emit("const_log2", [uin(p), rin(r)], () -> ((z, t) = log2c(p, r); [benc(z), iout(sign(t))]))
end

# predicates and comparisons
for (n, f) in (("equal_p", :mpfr_equal_p), ("lessequal_p", :mpfr_lessequal_p), ("greaterequal_p", :mpfr_greaterequal_p),
               ("less_p", :mpfr_less_p), ("greater_p", :mpfr_greater_p))
    @eval cmp2_(::Val{$(QuoteNode(f))}, x, y) = ccall(($(QuoteNode(f)), L), Int32, (Ref{BF}, Ref{BF}), x, y)
    for x in VBH, y in VBH
        emit(n, [benc(x), benc(y)], () -> [iout(cmp2_(Val(f), x, y))])
    end
end
for x in V
    emit("integer_p", [benc(x)], () -> [iout(ccall((:mpfr_integer_p, L), Int32, (Ref{BF},), x))])
    emit("get_prec", [benc(x)], () -> [iout(ccall((:mpfr_get_prec, L), Clong, (Ref{BF},), x))])
    isfinite(x) && !iszero(x) && emit("get_exp", [benc(x)], () -> [iout(ccall((:mpfr_get_exp, L), Clong, (Ref{BF},), x))])
    for (n, f) in (("nextabove", :mpfr_nextabove), ("nextbelow", :mpfr_nextbelow))
        emit(n, [benc(x)], () -> [benc(nextf(Val(f), x))])
    end
    for c in SZ
        emit("cmp_z", [benc(x), zin(c)], () -> [iout(sign(ccall((:mpfr_cmp_z, L), Int32, (Ref{BF}, Ref{BI}), x, c)))])
    end
    for c in SS
        emit("cmp_si", [benc(x), iin(c)], () -> [iout(sign(ccall((:mpfr_cmp_si, L), Int32, (Ref{BF}, Clong), x, c)))])
    end
    for c in SU
        emit("cmp_ui", [benc(x), uin(c)], () -> [iout(sign(ccall((:mpfr_cmp_ui, L), Int32, (Ref{BF}, Culong), x, c)))])
    end
    for c in SD
        emit("cmp_d", [benc(x), fin(c)], () -> [iout(sign(ccall((:mpfr_cmp_d, L), Int32, (Ref{BF}, Cdouble), x, c)))])
    end
end

# conversions to a BigFloat
for p in (1, 2, 24, 53, 64, 65, 130), r in MODES
    for x in VB
        emit("set", [benc(x), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set, L), Int32, (Ref{BF}, Ref{BF}, R.MPFRRoundingMode), z, x, rm(r)); [benc(z), iout(sign(t))]))
    end
    for c in SS
        emit("set_si", [iin(c), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set_si, L), Int32, (Ref{BF}, Clong, R.MPFRRoundingMode), z, c, rm(r)); [benc(z), iout(sign(t))]))
    end
    for c in SU
        emit("set_ui", [uin(c), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set_ui, L), Int32, (Ref{BF}, Culong, R.MPFRRoundingMode), z, c, rm(r)); [benc(z), iout(sign(t))]))
    end
    for c in SD
        emit("set_d", [fin(c), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set_d, L), Int32, (Ref{BF}, Cdouble, R.MPFRRoundingMode), z, c, rm(r)); [benc(z), iout(sign(t))]))
    end
    for c in SZ
        emit("set_z", [zin(c), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set_z, L), Int32, (Ref{BF}, Ref{BI}, R.MPFRRoundingMode), z, c, rm(r)); [benc(z), iout(sign(t))]))
    end
end

# conversions from a BigFloat
CV = BF[NANF, PINF, NINF, PZERO, NZERO, b53(1), b53(-1), b53(0.5), b53(-0.5), b53(1.5), b53(-2.5), b53(2.0^63), b53(-2.0^63), b53(2.0^64), b53(-2.0^64),
        b53(2.0^63 - 1024), b53(2.0^64 - 2048), b53(1e300), third, -pi130, b53(0.7), b53(-0.3), BF(2^62 + 0.5; precision=64), BF(-2^62 - 0.5; precision=64),
        BF(2.0^63 - 0.5; precision=64), BF(2.0^64 - 0.5; precision=70), ld(BF(1; precision=53), 1 << 40)]
for x in CV, r in MODES
    emit("get_sj", [benc(x), rin(r)], () -> [iout(ccall((:__gmpfr_mpfr_get_sj, L), Cintmax_t, (Ref{BF}, R.MPFRRoundingMode), x, rm(r)))])
    emit("get_uj", [benc(x), rin(r)], () -> [string("U:", ccall((:__gmpfr_mpfr_get_uj, L), Cuintmax_t, (Ref{BF}, R.MPFRRoundingMode), x, rm(r)))])
    isfinite(x) && abs(x) < BF(2)^200 && emit("get_z", [benc(x), rin(r)], () -> (z = BI(); t = ccall((:mpfr_get_z, L), Int32, (Ref{BI}, Ref{BF}, R.MPFRRoundingMode), z, x, rm(r)); [raw(z), iout(sign(t))]))
    isfinite(x) || emit("get_z", [benc(x), rin(r)], () -> (z = BI(); t = ccall((:mpfr_get_z, L), Int32, (Ref{BI}, Ref{BF}, R.MPFRRoundingMode), z, x, rm(r)); [raw(z), iout(sign(t))]))
end

# exponent range
emit("emin_emax", [], () -> [iout(ccall((:mpfr_get_emin, L), Clong, ())), iout(ccall((:mpfr_get_emax, L), Clong, ())),
    iout(ccall((:mpfr_get_emin_min, L), Clong, ())), iout(ccall((:mpfr_get_emax_max, L), Clong, ())),
    iout(ccall((:mpfr_get_emin_max, L), Clong, ())), iout(ccall((:mpfr_get_emax_min, L), Clong, ()))])
emit("version", [], () -> [sout(R.version().major == 4 ? string(R.version()) : "?"), iout(length(R.patches()))])
for n in Clong[0, 1, -1, 63, 64, 65, 128, 129, 256, 1 << 40, typemax(Clong) >> 1]
    emit("custom_get_size", [iin(n)], () -> [iout(ccall((:mpfr_custom_get_size, L), Csize_t, (Clong,), n))])
end
oldmin, oldmax = R.get_emin(), R.get_emax()
for (a, b) in ((-20, 20), (-1, 1), (0, 0), (-5, 3))
    global PFX = "R$(a),$(b):"
    R.set_emin!(a); R.set_emax!(b)
    emit("emin_emax", [], () -> [iout(R.get_emin()), iout(R.get_emax())])
    for (i, (x, y)) in enumerate(Iterators.product(BF[b53(1), b53(-1), b53(3), b53(0.1), b53(1e-3), b53(2.0^-6), b53(7.5), b53(-100), b53(0.75), b53(1.5), third], BF[b53(1), b53(-1), b53(3), b53(0.1), b53(2.0^-4), b53(40)])), p in (2, 53), r in MODES
        for (n, g) in (("add", bi_add), ("sub", bi_sub), ("mul", bi_mul), ("div", bi_div), ("pow", bi_pow), ("hypot", bi_hypot))
            keep(i + p + r + length(n), 3) || continue
            emit(n, [benc(x), benc(y), uin(p), rin(r)], () -> ((z, t) = g(x, y, p, r); [benc(z), iout(sign(t))]))
        end
    end
    for (i, x) in enumerate(BF[b53(1), b53(-1), b53(3), b53(0.1), b53(2.0^-6), b53(7.5), b53(25), b53(-25), b53(0.75), b53(1.5), third]), p in (2, 53), r in MODES
        for f in (:sqrt, :exp, :exp2, :expm1, :sinh, :cosh, :cbrt, :log, :set, :neg, :exp10)
            keep(i + p + r + length(string(f)), 2) || continue
            g = getfield(Main, Symbol(:un_, f))
            emit(string(f), [benc(x), uin(p), rin(r)], () -> ((z, t) = g(x, p, r); [benc(z), iout(sign(t))]))
        end
        for n in Clong[-30, -2, 2, 5, 30]
            keep(i + p + r + n, 3) || continue
            emit("mul_2si", [benc(x), iin(n), uin(p), rin(r)], () -> ((z, t) = mul2si(x, n, p, r); [benc(z), iout(sign(t))]))
        end
    end
    for c in SD, p in (2, 53), r in MODES
        emit("set_d", [fin(c), uin(p), rin(r)], () -> (z = newf(p); t = ccall((:mpfr_set_d, L), Int32, (Ref{BF}, Cdouble, R.MPFRRoundingMode), z, c, rm(r)); [benc(z), iout(sign(t))]))
    end
    for n in Culong[2, 3, 10, 30], p in (2, 53), r in MODES
        emit("fac_ui", [uin(n), uin(p), rin(r)], () -> ((z, t) = fac_(n, p, r); [benc(z), iout(sign(t))]))
    end
    global PFX = ""
end
R.set_emin!(oldmin); R.set_emax!(oldmax)
for n in Clong[typemin(Clong), -(1 << 62), (1 << 62), typemax(Clong), -(1 << 62) + 1, (1 << 62) - 1]
    emit("set_emin", [iin(n)], () -> (o = R.get_emin(); r = ccall((:mpfr_set_emin, L), Cint, (Clong,), n); e = R.get_emin(); R.set_emin!(o); [iout(r), iout(e)]))
    emit("set_emax", [iin(n)], () -> (o = R.get_emax(); r = ccall((:mpfr_set_emax, L), Cint, (Clong,), n); e = R.get_emax(); R.set_emax!(o); [iout(r), iout(e)]))
end

# strings
STRS = ["0", "-0", "+0", "1", "-1", "1.5", ".5", "5.", "1e5", "1E-5", "1e+5", "1e", "e5", "1.5e3x", "x", "", " 1", "1 ", "  -2.5e2", "+7",
        "nan", "NaN", "-nan", "+NaN", "nan(abc)", "@nan@", "inf", "-Inf", "+inf", "infinity", "-INFINITY", "@inf@", "infx", "in",
        "0x10", "0X1.8p3", "-0x.8", "0x1p-2", "0x", "0xp1", "0b101", "0B1.1p2", "0b", "0b102", "010", "0.1", "0.7", "123456789012345678901234567890",
        "1e1000000000000000000000", "1e-1000000000000000000000", "1e400", "1e-400", "1e1000", "123e-2", "9.99999999999999999999e22", "0e100000000000000000000",
        "1@2", "1@-2", "1p2", "1e2e3", "1.2.3", "1_000", "1,5", "+-1", "--1", "- 1", "0.00000000000000000000000000000000000001", "4.9e-324",
        "1.7976931348623157e308", "0.1e-1", "00012", "1e0", "-1e-0", "1E+0003", "2.5e", "2.5e+", "0x1.fffffffffffffp1023", "0x1.0000000000001p0"]
for s in STRS, base in (0, 10, 2, 16), p in (2, 53, 130), r in MODES
    keep(hash(s) % 1000 + base + p + r, 3) || continue
    emit("set_str", [sin_(s), iin(base), uin(p), rin(r)], () -> begin
        z = newf(p)
        e = ccall((:mpfr_set_str, L), Int32, (Ref{BF}, Cstring, Int32, R.MPFRRoundingMode), z, s, base, rm(r))
        e == 0 ? [iout(0), benc(z)] : [iout(e)]
    end)
end
for (s, base) in (("zz", 36), ("Zz", 62), ("zZ", 62), ("1.8", 16), ("ff.8@2", 16), ("1e2", 16), ("1e2", 36), ("7", 8), ("8", 8), ("11.1", 3),
                  ("10", 37), ("z@1", 62), ("A", 11), ("a", 11), ("a", 36), ("0.1", 7))
    for p in (2, 53, 130), r in MODES
        emit("set_str", [sin_(s), iin(base), uin(p), rin(r)], () -> begin
            z = newf(p)
            e = ccall((:mpfr_set_str, L), Int32, (Ref{BF}, Cstring, Int32, R.MPFRRoundingMode), z, s, base, rm(r))
            e == 0 ? [iout(0), benc(z)] : [iout(e)]
        end)
    end
end

# printing, with the formats mpfr.jl passes
for x in V
    isfinite(x) || continue
    for fmt in ("%Re", "%.0Re", "%.1Re", "%.5Re", "%.20Re", "%.100Re")
        emit("asprintf", [benc(x), sin_(fmt)], () -> [iout(length(R.string_mpfr(x, fmt))), sout(R.string_mpfr(x, fmt))])
    end
    emit("string", [benc(x)], () -> [sout(string(x)), sout(sprint(show, x; context=:compact => true))])
end
