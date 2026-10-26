function counter()
    n = 0
    () -> (n += 1)
end
c = counter()
c(); c()
squares = [i^2 for i in 1:5 if isodd(i)]
d = Dict(:a => 1, :b => 2)
s = "a $(c()) b"
println(squares, " ", sort(collect(keys(d))), " ", s)
map(x -> x + 1, Int[])
