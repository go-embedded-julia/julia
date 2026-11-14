# stdlib

The directory `stdlib/` holds julia 1.12.6's own standard libraries
(`share/julia/stdlib/v1.12/` of the official build, tag `v1.12.6`),
unmodified, under their own licences (each package's `LICENSE.md` where it
has one; julia's is MIT, <https://github.com/JuliaLang/julia/blob/v1.12.6/LICENSE.md>).
Each package keeps its `Project.toml` and `src/` (its `StdlibArtifacts.toml`
where it has one); tests, docs and CI files are left out.

- The seven packages `sysimg.jl:84-99` loads into the system image:
  `FileWatching`, `Libdl`, `Artifacts`, `SHA`, `Sockets`, `LinearAlgebra`
  and `Random`.
- The three packages `LinearAlgebra`'s `Project.toml` makes `require` find
  with it: `OpenBLAS_jll`, `libblastrampoline_jll` and
  `CompilerSupportLibraries_jll` (the last is their dependency).

They are embedded in the package (`vfs.go`) and mounted at `Sys.STDLIB`, where
`Base.require` finds them: `<VM.BinDir>/../share/julia/stdlib/v1.12`.
