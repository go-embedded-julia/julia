package lower

// The conversions between julia values and femtolisp S-expressions at the
// boundary of the front end: julia_to_scm and scm_to_julia of src/ast.c,
// jl_new_code_info_from_ir and jl_linetable_to_debuginfo of src/method.c,
// and jl_compress_codelocs of src/ircode.c (julia 1.12.6).

import (
	"encoding/binary"
	"strings"

	"github.com/go-embedded-julia/julia/expr"
)

// toSx is julia_to_scm_.
func toSx(v any) any {
	switch x := v.(type) {
	case expr.Symbol:
		return x
	case bool:
		if x {
			return list(sTrue)
		}
		return list(sFalse)
	case nil:
		return list(sNull)
	case *expr.Expr:
		out := make([]any, 1+len(x.Args))
		out[0] = x.Head
		for i, a := range x.Args {
			out[i+1] = toSx(a)
		}
		return out
	case expr.LineNumberNode:
		return list(sLine, toSx(int64(x.Line)), toSx(x.File))
	case expr.GotoNode:
		return list(sym("goto"), int64(x))
	case expr.QuoteNode:
		return list(sInert, toSx(x.Value))
	case expr.NewvarNode:
		return list(sym("newvar"), toSx(x.Slot))
	case expr.GlobalRef:
		if x.Mod == "Core" {
			return list(sCore, x.Name)
		}
		return list(sym("globalref"), &jlValue{expr.Module{Path: []expr.Symbol{x.Mod}, Obj: x.Obj}}, x.Name)
	case expr.SSAValue:
		errorf("SSAValue objects should not occur in an AST")
	case expr.SlotNumber:
		errorf("SlotNumber objects should not occur in an AST")
	case int64:
		return x // a fixnum
	}
	// an opaque julia value, wrapped anew for each occurrence
	return &jlValue{v}
}

// converter is scm_to_julia for one lowering: mod is the module lowering
// runs in, (thismodule).
type converter struct {
	mod      expr.Module
	top      expr.Symbol  // (top x) resolves here: Base, or Core inside Core
	notImage map[sym]bool // modules of GlobalRefs known not to be in the image
}

// base is jl_base_relative_to(mod) for the modules lowering runs in.
var baseModule = expr.Module{Path: []expr.Symbol{"Base"}}

func (c *converter) fromSx(e any) any {
	switch x := e.(type) {
	case int64:
		return x
	case sym:
		return x
	case sstr:
		return string(x)
	case []any:
		return c.fromList(x)
	case bool:
		panic(internalError("boolean in lowered output"))
	}
	return unbox(e)
}

