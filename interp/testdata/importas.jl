# `as` in import and using (toplevel.c:853-915), each case in a fresh
# module, its result or error printed.
module A
export x
x = 1
y = 2
macro m() 3 end
module B; z = 4; end
end
for src in ["using .A: x as x2; x2", "using .A: y as y2; y2", "import .A: y as y3; y3",
        "import .A.y as y4; y4", "import .A as AA; AA.y", "import .A.B as BB; BB.z",
        "using .A: @m as @mm; @mm", "using .A: @m as mm", "import .A: x as @xx",
        "using .A as AA", "using .A: y as y5, x as x5; (y5, x5)", "import .A.B.z as zz; zz",
        "import .A.@m as mm", "import Main as MM; MM === Main"]
    M = Module()
    Core.eval(M, :(const A = $(@__MODULE__).A))
    try
        println(repr(src), " => ", repr(Core.eval(M, Meta.parseall(src))))
    catch e
        println(repr(src), " => !", sprint(showerror, e))
    end
end
