# Base.allocatedinline (jl_stored_inline) of the cases strbuf.go decides,
# printed one per line as `repr(T) => answer`.
struct SelfRef; x::Union{Nothing, SelfRef}; end
struct HasPtr; x::Int; s::String; end
struct Uninit; x::String; Uninit() = new(); end
mutable struct Mut; x::Int; end
for T in Any[UInt8, Int64, Float64, Bool, Nothing, Char, Ptr{UInt8}, Some{Int}, Tuple{Int, Float64},
        Tuple{}, Union{Int8, UInt8}, Union{Nothing, Int}, Union{Int, String}, Union{}, Any,
        Integer, String, Symbol, Mut, DataType, Vector{Int}, Memory{Int},
        Some{String}, Tuple{Int, String}, HasPtr, SelfRef, Uninit, Union{Nothing, HasPtr},
        Tuple{Any}, Some, Vector, Type{Int}, NamedTuple{(:a,), Tuple{Int}}, NamedTuple{(:a,), Tuple{String}}]
    println(repr(T), " => ", ccall(:jl_stored_inline, Cint, (Any,), T))
end
