# julia — a Julia interpreter in pure Go

`github.com/go-embedded-julia/julia` is the start of a Julia implementation
written in Go (`CGO_ENABLED=0`), meant to be embedded in Go programs and to
run in the browser through WebAssembly — it will back the ` ```julia ` cells
of [go-notebook](https://github.com/go-notebook) the way
[rbgo](https://github.com/go-embedded-ruby/ruby) backs its Ruby cells.

The plan is [go-scikit/roadmap `bib/julia.md`](https://github.com/go-scikit/roadmap/blob/main/bib/julia.md).
Every stage is checked against the reference implementation, the pinned
**julia 1.12.6** (a stable release; commit `15346901f003`), never against
expectations written by hand.

## Status

| milestone | contents | state |
|---|---|---|
| M0 oracle harness | `internal/oracle`, `cmd/jloracle`, golden files | **done** |
| M1 parser | Go port of JuliaSyntax → `expr.Expr` | **done** — see parity below |
| M2 lowering | port of `src/julia-syntax.scm` → `CodeInfo` | **done** — see lowering parity below |
| M3 Core runtime and Base | types, subtyping, dispatch, the interpreter, julia's real Base and seven stdlibs | **done** (v0.13.0): `sysimg.jl` builds the system image, `Base.__init__` and every stdlib's `__init__` run, and 16 Base-level task programs give julia's output; known limits below |
| M4 Base bootstrap and image | Base evaluated (done in M3); an image writer and reader, so that a VM loads Base in seconds ([#40](https://github.com/go-embedded-julia/julia/issues/40)); a list of `test/` files passing | image done (v0.14.0); `test/` list planned |
| M5 numeric boundary | GMP, MPFR, PCRE2, dSFMT, LinearAlgebra and the other stdlibs in Go ([#36](https://github.com/go-embedded-julia/julia/issues/36)), numerics through go-numerics ([#33](https://github.com/go-embedded-julia/julia/issues/33)) | planned |
| M6 notebook bridge | a per-cell `Eval` with budgets for go-notebook (#40), the Julia–Go value table | per-cell `Eval` done (`Kernel`, v0.15.0); the Julia–Go value table planned |
| M7 wasm playground | a lazily loaded Julia core with its image | planned |

Known limits of v0.15.0:
- A VM loads the pinned system image in about 0.65 s; the image is not authenticated by itself and loading one writes process-shared type state, so a consumer pins its digest ([#49](https://github.com/go-embedded-julia/julia/issues/49)).
- The candidates of a `MethodError` whose candidates are all equally close (`sin()`) list in the order of the method table, which in julia is a function of type-name hashes that mix in each module's random build id (datatype.c:84): that order is one system image's, and is not reproduced. Every other part of the text is julia's.
- `rand!` of an array of 64 bytes or more (640 elements for `Bool`) runs julia's `XoshiroSimd`, whose `llvmcall` vector templates and `llvm.fshl.vNi64` rotations are performed in Go (`interp/llvmvec.go`) and not compiled: the stream of a generator started from a given state is julia's, bit for bit (`testdata/randsimd.golden`, written by julia 1.12.6). Seeding (`Random.seed!`, `Xoshiro(seed)`) is not yet julia's: it hashes the seed with SHA2-256, whose `transform!` is a `@generated` function, and the interpreter does not run generator bodies (a `@generated` function returns its fallback, `nothing` when it has none), so every seed gives the same state.
- Task-heavy code is slow on a cold VM.
- BLAS computes for `Float64` and `Float32`: libblastrampoline forwards julia's ILP64 symbols for the level-3 routines gemm, trsm, trmm, syrk and symm, gemv and the level-1 routines (dot, axpy, scal, nrm2, asum, iamax) to [go-blas/blas](https://github.com/go-blas/blas), so `A * B`, `A * x`, `x' * y`, `A' * A`, `Symmetric(A) * B`, `UpperTriangular(A) * B` and `B / UpperTriangular(A)` run, within go-blas' stated error bound of the exact value and not bit for bit with OpenBLAS. A `Memory` of `Float64` keeps its elements in a `[]float64`, which the double-precision routines read and write in place: no element is boxed, copied in or copied back, and a call costs 1.0 to 1.2 times go-blas called directly at n = 256 and 1024, from 3 to 20 times when the elements were boxed (measured in [#81](https://github.com/go-embedded-julia/julia/issues/81); n = 64 is 1.6 to 2.6 times for gemv, the shim's fixed cost). The price is on the read: a `Float64` read of an interpreted loop boxes, one allocation more per element (`s += v[i]` over 10^6 elements: +2.70% allocations, `sum(v)` +1.13%, `v .+ 1.0` +0.52%). A `Memory` of `Float32` is still boxed, so a single-precision call copies the elements it reads and writes. `Kernel.Get` and `Set` of a `[]float64` or a `Matrix[float64]` box nothing. The level-3 shims record the old values of the array they write (B for trsm and trmm, C for syrk and symm) for `Kernel.Reset`, as gemm does. The complex types, `A \ b` of a general `A`, the left triangular solve `UpperTriangular(A) \ B` (julia calls LAPACK's `trtrs`, not trsm; `BLAS.trsm!` itself runs), the triangular solve of a vector (level 2, `trsv`) and the factorizations (LAPACK) fail until go-blas has them.
- Writing a file in host mode is not ported (#32).
- Loading a C library is a stated divergence.

### Parser parity with julia 1.12.6

Measured on the whole `share/julia` tree of the release: `base/` (with
JuliaSyntax's own sources and tests), `Compiler/`, `stdlib/v1.12/` and
`test/` — 1191 files, 18.5 MB — and on the 976 golden snippets (every
`"input" => …` pair of JuliaSyntax's test files, and our own edge cases:
errors, invalid UTF-8, Unicode, line endings).

| level | compared with | corpus files identical | snippets identical |
|---|---|---|---|
| tokens | `JuliaSyntax.Tokenize.tokenize` | **1191 / 1191** | **976 / 976** |
| green tree + diagnostics | `parse!` then `build_tree(GreenNode)` | **1191 / 1191** | **976 / 976** |
| `Expr` | `Meta.parseall(text; filename)` | **1191 / 1191** | **976 / 976**, error messages included |

Code that does not parse yields julia's own result: the statements before
the first error, then `Expr(:error)` or `Expr(:incomplete)` holding the
formatted `ParseError` text, byte for byte.

Known gap: on a few malformed inputs JuliaSyntax itself throws (an
`AssertionError` on `import. ..` and `import. ...x`, a `BoundsError` on
`0:0:<`, all found by fuzzing) and julia falls back to its older flisp
parser, which is not ported. There `ParseAll` returns an
`Expr(:error)` and `ParseAllLimits` an error wrapping
`syntax.ErrParserFailed`, never a tree it cannot trust.

Speed, one core each (`jloracle bench`, Apple M4 Max): Go parses the corpus in
0.98 s, julia's `Meta.parseall` in 1.39 s (go/julia = 0.70).

### Lowering parity with julia 1.12.6

The reference is what `Meta.lower` returns in julia 1.12.6. That release
lowers with the femtolisp front end, `src/julia-syntax.scm`, by default;
JuliaLowering is not part of it. So the `lower` package ports
`julia-syntax.scm`, the code that produces the goldens, pass by pass:
`expand-forms`, `resolve-scopes`, `analyze-variables!`, `closure-convert`,
`linearize`, then renumbering into `CodeInfo`.

Macro expansion runs user code, so julia does it. For every top-level form
of every corpus file, the oracle expands the form with `macroexpand` in the
module its macros live in: `Base`, `Base.Compiler`, the stdlib, or a module
using `Test` for test files. It records the expanded form as the input and
`Meta.lower` of that form, in a fresh module per file, as the output.
`:toplevel` and `:module` are entered as `include` would enter them.

Outputs are compared after renumbering the front end's gensyms. Names such
as `#s12` and `#15#x` come from counters that live for the whole julia
session, so they differ between two sessions (macOS and Linux julia
disagree). The comparison is therefore "identical modulo numbering"; every
other part of the `CodeInfo` is compared, field by field.

