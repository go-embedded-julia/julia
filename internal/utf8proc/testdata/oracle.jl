
# The oracle of internal/utf8proc: what julia 1.12.6's own utf8proc (v2.10.0,
# Unicode 16.0.0) answers, written to the golden files the Go tests replay
# without julia. Regenerate with
#
#     pkgx julia@1.12.6 oracle.jl <dir>      # then gzip -9 <dir>/*.golden
#
# percp.golden     every code point 0..0x10FFFF: utf8proc_category,
#                  charwidth, charwidth_ambiguous, toupper, tolower, totitle
#                  (as the difference from the code point), islower, isupper,
#                  run-length encoded: "lo hi cat width amb up lo ti il iu"
# normalize.golden for every code point that is not a surrogate, under each
#                  option set below, the result when it is not the code point
#                  itself: "optsname cp result..." (hexadecimal)
# strings.golden   curated and random strings: "hex(string) | forms | graphemes"
#
# Everything is the library's own answer, called directly (ccall) or through
# Base.Unicode.utf8proc_map, never recomputed.

using Random

const U = Base.Unicode
dir = ARGS[1]
mkpath(dir)

hex(cp) = string(UInt32(cp), base=16)
hexs(s::AbstractString) = join((hex(c) for c in s), " ")

# ---- per code point -------------------------------------------------------
cat(cp) = Int(ccall(:utf8proc_category, Cint, (UInt32,), cp))
wid(cp) = Int(ccall(:utf8proc_charwidth, Cint, (UInt32,), cp))
amb(cp) = Int(ccall(:utf8proc_charwidth_ambiguous, UInt8, (UInt32,), cp))
up(cp) = Int(ccall(:utf8proc_toupper, UInt32, (UInt32,), cp)) - Int(cp)
lo(cp) = Int(ccall(:utf8proc_tolower, UInt32, (UInt32,), cp)) - Int(cp)
ti(cp) = Int(ccall(:utf8proc_totitle, UInt32, (UInt32,), cp)) - Int(cp)
isl(cp) = Int(ccall(:utf8proc_islower, Cint, (UInt32,), cp))
isu(cp) = Int(ccall(:utf8proc_isupper, Cint, (UInt32,), cp))
tuple_of(cp) = (cat(cp), wid(cp), amb(cp), up(cp), lo(cp), ti(cp), isl(cp), isu(cp))

open(joinpath(dir, "percp.golden"), "w") do io
    start = 0
    prev = tuple_of(0)
    for cp in 1:0x110000
        cur = cp == 0x110000 ? nothing : tuple_of(cp)
        if cur != prev
            println(io, start, " ", cp - 1, " ", join(prev, " "))
            start = cp
            prev = cur
        end
    end
end

# ---- normalization of every code point -------------------------------------
const OPTS = [
    ("NFD",   U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE),
    ("NFC",   U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE),
    ("NFKD",  U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE | U.UTF8PROC_COMPAT),
    ("NFKC",  U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_COMPAT),
    ("NFKCCF", U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_COMPAT | U.UTF8PROC_CASEFOLD | U.UTF8PROC_IGNORE),
    ("STRIPMARK", U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE | U.UTF8PROC_STRIPMARK),
    ("LUMP", U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_LUMP),
]
# (STRIPNA is not here: it removes every one of the 800 000 unassigned code points)
open(joinpath(dir, "normalize.golden"), "w") do io
    for (name, flags) in OPTS
        println(io, "# ", name, " ", flags)
        for cp in 0:0x10FFFF
            0xd800 <= cp <= 0xdfff && continue
            s = string(Char(cp))
            r = U.utf8proc_map(s, flags)
            r == s && continue
            println(io, name, " ", hex(cp), " ", hexs(r))
        end
    end
end

# ---- strings ----------------------------------------------------------------
curated = String[
    "", "a", "abc", "é", "é", "Å", "Å", "Å", "ẛ̣", "ḍ̇", "ḍ̇",
    "ﬁ", "Ω", "Ω", "가", "가", "각", "각ᆨ", "한글",
    "क़", "क़", "q̣̇", "q̣̇", "ế", "ẹ́", "ẹ́",
    "̈́", "ཱི", "ཱི", "ΐ", "ΐ", "ୋ", "ো", "ൊ",
    "ß", "İ", "ǅ", "ǆ", "ῼ", "ﬃ", "ŉ", "İ", "ı", "Σ", "ς", "σ", "ẞ",
    "\r\n", "\n", "a\r\nb", "\u0085", " ", " ", " ", "　",
    "\U0001f468‍\U0001f469‍\U0001f467", "\U0001f1fa\U0001f1f8\U0001f1eb\U0001f1f7\U0001f1e9", "\U0001f44b\U0001f3fd",
    "क्ष", "क्‍ष", "त्त", "กำ", "กํา",
    "a‍b", "؀a", "أ", "ᄀ가", "ꥠힰ",
    "x️", "❤️", "­", "​", "⁠", "᠎",
    "ᵀ0", "\U0001d400", "\U00020000", "\U0010ffff", "￿",
]
rng = MersenneTwister(20261115)
pools = [
    collect(0x20:0x7e), collect(0xa0:0x17f), collect(0x300:0x36f), collect(0x1100:0x11ff), collect(0xac00:0xac40),
    collect(0x0900:0x097f), collect(0x0980:0x09ff), collect(0x0e00:0x0e7f), collect(0x0600:0x06ff), collect(0x0400:0x04ff),
    collect(0x1f300:0x1f64f), [0x200d, 0xfe0f, 0xfe0e, 0x200c, 0x20e3], collect(0x1f1e6:0x1f1ff), collect(0x1f3fb:0x1f3ff),
    collect(0x4e00:0x4e40), collect(0x2000:0x206f), collect(0x2100:0x214f), collect(0xfb00:0xfb4f), collect(0x1e00:0x1eff),
    collect(0x1d400:0x1d440), collect(0xe0000:0xe0020),
]
function randstring_u(rng)
    n = rand(rng, 1:10)
    cps = UInt32[]
    for _ in 1:n
        pool = pools[rand(rng, 1:length(pools))]
        cp = pool[rand(rng, 1:length(pool))]
        0xd800 <= cp <= 0xdfff && continue
        push!(cps, cp)
    end
    String(Char.(cps))
