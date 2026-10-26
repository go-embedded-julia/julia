abstract type Shape end
struct Circle <: Shape
    r::Float64
end
struct Square <: Shape
    s::Float64
end
area(c::Circle) = π * c.r^2
area(s::Square) = s.s^2
shapes = Shape[Circle(1.0), Square(2.0)]
for s in shapes
    println(typeof(s), " ", area(s))
end
sum(area, shapes)