func (c *converter) fromList(e []any) any {
	if len(e) > 0 {
		switch e[0] {
		case sym("ssavalue"):
			return expr.SSAValue(e[1].(int64))
		case sym("slot"):
			return expr.SlotNumber(e[1].(int64))
		case sNull:
			if len(e) == 1 {
				return nil
			}
		case sTrue:
			if len(e) == 1 {
				return true
			}
		case sFalse:
			if len(e) == 1 {
				return false
			}
		}
	}
	var hd sym
	var args []any
	if len(e) > 0 {
		if s, ok := e[0].(sym); ok {
			hd, args = s, e[1:]
		}
	}
	if hd == "" {
		hd, args = "list", e
	}
	n := len(args)
	switch {
	case hd == sLine && (n == 1 || n == 2):
		ln := c.fromSx(args[0])
		var file any
		if n == 2 {
			file = c.fromSx(args[1])
		}
		line, _ := ln.(int64)
		return expr.LineNumberNode{Line: int(line), File: file}
	case hd == "lineinfo" && n == 3:
		file, _ := c.fromSx(args[0]).(sym)
		line, _ := c.fromSx(args[1]).(int64)
		at, _ := c.fromSx(args[2]).(int64)
		return expr.LineInfoNode{File: file, Line: int32(line), InlinedAt: int32(at)}
	case hd == "goto":
		return expr.GotoNode(c.fromSx(args[0]).(int64))
	case hd == "gotoifnot":
		return expr.GotoIfNot{Cond: c.fromSx(args[0]), Dest: int(c.fromSx(args[1]).(int64))}
	case hd == "enter":
		en := expr.EnterNode{Catch: int(c.fromSx(args[0]).(int64))}
		if n == 2 {
			en.Scope, en.HasScope = c.fromSx(args[1]), true
		}
		return en
	case hd == "newvar":
		return expr.NewvarNode{Slot: c.fromSx(args[0])}
	case hd == "globalref":
		m := c.fromSx(args[0]).(expr.Module)
		if len(m.Path) > 1 && m.Path[0] == "Main" {
			// a module defined under Main at run time (a macro's, from a
			// hygienic scope) is not in the system image
			if c.notImage == nil {
				c.notImage = map[sym]bool{}
			}
			c.notImage[m.Path[len(m.Path)-1]] = true
		}
		return expr.GlobalRef{Mod: m.Path[len(m.Path)-1], Name: c.fromSx(args[1]).(sym), Obj: m.Obj}
	case hd == sTop:
		return expr.GlobalRef{Mod: c.top, Name: c.fromSx(args[0]).(sym)}
	case hd == sCore:
		return expr.GlobalRef{Mod: "Core", Name: c.fromSx(args[0]).(sym)}
	case hd == "thismodule":
		return c.mod
	case n > 0 && (hd == sInert || (hd == sQuote && !isPair(args[0]))):
		return expr.QuoteNode{Value: c.fromSx(args[0])}
	case hd == sLambda:
		return c.codeInfo(args)
	}
	out := make([]any, n)
	for i, a := range args {
		out[i] = c.fromSx(a)
	}
	if hd == "list" {
		return expr.Vector(out)
	}
	return expr.New(hd, out...)
}

// codeInfo is jl_new_code_info_from_ir of the converted lambda
// (lambda (list args…) (list (list vinfo…) captured nssa sparams) (block
// stmts…) (list locs…) (list (lineinfo …)…)).
func (c *converter) codeInfo(args []any) *expr.CodeInfo {
	arglist := asList(args[0])[1:]
	vinfo := asList(args[1])[1:]
	body := asList(args[2])[1:]
	locs := asList(args[3])[1:]
	var linetable []LineInfo
	for _, li := range asList(args[4])[1:] {
		l := asList(li)
		linetable = append(linetable, LineInfo{c.fromSx(l[1]), int32(l[2].(int64)), int32(l[3].(int64))})
	}
	codelocs := make([]int64, len(locs))
	for i, l := range locs {
		codelocs[i] = l.(int64)
	}
	code := make(expr.Vector, len(body))
	for i, st := range body {
		code[i] = c.fromSx(st)
	}
	vis := asList(vinfo[0])[1:]
	slots := make([]Slot, len(vis))
	for i, v := range vis {
		vi := asList(v)[1:]
		slots[i] = Slot{vi[0].(sym), vi[2].(int64)}
	}
	return NewCodeInfo(len(arglist), slots, vinfo[2].(int64), code, codelocs, linetable, c.inImage)
}

// LineInfo is a (lineinfo file line inlined-at) entry of a lambda's line
// table, a LegacyLineInfoNode.
type LineInfo struct {
	File    any
	Line    int32
	Inlined int32
}

// Slot is a lambda's variable: its name and its vinfo flags.
type Slot struct {
	Name  expr.Symbol
	Flags int64
}

