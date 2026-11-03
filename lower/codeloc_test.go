package lower

import (
	"fmt"
	"testing"
)

// CompressCodelocs and UncompressCodeloc against julia 1.12.6's
// jl_compress_codelocs and jl_uncompress1_codeloc
// (interp/testdata/irflags.jl), for pc 0 to n+1: one-, two- and four-byte
// entries, julia's pc of 0 for four-byte ones included.
func TestCodelocsAgainstJulia(t *testing.T) {
	for _, c := range []struct {
		first int32
		locs  []int32
		bytes []byte
		want  string
	}{
		{3, []int32{5, 0, 0, 6, 1, 2, 300, 0, 0},
			[]byte{0x03, 0x00, 0x00, 0x00, 0x29, 0x01, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x00, 0x03, 0x00, 0x00, 0x00, 0x04, 0x00, 0x01, 0x02, 0x2a, 0x01, 0x00, 0x00},
			"[(3, 0, 0) (5, 0, 0) (6, 1, 2) (300, 0, 0) (-1, 0, 0)]"},
		{1, []int32{70000, 300, 70000, 2, 1, 1},
			[]byte{0x01, 0x00, 0x00, 0x00, 0x6f, 0x11, 0x01, 0x00, 0x70, 0x11, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x70, 0x11, 0x01, 0x00, 0x2c, 0x01, 0x00, 0x00, 0x70, 0x11, 0x01, 0x00, 0x02, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00},
			"[(1, 0, 0) (70000, 300, 0) (2, 1, 0) (-1, 0, 0)]"},
		{10, []int32{11, 0, 0, 12, 0, 0},
			[]byte{0x0a, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x02, 0x03},
			"[(10, 0, 0) (11, 0, 0) (12, 0, 0) (-1, 0, 0)]"},
	} {
		cl := CompressCodelocs(c.first, c.locs)
		if cl != string(c.bytes) {
			t.Errorf("CompressCodelocs(%d, %v) = % x, julia % x", c.first, c.locs, cl, c.bytes)
		}
		var got []string
		for pc := 0; pc <= len(c.locs)/3+1; pc++ {
			l, to, at := UncompressCodeloc(cl, pc)
			got = append(got, fmt.Sprintf("(%d, %d, %d)", l, to, at))
		}
		if s := fmt.Sprint(got); s != c.want {
			t.Errorf("UncompressCodeloc of %v: %s, julia %s", c.locs, s, c.want)
		}
	}
	// (the exported wrappers interp uses: julia's Base.isoperator answers)
	if !IsOperator("+") || !IsOperator("==") || IsOperator("x") {
		t.Error("IsOperator")
	}
	var st State
	if st.SetTop("Base"); st.top != "Base" {
		t.Error("SetTop")
	}
	// two-byte to and pc read back what was written
	two := CompressCodelocs(1, []int32{5, 300, 300})
	if l, to, at := UncompressCodeloc(two, 1); l != 5 || to != 300 || at != 300 {
		t.Errorf("two-byte entries: (%d, %d, %d)", l, to, at)
	}
	// no locations, a negative statement, and strings too short for their
	// header, which julia would read past
	full := CompressCodelocs(3, []int32{5, 0, 0, 6, 1, 2, 300, 0, 0})
	for _, c := range []struct {
		cl   string
		pc   int
		want string
	}{
		{"", 0, "(0, 0, 0)"},
		{full, -1, "(-1, 0, 0)"},
		{full[:8], 0, "(-1, 0, 0)"},
		{"\x01\x00\x00\x00\xff\xff\x00\x00\x00\x00\x00\x00", 0, "(-1, 0, 0)"},
		{full[:len(full)-1], 3, "(-1, 0, 0)"},
		{full, 4, "(-1, 0, 0)"},
	} {
		l, to, at := UncompressCodeloc(c.cl, c.pc)
		if s := fmt.Sprintf("(%d, %d, %d)", l, to, at); s != c.want {
			t.Errorf("UncompressCodeloc(% x, %d) = %s, want %s", c.cl, c.pc, s, c.want)
		}
	}
}
