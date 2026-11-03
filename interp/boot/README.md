# boot

The files here are julia 1.12.6's own (tag `v1.12.6`, commit
`15346901f003`), unmodified, under julia's MIT licence
(<https://github.com/JuliaLang/julia/blob/v1.12.6/LICENSE.md>), at their paths
under julia's `base/`:

- `boot.jl` is the Julia code that defines the rest of Core after the C
  runtime has created the type-system core (`jl_init_types`); the interpreter
  evaluates it in Core as julia does.
- `optimized_generics.jl` is the file `boot.jl` includes into Core
  (`include(Core, "optimized_generics.jl")`, boot.jl:1084).
- `Base_compiler.jl`, `sysimg.jl`, `Base.jl` and every other file of
  julia's `base/` (julia's build scripts left out: `Makefile`,
  `.gitignore`, `version_git.sh`).
- `build_h.jl` and `version_git.jl` are the two files julia's build
  generates into `base/` (from `base/Makefile` and `base/version_git.sh`).
  They are the official julia 1.12.6 darwin/arm64 build's, copied from its
  `share/julia/base/`: the platform the interpreter models.
- `julia/Compiler/src/` is julia's `Compiler/src/`, the type-inference
  compiler that `Base_compiler.jl` includes. During the bootstrap the
  Compiler's includes are relative to `base/`, under `DATAROOT *
  "julia/Compiler/src/"` (Compiler.jl:92-105), so it sits there with
  `DATAROOT` empty.

They are embedded in the package and are what `include` reads (`VM.Sources`,
`load.go`): an include never reaches the host's filesystem.