// NewCodeInfo is jl_new_code_info_from_ir (method.c:384) and
// jl_code_info_set_ir of a lambda's parts: nargs arguments, its slots, the
// number of SSA values, its statements (julia values), their locations in
// linetable. inImage tells the modules of the system image, for
// has_image_globalref. jl_method_def uses it for a method whose body a
// closure made at run time (method.c:1246-1251).
func NewCodeInfo(nargs int, slots []Slot, nssa int64, code expr.Vector, codelocs []int64, linetable []LineInfo, inImage func(expr.Symbol) bool) *expr.CodeInfo {
	ci := &expr.CodeInfo{
		SlotTypes: nil, RetType: expr.Opaque{Type: "DataType", Repr: "Any"}, Parent: nil,
		Edges: expr.Vector{}, MinWorld: uint64(1), MaxWorld: ^uint64(0), MethodForInferenceLH: nil,
		NArgs: uint64(nargs), PropagateInbounds: false, HasFcall: false,
		HasImageGlobalref: false, NoSpecializeInfer: false, IsVA: false,
		Inlining: uint8(0), Constprop: uint8(0), Purity: uint16(0), InliningCost: uint16(0xffff),
	}
	ci.DebugInfo = linetableToDebugInfo(codelocs, linetable)
	ci.Code = code
	ssaflags := make(expr.Vector, len(code))
	inbounds := 0
	var inlineFlags []bool // true: inline region, false: noinline region
	var purity []*expr.Expr
	var purityBits uint16
	for j, st := range code {
		flagStmt := false
		e, isExpr := st.(*expr.Expr)
		switch {
		case isExpr && e.Head == "meta":
			kept := e.Args[:0:0]
			for _, ma := range e.Args {
				switch ma {
				case sym("inline"):
					ci.Inlining = uint8(1)
				case sym("noinline"):
					ci.Inlining = uint8(2)
				case sym("propagate_inbounds"):
					ci.PropagateInbounds = true
				case sym("nospecializeinfer"):
					ci.NoSpecializeInfer = true
				case sym("aggressive_constprop"):
					ci.Constprop = uint8(1)
				case sym("no_constprop"):
					ci.Constprop = uint8(2)
				default:
					if expr.Is(ma, "purity") {
						pe := ma.(*expr.Expr)
						for k, b := range pe.Args {
							if b == true {
								purityBits |= 1 << k
							}
						}
						continue
					}
					kept = append(kept, ma)
				}
			}
			if len(kept) == 0 {
				code[j] = nil
			} else {
				code[j] = expr.New("meta", kept...)
			}
		case isExpr && e.Head == "inbounds":
			flagStmt = true
			switch e.Args[0] {
			case true:
				inbounds++
			case false:
				inbounds = 0
			default:
				if inbounds > 0 {
					inbounds--
				}
			}
			code[j] = nil
		case isExpr && (e.Head == "inline" || e.Head == "noinline"):
			flagStmt = true
			if e.Args[0] == true {
				inlineFlags = append(inlineFlags, e.Head == "inline")
			} else {
				inlineFlags = inlineFlags[:len(inlineFlags)-1]
			}
			code[j] = nil
		case isExpr && e.Head == "purity":
			flagStmt = true
			if len(e.Args) == numEffectsOverrides {
				purity = append(purity, e)
			} else {
				purity = purity[:len(purity)-1]
			}
			code[j] = nil
		case isExpr && e.Head == "boundscheck":
			flagStmt = true
		case isExpr && e.Head == "return":
			code[j] = expr.ReturnNode{Val: e.Args[0]}
		default:
			if gr, ok := st.(expr.GlobalRef); ok {
				if inImage(gr.Mod) {
					ci.HasImageGlobalref = true
				}
				break
			}
			if isExpr && e.Head == "=" {
				st = e.Args[1]
			}
			if expr.Is(st, "foreigncall") || expr.Is(st, "cfunction") {
				ci.HasFcall = true
			}
		}
		var flag uint32
		if !flagStmt {
			if inbounds > 0 {
				flag |= irFlagInbounds
			}
			if len(inlineFlags) > 0 {
				if inlineFlags[len(inlineFlags)-1] {
					flag |= 1 << 1
				} else {
					flag |= 1 << 2
				}
			}
			for _, pe := range purity {
				for k := 0; k < numEffectsOverrides; k++ {
					if pe.Args[k] == true {
						flag |= 1 << (numIRFlags + k)
					}
				}
			}
		}
		ssaflags[j] = flag
	}
	ci.Purity = purityBits
	ci.SSAFlags = ssaflags

	ci.SSAValueTypes = nssa
	names := make(expr.Vector, len(slots))
	flags := make(expr.Vector, len(slots))
	for i, v := range slots {
		name := v.Name
		if i > 0 && name != unusedSym && strings.HasPrefix(string(name), "#") {
			// convention for renamed variables: #...#original_name
			if k := strings.IndexByte(string(name[1:]), '#'); k >= 0 {
				name = name[k+2:]
			} else if len(name) > 1 && name[1] == 's' {
				name = "" // compiler-generated temporaries, #sXXX
			}
		}
		names[i] = name
		flags[i] = uint8(v.Flags) & (8 | 16 | 32 | 64)
	}
	ci.SlotNames = names
	ci.SlotFlags = flags
	return ci
}

