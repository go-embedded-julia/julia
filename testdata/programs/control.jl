function collatz(n)
    steps = 0
    while n != 1
        n = iseven(n) ? n ÷ 2 : 3n + 1
        steps += 1
    end
    steps
end
r = try
    error("boom")
catch e
    sprint(showerror, e)
finally
    println("finally")
end
println(collatz(27), " ", r)
big(2)^100
