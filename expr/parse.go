package expr

import (
	"errors"
	"fmt"
	"math"
	"strconv"
)

// ErrSyntax is wrapped by the errors of Parse.
var ErrSyntax = errors.New("expr: malformed serialisation")

// MaxParseDepth bounds the nesting Parse accepts, so that a hostile golden
// file cannot exhaust the stack.
const MaxParseDepth = 10000

// Parse reads the canonical text form written by Serialize (and by the
// reference oracle) back into Go values: Parse(Serialize(v)) is v, and
// Serialize(Parse(s)) is s for every s Serialize can write.
func Parse(s string) (any, error) {
	p := &parser{s: s}
	v, err := p.value(0)
	if err != nil {
		return nil, err
	}
	if p.i != len(s) {
		return nil, p.fail("trailing text")
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) fail(msg string) error {
	return fmt.Errorf("%w: %s at byte %d", ErrSyntax, msg, p.i)
}

func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) expect(c byte) error {
	if p.peek() != c {
		return p.fail("expected " + strconv.QuoteRune(rune(c)))
	}
	p.i++
	return nil
}

// space consumes the single separator between elements.
func (p *parser) space() error { return p.expect(' ') }

// word reads up to the next space or closing bracket.
func (p *parser) word() string {
	j := p.i
	for j < len(p.s) && p.s[j] != ' ' && p.s[j] != ')' && p.s[j] != ']' && p.s[j] != '}' {
		j++
	}
	w := p.s[p.i:j]
	p.i = j
	return w
}

