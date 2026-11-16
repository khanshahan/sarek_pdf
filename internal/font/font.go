package font

import (
	"bytes"
	"strconv"
	"strings"
	"sync"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
)

// Font is a decoded font resource. It is immutable after Load and safe for
// concurrent use.
type Font struct {
	Name       string // base font name without subset prefix
	Family     string // best-effort family name (for export)
	Bold       bool
	Italic     bool
	Monospace  bool
	Serif      bool
	Ascent     float64 // in text space units (per 1 unit of font size)
	Descent    float64 // negative
	Vertical   bool
	composite  bool
	type3      bool
	matrixXX   float64 // Type3 FontMatrix[0]; 0.001 for others
	codeMap    *CMap   // composite: code splitting & CIDs
	toUnicode  *CMap
	simpleText [256]string
	widths     [256]float64
	hasWidth   [256]bool
	missingW   float64
	cidW       map[uint32]float64
	dw         float64
	cidUni     func(cid uint32) string // from the embedded font program
	collection map[uint32]rune         // from a user-supplied CID map
	uniCache   sync.Map                // composite code -> string
}

// Glyph is one decoded character code.
type Glyph struct {
	Code  uint32
	Bytes int
	Text  string
	Width float64 // horizontal displacement in text space per unit size
	Space bool    // single-byte code 32 (word spacing applies)
}

// Loader caches fonts per document (keyed by reference).
type Loader struct {
	r  *pdf.Reader
	mu sync.Mutex
	m  map[pdf.Ref]*Font
	// CIDMaps optionally maps a CID collection ("Adobe-Japan1") to a
	// CID → Unicode table supplied by the caller at runtime.
	CIDMaps map[string]map[uint32]rune
}

// NewLoader returns a concurrency-safe font cache for r.
func NewLoader(r *pdf.Reader) *Loader { return &Loader{r: r, m: map[pdf.Ref]*Font{}} }

// Get loads (or returns a cached) font for the font object o.
func (l *Loader) Get(o pdf.Object) *Font {
	ref, isRef := o.(pdf.Ref)
	if isRef {
		l.mu.Lock()
		f := l.m[ref]
		l.mu.Unlock()
		if f != nil {
			return f
		}
	}
	f := load(l.r, l.r.Dict(o), l.CIDMaps)
	if isRef {
		l.mu.Lock()
		if old := l.m[ref]; old != nil {
			f = old
		} else {
			l.m[ref] = f
		}
		l.mu.Unlock()
	}
	return f
}

// Load builds a Font from a font dictionary. It never fails: missing
// information degrades to sensible defaults.
func Load(r *pdf.Reader, d pdf.Dict) *Font { return load(r, d, nil) }

func load(r *pdf.Reader, d pdf.Dict, cidMaps map[string]map[uint32]rune) *Font {
	f := &Font{matrixXX: 0.001, Ascent: 0.8, Descent: -0.2, missingW: 0, dw: 1000}
	if d == nil {
		f.Name = "Unknown"
		f.initSimple(r, nil, nil, "")
		return f
	}
	subtype := r.Name(d["Subtype"])
	base := string(r.Name(d["BaseFont"]))
	if i := strings.IndexByte(base, '+'); i == 6 {
		base = base[7:]
	}
	f.Name = base
	desc := r.Dict(d["FontDescriptor"])
	if subtype == "Type0" {
		f.composite = true
		if df := r.Array(d["DescendantFonts"]); len(df) > 0 {
			dd := r.Dict(df[0])
			if desc = r.Dict(dd["FontDescriptor"]); desc == nil {
				desc = nil
			}
			f.loadCIDWidths(r, dd)
		}
		switch enc := r.Resolve(d["Encoding"]).(type) {
		case pdf.Name:
			f.codeMap = predefinedCMap(string(enc))
		case *pdf.Stream:
			if data, err := r.Decode(enc); err == nil {
				f.codeMap = ParseCMap(data)
				if len(f.codeMap.spaces) == 0 {
					f.codeMap.spaces = IdentityCMap(false).spaces
				}
			}
		}
		if f.codeMap == nil {
			f.codeMap = IdentityCMap(false)
		}
		f.Vertical = f.codeMap.Vertical
		if df := r.Array(d["DescendantFonts"]); len(df) > 0 {
			dd := r.Dict(df[0])
			f.loadEmbeddedUnicode(r, dd, desc)
			if si := r.Dict(dd["CIDSystemInfo"]); si != nil && cidMaps != nil {
				key := pdf.TextString(r.Resolve(si["Registry"])) + "-" + pdf.TextString(r.Resolve(si["Ordering"]))
				f.collection = cidMaps[key]
			}
		}
	}
	if tu := r.Stream(d["ToUnicode"]); tu != nil {
		if data, err := r.Decode(tu); err == nil {
			if cm := ParseCMap(data); !cm.Empty() {
				f.toUnicode = cm
			}
		}
	}
	f.applyDescriptor(r, desc)
	f.applyNameStyle()
	if subtype == "Type3" {
		f.type3 = true
		if m := r.Numbers(d["FontMatrix"]); len(m) == 6 && m[0] != 0 {
			f.matrixXX = m[0]
		}
		f.Ascent, f.Descent = 0.8, -0.2
	}
	if !f.composite {
		f.initSimple(r, d, desc, subtype)
	}
	return f
}

