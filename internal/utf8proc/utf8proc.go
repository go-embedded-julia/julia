// Package utf8proc is a Go port of utf8proc v2.10.0 (Unicode 16.0.0), the
// library julia 1.12.6 calls for case mappings, categories, character widths,
// grapheme breaks and normalization (deps/utf8proc.version pins the release;
// base/strings/unicode.jl is its caller). The functions port utf8proc.c line
// for line, and the tables in tables_gen.go are utf8proc's own data
// (utf8proc_data.c, read by `jloracle utf8proc`), at the version of Unicode the
// release implements, not Go's unicode package's: the two differ in the
// characters Unicode added between them.
//
// utf8proc is MIT-licensed, derived from the Unicode data files (NOTICE).
//
// Normalization lives here and nowhere else in this repository: internal/unitab
// holds what the lexer asks (category, width, grapheme bound classes, dumped from
// julia). The normalization forms are the part that is to be reconciled with
// go-ruby-unicode-normalize under go-embedded-ruby/ruby#918's first extraction.
package utf8proc

import "math"

// Option is a utf8proc_option_t: the flags of Decompose, NormalizeUTF32 and
// Reencode (utf8proc.h:148-211).
type Option int

const (
	NullTerm     Option = 1 << 0
	Stable       Option = 1 << 1
	Compat       Option = 1 << 2
	Compose      Option = 1 << 3
	DecomposeOpt Option = 1 << 4
	Ignore       Option = 1 << 5
	RejectNA     Option = 1 << 6
	NLF2LS       Option = 1 << 7
	NLF2PS       Option = 1 << 8
	NLF2LF              = NLF2LS | NLF2PS
	StripCC      Option = 1 << 9
	CaseFold     Option = 1 << 10
	CharBound    Option = 1 << 11
	Lump         Option = 1 << 12
	StripMark    Option = 1 << 13
	StripNA      Option = 1 << 14
)

// The error codes (utf8proc.h:219-227), what Decompose and DecomposeChar
// answer as a negative count.
const (
	ErrNoMem       = -1
	ErrOverflow    = -2
	ErrInvalidUTF8 = -3
	ErrNotAssigned = -4
	ErrInvalidOpts = -5
	uint16Max      = 65535
	maxCodePoint   = 0x110000
	hangulSBase    = 0xAC00
	hangulLBase    = 0x1100
	hangulVBase    = 0x1161
	hangulTBase    = 0x11A7
	hangulLCount   = 19
	hangulVCount   = 21
	hangulTCount   = 28
	hangulNCount   = 588
	hangulSCount   = 11172
	boundStart     = 0
	boundOther     = 1
	boundCR        = 2
	boundLF        = 3
	boundControl   = 4
	boundExtend    = 5
	boundL         = 6
	boundV         = 7
	boundT         = 8
	boundLV        = 9
	boundLVT       = 10
	boundRI        = 11
	boundSpacing   = 12
	boundPrepend   = 13
	boundZWJ       = 14
	boundExtPict   = 19
	boundEZWG      = 20
	icbNone        = 0
	icbLinker      = 1
	icbConsonant   = 2
	icbExtend      = 3
	categoryLT     = 3
	categoryMn     = 6
	categoryMc     = 7
	categoryMe     = 8
	categoryPc     = 12
	categoryPd     = 13
	categoryZs     = 23
	categoryZl     = 24
	categoryZp     = 25
)

// maxWords is SSIZE_MAX/sizeof(int32)/2, the bound on the words of one
// decomposition (utf8proc.c:587-589); a variable so that a test can lower it.
var maxWords = math.MaxInt32 / 4 / 2

// Errmsg is utf8proc_errmsg (utf8proc.c:107-122).
func Errmsg(code int) string {
	switch code {
	case ErrNoMem:
		return "Memory for processing UTF-8 data could not be allocated."
	case ErrOverflow:
		return "UTF-8 string is too long to be processed."
	case ErrInvalidUTF8:
		return "Invalid UTF-8 string"
	case ErrNotAssigned:
		return "Unassigned Unicode code point found in UTF-8 string."
	case ErrInvalidOpts:
		return "Invalid options for UTF-8 processing chosen."
	}
	return "An unknown error occurred while processing UTF-8 data."
}

