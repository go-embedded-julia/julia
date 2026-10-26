// Ported from JuliaSyntax src/source_files.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import (
	"sort"

	"github.com/go-embedded-julia/julia/expr"
)

// SourceFile is UTF-8 source text with a file name and the byte index of the
// start of every line; the code may be a chunk of a larger text starting at
// byte ByteOffset+1.
type SourceFile struct {
	Code       string
	ByteOffset int
	Filename   string // "" when there is none
	FirstLine  int
	lineStarts []int // 1-based indices into Code
}

// NewSourceFile is SourceFile(code; filename, first_line, first_index).
func NewSourceFile(code, filename string, firstLine, firstIndex int) *SourceFile {
	starts := []int{1}
	for i := 0; i < len(code); i++ {
		// The line is considered to start after the `\n`
		if code[i] == '\n' {
			starts = append(starts, i+2)
		}
	}
	if len(code) == 0 || code[len(code)-1] != '\n' {
		starts = append(starts, len(code)+1)
	}
	return &SourceFile{Code: code, ByteOffset: firstIndex - 1, Filename: filename, FirstLine: firstLine, lineStarts: starts}
}

// sourceFile is SourceFile(stream; filename, first_line): the parsed text,
// extended to the last byte any diagnostic refers to.
func (s *parseStream) sourceFile(filename string, firstLine int) *SourceFile {
	fbyte := s.firstByte()
	lbyte := s.lastByte()
	for _, d := range s.diagnostics {
		lbyte = max(lbyte, d.LastByte)
	}
	text := string(s.textbuf)
	tmp := &SourceFile{Code: text}
	end := lbyte
	if lbyte >= 1 && lbyte <= len(text) {
		end = tmp.nextind(tmp.thisindRaw(lbyte)) - 1
	}
	end = min(end, len(text))
	code := ""
	if fbyte <= end {
		code = text[fbyte-1 : end]
	}
	return NewSourceFile(code, filename, firstLine, fbyte)
}

// lineIndex is _source_line_index.
func (s *SourceFile) lineIndex(byteIndex int) int {
	x := byteIndex - s.ByteOffset
	// searchsortedlast: the last index whose line start is <= x (1-based).
	idx := sort.Search(len(s.lineStarts), func(i int) bool { return s.lineStarts[i] > x })
	if idx < len(s.lineStarts) {
		return idx
	}
	return idx - 1
}

// Line is source_line(source, byte_index).
func (s *SourceFile) Line(byteIndex int) int {
	return s.lineIndex(byteIndex) + s.FirstLine - 1
}

// nextind is Julia's nextind over Code with 1-based indices, cutting
// characters as Julia does.
func (s *SourceFile) nextind(i int) int {
	if i < 1 {
		return 1
	}
	if i > len(s.Code) {
		return i + 1
	}
	// thisindRaw returns the start of the character that covers i, so the
	// next character starts right after it.
	start := s.thisindRaw(i)
	_, n := expr.DecodeChar([]byte(s.Code[start-1 : min(len(s.Code), start+3)]))
	return start + n
}

// thisindRaw is thisind on Code (1-based): the start of the character
// containing byte i.
func (s *SourceFile) thisindRaw(i int) int {
	if i < 1 || i > len(s.Code) {
		return i
	}
	// A character starts at most three bytes back.
	for j := i; j >= max(1, i-3); j-- {
		b := s.Code[j-1]
		if b&0xc0 != 0x80 {
			_, n := expr.DecodeChar([]byte(s.Code[j-1 : min(len(s.Code), j+3)]))
			if j+n > i {
				return j
			}
			return i
		}
	}
	return i
}

// slice is getindex(source, first:last): the text from byte first through
// the end of the character holding byte last.
func (s *SourceFile) slice(first, last int) string {
	i := first - s.ByteOffset
	j := s.prevind(last + 1 - s.ByteOffset)
	if j < i {
		return ""
	}
	end := s.nextind(j) - 1
	return s.Code[i-1 : min(end, len(s.Code))]
}

// prevind on Code (1-based).
func (s *SourceFile) prevind(i int) int {
	if i <= 1 {
		return i - 1
	}
	return s.thisindRaw(i - 1)
}
