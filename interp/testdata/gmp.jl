# julia 1.12.6's libgmp calls as Base's gmp.jl makes them, for
# interp/gmp_test.go: each call's results, one line each, tab-separated: the
# function, its inputs, its results. A BigInt input is z:<decimal>, a result
# Z:<size>;<limbs in hex, least significant first>, exactly the fields
# julia's code reads (size and *d). Run with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/gmp.jl > interp/testdata/gmp.golden
using Random
const MPZ = Base.GMP.MPZ
const MPQ = Base.GMP.MPQ

raw(x::BigInt) = string("Z:", x.size, ";", join((string(unsafe_load(x.d, i), base=16) for i in 1:abs(x.size)), ","))
zin(x) = string("z:", x)
uin(x) = string("u:", x)
iin(x) = string("i:", x)
fin(x::Float64) = string("f:", string(reinterpret(UInt64, x), base=16))
cin(x) = string("c:", x)
sstr(s) = string("s:", escape_string(s))
iout(x) = string("I:", x)
fout(x::Float64) = string("F:", string(reinterpret(UInt64, x), base=16))

function emit(op, ins, f)
    outs = try
        f()
    catch e
        ["E:" * string(nameof(typeof(e)))]
    end
    println(join(vcat(op, ins, outs), '\t'))
end

rng = MersenneTwister(20261105)
V = BigInt[0, 1, -1, 2, -3, 10, -4294967295, big(2)^63 - 1, -big(2)^63, big(2)^63, big(2)^64 - 1,
           -big(2)^64, big(2)^64 + 1, big(2)^128, -(big(2)^128 - 1), big(10)^30, -(big(10)^30 + 7),
           rand(rng, big(2)^320:big(2)^321), -rand(rng, big(2)^700:big(2)^701)]
W = BigInt[0, 1, -1, 7, -10, big(2)^64 + 13, -(big(2)^70 + 9), big(10)^30]
U = UInt64[0, 1, 2, 4294967295, 9223372036854775808, 18446744073709551615]
S = Int64[-9223372036854775808, -3, -1, 0, 1, 2, 9223372036854775807]
SH = UInt64[0, 1, 31, 63, 64, 65, 127, 128, 200]
D = Float64[0.0, -0.0, 0.5, -0.5, 1.0, -1.5, 1e15, 123456789.99, 2.0^53, 2.0^63, -2.0^63, 2.0^64,
            1e20, 1.7976931348623157e308, -1e300, 5e-324, Inf, -Inf, NaN]

# binary operations
for (n, f) in (("add", MPZ.add), ("sub", MPZ.sub), ("mul", MPZ.mul), ("fdiv_q", MPZ.fdiv_q),
               ("tdiv_q", MPZ.tdiv_q), ("cdiv_q", MPZ.cdiv_q), ("fdiv_r", MPZ.fdiv_r),
               ("tdiv_r", MPZ.tdiv_r), ("cdiv_r", MPZ.cdiv_r), ("gcd", MPZ.gcd), ("lcm", MPZ.lcm),
               ("and", MPZ.and), ("ior", MPZ.ior), ("xor", MPZ.xor)),
    a in V, b in V
    emit(n, [zin(a), zin(b)], () -> [raw(f(a, b))])
end

# the destination is the first or the second operand
for (n, f!) in (("add", MPZ.add!), ("sub", MPZ.sub!), ("mul", MPZ.mul!), ("tdiv_q", MPZ.tdiv_q!),
                ("fdiv_r", MPZ.fdiv_r!), ("gcd", MPZ.gcd!), ("lcm", MPZ.lcm!), ("and", MPZ.and!),
                ("ior", MPZ.ior!), ("xor", MPZ.xor!)),
    a in W, b in W
    emit(n * "_a", [zin(a), zin(b)], () -> (x = MPZ.set(a); f!(x, x, b); [raw(x)]))
    emit(n * "_b", [zin(a), zin(b)], () -> (x = MPZ.set(b); f!(x, a, x); [raw(x)]))
end

