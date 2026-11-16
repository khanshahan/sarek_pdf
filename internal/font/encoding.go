// Package font decodes PDF font dictionaries into code→Unicode and
// code→width mappings. All tables are written from the facts in
// ISO 32000-1 Annex D; no third-party data files are used.
package font

import (
	"strconv"
	"strings"
)

// Encoding maps single-byte codes to runes (0 = undefined).
type Encoding [256]rune

func asciiBase() Encoding {
	var e Encoding
	for c := 0x20; c < 0x7F; c++ {
		e[c] = rune(c)
	}
	return e
}

func fill(e *Encoding, from int, runes string) {
	i := from
	for _, r := range runes {
		if r != '\x00' {
			e[i] = r
		}
		i++
	}
}

// WinAnsiEncoding (CP1252 as specified in Annex D).
var WinAnsiEncoding = func() Encoding {
	e := asciiBase()
	for c := 0xA0; c <= 0xFF; c++ {
		e[c] = rune(c)
	}
	fill(&e, 0x80, "€\x00‚ƒ„…†‡ˆ‰Š‹Œ\x00Ž\x00\x00‘’“”•–—˜™š›œ\x00žŸ")
	e[0xAD] = 0x2D // soft hyphen renders as hyphen in WinAnsi fonts
	return e
}()

// MacRomanEncoding.
var MacRomanEncoding = func() Encoding {
	e := asciiBase()
	fill(&e, 0x80, "ÄÅÇÉÑÖÜáàâäãåçéèêëíìîïñóòôöõúùûü†°¢£§•¶ß®©™´¨≠ÆØ∞±≤≥¥µ∂∑∏π∫ªºΩæø¿¡¬√ƒ≈∆«»… ÀÃÕŒœ–—“”‘’÷◊ÿŸ⁄€‹›ﬁﬂ‡·‚„‰ÂÊÁËÈÍÎÏÌÓÔÒÚÛÙıˆ˜¯˘˙˚¸˝˛ˇ")
	return e
}()

// StandardEncoding (Adobe standard Latin).
var StandardEncoding = func() Encoding {
	e := asciiBase()
	e[0x27] = '’'
	e[0x60] = '‘'
	fill(&e, 0xA1, "¡¢£⁄¥ƒ§¤'“«‹›ﬁﬂ")
	fill(&e, 0xB1, "–†‡·\x00¶•‚„”»…‰\x00¿")
	fill(&e, 0xC1, "`´ˆ˜¯˘˙¨\x00˚¸\x00˝˛ˇ—")
	e[0xE1], e[0xE3], e[0xE8], e[0xE9], e[0xEA], e[0xEB] = 'Æ', 'ª', 'Ł', 'Ø', 'Œ', 'º'
	e[0xF1], e[0xF5], e[0xF8], e[0xF9], e[0xFA], e[0xFB] = 'æ', 'ı', 'ł', 'ø', 'œ', 'ß'
	return e
}()

// SymbolEncoding (built-in encoding of the Symbol font).
var SymbolEncoding = func() Encoding {
	e := asciiBase()
	fill(&e, 0x20, " !∀#∃%&∋()∗+,−./")
	fill(&e, 0x40, "≅ΑΒΧΔΕΦΓΗΙϑΚΛΜΝΟΠΘΡΣΤΥςΩΞΨΖ[∴]⊥_")
	fill(&e, 0x60, "αβχδεφγηιϕκλμνοπθρστυϖωξψζ{|}∼")
	fill(&e, 0xA0, "€ϒ′≤⁄∞ƒ♣♦♥♠↔←↑→↓")
	fill(&e, 0xB0, "°±″≥×∝∂•÷≠≡≈…↵")
	fill(&e, 0xC0, "ℵℑℜ℘⊗⊕∅∩∪⊃⊇⊄⊂⊆∈∉")
	fill(&e, 0xD0, "∠∇®©™∏√⋅¬∧∨⇔⇐⇑⇒⇓")
	fill(&e, 0xE0, "◊〈®©™∑")
	fill(&e, 0xF1, "〉∫⌠⌡")
	return e
}()

// ZapfDingbats has no meaningful Unicode for most glyphs; map a few
// common bullets and check marks.
var ZapfDingbatsEncoding = func() Encoding {
	var e Encoding
	e[0x20] = ' '
	e[0x33], e[0x34], e[0x35], e[0x36] = '✓', '✔', '✕', '✖'
	e[0x6C], e[0x6E], e[0x6F], e[0x71], e[0x75], e[0xA8] = '●', '■', '❏', '❑', '◆', '➢'
	return e
}()