// Property is utf8proc_property_t (utf8proc.h:236-): what the tables say of one
// code point, field by field in the order of the generated rows.
type Property struct {
	Category, CombiningClass, BidiClass, DecompType      int16
	DecompSeq, CasefoldSeq, UpperSeq, LowerSeq, TitleSeq uint16
	CombIndex                                            uint16
	CombLength                                           uint8
	CombIsSecond, BidiMirrored, CompExclusion            uint8
	Ignorable, ControlBoundary                           uint8
	Charwidth, AmbiguousWidth, Pad                       uint8
	BoundClass, IndicConjunctBreak                       uint8
}

// GetProperty is utf8proc_get_property (utf8proc.c:242-244): the properties of
// the code point uc, those of the unassigned one (index 0) outside 0..0x10FFFF.
func GetProperty(uc int32) *Property {
	if uc < 0 || uc >= maxCodePoint {
		return &properties[0]
	}
	return unsafeGetProperty(uc)
}

func unsafeGetProperty(uc int32) *Property {
	return &properties[stage2(int(stage1(int(uc>>8)))+int(uc&0xFF))]
}

// seqindexDecodeEntry is seqindex_decode_entry (utf8proc.c:363-372): the code
// point at sequences[i] (a surrogate pair is one), and the index after it.
func seqindexDecodeEntry(i int) (int32, int) {
	cp := int32(sequences(i))
	if cp&0xF800 == 0xD800 {
		i++
		cp = (cp&0x03FF)<<10 | int32(sequences(i))&0x03FF
		cp += 0x10000
	}
	return cp, i
}

func seqindexDecodeIndex(seqindex uint16) int32 {
	cp, _ := seqindexDecodeEntry(int(seqindex))
	return cp
}

// ToLower, ToUpper and ToTitle are utf8proc_tolower, _toupper and _totitle
// (utf8proc.c:399-415).
func ToLower(c int32) int32 {
	if cl := GetProperty(c).LowerSeq; cl != uint16Max {
		return seqindexDecodeIndex(cl)
	}
	return c
}

func ToUpper(c int32) int32 {
	if cu := GetProperty(c).UpperSeq; cu != uint16Max {
		return seqindexDecodeIndex(cu)
	}
	return c
}

func ToTitle(c int32) int32 {
	if cu := GetProperty(c).TitleSeq; cu != uint16Max {
		return seqindexDecodeIndex(cu)
	}
	return c
}

// IsLower and IsUpper are utf8proc_islower and _isupper (utf8proc.c:417-427).
func IsLower(c int32) bool {
	p := GetProperty(c)
	return p.LowerSeq != p.UpperSeq && p.LowerSeq == uint16Max
}

func IsUpper(c int32) bool {
	p := GetProperty(c)
	return p.LowerSeq != p.UpperSeq && p.UpperSeq == uint16Max && p.Category != categoryLT
}

// CharWidth is utf8proc_charwidth (utf8proc.c:431-433), CharWidthAmbiguous
// utf8proc_charwidth_ambiguous.
func CharWidth(c int32) int { return int(GetProperty(c).Charwidth) }

func CharWidthAmbiguous(c int32) bool { return GetProperty(c).AmbiguousWidth != 0 }

// Category is utf8proc_category (utf8proc.c:439-441), CategoryString
// utf8proc_category_string.
func Category(c int32) int { return int(GetProperty(c).Category) }

var categoryStrings = [...]string{"Cn", "Lu", "Ll", "Lt", "Lm", "Lo", "Mn", "Mc", "Me", "Nd", "Nl", "No", "Pc", "Pd", "Ps", "Pe", "Pi", "Pf", "Po", "Sm", "Sc", "Sk", "So", "Zs", "Zl", "Zp", "Cc", "Cf", "Cs", "Co"}

func CategoryString(c int32) string { return categoryStrings[Category(c)] }

