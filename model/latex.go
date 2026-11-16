package model

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// ParseLaTeX parses LaTeX math (as produced by formula recognisers) into a
// MathNode tree, so it can be rendered as MathML, Word equations or
// linear text. Unknown commands are kept as upright identifiers; the
// parser never fails, it degrades to flatter structure.
func ParseLaTeX(s string) *MathNode {
	p := &latexParser{s: s}
	out := &MathNode{Kind: MathRow}
	for !p.eof() {
		r := p.row(0)
		out.Children = append(out.Children, r.Children...)
		if !p.eof() {
			p.pos++ // stray '}', '&' or '\\' at top level
		}
	}
	return out
}

type latexParser struct {
	s     string
	pos   int
	depth int
}

var commandSymbols = func() map[string]string {
	m := map[string]string{}
	for r, cmd := range latexSymbols {
		if strings.HasPrefix(cmd, `\`) && !strings.ContainsAny(cmd, "{}^") {
			name := cmd[1:]
			if old, ok := m[name]; !ok || string(r) < old {
				m[name] = string(r)
			}
		}
	}
	extra := map[string]string{
		"le": "≤", "ge": "≥", "ne": "≠", "neq": "≠", "to": "→", "gets": "←", "lbrace": "{", "rbrace": "}",
		"vert": "|", "mid": "|", "Vert": "‖", "lvert": "|", "rvert": "|", "lVert": "‖", "rVert": "‖",
		"epsilon": "ϵ", "varepsilon": "ε", "phi": "ϕ", "varphi": "φ", "ast": "∗", "star": "⋆", "dots": "…",
		"ldots": "…", "cdots": "⋯", "vdots": "⋮", "ddots": "⋱", "prime": "′", "infty": "∞", "colon": ":",
		"lt": "<", "gt": ">", "leqslant": "≤", "geqslant": "≥", "iff": "⇔", "implies": "⇒", "land": "∧",
		"lor": "∨", "lnot": "¬", "langle": "⟨", "rangle": "⟩", "backslash": `\`, "smallint": "∫",
		"bigcup": "⋃", "bigcap": "⋂", "bigoplus": "⨁", "bigotimes": "⨂", "imath": "ı", "jmath": "ȷ",
		"surd": "√", "neg": "¬", "circ": "∘", "degree": "°", "pm": "±", "mp": "∓",
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}()

// Operators that take limits above/below in display style.
var limitOps = map[string]bool{"sum": true, "prod": true, "coprod": true, "bigcup": true, "bigcap": true,
	"bigvee": true, "bigwedge": true, "bigoplus": true, "bigotimes": true, "lim": true, "limsup": true,
	"liminf": true, "max": true, "min": true, "sup": true, "inf": true, "argmax": true, "argmin": true}

func (p *latexParser) eof() bool { return p.pos >= len(p.s) }

func (p *latexParser) peek() byte {
	if p.eof() {
		return 0
	}
	return p.s[p.pos]
}

func (p *latexParser) skipSpace() {
	for !p.eof() && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t' || p.s[p.pos] == '\n') {
		p.pos++
	}
}

// row parses until '}' / end / a stop word (\right, \end, &, \\).
func (p *latexParser) row(depth int) *MathNode {
	row := &MathNode{Kind: MathRow}
	if depth > 40 {
		p.pos = len(p.s)
		return row
	}
	for {
		p.skipSpace()
		if p.eof() || p.peek() == '}' || p.peek() == '&' {
			return row
		}
		if strings.HasPrefix(p.s[p.pos:], `\end`) || strings.HasPrefix(p.s[p.pos:], `\\`) {
			return row
		}
		start := p.pos
		atom, limits := p.atom(depth)
		if p.pos == start { // safety: always make progress
			p.pos++
			continue
		}
		if atom == nil {
			continue
		}
		atom = p.scripts(atom, limits, depth)
		row.Children = append(row.Children, atom)
	}
}

// scripts attaches following ^ / _ (and primes) to base.
func (p *latexParser) scripts(base *MathNode, limits bool, depth int) *MathNode {
	var sub, sup *MathNode
	for {
		p.skipSpace()
		switch p.peek() {
		case '^':
			p.pos++
			sup = p.arg(depth)
			continue
		case '_':
			p.pos++
			sub = p.arg(depth)
			continue
		case '\'':
			p.pos++
			sup = &MathNode{Kind: MathOperator, Text: "′"}
			continue
		}
		break
	}
	switch {
	case sub == nil && sup == nil:
		return base
	case limits:
		if sub == nil {
			sub = &MathNode{Kind: MathRow}
		}
		if sup == nil {
			sup = &MathNode{Kind: MathRow}
		}
		return &MathNode{Kind: MathUnderOver, Children: []*MathNode{base, sub, sup}}
	case sub != nil && sup != nil:
		return &MathNode{Kind: MathSubSup, Children: []*MathNode{base, sub, sup}}
	case sup != nil:
		return &MathNode{Kind: MathSup, Children: []*MathNode{base, sup}}
	default:
		return &MathNode{Kind: MathSub, Children: []*MathNode{base, sub}}
	}
}

// arg parses one argument: a {group} or a single token.
func (p *latexParser) arg(depth int) *MathNode {
	p.skipSpace()
	if p.eof() {
		return &MathNode{Kind: MathRow}
	}
	if p.peek() == '{' {
		p.pos++
		r := p.row(depth + 1)
		if p.peek() == '}' {
			p.pos++
		}
		return r
	}
	a, _ := p.atom(depth + 1)
	if a == nil {
		return &MathNode{Kind: MathRow}
	}
	return a
}

// rawArg returns the text of a {group} without parsing it.
func (p *latexParser) rawArg() string {
	p.skipSpace()
	if p.peek() != '{' {
		return ""
	}
	level, start := 0, p.pos
	for ; !p.eof(); p.pos++ {
		switch p.s[p.pos] {
		case '{':
			level++
		case '}':
			level--
			if level == 0 {
				p.pos++
				return p.s[start+1 : p.pos-1]
			}
		}
	}
	return p.s[start+1:]
}

func (p *latexParser) command() string {
	p.pos++ // backslash
	start := p.pos
	for !p.eof() && (p.s[p.pos] >= 'a' && p.s[p.pos] <= 'z' || p.s[p.pos] >= 'A' && p.s[p.pos] <= 'Z') {
		p.pos++
	}
	if p.pos == start && !p.eof() {
		p.pos++ // control symbol: \, \{ \| …
	}
	name := p.s[start:p.pos]
	if name == "operatorname" && p.peek() == '*' {
		p.pos++ // \operatorname* (limits form)
	}
	return name
}

// atom parses one element; limits reports operators taking limits.
func (p *latexParser) atom(depth int) (*MathNode, bool) {
	c := p.peek()
	switch {
	case c == '{':
		return p.arg(depth), false
	case c == '\\':
		return p.commandAtom(depth)
	case c >= '0' && c <= '9' || c == '.' && p.pos+1 < len(p.s) && p.s[p.pos+1] >= '0' && p.s[p.pos+1] <= '9':
		start := p.pos
		for !p.eof() && (p.s[p.pos] >= '0' && p.s[p.pos] <= '9' || p.s[p.pos] == '.') {
			p.pos++
		}
		return &MathNode{Kind: MathNumber, Text: p.s[start:p.pos]}, false
	}
	r, size := utf8.DecodeRuneInString(p.s[p.pos:])
	p.pos += size
	switch {
	case unicode.IsLetter(r):
		return &MathNode{Kind: MathIdent, Text: string(r)}, false
	case r == '~':
		return nil, false // non-breaking space
	}
	return &MathNode{Kind: MathOperator, Text: string(r)}, false
}

func (p *latexParser) commandAtom(depth int) (*MathNode, bool) {
	name := p.command()
	switch name {
	case ",", ";", ":", "!", " ", "quad", "qquad", "enspace", "thinspace", "displaystyle", "textstyle",
		"scriptstyle", "limits", "nolimits", "big", "Big", "bigg", "Bigg", "bigl", "bigr", "Bigl", "Bigr",
		"biggl", "biggr", "Biggl", "Biggr", "mathstrut", "strut", "nonumber", "notag":
		return nil, false
	case "frac", "dfrac", "tfrac", "cfrac":
		num, den := p.arg(depth), p.arg(depth)
		return &MathNode{Kind: MathFrac, Children: []*MathNode{num, den}}, false
	case "binom", "tbinom", "dbinom":
		a, b := p.arg(depth), p.arg(depth)
		return &MathNode{Kind: MathRow, Children: []*MathNode{mop("("), {Kind: MathFrac, Children: []*MathNode{a, b}}, mop(")")}}, false
	case "sqrt":
		p.skipSpace()
		var index *MathNode
		if p.peek() == '[' {
			p.pos++
			start := p.pos
			for !p.eof() && p.peek() != ']' {
				p.pos++
			}
			index = ParseLaTeX(p.s[start:p.pos])
			if !p.eof() {
				p.pos++
			}
		}
		body := p.arg(depth)
		if index != nil {
			return &MathNode{Kind: MathRoot, Children: []*MathNode{body, index}}, false
		}
		return &MathNode{Kind: MathSqrt, Children: []*MathNode{body}}, false
	case "left", "right", "middle":
		p.skipSpace()
		if p.eof() {
			return nil, false
		}
		if p.peek() == '\\' {
			d := p.command()
			if v, ok := commandSymbols[d]; ok {
				return mop(v), false
			}
			if d == "{" || d == "}" || d == "|" {
				return mop(d), false
			}
			return nil, false
		}
		ch, size := utf8.DecodeRuneInString(p.s[p.pos:])
		p.pos += size
		if ch == '.' {
			return nil, false // invisible delimiter
		}
		return mop(string(ch)), false
	case "mathrm", "text", "textrm", "operatorname", "mathsf", "texttt", "mathtt", "textbf", "textit", "mbox", "rm":
		t := strings.TrimSpace(p.rawArg())
		if t == "" {
			return nil, false
		}
		return &MathNode{Kind: MathIdent, Text: t, Upright: true}, name == "operatorname"
	case "mathbf", "mathit", "boldsymbol", "bm", "mathcal", "mathscr", "mathfrak", "pmb":
		return p.arg(depth), false
	case "mathbb":
		t := p.rawArg()
		bb := map[string]string{"R": "ℝ", "N": "ℕ", "Z": "ℤ", "Q": "ℚ", "C": "ℂ", "P": "ℙ", "E": "𝔼"}
		if v, ok := bb[strings.TrimSpace(t)]; ok {
			return &MathNode{Kind: MathIdent, Text: v}, false
		}
		return &MathNode{Kind: MathIdent, Text: t}, false
	case "hat", "widehat", "bar", "overline", "vec", "tilde", "widetilde", "dot", "ddot", "check", "breve",
		"acute", "grave", "overrightarrow", "underline":
		return &MathNode{Kind: MathAccent, Text: name, Children: []*MathNode{p.arg(depth)}}, false
	case "begin":
		env := p.rawArg()
		if strings.HasPrefix(env, "array") || env == "tabular" {
			p.rawArg() // column spec
		}
		return p.environment(env, depth), false
	case "end":
		p.rawArg()
		return nil, false
	}
	if v, ok := commandSymbols[name]; ok {
		kind := MathOperator
		if r, _ := utf8.DecodeRuneInString(v); unicode.IsLetter(r) {
			kind = MathIdent
		}
		return &MathNode{Kind: kind, Text: v}, limitOps[name]
	}
	if latexFuncs[name] || limitOps[name] {
		return &MathNode{Kind: MathIdent, Text: name, Upright: true}, limitOps[name]
	}
	if len(name) == 1 { // \{ \} \| \_ \% …
		return mop(name), false
	}
	return &MathNode{Kind: MathIdent, Text: name, Upright: true}, false
}

// environment parses matrix-like environments into a row with rows
// separated by ";" and cells by "," inside the environment's delimiters.
func (p *latexParser) environment(env string, depth int) *MathNode {
	open, close := "", ""
	switch strings.TrimSuffix(env, "*") {
	case "pmatrix":
		open, close = "(", ")"
	case "bmatrix":
		open, close = "[", "]"
	case "Bmatrix", "cases":
		open = "{"
		if env != "cases" {
			close = "}"
		}
	case "vmatrix":
		open, close = "|", "|"
	case "Vmatrix":
		open, close = "‖", "‖"
	}
	out := &MathNode{Kind: MathRow}
	if open != "" {
		out.Children = append(out.Children, mop(open))
	}
	first := true
	for !p.eof() && depth < 40 {
		cell := p.row(depth + 1)
		if !first || len(cell.Children) > 0 {
			out.Children = append(out.Children, cell.Children...)
		}
		first = false
		switch {
		case p.peek() == '&':
			p.pos++
			out.Children = append(out.Children, mop(","))
		case strings.HasPrefix(p.s[p.pos:], `\\`):
			p.pos += 2
			out.Children = append(out.Children, mop(";"))
		case strings.HasPrefix(p.s[p.pos:], `\end`):
			p.pos += 4
			p.rawArg()
			if n := len(out.Children); n > 0 && out.Children[n-1].Kind == MathOperator && out.Children[n-1].Text == ";" {
				out.Children = out.Children[:n-1]
			}
			if close != "" {
				out.Children = append(out.Children, mop(close))
			}
			return out
		default:
			if p.peek() == '}' {
				p.pos++
			} else if !p.eof() {
				p.pos++
			}
		}
	}
	return out
}

func mop(s string) *MathNode { return &MathNode{Kind: MathOperator, Text: s} }

// CompactLaTeX removes whitespace that has no meaning in math mode (as
// emitted between tokens by recognisers: "\mathrm { m a x }"), keeping a
// space only where it ends a control word before a letter ("\cdot x").
// Explicit spacing commands (\, \; \quad) are kept.
func CompactLaTeX(s string) string {
	var out []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != ' ' && c != '\t' && c != '\n' {
			out = append(out, c)
			continue
		}
		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n') {
			j++
		}
		next := byte(0)
		if j < len(s) {
			next = s[j]
		}
		if endsWithCommand(string(out)) && (next >= 'a' && next <= 'z' || next >= 'A' && next <= 'Z') {
			out = append(out, ' ')
		}
		i = j - 1
	}
	return string(out)
}
