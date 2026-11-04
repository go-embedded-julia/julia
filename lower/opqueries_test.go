package lower

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/go-embedded-julia/julia/expr"
)

// The parser's operator queries Base's show asks through ccall, against
// julia 1.12.6's answers (testdata/opqueries.golden: the name, then
// _isoperator, isunaryoperator, is_unary_and_binary_operator,
// is_syntactic_operator and operator_precedence).
func TestOperatorQueriesAgainstJulia(t *testing.T) {
	b, err := os.ReadFile("testdata/opqueries.golden")
	if err != nil {
		t.Fatal(err)
	}
	bit := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		name, _, _ := strings.Cut(line, "\t")
		got := fmt.Sprintf("%s\t%d\t%d\t%d\t%d\t%d", name, bit(IsOperator(expr.Symbol(name))), bit(IsUnaryOperator(name)),
			bit(IsUnaryAndBinaryOperator(name)), bit(IsSyntacticOperator(name)), OperatorPrecedence(name))
		if got != line {
			t.Errorf("got  %q\njulia %q", got, line)
		}
	}
}