// graphemeBreakSimple is grapheme_break_simple (utf8proc.c:261-289).
func graphemeBreakSimple(lbc, tbc int) bool {
	switch {
	case lbc == boundStart: // GB1
		return true
	case lbc == boundCR && tbc == boundLF: // GB3
		return false
	case lbc >= boundCR && lbc <= boundControl: // GB4
		return true
	case tbc >= boundCR && tbc <= boundControl: // GB5
		return true
	case lbc == boundL && (tbc == boundL || tbc == boundV || tbc == boundLV || tbc == boundLVT): // GB6
		return false
	case (lbc == boundLV || lbc == boundV) && (tbc == boundV || tbc == boundT): // GB7
		return false
	case (lbc == boundLVT || lbc == boundT) && tbc == boundT: // GB8
		return false
	case tbc == boundExtend || tbc == boundZWJ || tbc == boundSpacing || lbc == boundPrepend: // GB9, GB9a, GB9b
		return false
	case lbc == boundEZWG && tbc == boundExtPict: // GB11
		return false
	case lbc == boundRI && tbc == boundRI: // GB12/13
		return false
	}
	return true // GB999
}

// graphemeBreakExtended is grapheme_break_extended (utf8proc.c:291-343): the
// bound classes lbc and tbc, the indic conjunct break properties licb and ticb,
// and the state the caller keeps (nil for none).
func graphemeBreakExtended(lbc, tbc, licb, ticb int, state *int32) bool {
	if state == nil {
		return graphemeBreakSimple(lbc, tbc)
	}
	var stateBC, stateICB int
	if *state == 0 { // state initialization
		stateBC = lbc
		stateICB = icbNone
		if licb == icbConsonant {
			stateICB = licb
		}
	} else { // lbc and licb are already encoded in *state
		stateBC = int(*state & 0xff)
		stateICB = int(*state >> 8)
	}
	breakPermitted := graphemeBreakSimple(stateBC, tbc) && !(stateICB == icbLinker && ticb == icbConsonant) // GB9c

	// GB9c: no break between two consonants separated by 1+ linkers and 0+ extends
	if ticb == icbConsonant || stateICB == icbConsonant || stateICB == icbExtend {
		stateICB = ticb
	} else if stateICB == icbLinker {
		if ticb == icbExtend {
			stateICB = icbLinker
		} else {
			stateICB = ticb
		}
	}
	// GB12/13: after two regional indicators force a break
	if stateBC == tbc && tbc == boundRI {
		stateBC = boundOther
	} else if stateBC == boundExtPict { // GB11 (emoji extend* zwj / emoji)
		switch tbc {
		case boundExtend:
			stateBC = boundExtPict
		case boundZWJ:
			stateBC = boundEZWG
		default:
			stateBC = tbc
		}
	} else {
		stateBC = tbc
	}
	*state = int32(stateBC + stateICB<<8)
	return breakPermitted
}

// GraphemeBreakStateful is utf8proc_grapheme_break_stateful (utf8proc.c:345-355),
// GraphemeBreak utf8proc_grapheme_break.
func GraphemeBreakStateful(c1, c2 int32, state *int32) bool {
	p1, p2 := GetProperty(c1), GetProperty(c2)
	return graphemeBreakExtended(int(p1.BoundClass), int(p2.BoundClass), int(p1.IndicConjunctBreak), int(p2.IndicConjunctBreak), state)
}

func GraphemeBreak(c1, c2 int32) bool { return GraphemeBreakStateful(c1, c2, nil) }