| measured on | forms julia lowered | identical in Go | files fully identical |
|---|---|---|---|
| corpus (`share/julia`: base, Compiler, stdlib, test) | 53 997 | **53 997** | **1191 / 1191** |
| `testdata/lowering-snippets.jl` | 124 | **124** | — |
| `testdata/lowering-hygiene.jl`: macro-made `toplevel`s still in hygienic scopes | 48 | **48** | — |
| `testdata/lowering-exprs.jl`: built expressions (error paths, rare heads, values the parser never makes) | 477 | **477** | — |

A further 3 317 corpus forms are not counted. Their macro expansion throws
in the oracle, mostly test files using macros from packages that are not
loaded.

The port covers the whole femtolisp front end Meta.lower runs:
`julia-expand-macroscope` (hygiene: `resolve-expansion-vars`,
`rename-symbolic-labels`), the six passes, `deparse` for error messages
(119 forms checked against `julia --lisp`), and femtolisp's own behaviour
where lowering depends on it (hash-table iteration order, `#n=` labels,
`(error "malformed expression")` on malformed input). Internal functions
are also checked one by one against `julia --lisp` on S-expressions julia
values cannot spell (151 calls). Every statement of the package is
covered by a test whose expectation comes from julia.

Known gaps: a `Float64` inside a lowering error message makes `Lower`
return `lower.ErrUnsupported` (julia prints it with `%#.17g`, not ported);
macro expansion runs user code and stays with the caller, as it does in
julia. `Lower` refuses trees nested deeper than `lower.MaxDepth` (1000)
with `lower.ErrLimit`; `FuzzLower` runs on every pull request.

