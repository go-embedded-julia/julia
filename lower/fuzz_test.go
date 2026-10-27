package lower

import (
	"errors"
	"testing"
	"time"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/syntax"
)

// fuzzLimits bound what FuzzLower parses: lowering is then bounded by
// MaxDepth and by the size of the tree.
var fuzzLimits = syntax.Limits{MaxBytes: 1 << 14, MaxDepth: 2 * MaxDepth}

// fuzzBudget is the time one fuzz input may take to parse and lower.
const fuzzBudget = 2 * time.Second

// FuzzLower parses any input and lowers each of its top-level forms:
// lowering must return a value or an error (never panic) within its
// budget, and an error must be one of the package's.
func FuzzLower(f *testing.F) {
	for _, sn := range readGolden[gen.LoweringSnippet](f, "lowering-snippets.jsonl") {
		f.Add([]byte(sn.Src))
	}
	f.Add([]byte("macro m(x) Expr(:toplevel, x) end"))
	f.Fuzz(func(t *testing.T, src []byte) {
		start := time.Now()
		ex, err := syntax.ParseAllLimits(src, "fuzz", fuzzLimits)
		if err != nil {
			return
		}
		top, ok := ex.(*expr.Expr)
		if !ok {
			return
		}
		st := NewState()
		for _, a := range top.Args {
			if _, ok := a.(expr.LineNumberNode); ok {
				continue
			}
			_, err := st.Lower(a)
			if err != nil && !errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrInternal) && !errors.Is(err, ErrLimit) {
				t.Fatalf("%s: foreign error %v", expr.Serialize(a), err)
			}
		}
		if d := time.Since(start); d > fuzzBudget {
			t.Fatalf("%d bytes took %v", len(src), d)
		}
	})
}

// TestLowerDepthLimit: a tree deeper than MaxDepth is refused, not walked.
func TestLowerDepthLimit(t *testing.T) {
	var v any = expr.Symbol("x")
	for range MaxDepth + 5 {
		v = expr.New("call", expr.Symbol("f"), v)
	}
	if _, err := NewState().Lower(v); !errors.Is(err, ErrLimit) {
		t.Fatalf("deep call: %v", err)
	}
	v = expr.Symbol("x")
	for range MaxDepth + 5 {
		v = expr.QuoteNode{Value: expr.Vector{v}}
	}
	if !tooDeep(v, 0) {
		t.Fatal("deep quote not seen")
	}
}
