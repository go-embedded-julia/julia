package unitab

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// identifierMap is JuliaSyntax's utf8proc_custom_func: characters mapped
// before canonical composition.
var identifierMap = map[rune]rune{
	0x025B: 0x03B5, // 'ɛ' => 'ε'
	0x00B5: 0x03BC, // 'µ' => 'μ'
	0x00B7: 0x22C5, // '·' => '⋅'
	0x0387: 0x22C5, // '·' => '⋅'
	0x2212: 0x002D, // '−' (\minus) => '-'
	0x210F: 0x0127, // 'ℏ' (\hslash) => 'ħ' \hbar
}

// NormalizeIdentifier is JuliaSyntax's normalize_identifier for non-ASCII
// text: utf8proc_map with UTF8PROC_STABLE | UTF8PROC_COMPOSE (canonical
// decomposition then composition, i.e. NFC) after the custom character map.
// Invalid UTF-8 is returned unchanged: utf8proc rejects it, which makes
// JuliaSyntax itself fail.
func NormalizeIdentifier(s string) string {
	if !utf8.ValidString(s) {
		return s
	}
	mapped := strings.Map(func(r rune) rune {
		if m, ok := identifierMap[r]; ok {
			return m
		}
		return r
	}, s)
	return norm.NFC.String(mapped)
}