func (f *Font) applyDescriptor(r *pdf.Reader, desc pdf.Dict) {
	if desc == nil {
		return
	}
	flags := r.Int(desc["Flags"], 0)
	f.Monospace = flags&1 != 0
	f.Serif = flags&2 != 0
	if flags&(1<<6) != 0 {
		f.Italic = true
	}
	if flags&(1<<18) != 0 {
		f.Bold = true
	}
	if w := r.Num(desc["FontWeight"], 0); w >= 600 {
		f.Bold = true
	}
	if a := r.Num(desc["ItalicAngle"], 0); a < -4 || a > 4 {
		f.Italic = true
	}
	if a := r.Num(desc["Ascent"], 0); a > 100 && a < 2000 {
		f.Ascent = a / 1000
	}
	if dsc := r.Num(desc["Descent"], 0); dsc < 0 && dsc > -1000 {
		f.Descent = dsc / 1000
	}
	f.missingW = r.Num(desc["MissingWidth"], 0)
	if fam := pdf.TextString(r.Resolve(desc["FontFamily"])); fam != "" {
		f.Family = fam
	}
}

func (f *Font) applyNameStyle() {
	n := strings.ToLower(f.Name)
	for _, s := range []string{"bold", "black", "heavy", "semibold", "demi", ",bd", "-bd"} {
		if strings.Contains(n, s) {
			f.Bold = true
		}
	}
	if strings.Contains(n, "italic") || strings.Contains(n, "oblique") || strings.HasSuffix(n, "-it") || strings.Contains(n, ",it") {
		f.Italic = true
	}
	if strings.Contains(n, "courier") || strings.Contains(n, "mono") || strings.Contains(n, "consol") {
		f.Monospace = true
	}
	if strings.Contains(n, "times") || strings.Contains(n, "serif") && !strings.Contains(n, "sans") {
		f.Serif = true
	}
	if f.Family == "" {
		f.Family = familyFromName(f.Name)
	}
}

// familyFromName strips style suffixes: "Arial-BoldMT" -> "Arial".
func familyFromName(name string) string {
	n := name
	if i := strings.IndexAny(n, "-,"); i > 0 {
		n = n[:i]
	}
	for _, suf := range []string{"PSMT", "MT", "PS"} {
		n = strings.TrimSuffix(n, suf)
	}
	switch strings.ToLower(n) {
	case "helvetica", "arial", "arialmt":
		return "Arial"
	case "times", "timesnewroman", "timesnewromanps", "times new roman":
		return "Times New Roman"
	case "courier", "couriernew", "couriernewps":
		return "Courier New"
	}
	// Split CamelCase family names: "TimesNewRoman" -> "Times New Roman".
	var sb strings.Builder
	for i, c := range n {
		if i > 0 && c >= 'A' && c <= 'Z' && n[i-1] >= 'a' && n[i-1] <= 'z' {
			sb.WriteByte(' ')
		}
		sb.WriteRune(c)
	}
	if sb.Len() == 0 {
		return "Arial"
	}
	return sb.String()
}

