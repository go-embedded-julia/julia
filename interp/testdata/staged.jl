# Staged functions (@generated) and what Base builds on them (issue #126).
# Each line is one cell. Regenerate staged.golden with real julia:
#     pkgx julia@1.12.6 staged.jl > staged.golden
using SHA, Random
@generated function gen1(x) :(x + 1) end
println("gen1 ", gen1(1), " ", gen1(1.5))
@generated function gen2(x, ys...) :(($x, $(length(ys)))) end
println("gen2 ", gen2(1, 2, 3), " ", gen2("a"))
@generated function gen3(x::T) where T; :($T) end
println("gen3 ", gen3(1), " ", gen3(0x01))
@generated function gen4(::Val{N}) where N; :(N * 2) end
println("gen4 ", gen4(Val(21)))
function gen5(x) if @generated; :(x + 1) else x + 1 end end
println("gen5 ", gen5(1))
@generated gen6(x) = :(typeof(x))
println("gen6 ", gen6(1), " ", gen6("s"))
const RUNS = Ref(0)
@generated function gen7(x) RUNS[] += 1; :(x) end
gen7(1); gen7(2); gen7(3); gen7(1.0); gen7(2.0)
println("gen7 ", RUNS[])
println("sha256 ", bytes2hex(sha256("abc")))
println("sha256 empty ", bytes2hex(sha256("")))
println("sha256 448 bits ", bytes2hex(sha256("abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq")))
println("sha256 1000000 a ", bytes2hex(sha256(repeat("a", 1000000))))
println("sha512 ", bytes2hex(sha512("abc")))
println("sha1 ", bytes2hex(sha1("abc")))
println("seed 42 rand ", rand(Xoshiro(42), 3))
println("seed 0 rand ", rand(Xoshiro(0), 3))
println("seed 1 UInt64 ", rand(Xoshiro(1), UInt64, 2))
Random.seed!(42); println("seed! 42 rand ", rand(3))
Random.seed!(42); println("seed! 42 bulk ", rand(1000)[500], " ", rand(1000)[end])
println("seed tuple ", rand(Xoshiro(0x01), Int8, 4))
