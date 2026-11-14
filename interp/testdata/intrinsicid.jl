# IntrinsicFunction as 32-bit values (intrinsics.h's enum): each intrinsic's
# number, and what an out-of-range number gives.
for f in (Core.Intrinsics.bitcast, Core.Intrinsics.neg_float_fast, Core.Intrinsics.fpiseq,
        Core.Intrinsics.have_fma, Core.Intrinsics.llvmcall)
    println(repr(f), " => ", reinterpret(Int32, f))
end
println(isprimitivetype(Core.IntrinsicFunction), " ", sizeof(Core.IntrinsicFunction), " ",
    supertype(Core.IntrinsicFunction))
println(repr(reinterpret(Core.IntrinsicFunction, Int32(1))))
x = reinterpret(Core.IntrinsicFunction, Int32(200))
println(typeof(x))
try
    println(repr(x))
catch e
    println("repr !", repr(e))
end
try
    x(1)
catch e
    println("call !", repr(e))
end
