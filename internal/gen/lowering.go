package gen

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
)

// LoweringForm is one top-level form of a file, as the oracle's lowerforms
// answers it (internal/oracle/oracle.jl):
//
//	Tag "F": Input is the macro-expanded form, Output Meta.lower of it;
//	Tag "E": macro expansion threw; Input is the form, Output the message;
//	Tag "L": lowering threw; Input is the expanded form, Output the message.
//
// Input and Output are serialised with expr.Serialize's format, raw: the
// front end's gensym numbers in them depend on the julia session, so they
// are compared after renumbering (lower.Canonical).
type LoweringForm struct {
	Tag    string `json:"tag"`
	Input  string `json:"input"`
	Output string `json:"output"`
}

// LoweringForms asks the oracle to lower src as the corpus file rel.
func LoweringForms(o Oracle, src, rel string) ([]LoweringForm, error) {
	ans, err := o.Call("lowerforms", src, rel)
	if err != nil {
		return nil, err
	}
	return parseLoweringForms(ans)
}

// LoweringFile asks the oracle to lower the corpus file rel.
func LoweringFile(o Oracle, rel string) ([]LoweringForm, error) {
	ans, err := o.Call("lowerfile", rel)
	if err != nil {
		return nil, err
	}
	return parseLoweringForms(ans)
}

func parseLoweringForms(ans string) ([]LoweringForm, error) {
	var out []LoweringForm
	for _, line := range strings.Split(ans, "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || (parts[0] != "F" && parts[0] != "E" && parts[0] != "L") {
			return nil, fmt.Errorf("gen: bad lowering record %q", line)
		}
		in, err1 := base64.StdEncoding.DecodeString(parts[1])
		outp, err2 := base64.StdEncoding.DecodeString(parts[2])
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("gen: bad lowering record %q", line)
		}
		out = append(out, LoweringForm{Tag: parts[0], Input: string(in), Output: string(outp)})
	}
	return out, nil
}

// LoweringSnippet is the golden record of one snippet of
// testdata/lowering-snippets.jl: its top-level forms, lowered by julia.
type LoweringSnippet struct {
	Src   string         `json:"src"`
	Forms []LoweringForm `json:"forms"`
}

// LoweringSnippetPath is the corpus-relative path the snippets are lowered
// as: their macros come from a module using Test, Random and InteractiveUtils.
const LoweringSnippetPath = "test/lowering-snippets.jl"

// LoweringSnippets returns the golden records of srcs as JSON lines. The
// serialised inputs and outputs are passed through canonical (the gensym
// renumbering), so that the golden file does not depend on the julia
// session that wrote it.
func LoweringSnippets(o Oracle, srcs []string, canonical func(string) (string, error)) ([]byte, error) {
	var b bytes.Buffer
	for _, src := range srcs {
		forms, err := LoweringForms(o, src, LoweringSnippetPath)
		if err != nil {
			return nil, fmt.Errorf("gen: lowering snippet %q: %w", src, err)
		}
		for i := range forms {
			f := &forms[i]
			if f.Input, err = canonical(f.Input); err == nil && f.Tag == "F" {
				f.Output, err = canonical(f.Output)
			}
			if err != nil {
				return nil, fmt.Errorf("gen: lowering snippet %q: %w", src, err)
			}
		}
		writeJSONLine(&b, LoweringSnippet{Src: src, Forms: forms})
	}
	return b.Bytes(), nil
}

// LoweringCorpusEntry records one corpus file at the lowering level: how
// many of its top-level forms julia expanded and lowered, the short digests
// of the canonical lowered output of each, and how many the Go lowering
// reproduces.
type LoweringCorpusEntry struct {
	Path        string `json:"path"`
	Forms       int    `json:"forms"`
	Unexpanded  int    `json:"unexpanded"`
	LowerErrors int    `json:"lower_errors"`
	Digests     string `json:"digests"`
	Identical   int    `json:"identical"`
}

// LoweringSide is the Go lowering, as the corpus manifest needs it.
type LoweringSide struct {
	// Canonical renumbers the gensyms of a serialised lowered form.
	Canonical func(serialized string) (string, error)
	// Lower lowers the "F" forms of one file in order and reports, per
	// form, whether the Go output is the canonical julia output.
	Lower func(forms []LoweringForm) []bool
}

// ShortDigest is the first 8 hex digits of the SHA-256 of s.
func ShortDigest(s string) string { return Digest(s)[:8] }

// LoweringCorpus lowers every corpus file with the oracle and returns the
// manifest as JSON lines.
func LoweringCorpus(o Oracle, g LoweringSide) ([]byte, error) {
	list, err := o.Call("corpus")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for _, path := range strings.Split(list, "\n") {
		if path == "" {
			continue
		}
		forms, err := LoweringFile(o, path)
		if err != nil {
			return nil, fmt.Errorf("gen: %s: %w", path, err)
		}
		e, err := LoweringEntry(path, forms, g)
		if err != nil {
			return nil, err
		}
		writeJSONLine(&b, e)
	}
	return b.Bytes(), nil
}

// LoweringEntry summarises the forms of one file.
func LoweringEntry(path string, forms []LoweringForm, g LoweringSide) (LoweringCorpusEntry, error) {
	e := LoweringCorpusEntry{Path: path}
	var digests []string
	var lowered []LoweringForm
	for _, f := range forms {
		switch f.Tag {
		case "E":
			e.Unexpanded++
			continue
		case "L":
			e.LowerErrors++
			continue
		}
		e.Forms++
		c, err := g.Canonical(f.Output)
		if err != nil {
			return e, fmt.Errorf("gen: %s: %w", path, err)
		}
		digests = append(digests, ShortDigest(c))
		lowered = append(lowered, f)
	}
	e.Digests = strings.Join(digests, " ")
	for _, ok := range g.Lower(lowered) {
		if ok {
			e.Identical++
		}
	}
	return e, nil
}
