package core

import "testing"

// Regression: an empty but non-nil environment against a y that is not a
// UnionAll panicked (a type assertion on y, which julia's C only casts),
// found by the dispatch differential test.
func TestSubtypeEnvEmptyAndTooLong(t *testing.T) {
	x := mustApply(AnyTupleType, Int64Type)
	y := mustApply(AnyTupleType, RealType)
	if sub, err := SubtypeEnv(x, y, []any{}); err != nil || !sub {
		t.Fatalf("empty env: %v, %v; want true, nil", sub, err)
	}
	if _, err := SubtypeEnv(x, y, make([]any, 1)); err == nil {
		t.Fatal("an env longer than y's variables was accepted")
	}
}
