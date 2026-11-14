# jl_matching_methods (gf.c:3113-3127): each query printed as its
# MethodMatches, in order, as (spec_types, sparams, method's sig,
# fully_covers), or `nothing`.
ROOT() = nothing
g(x::Int) = 1
g(x::Integer) = 2
g(x) = 3
h(x::T, y::T) where {T} = 1
k(x::String) = 1
m2(x::Vector{T}) where {T<:Real} = T
const W = Base.get_world_counter()
show_matches(r) = r === nothing ? "nothing" :
    "[" * join(map(x -> repr((x.spec_types, x.sparams, x.method.sig, x.fully_covers)), r), ", ") * "]"
for (name, tt, lim) in [
        ("ROOT all", Tuple{typeof(ROOT), Vararg{Any}}, -1),
        ("g all", Tuple{typeof(g), Vararg{Any}}, -1),
        ("g Int", Tuple{typeof(g), Int}, -1),
        ("g Int8", Tuple{typeof(g), Int8}, -1),
        ("g String", Tuple{typeof(g), String}, -1),
        ("g Int lim 1", Tuple{typeof(g), Int}, 1),
        ("g all lim 1", Tuple{typeof(g), Vararg{Any}}, 1),
        ("g all lim 0", Tuple{typeof(g), Vararg{Any}}, 0),
        ("g none", Tuple{typeof(g), Int, Int}, -1),
        ("h all", Tuple{typeof(h), Vararg{Any}}, -1),
        ("h Int Int", Tuple{typeof(h), Int, Int}, -1),
        ("k Int", Tuple{typeof(k), Int}, -1),
        ("m2 all", Tuple{typeof(m2), Vararg{Any}}, -1),
        ("m2 Int", Tuple{typeof(m2), Vector{Int}}, -1),
        # intersections neither side of which is a subtype of the other
        ("h Int Any", Tuple{typeof(h), Int, Any}, -1),
        ("g Real", Tuple{typeof(g), Real}, -1),
        ("m2 AbstractVector", Tuple{typeof(m2), AbstractVector}, -1),
        ("m2 Vector Number", Tuple{typeof(m2), Vector{<:Number}}, -1),
        ("empty tuple", Tuple{}, -1),
        ("not a tuple", Int, -1),
    ]
    println(name, " => ", show_matches(Base._methods_by_ftype(tt, lim, W)))
end