// Iterate is utf8proc_iterate (utf8proc.c:125-171) over str: the code point at
// its start and the bytes it takes, or -1 and ErrInvalidUTF8; 0 bytes for an
// empty str.
func Iterate(str []byte) (cp int32, n int) {
	if len(str) == 0 {
		return -1, 0
	}
	uc := int32(str[0])
	if uc < 0x80 {
		return uc, 1
	}
	// must be between 0xc2 and 0xf4 inclusive to be valid
	if uint32(uc-0xc2) > 0xf4-0xc2 {
		return -1, ErrInvalidUTF8
	}
	cont := func(i int) bool { return i < len(str) && str[i]&0xc0 == 0x80 }
	if uc < 0xe0 { // 2-byte sequence
		if !cont(1) {
			return -1, ErrInvalidUTF8
		}
		return (uc&0x1f)<<6 | int32(str[1]&0x3f), 2
	}
	if uc < 0xf0 { // 3-byte sequence
		if !cont(1) || !cont(2) {
			return -1, ErrInvalidUTF8
		}
		if uc == 0xed && str[1] > 0x9f { // surrogates
			return -1, ErrInvalidUTF8
		}
		uc = (uc&0xf)<<12 | int32(str[1]&0x3f)<<6 | int32(str[2]&0x3f)
		if uc < 0x800 {
			return -1, ErrInvalidUTF8
		}
		return uc, 3
	}
	// 4-byte sequence
	if !cont(1) || !cont(2) || !cont(3) {
		return -1, ErrInvalidUTF8
	}
	if uc == 0xf0 {
		if str[1] < 0x90 {
			return -1, ErrInvalidUTF8
		}
	} else if uc == 0xf4 {
		if str[1] > 0x8f {
			return -1, ErrInvalidUTF8
		}
	}
	return (uc&7)<<18 | int32(str[1]&0x3f)<<12 | int32(str[2]&0x3f)<<6 | int32(str[3]&0x3f), 4
}

// EncodeChar is utf8proc_encode_char (utf8proc.c:177-201): the UTF-8 of uc
// (surrogates are encoded as they are, as utf8proc does), nothing for a value
// out of range.
func EncodeChar(uc int32) []byte { return appendChar(nil, uc, false) }

// appendChar is utf8proc_encode_char and charbound_encode_char (utf8proc.c:204-230)
// onto dst: with charbound, -1 is the 0xFF byte of a grapheme break.
func appendChar(dst []byte, uc int32, charbound bool) []byte {
	switch {
	case uc < 0:
		if charbound && uc == -1 {
			return append(dst, 0xFF)
		}
		return dst
	case uc < 0x80:
		return append(dst, byte(uc))
	case uc < 0x800:
		return append(dst, byte(0xC0+(uc>>6)), byte(0x80+(uc&0x3F)))
	case uc < 0x10000:
		return append(dst, byte(0xE0+(uc>>12)), byte(0x80+((uc>>6)&0x3F)), byte(0x80+(uc&0x3F)))
	case uc < maxCodePoint:
		return append(dst, byte(0xF0+(uc>>18)), byte(0x80+((uc>>12)&0x3F)), byte(0x80+((uc>>6)&0x3F)), byte(0x80+(uc&0x3F)))
	}
	return dst
}

// seqindexWriteCharDecomposed is seqindex_write_char_decomposed
// (utf8proc.c:380-397).
func seqindexWriteCharDecomposed(seqindex uint16, dst []int32, options Option, lastBound *int) int {
	written := 0
	i := int(seqindex & 0x3FFF)
	n := int(seqindex >> 14)
	if n >= 3 {
		n = int(sequences(i))
		i++
	}
	for ; n >= 0; i, n = i+1, n-1 {
		var cp int32
		cp, i = seqindexDecodeEntry(i)
		var room []int32
		if len(dst) > written {
			room = dst[written:]
		}
		// utf8proc returns ErrOverflow here if the decomposition of a code
		// point of the table fails; the table holds assigned code points only,
		// for which it cannot
		written += DecomposeChar(cp, room, options, lastBound)
	}
	return written
}

