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
| M3 Core runtime | types, subtyping, method specificity, dispatch, builtins, intrinsics | **in progress** — types, subtyping and specificity done (`core`); dispatch of concrete calls done (`core/dispatch`); interpreter (`interp`): lowered code over Core, `struct` and `abstract type` definitions with their default constructors, 23 builtins, the integer and float intrinsics; parametric types, `boot.jl`, the remaining builtins and intrinsics next |
| M4 – M7 | Base, numeric stdlib, notebook bridge, wasm playground | planned |

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

### Types and subtyping (core)

core — Julia's type objects, apply_type, and subtyping (a port of jltypes.c and subtype.c of 1.12.6, without intersection): identical to julia on 20 000 random pairs and 4 902 cases from test/subtype.jl; and method specificity (jl_type_morespecific), identical to Base.morespecific on the same corpus.

core/dispatch — method tables with world ages, and the method a call with concrete argument types runs (the most specific applicable one, or an ambiguity), with its static parameters: identical to julia's `which` and dispatch environment on 1 500 random method tables and 9 000 calls (4 675 method choices, 1 968 of them with static parameters, 87 ambiguities). Not yet ported: the interference sets julia 1.12 computes at method insertion, which need type intersection.

Speed: subtyping is **1.49× slower** than julia's `jl_subtype` on one core (3.63 ms against 2.44 ms for the 20 000 pairs), an open defect; the time goes to allocation.

### Interpreter (interp)

interp — a port of julia's `src/interpreter.c` that runs lowered code: `prepare` checks every SSA value, slot and label of a `CodeInfo` once, `evalBody` walks the statements (goto, conditional goto, return, try/catch through `EnterNode`/`leave`/`pop_exception`, assignment, method definition, global and constant declarations, world ages, `import`/`using`/`export`), `evalValue` evaluates operands and calls. Generic functions dispatch through `core/dispatch` in the task's world. `struct` and `abstract type` definitions run through the builtins lowering calls (`_structtype`, `_abstracttype`, `_setsuper!`, `_typebody!`, `_equiv_typedef`, `_defaultctors`), which build the default outer and inner constructors as `jl_ctor_def` does, their bodies lowered by `lower` (`DefaultInnerCtorBody`, `DefaultOuterCtorBody`); calling a type dispatches on its constructors, and user-defined outer constructors join them. Core holds the built-in types, the exception and AST types of `boot.jl` and `jltypes.c`, 23 builtins (`===`, `typeof`, `isa`, `<:`, `typeassert`, `throw`, `ifelse`, `tuple`, `svec`, `getfield`, `nfields`, `fieldtype`, `getglobal`, `setglobal!`, `isdefinedglobal`, `get_binding_type`, `apply_type` and the six type-definition builtins) and the integer and float intrinsics, with julia's argument checks and messages.

Checked against julia: 65 Core-only programs (`interp/testdata/snippets.jl`: integer and float intrinsics on every width, overflow and division by zero, tuples, `===`, `isa`, typed methods and dispatch, varargs, try/catch nested in functions and loops, undefined locals and globals, constants, recursion to `StackOverflowError`, structs, mutable structs, singletons, abstract hierarchies with dispatch, struct redefinition, outer constructors) show the same value in Go and in julia 1.12.6; 100% statement coverage; fuzzed (`FuzzEval`).

Not yet: Base (a program that calls `+`, `println` or `a.b` reaches a Base function and gets an `UndefVarError` for it — so does a constructor call whose argument needs `convert` to the field type), macro expansion, types with parameters and `where` clauses (the `TypeVar` constructor comes from `boot.jl`), `const` and atomic fields, arrays, strings beyond literals, `finally`. The plain integer division intrinsics follow arm64, where the goldens are made: C division, undefined on a zero divisor, does not trap there (x86_64 julia raises `DivideError`). Not measured yet: speed against julia's interpreter (`--compile=min`).

Untrusted cells: `VM.MaxSteps` bounds the statements a VM runs (`while true end` is valid Julia), `VM.MaxDepth` bounds call and try-block nesting with a `StackOverflowError`, and `Session.Eval` reports a broken invariant as `interp.ErrInternal` instead of panicking.

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
| `internal/oracle` | the reference julia as a differential oracle |
| `internal/gen`, `cmd/jloracle` | the golden-file generators |

## Licence

BSD 3-Clause (`LICENSE`). The parser is a translation of JuliaSyntax.jl and
parts of Julia and utf8proc, all MIT-licensed; their notices are in `NOTICE`
and in the header of each translated file.
