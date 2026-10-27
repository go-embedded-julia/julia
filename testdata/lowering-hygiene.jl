# Forms that reach Meta.lower still wrapped in Expr(:hygienic-scope): a
# macro whose result is an Expr(:toplevel) has its arguments re-wrapped by
# macroexpand.scm (resolve-expansion-vars-, the toplevel case), and lowering
# resolves them (julia-expand-macroscope). TestHygieneGolden runs this file
# in the oracle; it prints one `F\tinput\toutput` line (base64) per form.
module HygM
macro tl(ex)
    Expr(:toplevel, ex)
end
macro tlesc(ex, v)
    Expr(:toplevel, :(z = $(esc(v)) + w; $ex))
end
# Macro calls left inside a toplevel argument are expanded by Meta.lower
# itself (jl_expand_macros), which runs user code: the cases have none.
# Nested scopes come from a macro whose expansion calls @tl.
macro outer(ex)
    :(@HygM.tl (tmp = 1; $ex; tmp2 = tmp))
end
macro outeresc(ex)
    :(@HygM.tl (tmp = 1; $(esc(ex)); tmp2 = tmp))
end
macro escaped(name)
    Expr(:toplevel, :(function $(esc(name))(x) y = x; y end))
end
macro withlabel()
    Expr(:toplevel, :(function lab(n)
        $(Expr(:symboliclabel, :top))
        n -= 1
        n > 0 && $(Expr(:symbolicgoto, :top))
        n
    end))
end
macro escgoto()
    Expr(:toplevel, :(function lab2(n)
        $(Expr(:symboliclabel, :top))
        n -= 1
        $(esc(Expr(:symbolicgoto, :top)))
    end))
end
end

const HYGIENE_CASES = [
    :(@HygM.tl x = 1),
    :(@HygM.tl global g = 2),
    :(@HygM.tl begin local q = 1; q end),
    :(@HygM.tl const C = 1),
    :(@HygM.tl x::Int = 3),
    :(@HygM.tl function f(x; k=1) y = x + k; for i in 1:2; y += i; end; y end),
    :(@HygM.tl f(x::Int, ::Float64, args...; kw...) = x),
    :(@HygM.tl g(x::T) where {T<:Real} = x),
    :(@HygM.tl h()::Int = 1),
    :(@HygM.tl k(; a::Int=1, b) = a + b),
    :(@HygM.tl ((a, b) -> a + b)),
    :(@HygM.tl (x -> x)),
    :(@HygM.tl function (::Vector)(x) x end),
    :(@HygM.tl function (s::Base.Some)(x) s end),
    :(@HygM.tl function fw(x::T, y::S) where {T, S<:T} x end),
    :(@HygM.tl function fc{T}(x) x end),
    :(@HygM.tl struct S{T} a::T; b; const c::Int; S(a) = new{typeof(a)}(a, 1, 2) end),
    :(@HygM.tl mutable struct MS; const n::Int; end),
    :(@HygM.tl abstract type A{T} <: Number end),
    :(@HygM.tl primitive type P8 8 end),
    :(@HygM.tl let a = 1, b, c::Int = 2, (d, e) = (3, 4), fl() = 5; a + c + d + fl() end),
    :(@HygM.tl [i * j for i in 1:3, j in 1:2 if i > j]),
    :(@HygM.tl (i for i in 1:3)),
    :(@HygM.tl(for (i, j) in zip(1:2, 3:4) println(i, j) end)),
    :(@HygM.tl(for i = 1:2, j = 1:i println(i, j) end)),
    :(@HygM.tl try error("x") catch err; err end),
    :(@HygM.tl try error("x") catch; 1 finally 2 end),
    :(@HygM.tl (p, q) = (1, 2)),
    :(@HygM.tl (; aa, bb) = (aa=1, bb=2)),
    :(@HygM.tl quote $x + 1 end),
    :(@HygM.tl ccall(:jl_typeof_str, Ptr{UInt8}, (Any,), 1)),
    :(@HygM.tl begin; import Base: sin; sin(1); end),
    :(@HygM.tl (t = (a=1, b=2); t.a)),
    :(@HygM.tl x -> begin; local y = x; y; end),
    :(@HygM.tl _ = 3),
    :(@HygM.tl (u = 1; begin; global u2 = u; end)),
    :(@HygM.tl macro mm(x) x end),
    :(@HygM.tl f2(x) = (y = x; z(w) = w + y; z(1))),
    :(@HygM.tl(while true break end)),
    :(@HygM.tl $(Expr(:inbounds, true))),
    :(@HygM.tl (x -> y) where T),
    :(@HygM.tlesc(q + 1, a)),
    :(@HygM.outer tmp + 3),
    :(@HygM.outeresc tmp + 3),
    :(@HygM.outer function ff(a) tmp + a end),
    :(@HygM.escaped myf),
    :(@HygM.withlabel),
    :(@HygM.escgoto),
]

hasmacro(x) = x isa Expr && (x.head === :macrocall || any(hasmacro, x.args))

let lm = Module(:L, false, false), io = IOBuffer()
    for c in HYGIENE_CASES
        x = macroexpand(@__MODULE__, c)
        args = x isa Expr && x.head === :toplevel ? x.args : Any[x]
        for a in args
            a isa LineNumberNode && continue
            hasmacro(a) && error("a macro call is left in $a")
            out = Meta.lower(lm, a)
            println(io, "F\t", Main.base64encode(Main.serialize_expr(a)), "\t", Main.base64encode(Main.serialize_expr(out)))
        end
    end
    print(String(take!(io)))
end
nothing
