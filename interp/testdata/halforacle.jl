# The 16-bit float conversions, hashed by chunks: julia 1.12.6 runs this
# (JLGO_ORACLE=1 go test ./interp -run TestHalfAgainstJulia) and its output
# is testdata/half.golden; halforacle_test.go computes the same lines.
# Each line: section chunk hash. A hash folds UInt64s: h = (h ⊻ x) * FNV.

const FNV = 0x00000100000001b3
fold(h::UInt64, x::Integer) = (h ⊻ (x % UInt64)) * FNV
fold(h::UInt64, x::UInt128) = fold(fold(h, (x >> 64) % UInt64), x % UInt64)
fold(h::UInt64, s::String) = fold(foldl(fold, codeunits(s); init = h), 0xff)
lcg(s::UInt64) = s * 0x5851f42d4c957f2d + 0x14057b7ef767814f

const CHUNK = 65536
function emit(name, xs, f)
    h = UInt64(0)
    for (i, x) in enumerate(xs)
        h = fold(h, f(x))
        if i % CHUNK == 0 || i == length(xs)
            println(name, " ", (i - 1) ÷ CHUNK, " ", string(h, base = 16))
            h = UInt64(0)
        end
    end
end

halves = [reinterpret(Float16, UInt16(i)) for i in 0:0xffff]
emit("show", halves, repr)
emit("showelem", halves, x -> sprint(show, x; context = :typeinfo => Float16))
emit("widen", halves, x -> reinterpret(UInt32, Float32(x)))

# Float32 to Float16 and BFloat16: every top 19 bits with low-bit patterns
# around the ties of both
f32s = Float32[]
for k in UInt32(0):UInt32(0x7ffff), p in (0x0000, 0x0001, 0x0fff, 0x1000, 0x1001, 0x1fff)
    push!(f32s, reinterpret(Float32, k << 13 | p))
end
emit("f32tohalf", f32s, x -> reinterpret(UInt16, Float16(x)))
# LLVM 18 cannot select a BFloat16 fptrunc on arm64: the runtime's, called
bf16(x) = reinterpret(UInt16, ccall(:jl_fptrunc, Any, (Any, Any), Core.BFloat16, x)::Core.BFloat16)
emit("f32tobf16", f32s, bf16)

# Float64 to Float16 and BFloat16: exponents about the halves' range, random
# significands, and significands on a tie or one ulp past it
f64s = Float64[]
let s = UInt64(1)
    for i in 1:1_000_000
        s = lcg(s); m = s & 0x000fffffffffffff
        s = lcg(s); e = UInt64(1023 - 30 + (s >> 33) % 50); neg = (s >> 63) << 63
        tie = (i % 2 == 0) ? ((m & ~UInt64(2^42 - 1)) | UInt64(2)^41 | ((s >> 20) & 1)) :
                             ((m & ~UInt64(2^29 - 1)) | UInt64(2)^28 | ((s >> 20) & 1))
        push!(f64s, reinterpret(Float64, neg | e << 52 | m))
        push!(f64s, reinterpret(Float64, neg | e << 52 | tie))
    end
end
emit("f64tohalf", f64s, x -> reinterpret(UInt16, Float16(x)))
emit("f64tobf16", f64s, bf16)

# the runtime's 128-bit conversions, one top-level call each so that the C
# intrinsics answer and not LLVM's
ints = UInt128[]
let s = UInt64(7)
    for i in 1:400
        s = lcg(s); hi = s
        s = lcg(s); lo = s
        n = (s >> 40) % 129
        push!(ints, n == 0 ? UInt128(0) : (UInt128(hi) << 64 | lo) >> (128 - n))
    end
end
rt(f, T, x) = Core.eval(Main, Expr(:call, f, T, x))
emit("sitofp", ints, x -> reinterpret(UInt64, rt(Core.Intrinsics.sitofp, Float64, reinterpret(Int128, x))))
emit("uitofp", ints, x -> reinterpret(UInt64, rt(Core.Intrinsics.uitofp, Float64, x)))
emit("uitofp32", ints, x -> reinterpret(UInt32, rt(Core.Intrinsics.uitofp, Float32, x)))
dbls = Float64[]
let s = UInt64(11)
    for i in 1:400
        s = lcg(s)
        push!(dbls, reinterpret(Float64, (s & 0x800fffffffffffff) | UInt64(1023 - 4 + (s >> 40) % 135) << 52))
    end
end
emit("fptosi", dbls, x -> reinterpret(UInt128, rt(Core.Intrinsics.fptosi, Int128, x)))
emit("fptoui", dbls, x -> rt(Core.Intrinsics.fptoui, UInt128, x))
