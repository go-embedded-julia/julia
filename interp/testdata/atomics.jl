# The atomic pointer intrinsics (runtime_intrinsics.c:455-632) through
# pointers into a Memory{Int}, a Memory{Any} and a Memory{NTuple{3, Int}},
# called in sequence; each printed as `src => result` or `src => !error`.
using Core.Intrinsics: atomic_pointerref, atomic_pointerset, atomic_pointerswap,
    atomic_pointermodify, atomic_pointerreplace, atomic_fence
m = Memory{Int}(undef, 2); m[1] = 5; m[2] = 6
p = Ptr{Int}(pointer(m))
ma = Memory{Any}(undef, 1); ma[1] = :a
pa = Ptr{Any}(pointer(ma))
mt = Memory{NTuple{3, Int}}(undef, 1)
pt = Ptr{NTuple{3, Int}}(pointer(mt))
parts(r) = isstructtype(typeof(r)) && nfields(r) > 0 && !(r isa Ptr) ?
    (typeof(r), ntuple(i -> getfield(r, i), nfields(r))...) : r
for (src, f) in [
        "ref sc" => () -> atomic_pointerref(p, :sequentially_consistent),
        "ref not_atomic" => () -> atomic_pointerref(p, :not_atomic),
        "ref release" => () -> atomic_pointerref(p, :release),
        "ref 1" => () -> atomic_pointerref(p, 1),
        "set release" => () -> (atomic_pointerset(p, 7, :release); m[1]),
        "set acquire" => () -> atomic_pointerset(p, 7, :acquire),
        "set float" => () -> atomic_pointerset(p, 1.5, :release),
        "swap" => () -> (atomic_pointerswap(p, 9, :acquire_release), m[1]),
        "modify" => () -> parts(atomic_pointermodify(p, +, 1, :monotonic)),
        "modify float" => () -> atomic_pointermodify(p, (a, b) -> 1.5, 1, :monotonic),
        "replace hit" => () -> parts(atomic_pointerreplace(p, 10, 11, :sequentially_consistent, :monotonic)),
        "replace miss" => () -> parts(atomic_pointerreplace(p, 3, 4, :sequentially_consistent, :monotonic)),
        "replace order" => () -> atomic_pointerreplace(p, 3, 4, :monotonic, :sequentially_consistent),
        "replace float" => () -> atomic_pointerreplace(p, 3, 4.0, :sequentially_consistent, :monotonic),
        "replace sym" => () -> atomic_pointerreplace(p, 3, 4, 1, :monotonic),
        "any ref" => () -> atomic_pointerref(pa, :monotonic),
        "any set" => () -> (atomic_pointerset(pa, 1.5, :monotonic); ma[1]),
        "any replace" => () -> parts(atomic_pointerreplace(pa, 1.5, "s", :monotonic, :monotonic)),
        "any modify" => () -> parts(atomic_pointermodify(pa, (a, b) -> b, 2, :monotonic)),
        "tuple ref" => () -> atomic_pointerref(pt, :monotonic),
        "fence sc" => () -> atomic_fence(:sequentially_consistent),
        "fence acquire" => () -> atomic_fence(:acquire),
        "fence unordered" => () -> atomic_fence(:unordered),
        "fence 1" => () -> atomic_fence(1),
    ]
    try
        println(src, " => ", repr(f()))
    catch e
        println(src, " => !", repr(e))
    end
end
