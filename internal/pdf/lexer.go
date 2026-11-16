package pdf

import (
	"errors"
	"fmt"
	"strconv"
)

// Character classes per ISO 32000-1 §7.2.2.
var (
	isWhite [256]bool
	isDelim [256]bool
)

func init() {
	for _, c := range []byte{0, '\t', '\n', '\f', '\r', ' '} {
		isWhite[c] = true
	}
	for _, c := range []byte("()<>[]{}/%") {
		isDelim[c] = true
	}
}

// IsWhite reports whether c is PDF whitespace.
func IsWhite(c byte) bool { return isWhite[c] }

// IsRegular reports whether c is a regular (non-white, non-delimiter) char.
func IsRegular(c byte) bool { return !isWhite[c] && !isDelim[c] }

// errEOF is returned when the input ends where an object was expected.
var errEOF = errors.New("pdf: unexpected end of data")

// maxDepth bounds nesting of arrays/dicts to defend against hostile input.
const maxDepth = 256

// Parser is a zero-copy recursive-descent parser over a byte slice.
// It is not safe for concurrent use; create one per goroutine (cheap).
type Parser struct {
	B   []byte
	Pos int
	// Decrypt, when non-nil, transforms literal and hex strings.
	Decrypt func(String) String
	depth   int
}

// NewParser returns a parser positioned at off.
func NewParser(b []byte, off int) *Parser { return &Parser{B: b, Pos: off} }

// SkipWS skips whitespace and comments.
func (p *Parser) SkipWS() {
	b := p.B
	for p.Pos < len(b) {
		c := b[p.Pos]
		if isWhite[c] {
			p.Pos++
			continue
		}
		if c == '%' {
			for p.Pos < len(b) && b[p.Pos] != '\n' && b[p.Pos] != '\r' {
				p.Pos++
			}
			continue
		}
		return
	}
}

// EOF reports whether only whitespace remains.
func (p *Parser) EOF() bool { p.SkipWS(); return p.Pos >= len(p.B) }

// ReadToken returns the next regular-character token without interpretation.
func (p *Parser) ReadToken() []byte {
	p.SkipWS()
	start := p.Pos
	for p.Pos < len(p.B) && IsRegular(p.B[p.Pos]) {
		p.Pos++
	}
	return p.B[start:p.Pos]
}

// ParseObject parses one object. References "n g R" are recognised.
// Bare keywords are returned as Keyword.
func (p *Parser) ParseObject() (Object, error) {
	p.SkipWS()
	if p.Pos >= len(p.B) {
		return nil, errEOF
	}
	b := p.B
	c := b[p.Pos]
	switch c {
	case '/':
		return p.parseName(), nil
	case '(':
		return p.parseLiteral(), nil
	case '<':
		if p.Pos+1 < len(b) && b[p.Pos+1] == '<' {
			return p.parseDict()
		}
		return p.parseHex(), nil
	case '[':
		return p.parseArray()
	case ']', '>', ')', '}', '{':
		p.Pos++
		return Keyword(string(c)), nil
	}
	if c == '+' || c == '-' || c == '.' || (c >= '0' && c <= '9') {
		return p.parseNumberOrRef(), nil
	}
	start := p.Pos
	for p.Pos < len(b) && IsRegular(b[p.Pos]) {
		p.Pos++
	}
	if p.Pos == start { // stray delimiter
		p.Pos++
		return Keyword(string(b[start:p.Pos])), nil
	}
	tok := b[start:p.Pos]
	switch string(tok) {
	case "true":
		return Boolean(true), nil
	case "false":
		return Boolean(false), nil
	case "null":
		return nil, nil
	}
	if k, ok := internedKeywords[string(tok)]; ok {
		return k, nil
	}
	return Keyword(tok), nil
}

// internedKeywords avoids allocating for common operators.
var internedKeywords = func() map[string]Keyword {
	m := map[string]Keyword{}
	for _, k := range []string{"q", "Q", "cm", "w", "J", "j", "M", "d", "ri", "i", "gs",
		"m", "l", "c", "v", "y", "h", "re", "S", "s", "f", "F", "f*", "B", "B*", "b", "b*", "n", "W", "W*",
		"BT", "ET", "Tc", "Tw", "Tz", "TL", "Tf", "Tr", "Ts", "Td", "TD", "Tm", "T*", "Tj", "TJ", "'", "\"",
		"d0", "d1", "CS", "cs", "SC", "SCN", "sc", "scn", "G", "g", "RG", "rg", "K", "k", "sh", "Do",
		"BI", "ID", "EI", "MP", "DP", "BMC", "BDC", "EMC", "BX", "EX", "obj", "endobj", "stream", "endstream", "R"} {
		m[k] = Keyword(k)
	}
	return m
}()

