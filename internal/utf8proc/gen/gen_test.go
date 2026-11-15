package gen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const header = `typedef enum { UTF8PROC_CATEGORY_CN = 0, UTF8PROC_CATEGORY_LU = 1, UTF8PROC_BOUNDCLASS_OTHER = 1, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE = 0 } x;`

// miniData is the shape of utf8proc_data.c with a handful of entries: the
// arrays, a property row with names, UINT16_MAX and booleans, hexadecimal.
const miniData = `static const utf8proc_uint16_t utf8proc_sequences[] = {
  0x41, 0x42, 97,
};

static const utf8proc_uint16_t utf8proc_stage1table[] = {
  0, 1,
};

static const utf8proc_uint16_t utf8proc_stage2table[] = {
  0, 1, 0,
};

static const utf8proc_property_t utf8proc_properties[] = {
  {0, 0, 0, 0, UINT16_MAX, UINT16_MAX, UINT16_MAX, UINT16_MAX, UINT16_MAX,  0x3FF,0,false,  false,false,false,false, 1, 0, 0, UTF8PROC_BOUNDCLASS_OTHER, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE},
  {UTF8PROC_CATEGORY_LU, 0, 0, 0, 5, UINT16_MAX, UINT16_MAX, 7, UINT16_MAX, 1023, 0, false, false, false, true, true, 2, false, 0, UTF8PROC_BOUNDCLASS_OTHER, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE},
};

static const utf8proc_int32_t utf8proc_combinations_second[] = {
  824,
  825,
};

static const utf8proc_int32_t utf8proc_combinations_combined[] = {
  8814,
  8815,
};
`

func TestGenerate(t *testing.T) {
	out, err := Generate(miniData, header, "v9.9.9", "99.0.0")
	if err != nil {
		t.Fatal(err)
	}
	src := string(out)
	for _, want := range []string{
		"Code generated", `"v9.9.9"`, `"99.0.0"`,
		// 0x41 0x42 97 as uint16 little-endian hex; 824, 825 and 8814, 8815 as 3 bytes
		`const sequencesData = "410042006100"`, "const sequencesLen = 3", `const stage1Data = "00000100"`, `const stage2Data = "000001000000"`,
		`const combSecondData = "380300390300"`, `const combCombinedData = "6e22006f2200"`,
		// the first property row: four zero bytes, five UINT16_MAX, 0x3FF
		`const propertiesData = "00000000ffffffffffffffffffffff03`,
		"func decodeProperties(", "func byteAt(s string, i int) byte",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the output lacks %q:\n%s", want, src)
		}
	}
	// the output is Go
	if _, err := parser.ParseFile(token.NewFileSet(), "tables_gen.go", out, 0); err != nil {
		t.Fatal(err)
	}
}

func TestQuote(t *testing.T) {
	if got := quote(nil); got != `""` {
		t.Errorf("empty: %s", got)
	}
	b := make([]byte, 65)
	b[0], b[64] = 0xab, 0x0f
	want := `"ab` + strings.Repeat("00", 63) + `" +` + "\n\t" + `"0f"`
	if got := quote(b); got != want {
		t.Errorf("65 bytes: %s", got)
	}
}

func TestGenerateRefusals(t *testing.T) {
	for name, c := range map[string]struct{ data, header, want string }{
		"no enumerator":                    {miniData, "typedef int x;", "no enumerator"},
		"a missing array":                  {strings.Replace(miniData, "utf8proc_stage1table", "other", 1), header, "no array utf8proc_stage1table"},
		"a missing property":               {strings.Replace(miniData, "utf8proc_properties", "other", 1), header, "no array utf8proc_properties"},
		"an unknown name":                  {strings.Replace(miniData, "0x41", "NOT_A_NAME", 1), header, "NOT_A_NAME"},
		"a short row":                      {strings.Replace(miniData, "1, 0, 0, UTF8PROC_BOUNDCLASS_OTHER, UTF8PROC_INDIC_CONJUNCT_BREAK_NONE},\n  {UTF8PROC", "1, 0, UTF8PROC_BOUNDCLASS_OTHER},\n  {UTF8PROC", 1), header, "fields, want 21"},
		"a category too wide for its byte": {strings.Replace(miniData, "{UTF8PROC_CATEGORY_LU,", "{300,", 1), header, "property field 0: 300 does not fit 1 byte"},
		"a sequence too wide":              {strings.Replace(miniData, "0x41,", "70000,", 1), header, "does not fit 2 byte"},
		"a negative value":                 {strings.Replace(miniData, "0x41,", "-1,", 1), header, "does not fit"},
		"a bad row value":                  {strings.Replace(miniData, "UTF8PROC_CATEGORY_LU", "UTF8PROC_CATEGORY_XX", 1), header, "UTF8PROC_CATEGORY_XX"},
	} {
		if _, err := Generate(c.data, c.header, "v", "u"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
	// an enumerator too large for a number
	if _, err := Generate(miniData, "UTF8PROC_X = 99999999999999999999999", "v", "u"); err == nil {
		t.Error("an enumerator that is not a number")
	}
}
