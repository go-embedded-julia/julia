# julia 1.12.6's jl_widen_diagonal of each line of widen-cases.jl, for
# TestWidenDiagonalCases, in the structural form ser prints (variables by
# order of first appearance, with their bounds). Run from the repository:
#   pkgx julia@1.12.6 --startup-file=no core/testdata/widen.jl > core/testdata/widen.golden
function ser(x, seen=IdDict{TypeVar,Int}())
    if x === Union{}
        return "Bot"
    elseif x isa TypeVar
        haskey(seen, x) && return "v$(seen[x])"
        i = length(seen)
        seen[x] = i
        return "V$(i)[$(ser(x.lb, seen)),$(ser(x.ub, seen))]"
    elseif x isa UnionAll
        return "U($(ser(x.var, seen)),$(ser(x.body, seen)))"
    elseif x isa Union
        return "Un($(ser(x.a, seen)),$(ser(x.b, seen)))"
    elseif x isa Core.TypeofVararg
        return "Va($(isdefined(x, :T) ? ser(x.T, seen) : "-"),$(isdefined(x, :N) ? ser(x.N, seen) : "-"))"
    elseif x isa DataType
        return string(x.name.name, "{", join([ser(p, seen) for p in x.parameters], ","), "}")
    end
    return repr(x)
end

widen(ua) = ser(ccall(:jl_widen_diagonal, Any, (Any, Any), Base.unwrap_unionall(ua), ua))

# types no source writes: a variable bound again inside the body (the one
# that is troot's, and one deeper in its chain), and a free variable
let T = TypeVar(:T), S = TypeVar(:S), R = TypeVar(:R), F = TypeVar(:F, T)
    println("#shadow2\t", widen(UnionAll(S, UnionAll(T, Tuple{T,UnionAll(S, Ref{S}),T,S}))))
    println("#shadow3\t", widen(UnionAll(R, UnionAll(S, UnionAll(T, Tuple{T,UnionAll(R, Ref{R}),T,R,S})))))
    println("#unused\t", widen(UnionAll(T, Tuple{T,T,UnionAll(S, Tuple{T,T})})))
    println("#free\t", widen(UnionAll(T, Tuple{T,T,F})))
end

for line in eachline(joinpath(@__DIR__, "widen-cases.jl"))
    (isempty(strip(line)) || startswith(line, "#")) && continue
    out = try
        ua = eval(Meta.parse(line))
        ser(ccall(:jl_widen_diagonal, Any, (Any, Any), Base.unwrap_unionall(ua), ua))
    catch
        "E"
    end
    println(line, "\t", out)
end