// The ssaflags layout of julia 1.12.6 (src/julia_internal.h).
const (
	irFlagInbounds      = 1 << 0
	numIRFlags          = 3
	numEffectsOverrides = 11
)

const unusedSym sym = "#unused#"

// inImage is jl_object_in_image of a module, which is true of every module
// loaded from the system image or a package image: all the modules the
// code refers to except those created at run time, the module being
// lowered and the oracle's macro-context modules (Context_*).
func (c *converter) inImage(m sym) bool {
	return m != c.mod.Path[len(c.mod.Path)-1] && !strings.HasPrefix(string(m), "Context_") && !c.notImage[m]
}

// linetableToDebugInfo is jl_linetable_to_debuginfo.
func linetableToDebugInfo(codelocsAny []int64, linetable []LineInfo) *expr.DebugInfo {
	top := linetable[0]
	type edge struct {
		file any
		sub  *[]*edge
		locs []int32 // triples
	}
	var edges []*edge
	var addEdge func(list *[]*edge, stack *[]LineInfo) (int32, int32)
	addEdge = func(list *[]*edge, stack *[]LineInfo) (int32, int32) {
		li := (*stack)[len(*stack)-1]
		*stack = (*stack)[:len(*stack)-1]
		var e *edge
		i := 0
		for ; i < len(*list); i++ {
			if equal((*list)[i].file, li.File) {
				e = (*list)[i]
				break
			}
		}
		if e == nil {
			e = &edge{file: li.File, sub: new([]*edge)}
			*list = append(*list, e)
		}
		to := int32(i + 1)
		var subTo, subPC int32
		if len(*stack) > 0 {
			subTo, subPC = addEdge(e.sub, stack)
		}
		k := 0
		for ; k < len(e.locs); k += 3 {
			if e.locs[k] == li.Line && e.locs[k+1] == subTo && e.locs[k+2] == subPC {
				break
			}
		}
		if k == len(e.locs) {
			e.locs = append(e.locs, li.Line, subTo, subPC)
		}
		return to, int32(k/3 + 1)
	}
	codelocs := make([]int32, 0, 3*len(codelocsAny))
	var stack []LineInfo
	for _, idx := range codelocsAny {
		for idx != 0 {
			li := linetable[idx-1]
			idx = int64(li.Inlined)
			stack = append(stack, li)
		}
		var line, to, pc int32
		if len(stack) > 0 {
			li := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if equal(li.File, top.File) {
				line = li.Line
			} else {
				stack = append(stack, li)
			}
			if len(stack) > 0 {
				to, pc = addEdge(&edges, &stack)
			}
		}
		codelocs = append(codelocs, line, to, pc)
	}
	var alloc func(list []*edge) expr.Vector
	alloc = func(list []*edge) expr.Vector {
		out := make(expr.Vector, len(list))
		for i, e := range list {
			out[i] = &expr.DebugInfo{Def: e.file, Linetable: nil, Edges: alloc(*e.sub),
				Codelocs: CompressCodelocs(0, e.locs)}
		}
		return out
	}
	return &expr.DebugInfo{Def: top.File, Linetable: nil, Edges: alloc(edges),
		Codelocs: CompressCodelocs(top.Line, codelocs)}
}