Speed: lowering the 53 997 corpus forms takes Go **0.56×** julia's time:
34.2 s against 1 min 0.9 s for julia 1.12.6's `Meta.lower` on the same
forms. Run with `JLGO_ORACLE=1 JLGO_BENCH=1 go test ./lower -run
TestBenchLowering`, Apple M4 Max, 1-minute load 6.6, 2026-11-02.

### Types and subtyping (core)

core — Julia's type objects, apply_type, subtyping and type intersection (a port of jltypes.c and subtype.c of 1.12.6): subtyping identical to julia on 20 000 random pairs and 4 902 cases from test/subtype.jl; intersection (`typeintersect`, `jl_type_intersection_with_env`, `jl_has_empty_intersection`) identical to julia on 20 000 other random pairs and 4 738 cases, most of them from test/subtype.jl's intersection tests; and method specificity (jl_type_morespecific), identical to Base.morespecific on the subtyping corpus.

core/dispatch — method tables with world ages, and the method a call with concrete argument types runs (the most specific applicable one, or an ambiguity), with its static parameters: identical to julia's `which` and dispatch environment on 1 500 random method tables and 9 000 calls (4 675 method choices, 1 968 of them with static parameters, 87 ambiguities). Not yet ported: the interference sets julia 1.12 computes at method insertion, which need type intersection.

Speed: subtyping is **1.31× slower** than julia's `jl_subtype` on one core (3.19 ms against 2.43 ms for the 20 000 pairs, best of 7, load 3.3, 2026-11-02), an open defect: [#20](https://github.com/go-embedded-julia/julia/issues/20). It was 1.37× on 2026-11-01 and 1.43× before the query environments were pooled, which halved the allocations (−51%, −66% of bytes).

### Interpreter (interp)

interp — a port of julia's `src/interpreter.c` that runs lowered code: `prepare` checks every SSA value, slot and label of a `CodeInfo` once, `evalBody` walks the statements (goto, conditional goto, return, try/catch through `EnterNode`/`leave`/`pop_exception`, assignment, method definition, global and constant declarations, world ages, `import`/`using`/`export`), `evalValue` evaluates operands and calls. Generic functions dispatch through `core/dispatch` in the task's world. `struct` and `abstract type` definitions run through the builtins lowering calls (`_structtype`, `_abstracttype`, `_setsuper!`, `_typebody!`, `_equiv_typedef`, `_defaultctors`), which build the default outer and inner constructors as `jl_ctor_def` does, their bodies lowered by `lower` (`DefaultInnerCtorBody`, `DefaultOuterCtorBody`); calling a type dispatches on its constructors, and user-defined outer constructors join them. Types may have parameters (`struct Q{T}`, `abstract type A{T}`): `Q(1)` infers `Q{Int64}` through the outer constructor, `Q{Integer}(1)` runs the inner one, and a parameter that no field constrains gets no outer constructor, as `jl_ctor_def` decides. As in julia's one method table, a constructor's signature keeps the called type first (`Type{Q}`, `Type{Q{T}} where T`), and a call dispatches on `Type{X}` for a type argument (`jl_inst_arg_tuple_type`). Methods with `where` clauses dispatch with their static parameters, through the `TypeVar` and `UnionAll` constructors of `boot.jl:335-338` (built in Go until `boot.jl` runs, like `Core.Typeof`). Macros are expanded before lowering, as `jl_expand_macros` does (`ast.c:1104-1254`): a macro call runs the macro's method with `__source__` and `__module__`, nested and recursive calls are expanded in turn, and the result is wrapped in a `hygienic-scope` that `lower` resolves, so that a macro's own names do not capture the caller's; quotes are expanded through `lower.Backquote` (`julia-bq-macro`). Core holds the built-in types, the exception and AST types of `boot.jl` and `jltypes.c` (with the `QuoteNode`, `LineNumberNode` and `Expr` constructors of `boot.jl:293, 519-534`), the builtins (`===`, `typeof`, `isa`, `<:`, `typeassert`, `throw`, `ifelse`, `tuple`, `svec`, `getfield`, `nfields`, `fieldtype`, `getglobal`, `setglobal!`, `isdefinedglobal`, `get_binding_type`, `apply_type`, `_typevar`, `_expr`, `_apply_iterate` (splatting tuples and simple vectors) and the six type-definition builtins) and the integer and float intrinsics, with julia's argument checks and messages.

