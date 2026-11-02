# DataType.instance (datatype.c:346-359, 617-626): `T => repr(instance)`
# or `T => #undef`.
struct Z end
struct ZZ; a::Nothing; b::Z; end
struct NZ; x::Int; end
mutable struct MZ end
f() = 1
for T in Any[Nothing, typeof(Union{}), Tuple{}, Z, ZZ, NZ, MZ, Int64, Any, Integer, typeof(f),
        typeof(Core.tuple), Memory{Int}, GenericMemory{:atomic, Int, Core.CPU}, Tuple{Nothing},
        Val{1}, Core.IntrinsicFunction, Tuple{Int}]
    print(repr(T), " => ")
    println(isdefined(T, :instance) ? repr(getfield(T, :instance)) : "#undef")
end
println(Z() === getfield(Z, :instance), " ", NZ(1) === NZ(1))
