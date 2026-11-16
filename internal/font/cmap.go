package font

import (
	"sort"
	"unicode/utf16"

	"github.com/khanshahan/sarek_pdf/internal/pdf"
)

type codespace struct {
	n      int
	lo, hi [4]byte
}

type rangeMap struct {
	lo, hi uint32
	n      int // code length in bytes
	dst    []uint16
	arr    []string // explicit array form
	cid    uint32   // for cid ranges
}

// CMap holds both code-splitting information (codespace ranges) and the
// code→Unicode (ToUnicode) or code→CID mappings.
type CMap struct {
	spaces   []codespace
	single   map[uint32]string // bfchar (key packs code length in high byte)
	ranges   []rangeMap
	cidOne   map[uint32]uint32
	cidRng   []rangeMap
	Name     string
	Vertical bool
	identity bool
}

// IdentityCMap is the predefined Identity-H/-V CMap (2-byte codes, CID=code).
func IdentityCMap(vertical bool) *CMap {
	c := &CMap{identity: true, Vertical: vertical, Name: "Identity-H"}
	c.spaces = []codespace{{n: 2, lo: [4]byte{0, 0}, hi: [4]byte{0xFF, 0xFF}}}
	return c
}

func key(code uint32, n int) uint32 { return code | uint32(n)<<28 }

// ParseCMap parses an embedded CMap / ToUnicode stream.
func ParseCMap(data []byte) *CMap {
	c := &CMap{single: map[uint32]string{}, cidOne: map[uint32]uint32{}}
	p := pdf.NewParser(data, 0)
	var stack []pdf.Object
	for !p.EOF() {
		o, err := p.ParseObject()
		if err != nil {
			break
		}
		kw, ok := o.(pdf.Keyword)
		if !ok {
			stack = append(stack, o)
			if len(stack) > 64 {
				stack = stack[len(stack)-8:]
			}
			continue
		}
		switch kw {
		case "begincodespacerange":
			c.readCodespace(p)
		case "beginbfchar":
			c.readBfChar(p)
		case "beginbfrange":
			c.readBfRange(p)
		case "begincidchar":
			c.readCidChar(p)
		case "begincidrange":
			c.readCidRange(p)
		case "def":
			if len(stack) >= 2 {
				if n, ok := stack[len(stack)-2].(pdf.Name); ok {
					switch n {
					case "CMapName":
						if v, ok := stack[len(stack)-1].(pdf.Name); ok {
							c.Name = string(v)
						}
					case "WMode":
						if v, ok := pdf.Int(stack[len(stack)-1]); ok && v == 1 {
							c.Vertical = true
						}
					}
				}
			}
		}
		stack = stack[:0]
	}
	sort.Slice(c.ranges, func(i, j int) bool { return c.ranges[i].lo < c.ranges[j].lo })
	return c
}

func codeOf(s pdf.String) (uint32, int) {
	var v uint32
	n := len(s)
	if n > 4 {
		n = 4
	}
	for i := 0; i < n; i++ {
		v = v<<8 | uint32(s[i])
	}
	return v, n
}

func (c *CMap) readCodespace(p *pdf.Parser) {
	for {
		lo, err1 := p.ParseObject()
		if k, ok := lo.(pdf.Keyword); ok || err1 != nil {
			_ = k
			return
		}
		hi, _ := p.ParseObject()
		ls, ok1 := lo.(pdf.String)
		hs, ok2 := hi.(pdf.String)
		if !ok1 || !ok2 || len(ls) == 0 || len(ls) > 4 || len(hs) != len(ls) {
			continue
		}
		var cs codespace
		cs.n = len(ls)
		copy(cs.lo[:], ls)
		copy(cs.hi[:], hs)
		c.spaces = append(c.spaces, cs)
	}
}

func utf16BE(s pdf.String) string {
	if len(s) == 1 {
		return string(rune(s[0]))
	}
	u := make([]uint16, 0, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		u = append(u, uint16(s[i])<<8|uint16(s[i+1]))
	}
	return string(utf16.Decode(u))
}

func dstString(o pdf.Object) string {
	switch v := o.(type) {
	case pdf.String:
		return utf16BE(v)
	case pdf.Name:
		return GlyphToUnicode(string(v))
	}
	return ""
}

func (c *CMap) readBfChar(p *pdf.Parser) {
	for {
		src, err := p.ParseObject()
		if _, ok := src.(pdf.Keyword); ok || err != nil {
			return
		}
		dst, _ := p.ParseObject()
		s, ok := src.(pdf.String)
		if !ok || len(s) == 0 {
			continue
		}
		code, n := codeOf(s)
		c.single[key(code, n)] = dstString(dst)
	}
}

func (c *CMap) readBfRange(p *pdf.Parser) {
	for {
		lo, err := p.ParseObject()
		if _, ok := lo.(pdf.Keyword); ok || err != nil {
			return
		}
		hi, _ := p.ParseObject()
		dst, _ := p.ParseObject()
		ls, ok1 := lo.(pdf.String)
		hs, ok2 := hi.(pdf.String)
		if !ok1 || !ok2 || len(ls) == 0 {
			continue
		}
		l, n := codeOf(ls)
		h, _ := codeOf(hs)
		if h < l || h-l > 0xFFFF {
			continue
		}
		rm := rangeMap{lo: l, hi: h, n: n}
		switch d := dst.(type) {
		case pdf.String:
			u := make([]uint16, 0, len(d)/2+1)
			if len(d) == 1 {
				u = append(u, uint16(d[0]))
			}
			for i := 0; i+1 < len(d); i += 2 {
				u = append(u, uint16(d[i])<<8|uint16(d[i+1]))
			}
			rm.dst = u
		case pdf.Array:
			for _, e := range d {
				rm.arr = append(rm.arr, dstString(e))
			}
		default:
			continue
		}
		c.ranges = append(c.ranges, rm)
	}
}

