# julia 1.12.6's answers for interp/irflags_test.go: the jl_ir_* accessors
# of a lowered CodeInfo, a TypeName's field masks, jl_argument_datatype,
# and code locations compressed then read back one statement at a time.
f(x) = x + 1
ci = Base.code_lowered(f)[1]
println("inlining ", ccall(:jl_ir_flag_inlining, UInt8, (Any,), ci))
println("has_fcall ", ccall(:jl_ir_flag_has_fcall, UInt8, (Any,), ci))
println("has_image_globalref ", ccall(:jl_ir_flag_has_image_globalref, UInt8, (Any,), ci))
println("inlining_cost ", ccall(:jl_ir_inlining_cost, UInt16, (Any,), ci))
println("nslots ", ccall(:jl_ir_nslots, Int, (Any,), ci))
println("slotflags ", [ccall(:jl_ir_slotflag, UInt8, (Any, Csize_t), ci, i) for i in 0:1])
println("atomicfields Binding ", unsafe_load(Ptr{UInt32}(Core.Binding.name.atomicfields)))
println("constfields Binding ", unsafe_load(Ptr{UInt32}(Core.Binding.name.constfields)))
println("atomicfields Int ", Int.name.atomicfields === C_NULL)
ad(t) = ccall(:jl_argument_datatype, Any, (Any,), t)
for t in (Int, Vector{T} where T, Union{Vector{Int}, Vector{Float64}}, Union{Int, String},
          (T where T<:Integer), Union{Int, Vector{Int}}, Union{})
    println("argument_datatype ", t, " => ", ad(t))
end
for (first, locs) in ((3, Int32[5, 0, 0, 6, 1, 2, 300, 0, 0]),
                      (1, Int32[70000, 300, 70000, 2, 1, 1]),
                      (10, Int32[11, 0, 0, 12, 0, 0]))
    n = length(locs) ÷ 3
    cl = ccall(:jl_compress_codelocs, Any, (Int32, Any, Int), first, locs, n)::String
    println("codelocs ", first, " ", locs, " => ", codeunits(cl), " ",
            [ccall(:jl_uncompress1_codeloc, NTuple{3,Int32}, (Any, Int), cl, pc) for pc in 0:n+1])
end
