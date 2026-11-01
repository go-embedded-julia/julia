# boot.jl

The files here are julia 1.12.6's own (tag `v1.12.6`, commit
`15346901f003`), unmodified, under julia's MIT licence
(<https://github.com/JuliaLang/julia/blob/v1.12.6/LICENSE.md>), at their paths
under julia's `base/`:

- `boot.jl` is the Julia code that defines the rest of Core after the C
  runtime has created the type-system core (`jl_init_types`); the interpreter
  evaluates it in Core as julia does.
- `optimized_generics.jl` is the file `boot.jl` includes into Core
  (`include(Core, "optimized_generics.jl")`, boot.jl:1084).

They are embedded in the package and are what `include` reads (`VM.Sources`,
`load.go`): an include never reaches the host's filesystem.