func (c *CMap) readCidChar(p *pdf.Parser) {
	for {
		src, err := p.ParseObject()
		if _, ok := src.(pdf.Keyword); ok || err != nil {
			return
		}
		dst, _ := p.ParseObject()
		s, ok := src.(pdf.String)
		cid, ok2 := pdf.Int(dst)
		if ok && ok2 {
			code, n := codeOf(s)
			c.cidOne[key(code, n)] = uint32(cid)
		}
	}
}

func (c *CMap) readCidRange(p *pdf.Parser) {
	for {
		lo, err := p.ParseObject()
		if _, ok := lo.(pdf.Keyword); ok || err != nil {
			return
		}
		hi, _ := p.ParseObject()
		dst, _ := p.ParseObject()
		ls, ok1 := lo.(pdf.String)
		hs, ok2 := hi.(pdf.String)
		cid, ok3 := pdf.Int(dst)
		if ok1 && ok2 && ok3 {
			l, n := codeOf(ls)
			h, _ := codeOf(hs)
			c.cidRng = append(c.cidRng, rangeMap{lo: l, hi: h, n: n, cid: uint32(cid)})
		}
	}
}

// NextCode splits the next character code from s, returning the code and
// its length in bytes (≥1).
func (c *CMap) NextCode(s []byte) (uint32, int) {
	if len(c.spaces) == 0 {
		if len(s) >= 2 {
			return uint32(s[0])<<8 | uint32(s[1]), 2
		}
		return uint32(s[0]), 1
	}
	for n := 1; n <= 4 && n <= len(s); n++ {
		for i := range c.spaces {
			cs := &c.spaces[i]
			if cs.n != n {
				continue
			}
			match := true
			for k := 0; k < n; k++ {
				if s[k] < cs.lo[k] || s[k] > cs.hi[k] {
					match = false
					break
				}
			}
			if match {
				var v uint32
				for k := 0; k < n; k++ {
					v = v<<8 | uint32(s[k])
				}
				return v, n
			}
		}
	}
	// No match: consume the shortest codespace length (§9.7.6.3).
	n := 4
	for _, cs := range c.spaces {
		n = min(n, cs.n)
	}
	n = min(n, len(s))
	var v uint32
	for k := 0; k < n; k++ {
		v = v<<8 | uint32(s[k])
	}
	return v, n
}

// CID maps a code to a CID.
func (c *CMap) CID(code uint32, n int) uint32 {
	if c.identity {
		return code
	}
	if v, ok := c.cidOne[key(code, n)]; ok {
		return v
	}
	for _, r := range c.cidRng {
		if r.n == n && code >= r.lo && code <= r.hi {
			return r.cid + (code - r.lo)
		}
	}
	return code
}

// Lookup maps a code to Unicode text via bfchar/bfrange.
func (c *CMap) Lookup(code uint32, n int) (string, bool) {
	if s, ok := c.single[key(code, n)]; ok {
		return s, true
	}
	// Some producers write codes with a different byte length than used.
	for _, r := range c.ranges {
		if code < r.lo || code > r.hi || (r.n != n && n > 0 && r.n > 0 && r.lo > 0xFF) {
			continue
		}
		off := code - r.lo
		if r.arr != nil {
			if int(off) < len(r.arr) {
				return r.arr[off], true
			}
			continue
		}
		u := append([]uint16(nil), r.dst...)
		if len(u) == 0 {
			continue
		}
		u[len(u)-1] += uint16(off)
		return string(utf16.Decode(u)), true
	}
	if n != 1 {
		return "", false
	}
	// Try 2-byte keyed entry for 1-byte codes (common writer bug).
	if s, ok := c.single[key(code, 2)]; ok {
		return s, true
	}
	return "", false
}

// Empty reports whether the CMap contains no Unicode mappings.
func (c *CMap) Empty() bool { return len(c.single) == 0 && len(c.ranges) == 0 }

// ParseCIDToUnicode reads a Unicode→CID CMap (for example the
// "Adobe-Japan1-UCS2" resource) and returns the inverse CID→Unicode map.
// Such resources are not shipped with this library; callers may supply
// them at runtime (see sarekpdf.WithCIDToUnicode).
func ParseCIDToUnicode(data []byte) map[uint32]rune {
	c := ParseCMap(data)
	out := make(map[uint32]rune, len(c.cidOne)+len(c.cidRng)*16)
	decode := func(code uint32, n int) rune {
		if n == 4 { // UTF-16 surrogate pair
			hi, lo := rune(code>>16), rune(code&0xFFFF)
			if hi >= 0xD800 && hi < 0xDC00 && lo >= 0xDC00 && lo < 0xE000 {
				return (hi-0xD800)<<10 + (lo - 0xDC00) + 0x10000
			}
		}
		return rune(code)
	}
	set := func(cid uint32, r rune) {
		if old, ok := out[cid]; !ok || r < old {
			out[cid] = r
		}
	}
	for k, cid := range c.cidOne {
		set(cid, decode(k&0x0FFFFFFF, int(k>>28)))
	}
	for _, rg := range c.cidRng {
		for code := rg.lo; code <= rg.hi && code-rg.lo < 0x10000; code++ {
			set(rg.cid+(code-rg.lo), decode(code, rg.n))
		}
	}
	return out
}
