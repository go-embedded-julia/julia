# boot.jl

`boot.jl` is `base/boot.jl` of julia 1.12.6 (tag `v1.12.6`, commit
`15346901f003`), unmodified, under julia's MIT licence
(https://github.com/JuliaLang/julia/blob/v1.12.6/LICENSE.md). It is the Julia
code that defines the rest of Core after the C runtime has created the
type-system core (`jl_init_types`), and the interpreter evaluates it in
Core as julia does.
