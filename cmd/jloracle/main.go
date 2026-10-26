// Command jloracle regenerates everything this repository derives from the
// reference julia: the lexer's Unicode tables and the golden files the test
// suite replays without julia.
//
//	jloracle regen      # all of the below
//	jloracle unicode    # internal/unitab/tables_gen.go
//	jloracle snippets   # testdata/golden/snippets.jsonl
//	jloracle corpus     # testdata/golden/corpus.jsonl
//	jloracle programs   # testdata/golden/programs.jsonl
//
// The reference is the release pinned in internal/oracle (run through pkgx,
// or $JLGO_JULIA).
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
)

var osExit = os.Exit

// startOracle is replaced by tests.
var startOracle = func() (gen.Oracle, func() error, error) {
	p, err := oracle.Start()
	if err != nil {
		return nil, nil, err
	}
	return p, p.Close, nil
}

// goSide is the Go parser at the levels the corpus manifest records; the
// files of the parser's steps fill it (a nil level records no parity).
var goSide gen.GoSide

// extraCommands are the subcommands other files of the package add.
var extraCommands []func() *cobra.Command

// RootCmd builds the command tree.
func RootCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:          "jloracle",
		Short:        "Regenerate the files derived from the reference julia " + oracle.Version,
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVarP(&root, "root", "C", ".", "repository root")

	steps := map[string]func(o gen.Oracle, root string, out io.Writer) error{
		"unicode":  writeUnicode,
		"snippets": writeSnippets,
		"corpus":   writeCorpus,
		"programs": writePrograms,
	}
	order := []string{"unicode", "snippets", "corpus", "programs"}
	run := func(names []string) func(*cobra.Command, []string) error {
		return func(c *cobra.Command, _ []string) error {
			o, closer, err := startOracle()
			if err != nil {
				return err
			}
			defer closer()
			for _, n := range names {
				if err := steps[n](o, root, c.OutOrStdout()); err != nil {
					return err
				}
			}
			return nil
		}
	}
	help := map[string]string{
		"unicode":  "write internal/unitab/tables_gen.go",
		"snippets": "write testdata/golden/snippets.jsonl",
		"corpus":   "write testdata/golden/corpus.jsonl",
		"programs": "write testdata/golden/programs.jsonl",
	}
	for _, n := range order {
		cmd.AddCommand(&cobra.Command{Use: n, Short: help[n], Args: cobra.NoArgs, RunE: run([]string{n})})
	}
	cmd.AddCommand(&cobra.Command{Use: "regen", Short: "regenerate every derived file", Args: cobra.NoArgs, RunE: run(order)})
	for _, extra := range extraCommands {
		cmd.AddCommand(extra())
	}
	return cmd
}

func write(root, rel string, data []byte, out io.Writer) error {
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s (%d bytes)\n", rel, len(data))
	return nil
}

func writeUnicode(o gen.Oracle, root string, out io.Writer) error {
	src, err := gen.UnicodeTables(o, oracle.Version)
	if err != nil {
		return err
	}
	if err := write(root, "internal/unitab/tables_gen.go", src, out); err != nil {
		return err
	}
	norm, err := o.Call("normalize")
	if err != nil {
		return err
	}
	if err := write(root, "internal/unitab/testdata/normalize.txt", []byte(norm), out); err != nil {
		return err
	}
	gb, err := gen.GraphemeGolden(o)
	if err != nil {
		return err
	}
	return write(root, "internal/unitab/testdata/graphemebreak.txt", gb, out)
}

// binarySnippets are extra snippets a text file cannot hold well: invalid
// UTF-8, control characters, odd line endings.
var binarySnippets = []string{
	"x = \xff",
	"\"a\xffb\"",
	"'\xff'",
	"`\xff`",
	"# comment \xff\nx",
	"#= \xff =#",
	"x\xe2\x88",
	"\"\xc0\x80\"",
	"x = \"\x01\x1b\x00" + "1\"‪",
	"\"\\x00\\1\" \"‪\x7f\x00a\"",
	"a = 1\r\nb = 2\r\n",
	"\"a\r\nb\" * x\"c\r\nd\" * \"\"\"\r\n  e\r\n  f\"\"\"",
	"\"\"\"\r  a\r  b\"\"\"",
	"x\"\"\"\r\n  a\r\n  b\"\"\"",
	"'\r\n'",
	"\xef\xbb\xbfx = 1",
	"\t\tx y",
	"\tf(\n\t\ta\n\t\tb",
	"f(\n  a\n  b\n  c\n  d\n  e\n  f\n  g",
	"x = \"⁦\"",
	"x = \"⁩‬\"",
	"\"\xed\xa0\x80\"",
	"x́‍",
	"🇫🇷x = 1",
	"\"$x\xff\"",
	"a +́ b",
	"क्‍ष = 1",
}

func writeSnippets(o gen.Oracle, root string, out io.Writer) error {
	extra, err := os.ReadFile(filepath.Join(root, "testdata", "snippets-extra.jl"))
	if err != nil {
		return err
	}
	data, err := gen.Snippets(o, append(gen.SplitExtra(string(extra)), binarySnippets...))
	if err != nil {
		return err
	}
	return write(root, "testdata/golden/snippets.jsonl", data, out)
}

func writeCorpus(o gen.Oracle, root string, out io.Writer) error {
	data, err := gen.Corpus(o, goSide)
	if err != nil {
		return err
	}
	entries, _ := gen.ReadJSONLines[gen.CorpusEntry](data)
	var nt, ng, ne int
	for _, e := range entries {
		if e.TokensParity {
			nt++
		}
		if e.GreenParity {
			ng++
		}
		if e.Parity {
			ne++
		}
	}
	fmt.Fprintf(out, "corpus parity: tokens %d/%d, green trees %d/%d, Expr %d/%d files\n",
		nt, len(entries), ng, len(entries), ne, len(entries))
	return write(root, "testdata/golden/corpus.jsonl", data, out)
}

func writePrograms(o gen.Oracle, root string, out io.Writer) error {
	dir := filepath.Join(root, "testdata", "programs")
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	srcs := map[string]string{}
	var names []string
	for _, f := range files {
		if !strings.HasSuffix(f.Name(), ".jl") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return err
		}
		names = append(names, f.Name())
		srcs[f.Name()] = string(b)
	}
	sort.Strings(names)
	data, err := gen.Programs(o, names, srcs)
	if err != nil {
		return err
	}
	return write(root, "testdata/golden/programs.jsonl", data, out)
}

func main() {
	if err := RootCmd().Execute(); err != nil {
		osExit(1)
	}
}