func predefinedCMap(name string) *CMap {
	switch name {
	case "Identity-H", "":
		return IdentityCMap(false)
	case "Identity-V":
		return IdentityCMap(true)
	}
	c := IdentityCMap(strings.HasSuffix(name, "-V"))
	c.identity = false
	c.Name = name
	// UCS2/UTF16 CMaps use 2-byte codes equal to Unicode; others are mostly
	// variable-width – approximate as 1 or 2 bytes using common lead bytes.
	if strings.Contains(name, "RKSJ") || strings.Contains(name, "EUC") || strings.Contains(name, "GBK") || strings.Contains(name, "B5") {
		c.spaces = []codespace{
			{n: 1, lo: [4]byte{0x00}, hi: [4]byte{0x80}},
			{n: 2, lo: [4]byte{0x81, 0x40}, hi: [4]byte{0xFE, 0xFE}},
		}
	}
	return c
}

func (f *Font) loadCIDWidths(r *pdf.Reader, dd pdf.Dict) {
	f.dw = r.Num(dd["DW"], 1000)
	w := r.Array(dd["W"])
	if len(w) == 0 {
		return
	}
	f.cidW = make(map[uint32]float64, len(w)*2)
	for i := 0; i < len(w); {
		first, ok := pdf.Int(r.Resolve(w[i]))
		if !ok || i+1 >= len(w) {
			break
		}
		switch nx := r.Resolve(w[i+1]).(type) {
		case pdf.Array:
			for k, e := range nx {
				if v, ok := pdf.Num(r.Resolve(e)); ok {
					f.cidW[uint32(first+k)] = v
				}
			}
			i += 2
		default:
			last, ok1 := pdf.Int(nx)
			if i+2 >= len(w) || !ok1 {
				return
			}
			v, _ := pdf.Num(r.Resolve(w[i+2]))
			if last-first > 65535 {
				last = first + 65535
			}
			for c := first; c <= last; c++ {
				f.cidW[uint32(c)] = v
			}
			i += 3
		}
	}
}

func (f *Font) initSimple(r *pdf.Reader, d, desc pdf.Dict, subtype pdf.Name) {
	base := WinAnsiEncoding
	symbolic := false
	if desc != nil {
		fl := r.Int(desc["Flags"], 0)
		symbolic = fl&4 != 0 && fl&32 == 0
	}
	lname := strings.ToLower(f.Name)
	switch {
	case strings.HasPrefix(lname, "symbol"):
		base = SymbolEncoding
	case strings.HasPrefix(lname, "zapfdingbats") || strings.Contains(lname, "dingbat"):
		base = ZapfDingbatsEncoding
	case subtype == "Type1" || subtype == "MMType1":
		base = StandardEncoding
		if d != nil && desc != nil {
			if enc, ok := builtinType1Encoding(r, desc); ok {
				base = enc
			}
		}
	}
	var diffs map[int]string
	if d != nil {
		switch enc := r.Resolve(d["Encoding"]).(type) {
		case pdf.Name:
			base = namedEncoding(enc, base)
		case pdf.Dict:
			if be, ok := r.Resolve(enc["BaseEncoding"]).(pdf.Name); ok {
				base = namedEncoding(be, base)
			} else if symbolic {
				// keep built-in
			}
			diffs = map[int]string{}
			code := 0
			for _, e := range r.Array(enc["Differences"]) {
				switch v := r.Resolve(e).(type) {
				case pdf.Integer:
					code = int(v)
				case pdf.Name:
					if code >= 0 && code < 256 {
						diffs[code] = string(v)
					}
					code++
				}
			}
		}
	}
	for c := 0; c < 256; c++ {
		if f.toUnicode != nil {
			if s, ok := f.toUnicode.Lookup(uint32(c), 1); ok {
				f.simpleText[c] = s
				continue
			}
		}
		if name, ok := diffs[c]; ok {
			if s := GlyphToUnicode(name); s != "" {
				f.simpleText[c] = s
				continue
			}
			// Unknown names like "g31", "C0032", "cid12" — try trailing number.
			if s := numericGlyph(name); s != "" {
				f.simpleText[c] = s
				continue
			}
		}
		if base[c] != 0 {
			f.simpleText[c] = string(base[c])
		} else if symbolic && c >= 0x20 && c < 0x7F {
			f.simpleText[c] = string(rune(c))
		}
	}
	// Widths.
	if d != nil {
		first := r.Int(d["FirstChar"], 0)
		for i, e := range r.Array(d["Widths"]) {
			c := first + i
			if c < 0 || c > 255 {
				continue
			}
			if v, ok := pdf.Num(r.Resolve(e)); ok {
				f.widths[c], f.hasWidth[c] = v, true
			}
		}
	}
	std := standardWidths(f.Name)
	for c := 0; c < 256; c++ {
		if f.hasWidth[c] {
			continue
		}
		if std != nil {
			if t := f.simpleText[c]; t != "" {
				rr := []rune(t)[0]
				if rr < 128 && std[rr] > 0 {
					f.widths[c] = std[rr]
					continue
				}
			}
			f.widths[c] = std[0]
			continue
		}
		if f.missingW > 0 {
			f.widths[c] = f.missingW
		} else {
			f.widths[c] = 500
		}
	}
}