`VM.Boot` evaluates julia 1.12.6's own `base/boot.jl`, unmodified and embedded, in Core, all 363 forms, as julia does after `jl_init_types`, then runs `post_boot_hooks` (`jltypes.c:3909-3968`), so that the exceptions the builtins raise are the ones `boot.jl` defines and an error in an included file is a `LoadError`. What `boot.jl` needs from C is here: the types `jl_init_types` creates (the IR nodes with their layouts, `GenericMemory`, `GenericMemoryRef` and `Array` with theirs, `AddrSpace` and `Core.CPU`, the runtime's opaque types), `module` expressions and relative `using .M` paths (`toplevel.c:114-198, 507-570`), `_primitivetype`, `setfield!`, `invokelatest`, `invoke_in_world`, `getfield` of types (`DataType`, `TypeName`, `TypeVar`, `UnionAll`, `Union`, `Vararg`) and of AST nodes, the conversion (`trunc_int`, `sext_int`, `zext_int`, `bitcast`) and shift (`shl_int`, `lshr_int`, `ashr_int`) intrinsics, and `ccall` to Go shims keyed by the C function's name (`jl_toplevel_eval_in`, `jl_load_`, `jl_type_unionall`, …; an unknown name is `ErrUnsupported` when the call runs). `include` (`jl_load_`) reads `VM.Sources`, julia's embedded sources by default, never the host's filesystem.

Arrays are julia 1.11+'s: `GenericMemory` and `GenericMemoryRef` values with the builtins `memorynew`, `memoryrefnew`, `memoryrefoffset`, `memoryrefget`, `memoryrefset!` and `memoryref_isassigned` (`builtins.c:1711-1856`, `genericmemory.c`), and `Array{T,N}(undef, dims…)` as `boot.jl` builds it over them, with the overflow-checked integer intrinsics it needs. Atomic memory and the atomic read-modify-write builtins are not here yet. Values are shown as julia shows them, checked against it: arrays with the element-type prefix of `arrayshow.jl` (`Int8[1, 2]`, `Any[#undef, 1]`, `[1 2 3; 4 5 6]`, `[1 3; 2 4;;; 5 7; 6 8]`, `Matrix{Int64}(undef, 0, 3)`, `fill(7)`), types with julia's where clauses and the aliases of the array and memory types (`Vector{Int64}`, `Matrix{T} where T<:Integer`, `Memory`, `Array{Vector{T}, 1} where T`). Of julia's alias search only those aliases are matched, by shape: the general search needs type intersection. A `MemoryRef` shows its element's index where julia shows its address. An `Expr`'s `args` is one `Vector{Any}` per expression (`ex.args === ex.args`) whose memory is the expression's own arguments: a write through it, replacing its memory or size (as `push!` does), and `setfield!` of the `Expr`'s `head` or `args` all reach what macro expansion and lowering read.

Checked against julia: 97 Core-only programs (`interp/testdata/snippets.jl`: integer and float intrinsics on every width, overflow and division by zero, tuples, `===`, `isa`, typed methods and dispatch, varargs, try/catch nested in functions and loops, undefined locals and globals, constants, recursion to `StackOverflowError`, structs, mutable structs, singletons, abstract hierarchies with dispatch, struct redefinition, outer constructors, `where` clauses with static parameters and bounds, `TypeVar` and `UnionAll`, structs with one or two parameters, parametric abstract supertypes, bounds, parameters no field constrains, macros with quotes and interpolation, hygiene, nested macro calls, `__source__` and `__module__`, splatting) show the same value in Go and in julia 1.12.6; 100% statement coverage; fuzzed (`FuzzEval`).

