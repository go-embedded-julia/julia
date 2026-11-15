package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/go-embedded-julia/julia/internal/utf8proc/gen"
)

func init() {
	extraCommands = append(extraCommands, utf8procCommand)
}

// utf8procCommand writes internal/utf8proc/tables_gen.go from a checkout of
// utf8proc at the release julia pins (deps/utf8proc.version), by reading its
// utf8proc_data.c and utf8proc.h.
func utf8procCommand() *cobra.Command {
	var src, version, unicode string
	cmd := &cobra.Command{
		Use:   "utf8proc",
		Short: "regenerate internal/utf8proc/tables_gen.go from a checkout of utf8proc",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			root, _ := c.Flags().GetString("root")
			data, err := os.ReadFile(filepath.Join(src, "utf8proc_data.c"))
			if err != nil {
				return err
			}
			header, err := os.ReadFile(filepath.Join(src, "utf8proc.h"))
			if err != nil {
				return err
			}
			out, err := gen.Generate(string(data), string(header), version, unicode)
			if err != nil {
				return err
			}
			path := filepath.Join(root, "internal", "utf8proc", "tables_gen.go")
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "wrote %s (%d bytes)\n", path, len(out))
			return nil
		},
	}
	cmd.Flags().StringVar(&src, "src", "", "a checkout of utf8proc (utf8proc_data.c and utf8proc.h)")
	cmd.Flags().StringVar(&version, "version", "v2.10.0", "the utf8proc release (deps/utf8proc.version)")
	cmd.Flags().StringVar(&unicode, "unicode", "16.0.0", "the Unicode version of that release (utf8proc_unicode_version)")
	cmd.MarkFlagRequired("src")
	return cmd
}
