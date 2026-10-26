#----
x = 1 + 0x02
'a'
#----
f(x; y=1) = x + y
#----
"""
    doc
"""
function g(a::T, b...) where {T<:Real}
    return a ≤ b[1] ? -a : a^2
end
#----
s = "a $(b) c\$ é \xff"
#----
x"raw\"str"suffix
#----
`cmd $x`
#----
[1 2; 3 4]
[1;; 2;;; 3]
#----
1_000_000 + 0b1010 + 0o17 + 0xFFFF_FFFF_FFFF_FFFF_F + 123456789012345678901234567890
#----
1.5e10 + 2.5f0 + 0x1p3 + 1e-400
#----
a.b.c, a.:+, a.$b, @m(x), A.@m y, @m.x
#----
for i in 1:10, j ∈ 2:3
    @show i, j
end
#----
let x = 1, y
    x
end
#----
try
    x
catch e
    y
else
    z
finally
    w
end
#----
struct S{T} <: AbstractS
    "field doc"
    x::T
    const y
end
#----
module M
export f, @g
import Base: +, -
using ..N: a as b
end
#----
x -> x + 1; (a, b) -> a; (;k) -> k
#----
quote
    $x + :y
end
#----
[x for x in xs if x > 0]
(x, y for x in a for y in b)
#----
x .+= y .* z'
#----
f(x
#----
1 +
#----
"unterminated
#----
αβγ = ∑ᵢ = ℏ + µ + ɛ
#----
a = b = c ? d : e ? f : g
#----
@doc raw"""
doc
""" f
#----
end
#----
)
#----
x = 1 # comment
#= multi
   line =# y = 2
