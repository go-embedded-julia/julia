# show of a value that contains itself (show.jl:441-453, 489-510).
mutable struct Node
    next
end
a = Node(nothing)
a.next = a
println(repr(a))
b = Node(Node(nothing))
b.next.next = b
println(repr(b))
struct Wrap
    x
end
c = Node(nothing)
c.next = Wrap(c)
println(repr(c))
v = Any[1]
push!(v, v)
println(repr(v))
n = Node(nothing)
n.next = Any[n]
println(repr(n))
