package lower

// The operator tables of src/julia-parser.scm (julia 1.12.6) that lowering
// consults: dotop?, is-prec-assignment? and strip-op-suffix.

import (
	"strings"
	"unicode/utf8"

	"github.com/go-embedded-julia/julia/syntax"
)

func fields(s string) []string { return strings.Fields(s) }

// addDots is add-dots: the operators followed by their dotted forms.
func addDots(ops []string) []string {
	out := append([]string{}, ops...)
	for _, op := range ops {
		out = append(out, "."+op)
	}
	return out
}

var (
	precAssignmentList = append(append(
		addDots(fields(`= += -= −= *= /= //= \= ^= ÷= %= <<= >>= >>>= |= &= ⊻= ≔ ⩴ ≕`)),
		addDots(fields(`~`))...), ":=", "$=")
	precPair        = addDots(fields(`=>`))
	precConditional = fields(`?`)
	precArrow       = addDots(fields(`← → ↔ ↚ ↛ ↞ ↠ ↢ ↣ ↦ ↤ ↮ ⇎ ⇍ ⇏ ⇐ ⇒ ⇔ ⇴ ⇶ ⇷ ⇸ ⇹ ⇺ ⇻ ⇼ ⇽ ⇾ ⇿ ⟵ ⟶ ⟷ ⟹ ⟺ ⟻ ⟼ ⟽ ⟾ ⟿ ⤀ ⤁ ⤂ ⤃ ⤄ ⤅ ⤆ ⤇ ⤌ ⤍ ⤎ ⤏ ⤐ ⤑ ⤔ ⤕ ⤖ ⤗ ⤘ ⤝ ⤞ ⤟ ⤠ ⥄ ⥅ ⥆ ⥇ ⥈ ⥊ ⥋ ⥎ ⥐ ⥒ ⥓ ⥖ ⥗ ⥚ ⥛ ⥞ ⥟ ⥢ ⥤ ⥦ ⥧ ⥨ ⥩ ⥪ ⥫ ⥬ ⥭ ⥰ ⧴ ⬱ ⬰ ⬲ ⬳ ⬴ ⬵ ⬶ ⬷ ⬸ ⬹ ⬺ ⬻ ⬼ ⬽ ⬾ ⬿ ⭀ ⭁ ⭂ ⭃ ⥷ ⭄ ⥺ ⭇ ⭈ ⭉ ⭊ ⭋ ⭌ ￩ ￫ ⇜ ⇝ ↜ ↝ ↩ ↪ ↫ ↬ ↼ ↽ ⇀ ⇁ ⇄ ⇆ ⇇ ⇉ ⇋ ⇌ ⇚ ⇛ ⇠ ⇢ ↷ ↶ ↺ ↻ --> <-- <--> 🢲`))
	precLazyOr      = addDots(fields(`||`))
	precLazyAnd     = addDots(fields(`&&`))
	precComparison  = append(fields(`in isa`), addDots(fields(`> < >= ≥ <= ≤ == === ≡ != ≠ !== ≢ ∈ ∉ ∋ ∌ ⊆ ⊈ ⊂ ⊄ ⊊ ∝ ∊ ∍ ∥ ∦ ∷ ∺ ∻ ∽ ∾ ≁ ≃ ≂ ≄ ≅ ≆ ≇ ≈ ≉ ≊ ≋ ≌ ≍ ≎ ≐ ≑ ≒ ≓ ≖ ≗ ≘ ≙ ≚ ≛ ≜ ≝ ≞ ≟ ≣ ≦ ≧ ≨ ≩ ≪ ≫ ≬ ≭ ≮ ≯ ≰ ≱ ≲ ≳ ≴ ≵ ≶ ≷ ≸ ≹ ≺ ≻ ≼ ≽ ≾ ≿ ⊀ ⊁ ⊃ ⊅ ⊇ ⊉ ⊋ ⊏ ⊐ ⊑ ⊒ ⊜ ⊩ ⊬ ⊮ ⊰ ⊱ ⊲ ⊳ ⊴ ⊵ ⊶ ⊷ ⋍ ⋐ ⋑ ⋕ ⋖ ⋗ ⋘ ⋙ ⋚ ⋛ ⋜ ⋝ ⋞ ⋟ ⋠ ⋡ ⋢ ⋣ ⋤ ⋥ ⋦ ⋧ ⋨ ⋩ ⋪ ⋫ ⋬ ⋭ ⋲ ⋳ ⋴ ⋵ ⋶ ⋷ ⋸ ⋹ ⋺ ⋻ ⋼ ⋽ ⋾ ⋿ ⟈ ⟉ ⟒ ⦷ ⧀ ⧁ ⧡ ⧣ ⧤ ⧥ ⩦ ⩧ ⩪ ⩫ ⩬ ⩭ ⩮ ⩯ ⩰ ⩱ ⩲ ⩳ ⩵ ⩶ ⩷ ⩸ ⩹ ⩺ ⩻ ⩼ ⩽ ⩾ ⩿ ⪀ ⪁ ⪂ ⪃ ⪄ ⪅ ⪆ ⪇ ⪈ ⪉ ⪊ ⪋ ⪌ ⪍ ⪎ ⪏ ⪐ ⪑ ⪒ ⪓ ⪔ ⪕ ⪖ ⪗ ⪘ ⪙ ⪚ ⪛ ⪜ ⪝ ⪞ ⪟ ⪠ ⪡ ⪢ ⪣ ⪤ ⪥ ⪦ ⪧ ⪨ ⪩ ⪪ ⪫ ⪬ ⪭ ⪮ ⪯ ⪰ ⪱ ⪲ ⪳ ⪴ ⪵ ⪶ ⪷ ⪸ ⪹ ⪺ ⪻ ⪼ ⪽ ⪾ ⪿ ⫀ ⫁ ⫂ ⫃ ⫄ ⫅ ⫆ ⫇ ⫈ ⫉ ⫊ ⫋ ⫌ ⫍ ⫎ ⫏ ⫐ ⫑ ⫒ ⫓ ⫔ ⫕ ⫖ ⫗ ⫘ ⫙ ⫷ ⫸ ⫹ ⫺ ⊢ ⊣ ⟂ ⫪ ⫫ <: >:`))...)
	precPipeLt      = fields(`.<| <|`)
	precPipeGt      = fields(`.|> |>`)
	precColon       = append(fields(`: ..`), addDots(fields(`… ⁝ ⋮ ⋱ ⋰ ⋯`))...)
	precPlus        = append(fields(`$`), addDots(fields(`+ - − ¦ | ⊕ ⊖ ⊞ ⊟ ++ ∪ ∨ ⊔ ± ∓ ∔ ∸ ≏ ⊎ ⊻ ⊽ ⋎ ⋓ ⟇ ⧺ ⧻ ⨈ ⨢ ⨣ ⨤ ⨥ ⨦ ⨧ ⨨ ⨩ ⨪ ⨫ ⨬ ⨭ ⨮ ⨹ ⨺ ⩁ ⩂ ⩅ ⩊ ⩌ ⩏ ⩐ ⩒ ⩔ ⩖ ⩗ ⩛ ⩝ ⩡ ⩢ ⩣`))...)
	precTimes       = addDots(fields(`* / ⌿ ÷ % & · · ⋅ ∘ × \ ∩ ∧ ⊗ ⊘ ⊙ ⊚ ⊛ ⊠ ⊡ ⊓ ∗ ∙ ∤ ⅋ ≀ ⊼ ⋄ ⋆ ⋇ ⋉ ⋊ ⋋ ⋌ ⋏ ⋒ ⟑ ⦸ ⦼ ⦾ ⦿ ⧶ ⧷ ⨇ ⨰ ⨱ ⨲ ⨳ ⨴ ⨵ ⨶ ⨷ ⨸ ⨻ ⨼ ⨽ ⩀ ⩃ ⩄ ⩋ ⩍ ⩎ ⩑ ⩓ ⩕ ⩘ ⩚ ⩜ ⩞ ⩟ ⩠ ⫛ ⊍ ▷ ⨝ ⟕ ⟖ ⟗ ⨟`))
	precRational    = addDots(fields(`//`))
	precBitshift    = addDots(fields(`<< >> >>>`))
	precPower       = addDots(fields(`^ ↑ ↓ ⇵ ⟰ ⟱ ⤈ ⤉ ⤊ ⤋ ⤒ ⤓ ⥉ ⥌ ⥍ ⥏ ⥑ ⥔ ⥕ ⥘ ⥙ ⥜ ⥝ ⥠ ⥡ ⥣ ⥥ ⥮ ⥯ ￪ ￬`))
	precDecl        = fields(`::`)
	precDot         = fields(`.`)

	unaryOps = append(fields(`<: >:`), addDots(fields(`+ - ! ~ ¬ √ ∛ ∜ ⋆ ± ∓`))...)
)

