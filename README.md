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
| M2 lowering | port of `src/julia-syntax.scm` → `CodeInfo` | in progress — see lowering parity below |
| M3 – M7 | Core runtime, Base, numeric stdlib, notebook bridge, wasm playground | planned |

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
| corpus (`share/julia`: base, Compiler, stdlib, test) | 53 997 | 1 601 (atoms and top-level-only forms) | 28 / 1191 |
| `testdata/lowering-snippets.jl` | 124 | 5 | — |

A further 3 317 corpus forms are not counted. Their macro expansion throws
in the oracle, mostly test files using macros from packages that are not
loaded.

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
| `testdata/golden/programs.jsonl` | parse, `Meta.lower` and evaluation of `testdata/programs/*.jl` (for M2+) |
| `internal/unitab/tables_gen.go` | utf8proc's Unicode 16 properties the lexer uses |

## Layout

| package | role |
|---|---|
| `expr` | Julia syntax-tree values, canonical serialisation |
| `syntax` | the parser (port of JuliaSyntax: `tokenize.go`, `parse_stream.go`, `parser*.go`, `build.go`, `errors.go`) |
| `lower` | lowering to `CodeInfo` (port of `src/julia-syntax.scm`, in progress) |
| `internal/unitab` | Unicode tables from the reference build, utf8proc's grapheme breaks |
| `internal/oracle` | the reference julia as a differential oracle |
| `internal/gen`, `cmd/jloracle` | the golden-file generators |

## Licence

BSD 3-Clause (`LICENSE`). The parser is a translation of JuliaSyntax.jl and
parts of Julia and utf8proc, all MIT-licensed; their notices are in `NOTICE`
and in the header of each translated file.
