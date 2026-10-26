package main

import "testing"

func TestGoSide(t *testing.T) {
	if goSide.Tokens([]byte("x")) != "Identifier 0 0 0 0\nEndMarker 1 0 0 0\n" {
		t.Fatalf("tokens: %q", goSide.Tokens([]byte("x")))
	}
	if g := goSide.Green([]byte("x")); g != "(toplevel 0 1 (Identifier 0 1))" {
		t.Fatalf("green: %q", g)
	}
}
