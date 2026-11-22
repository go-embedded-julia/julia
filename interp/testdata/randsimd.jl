# The arrays rand! fills with Random's XoshiroSimd (julia 1.12.6,
# stdlib/Random/src/XoshiroSimd.jl): lengths below, at and above its thresholds
# (64 bytes; 640 elements for Bool). Each line is one cell; the output of
# every cell is the line's text, a hash of the element bits with the first
# and last element, so a stream that differs in one element differs here.
# The generators start from a fixed state (X(s)), not a seed: seeding hashes
# the seed with SHA2-256, which is another part of the interpreter's story.
# Regenerate randsimd.golden with real julia:
#     pkgx julia@1.12.6 randsimd.jl > randsimd.golden
using Random
function show_case(name, x) h = UInt64(14695981039346656037); for v in x; h = (h ⊻ (v isa AbstractFloat ? reinterpret(UInt64, Float64(v)) : UInt64(v % UInt64))) * 1099511628211; end; println(name, " ", length(x), " ", h, " ", x[1], " ", x[end]) end
X(s) = (s = UInt64(s); Xoshiro(s * 0x9E3779B97F4A7C15, s * 0xBF58476D1CE4E5B9 + 1, s * 0x94D049BB133111EB + 2, s * 0xD6E8FEB86659FD93 + 3))
for s in (1, 42), n in (7, 8, 9, 16, 100) show_case("Float64 state $s", rand(X(s), n)) end
copy!(Random.default_rng(), X(42)); show_case("Float64 task-local 100x100", rand(100, 100))
for s in (1, 42), n in (15, 16, 17, 100) show_case("Float32 state $s", rand(X(s), Float32, n)) end
for s in (1, 42), n in (31, 32, 33) show_case("Float16 state $s", rand(X(s), Float16, n)) end
for s in (1, 42), n in (7, 8, 9, 33) show_case("UInt64 state $s", rand(X(s), UInt64, n)) end
for s in (5, 6), n in (63, 64, 65) show_case("UInt8 state $s", rand(X(s), UInt8, n)) end
for s in (3, 4), n in (15, 16, 17) show_case("Int32 state $s", rand(X(s), Int32, n)) end
for s in (7, 8), n in (639, 640, 1151, 1152, 2000) show_case("Bool state $s", rand(X(s), Bool, n)) end
copy!(Random.default_rng(), X(11)); show_case("Bool task-local 1000", rand(Bool, 1000)); show_case("Float32 task-local 1000", rand(Float32, 1000))
