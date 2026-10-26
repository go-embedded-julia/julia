package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-embedded-julia/julia/internal/gen"
	"github.com/go-embedded-julia/julia/internal/oracle"
	"github.com/go-embedded-julia/julia/syntax"
)

func init() {
	extraCommands = append(extraCommands, benchCommand)
}

func benchCommand() *cobra.Command {
	var rounds int
	bench := &cobra.Command{
		Use:   "bench",
		Short: "time the Go parser against julia's on the whole corpus, one core each",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			o, closer, err := startOracle()
			if err != nil {
				return err
			}
			defer closer()
			return benchCorpus(o, rounds, c.OutOrStdout())
		},
	}
	bench.Flags().IntVarP(&rounds, "rounds", "n", 3, "timed rounds (best is kept)")
	return bench
}

// benchCorpus times Meta.parseall in julia and ParseAll in Go over the
// corpus, best of n rounds each, after a warm-up round.
func benchCorpus(o gen.Oracle, n int, out io.Writer) error {
	list, err := o.Call("corpus")
	if err != nil {
		return err
	}
	type file struct {
		path string
		src  []byte
	}
	var files []file
	total := 0
	for _, p := range strings.Split(list, "\n") {
		src, err := o.Call("readfile", p)
		if err != nil {
			return err
		}
		files = append(files, file{p, []byte(src)})
		total += len(src)
	}
	jl, err := o.Call("benchparse", strconv.Itoa(n))
	if err != nil {
		return err
	}
	f := strings.Fields(jl)
	jlNS, _ := strconv.ParseFloat(f[0], 64)
	best := time.Duration(1<<63 - 1)
	for r := 0; r <= n; r++ {
		t0 := time.Now()
		for _, fl := range files {
			syntax.ParseAll(fl.src, fl.path)
		}
		if dt := time.Since(t0); r > 0 && dt < best {
			best = dt
		}
	}
	mb := float64(total) / 1e6
	fmt.Fprintf(out, "corpus: %d files, %.1f MB\n", len(files), mb)
	fmt.Fprintf(out, "julia %s Meta.parseall: %.3f s (%.1f MB/s)\n", oracle.Version, jlNS/1e9, mb/(jlNS/1e9))
	fmt.Fprintf(out, "go ParseAll:            %.3f s (%.1f MB/s)\n", best.Seconds(), mb/best.Seconds())
	fmt.Fprintf(out, "ratio go/julia: %.2f\n", best.Seconds()/(jlNS/1e9))
	return nil
}