func (p *Parser) parseName() Name {
	p.Pos++ // '/'
	b := p.B
	start := p.Pos
	hasEsc := false
	for p.Pos < len(b) && IsRegular(b[p.Pos]) {
		if b[p.Pos] == '#' {
			hasEsc = true
		}
		p.Pos++
	}
	raw := b[start:p.Pos]
	if !hasEsc {
		return Name(raw)
	}
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] == '#' && i+2 < len(raw) {
			if v, ok := unhex2(raw[i+1], raw[i+2]); ok {
				out = append(out, v)
				i += 2
				continue
			}
		}
		out = append(out, raw[i])
	}
	return Name(out)
}

func unhexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}

func unhex2(a, b byte) (byte, bool) {
	x, ok1 := unhexDigit(a)
	y, ok2 := unhexDigit(b)
	return x<<4 | y, ok1 && ok2
}

func (p *Parser) str(s []byte) String {
	if p.Decrypt != nil {
		return p.Decrypt(String(s))
	}
	return String(s)
}

func (p *Parser) parseLiteral() String {
	b := p.B
	p.Pos++ // '('
	// Fast path: no escapes, no CR, balanced.
	depth := 1
	i := p.Pos
	simple := true
	for i < len(b) {
		c := b[i]
		if c == '\\' || c == '\r' {
			simple = false
			break
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				break
			}
		}
		i++
	}
	if simple {
		s := b[p.Pos:min(i, len(b))]
		p.Pos = min(i+1, len(b))
		return p.str(s)
	}
	out := make([]byte, 0, 32)
	depth = 1
	for p.Pos < len(b) {
		c := b[p.Pos]
		p.Pos++
		switch c {
		case '(':
			depth++
			out = append(out, c)
		case ')':
			depth--
			if depth == 0 {
				return p.str(out)
			}
			out = append(out, c)
		case '\r':
			out = append(out, '\n')
			if p.Pos < len(b) && b[p.Pos] == '\n' {
				p.Pos++
			}
		case '\\':
			if p.Pos >= len(b) {
				return p.str(out)
			}
			e := b[p.Pos]
			p.Pos++
			switch e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case '\r':
				if p.Pos < len(b) && b[p.Pos] == '\n' {
					p.Pos++
				}
			case '\n':
			case '0', '1', '2', '3', '4', '5', '6', '7':
				v := int(e - '0')
				for k := 0; k < 2 && p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '7'; k++ {
					v = v*8 + int(b[p.Pos]-'0')
					p.Pos++
				}
				out = append(out, byte(v))
			default:
				out = append(out, e)
			}
		default:
			out = append(out, c)
		}
	}
	return p.str(out)
}

func (p *Parser) parseHex() String {
	b := p.B
	p.Pos++ // '<'
	out := make([]byte, 0, 16)
	var hi byte
	half := false
	for p.Pos < len(b) {
		c := b[p.Pos]
		p.Pos++
		if c == '>' {
			break
		}
		v, ok := unhexDigit(c)
		if !ok {
			continue
		}
		if half {
			out = append(out, hi<<4|v)
			half = false
		} else {
			hi, half = v, true
		}
	}
	if half {
		out = append(out, hi<<4)
	}
	return p.str(out)
}

func (p *Parser) parseArray() (Object, error) {
	p.Pos++
	if p.depth++; p.depth > maxDepth {
		return nil, errors.New("pdf: nesting too deep")
	}
	defer func() { p.depth-- }()
	arr := make(Array, 0, 8)
	for {
		p.SkipWS()
		if p.Pos >= len(p.B) {
			return arr, nil // tolerate truncation
		}
		if p.B[p.Pos] == ']' {
			p.Pos++
			return arr, nil
		}
		o, err := p.ParseObject()
		if err != nil {
			return arr, err
		}
		if k, ok := o.(Keyword); ok && (k == ">" || k == ")" || k == "endobj") {
			return arr, nil
		}
		arr = append(arr, o)
	}
}

