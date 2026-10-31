# Core-only programs, each run in a fresh module by julia 1.12.6 (the
# oracle's eval op) and by the Go interpreter; the shown values must be
# equal. A program uses only Core and its intrinsics: Base's code does not
# run in the Go interpreter yet. Programs are separated by `#---` lines.
#---
import Core.Intrinsics: add_int
add_int(1, 2)
#---
import Core.Intrinsics: add_int
add_int(9223372036854775807, 1)
#---
import Core.Intrinsics: add_int, sub_int, mul_int
(add_int(0xff, 0x01), sub_int(0x00, 0x01), mul_int(0xffff, 0x0002))
#---
import Core.Intrinsics: mul_int, sub_int
(mul_int(0xffffffff, 0x00000003), sub_int(0x0000000000000000, 0x0000000000000001))
#---
import Core.Intrinsics: slt_int, sle_int, ult_int, ule_int, eq_int, ne_int
(slt_int(-1, 1), ult_int(-1, 1), sle_int(2, 2), ule_int(0x02, 0x01), eq_int(3, 3), ne_int(3, 3))
#---
import Core.Intrinsics: checked_sdiv_int, checked_srem_int, checked_udiv_int, checked_urem_int
(checked_sdiv_int(7, -2), checked_srem_int(7, -2), checked_udiv_int(0x07, 0x02), checked_urem_int(0x07, 0x02))
#---
import Core.Intrinsics: checked_sdiv_int
try
    checked_sdiv_int(1, 0)
catch e
    e
end
#---
import Core.Intrinsics: checked_sdiv_int
try
    checked_sdiv_int(-9223372036854775808, -1)
catch e
    typeof(e)
end
#---
import Core.Intrinsics: checked_srem_int, sdiv_int, srem_int
(checked_srem_int(-9223372036854775808, -1), sdiv_int(-7, 2), srem_int(-7, 2))
#---
import Core.Intrinsics: udiv_int
try
    udiv_int(0x01, 0x00)
catch e
    e
end
#---
import Core.Intrinsics: sdiv_int, srem_int, udiv_int, urem_int
(sdiv_int(5, 0), srem_int(5, 0), urem_int(0x05, 0x00), sdiv_int(-9223372036854775808, -1), srem_int(-9223372036854775808, -1), udiv_int(0xffff, 0x0000))
#---
import Core.Intrinsics: not_int, neg_int, and_int, or_int, xor_int
(not_int(true), not_int(0x0f), neg_int(5), and_int(0x0c, 0x0a), or_int(0x0c, 0x0a), xor_int(0x0c, 0x0a))
#---
import Core.Intrinsics: neg_int
neg_int(-9223372036854775808)
#---
import Core.Intrinsics: add_int
try
    add_int(1, 0x01)
catch e
    e
end
#---
import Core.Intrinsics: add_int
try
    add_int(1)
catch e
    e
end
#---
import Core.Intrinsics: add_int
try
    add_int(:a, :b)
catch e
    e
end
#---
import Core.Intrinsics: add_float, sub_float, mul_float, div_float
(add_float(1.5, 2.25), sub_float(1.0, 3.0), mul_float(2.0, 0.5), div_float(1.0, 4.0))
#---
import Core.Intrinsics: div_float, neg_float
(div_float(1.0, 0.0), div_float(-1.0, 0.0), neg_float(0.0), div_float(1.0e300, 1.0e-300))
#---
import Core.Intrinsics: mul_float
(mul_float(1000.0, 1000.0), mul_float(100.0, 1000.0), mul_float(0.001, 0.1), mul_float(0.01, 0.01))
#---
import Core.Intrinsics: lt_float, le_float, eq_float, ne_float
(lt_float(1.0, 2.0), le_float(2.0, 2.0), eq_float(0.0, -0.0), ne_float(1.0, 1.0))
#---
import Core.Intrinsics: add_float
try
    add_float(1, 2)
catch e
    e
end
#---
import Core.Intrinsics: neg_float
try
    neg_float(1)
catch e
    e
end
#---
t = tuple(1, :a, true)
(getfield(t, 2), nfields(t), typeof(t))
#---
try
    getfield(tuple(1, 2), 3)
catch e
    e
end
#---
try
    getfield(tuple(1, 2), 1.5)
catch e
    e
end
#---
(tuple(1, 2) === tuple(1, 2), :a === :a, 1 === 0x01, nothing === nothing, tuple() === tuple())
#---
(isa(1, Int), isa(1, Integer), isa(0x01, Signed), isa(nothing, Nothing), isa(tuple(1), Tuple))
#---
(typeof(1), typeof(0x02), typeof(tuple(1, 0x02)), typeof(nothing), typeof(typeof(1)))
#---
(Int <: Integer, Integer <: Int, Union{} <: Int, tuple === tuple)
#---
try
    isa(1, 2)
catch e
    e
end
#---
try
    typeassert(1, Bool)
catch e
    e
end
#---
typeassert(0x01, Unsigned)
#---
try
    throw(:boom)
catch e
    e
end
#---
try
    if 1
        2
    end
catch e
    e
end
#---
import Core.Intrinsics: add_int, slt_int
function collatz_like(n)
    s = 0
    i = 0
    while slt_int(i, n)
        s = add_int(s, i)
        i = add_int(i, 1)
    end
    s
end
(collatz_like(10), collatz_like(0))
#---
import Core.Intrinsics: add_int, sub_int, slt_int
fib(n) = slt_int(n, 2) ? n : add_int(fib(sub_int(n, 1)), fib(sub_int(n, 2)))
fib(20)
#---
f(x::Int) = 2
f(x::Integer) = 1
f(x) = 0
(f(1), f(0x01), f(:a))
#---
f(x::Int) = x
try
    f(0x01)
catch e
    (typeof(e), getfield(e, :args))
end
#---
f(xs...) = xs
(f(), f(1), f(1, :b))
#---
g(a, bs...) = (a, bs)
g(1, 2, 3)
#---
function g(c)
    if c
        y = 1
    end
    y
end
try
    g(false)
catch e
    (typeof(e), getfield(e, :var), getfield(e, :scope))
end
#---
try
    an_undefined_name
catch e
    (typeof(e), getfield(e, :var))
end
#---
const c = 5
c
#---
const c = 5
try
    c = 6
catch e
    typeof(e)
end
#---
x = 1
x = tuple(x, x)
x
#---
r = try
    throw(5)
catch e
    tuple(e, e)
end
r
#---
function h(n)
    try
        throw(n)
    catch e
        try
            throw(tuple(e, e))
        catch e2
            e2
        end
    end
end
h(3)
#---
function k(n)
    for_ever = true
    i = 0
    while for_ever
        try
            throw(i)
        catch e
            for_ever = false
        end
    end
    for_ever
end
k(1)
#---
import Core.Intrinsics: add_int
function deep(n)
    deep(add_int(n, 1))
end
try
    deep(0)
catch e
    typeof(e)
end
#---
f() = 1
f() = 2
f()
#---
(svec_free = 1; Core)
#---
nothing
#---
:sym
#---
"a string"
