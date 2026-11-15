# The same large string and the same calls as TestUtf8procBase (interp), timed
# in julia 1.12.6 on one thread, best of 5 rounds after a warm-up:
#
#     pkgx julia@1.12.6 bench.jl
#
# The unit has 33 characters ("Héllo wörld ǆ ß Straße ﬁ é 각 ΑΣ "),
# repeated 20000 times: 660 000 characters.

unit = "Héllo wörld ǆ ß Straße ﬁ é 각 ΑΣ "
mid = repeat(unit, 2000)    # uppercase, lowercase: 66 000 characters
big = repeat(unit, 20000)   # normalize: 660 000 characters
calls = [("uppercase", mid, () -> uppercase(mid)), ("lowercase", mid, () -> lowercase(mid)),
         ("normalize NFC", big, () -> Base.Unicode.normalize(big, :NFC)), ("normalize NFD", big, () -> Base.Unicode.normalize(big, :NFD))]
for (name, s, f) in calls
    f()  # warm-up: compilation
    best = minimum(@elapsed(f()) for _ in 1:3)
    println(name, " of ", length(s), " characters: ", round(best * 1e3, digits=3), " ms")
end