Not yet: Base (a program that calls `+`, `println` or `a.b` reaches a Base function and gets an `UndefVarError` for it — so does a constructor call whose argument needs `convert` to the field type; so do macros that use `esc` or Base's macros such as `@inline`), splatting anything but a tuple or a simple vector (it calls `Base.iterate`), redefining a struct that has parameters (lowering reads the old one through `Base.getproperty`), `const` and atomic fields, julia's quoted show of an `Expr` (`:(f(2))`; it is shown as the constructor call `Expr(:call, :f, 2)`, which reads back), indexing arrays (`getindex` is Base), strings beyond literals, `finally`. The plain integer division intrinsics follow arm64, where the goldens are made: C division, undefined on a zero divisor, does not trap there (x86_64 julia raises `DivideError`). Not measured yet: speed against julia's interpreter (`--compile=min`).

Untrusted cells: `VM.MaxSteps` bounds the statements a VM runs (`while true end` is valid Julia), `VM.MaxDepth` bounds call and try-block nesting with a `StackOverflowError`, and `Session.Eval` reports a broken invariant as `interp.ErrInternal` instead of panicking. `include` reads only `VM.Sources` (julia's own embedded sources unless the embedder supplies others), and an include that nests without end raises `StackOverflowError` like a call. `VM.MaxMemoryElems` (16 Mi elements by default) bounds one `memorynew`: a larger allocation stops with `interp.ErrMemoryLimit` instead of exhausting the host. The total a VM allocates is not bounded yet ([#24](https://github.com/go-embedded-julia/julia/issues/24)).

The VM's file system has two modes (`interp/vfs.go`), one layer under every file call Base makes (`jl_stat` and its buffer, `ios_*` of an `IOStream`, libuv's `uv_fs_*`, `jl_fs_access`, `getattrlist`). In the sandbox, the default, only julia's own tree exists: the Base sources and the stdlibs (`FileWatching`, `Libdl`, `Artifacts`, `SHA`, `Sockets`, `LinearAlgebra`, `Random` and the JLLs `LinearAlgebra` depends on, embedded unmodified, `interp/stdlib.md`), mounted where `Sys.BINDIR`, `Sys.STDLIB` and `include` look for them (`<BinDir>/../share/julia`); the tree is read-only, and no path leaves it (`..`, an absolute path outside the mount, a link: the embedded tree holds none). With `VM.Host` set the tree stays mounted at its paths and every other path is the real file system, through package `os`. Reading is ported; writing a file is refused in the sandbox (`EROFS`) and not ported on the host yet.

Dynamic libraries (`interp/dlload.go`) are the ones the interpreter implements in Go (GMP, MPFR, PCRE2, dSFMT so far, and the libraries the JLLs open). They sit, as julia's bundled libraries do, in `lib/julia` beside `Sys.BINDIR`, under julia 1.12.6's darwin file names. `dlopen` finds them by those names, by `@rpath/`, `@loader_path/` or `@executable_path/` and by absolute path, and `dlpath`, `dlsym`, `dlclose` and a `ccall` through a `dlsym` pointer work on them. Any other library cannot be opened, in the sandbox or on the host, and `dlopen` fails with julia's message: loading a C library is a stated divergence. A library that is open but whose functions are not written yet (OpenBLAS: the routines go-blas has, in single and double precision, are forwarded; the rest, #67) refuses each call as not ported, never silently.

Tasks and the event loop (`interp/taskswitch.go`, `taskget.go`, `uvloop.go`, `cfunction.go`) are what `@async`, `schedule`, `yield`, `wait`, `Channel`, `Timer` and `sleep` need from the runtime. A task is a goroutine and a baton channel says whose turn it is, so exactly one goroutine runs julia code at any time; `jl_new_task`, `jl_switch`, `_start_task` and `jl_finish_task` are ported from `task.c`, and the per-task VM state (world age, call depth, exception stack, typeinf timing, finalizers inhibited) is saved and loaded at each switch (the inventory is the doc comment of `taskswitch.go`). `jl_task_get_next` is `scheduler.c`'s for one thread: when no task is runnable the libuv loop, written in Go (async and timer handles, `uv_ref`/`uv_unref`, `jl_close_uv`, `jl_process_events`), runs once. `@cfunction` evaluates to a `Ptr{Cvoid}` that holds the callable and its signature, which the loop calls. The thread calls answer as `julia -t 1` does. Two bounds protect the host: `VM.MaxTasks` (started tasks that have not finished) and `VM.MaxHandles`, and a task that waits when nothing can wake it (no task runnable, no referenced handle that a timer or a send can fire) is refused with `ErrDeadlock` naming the waiting task, where julia would sleep for ever (a stated divergence). Call `VM.Close` when the VM is done, to end the goroutines of tasks that were left waiting.

## Use

```go
import (
	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/syntax"
)

ex := syntax.ParseAll([]byte("f(x) = x^2 + 1"), "cell.jl") // Meta.parseall
fmt.Println(expr.Serialize(ex))
// (:toplevel {L 1 :"cell.jl"} (:"=" (:call :f :x) (:block {L 1 :"cell.jl"} (:call :"+" (:call :"^" :x 2) 1))))
```

- `syntax.ParseAll` — the Expr tree julia's `Meta.parseall` returns, errors
  included; `syntax.ParseAllLimits` with explicit `Limits`.
- `syntax.Tokenize`, `syntax.TokenDump`, `syntax.GreenDump` — the lexer and
  the lossless green tree, for tools.
- `lower.NewState().Lower(ex)` — `Meta.lower` of a macro-expanded top-level
  form: an `Expr(:thunk, CodeInfo)`, an atom, or `Expr(:error)`; one
  `State` per module, in file order.
- `interp.New().NewSession().Eval(src)` — parse, lower and run a cell of
  Core-only code in a module of its own, returning the last value
  (`interp.Repr` shows it as julia does); state persists across `Eval` calls
  of one session, as across notebook cells.
- `vm.Boot()` — evaluate julia's `boot.jl` in the VM's Core before running
  code, as julia's Core is defined; `vm.CoreSession().Eval(src)` then runs
  code in Core.
- `expr` — the Go values of Julia's syntax tree (`*expr.Expr`, `expr.Symbol`,
  `expr.LineNumberNode`, `expr.QuoteNode`, `expr.GlobalRef`, `expr.Char` …)
  and `expr.Serialize`, the canonical type-tagged text form.