#----
1.2.3
#----
'ab'
#----
0x
#----
0x0p0 + 0x1p-2000 + 1e-400 + 1.5f-50
#----
s = "a
b"
#----
x"a
b"
#----
"abc\
#----
"\777"
#----
"\q"
#----
'\777'
#----
'
#----
''
#----
#= unterminated
#----
#= nested #= =# unterminated
#----
`abc
#----
let x
#----
quote x
#----
try x
#----
for x in y
#----
while true
#----
function f()
#----
if x
#----
module M
#----
struct S
#----
f() do x
#----
for
#----
function f
#----
let x y
end
#----
break x
#----
do x end
#----
try
else
    x
end
#----
try x finally y catch e z end
#----
import A .==
#----
import A.=
#----
import .⋆
#----
import A...
#----
import A.B.@x, $C, .., ...D
#----
[x for x
 in y]
#----
for x y end
#----
(1:)
#----
[a ; ; b]
#----
[a b ;; c]
#----
[a; b, c]
#----
[a ;; b c]
#----
[a b ;;
 c]
#----
"$1"
#----
{a ;; b}
#----
var"abc
#----
var"x"y
#----
var"x"""
#----
x ¨ y
#----
x ­ y
#----
x
 y
#----
x  y
#----
x 􏿿 y
#----
"a‪b"
#----
x"$‪"
#----
#= ‪ =#
#----
x ₂
#----
.−= x
#----
a .− b
#----
a <--- b
#----
a <--< b
#----
a ** b
#----
1._
#----
1.e
#----
1.x
#----
1e1.
#----
1.e1.
#----
1e+
#----
0x1p
#----
0x.
#----
0x1.0
#----
0b12
#----
1.(x)
#----
1.+2
#----
1..2
#----
1.2.
#----
x"""
	a
	b"""
#----
```
  cmd
 ```
#----
"""
  a
 b"""
#----
"""
	a
  b"""
#----
"""
  $a
  $b
"""
#----
'''
#----
'\''
#----
a‍b + 👨‍👩 + x‌
#----
f(x) = 1 =
#----
"x"
]
#----
f(;)
f(a;b;c)
f(a, b...; c=1)
#----
(a,b=1; c,d=2; e,f=3)
#----
(x...; y)
#----
(;;)
#----
@doc x
y
#----
@doc x

y
#----
A.@doc x
y
#----
@foo(a, b) do x
    x
end
#----
f() do; end
#----
f.(x; y=1)
#----
a.(b).c
#----
x.'
#----
struct A
    "doc"
    x
    "doc2"
    y::Int
end
#----
mutable struct B <: A; x; end
#----
abstract type A <: B{T} end
primitive type P <: Q 32 end
#----
global const x = 1
local const y = 2
const global z = 3
global (a, b)
const x, y = 1, 2
#----
const x .= 1
#----
global const x
#----
function (x) end
function (x...) end
function ()(x) end
function (A).f() end
function (@f(x)) end
#----
function f()::T where {T} end
#----
function begin() end
#----
macro while(ex) end
#----
macro m end
#----
function f body end
#----
x -> begin
  x
end
(a; b=1) -> a
(a; b) -> a
(;) -> 1
(x,) -> x
#----
module A
public f, g
public(x) = 1
end
#----
public a
#----
export @a, $b, (c), +
#----
import A: x as y, B
using A as B
import A as B: x
import A: x, B: y
#----
a :> b
a :< b
#----
1:
2
#----
1 : 2 : 3 : 4 : 5
#----
a--b
#----
x where {T S}
x where {y for y in ys}
#----
<: x
<:{T}(x::T)
<: A where B
<:(a,)
<:(a)
#----
+(a;b)
+(a=1)
+(a)^2
.+(a)
+ (a,b)
.-2
-0x1
#----
√x + ∛y + 1√x
#----
2x + 2(x) + (2)(3)x + x'y
#----
"a"x
#----
[x +y]
[x+y +z]
[x +₁y]
[1 :a]
[a ~b]
[f (x)]
#----
a ? b :c
#----
if true; x ? true elseif true end
#----
if a xx else if b yy end
#----
if
end
#----
try x catch e+3 y end
#----
let x::1 ; end
#----
f(x for x in y)
#----
((x)for x in xs)
#----
[;;]
[\n  ;; \n ]
#----
x"s"10.0 + x"s"in
#----
@S[a ;; b]
@S{a}
A.@S[a]
@M.(x)
A.@B.x
@A.B.@x a
@(A) x
@var"#" a
@[x] y z
#----
f.$x + A.$B.@x
#----
:(=) + :(::) + :end + :(end) + a[:(end)]
#----
"$(x,y)" * "$(x for x in y)"
#----
a[begin] + a[end]
#----
x = y ? z : w
#----
1 +
2
#----
:
#----
::
#----
$
#----
&
#----
0x1.8p3 + 0x1p-1074 + 1e308 * 1e309
#----
2.0e400
#----
1f50
#----
a >>> b; a >>>= b; a >>= b; a >= b; a << b; a <<= b; a <= b; a === b; a !== b; a != b
a % b; a %= b; a ++ b; a ÷ b; a ÷= b; a ⊻ b; a ⊻= b; a |= b; a &= b; a ^= b; a \= b
a //= b; a /= b; a *= b; a -= b; a += b; a $= b; a −= b; a := b; a ≔ b
a .>>> b; a .>= b; a .<< b; a .<= b; a .=== b; a .!== b; a .!= b; a .% b; a .÷ b
a .⊻ b; a .|| b; a .&& b; a .| b; a .|= b; a .& b; a .&= b; a .== b; a .=> b; a .~ b
a .* b; a ./ b; a .\ b; a .^ b; a .+ b; a .- b; a .− b; a .−= b; a .-> b; a .⊗ b; a .√ b
#----
0x1 + 0x123 + 0x12345 + 0x123456789 + 0x12345678901234567 + 0x123456789012345678901234567890123
0b1 + 0b101010101 + 0b10101010101010101 + 0b101010101010101010101010101010101
0b10101010101010101010101010101010101010101010101010101010101010101
0b101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101010101
0o7 + 0o777 + 0o7777777 + 0o777777777777 + 0o3777777777777777777777 + 0o37777777777777777777777777777777777777777
0o7777777777777777777777777777777777777777777777 + 0o377777777777777777777777777777777777777777777
0o4000000000000000000000000000000000000000000 + 0o0000000000000000000000000000000000000000001
10000000000000000000000000000000000000000 + 9223372036854775807 + 9223372036854775808
170141183460469231731687303715884105727 + 170141183460469231731687303715884105728
#----
"\n\t\r\e\b\f\v\a\\\'\"\$\`\x41A\U00000041\101"
'\n' + '\x41' + 'é' + '\U1F600' + '\101' + '\e'
#----
T[a ;; b]
[a ; b ;; c ; d]
[a ; b ;;; c ; d]
#----
for outer i = 1:10 end
for outer = 1:10 end
for outer (x, y) in z end
#----
function @m(x) end
#----
a .|| b .&& c
#----
[x *y]
[x &y]
[x .+y]
#----
f(x for x
in y)
#----
@m x for x in y
#----
[x for x in y
 if x]
#----
"$x‰"
#----
"a\
   b"
#----
a +² b; a ++′ b; a − b
#----
1_x
#----
.1.2
#----
1.e+
#----
a ..+ b
#----
verylongidentifier = averyveryverylongname
#----
try x catch e y else z end
#----
(a;) -> 1
#----
0o00000000000000000000000000000000000000000000000001 + 0o00000000000000000000000000000000000000000000000000
#----
0.0 + 0e5 + 0x0p0 + 0.0f0 + 1e-400
#----
"\18" * "\08"
#----
try
  x
finally
  y
catch e
  z
end
#----
@
(A) x
#----
try
  x
#----
try
    a
    b
    c
    d
end
#----
using .. .