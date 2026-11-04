# julia 1.12.6's jl_compress_ir of more lowered code, for interp/ircode_test.go
# (TestCompressIRMoreAgainstJulia): the literals that are written as raw
# memory (TAG_GENERAL), integers of each width, GlobalRefs, calls with more
# than 255 arguments, statements numbered past 255. As ircode.jl: each
# method's lowered CodeInfo compressed, as hex, then the method's roots.
# Run with
#   pkgx julia@1.12.6 --startup-file=no interp/testdata/ircode2.jl > interp/testdata/ircode2.golden
module L
hg() = 1
h1(x) = tuple(x, 0x0001, 0x00000002, 0x0000000000000003, 1.5f0, -300, 32768, 2147483648, -2147483649, 'é', 0.1)
h2() = tuple(1, 2)
h6(x) = (Int, Float64, Vector, Tuple, Array, nothing, x)
h7(x) = (:a, :b, "s", "s", :a)
h8(x) = (x; tuple(x, 1.5, 1.5))
h9(x, y) = x === y ? (1, 2.5) : (y, :sym)
h10() = (Int8(1), 2)
end
Core.eval(L, Meta.parse("h3() = tuple(" * join(1:300, ", ") * ")"))
Core.eval(L, Meta.parse("h4() = tuple(" * join(fill("hg()", 600), ", ") * ")"))
Core.eval(L, Meta.parse("h5(x) = (" * join(["x = hg(); " for _ in 1:3], "") * "x)"))
for f in (L.h1, L.h2, L.h3, L.h4, L.h5, L.h6, L.h7, L.h8, L.h9, L.h10)
    m = first(methods(f))
    ci = Base.uncompressed_ir(m)
    s = ccall(:jl_compress_ir, Any, (Any, Any), m, ci)::String
    roots = isdefined(m, :roots) ? m.roots : Any[]
    println(nameof(f), " ", bytes2hex(codeunits(s)), " ", repr(roots))
end