Parsing is bounded (`syntax.DefaultLimits`: 64 MiB of input, 4000 levels of
nesting): a hostile notebook cell can make the parser return an error, not
hang it or exhaust memory. The parser is fuzzed (`FuzzLex`, `FuzzGreen`,
`FuzzParse`), and differentially against julia: with `JLGO_ORACLE=1`,
`TestParserFailuresAgreeWithJulia` mutates the snippets and checks every
input both ways — `ErrParserFailed` exactly where JuliaSyntax throws, the
same `Expr` everywhere else (200 002 inputs: 7 failures on both sides,
0 differences; CI runs 50 000 on every pull request).

## Running cells: Kernel

`interp.Kernel` is the session a notebook keeps: one VM, one module (`Main`),
one cell at a time.

```go
k, err := interp.FetchKernel(ctx, "v0.14.0", digest, interp.FetchOptions{},
	interp.LoadOptions{}, interp.KernelOptions{})   // or LoadKernel(r, …), or NewKernel(vm, …)
if err != nil { … }
defer k.Close()
r := k.Eval(ctx, `f(x) = x + 1; f(41)`)
// r.Value "42" (julia's text/plain display), r.Stdout, r.Stderr (this cell's
// only), r.Err (nil, a *JuliaError with julia's showerror text, or a typed error)
```

- **Cancellation.** Cancelling `ctx` raises `InterruptException` in the running
  cell (also inside a `try`, which cannot swallow it, and in `sleep` or `wait`);
  `Eval` answers `ErrCellCanceled`, wrapping `ctx.Err()`. The kernel runs the
  next cell.
