// Package unitab answers the Unicode questions the Julia lexer asks, with
// the answers of the reference build.
//
// The tables in tables_gen.go are not derived from Go's unicode package or
// from the Unicode data files: `jloracle unicode` dumps them from the pinned
// julia, so that each predicate is the one JuliaSyntax evaluates —
// Base.is_id_start_char, Base.is_id_char, Base.Unicode.category_code, isspace,
// utf8proc's grapheme bound classes and textwidth — over the same utf8proc
// version. The grapheme-break state machine is a port of utf8proc's
// grapheme_break_extended (utf8proc.c, v2.10.0, MIT licence; see NOTICE).
package unitab

import "sort"

type rng struct {
	lo, hi uint32
	v      uint8
}

func lookup(t []rng, cp uint32) uint8 {
	i := sort.Search(len(t), func(i int) bool { return t[i].hi >= cp })
	if i < len(t) && t[i].lo <= cp {
		return t[i].v
	}
	return 0
}

// IsIDStart is Base.is_id_start_char of the code point cp.
func IsIDStart(cp uint32) bool { return lookup(idStartTable, cp) != 0 }

// IsIDChar is Base.is_id_char of the code point cp.
func IsIDChar(cp uint32) bool { return lookup(idCharTable, cp) != 0 }

// Category is Base.Unicode.category_code of cp (utf8proc's numbering).
func Category(cp uint32) int { return int(lookup(categoryTable, cp)) }

// IsSpace is Base.isspace of cp.
func IsSpace(cp uint32) bool { return lookup(spaceTable, cp) != 0 }

// Width is Base.textwidth of the character cp.
func Width(cp uint32) int { return int(lookup(widthTable, cp)) }

// Bound returns utf8proc's grapheme bound class and Indic conjunct break
// property of cp.
func Bound(cp uint32) (boundclass, icb int) {
	v := lookup(boundTable, cp)
	return int(v & 0x3f), int(v >> 6)
}

// utf8proc category codes used by the lexer (utf8proc.h).
const (
	CatMn = 6
	CatMc = 7
	CatMe = 8
	CatPc = 12
	CatPd = 13
	CatPo = 18
	CatZs = 23
	CatCs = 28
)

// Grapheme bound classes (utf8proc_boundclass_t).
const (
	bcOther              = 1
	bcCR                 = 2
	bcLF                 = 3
	bcControl            = 4
	bcExtend             = 5
	bcL                  = 6
	bcV                  = 7
	bcT                  = 8
	bcLV                 = 9
	bcLVT                = 10
	bcRegionalIndicator  = 11
	bcSpacingMark        = 12
	bcPrepend            = 13
	bcZWJ                = 14
	bcExtendedPictograph = 19
	bcEZWG               = 20
)

// Indic conjunct break values (utf8proc_indic_conjunct_break_t).
const (
	icbNone      = 0
	icbLinker    = 1
	icbConsonant = 2
	icbExtend    = 3
)

// graphemeBreakSimple is utf8proc's grapheme_break_simple, without its
// first rule (GB1: break after UTF8PROC_BOUNDCLASS_START): no code point
// has that class in the tables (TestLookups checks it), and the state
// machine never stores it, so the rule cannot fire here.
func graphemeBreakSimple(lbc, tbc int) bool {
	switch {
	case lbc == bcCR && tbc == bcLF:
		return false
	case lbc >= bcCR && lbc <= bcControl:
		return true
	case tbc >= bcCR && tbc <= bcControl:
		return true
	case lbc == bcL && (tbc == bcL || tbc == bcV || tbc == bcLV || tbc == bcLVT):
		return false
	case (lbc == bcLV || lbc == bcV) && (tbc == bcV || tbc == bcT):
		return false
	case (lbc == bcLVT || lbc == bcT) && tbc == bcT:
		return false
	case tbc == bcExtend || tbc == bcZWJ || tbc == bcSpacingMark || lbc == bcPrepend:
		return false
	case lbc == bcEZWG && tbc == bcExtendedPictograph:
		return false
	case lbc == bcRegionalIndicator && tbc == bcRegionalIndicator:
		return false
	}
	return true
}

// GraphemeBreak is utf8proc_grapheme_break_stateful(c1, c2, state): whether
// a grapheme cluster boundary lies between the code points c1 and c2, with
// *state threading the context of the previous pairs (start it at 0).
func GraphemeBreak(state *int32, c1, c2 uint32) bool {
	lbc, licb := Bound(c1)
	tbc, ticb := Bound(c2)
	var stateBC, stateICB int
	if *state == 0 {
		stateBC = lbc
		stateICB = icbNone
		if licb == icbConsonant {
			stateICB = licb
		}
	} else {
		stateBC = int(*state & 0xff)
		stateICB = int(*state >> 8)
	}
	breakPermitted := graphemeBreakSimple(stateBC, tbc) &&
		!(stateICB == icbLinker && ticb == icbConsonant)
	if ticb == icbConsonant || stateICB == icbConsonant || stateICB == icbExtend {
		stateICB = ticb
	} else if stateICB == icbLinker {
		if ticb == icbExtend {
			stateICB = icbLinker
		} else {
			stateICB = ticb
		}
	}
	switch {
	case stateBC == tbc && tbc == bcRegionalIndicator:
		stateBC = bcOther
	case stateBC == bcExtendedPictograph:
		switch tbc {
		case bcExtend:
			stateBC = bcExtendedPictograph
		case bcZWJ:
			stateBC = bcEZWG
		default:
			stateBC = tbc
		}
	default:
		stateBC = tbc
	}
	*state = int32(stateBC + stateICB<<8)
	return breakPermitted
}
