// Ported from JuliaSyntax src/kinds.jl (Julia 1.12.6, JuliaSyntax
// 29ec5226f7b1).
// Copyright (c) 2021 Julia Computing and contributors. MIT "Expat" License;
// see NOTICE.

package syntax

import "fmt"

// Kind is the type tag of a token or syntax tree node, as JuliaSyntax's
// Kind. Kinds are numbered exactly as kinds.jl registers them, BEGIN_/END_
// category markers aliasing the first and last kind of their category, so
// that a category test is a range comparison.
type Kind uint16

// kindNames is the list of kinds.jl's register_kinds!(JuliaSyntax, 0, [...]),
// in order.
var kindNames = []string{
	// Whitespace
	"Comment",
	"Whitespace",
	"NewlineWs",

	// Identifiers
	"BEGIN_IDENTIFIERS",
	"Identifier",
	"Placeholder",
	"BEGIN_MACRO_NAMES",
	"MacroName",
	"StringMacroName",
	"CmdMacroName",
	"END_MACRO_NAMES",
	"END_IDENTIFIERS",

	"BEGIN_KEYWORDS",
	"baremodule",
	"begin",
	"break",
	"const",
	"continue",
	"do",
	"export",
	"for",
	"function",
	"global",
	"if",
	"import",
	"let",
	"local",
	"macro",
	"module",
	"quote",
	"return",
	"struct",
	"try",
	"using",
	"while",
	"BEGIN_BLOCK_CONTINUATION_KEYWORDS",
	"catch",
	"finally",
	"else",
	"elseif",
	"end",
	"END_BLOCK_CONTINUATION_KEYWORDS",
	"BEGIN_CONTEXTUAL_KEYWORDS",
	"abstract",
	"as",
	"doc",
	"mutable",
	"outer",
	"primitive",
	"public",
	"type",
	"var",
	"END_CONTEXTUAL_KEYWORDS",
	"END_KEYWORDS",

	"BEGIN_LITERAL",
	"BEGIN_NUMBERS",
	"Bool",
	"Integer",
	"BinInt",
	"HexInt",
	"OctInt",
	"Float",
	"Float32",
	"END_NUMBERS",
	"String",
	"Char",
	"CmdString",
	"END_LITERAL",

	"BEGIN_DELIMITERS",
	"@",
	",",
	";",
	"[",
	"]",
	"{",
	"}",
	"(",
	")",
	"\"",
	"\"\"\"",
	"`",
	"```",
	"END_DELIMITERS",

	"BEGIN_OPS",
	"ErrorInvalidOperator",
	"Error**",

	"...",

	// Level 1
	"BEGIN_ASSIGNMENTS",
	"BEGIN_SYNTACTIC_ASSIGNMENTS",
	"=",
	"op=",
	":=",
	"END_SYNTACTIC_ASSIGNMENTS",
	"~",
	"≔",
	"⩴",
	"≕",
	"END_ASSIGNMENTS",

	"BEGIN_PAIRARROW",
	"=>",
	"END_PAIRARROW",

	// Level 2
	"BEGIN_CONDITIONAL",
	"?",
	"END_CONDITIONAL",

	// Level 3
	"BEGIN_ARROW",
	"-->", "<--", "<-->", "←", "→", "↔", "↚", "↛", "↞", "↠", "↢", "↣", "↤", "↦",
	"↮", "⇎", "⇍", "⇏", "⇐", "⇒", "⇔", "⇴", "⇶", "⇷", "⇸", "⇹", "⇺", "⇻", "⇼",
	"⇽", "⇾", "⇿", "⟵", "⟶", "⟷", "⟹", "⟺", "⟻", "⟼", "⟽", "⟾", "⟿", "⤀", "⤁",
	"⤂", "⤃", "⤄", "⤅", "⤆", "⤇", "⤌", "⤍", "⤎", "⤏", "⤐", "⤑", "⤔", "⤕", "⤖",
	"⤗", "⤘", "⤝", "⤞", "⤟", "⤠", "⥄", "⥅", "⥆", "⥇", "⥈", "⥊", "⥋", "⥎", "⥐",
	"⥒", "⥓", "⥖", "⥗", "⥚", "⥛", "⥞", "⥟", "⥢", "⥤", "⥦", "⥧", "⥨", "⥩", "⥪",
	"⥫", "⥬", "⥭", "⥰", "⧴", "⬱", "⬰", "⬲", "⬳", "⬴", "⬵", "⬶", "⬷", "⬸", "⬹",
	"⬺", "⬻", "⬼", "⬽", "⬾", "⬿", "⭀", "⭁", "⭂", "⭃", "⥷", "⭄", "⥺", "⭇", "⭈",
	"⭉", "⭊", "⭋", "⭌", "￩", "￫", "⇜", "⇝", "↜", "↝", "↩", "↪", "↫", "↬", "↼",
	"↽", "⇀", "⇁", "⇄", "⇆", "⇇", "⇉", "⇋", "⇌", "⇚", "⇛", "⇠", "⇢", "↷", "↶",
	"↺", "↻", "🢲",
	"END_ARROW",

	// Level 4
	"BEGIN_LAZYOR",
	"||",
	"END_LAZYOR",

	// Level 5
	"BEGIN_LAZYAND",
	"&&",
	"END_LAZYAND",

	// Level 6
	"BEGIN_COMPARISON",
	"<:", ">:", ">", "<", ">=", "≥", "<=", "≤", "==", "===", "≡", "!=", "≠", "!==",
	"≢", "∈", "in", "isa", "∉", "∋", "∌", "⊆", "⊈", "⊂", "⊄", "⊊", "∝", "∊", "∍",
	"∥", "∦", "∷", "∺", "∻", "∽", "∾", "≁", "≃", "≂", "≄", "≅", "≆", "≇", "≈", "≉",
	"≊", "≋", "≌", "≍", "≎", "≐", "≑", "≒", "≓", "≖", "≗", "≘", "≙", "≚", "≛", "≜",
	"≝", "≞", "≟", "≣", "≦", "≧", "≨", "≩", "≪", "≫", "≬", "≭", "≮", "≯", "≰", "≱",
	"≲", "≳", "≴", "≵", "≶", "≷", "≸", "≹", "≺", "≻", "≼", "≽", "≾", "≿", "⊀", "⊁",
	"⊃", "⊅", "⊇", "⊉", "⊋", "⊏", "⊐", "⊑", "⊒", "⊜", "⊩", "⊬", "⊮", "⊰", "⊱", "⊲",
	"⊳", "⊴", "⊵", "⊶", "⊷", "⋍", "⋐", "⋑", "⋕", "⋖", "⋗", "⋘", "⋙", "⋚", "⋛", "⋜",
	"⋝", "⋞", "⋟", "⋠", "⋡", "⋢", "⋣", "⋤", "⋥", "⋦", "⋧", "⋨", "⋩", "⋪", "⋫", "⋬",
	"⋭", "⋲", "⋳", "⋴", "⋵", "⋶", "⋷", "⋸", "⋹", "⋺", "⋻", "⋼", "⋽", "⋾", "⋿", "⟈",
	"⟉", "⟒", "⦷", "⧀", "⧁", "⧡", "⧣", "⧤", "⧥", "⩦", "⩧", "⩪", "⩫", "⩬", "⩭", "⩮",
	"⩯", "⩰", "⩱", "⩲", "⩳", "⩵", "⩶", "⩷", "⩸", "⩹", "⩺", "⩻", "⩼", "⩽", "⩾", "⩿",
	"⪀", "⪁", "⪂", "⪃", "⪄", "⪅", "⪆", "⪇", "⪈", "⪉", "⪊", "⪋", "⪌", "⪍", "⪎", "⪏",
	"⪐", "⪑", "⪒", "⪓", "⪔", "⪕", "⪖", "⪗", "⪘", "⪙", "⪚", "⪛", "⪜", "⪝", "⪞", "⪟",
	"⪠", "⪡", "⪢", "⪣", "⪤", "⪥", "⪦", "⪧", "⪨", "⪩", "⪪", "⪫", "⪬", "⪭", "⪮", "⪯",
	"⪰", "⪱", "⪲", "⪳", "⪴", "⪵", "⪶", "⪷", "⪸", "⪹", "⪺", "⪻", "⪼", "⪽", "⪾", "⪿",
	"⫀", "⫁", "⫂", "⫃", "⫄", "⫅", "⫆", "⫇", "⫈", "⫉", "⫊", "⫋", "⫌", "⫍", "⫎", "⫏",
	"⫐", "⫑", "⫒", "⫓", "⫔", "⫕", "⫖", "⫗", "⫘", "⫙", "⫷", "⫸", "⫹", "⫺", "⊢", "⊣",
	"⟂",
	// ⫪,⫫ see https://github.com/JuliaLang/julia/issues/39350
	"⫪", "⫫",
	"END_COMPARISON",

	// Level 7
	"BEGIN_PIPE",
	"<|",
	"|>",
	"END_PIPE",

	// Level 8
	"BEGIN_COLON",
	":", "..", "…", "⁝", "⋮", "⋱", "⋰", "⋯",
	"END_COLON",

	// Level 9
	"BEGIN_PLUS",
	"$", "+", "-", "++", "⊕", "⊖", "⊞", "⊟", "|", "∪", "∨", "⊔", "±", "∓", "∔", "∸",
	"≏", "⊎", "⊻", "⊽", "⋎", "⋓", "⟇", "⧺", "⧻", "⨈", "⨢", "⨣", "⨤", "⨥", "⨦", "⨧",
	"⨨", "⨩", "⨪", "⨫", "⨬", "⨭", "⨮", "⨹", "⨺", "⩁", "⩂", "⩅", "⩊", "⩌", "⩏", "⩐",
	"⩒", "⩔", "⩖", "⩗", "⩛", "⩝", "⩡", "⩢", "⩣", "¦",
	"END_PLUS",

	// Level 10
	"BEGIN_TIMES",
	"*", "/", "÷", "%", "⋅", "∘", "×", "\\", "&", "∩", "∧", "⊗", "⊘", "⊙", "⊚", "⊛",
	"⊠", "⊡", "⊓", "∗", "∙", "∤", "⅋", "≀", "⊼", "⋄", "⋆", "⋇", "⋉", "⋊", "⋋", "⋌",
	"⋏", "⋒", "⟑", "⦸", "⦼", "⦾", "⦿", "⧶", "⧷", "⨇", "⨰", "⨱", "⨲", "⨳", "⨴", "⨵",
	"⨶", "⨷", "⨸", "⨻", "⨼", "⨽", "⩀", "⩃", "⩄", "⩋", "⩍", "⩎", "⩑", "⩓", "⩕", "⩘",
	"⩚", "⩜", "⩞", "⩟", "⩠", "⫛", "⊍", "▷", "⨝", "⟕", "⟖", "⟗", "⌿", "⨟",
	"END_TIMES",

	// Level 11
	"BEGIN_RATIONAL",
	"//",
	"END_RATIONAL",

	// Level 12
	"BEGIN_BITSHIFTS",
	"<<",
	">>",
	">>>",
	"END_BITSHIFTS",

	// Level 13
	"BEGIN_POWER",
	"^", "↑", "↓", "⇵", "⟰", "⟱", "⤈", "⤉", "⤊", "⤋", "⤒", "⤓", "⥉", "⥌", "⥍", "⥏",
	"⥑", "⥔", "⥕", "⥘", "⥙", "⥜", "⥝", "⥠", "⥡", "⥣", "⥥", "⥮", "⥯", "￪", "￬",
	"END_POWER",

	// Level 14
	"BEGIN_DECL",
	"::",
	"END_DECL",

	// Level 15
	"BEGIN_WHERE",
	"where",
	"END_WHERE",

	// Level 16
	"BEGIN_DOT",
	".",
	"END_DOT",

	"!",
	"'",
	".'",
	"->",

	"BEGIN_UNICODE_OPS",
	"¬",
	"√",
	"∛",
	"∜",
	"END_UNICODE_OPS",
	"END_OPS",

	// Nonterminals which are exposed in the AST, but where the surface syntax
	// doesn't have a token corresponding to the node type.
	"BEGIN_SYNTAX_KINDS",
	"block",
	"call",
	"dotcall",
	"comparison",
	"curly",
	"inert",
	"juxtapose",
	"string",
	"cmdstring",
	"char",
	"macrocall",
	"parameters",
	"toplevel",
	"tuple",
	"ref",
	"vect",
	"parens",
	"importpath",
	"braces",
	"bracescat",
	"hcat",
	"vcat",
	"ncat",
	"typed_hcat",
	"typed_vcat",
	"typed_ncat",
	"row",
	"nrow",
	"generator",
	"filter",
	"iteration",
	"comprehension",
	"typed_comprehension",
	"wrapper",
	"END_SYNTAX_KINDS",

	// Special tokens
	"TOMBSTONE",
	"None",
	"EndMarker",

	"BEGIN_ERRORS",
	"ErrorEofMultiComment",
	"ErrorInvalidNumericConstant",
	"ErrorHexFloatMustContainP",
	"ErrorAmbiguousNumericConstant",
	"ErrorAmbiguousNumericDotMultiply",
	"ErrorInvalidInterpolationTerminator",
	"ErrorNumericOverflow",
	"ErrorInvalidEscapeSequence",
	"ErrorOverLongCharacter",
	"ErrorInvalidUTF8",
	"ErrorInvisibleChar",
	"ErrorIdentifierStart",
	"ErrorUnknownCharacter",
	"ErrorBidiFormatting",
	"error",
	"END_ERRORS",
}

