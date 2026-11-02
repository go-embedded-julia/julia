# swapglobal!, modifyglobal!, replaceglobal!, setglobalonce! and
# setglobal!'s orders (builtins.c:1380-1511), each on fresh globals of a new
# module M: `g = 1`, `t::Int = 2`, `const c = 3`, `global u`, `u2::Int`,
# and an import of Base.sin as s. Printed as `case => result` or
# `case => !error`, then the global after.
parts(r) = isstructtype(typeof(r)) && nfields(r) > 0 ?
    (typeof(r), ntuple(i -> getfield(r, i), nfields(r))...) : r
cases = [
    ("swapglobal!", (:g, 10)), ("swapglobal!", (:t, 1.5)), ("swapglobal!", (:t, 5)),
    ("swapglobal!", (:c, 1)), ("swapglobal!", (:s, 1)), ("swapglobal!", (:nope, 1)),
    ("swapglobal!", (:u, 7)), ("swapglobal!", (:g, 1, :not_atomic)), ("swapglobal!", (:g, 1, :unordered)),
    ("swapglobal!", (:g, 1, :acquire)), ("swapglobal!", (:g, 1, 2)), ("swapglobal!", (:g,)),
    ("modifyglobal!", (:g, +, 1)), ("modifyglobal!", (:t, +, 1)), ("modifyglobal!", (:t, (a, b) -> 1.5, 1)),
    ("modifyglobal!", (:u, +, 1)), ("modifyglobal!", (:c, +, 1)), ("modifyglobal!", (:g, +, 1, :not_atomic)),
    ("replaceglobal!", (:g, 1, 2)), ("replaceglobal!", (:g, 5, 2)), ("replaceglobal!", (:t, 2, 1.5)),
    ("replaceglobal!", (:t, 2, 3)), ("replaceglobal!", (:u, 1, 2)),
    ("replaceglobal!", (:g, 1, 2, :monotonic, :sequentially_consistent)),
    ("replaceglobal!", (:g, 1, 2, :sequentially_consistent, :not_atomic)),
    ("replaceglobal!", (:g, 1, 2, :not_atomic)),
    ("setglobalonce!", (:u, 1)), ("setglobalonce!", (:g, 1)), ("setglobalonce!", (:u2, 1.5)),
    ("setglobalonce!", (:u2, 1)), ("setglobalonce!", (:u, 1, :sequentially_consistent, :not_atomic)),
    ("setglobal!", (:g, 1, :not_atomic)), ("setglobal!", (:g, 9, :release)), ("setglobal!", (:g, 9, :acquire)),
]
for (f, args) in cases
    M = Module()
    Core.eval(M, :(begin g = 1; t::Int = 2; const c = 3; global u; global u2::Int; import Base: sin as s end))
    try
        r = Base.invokelatest(getfield(Core, Symbol(f)), M, args...)
        after = Base.invokelatest(isdefinedglobal, M, args[1]) ? Base.invokelatest(getglobal, M, args[1]) : nothing
        println(f, repr(args), " => ", repr(parts(r)), " ; ", repr(after))
    catch e
        println(f, repr(args), " => !", sprint(showerror, e))
    end
end
try; Core.swapglobal!(1, :x, 1); catch e; println("not a module => !", repr(e)); end
try; Core.swapglobal!(Main, 1, 1); catch e; println("not a symbol => !", repr(e)); end