// DecomposeChar is utf8proc_decompose_char (utf8proc.c:452-538): the code
// points uc decomposes to under options, written to dst as far as it has room;
// the number there are (more than len(dst) when it is too short), or a
// negative error. lastBound is the grapheme state CharBound keeps, which may
// be nil.
func DecomposeChar(uc int32, dst []int32, options Option, lastBound *int) int {
	if uc < 0 || uc >= maxCodePoint {
		return ErrNotAssigned
	}
	property := unsafeGetProperty(uc)
	category := int(property.Category)
	hangulSIndex := int(uc) - hangulSBase
	if options&(Compose|DecomposeOpt) != 0 {
		if hangulSIndex >= 0 && hangulSIndex < hangulSCount {
			if len(dst) >= 1 {
				dst[0] = int32(hangulLBase + hangulSIndex/hangulNCount)
				if len(dst) >= 2 {
					dst[1] = int32(hangulVBase + (hangulSIndex%hangulNCount)/hangulTCount)
				}
			}
			hangulTIndex := hangulSIndex % hangulTCount
			if hangulTIndex == 0 {
				return 2
			}
			if len(dst) >= 3 {
				dst[2] = int32(hangulTBase + hangulTIndex)
			}
			return 3
		}
	}
	if options&RejectNA != 0 && category == 0 {
		return ErrNotAssigned
	}
	if options&Ignore != 0 && property.Ignorable != 0 {
		return 0
	}
	if options&StripNA != 0 && category == 0 {
		return 0
	}
	if options&Lump != 0 {
		lump := func(r int32) int { return DecomposeChar(r, dst, options&^Lump, lastBound) }
		switch {
		case category == categoryZs:
			return lump(0x0020)
		case uc == 0x2018 || uc == 0x2019 || uc == 0x02BC || uc == 0x02C8:
			return lump(0x0027)
		case category == categoryPd || uc == 0x2212:
			return lump(0x002D)
		case uc == 0x2044 || uc == 0x2215:
			return lump(0x002F)
		case uc == 0x2236:
			return lump(0x003A)
		case uc == 0x2039 || uc == 0x2329 || uc == 0x3008:
			return lump(0x003C)
		case uc == 0x203A || uc == 0x232A || uc == 0x3009:
			return lump(0x003E)
		case uc == 0x2216:
			return lump(0x005C)
		case uc == 0x02C4 || uc == 0x02C6 || uc == 0x2038 || uc == 0x2303:
			return lump(0x005E)
		case category == categoryPc || uc == 0x02CD:
			return lump(0x005F)
		case uc == 0x02CB:
			return lump(0x0060)
		case uc == 0x2223:
			return lump(0x007C)
		case uc == 0x223C:
			return lump(0x007E)
		case options&NLF2LS != 0 && options&NLF2PS != 0 && (category == categoryZl || category == categoryZp):
			return lump(0x000A)
		}
	}
	if options&StripMark != 0 && (category == categoryMn || category == categoryMc || category == categoryMe) {
		return 0
	}
	if options&CaseFold != 0 && property.CasefoldSeq != uint16Max {
		return seqindexWriteCharDecomposed(property.CasefoldSeq, dst, options, lastBound)
	}
	if options&(Compose|DecomposeOpt) != 0 && property.DecompSeq != uint16Max && (property.DecompType == 0 || options&Compat != 0) {
		return seqindexWriteCharDecomposed(property.DecompSeq, dst, options, lastBound)
	}
	if options&CharBound != 0 {
		var state int32
		if lastBound != nil {
			state = int32(*lastBound)
		}
		boundary := graphemeBreakExtended(0, int(property.BoundClass), 0, int(property.IndicConjunctBreak), &state)
		if lastBound != nil {
			*lastBound = int(state)
		}
		if boundary {
			if len(dst) >= 1 {
				dst[0] = -1 // sentinel value for a grapheme break
			}
			if len(dst) >= 2 {
				dst[1] = uc
			}
			return 2
		}
	}
	if len(dst) >= 1 {
		dst[0] = uc
	}
	return 1
}