for a in V, b in V
    emit("tdiv_qr", [zin(a), zin(b)], () -> ((q, r) = MPZ.tdiv_qr(a, b); [raw(q), raw(r)]))
    emit("cmp", [zin(a), zin(b)], () -> [iout(sign(MPZ.cmp(a, b)))])
    b == 0 || emit("invert", [zin(a), zin(b)], () -> (x = BigInt(); rc = MPZ.invert!(x, a, b);
                                                       rc == 0 ? [iout(0)] : [iout(1), raw(x)]))
    emit("gcdext", [zin(a), zin(b)], () -> ((g, s, t) = MPZ.gcdext(a, b); [raw(g), raw(s), raw(t)]))
end
for a in -6:6, b in -6:6
    emit("gcdext", [zin(big(a)), zin(big(b))], () -> ((g, s, t) = MPZ.gcdext(big(a), big(b)); [raw(g), raw(s), raw(t)]))
end

for a in W, b in W, c in W
    emit("powm", [zin(a), zin(b), zin(c)], () -> [raw(MPZ.powm(a, b, c))])
end

# unsigned long, long and shift operands
for (n, f) in (("add_ui", MPZ.add_ui), ("sub_ui", MPZ.sub_ui), ("mul_ui", MPZ.mul_ui)), a in V, u in U
    emit(n, [zin(a), uin(u)], () -> [raw(f(a, u))])
end
for a in V, u in U
    emit("ui_sub", [uin(u), zin(a)], () -> [raw(MPZ.ui_sub(u, a))])
    emit("cmp_ui", [zin(a), uin(u)], () -> [iout(sign(MPZ.cmp_ui(a, u)))])
end
for a in V, s in S
    emit("mul_si", [zin(a), iin(s)], () -> [raw(MPZ.mul_si(a, s))])
    emit("cmp_si", [zin(a), iin(s)], () -> [iout(sign(MPZ.cmp_si(a, s)))])
end
for a in V, e in SH
    emit("mul_2exp", [zin(a), uin(e)], () -> [raw(MPZ.mul_2exp(a, e))])
    emit("fdiv_q_2exp", [zin(a), uin(e)], () -> [raw(MPZ.fdiv_q_2exp(a, e))])
end
for a in BigInt[0, 1, -1, 2, -2, 3, -10, big(2)^64 + 1, big(10)^30], e in UInt64[0, 1, 2, 3, 5, 10, 64]
    emit("pow_ui", [zin(a), uin(e)], () -> [raw(MPZ.pow_ui(a, e))])
end
for n in BigInt[0, 1, 5, 10, 50, 100, -1, -5, -30, big(2)^64 + 1, big(10)^30],
    k in UInt64[0, 1, 2, 3, 10, 25, 64, 100]
    emit("bin_ui", [zin(n), uin(k)], () -> [raw(MPZ.bin_ui(n, k))])
end
for n in UInt64[0, 1, 2, 3, 10, 20, 21, 25, 50, 100, 500]
    emit("fac_ui", [uin(n)], () -> [raw(MPZ.fac_ui(n))])
end
for u in U
    emit("set_ui", [uin(u)], () -> [raw(MPZ.set_ui(u))])
end
for s in S
    emit("set_si", [iin(s)], () -> [raw(MPZ.set_si(s))])
end
for d in D
    emit("set_d", [fin(d)], () -> [raw(MPZ.set_d(d))])
    for a in V
        isnan(d) || emit("cmp_d", [zin(a), fin(d)], () -> [iout(sign(MPZ.cmp_d(a, d)))])
    end
end

