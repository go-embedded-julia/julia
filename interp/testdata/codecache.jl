# The code cache (gf.c:636-768, 3150-3170; engine.cpp:60-149), the module
# compiler options (module.c:675-741, interpreter.c:657-686), a method's
# source as a CodeInfo (jltypes.c:3483-3535), and bindings as inference
# reads them (runtime_internals.jl:202-257).
module MO; module Inner end; end
opts(m) = (ccall(:jl_get_module_optlevel, Cint, (Any,), m), ccall(:jl_get_module_compile, Cint, (Any,), m),
    ccall(:jl_get_module_infer, Cint, (Any,), m), ccall(:jl_get_module_max_methods, Cint, (Any,), m))
println("defaults => ", opts(MO.Inner))
Core.eval(MO, Expr(:meta, :optlevel, 1))
Core.eval(MO, Expr(:meta, :compile, 0))
Core.eval(MO, Expr(:meta, :infer, 0))
Core.eval(MO, Expr(:meta, :max_methods, 2))
println("set => ", opts(MO.Inner))
println("asserts => ", ccall(:jl_is_assertsbuild, Cchar, ()))

f(x) = x + 1
m = which(f, (Int,))
mi = ccall(:jl_specializations_get_linfo, Ref{Core.MethodInstance}, (Any, Any, Any), m, Tuple{typeof(f), Int}, Core.svec())
W = Base.get_world_counter()
inferred(owner, lo, hi) = ccall(:jl_rettype_inferred, Any, (Any, Any, UInt, UInt), owner, mi, lo, hi)
println("empty => ", repr(inferred(nothing, W, W)))
ci = Core.CodeInstance(mi, :me, Int, Union{}, 2, nothing, Int32(2), UInt(5), typemax(UInt), UInt32(7), nothing, nothing, Core.svec())
ccall(:jl_mi_cache_insert, Cvoid, (Any, Any), mi, ci)
println("owner nothing => ", repr(inferred(nothing, W, W)))
println("owner me => ", inferred(:me, W, W) === ci)
println("too old => ", repr(inferred(:me, UInt(4), W)))
println("fields => ", (ci.min_world, ci.max_world, ci.rettype, ci.exctype, ci.rettype_const, ci.ipo_purity_bits, isdefined(ci, :next), isdefined(ci, :inferred)))
r = ccall(:jl_engine_reserve, Any, (Any, Any), mi, :other)
println("reserved => ", (r.min_world, r.max_world, isdefined(r, :inferred), isdefined(r, :rettype)))
ccall(:jl_fill_codeinst, Cvoid, (Any, Any, Any, Any, Int32, UInt, UInt, UInt32, Any, Any, Any),
    r, Int, Union{}, nothing, Int32(0), UInt(3), typemax(UInt), UInt32(1), nothing, nothing, Core.svec())
println("filled => ", (r.min_world, r.rettype, r.inferred, isdefined(r, :rettype_const)))
ccall(:jl_engine_fulfill, Cvoid, (Any, Ptr{Cvoid}), r, C_NULL)

src = m.source isa String ? Base.uncompressed_ir(m) : copy(m.source)
println("source => ", (typeof(src.code), src.slotnames, src.slotflags, src.ssaflags, src.ssavaluetypes, src.nargs, src.isva, src.edges, src.min_world, src.inlining_cost))

module B
const c = 1
g = 2
global d
global t::Int = 3
using Base: sin
import Base: cos
export e
end
for s in (:c, :g, :d, :t, :sin, :cos, :println, :nope)
    bp = Base.lookup_binding_partition(Base.get_world_counter(), GlobalRef(B, s))
    k = Base.binding_kind(bp)
    println(s, " => ", (k, bp.max_world == typemax(UInt), k in (0x0, 0x2, 0x4) ? Base.partition_restriction(bp) : nothing))
end
