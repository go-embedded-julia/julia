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
		return list(sym("globalref"), expr.Module{Path: []expr.Symbol{x.Mod}}, x.Name)
	case expr.SSAValue:
		errorf("SSAValue objects should not occur in an AST")
	case expr.SlotNumber:
		errorf("SlotNumber objects should not occur in an AST")
	}
	// int64 is a fixnum; everything else an opaque julia value.
	return v
}

// converter is scm_to_julia for one lowering: mod is the module lowering
// runs in, (thismodule).
type converter struct {
	mod expr.Module
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
	return e
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
		return expr.GlobalRef{Mod: m.Path[len(m.Path)-1], Name: c.fromSx(args[1]).(sym)}
	case hd == sTop:
		return expr.GlobalRef{Mod: "Base", Name: c.fromSx(args[0]).(sym)}
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

// lineInfo is a (lineinfo file line inlined-at) entry of the line table.
type lineInfo struct {
	file    any
	line    int32
	inlined int32
}

// codeInfo is jl_new_code_info_from_ir of the converted lambda
// (lambda (list args…) (list (list vinfo…) captured nssa sparams) (block
// stmts…) (list locs…) (list (lineinfo …)…)).
func (c *converter) codeInfo(args []any) *expr.CodeInfo {
	arglist := asList(args[0])[1:]
	vinfo := asList(args[1])[1:]
	body := asList(args[2])[1:]
	locs := asList(args[3])[1:]
	var linetable []lineInfo
	for _, li := range asList(args[4])[1:] {
		l := asList(li)
		linetable = append(linetable, lineInfo{c.fromSx(l[1]), int32(l[2].(int64)), int32(l[3].(int64))})
	}
	ci := &expr.CodeInfo{
		SlotTypes: nil, RetType: expr.Opaque{Type: "DataType", Repr: "Any"}, Parent: nil,
		Edges: expr.Vector{}, MinWorld: uint64(1), MaxWorld: ^uint64(0), MethodForInferenceLH: nil,
		NArgs: uint64(len(arglist)), PropagateInbounds: false, HasFcall: false,
		HasImageGlobalref: false, NoSpecializeInfer: false, IsVA: false,
		Inlining: uint8(0), Constprop: uint8(0), Purity: uint16(0), InliningCost: uint16(0xffff),
	}
	codelocs := make([]int64, len(locs))
	for i, l := range locs {
		codelocs[i] = l.(int64)
	}
	ci.DebugInfo = linetableToDebugInfo(codelocs, linetable)

	code := make(expr.Vector, len(body))
	for i, st := range body {
		code[i] = c.fromSx(st)
	}
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
				if c.inImage(gr.Mod) {
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

	vis := asList(vinfo[0])[1:]
	ci.SSAValueTypes = vinfo[2].(int64)
	names := make(expr.Vector, len(vis))
	flags := make(expr.Vector, len(vis))
	for i, v := range vis {
		vi := asList(v)[1:]
		name := vi[0].(sym)
		if i > 0 && name != unusedSym && strings.HasPrefix(string(name), "#") {
			// convention for renamed variables: #...#original_name
			if k := strings.IndexByte(string(name[1:]), '#'); k >= 0 {
				name = name[k+2:]
			} else if len(name) > 1 && name[1] == 's' {
				name = "" // compiler-generated temporaries, #sXXX
			}
		}
		names[i] = name
		flags[i] = uint8(vi[2].(int64)) & (8 | 16 | 32 | 64)
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
	return m != c.mod.Path[len(c.mod.Path)-1] && !strings.HasPrefix(string(m), "Context_")
}

// linetableToDebugInfo is jl_linetable_to_debuginfo.
func linetableToDebugInfo(codelocsAny []int64, linetable []lineInfo) *expr.DebugInfo {
	top := linetable[0]
	type edge struct {
		file any
		sub  *[]*edge
		locs []int32 // triples
	}
	var edges []*edge
	var addEdge func(list *[]*edge, stack *[]lineInfo) (int32, int32)
	addEdge = func(list *[]*edge, stack *[]lineInfo) (int32, int32) {
		li := (*stack)[len(*stack)-1]
		*stack = (*stack)[:len(*stack)-1]
		var e *edge
		i := 0
		for ; i < len(*list); i++ {
			if equal((*list)[i].file, li.file) {
				e = (*list)[i]
				break
			}
		}
		if e == nil {
			e = &edge{file: li.file, sub: new([]*edge)}
			*list = append(*list, e)
		}
		to := int32(i + 1)
		var subTo, subPC int32
		if len(*stack) > 0 {
			subTo, subPC = addEdge(e.sub, stack)
		}
		k := 0
		for ; k < len(e.locs); k += 3 {
			if e.locs[k] == li.line && e.locs[k+1] == subTo && e.locs[k+2] == subPC {
				break
			}
		}
		if k == len(e.locs) {
			e.locs = append(e.locs, li.line, subTo, subPC)
		}
		return to, int32(k/3 + 1)
	}
	codelocs := make([]int32, 0, 3*len(codelocsAny))
	var stack []lineInfo
	for _, idx := range codelocsAny {
		for idx != 0 {
			li := linetable[idx-1]
			idx = int64(li.inlined)
			stack = append(stack, li)
		}
		var line, to, pc int32
		if len(stack) > 0 {
			li := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if equal(li.file, top.file) {
				line = li.line
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
				Codelocs: compressCodelocs(0, e.locs)}
		}
		return out
	}
	return &expr.DebugInfo{Def: top.file, Linetable: nil, Edges: alloc(edges),
		Codelocs: compressCodelocs(top.line, codelocs)}
}

// compressCodelocs is jl_compress_codelocs (little-endian, as julia writes
// it on the platforms the goldens come from).
func compressCodelocs(firstline int32, codelocs []int32) string {
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
