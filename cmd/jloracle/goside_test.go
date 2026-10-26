package main

import "testing"

func TestGoSide(t *testing.T) {
	if goSide.Tokens([]byte("x")) != "Identifier 0 0 0 0\nEndMarker 1 0 0 0\n" {
		t.Fatalf("tokens: %q", goSide.Tokens([]byte("x")))
	}
}