func (p *parser) value(depth int) (any, error) {
	if depth > MaxParseDepth {
		return nil, p.fail("nesting too deep")
	}
	switch c := p.peek(); {
	case c == '(':
		p.i++
		head, err := p.symbol()
		if err != nil {
			return nil, err
		}
		args, err := p.sequence(depth, ')')
		if err != nil {
			return nil, err
		}
		return New(head, args...), nil
	case c == '[':
		p.i++
		elts, err := p.sequence(depth, ']')
		if err != nil {
			return nil, err
		}
		return Vector(elts), nil
	case c == ':':
		return p.symbol()
	case c == '"':
		return p.str()
	case c == '{':
		return p.tagged(depth)
	}
	switch w := p.word(); w {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "nothing":
		return nil, nil
	default:
		n, err := strconv.ParseInt(w, 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != w {
			return nil, p.fail("unknown atom " + strconv.Quote(w))
		}
		return n, nil
	}
}

// sequence reads ` elt` items up to the closing byte.
func (p *parser) sequence(depth int, end byte) ([]any, error) {
	out := []any{}
	first := true
	for p.peek() != end {
		if !first || end == ')' {
			if err := p.space(); err != nil {
				return nil, err
			}
		}
		first = false
		v, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	p.i++
	return out, nil
}

func (p *parser) symbol() (Symbol, error) {
	if err := p.expect(':'); err != nil {
		return "", err
	}
	if p.peek() == '"' {
		s, err := p.str()
		if err != nil {
			return "", err
		}
		if isSimpleSymbol(s) {
			return "", p.fail("quoted simple symbol")
		}
		return Symbol(s), nil
	}
	w := p.word()
	if !isSimpleSymbol(w) {
		return "", p.fail("bad symbol " + strconv.Quote(w))
	}
	return Symbol(w), nil
}

// str reads a quoted string: \" \\ and \xHH escapes, other bytes verbatim.
func (p *parser) str() (string, error) {
	start := p.i
	if err := p.expect('"'); err != nil {
		return "", err
	}
	var b []byte
	for {
		if p.i >= len(p.s) {
			return "", p.fail("unterminated string")
		}
		c := p.s[p.i]
		p.i++
		switch c {
		case '"':
			// Only the canonical spelling is accepted, so that a parsed
			// golden serialises back to the same bytes.
			s := string(b)
			if Quote(s) != p.s[start:p.i] {
				return "", p.fail("non-canonical string")
			}
			return s, nil
		case '\\':
			if p.i >= len(p.s) {
				return "", p.fail("unterminated escape")
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\':
				b = append(b, e)
			case 'x':
				if p.i+2 > len(p.s) {
					return "", p.fail("short \\x escape")
				}
				v, err := strconv.ParseUint(p.s[p.i:p.i+2], 16, 8)
				if err != nil {
					return "", p.fail("bad \\x escape")
				}
				b = append(b, byte(v))
				p.i += 2
			default:
				return "", p.fail("unknown escape")
			}
		default:
			b = append(b, c)
		}
	}
}

func (p *parser) tagged(depth int) (any, error) {
	p.i++ // {
	tag := p.word()
	switch tag {
	case "Unreachable":
		return Unreachable{}, p.expect('}')
	case "M":
		var path []Symbol
		for p.peek() == ' ' {
			p.i++
			s, err := p.symbol()
			if err != nil {
				return nil, err
			}
			path = append(path, s)
		}
		return Module{Path: path}, p.expect('}')
	case "CodeInfo", "DI":
		var fields []any
		for p.peek() == ' ' {
			p.i++
			v, err := p.value(depth + 1)
			if err != nil {
				return nil, err
			}
			fields = append(fields, v)
		}
		if err := p.expect('}'); err != nil {
			return nil, err
		}
		if tag == "DI" {
			return debugInfoFrom(fields, p)
		}
		ci, ok := CodeInfoFrom(fields)
		if !ok {
			return nil, p.fail("malformed CodeInfo")
		}
		return ci, nil
	}
	if err := p.space(); err != nil {
		return nil, err
	}
	var v any
	var err error
	switch tag {
	case "L":
		v, err = p.lineNumber(depth)
	case "Q":
		var x any
		x, err = p.value(depth + 1)
		v = QuoteNode{Value: x}
	case "G":
		v, err = p.globalRef()
	case "ParseError":
		var s string
		s, err = p.str()
		v = &ParseError{Msg: s}
	case "?":
		v, err = p.opaque()
	case "GotoIfNot":
		v, err = p.gotoIfNot(depth)
	case "Return":
		var x any
		x, err = p.value(depth + 1)
		v = ReturnNode{Val: x}
	case "Newvar":
		var x any
		x, err = p.value(depth + 1)
		v = NewvarNode{Slot: x}
	case "Enter":
		v, err = p.enter(depth)
	case "LI":
		v, err = p.lineInfo()
	default:
		v, err = p.scalar(tag, p.word())
	}
	if err != nil {
		return nil, err
	}
	return v, p.expect('}')
}

func debugInfoFrom(f []any, p *parser) (any, error) {
	if len(f) != 4 {
		return nil, p.fail("malformed DebugInfo")
	}
	edges, ok1 := f[2].(Vector)
	locs, ok2 := f[3].(string)
	if !ok1 || !ok2 {
		return nil, p.fail("malformed DebugInfo")
	}
	return &DebugInfo{Def: f[0], Linetable: f[1], Edges: edges, Codelocs: locs}, nil
}

func (p *parser) lineNumber(depth int) (any, error) {
	n, err := p.int(p.word())
	if err != nil {
		return nil, err
	}
	if err := p.space(); err != nil {
		return nil, err
	}
	f, err := p.value(depth + 1)
	if err != nil {
		return nil, err
	}
	return LineNumberNode{Line: int(n), File: f}, nil
}

func (p *parser) lineInfo() (any, error) {
	f, err := p.symbol()
	if err != nil {
		return nil, err
	}
	var n [2]int32
	for i := range n {
		if err := p.space(); err != nil {
			return nil, err
		}
		v, err := p.int(p.word())
		if err != nil {
			return nil, err
		}
		if int64(int32(v)) != v {
			return nil, p.fail("line info out of Int32 range")
		}
		n[i] = int32(v)
	}
	return LineInfoNode{File: f, Line: n[0], InlinedAt: n[1]}, nil
}

func (p *parser) globalRef() (any, error) {
	m, err := p.symbol()
	if err != nil {
		return nil, err
	}
	if err := p.space(); err != nil {
		return nil, err
	}
	n, err := p.symbol()
	if err != nil {
		return nil, err
	}
	return GlobalRef{Mod: m, Name: n}, nil
}

func (p *parser) opaque() (any, error) {
	t, err := p.str()
	if err != nil {
		return nil, err
	}
	if err := p.space(); err != nil {
		return nil, err
	}
	r, err := p.str()
	if err != nil {
		return nil, err
	}
	return Opaque{Type: t, Repr: r}, nil
}

func (p *parser) gotoIfNot(depth int) (any, error) {
	c, err := p.value(depth + 1)
	if err != nil {
		return nil, err
	}
	if err := p.space(); err != nil {
		return nil, err
	}
	d, err := p.int(p.word())
	if err != nil {
		return nil, err
	}
	return GotoIfNot{Cond: c, Dest: int(d)}, nil
}

func (p *parser) enter(depth int) (any, error) {
	d, err := p.int(p.word())
	if err != nil {
		return nil, err
	}
	e := EnterNode{Catch: int(d)}
	if p.peek() == ' ' {
		p.i++
		e.Scope, err = p.value(depth + 1)
		e.HasScope = true
	}
	return e, err
}

// int reads a canonical decimal int (no leading zeros or plus sign).
func (p *parser) int(w string) (int64, error) {
	n, err := strconv.ParseInt(w, 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != w {
		return 0, p.fail("bad integer " + strconv.Quote(w))
	}
	return n, nil
}

func (p *parser) uint(w string, bits int) (uint64, error) {
	n, err := strconv.ParseUint(w, 10, bits)
	if err != nil || strconv.FormatUint(n, 10) != w {
		return 0, p.fail("bad integer " + strconv.Quote(w))
	}
	return n, nil
}

func (p *parser) hex(w string, digits int) (uint64, error) {
	if len(w) != 2+digits || w[:2] != "0x" {
		return 0, p.fail("bad hex " + strconv.Quote(w))
	}
	n, err := strconv.ParseUint(w[2:], 16, 64)
	if err != nil || fmt.Sprintf("%0*x", digits, n) != w[2:] {
		return 0, p.fail("bad hex " + strconv.Quote(w))
	}
	return n, nil
}

// scalar reads the value of a {Type value} tag.
func (p *parser) scalar(tag, w string) (any, error) {
	var err error
	var n int64
	var u uint64
	switch tag {
	case "Int8", "Int16", "Int32":
		n, err = p.int(w)
		bits := map[string]int{"Int8": 8, "Int16": 16, "Int32": 32}[tag]
		if err == nil && (n < -(1<<(bits-1)) || n >= 1<<(bits-1)) {
			err = p.fail("integer out of range")
		}
		switch tag {
		case "Int8":
			return int8(n), err
		case "Int16":
			return int16(n), err
		}
		return int32(n), err
	case "UInt8":
		u, err = p.uint(w, 8)
		return uint8(u), err
	case "UInt16":
		u, err = p.uint(w, 16)
		return uint16(u), err
	case "UInt32":
		u, err = p.uint(w, 32)
		return uint32(u), err
	case "UInt64":
		u, err = p.uint(w, 64)
		return u, err
	case "Float64":
		u, err = p.hex(w, 16)
		return math.Float64frombits(u), err
	case "Float32":
		u, err = p.hex(w, 8)
		return math.Float32frombits(uint32(u)), err
	case "Char":
		u, err = p.hex(w, 8)
		return Char(u), err
	case "SSA", "Slot", "Arg", "Goto":
		n, err = p.int(w)
		switch tag {
		case "SSA":
			return SSAValue(n), err
		case "Slot":
			return SlotNumber(n), err
		case "Arg":
			return Argument(n), err
		}
		return GotoNode(n), err
	}
	if !isSimpleSymbol(tag) || w == "" {
		return nil, p.fail("unknown tag " + strconv.Quote(tag))
	}
	return Tagged{Type: tag, Text: w}, nil
}
