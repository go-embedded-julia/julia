x = 1
#----
x
#----
1
#----
"str"
#----
:sym
#----
nothing
#----
begin
    a = 1
    b = a + 2
end
#----
global g = 3
#----
const c = 4
#----
local l = 5
#----
x::Int = 6
#----
a, b = 1, 2
#----
(a, b) = f()
#----
a, b... = t
#----
x += 1
#----
x .+= 1
#----
y = x .+ 1
#----
f.(x, y)
#----
a[1] = 2
#----
a[i, j] += 1
#----
a[end]
#----
s.f = 3
#----
s.f
#----
f(x) = x + 1
#----
function f(x, y)
    return x * y
end
#----
function f(x::Int, y::T) where {T<:Real}
    x + y
end
#----
function f(x, y=2, z=3)
    x + y + z
end
#----
function f(x; k=1, kw...)
    x + k
end
#----
function f(args...)
    length(args)
end
#----
function f end
#----
f(::Type{T}) where T = T
#----
(x -> x + 1)(2)
#----
g = x -> 2x
#----
function outer(n)
    inner(x) = x + n
    inner(1)
end
#----
function counter()
    c = 0
    () -> (c += 1)
end
#----
map(xs) do x
    x * 2
end
#----
if x > 0
    1
elseif x < 0
    -1
else
    0
end
#----
x > 0 ? 1 : 2
#----
x && y
#----
x || y
#----
!x
#----
while i < 10
    i += 1
end
#----
for i in 1:10
    println(i)
end
#----
for i in 1:3, j in 1:2
    s += i * j
end
#----
for i in xs
    i > 3 && break
    i == 2 && continue
end
#----
let a = 1, b = 2
    a + b
end
#----
let
    local z = 1
    z
end
#----
try
    f()
catch e
    g(e)
end
#----
try
    f()
finally
    cleanup()
end
#----
try
    f()
catch
    rethrow()
else
    ok()
finally
    done()
end
#----
[1, 2, 3]
#----
[1 2; 3 4]
#----
[1 2 3]
#----
Int[1, 2]
#----
(1, 2)
#----
(a=1, b=2)
#----
[x^2 for x in 1:10]
#----
[x for x in xs if x > 0]
#----
(x for x in xs)
#----
Dict(k => v for (k, v) in pairs)
#----
sum(x for x in 1:10)
#----
"a$(x)b"
#----
"$x and $y"
#----
struct Point
    x::Float64
    y::Float64
end
#----
mutable struct Box{T}
    v::T
end
#----
struct S{T<:Number}
    a::T
    S(a) = new{typeof(a)}(a)
end
#----
abstract type Shape end
#----
abstract type Sub <: Shape end
#----
primitive type U24 24 end
#----
f(x...)
#----
f(x; y=1)
#----
f(; kw...)
#----
f(a, b...; c...)
#----
x::Int
#----
Vector{Int}
#----
Union{Int, Nothing}
#----
T where T
#----
quote
    x + 1
end
#----
:(a + $b)
#----
@inline f(x) = x
#----
@noinline function h(x)
    x
end
#----
@inbounds a[1]
#----
@assert x > 0
#----
@show x
#----
@time f()
#----
@eval f() = 1
#----
@generated function gen(x)
    :(x + 1)
end
#----
"doc string"
function documented(x)
    x
end
#----
ccall(:strlen, Csize_t, (Cstring,), s)
#----
@ccall strlen(s::Cstring)::Csize_t
#----
macro m(x)
    esc(x)
end
#----
module Inner
    f() = 1
end
#----
using Base
#----
import Base: show
#----
export f
#----
x = y = 3
#----
f(x)::Int = x
#----
function (p::Point)(z)
    p.x + z
end
#----
Base.show(io::IO, p::Point) = print(io, p.x)
#----
a.b.c = 1
#----
x[2:end]
#----
x'
#----
a .= b .* c
#----
@. a = b + c
#----
z = 1:2:10
#----
x isa Int
#----
x === nothing
#----
begin
    local i = 0
    @label top
    i += 1
    i < 3 && @goto top
end
#----
function f(x)
    @isdefined(y) || return x
    y
end
#----
global function gf(x) x end
#----
const C1, C2 = 1, 2
#----
function f(x)
    y = x
    function g()
        y += 1
    end
    g()
    y
end
#----
function f()
    local x::Int = 1.0
    x
end
#----
(a, b) -> a + b
#----
function f((a, b))
    a + b
end
#----
function f(x::T) where {T}
    T
end
#----
f(x)::T where {T} = x
#----
new_x = [i for i in 1:3 for j in 1:i]
#----
cond ? (return 1) : nothing
#----
function g()
    for i in 1:3
        try
            i == 2 && continue
        finally
            println(i)
        end
    end
end
#----
function f(a::Vararg{Int,N}) where N
    N
end
#----
struct P2
    a::Int
    b
    P2(a) = new(a)
    P2(a, b) = new(a, b)
end
#----
Base.@kwdef struct K
    a::Int = 1
end
#----
x -> begin
    y = x
    z -> y + z
end