// glyphNames is a compact "name:hex" list of common glyph names.
const glyphNames = `space:20 exclam:21 quotedbl:22 numbersign:23 dollar:24 percent:25 ampersand:26 quotesingle:27
parenleft:28 parenright:29 asterisk:2A plus:2B comma:2C hyphen:2D period:2E slash:2F zero:30 one:31 two:32
three:33 four:34 five:35 six:36 seven:37 eight:38 nine:39 colon:3A semicolon:3B less:3C equal:3D greater:3E
question:3F at:40 bracketleft:5B backslash:5C bracketright:5D asciicircum:5E underscore:5F grave:60
braceleft:7B bar:7C braceright:7D asciitilde:7E nbspace:A0 nonbreakingspace:A0 exclamdown:A1 cent:A2 sterling:A3
currency:A4 yen:A5 brokenbar:A6 section:A7 dieresis:A8 copyright:A9 ordfeminine:AA guillemotleft:AB
guillemetleft:AB logicalnot:AC sfthyphen:AD softhyphen:AD registered:AE macron:AF degree:B0 plusminus:B1
twosuperior:B2 threesuperior:B3 acute:B4 mu:B5 paragraph:B6 periodcentered:B7 cedilla:B8 onesuperior:B9
ordmasculine:BA guillemotright:BB guillemetright:BB onequarter:BC onehalf:BD threequarters:BE
questiondown:BF Agrave:C0 Aacute:C1 Acircumflex:C2 Atilde:C3 Adieresis:C4 Aring:C5 AE:C6 Ccedilla:C7
Egrave:C8 Eacute:C9 Ecircumflex:CA Edieresis:CB Igrave:CC Iacute:CD Icircumflex:CE Idieresis:CF Eth:D0
Ntilde:D1 Ograve:D2 Oacute:D3 Ocircumflex:D4 Otilde:D5 Odieresis:D6 multiply:D7 Oslash:D8 Ugrave:D9
Uacute:DA Ucircumflex:DB Udieresis:DC Yacute:DD Thorn:DE germandbls:DF agrave:E0 aacute:E1 acircumflex:E2
atilde:E3 adieresis:E4 aring:E5 ae:E6 ccedilla:E7 egrave:E8 eacute:E9 ecircumflex:EA edieresis:EB
igrave:EC iacute:ED icircumflex:EE idieresis:EF eth:F0 ntilde:F1 ograve:F2 oacute:F3 ocircumflex:F4
otilde:F5 odieresis:F6 divide:F7 oslash:F8 ugrave:F9 uacute:FA ucircumflex:FB udieresis:FC yacute:FD
thorn:FE ydieresis:FF Amacron:100 amacron:101 Abreve:102 abreve:103 Aogonek:104 aogonek:105 Cacute:106
cacute:107 Ccaron:10C ccaron:10D Dcaron:10E dcaron:10F Dcroat:110 dcroat:111 Emacron:112 emacron:113
Edotaccent:116 edotaccent:117 Eogonek:118 eogonek:119 Ecaron:11A ecaron:11B Gbreve:11E gbreve:11F
Gcommaaccent:122 gcommaaccent:123 Imacron:12A imacron:12B Iogonek:12E iogonek:12F Idotaccent:130
dotlessi:131 Kcommaaccent:136 kcommaaccent:137 Lacute:139 lacute:13A Lcommaaccent:13B lcommaaccent:13C
Lcaron:13D lcaron:13E Lslash:141 lslash:142 Nacute:143 nacute:144 Ncommaaccent:145 ncommaaccent:146
Ncaron:147 ncaron:148 Omacron:14C omacron:14D Ohungarumlaut:150 ohungarumlaut:151 OE:152 oe:153
Racute:154 racute:155 Rcommaaccent:156 rcommaaccent:157 Rcaron:158 rcaron:159 Sacute:15A sacute:15B
Scedilla:15E scedilla:15F Scaron:160 scaron:161 Tcommaaccent:162 tcommaaccent:163 Tcaron:164 tcaron:165
Umacron:16A umacron:16B Uring:16E uring:16F Uhungarumlaut:170 uhungarumlaut:171 Uogonek:172 uogonek:173
Ydieresis:178 Zacute:179 zacute:17A Zdotaccent:17B zdotaccent:17C Zcaron:17D zcaron:17E florin:192
Scommaaccent:218 scommaaccent:219 Tcedilla:162 tcedilla:163 dotlessj:237 circumflex:2C6 caron:2C7
breve:2D8 dotaccent:2D9 ring:2DA ogonek:2DB tilde:2DC hungarumlaut:2DD Alpha:391 Beta:392 Gamma:393
Delta:394 Epsilon:395 Zeta:396 Eta:397 Theta:398 Iota:399 Kappa:39A Lambda:39B Mu:39C Nu:39D Xi:39E
Omicron:39F Pi:3A0 Rho:3A1 Sigma:3A3 Tau:3A4 Upsilon:3A5 Phi:3A6 Chi:3A7 Psi:3A8 Omega:3A9 alpha:3B1
beta:3B2 gamma:3B3 delta:3B4 epsilon:3B5 zeta:3B6 eta:3B7 theta:3B8 iota:3B9 kappa:3BA lambda:3BB
mugreek:3BC nu:3BD xi:3BE omicron:3BF pi:3C0 rho:3C1 sigma1:3C2 sigma:3C3 tau:3C4 upsilon:3C5 phi:3C6
chi:3C7 psi:3C8 omega:3C9 theta1:3D1 Upsilon1:3D2 phi1:3D5 omega1:3D6 afii10017:410 afii10018:411
endash:2013 emdash:2014 afii00208:2015 quoteleft:2018 quoteright:2019 quotesinglbase:201A quotereversed:201B
quotedblleft:201C quotedblright:201D quotedblbase:201E dagger:2020 daggerdbl:2021 bullet:2022
onedotenleader:2024 twodotenleader:2025 ellipsis:2026 perthousand:2030 minute:2032 second:2033
guilsinglleft:2039 guilsinglright:203A exclamdbl:203C fraction:2044 Euro:20AC trademark:2122 Ifraktur:2111
weierstrass:2118 Rfraktur:211C Omega:3A9 aleph:2135 onethird:2153 twothirds:2154 arrowleft:2190
arrowup:2191 arrowright:2192 arrowdown:2193 arrowboth:2194 arrowupdn:2195 carriagereturn:21B5
arrowdblleft:21D0 arrowdblup:21D1 arrowdblright:21D2 arrowdbldown:21D3 arrowdblboth:21D4 universal:2200
partialdiff:2202 existential:2203 emptyset:2205 increment:2206 gradient:2207 element:2208 notelement:2209
suchthat:220B product:220F summation:2211 minus:2212 asteriskmath:2217 radical:221A proportional:221D
infinity:221E orthogonal:221F angle:2220 logicaland:2227 logicalor:2228 intersection:2229 union:222A
integral:222B therefore:2234 similar:223C congruent:2245 approxequal:2248 notequal:2260 equivalence:2261
lessequal:2264 greaterequal:2265 propersubset:2282 propersuperset:2283 notsubset:2284 reflexsubset:2286
reflexsuperset:2287 circleplus:2295 circlemultiply:2297 perpendicular:22A5 dotmath:22C5 house:2302
angleleft:2329 angleright:232A filledbox:25A0 H22073:25A1 H18543:25AA H18551:25AB filledrect:25AC
triagup:25B2 triagrt:25BA triagdn:25BC triaglf:25C4 lozenge:25CA circle:25CB H18533:25CF openbullet:25E6
invbullet:25D8 invcircle:25D9 smileface:263A sun:263C female:2640 male:2642 spade:2660 club:2663
heart:2665 diamond:2666 musicalnote:266A checkmark:2713 ff:FB00 fi:FB01 fl:FB02 ffi:FB03 ffl:FB04
dotlessi.sc:131 nbsp:A0 Ohm:2126 micro:B5 periodcentered.loclCAT:B7`

