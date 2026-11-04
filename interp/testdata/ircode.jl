# julia 1.12.6's jl_compress_ir of lowered code, for interp/ircode_test.go:
# each method's lowered CodeInfo compressed, as hex, then the method's
# roots. The methods are defined in a module made at run time, as the
# test's session module is (has_image_globalref is about such modules),
# with names lowering treats alike with or without Base.
module L
f1(x) = x
f2(x, y) = tuple(x, y, 1.5, :sym, "str", 'c', 0x07, 300, 70000, 5000000000, nothing, true, -2)
f3(x) = x === nothing ? 1 : 2
f4(x) = try; x; catch; 0; end
f5(x::T) where {T} = T
f6() = :(a + b)
f7(x) = (y = x; while y === 1; y = 2; end; y)
f8(xs...) = xs
f9(x) = typeof(x)
f10(x) = Core.Typeof(x)
end
for f in (L.f1, L.f2, L.f3, L.f4, L.f5, L.f6, L.f7, L.f8, L.f9, L.f10)
    m = first(methods(f))
    ci = Base.uncompressed_ir(m)
    s = ccall(:jl_compress_ir, Any, (Any, Any), m, ci)::String
    roots = isdefined(m, :roots) ? m.roots : Any[]
    println(nameof(f), " ", bytes2hex(codeunits(s)), " ", repr(roots))
end
