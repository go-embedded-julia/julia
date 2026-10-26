// Package syntax is a Go port of JuliaSyntax, the parser of Julia, in the
// version Julia 1.12.6 ships (JuliaSyntax 29ec5226f7b1). It turns Julia
// source text into Base.Expr trees, represented by the values of package
// expr, identical to those the reference julia produces.
//
// The port follows JuliaSyntax file by file and function by function, and is
// checked against the reference julia at each level it implements:
//
//   - Tokenize / TokenDump: the raw lexer (tokenize.go is tokenize.jl);
//   - GreenDump: the lossless green tree and the diagnostics of the parse
//     stream (parse_stream.go is parse_stream.jl, parser*.go are parser.jl);
//   - ParseAll: the Expr tree (build.go is expr.jl), as Meta.parseall
//     returns it.
//
// Parsing is bounded: input size and nesting depth are limited (see
// Limits), so that hostile input cannot hang the parser or exhaust memory.
package syntax