var glyphMap = func() map[string]rune {
	m := make(map[string]rune, 600)
	for _, f := range strings.Fields(glyphNames) {
		i := strings.LastIndexByte(f, ':')
		v, err := strconv.ParseUint(f[i+1:], 16, 32)
		if err == nil {
			m[f[:i]] = rune(v)
		}
	}
	for c := 'A'; c <= 'Z'; c++ {
		m[string(c)] = c
		m[string(c+32)] = c + 32
	}
	return m
}()

// GlyphToUnicode maps a glyph name to text following the standard
// algorithmic rules (uniXXXX, uXXXX[XX], ligature components, suffixes).
func GlyphToUnicode(name string) string {
	if r, ok := glyphMap[name]; ok {
		return string(r)
	}
	if i := strings.IndexByte(name, '.'); i > 0 {
		name = name[:i] // drop suffix like ".sc", ".alt"
		if r, ok := glyphMap[name]; ok {
			return string(r)
		}
	}
	if strings.Contains(name, "_") {
		var sb strings.Builder
		for _, part := range strings.Split(name, "_") {
			sb.WriteString(GlyphToUnicode(part))
		}
		return sb.String()
	}
	if strings.HasPrefix(name, "uni") && len(name) >= 7 && (len(name)-3)%4 == 0 {
		var sb strings.Builder
		for i := 3; i+4 <= len(name); i += 4 {
			v, err := strconv.ParseUint(name[i:i+4], 16, 32)
			if err != nil || (v >= 0xD800 && v <= 0xDFFF) {
				return ""
			}
			sb.WriteRune(rune(v))
		}
		return sb.String()
	}
	if strings.HasPrefix(name, "u") && len(name) >= 5 && len(name) <= 7 {
		if v, err := strconv.ParseUint(name[1:], 16, 32); err == nil && v <= 0x10FFFF {
			return string(rune(v))
		}
	}
	return ""
}