// DecomposeCustom is utf8proc_decompose_custom (utf8proc.c:547-612) over str:
// the code points of the string under options, into buffer as far as it goes;
// how many there are (the size to ask for, if buffer is short), or a negative
// error. custom, if not nil, maps each code point first.
func DecomposeCustom(str []byte, buffer []int32, options Option, custom func(int32) int32) int {
	if options&Compose != 0 && options&DecomposeOpt != 0 {
		return ErrInvalidOpts
	}
	if options&StripMark != 0 && options&Compose == 0 && options&DecomposeOpt == 0 {
		return ErrInvalidOpts
	}
	wpos := 0
	rpos := 0
	boundclass := boundStart
	for {
		var uc int32
		if options&NullTerm != 0 {
			cp, n := iterateNullTerm(str[min(rpos, len(str)):])
			if cp < 0 {
				return ErrInvalidUTF8
			}
			rpos += n
			if cp == 0 {
				break
			}
			uc = cp
		} else {
			if rpos >= len(str) {
				break
			}
			cp, n := Iterate(str[rpos:])
			if cp < 0 {
				return ErrInvalidUTF8
			}
			rpos += n
			uc = cp
		}
		if custom != nil {
			uc = custom(uc)
		}
		var room []int32
		if len(buffer) > wpos {
			room = buffer[wpos:]
		}
		r := DecomposeChar(uc, room, options, &boundclass)
		if r < 0 {
			return r
		}
		wpos += r
		// prohibiting integer overflows due to too long strings
		if wpos < 0 || wpos > maxWords {
			return ErrOverflow
		}
	}
	if options&(Compose|DecomposeOpt) != 0 && len(buffer) >= wpos {
		// canonical ordering of the combining marks
		pos := 0
		for pos < wpos-1 {
			uc1, uc2 := buffer[pos], buffer[pos+1]
			// GetProperty, where C has unsafe_get_property: with CharBound the
			// buffer holds the grapheme-break marker -1, for which C reads the
			// table before its start (undefined); here it is the unassigned code
			// point's properties, combining class 0
			p1, p2 := GetProperty(uc1), GetProperty(uc2)
			if p1.CombiningClass > p2.CombiningClass && p2.CombiningClass > 0 {
				buffer[pos], buffer[pos+1] = uc2, uc1
				if pos > 0 {
					pos--
				} else {
					pos++
				}
			} else {
				pos++
			}
		}
	}
	return wpos
}

// iterateNullTerm is utf8proc_iterate with a length of -1: up to four bytes, a
// NUL byte ending the string (the code point 0).
func iterateNullTerm(str []byte) (int32, int) {
	var four [4]byte
	n := copy(four[:], str)
	if n == 0 {
		return 0, 0 // the string ended without a NUL: as if it were one
	}
	// a NUL inside the four bytes ends them: the bytes after it are not read
	for i := 0; i < n; i++ {
		if four[i] == 0 && i > 0 {
			n = i
			break
		}
	}
	cp, k := Iterate(four[:n])
	if cp < 0 {
		return -1, k
	}
	return cp, k
}

// Decompose is utf8proc_decompose (utf8proc.c:540-545).
func Decompose(str []byte, buffer []int32, options Option) int {
	return DecomposeCustom(str, buffer, options, nil)
}

