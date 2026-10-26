// Ported from JuliaSyntax src/diagnostics.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

// Diagnostic levels.
const (
	levelError   = "error"
	levelWarning = "warning"
)

// Diagnostic is a message about the source byte range [FirstByte, LastByte]
// (1-based, inclusive). Level is "error" or "warning".
type Diagnostic struct {
	FirstByte int
	LastByte  int
	Level     string
	Message   string
}

func newDiag(first, last int, level, msg string) Diagnostic {
	return Diagnostic{FirstByte: first, LastByte: last, Level: level, Message: msg}
}

// IsError reports whether the diagnostic is an error.
func (d Diagnostic) IsError() bool { return d.Level == levelError }

func anyError(ds []Diagnostic) bool {
	for _, d := range ds {
		if d.IsError() {
			return true
		}
	}
	return false
}