# unary operations and queries
for a in V
    emit("neg", [zin(a)], () -> [raw(MPZ.neg(a))])
    emit("com", [zin(a)], () -> [raw(MPZ.com(a))])
    emit("sqrt", [zin(a)], () -> [raw(MPZ.sqrt(a))])
    emit("set", [zin(a)], () -> [raw(MPZ.set(a))])
    emit("get_d", [zin(a)], () -> [fout(MPZ.get_d(a))])
    emit("popcount", [zin(a)], () -> [iout(signed(MPZ.popcount(a)))])
    a == 0 || emit("mpn_popcount", [zin(a)], () -> [iout(MPZ.mpn_popcount(abs(a)))])
    for base in 2:62
        emit("sizeinbase", [zin(a), cin(base)], () -> [iout(MPZ.sizeinbase(a, base))])
    end
    for st in UInt64[0, 1, 63, 64, 65, 130, 500, 18446744073709551615]
        emit("scan0", [zin(a), uin(st)], () -> [iout(MPZ.scan0(a, st))])
        emit("scan1", [zin(a), uin(st)], () -> [iout(MPZ.scan1(a, st))])
    end
    for bit in UInt64[0, 1, 2, 63, 64, 65, 200, 1000, 18446744073709551615]
        emit("tstbit", [zin(a), uin(bit)], () -> [iout(Int(MPZ.tstbit(a, bit)))])
    end
    for bit in UInt64[0, 1, 63, 64, 200, 1000]
        emit("setbit", [zin(a), uin(bit)], () -> (x = MPZ.set(a); MPZ.setbit!(x, bit); [raw(x)]))
    end
end
for a in V, b in V
    if a.size != 0 && b.size != 0 && abs(a.size) == abs(b.size)
        emit("mpn_cmp", [zin(a), zin(b)], () -> [iout(sign(MPZ.mpn_cmp(abs(a), abs(b), abs(a.size))))])
    end
end

# text
function getstr(a, base)
    buf = zeros(UInt8, Int(MPZ.sizeinbase(a, abs(base))) + 3)
    GC.@preserve buf MPZ.get_str!(Ptr{Cchar}(pointer(buf)), base, a)
    String(buf[1:findfirst(==(0), buf)-1])
end
for a in V, base in (2, 3, 7, 10, 16, 36, 37, 61, 62, -2, -10, -16, -36)
    emit("get_str", [zin(a), cin(base)], () -> ["S:" * escape_string(getstr(a, base))])
end
STRS = ["0", "1", "-1", "  12  ", "12 34", "1_0", "+5", "", " ", "-", "--1", "0x1f", "0X1F", "0b101", "0B11",
        "017", "08", "zz", "ZZ", "Zz", "aB", "1e3", "9999999999999999999999999999999999999", "-000123", "0000",
        "\t\n 7 \n", "0x", "-0x10", "  -  5", "ff", "FF", "Gg", "-z", "7f", "00x1"]
for s in STRS, base in (10, 16, 2, 8, 36, 37, 62, 0, 1, 63)
    emit("set_str", [sstr(s), cin(base)], () -> (x = BigInt(); rc = MPZ.set_str!(x, s, base);
                                                 rc == 0 ? [iout(rc), raw(x)] : [iout(rc)]))
end

# export
for a in V, T in (UInt8, UInt16, Int32, UInt64), order in (-1, 1), endian in (-1, 0, 1), nails in (0, 1, 8sizeof(T) - 3)
    emit("export", [zin(a), uin(sizeof(T)), cin(order), cin(endian), uin(nails)], () -> begin
        v = zeros(T, 0)
        v, cnt = MPZ.export!(v, a; order, nails, endian)
        [iout(cnt), "H:" * join((string(reinterpret(unsigned(T), x), base=16) for x in v[1:cnt]), ",")]
    end)
end

# rationals
for a in W, b in W
    b == 0 && continue
    emit("q_canon", [zin(a), zin(b)], () -> (r = Rational{BigInt}(a, b); [raw(r.num), raw(r.den)]))
end
QV = [Rational{BigInt}(a, b) for (a, b) in ((0, 1), (1, 3), (-5, 7), (22, 7), (big(2)^64, 3), (-big(10)^30, 11), (7, 1))]
for x in QV, y in QV
    for (n, f) in (("q_add", +), ("q_sub", -), ("q_mul", *), ("q_div", //))
        # MPQ.div of a zero divisor is julia's, not GMP's
        n == "q_div" && y.num == 0 && continue
        emit(n, [zin(x.num), zin(x.den), zin(y.num), zin(y.den)], () -> (r = f(x, y); [raw(r.num), raw(r.den)]))
    end
    emit("q_cmp", [zin(x.num), zin(x.den), zin(y.num), zin(y.den)], () -> [iout(sign(Base.cmp(x, y)))])
end
