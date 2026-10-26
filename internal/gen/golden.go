package gen

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Snippet is one golden record of a source text, at the three levels the
// parser is checked at: the raw lexer's tokens, the green tree (with the
// diagnostics) of parse!, and the serialised Expr that Meta.parseall(src)
// returns, all on the reference julia. A source which is not valid UTF-8
// (JSON strings cannot hold it) is stored in base64 instead.
type Snippet struct {
	Src    string `json:"src,omitempty"`
	SrcB64 string `json:"src_base64,omitempty"`
	Tokens string `json:"tokens"`
	Green  string `json:"green"`
	Expr   string `json:"expr"`
}

// Source returns the snippet's source text.
func (s Snippet) Source() string {
	if s.SrcB64 == "" {
		return s.Src
	}
	b, _ := base64.StdEncoding.DecodeString(s.SrcB64)
	return string(b)
}

func setSource(s *Snippet, src string) {
	if utf8.ValidString(src) {
		s.Src = src
	} else {
		s.SrcB64 = base64.StdEncoding.EncodeToString([]byte(src))
	}
}

// SnippetFilename is the file name the snippets are parsed under, Meta.parseall's default.
const SnippetFilename = "none"

// Snippets asks the oracle for the input strings of JuliaSyntax's own test
// files, plus extra, and returns their golden records as JSON lines.
func Snippets(o Oracle, extra []string) ([]byte, error) {
	list, err := o.Call("snippets")
	if err != nil {
		return nil, err
	}
	var srcs []string
	for _, l := range strings.Split(list, "\n") {
		if l == "" {
			continue
		}
		s, err := base64.StdEncoding.DecodeString(l)
		if err != nil {
			return nil, fmt.Errorf("gen: bad snippet line: %w", err)
		}
		srcs = append(srcs, string(s))
	}
	srcs = append(srcs, extra...)
	var b bytes.Buffer
	for _, src := range srcs {
		var s Snippet
		setSource(&s, src)
		if s.Tokens, err = o.Call("tokens", src); err == nil {
			if s.Green, err = o.Call("green", src); err == nil {
				s.Expr, err = o.Call("parseall", src, SnippetFilename)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("gen: snippet %q: %w", src, err)
		}
		writeJSONLine(&b, s)
	}
	return b.Bytes(), nil
}

// ExtraSeparator separates the snippets of testdata/snippets-extra.jl.
const ExtraSeparator = "#----\n"

// SplitExtra splits the extra-snippet file into its snippets. A snippet is
// everything between two separator lines, its final newline excluded, so that
// a snippet without a trailing newline can be written down.
func SplitExtra(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ExtraSeparator) {
		part = strings.TrimSuffix(part, "\n")
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func writeJSONLine(b *bytes.Buffer, v any) {
	enc := json.NewEncoder(b)
	enc.SetEscapeHTML(false)
	enc.Encode(v) // values here are strings, bools and numbers: Encode cannot fail
}

// CorpusEntry records one file of the reference corpus: its identity, the
// digests of what julia makes of it at each level, and whether the Go
// parser agrees at each level.
type CorpusEntry struct {
	Path         string `json:"path"`
	Bytes        int    `json:"bytes"`
	SrcSHA256    string `json:"src_sha256"`
	TokensSHA256 string `json:"tokens_sha256"`
	GreenSHA256  string `json:"green_sha256"`
	ExprSHA256   string `json:"expr_sha256"`
	TokensParity bool   `json:"tokens_parity"`
	GreenParity  bool   `json:"green_parity"`
	Parity       bool   `json:"parity"`
}

// Digest is the hex SHA-256 of s.
func Digest(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// GoSide is the Go parser, at the three levels the corpus checks. A nil
// function leaves its parity column false.
type GoSide struct {
	Tokens func(src []byte) string
	Green  func(src []byte) string
	Expr   func(src []byte, filename string) string
}

// Corpus walks the corpus of the reference installation (base, Compiler,
// stdlib, test) and returns its manifest as JSON lines.
func Corpus(o Oracle, g GoSide) ([]byte, error) {
	list, err := o.Call("corpus")
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	for _, path := range strings.Split(list, "\n") {
		if path == "" {
			continue
		}
		var src, toks, green, ex string
		if src, err = o.Call("readfile", path); err == nil {
			if toks, err = o.Call("tokens", src); err == nil {
				if green, err = o.Call("green", src); err == nil {
					ex, err = o.Call("parsefile", path)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("gen: %s: %w", path, err)
		}
		e := CorpusEntry{Path: path, Bytes: len(src), SrcSHA256: Digest(src),
			TokensSHA256: Digest(toks), GreenSHA256: Digest(green), ExprSHA256: Digest(ex)}
		if g.Tokens != nil {
			e.TokensParity = Digest(g.Tokens([]byte(src))) == e.TokensSHA256
		}
		if g.Green != nil {
			e.GreenParity = Digest(g.Green([]byte(src))) == e.GreenSHA256
		}
		if g.Expr != nil {
			e.Parity = Digest(g.Expr([]byte(src), path)) == e.ExprSHA256
		}
		writeJSONLine(&b, e)
	}
	return b.Bytes(), nil
}

// Program is the golden record of one program of testdata/programs: what the
// reference julia parses it to, lowers it to and prints when it runs.
type Program struct {
	Name  string `json:"name"`
	Parse string `json:"parse"`
	Lower string `json:"lower"`
	Eval  string `json:"eval"`
}

// Programs returns the golden records of the given programs (name → source).
func Programs(o Oracle, names []string, srcs map[string]string) ([]byte, error) {
	var b bytes.Buffer
	for _, name := range names {
		src := srcs[name]
		p := Program{Name: name}
		var err error
		if p.Parse, err = o.Call("parseall", src, name); err == nil {
			if p.Lower, err = o.Call("lower", src); err == nil {
				p.Eval, err = o.Call("eval", src)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("gen: %s: %w", name, err)
		}
		writeJSONLine(&b, p)
	}
	return b.Bytes(), nil
}

// ReadJSONLines decodes a JSON-lines golden file into a slice of T.
func ReadJSONLines[T any](data []byte) ([]T, error) {
	var out []T
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var v T
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}
