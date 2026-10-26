// Ported from JuliaSyntax src/diagnostics.jl and src/source_files.jl
// (Julia 1.12.6, JuliaSyntax 29ec5226f7b1): the formatting of diagnostics.
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"strings"

	"github.com/go-embedded-julia/julia/expr"
	"github.com/go-embedded-julia/julia/internal/unitab"
)

// showDiagnostic formats one diagnostic as JuliaSyntax's show_diagnostic
// does on an IO without color.
func showDiagnostic(b *strings.Builder, d Diagnostic, src *SourceFile) {
	prefix := "Info"
	switch d.Level {
	case levelError:
		prefix = "Error"
	case levelWarning:
		prefix = "Warning"
	}
	line, col := src.location(d.FirstByte)
	linecol := itoa(line) + ":" + itoa(col)
	locstr := "line " + linecol
	if src.Filename != "" {
		locstr = src.Filename + ":" + linecol
	}
	b.WriteString("# " + prefix + " @ " + locstr + "\n")
	src.highlight(b, d.FirstByte, d.LastByte, d.Message, 1, 1)
}

// showDiagnostics is show_diagnostics: the diagnostics separated by newlines.
func showDiagnostics(b *strings.Builder, ds []Diagnostic, src *SourceFile) {
	for i, d := range ds {
		if i > 0 {
			b.WriteByte('\n')
		}
		showDiagnostic(b, d, src)
	}
}

// location is source_location(source, byte_index): (line, column).
func (s *SourceFile) location(byteIndex int) (int, int) {
	li := s.lineIndex(byteIndex)
	i := s.lineStarts[li-1]
	col := 1
	for i < byteIndex-s.ByteOffset {
		i = s.nextind(i)
		col++
	}
	return li + s.FirstLine - 1, col
}

// lineRange is source_line_range: the byte range of the line holding
// byteIndex, widened by context lines.
func (s *SourceFile) lineRange(byteIndex, before, after int) (int, int) {
	li := s.lineIndex(byteIndex)
	f := s.lineStarts[max(li-before, 1)-1]
	l := s.lineStarts[min(li+1+after, len(s.lineStarts))-1] - 1
	return f + s.ByteOffset, l + s.ByteOffset
}

func (s *SourceFile) thisind(i int) int    { return s.thisindRaw(i-s.ByteOffset) + s.ByteOffset }
func (s *SourceFile) nextindSrc(i int) int { return s.nextind(i-s.ByteOffset) + s.ByteOffset }
func (s *SourceFile) charAt(i int) expr.Char {
	j := i - s.ByteOffset
	c, _ := expr.DecodeChar([]byte(s.Code[j-1 : min(len(s.Code), j+3)]))
	return c
}

// charWidth is Base.textwidth(c).
func charWidth(c expr.Char) int {
	u := uint32(c)
	if u < 0x7f000000 {
		if u >= 0x20000000 {
			return 1
		}
		return 0
	}
	if c.IsMalformed() || c.IsOverlong() {
		return 1
	}
	cp, _ := c.CodePoint()
	return unitab.Width(cp)
}

func textWidth(s string) int {
	w := 0
	for _, c := range juliaChars(s) {
		w += charWidth(c)
	}
	return w
}

func isSpaceChar(c expr.Char) bool {
	if !c.IsValid() {
		return false
	}
	return unitab.IsSpace(uint32(c.Rune()))
}

// printMarkerLine is _print_marker_line without color.
func printMarkerLine(b *strings.Builder, prefix, str string, underline, singleline bool, note string, hasNote bool) {
	var ind strings.Builder
	for _, c := range juliaChars(prefix) {
		if isSpaceChar(c) {
			ind.Write(c.Bytes())
		} else {
			ind.WriteString(strings.Repeat(" ", charWidth(c)))
		}
	}
	indent := ind.String()
	// Assume tabs are 4 wide rather than 0.
	w := textWidth(str) + 4*strings.Count(str, "\t")
	if indent != "" {
		if indent[0] == '\t' {
			indent = "#" + indent
		} else {
			first := juliaChars(indent)[0]
			indent = "#" + indent[len(first.Bytes()):]
		}
	}
	startstr, endstr, singlestart := "┌", "┐", "╓"
	if underline {
		startstr, endstr, singlestart = "└", "┘", "╙"
	}
	var mark string
	if singleline {
		switch w {
		case 0:
			mark = indent + startstr
		case 1:
			mark = indent + singlestart
		default:
			mark = indent + startstr + strings.Repeat("─", w-2) + endstr
		}
	} else if underline && indent == "" && w > 1 {
		mark = "#" + strings.Repeat("─", w-2) + endstr
	} else {
		s, e := startstr, ""
		if underline {
			s, e = "", endstr
		}
		if w == 0 {
			mark = indent + s + e
		} else {
			mark = indent + s + strings.Repeat("─", w-1) + e
		}
	}
	if hasNote {
		mark += " ── "
	}
	b.WriteString(mark)
	if hasNote {
		b.WriteString(note)
	}
}

// highlight is JuliaSyntax's highlight(io, source, p:q; note, context...)
// on an IO without color, with context_lines_after=0 as show_diagnostic
// passes it.
func (s *SourceFile) highlight(b *strings.Builder, p, q int, note string, before, inner int) {
	x, y := s.lineRange(p, before, inner)
	a, bb := s.lineRange(p, 0, 0)
	q1 := max(q, p) // Ignore q for empty ranges
	c, d := s.lineRange(q1, 0, 0)
	z, _ := s.lineRange(q1, inner, 0)
	pLine := s.Line(p)
	qLine := s.Line(q)
	first := 1 + s.ByteOffset
	if pLine >= qLine {
		hitext := s.slice(p, q)
		b.WriteString(s.slice(x, p-1))
		b.WriteString(hitext)
		b.WriteString(s.slice(s.nextindSrc(q), d))
		if d >= first && s.charAt(s.thisind(d)) != '\n'<<24 {
			b.WriteString("\n")
		}
		printMarkerLine(b, s.slice(a, p-1), hitext, true, true, note, true)
	} else {
		prefix1 := s.slice(a, p-1)
		b.WriteString(s.slice(x, a-1))
		printMarkerLine(b, prefix1, s.slice(p, bb), false, false, "", false)
		b.WriteString("\n")
		b.WriteString(prefix1)
		if qLine-pLine-1 <= 2*inner {
			b.WriteString(s.slice(p, q))
		} else {
			b.WriteString(s.slice(p, y))
			b.WriteString("⋮\n")
			b.WriteString(s.slice(z, q))
		}
		b.WriteString(s.slice(s.nextindSrc(q), d))
		if s.charAt(s.thisind(d)) != '\n'<<24 {
			b.WriteString("\n")
		}
		printMarkerLine(b, "", s.slice(c, q), true, false, note, true)
	}
}
