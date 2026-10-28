package expr

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// values covers every Go value the serialisation knows.
var values = []any{
	New("call", Symbol("f"), int64(-3), "s\"\\\x01\xff", true, false, nil),
	Symbol("a b"), Symbol("x"), int8(-8), int16(16), int32(-32), uint8(8), uint16(16), uint32(32),
	uint64(math.MaxUint64), 1.5, float32(-2.5), math.Inf(1), Char(0x61000000), EOFChar,
	LineNumberNode{Line: 3, File: Symbol("f.jl")}, LineNumberNode{Line: 0, File: nil},
	QuoteNode{Value: New("block")}, GlobalRef{Mod: "Base", Name: "+"}, &ParseError{Msg: "boom"},
	SSAValue(1), SlotNumber(2), Argument(3), GotoNode(4), GotoIfNot{Cond: SSAValue(1), Dest: 9},
	ReturnNode{Val: nil}, Unreachable{}, NewvarNode{Slot: SlotNumber(1)},
	LineInfoNode{File: "f.jl", Line: 3, InlinedAt: -1},
	EnterNode{Catch: 5}, EnterNode{Catch: 6, Scope: SSAValue(2), HasScope: true},
	Module{Path: []Symbol{"Main", "L"}}, Module{}, Tagged{Type: "Int128", Text: "-170141183460469231731687303715884105728"},
	Opaque{Type: "DataType", Repr: "Any"}, Vector{}, Vector{int64(1), Vector{Symbol("x")}},
	&DebugInfo{Def: Symbol("none"), Edges: Vector{}, Codelocs: "\x01\x00"},
	&CodeInfo{Code: Vector{ReturnNode{Val: int64(1)}}, DebugInfo: &DebugInfo{Edges: Vector{}},
		SSAValueTypes: int64(1), SSAFlags: Vector{uint32(0)}, SlotNames: Vector{Symbol("x")},
		SlotFlags: Vector{uint8(8)}, MinWorld: uint64(1), IsVA: false, Purity: uint16(0)},
	New("thunk", New("block", New("=", Symbol("x"), int64(1)))),
}

func TestParseRoundTrip(t *testing.T) {
	for _, v := range values {
		s := Serialize(v)
		got, err := Parse(s)
		if err != nil {
			t.Errorf("%s: %v", s, err)
			continue
		}
		if Serialize(got) != s {
			t.Errorf("%s: came back as %s", s, Serialize(got))
		}
	}
}

// TestParseGoldens parses every serialised tree of the golden files and
// checks that it serialises back to the same bytes.
func TestParseGoldens(t *testing.T) {
	n := 0
	for _, name := range []string{"snippets.jsonl", "lowering-snippets.jsonl"} {
		data, err := os.ReadFile(filepath.Join("..", "testdata", "golden", name))
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		for dec.More() {
			var rec map[string]any
			if err := dec.Decode(&rec); err != nil {
				t.Fatal(err)
			}
			var trees []string
			if e, ok := rec["expr"].(string); ok {
				trees = append(trees, e)
			}
			if forms, ok := rec["forms"].([]any); ok {
				for _, f := range forms {
					m := f.(map[string]any)
					trees = append(trees, m["input"].(string))
					if m["tag"] == "F" {
						trees = append(trees, m["output"].(string))
					}
				}
			}
			for _, s := range trees {
				v, err := Parse(s)
				if err != nil {
					t.Fatalf("%s: %v", s, err)
				}
				if Serialize(v) != s {
					t.Fatalf("%s: came back as %s", s, Serialize(v))
				}
				n++
			}
		}
	}
	if n < 1000 {
		t.Fatalf("only %d trees", n)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		strings.Repeat("[", MaxParseDepth+2), ":x ", "(1)", "(:f :)", "[:]", "abc", "01", "", "(:f:x)",
		"[:a(:b)]", `:"a"`, `:"abc`, `"abc`, `"\`, `"\x1`, `"\xzz"`, `"\n"`, `"\x41"`,
		"{Unreachable ", "{M :}", "{M :a", "{CodeInfo (}", "{CodeInfo 1", "{DI 1}", "{DI 1 2 3 4}",
		"{CodeInfo 1}", "{L}", "{L x :f}", "{L 1}", "{L 1 (}", "{L 01 :f}", "{Q (}", "{G 1 :x}",
		"{G :a}", "{G :a 1}", "{ParseError 1}", `{? 1 "x"}`, `{? "a"}`, `{? "a" 1}`,
		"{GotoIfNot ( 1}", "{GotoIfNot :x}", "{GotoIfNot :x y}", "{Return (}", "{Newvar (}",
		"{LI 1 2 3}", "{LI :f}", "{LI :f x 0}", "{LI :f 0 x}", "{LI :f 3000000000 0}", "{LI :f 1}",
		"{Enter x}", "{Enter 1 (}", "{Int8 300}", "{Int16 -40000}", "{Int32 x}", "{Int8 x}",
		"{UInt8 -1}", "{UInt16 x}", "{UInt32 x}", "{UInt64 x}", "{Float64 0x1}",
		"{Float64 0xZZZZZZZZZZZZZZZZ}", "{Float64 0x00000000000000AA}", "{Float32 1}", "{Char 1}",
		"{SSA x}", "{Slot x}", "{Arg x}", "{Goto x}", "{9x 1}", "{Foo}", "{Foo }", "{SSA 1 2}",
		`["a""b"]`, "{CodeInfo" + strings.Repeat(" 1", 23) + "}",
	}
	for _, s := range bad {
		if v, err := Parse(s); err == nil || !errors.Is(err, ErrSyntax) {
			t.Errorf("%q: %v, %v", s, v, err)
		}
	}
}

// FuzzParseSerialized: Parse never panics, and whatever it accepts
// serialises back to the input.
func FuzzParseSerialized(f *testing.F) {
	for _, v := range values {
		f.Add(Serialize(v))
	}
	f.Add("{CodeInfo [] nothing 0 [] [] [] nothing nothing nothing [] 1 1 nothing 0 false false false false false 0 0 0 0}")
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<16 {
			return
		}
		v, err := Parse(s)
		if err != nil {
			return
		}
		if Serialize(v) != s {
			t.Fatalf("%q came back as %q", s, Serialize(v))
		}
	})
}