// UncompressCodeloc is jl_uncompress1_codeloc (ircode.c:1425-1433, over
// codelocs_parseheader and unpack_codeloc, 956-981 and 1352-1420): the
// (line, to, pc) of statement pc (0 the first line), (-1, 0, 0) out of
// range. julia reads past a malformed string; a short one is out of range
// here. With 4-byte entries julia's switch never assigns pc (it has case 3
// for 4): pc is 0 then.
func UncompressCodeloc(cl string, pc int) (line, to, at int32) {
	if len(cl) == 0 {
		return 0, 0, 0
	}
	if len(cl) < 12 {
		return -1, 0, 0
	}
	header := func(i int) int32 { return int32(binary.LittleEndian.Uint32([]byte(cl[4*i : 4*i+4]))) }
	lineOffset := header(0)
	lineBytes, toBytes := 4, 4
	switch h := header(1); {
	case h < 255:
		lineBytes = 1
	case h < 65535:
		lineBytes = 2
	}
	switch h := header(2); {
	case h == 0:
		toBytes = 0
	case h < 255:
		toBytes = 1
	case h < 65535:
		toBytes = 2
	}
	if len(cl) < 12+lineBytes {
		return -1, 0, 0
	}
	nstmts := (len(cl) - 12 - lineBytes) / (lineBytes + toBytes*2)
	if pc < 0 || pc > nstmts {
		return -1, 0, 0
	}
	// pc ≤ nstmts keeps every read inside cl
	off := 12
	if pc == 0 {
		toBytes = 0
	} else {
		off += lineBytes + (pc-1)*(lineBytes+toBytes*2)
	}
	read := func(n int) int32 {
		var v uint32
		for i := n - 1; i >= 0; i-- {
			v = v<<8 | uint32(cl[off+i])
		}
		off += n
		return int32(v)
	}
	if line = read(lineBytes); line > 0 {
		line += lineOffset - 1
	}
	to = read(toBytes)
	if at = read(toBytes); toBytes == 4 {
		at = 0
	}
	return line, to, at
}

// CompressCodelocs is jl_compress_codelocs (ircode.c:1446-1566): a
// DebugInfo's codelocs, three Int32 per statement, as julia's compressed
// string (little-endian, as julia writes it on the platforms the goldens
// come from).
func CompressCodelocs(firstline int32, codelocs []int32) string {
	nstmts := len(codelocs) / 3
	allzero := true
	for _, v := range codelocs {
		if v != 0 {
			allzero = false
			break
		}
	}
	if allzero {
		return ""
	}
	const imax = int32(^uint32(0) >> 1)
	mn := [3]int32{imax, imax, imax}
	if firstline > 0 {
		mn = [3]int32{firstline, firstline, firstline}
	}
	var mx [3]int32
	for i := 0; i < nstmts; i++ {
		cl := codelocs[3*i : 3*i+3]
		if cl[0] > 0 && cl[0] < mn[0] {
			mn[0] = cl[0]
		}
		for k := 0; k < 3; k++ {
			if k > 0 && cl[k] < mn[k] {
				mn[k] = cl[k]
			}
			if cl[k] > mx[k] {
				mx[k] = cl[k]
			}
		}
	}
	var header [3]int32
	if mn[0] <= mx[0] {
		header[0] = mn[0]
		header[1] = mx[0] - mn[0]
	}
	header[2] = max(mx[1], mx[2])
	width := func(v int32, zero int) int {
		switch {
		case v == 0 && zero == 0:
			return 0
		case v < 255:
			return 1
		case v < 65535:
			return 2
		}
		return 4
	}
	lineBytes := width(header[1], 1)
	toBytes := width(header[2], 0)
	out := make([]byte, 0, 12+lineBytes+nstmts*(lineBytes+2*toBytes))
	for _, h := range header {
		out = binary.LittleEndian.AppendUint32(out, uint32(h))
	}
	put := func(v int32, n int) {
		switch n {
		case 1:
			out = append(out, byte(v))
		case 2:
			out = binary.LittleEndian.AppendUint16(out, uint16(v))
		case 4:
			out = binary.LittleEndian.AppendUint32(out, uint32(v))
		}
	}
	rel := func(line int32) int32 {
		if line > 0 {
			return line - header[0] + 1
		}
		return 0
	}
	put(rel(firstline), lineBytes)
	for i := 0; i < nstmts; i++ {
		put(rel(codelocs[3*i]), lineBytes)
		put(codelocs[3*i+1], toBytes)
		put(codelocs[3*i+2], toBytes)
	}
	return string(out)
}
