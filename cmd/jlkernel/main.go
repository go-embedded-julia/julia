// Command jlkernel loads a system image by its pinned SHA-256 and runs one
// cell on a Kernel: the image is checked before it is decoded, and the cell's
// source is read from standard input.
//
//	jlkernel --image julia-image-v0.15.0.bin --digest a4568120… < cell.jl
//
// The value goes to standard output after what the cell printed, its stderr
// to standard error, and an error to standard error with exit status 1. It is
// the program the WebAssembly lane of CI runs under node (GOOS=js), and the
// one that measures the load of an image there.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/go-embedded-julia/julia/interp"
)

var osExit = os.Exit

// RootCmd builds the command.
func RootCmd() *cobra.Command {
	var image, digest string
	var timeout time.Duration
	var stats bool
	cmd := &cobra.Command{
		Use:          "jlkernel --image FILE --digest SHA256 < cell",
		Short:        "Run a cell on a system image pinned by its SHA-256",
		Args:         cobra.NoArgs,
		SilenceUsage: true, SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return run(cmd, image, digest, timeout, stats)
		},
	}
	cmd.Flags().StringVar(&image, "image", "", "the image file")
	cmd.Flags().StringVar(&digest, "digest", "", "the SHA-256 of the image, 64 hexadecimal digits")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "the budget of the cell")
	cmd.Flags().BoolVar(&stats, "stats", false, "report the load time and the memory on standard error")
	cmd.MarkFlagRequired("image")
	cmd.MarkFlagRequired("digest")
	return cmd
}

func run(cmd *cobra.Command, image, digest string, timeout time.Duration, stats bool) error {
	want, err := interp.ParseDigest(digest)
	if err != nil {
		return err
	}
	img, err := os.ReadFile(image)
	if err != nil {
		return err
	}
	src, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return err
	}
	t0 := time.Now()
	k, err := interp.LoadKernel(bytes.NewReader(img), interp.LoadOptions{Digest: &want},
		interp.KernelOptions{CellOptions: interp.CellOptions{Timeout: timeout}})
	if err != nil {
		return err
	}
	defer k.Close()
	load := time.Since(t0)
	r := k.Eval(cmd.Context(), string(src))
	fmt.Fprint(cmd.OutOrStdout(), r.Stdout)
	fmt.Fprint(cmd.ErrOrStderr(), r.Stderr)
	if stats {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Fprintf(cmd.ErrOrStderr(), "image %d bytes, load %v, first cell %v, heap %d MiB, from the system %d MiB\n",
			len(img), load, r.Elapsed, m.HeapAlloc>>20, m.Sys>>20)
	}
	if r.Err != nil {
		return r.Err
	}
	fmt.Fprintln(cmd.OutOrStdout(), r.Value)
	return nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := RootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "jlkernel:", err)
		stop()
		osExit(1)
	}
}