end
strs = vcat(curated, [randstring_u(rng) for _ in 1:2500])

forms = [("NFC", U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE), ("NFD", U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE),
         ("NFKC", U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_COMPAT), ("NFKD", U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE | U.UTF8PROC_COMPAT),
         ("CF", U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_CASEFOLD | U.UTF8PROC_IGNORE | U.UTF8PROC_COMPAT)]
open(joinpath(dir, "strings.golden"), "w") do io
    for s in strs
        parts = String[]
        for (_, flags) in forms
            push!(parts, hexs(U.utf8proc_map(s, flags)))
        end
        gl = [length(g) for g in U.graphemes(s)]
        up = hexs(uppercase(s)); lo = hexs(lowercase(s)); tc = hexs(titlecase(s))
        println(io, hexs(s), " | ", join(parts, " | "), " | ", join(gl, " "), " | ", up, " | ", lo, " | ", tc)
    end
end

# grapheme_break of pairs: one code point of each kind of bound class and some more
reps = UInt32[0x0a, 0x0d, 0x01, 0x41, 0x300, 0x200d, 0x1100, 0x1161, 0x11a8, 0xac00, 0xac01, 0x1f1e6, 0x903, 0x600,
              0x1f468, 0x1f3fb, 0x2764, 0x915, 0x94d, 0x93c, 0x200c, 0x20e3, 0xfe0f, 0xe0020, 0x1f44b, 0xad, 0x0e33, 0x1f600]
open(joinpath(dir, "graphemebreak.golden"), "w") do io
    for a in reps, b in reps
        println(io, hex(a), " ", hex(b), " ", Int(ccall(:utf8proc_grapheme_break, UInt8, (UInt32, UInt32), a, b)))
    end
end

# ---- every combination of the options, over crafted inputs --------------------
# "flags hex(input) | hex(output bytes) or ERR message": the library's own answer
# to utf8proc_map, an error as the message utf8proc_errmsg gives.
inputs = [
    Vector{UInt8}("a\r\nb\nc\rd\u0085e f g\vh\fi\tj\x01k\x7f\u0080"),
    Vector{UInt8}("  ‘x’ ‐-− ⁄/ ∶ ‹› ∖ ˄ _‿ ˋ ∣ ∼ ʼˈ〈〈〉〉‸⌃ˍ∕"),
    Vector{UInt8}("á̀ḅ̇ éḍ̇ ﬁ①½Ω 각각 क़̈́"),
    Vector{UInt8}("͸x࿾\U000e0001­​﻿⁠ ᠎"),
    Vector{UInt8}("ǅßẞİΣς ᾈ ﬃ\U0001e900"),
    UInt8[0xff, 0x41], UInt8[0x41, 0xe2, 0x82], UInt8[0xed, 0xa0, 0x80], UInt8[0xf4, 0x90, 0x80, 0x80], UInt8[0xc0, 0x80], UInt8[],
    Vector{UInt8}("\U0001f468‍\U0001f469 \U0001f1fa\U0001f1f8 क्ष é \r\n"),
]
flagbits = [U.UTF8PROC_NLF2LS, U.UTF8PROC_NLF2PS, U.UTF8PROC_STRIPCC, U.UTF8PROC_CASEFOLD, U.UTF8PROC_LUMP, U.UTF8PROC_STRIPMARK,
            1 << 14, U.UTF8PROC_REJECTNA, U.UTF8PROC_IGNORE, U.UTF8PROC_COMPAT, U.UTF8PROC_CHARBOUND]
bases = [U.UTF8PROC_STABLE | U.UTF8PROC_DECOMPOSE, U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE, U.UTF8PROC_STABLE, U.UTF8PROC_STABLE | U.UTF8PROC_COMPOSE | U.UTF8PROC_DECOMPOSE]
hexb(b) = join((string(x, base=16, pad=2) for x in b), "")
open(joinpath(dir, "options.golden"), "w") do io
    for base in bases, mask in 0:(1 << length(flagbits)) - 1
        flags = base
        for (i, bit) in enumerate(flagbits)
            mask & (1 << (i - 1)) != 0 && (flags |= bit)
        end
        for inp in inputs
            s = String(copy(inp))
            res = try
                hexb(codeunits(U.utf8proc_map(s, flags)))
            catch e
                "ERR " * e.msg
            end
            println(io, flags, " ", hexb(inp), " | ", res)
        end
    end
end
