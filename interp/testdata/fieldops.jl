# swapfield!, modifyfield!, replacefield!, setfieldonce! (builtins.c:1081-1177),
# each case on a fresh F, printed as `src => (typeof(result), its fields...)`
# or `src => !error`, and the field after.
mutable struct F
    const c::Int
    @atomic a::Int
    x::Int
    y
    u::String
    F() = new(1, 2, 3, 4)
end
parts(r) = isstructtype(typeof(r)) && nfields(r) > 0 && !(r isa String) ?
    (typeof(r), ntuple(i -> getfield(r, i), nfields(r))...) : r
cases = [
    ("swapfield!", (:x, 10)), ("swapfield!", (:x, 1.5)), ("swapfield!", (:c, 1)),
    ("swapfield!", (:a, 5)), ("swapfield!", (:a, 5, :sequentially_consistent)),
    ("swapfield!", (:x, 5, :acquire)), ("swapfield!", (:x, 5, :unordered)),
    ("swapfield!", (:u, "s")), ("swapfield!", (:zz, 1)), ("swapfield!", (9, 1)),
    ("swapfield!", (:x, 1, 2)), ("swapfield!", (:x,)), ("swapfield!", (1.0, 1)),
    ("modifyfield!", (:x, +, 1)), ("modifyfield!", (:x, (a, b) -> 1.5, 1)),
    ("modifyfield!", (:y, +, 1)), ("modifyfield!", (:u, *, "s")),
    ("modifyfield!", (:a, +, 1, :monotonic)), ("modifyfield!", (:a, +, 1)),
    ("modifyfield!", (:x, (a, b) -> error("op"), 1)),
    ("replacefield!", (:x, 3, 7)), ("replacefield!", (:x, 4, 7)), ("replacefield!", (:x, 3, 1.5)),
    ("replacefield!", (:a, 2, 3, :sequentially_consistent, :monotonic)),
    ("replacefield!", (:a, 2, 3, :monotonic, :sequentially_consistent)),
    ("replacefield!", (:x, 3, 7, :not_atomic, :acquire)),
    ("replacefield!", (:a, 2, 3, :sequentially_consistent, :not_atomic)),
    ("replacefield!", (:a, 2, 3, :release, :release)),
    ("replacefield!", (:a, 2, 3, :sequentially_consistent)),
    ("replacefield!", (:u, "a", "b")), ("replacefield!", (:y, 4, 5)), ("replacefield!", (:y, 4.0, 5)),
    ("setfieldonce!", (:u, "s")), ("setfieldonce!", (:x, 1)), ("setfieldonce!", (:u, 1)),
    ("setfieldonce!", (:a, 1, :sequentially_consistent)), ("setfieldonce!", (:a, 1)),
    ("setfieldonce!", (:u, "s", :not_atomic, :acquire)),
]
for (f, args) in cases
    o = F()
    fld = args[1]
    try
        r = getfield(Core, Symbol(f))(o, args...)
        after = fld isa Symbol && isdefined(o, fld) ? getfield(o, fld) : nothing
        println(f, repr(args), " => ", repr(parts(r)), " ; ", repr(after))
    catch e
        println(f, repr(args), " => !", repr(e))
    end
end
for (f, v, args) in [("swapfield!", (1, 2), (1, 3)), ("swapfield!", Main, (:x, 1)), ("modifyfield!", :sym, (1, +, 1))]
    try
        getfield(Core, Symbol(f))(v, args...)
    catch e
        println(f, repr((v, args...)), " => !", repr(e))
    end
end
o = F(); println(Core.setfieldonce!(o, :u, "s"), Core.setfieldonce!(o, :u, "t"), " ", repr(o.u))