// The kind tables are built by a variable initializer, not by init(), because
// the k* variables below are initialized from them and package variables are
// initialized before any init function runs.
var kindByName, kindString = registerKinds()

// registerKinds is _register_kinds! with module_id 0.
func registerKinds() (map[string]Kind, map[Kind]string) {
	kindByName := map[string]Kind{}
	kindString := map[Kind]string{}
	i := 0
	for _, name := range kindNames {
		var j int
		normal := false
		switch {
		case len(name) > 6 && name[:6] == "BEGIN_":
			j = i
		case len(name) > 4 && name[:4] == "END_":
			j = i - 1
		default:
			normal = true
			j = i
			i++
		}
		kindByName[name] = Kind(j)
		if normal {
			kindString[Kind(j)] = name
		}
	}
	return kindByName, kindString
}

// K returns the kind named s, as JuliaSyntax's K"s". It panics on an
// unknown name, which is a programming error.
func K(s string) Kind {
	k, ok := kindByName[s]
	if !ok {
		panic(fmt.Sprintf("unknown Kind name %q", s))
	}
	return k
}

// String returns the name of the kind.
func (k Kind) String() string {
	if s, ok := kindString[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", uint16(k))
}

func between(k Kind, lo, hi Kind) bool { return lo <= k && k <= hi }

// Kind category predicates of kinds.jl that the lexer uses; those of the
// parser are in predicates.go.

func isContextualKeyword(k Kind) bool { return between(k, kBeginContextual, kEndContextual) }
func isLiteral(k Kind) bool           { return between(k, kBeginLiteral, kEndLiteral) }
func isOperator(k Kind) bool          { return between(k, kBeginOps, kEndOps) }
func isWordOperator(k Kind) bool      { return k == kIn || k == kIsa || k == kWhere }
func isPrecAssignment(k Kind) bool    { return between(k, kBeginAssignments, kEndAssignments) }

// Kinds referred to by the lexer and the parser.
var (
	kComment, kWhitespace, kNewlineWs                     = K("Comment"), K("Whitespace"), K("NewlineWs")
	kIdentifier, kPlaceholder                             = K("Identifier"), K("Placeholder")
	kMacroName, kStringMacroName, kCmdMacroName           = K("MacroName"), K("StringMacroName"), K("CmdMacroName")
	kBeginKeywords, kEndKeywords                          = K("BEGIN_KEYWORDS"), K("END_KEYWORDS")
	kBeginBlockContinuation, kEndBlockContinuation        = K("BEGIN_BLOCK_CONTINUATION_KEYWORDS"), K("END_BLOCK_CONTINUATION_KEYWORDS")
	kBeginContextual, kEndContextual                      = K("BEGIN_CONTEXTUAL_KEYWORDS"), K("END_CONTEXTUAL_KEYWORDS")
	kBaremodule, kBegin, kBreak, kConst, kContinue, kDo   = K("baremodule"), K("begin"), K("break"), K("const"), K("continue"), K("do")
	kExport, kFor, kFunction, kGlobal, kIf, kImport       = K("export"), K("for"), K("function"), K("global"), K("if"), K("import")
	kLet, kLocal, kMacro, kModule, kQuote, kReturn        = K("let"), K("local"), K("macro"), K("module"), K("quote"), K("return")
	kStruct, kTry, kUsing, kWhile                         = K("struct"), K("try"), K("using"), K("while")
	kCatch, kFinally, kElse, kElseif, kEnd                = K("catch"), K("finally"), K("else"), K("elseif"), K("end")
	kAbstract, kAs, kDoc, kMutable, kOuter, kPrimitive    = K("abstract"), K("as"), K("doc"), K("mutable"), K("outer"), K("primitive")
	kPublic, kType, kVar                                  = K("public"), K("type"), K("var")
	kBeginLiteral, kEndLiteral                            = K("BEGIN_LITERAL"), K("END_LITERAL")
	kBeginNumbers, kEndNumbers                            = K("BEGIN_NUMBERS"), K("END_NUMBERS")
	kBool, kInteger, kBinInt, kHexInt, kOctInt            = K("Bool"), K("Integer"), K("BinInt"), K("HexInt"), K("OctInt")
	kFloat, kFloat32, kString, kChar, kCmdString          = K("Float"), K("Float32"), K("String"), K("Char"), K("CmdString")
	kAt, kComma, kSemicolon                               = K("@"), K(","), K(";")
	kLsquare, kRsquare, kLbrace, kRbrace, kLparen         = K("["), K("]"), K("{"), K("}"), K("(")
	kRparen, kDquote, kTripleDquote, kBacktick            = K(")"), K("\""), K("\"\"\""), K("`")
	kTripleBacktick                                       = K("```")
	kBeginOps, kEndOps                                    = K("BEGIN_OPS"), K("END_OPS")
	kErrorInvalidOperator, kErrorStarStar, kDotDotDot     = K("ErrorInvalidOperator"), K("Error**"), K("...")
	kEq, kOpEq, kColonEq, kTilde                          = K("="), K("op="), K(":="), K("~")
	kColonEqUnicode, kAssignUnicode2                      = K("≔"), K("⩴")
	kPairArrow, kQuestion                                 = K("=>"), K("?")
	kBeginArrow, kEndArrow, kLongArrow                    = K("BEGIN_ARROW"), K("END_ARROW"), K("-->")
	kLeftLongArrow, kBothLongArrow                        = K("<--"), K("<-->")
	kOrOr, kAndAnd                                        = K("||"), K("&&")
	kBeginComparison, kEndComparison                      = K("BEGIN_COMPARISON"), K("END_COMPARISON")
	kSubtype, kSupertype, kGreater, kLess                 = K("<:"), K(">:"), K(">"), K("<")
	kGreaterEq, kLessEq, kEqEq, kEqEqEq, kNotEq, kNotEqEq = K(">="), K("<="), K("=="), K("==="), K("!="), K("!==")
	kIn, kIsa, kElementOf                                 = K("in"), K("isa"), K("∈")
	kPipeLt, kPipeGt                                      = K("<|"), K("|>")
	kBeginColon, kEndColon, kColon, kDotDot               = K("BEGIN_COLON"), K("END_COLON"), K(":"), K("..")
	kBeginPlus, kEndPlus                                  = K("BEGIN_PLUS"), K("END_PLUS")
	kDollar, kPlus, kMinus, kPlusPlus, kBar               = K("$"), K("+"), K("-"), K("++"), K("|")
	kPlusMinus, kMinusPlus, kXor                          = K("±"), K("∓"), K("⊻")
	kBeginTimes, kEndTimes                                = K("BEGIN_TIMES"), K("END_TIMES")
	kStar, kSlash, kDivide, kPercent, kCdot, kBackslash   = K("*"), K("/"), K("÷"), K("%"), K("⋅"), K("\\")
	kAmp, kStarOp                                         = K("&"), K("⋆")
	kRational, kShl, kShr, kUshr                          = K("//"), K("<<"), K(">>"), K(">>>")
	kBeginPower, kEndPower, kCaret                        = K("BEGIN_POWER"), K("END_POWER"), K("^")
	kDecl, kWhere, kDot                                   = K("::"), K("where"), K(".")
	kBeginAssignments, kEndAssignments                    = K("BEGIN_ASSIGNMENTS"), K("END_ASSIGNMENTS")
	kNot, kPrime, kDotPrime, kArrow                       = K("!"), K("'"), K(".'"), K("->")
	kNotSign, kSqrt, kCbrt, kFourthRoot                   = K("¬"), K("√"), K("∛"), K("∜")
	kBeginSyntaxKinds, kEndSyntaxKinds                    = K("BEGIN_SYNTAX_KINDS"), K("END_SYNTAX_KINDS")
	kBlock, kCall, kDotcall, kComparison, kCurly, kInert  = K("block"), K("call"), K("dotcall"), K("comparison"), K("curly"), K("inert")
	kJuxtapose, kStringNode, kCmdstring, kCharNode        = K("juxtapose"), K("string"), K("cmdstring"), K("char")
	kMacrocall, kParameters, kToplevel, kTuple, kRef      = K("macrocall"), K("parameters"), K("toplevel"), K("tuple"), K("ref")
	kVect, kParens, kImportpath, kBraces, kBracescat      = K("vect"), K("parens"), K("importpath"), K("braces"), K("bracescat")
	kHcat, kVcat, kNcat, kTypedHcat, kTypedVcat           = K("hcat"), K("vcat"), K("ncat"), K("typed_hcat"), K("typed_vcat")
	kTypedNcat, kRow, kNrow, kGenerator, kFilter          = K("typed_ncat"), K("row"), K("nrow"), K("generator"), K("filter")
	kIteration, kComprehension, kTypedComprehension       = K("iteration"), K("comprehension"), K("typed_comprehension")
	kWrapper, kTombstone, kNone, kEndMarker               = K("wrapper"), K("TOMBSTONE"), K("None"), K("EndMarker")
	kBeginErrors, kEndErrors                              = K("BEGIN_ERRORS"), K("END_ERRORS")
	kErrorEofMultiComment                                 = K("ErrorEofMultiComment")
	kErrorInvalidNumericConstant                          = K("ErrorInvalidNumericConstant")
	kErrorHexFloatMustContainP                            = K("ErrorHexFloatMustContainP")
	kErrorAmbiguousNumericConstant                        = K("ErrorAmbiguousNumericConstant")
	kErrorAmbiguousNumericDotMultiply                     = K("ErrorAmbiguousNumericDotMultiply")
	kErrorInvalidInterpolationTerminator                  = K("ErrorInvalidInterpolationTerminator")
	kErrorNumericOverflow                                 = K("ErrorNumericOverflow")
	kErrorInvalidEscapeSequence                           = K("ErrorInvalidEscapeSequence")
	kErrorOverLongCharacter                               = K("ErrorOverLongCharacter")
	kErrorInvalidUTF8, kErrorInvisibleChar                = K("ErrorInvalidUTF8"), K("ErrorInvisibleChar")
	kErrorIdentifierStart, kErrorUnknownCharacter         = K("ErrorIdentifierStart"), K("ErrorUnknownCharacter")
	kErrorBidiFormatting, kError                          = K("ErrorBidiFormatting"), K("error")
)
