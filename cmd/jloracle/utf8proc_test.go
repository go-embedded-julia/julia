package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const u8Header = `typedef enum { UTF8PROC_CATEGORY_CN = 0, UTF8PROC_BOUNDCLASS_OTHER = 1, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE = 0 } x;`

const u8Data = `static const utf8proc_uint16_t utf8proc_sequences[] = {
  65,
};
static const utf8proc_uint16_t utf8proc_stage1table[] = {
  0,
};
static const utf8proc_uint16_t utf8proc_stage2table[] = {
  0,
};
static const utf8proc_property_t utf8proc_properties[] = {
  {0, 0, 0, 0, UINT16_MAX, UINT16_MAX, UINT16_MAX, UINT16_MAX, UINT16_MAX, 0x3FF, 0, false, false, false, false, false, 1, 0, 0, UTF8PROC_BOUNDCLASS_OTHER, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE},
};
static const utf8proc_int32_t utf8proc_combinations_second[] = {
  1,
};
static const utf8proc_int32_t utf8proc_combinations_combined[] = {
  2,
};
`

func TestUtf8procCommand(t *testing.T) {
	src, root := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(src, "utf8proc_data.c"), []byte(u8Data), 0o644)
	os.WriteFile(filepath.Join(src, "utf8proc.h"), []byte(u8Header), 0o644)
	os.MkdirAll(filepath.Join(root, "internal", "utf8proc"), 0o755)

	var out bytes.Buffer
	cmd := RootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"-C", root, "utf8proc", "--src", src, "--version", "v1.2.3", "--unicode", "9.0.0"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "internal", "utf8proc", "tables_gen.go"))
	if err != nil || !strings.Contains(string(b), `"v1.2.3"`) || !strings.Contains(string(b), `"9.0.0"`) {
		t.Errorf("tables_gen.go: %v\n%s", err, b)
	}
	if !strings.Contains(out.String(), "wrote ") {
		t.Errorf("output %q", out.String())
	}

	// each way it fails
	run := func(args ...string) error {
		cmd := RootCmd()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run("-C", root, "utf8proc"); err == nil {
		t.Error("no --src")
	}
	if err := run("-C", root, "utf8proc", "--src", filepath.Join(src, "none")); err == nil {
		t.Error("no such source")
	}
	noHeader := t.TempDir()
	os.WriteFile(filepath.Join(noHeader, "utf8proc_data.c"), []byte(u8Data), 0o644)
	if err := run("-C", root, "utf8proc", "--src", noHeader); err == nil {
		t.Error("no header")
	}
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "utf8proc_data.c"), []byte("nothing"), 0o644)
	os.WriteFile(filepath.Join(bad, "utf8proc.h"), []byte(u8Header), 0o644)
	if err := run("-C", root, "utf8proc", "--src", bad); err == nil {
		t.Error("a data file with no arrays")
	}
	if err := run("-C", filepath.Join(root, "none"), "utf8proc", "--src", src); err == nil {
		t.Error("an unwritable output")
	}
}
