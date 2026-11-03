# memoryrefnew stepping back from an offset (builtins.c:1740-1745).
m = Memory{Any}(undef, 4)
r3 = Core.memoryrefnew(Core.memoryrefnew(m), 3, false)
for (name, f) in ["3 then 0" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, 0, false)),
        "3 then -1" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, -1, true)),
        "3 then -2" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, -2, true)),
        "3 then -3" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, -3, false)),
        "3 then 2" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, 2, true)),
        "3 then 3" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, 3, true)),
        "1 then 0" => () -> Core.memoryrefoffset(Core.memoryrefnew(Core.memoryrefnew(m), 0, false)),
        "typemin" => () -> Core.memoryrefoffset(Core.memoryrefnew(r3, typemin(Int), false))]
    try
        println(name, " => ", f())
    catch e
        println(name, " => !", typeof(e))
    end
end