- **Budgets per cell** (`CellOptions`, on the kernel or per `EvalWith`; zero is
  the kernel's value, negative is no bound): `Timeout` (`ErrCellTimeout`),
  `MaxSteps` (`ErrStepLimit`), `MaxAlloc`, the elements allocated in total
  (`ErrMemoryLimit`), `MaxOutput` per stream (the rest is dropped,
  `Result.Truncated`). `DefaultCellOptions` is 30 s, 2^25 elements and 1 MiB.
- **Sandbox by default.** The filesystem is julia's own tree and the environment
  is empty. Host mode is `KernelOptions{Host: true}`; a VM already in host mode
  is refused (`ErrKernelHost`) without it.
- **One kernel per VM** (`ErrKernelExists`), and one running cell per kernel
  (`ErrKernelBusy`). Kernels of different VMs run side by side in one process:
  what they share is core's type tables, which are guarded, and the hints an
  image sets on core's shared types, which an image sets once and another
  image may not change (`ErrImageConflict`, #49), so kernels over images of
  one release are safe and a second release is refused at its load.
- **WebAssembly.** The Kernel runs as a `js/wasm` binary (the playground's
  platform): CI runs the kernel tests under node with `wasm_exec`, loading an
  image from bytes with its digest (`LoadOptions.Digest`). A program that ships
  the image as a static asset passes the bytes in; `FetchImage` needs the
  network and a cache directory and is for programs that have them.
  `cmd/jlkernel` is a small program that does it from the command line
  (`jlkernel --image FILE --digest SHA256 < cell`), the one that measures the
  load of an image under node.
- **Soft scope, as the REPL and IJulia have it.** A loop or a block at the top
  of a cell assigns to a global that exists instead of making a local:
  `s = 0` in one cell and `for i in 1:10; s += i; end` in the next gives
  `s == 55`. It is `REPL.softscope` (stdlib/REPL/src/REPL.jl:145-161) with
  lowering's `softscope` and `defined-julia-global` (julia-syntax.scm:3233-3250,
  ast.c:172-182), checked on the cases of julia's `test/syntax.jl:2113-2221`.
  `CellOptions{HardScope: true}` gives the scope of a file instead.
- **The Julia-Go value table.** `k.Get(name)` reads a global of `Main` as a Go
  value and `k.Set(name, v)` assigns one, as a cell `name = v` would (also
  over an existing binding). Both copy, never alias; both are refused with
  `ErrKernelBusy` while a cell runs. `Set` checks a slice's length (and a
  `Matrix`'s `Rows*Cols`, overflow-checked, against `len(Data)`) against
  `VM.MaxMemoryElems` before it converts anything, so a refused slice costs a
  few KiB. `k.Has(name)` and `k.TypeOf(name)` (julia's type as text, e.g.
  `Matrix{Float64}`, also for a type the table does not convert) answer
  without copying; they return an error, not a bare `false`, while a cell runs.

  | julia | Go |
  |---|---|
  | `Int64` | `int64` (`Set` also takes an `int`) |
  | `Float64` | `float64`, every bit kept: NaN payloads, `-0.0` |
  | `Bool`, `String` | `bool`, `string` |
  | `Vector{Float64}`, `Vector{Int64}` | `[]float64`, `[]int64` |
  | `Matrix{Float64}`, `Matrix{Int64}` | `interp.Matrix[float64]`, `interp.Matrix[int64]`: `Rows`, `Cols`, `Data` column-major, `At(i, j)` |

  `Get` answers `(value, ok, err)`: `ok` is false for a name that is not bound;
  any other type is a `*ConversionError` (`ErrKernelType`) that names the julia
  type, and `Set` of a Go type outside the table names the Go type.
- **Reset.** `k.Reset()` puts the kernel back in the state `NewKernel` returned
  it in, for the next notebook run: what the cells defined, assigned, imported,
  overloaded or mutated is gone (the generator of `rand` included), measured
  against a fresh kernel cell for cell (`TestKernelResetBase`). It costs what
  the cells changed, about 0.6 ms for a corpus of 18 cells that wrote 3790 times
  to the image, not a load (about 0.85 s): every write to an object the image
  holds is logged (`setfield!`, `swapfield!`, `modifyfield!`, `replacefield!`,
  `setfieldonce!`, memory stores and copies) and replayed in reverse, the
  methods of the functions a cell defined into are reverted to the world of the
  start, and the bindings of every module are put back. Past `DefaultMaxUndo`
  (262144) writes `Reset` loads the image again and says so
  (`ResetInfo.Reloaded`), or, for a kernel made by `NewKernel` from a VM and so
  without an image, answers `ErrResetOverflow` and changes nothing. It is
  refused with `ErrKernelBusy` while a cell runs. Before it rolls back, `Reset`
  stops the tasks a cell started that have not finished and closes the libuv
  timers it opened (`ResetInfo.Tasks`, `ResetInfo.Timers`; `k.Tasks()` counts
  them for an embedder): a stopped task never runs again and **none of its
  `finally` or `catch` blocks runs**, since user code during a `Reset` could
  print or write the state it restores. Base's own tasks are left alone. A
  kernel yields once when it is made, so that the tasks Base makes in its
  initializers are parked before the state `Reset` returns to. Not undone, by
  design: Go-side state reached by a `ccall` (files, sockets, the environment,
  the working directory, the output already written, libgmp and libmpfr), the
  caches (types a cell made stay in the weak type caches
  until collected, specializations stay), limits set through `k.VM()` between
  cells, and the world and object-id counters, which only move forward.
- **Without Base** (a `Boot()`-only image) the value and the error are the Go
  repr, and cells use `Core`'s names; with the Base image they are julia's.

## The system image

`SaveImage` / `LoadImage` write and read a booted VM (Base built to the cut
where julia writes `sys.so`). Each release publishes its image as the asset
`julia-image-vX.Y.Z.bin`, with `julia-image-vX.Y.Z.sha256`; the workflow
`.github/workflows/image.yml` builds it when the release is published and
writes the SHA-256 in the release notes. The key of an image holds the module
version (`ImageVersion`: a clean tag only, never `(devel)`, a pseudo-version
or `+dirty`), so the builder and a program that imports `julia@vX` ask for
the same image.

An image is not authenticated by its key or its trailer, and loading one
writes state the whole process shares, so the consumer pins the digest as
`go.sum` does (the digest of a version cannot be in that version's own
source): `interp.FetchImage(ctx, "vX.Y.Z", digest, interp.FetchOptions{})`
downloads over https within a size bound, refuses any other digest with
`ErrImageDigest` before the bytes reach `LoadImage`, and keeps the verified
file in the user cache directory (hashed again at every use). A program that
holds the image itself (a static asset, bytes it was given) pins it the same
way: `LoadOptions{Digest: &digest}` makes `LoadImage` and `LoadKernel` hash the
whole input first and refuse any other bytes with `ErrImageDigest`, before the
key, the framing or an object is read; `nil` (the default) does not pin. `images.sum`
at the root lists the digest of each release; a pull request after the
release adds its line. A manual build of the asset: `JLGO_BASE=1
JLGO_IMAGE_VERSION=vX.Y.Z JLGO_IMAGE_OUT=out.bin go test ./interp -run
TestBuildImage`.

## The oracle

`internal/oracle` runs the reference julia as a persistent process speaking a
line protocol (base64 payloads); `oracle.jl` is the julia side. Golden files
are regenerated by one command:

```sh
go run ./cmd/jloracle regen     # unicode tables, snippets, corpus, programs
go run ./cmd/jloracle bench     # Go vs julia parse time on the corpus
```

The julia is `pkgx julia@1.12.6`, or `$JLGO_JULIA`. Tests replay the golden
files without julia; `JLGO_ORACLE=1` also runs the live checks, and
`JLGO_CORPUS=<julia>/share/julia` runs the corpus comparison (CI does both,
with the official release tarball).

| file | what julia answered |
|---|---|
| `testdata/golden/snippets.jsonl` | tokens, green tree, Expr of each snippet |
| `testdata/golden/corpus.jsonl` | digests of the three levels for every corpus file |
| `testdata/golden/lowering-snippets.jsonl` | macro expansion and `Meta.lower` of each form of `testdata/lowering-snippets.jl` |
| `testdata/golden/lowering.jsonl` | per corpus file: forms lowered, digests of julia's lowered forms, forms the Go lowering reproduces |
| `testdata/golden/lowering-hygiene.jsonl`, `lowering-exprs.jsonl` | `Meta.lower` of the expressions `testdata/lowering-hygiene.jl` and `lowering-exprs.jl` build |
| `testdata/golden/lowering-cover.jsonl` | the 366 smallest corpus forms that together reach every statement the corpus reaches, replayed offline |
| `testdata/golden/deparse.jsonl`, `lisp.jsonl` | femtolisp's answers (`julia --lisp`) for `deparse` and front-end functions |
| `testdata/golden/programs.jsonl` | parse, `Meta.lower` and evaluation of `testdata/programs/*.jl` (for M2+) |
| `interp/testdata/eval.golden` | the value julia shows for each program of `interp/testdata/snippets.jl` (`JLGO_ORACLE=1 JLGO_UPDATE_INTERP=1`) |
| `internal/unitab/tables_gen.go` | utf8proc's Unicode 16 properties the lexer uses |

## Layout

| package | role |
|---|---|
| `expr` | Julia syntax-tree values, canonical serialisation |
| `syntax` | the parser (port of JuliaSyntax: `tokenize.go`, `parse_stream.go`, `parser*.go`, `build.go`, `errors.go`) |
| `lower` | lowering to `CodeInfo` (port of `src/julia-syntax.scm`, `macroexpand.scm`, `ast.scm`, `match.scm`) |
| `core` | Julia's type objects, apply_type, subtyping and specificity (port of jltypes.c, subtype.c) |
| `core/dispatch` | method tables, world ages, dispatch of concrete calls (after gf.c) |
| `interp` | the interpreter of lowered code (port of interpreter.c, with builtins.c and runtime_intrinsics.c in part) |
| `internal/unitab` | Unicode tables from the reference build, utf8proc's grapheme breaks |
| `internal/utf8proc` | a Go port of utf8proc v2.10.0 (Unicode 16.0.0), the library `Base.Unicode` calls: case mappings, category, width, grapheme breaks, normalization; tables generated from utf8proc's own data (`jloracle utf8proc`), checked over every code point against julia 1.12.6 (`uppercase`, `lowercase`, `normalize` work in a cell) |
| `internal/oracle` | the reference julia as a differential oracle |
| `internal/gen`, `cmd/jloracle` | the golden-file generators |

## Licence

BSD 3-Clause (`LICENSE`). The parser is a translation of JuliaSyntax.jl and
parts of Julia and utf8proc, all MIT-licensed; their notices are in `NOTICE`
and in the header of each translated file.
