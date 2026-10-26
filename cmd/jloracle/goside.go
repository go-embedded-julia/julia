package main

import "github.com/go-embedded-julia/julia/syntax"

// The levels of the Go parser that exist, for the corpus manifest.
func init() {
	goSide.Tokens = func(src []byte) string { return syntax.TokenDump(src) }
}