// NormalizeUTF32 is utf8proc_normalize_utf32 (utf8proc.c:614-728): the newline
// and control transformations, then the composition, in buffer[:length]; the
// new length.
func NormalizeUTF32(buffer []int32, length int, options Option) int {
	if options&(NLF2LS|NLF2PS|StripCC) != 0 {
		wpos := 0
		for rpos := 0; rpos < length; rpos++ {
			uc := buffer[rpos]
			if uc == 0x000D && rpos < length-1 && buffer[rpos+1] == 0x000A {
				rpos++
			}
			if uc == 0x000A || uc == 0x000D || uc == 0x0085 || (options&StripCC != 0 && (uc == 0x000B || uc == 0x000C)) {
				switch {
				case options&NLF2LS != 0 && options&NLF2PS != 0:
					buffer[wpos] = 0x000A
				case options&NLF2LS != 0:
					buffer[wpos] = 0x2028
				case options&NLF2PS != 0:
					buffer[wpos] = 0x2029
				default:
					buffer[wpos] = 0x0020
				}
				wpos++
			} else if options&StripCC != 0 && (uc < 0x0020 || (uc >= 0x007F && uc < 0x00A0)) {
				if uc == 0x0009 {
					buffer[wpos] = 0x0020
					wpos++
				}
			} else {
				buffer[wpos] = uc
				wpos++
			}
		}
		length = wpos
	}
	if options&Compose != 0 {
		starter := -1
		var starterProperty *Property
		maxCombiningClass := int16(-1)
		wpos := 0
		for rpos := 0; rpos < length; rpos++ {
			currentChar := buffer[rpos]
			currentProperty := GetProperty(currentChar) // see DecomposeCustom: the marker -1
			if starter >= 0 && currentProperty.CombiningClass > maxCombiningClass {
				// combination perhaps possible
				hangulLIndex := int(buffer[starter]) - hangulLBase
				if hangulLIndex >= 0 && hangulLIndex < hangulLCount {
					hangulVIndex := int(currentChar) - hangulVBase
					if hangulVIndex >= 0 && hangulVIndex < hangulVCount {
						buffer[starter] = int32(hangulSBase + (hangulLIndex*hangulVCount+hangulVIndex)*hangulTCount)
						starterProperty = nil
						continue
					}
				}
				hangulSIndex := int(buffer[starter]) - hangulSBase
				if hangulSIndex >= 0 && hangulSIndex < hangulSCount && hangulSIndex%hangulTCount == 0 {
					hangulTIndex := int(currentChar) - hangulTBase
					if hangulTIndex >= 0 && hangulTIndex < hangulTCount {
						buffer[starter] += int32(hangulTIndex)
						starterProperty = nil
						continue
					}
				}
				if starterProperty == nil {
					starterProperty = GetProperty(buffer[starter])
				}
				idx := int(starterProperty.CombIndex)
				if idx < 0x3FF && currentProperty.CombIsSecond != 0 {
					n := int(starterProperty.CombLength)
					maxSecond := combSecond(idx + n - 1)
					if currentChar <= maxSecond {
						for off := 0; off < n; off++ {
							second := combSecond(idx + off)
							if currentChar < second {
								break // not found
							}
							if currentChar == second {
								buffer[starter] = combCombined(idx + off)
								starterProperty = nil
								break
							}
						}
						if starterProperty == nil {
							continue // found
						}
					}
				}
			}
			buffer[wpos] = currentChar
			if currentProperty.CombiningClass != 0 {
				if currentProperty.CombiningClass > maxCombiningClass {
					maxCombiningClass = currentProperty.CombiningClass
				}
			} else {
				starter = wpos
				starterProperty = nil
				maxCombiningClass = -1
			}
			wpos++
		}
		length = wpos
	}
	return length
}

// Reencode is utf8proc_reencode (utf8proc.c:730-752): buffer[:length] as
// normalized under options and encoded as UTF-8.
func Reencode(buffer []int32, length int, options Option) []byte {
	length = NormalizeUTF32(buffer, length, options)
	out := make([]byte, 0, length)
	for _, uc := range buffer[:length] {
		out = appendChar(out, uc, options&CharBound != 0)
	}
	return out
}

// MapCustom is utf8proc_map_custom (utf8proc.c:760-788): str mapped under
// options, or a negative error.
func MapCustom(str []byte, options Option, custom func(int32) int32) ([]byte, int) {
	n := DecomposeCustom(str, nil, options, custom)
	if n < 0 {
		return nil, n
	}
	buffer := make([]int32, n)
	n = DecomposeCustom(str, buffer, options, custom)
	if n < 0 {
		return nil, n
	}
	return Reencode(buffer, n, options), 0
}

// Map is utf8proc_map (utf8proc.c:754-758).
func Map(str []byte, options Option) ([]byte, int) { return MapCustom(str, options, nil) }

// NFD, NFC, NFKD, NFKC and NFKCCasefold are the functions of utf8proc.c:790-823
// over a Go string (which may not be valid UTF-8: the error is the third result).
func nf(s string, options Option) (string, int) {
	b, code := Map([]byte(s), options|Stable)
	return string(b), code
}

func NFD(s string) (string, int)  { return nf(s, DecomposeOpt) }
func NFC(s string) (string, int)  { return nf(s, Compose) }
func NFKD(s string) (string, int) { return nf(s, DecomposeOpt|Compat) }
func NFKC(s string) (string, int) { return nf(s, Compose|Compat) }

// NFKCCasefold is utf8proc_NFKC_Casefold.
func NFKCCasefold(s string) (string, int) { return nf(s, Compose|Compat|CaseFold|Ignore) }
