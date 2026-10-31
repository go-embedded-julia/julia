package interp

import "testing"

func TestExplore(t *testing.T) {
	src := `import Core.Intrinsics: add_int, slt_int
x = 1
function f(n)
    s = 0
    i = 0
    while slt_int(i, n)
        s = add_int(s, i)
        i = add_int(i, 1)
    end
    s
end
r = try
    throw(5)
catch e
    e
end
(f(4), x, r)
`
	s := New().NewSession()
	v, err := s.Eval(src)
	t.Logf("%s %v", Repr(v), err)
}
