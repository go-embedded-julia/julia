// Package syntax is a Go port of JuliaSyntax, the parser of Julia, in the
// version Julia 1.12.6 ships (JuliaSyntax 29ec5226f7b1).
//
// The port follows JuliaSyntax file by file and function by function, and is
// checked against the reference julia at each level it implements. This
// first step is the lexer: Tokenize and TokenDump are tokenize.jl's
// Tokenize.tokenize.
package syntax