func numericGlyph(name string) string {
	// Only decode the well-known "uniXXXX"-like patterns already handled;
	// for others (g12/cid12) we cannot know the Unicode. Leave empty so
	// callers can fall back to the base encoding.
	_ = name
	return ""
}

func namedEncoding(n pdf.Name, def Encoding) Encoding {
	switch n {
	case "WinAnsiEncoding":
		return WinAnsiEncoding
	case "MacRomanEncoding", "MacExpertEncoding":
		return MacRomanEncoding
	case "StandardEncoding":
		return StandardEncoding
	}
	return def
}

// builtinType1Encoding reads "dup <code> /<name> put" entries from the
// cleartext portion of an embedded Type 1 font program.
func builtinType1Encoding(r *pdf.Reader, desc pdf.Dict) (Encoding, bool) {
	s := r.Stream(desc["FontFile"])
	if s == nil {
		return Encoding{}, false
	}
	data, err := r.Decode(s)
	if err != nil {
		return Encoding{}, false
	}
	if l1 := r.Int(s.Dict["Length1"], 0); l1 > 0 && l1 < len(data) {
		data = data[:l1]
	}
	i := bytes.Index(data, []byte("/Encoding"))
	if i < 0 || bytes.Contains(data[i:min(i+40, len(data))], []byte("StandardEncoding")) {
		return Encoding{}, false
	}
	var e Encoding
	found := false
	rest := data[i:]
	if j := bytes.Index(rest, []byte("readonly def")); j > 0 {
		rest = rest[:j]
	}
	fields := bytes.Fields(rest)
	for k := 0; k+3 < len(fields); k++ {
		if string(fields[k]) != "dup" || string(fields[k+3]) != "put" || len(fields[k+2]) < 2 || fields[k+2][0] != '/' {
			continue
		}
		code, err := strconv.Atoi(string(fields[k+1]))
		if err != nil || code < 0 || code > 255 {
			continue
		}
		if t := GlyphToUnicode(string(fields[k+2][1:])); t != "" {
			e[code] = []rune(t)[0]
			found = true
		}
	}
	return e, found
}

// Composite reports whether the font is a Type0 font.
func (f *Font) Composite() bool { return f.composite }

// Decode splits s into glyphs and calls fn for each.
func (f *Font) Decode(s []byte, fn func(g Glyph)) {
	if !f.composite {
		for _, c := range s {
			fn(Glyph{
				Code: uint32(c), Bytes: 1, Text: f.simpleText[c],
				Width: f.widths[c] * f.matrixXX, Space: c == 32,
			})
		}
		return
	}
	for len(s) > 0 {
		code, n := f.codeMap.NextCode(s)
		cid := f.codeMap.CID(code, n)
		w := f.dw
		if v, ok := f.cidW[cid]; ok {
			w = v
		}
		fn(Glyph{Code: code, Bytes: n, Text: f.compositeText(code, n, cid), Width: w * 0.001, Space: n == 1 && code == 32})
		s = s[n:]
	}
}

func (f *Font) compositeText(code uint32, n int, cid uint32) string {
	k := key(code, n)
	if v, ok := f.uniCache.Load(k); ok {
		return v.(string)
	}
	var t string
	if f.toUnicode != nil {
		t, _ = f.toUnicode.Lookup(code, n)
	}
	if t == "" && f.cidUni != nil {
		t = f.cidUni(cid)
	}
	if t == "" && f.collection != nil {
		if r, ok := f.collection[cid]; ok {
			t = string(r)
		}
	}
	if t == "" && !f.codeMap.identity && (strings.Contains(f.codeMap.Name, "UCS2") || strings.Contains(f.codeMap.Name, "UTF16")) {
		t = string(rune(code))
	}
	f.uniCache.Store(k, t)
	return t
}

// HasUnicode reports whether the font has any reliable text mapping.
func (f *Font) HasUnicode() bool {
	return f.toUnicode != nil || !f.composite
}
