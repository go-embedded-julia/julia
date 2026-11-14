# jl_gf_invoke_lookup_worlds (gf.c:4217-4254) and jl_method_table_disable
# (gf.c:2585-2606), as which and delete_method call them.
g(x::Int) = 1
g(x::Integer) = 2
g(x) = 3
amb(x::Int, y) = 1
amb(x, y::Int) = 2
const W = Base.get_world_counter()
lookup(tt) = (r = ccall(:jl_gf_invoke_lookup_worlds, Any, (Any, Any, UInt, Ptr{Csize_t}, Ptr{Csize_t}),
        tt, nothing, W, Ref{UInt}(0), Ref{UInt}(typemax(UInt))); r === nothing ? nothing : (r.spec_types, r.sparams, r.method.sig, r.fully_covers))
for (name, tt) in ["g Int" => Tuple{typeof(g), Int}, "g Int8" => Tuple{typeof(g), Int8},
        "g Any" => Tuple{typeof(g), Any}, "g all" => Tuple{typeof(g), Vararg{Any}},
        "g two" => Tuple{typeof(g), Int, Int}, "amb Int Int" => Tuple{typeof(amb), Int, Int},
        "amb Int Any" => Tuple{typeof(amb), Int, Any}, "not a tuple" => Int]
    println(name, " => ", repr(lookup(tt)))
end
m = which(g, (Int8,))
Base.delete_method(m)
println("after delete => ", repr(g(Int8(1))))
try
    Base.delete_method(m)
catch e
    println("again => !", repr(e))
end
println(repr(gensym("x"))[1:5], " ", repr(gensym())[1:4], " ", repr(gensym(:y))[1:5])
try; gensym("a\0b"); catch e; println("nul => !", repr(e)); end