func setOf(lists ...[]string) map[string]bool {
	m := map[string]bool{}
	for _, l := range lists {
		for _, s := range l {
			m[s] = true
		}
	}
	return m
}

var (
	noSuffix = setOf(precAssignmentList, precConditional, precLazyOr, precLazyAnd, precColon,
		precDecl, precDot, fields(`-> <: >: in isa $`), []string{"'", ".'", "..."})
	precAssignment = setOf(precAssignmentList)
	dotOperators   = func() map[string]bool {
		all := setOf([]string{"->", "'", ".'", "..."}, unaryOps, precAssignmentList, precPair,
			precConditional, precArrow, precLazyOr, precLazyAnd, precComparison, precPipeLt,
			precPipeGt, precColon, precPlus, precTimes, precRational, precBitshift, precPower,
			precDecl, precDot)
		m := map[string]bool{}
		for op := range all {
			if isWordOperator(op) || op == "." || op[0] != '.' || (len(op) > 1 && op[1] == '.') {
				continue
			}
			m[op] = true
		}
		return m
	}()
)

// isWordOperator: every character starts an identifier (in, isa).
func isWordOperator(op string) bool {
	for _, c := range op {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_') {
			return false
		}
	}
	return true
}

// stripOpSuffix is strip-op-suffix (src/flisp/julia_extensions.c).
func stripOpSuffix(op string) string {
	for i := 0; i < len(op); {
		c, n := utf8.DecodeRuneInString(op[i:])
		if syntax.IsOpSuffixChar(c) {
			if i == 0 {
				return op
			}
			return op[:i]
		}
		i += n
	}
	return op
}

// maybeStripOpSuffix is maybe-strip-op-suffix.
func maybeStripOpSuffix(op string) string {
	s := stripOpSuffix(op)
	if s == op || noSuffix[s] {
		return op
	}
	return s
}

func suffSet(set map[string]bool, x any) bool {
	s, ok := x.(sym)
	if !ok {
		return false
	}
	return set[maybeStripOpSuffix(string(s))]
}

func isDotop(x any) bool            { return suffSet(dotOperators, x) }
func isPrecAssignment(x any) bool   { return suffSet(precAssignment, x) }
func eventuallyCall(ex any) bool {
	return isPair(ex) && (car(ex) == sCall ||
		((car(ex) == sym("where") || car(ex) == sDecl) && eventuallyCall(cadr(ex))))
}
func hasParameters(l any) bool {
	return isPair(l) && is(car(l), "parameters")
}