func (p *Parser) parseDict() (Object, error) {
	p.Pos += 2
	if p.depth++; p.depth > maxDepth {
		return nil, errors.New("pdf: nesting too deep")
	}
	defer func() { p.depth-- }()
	d := make(Dict, 8)
	for {
		p.SkipWS()
		if p.Pos >= len(p.B) {
			return d, nil
		}
		if p.B[p.Pos] == '>' {
			p.Pos++
			if p.Pos < len(p.B) && p.B[p.Pos] == '>' {
				p.Pos++
			}
			return d, nil
		}
		if p.B[p.Pos] != '/' {
			// Malformed: skip a token and keep going.
			o, err := p.ParseObject()
			if err != nil {
				return d, err
			}
			if k, ok := o.(Keyword); ok && (k == "endobj" || k == "stream") {
				p.Pos -= len(k)
				return d, nil
			}
			continue
		}
		key := p.parseName()
		p.SkipWS()
		if p.Pos < len(p.B) && p.B[p.Pos] == '>' {
			d[key] = nil
			continue
		}
		v, err := p.ParseObject()
		if err != nil {
			return d, err
		}
		if k, ok := v.(Keyword); ok && (k == "endobj" || k == "stream") {
			p.Pos -= len(k)
			return d, nil
		}
		if v != nil {
			d[key] = v
		}
	}
}

// parseNumberOrRef parses a number; for a non-negative integer it looks
// ahead for "gen R".
func (p *Parser) parseNumberOrRef() Object {
	b := p.B
	start := p.Pos
	n, isInt := p.scanNumber()
	if !isInt || b[start] == '-' || b[start] == '+' {
		return n
	}
	save := p.Pos
	p.SkipWS()
	gstart := p.Pos
	for p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '9' {
		p.Pos++
	}
	if p.Pos > gstart && p.Pos < len(b) && !IsRegular(b[p.Pos]) {
		gen, _ := strconv.Atoi(string(b[gstart:p.Pos]))
		p.SkipWS()
		if p.Pos < len(b) && b[p.Pos] == 'R' && (p.Pos+1 >= len(b) || !IsRegular(b[p.Pos+1])) {
			p.Pos++
			return Ref{Num: uint32(n.(Integer)), Gen: uint16(gen)}
		}
	}
	p.Pos = save
	return n
}

// scanNumber parses a number tolerantly ("--5", "4.", ".5", "1e3" -> 1).
func (p *Parser) scanNumber() (Object, bool) {
	b := p.B
	neg := false
	for p.Pos < len(b) && (b[p.Pos] == '-' || b[p.Pos] == '+') {
		if b[p.Pos] == '-' {
			neg = !neg
		}
		p.Pos++
	}
	var ip int64
	digits := 0
	for p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '9' {
		if ip < 1<<58 {
			ip = ip*10 + int64(b[p.Pos]-'0')
		}
		p.Pos++
		digits++
	}
	if p.Pos < len(b) && b[p.Pos] == '.' {
		p.Pos++
		f := float64(ip)
		scale := 0.1
		for p.Pos < len(b) && b[p.Pos] >= '0' && b[p.Pos] <= '9' {
			f += float64(b[p.Pos]-'0') * scale
			scale /= 10
			p.Pos++
		}
		// Skip any trailing garbage like a second '.'.
		for p.Pos < len(b) && IsRegular(b[p.Pos]) {
			p.Pos++
		}
		if neg {
			f = -f
		}
		return Real(f), false
	}
	for p.Pos < len(b) && IsRegular(b[p.Pos]) {
		p.Pos++
	}
	if neg {
		ip = -ip
	}
	_ = digits
	return Integer(ip), true
}

// ExpectKeyword consumes kw or returns an error.
func (p *Parser) ExpectKeyword(kw string) error {
	p.SkipWS()
	if p.Pos+len(kw) <= len(p.B) && string(p.B[p.Pos:p.Pos+len(kw)]) == kw {
		p.Pos += len(kw)
		return nil
	}
	return fmt.Errorf("pdf: expected %q at offset %d", kw, p.Pos)
}
