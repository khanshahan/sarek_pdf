package model

import (
	"strings"
	"unicode"
)

// Formula is a displayed mathematical formula, reconstructed from the
// positions of its glyphs and rules (fraction bars, radical bars) in the
// PDF text layer. Nothing is inferred beyond that geometry.
type Formula struct {
	BBox Rect
	// Root is the formula's structure.
	Root *MathNode
	// Number is an equation number printed beside the formula, e.g. "(3)".
	Number string
	Source Source
}

// Bounds implements Block.
func (f *Formula) Bounds() Rect { return f.BBox }
func (*Formula) isBlock()       {}

// LaTeX returns the formula as LaTeX math (without delimiters).
func (f *Formula) LaTeX() string { return f.Root.LaTeX() }

// MathML returns the formula as a MathML <math> element.
func (f *Formula) MathML() string {
	return `<math xmlns="http://www.w3.org/1998/Math/MathML" display="block">` + f.Root.MathML() + `</math>`
}

// Text returns a readable linear form, e.g. "x = (−b ± √(b^2 − 4ac))/(2a) (1)".
func (f *Formula) Text() string {
	t := f.Root.Linear()
	if f.Number != "" {
		t += " " + f.Number
	}
	return t
}

// MathKind is the type of a MathNode.
type MathKind uint8

const (
	MathRow       MathKind = iota // Children in sequence
	MathIdent                     // identifier (variable, function name): Text
	MathNumber                    // number: Text
	MathOperator                  // operator, relation, delimiter: Text
	MathFrac                      // Children: numerator, denominator
	MathSqrt                      // Children: radicand
	MathRoot                      // Children: radicand, index
	MathSup                       // Children: base, superscript
	MathSub                       // Children: base, subscript
	MathSubSup                    // Children: base, subscript, superscript
	MathUnderOver                 // Children: base (large operator), under, over (may be empty rows)
)

// MathNode is a node of a formula tree.
type MathNode struct {
	Kind     MathKind
	Text     string
	Upright  bool // identifier set in an upright font (function names, units)
	Children []*MathNode
}

func (n *MathNode) child(i int) *MathNode {
	if n == nil || i >= len(n.Children) || n.Children[i] == nil {
		return &MathNode{Kind: MathRow}
	}
	return n.Children[i]
}

// ---------------------------------------------------------------------------
// LaTeX

var latexSymbols = map[rune]string{
	'α': `\alpha`, 'β': `\beta`, 'γ': `\gamma`, 'δ': `\delta`, 'ε': `\varepsilon`, 'ϵ': `\epsilon`,
	'ζ': `\zeta`, 'η': `\eta`, 'θ': `\theta`, 'ϑ': `\vartheta`, 'ι': `\iota`, 'κ': `\kappa`,
	'λ': `\lambda`, 'μ': `\mu`, 'µ': `\mu`, 'ν': `\nu`, 'ξ': `\xi`, 'π': `\pi`, 'ϖ': `\varpi`,
	'ρ': `\rho`, 'ϱ': `\varrho`, 'σ': `\sigma`, 'ς': `\varsigma`, 'τ': `\tau`, 'υ': `\upsilon`,
	'φ': `\varphi`, 'ϕ': `\phi`, 'χ': `\chi`, 'ψ': `\psi`, 'ω': `\omega`,
	'Γ': `\Gamma`, 'Δ': `\Delta`, 'Θ': `\Theta`, 'Λ': `\Lambda`, 'Ξ': `\Xi`, 'Π': `\Pi`,
	'Σ': `\Sigma`, 'Υ': `\Upsilon`, 'Φ': `\Phi`, 'Ψ': `\Psi`, 'Ω': `\Omega`,
	'−': `-`, '±': `\pm`, '∓': `\mp`, '×': `\times`, '·': `\cdot`, '⋅': `\cdot`, '÷': `\div`,
	'∗': `\ast`, '∘': `\circ`, '•': `\bullet`, '⊕': `\oplus`, '⊗': `\otimes`,
	'≤': `\leq`, '≥': `\geq`, '≠': `\neq`, '≈': `\approx`, '≡': `\equiv`, '∼': `\sim`, '≃': `\simeq`,
	'≅': `\cong`, '∝': `\propto`, '≪': `\ll`, '≫': `\gg`, '≺': `\prec`, '≻': `\succ`,
	'∈': `\in`, '∉': `\notin`, '∋': `\ni`, '⊂': `\subset`, '⊃': `\supset`, '⊆': `\subseteq`,
	'⊇': `\supseteq`, '∪': `\cup`, '∩': `\cap`, '∅': `\emptyset`, '∖': `\setminus`,
	'∀': `\forall`, '∃': `\exists`, '¬': `\neg`, '∧': `\wedge`, '∨': `\vee`,
	'→': `\to`, '←': `\leftarrow`, '↔': `\leftrightarrow`, '⇒': `\Rightarrow`, '⇐': `\Leftarrow`,
	'⇔': `\Leftrightarrow`, '↦': `\mapsto`, '↑': `\uparrow`, '↓': `\downarrow`,
	'∞': `\infty`, '∂': `\partial`, '∇': `\nabla`, '′': `'`, '″': `''`, 'ℓ': `\ell`, 'ℏ': `\hbar`,
	'∑': `\sum`, '∏': `\prod`, '∐': `\coprod`, '∫': `\int`, '∬': `\iint`, '∭': `\iiint`, '∮': `\oint`,
	'⋃': `\bigcup`, '⋂': `\bigcap`, '⋁': `\bigvee`, '⋀': `\bigwedge`, '⨁': `\bigoplus`, '⨂': `\bigotimes`,
	'…': `\ldots`, '⋯': `\cdots`, '⋮': `\vdots`, '⋱': `\ddots`,
	'⟨': `\langle`, '⟩': `\rangle`, '⌊': `\lfloor`, '⌋': `\rfloor`, '⌈': `\lceil`, '⌉': `\rceil`,
	'‖': `\|`, '{': `\{`, '}': `\}`, '%': `\%`, '#': `\#`, '&': `\&`, '_': `\_`, '$': `\$`,
	'ℝ': `\mathbb{R}`, 'ℕ': `\mathbb{N}`, 'ℤ': `\mathbb{Z}`, 'ℚ': `\mathbb{Q}`, 'ℂ': `\mathbb{C}`,
	'⊤': `\top`, '⊥': `\perp`, '⊢': `\vdash`, '√': `\surd`, '°': `^{\circ}`,
}

// Functions typeset upright that LaTeX has commands for.
var latexFuncs = map[string]bool{
	"sin": true, "cos": true, "tan": true, "cot": true, "sec": true, "csc": true, "arcsin": true,
	"arccos": true, "arctan": true, "sinh": true, "cosh": true, "tanh": true, "log": true, "ln": true,
	"lg": true, "exp": true, "lim": true, "liminf": true, "limsup": true, "max": true, "min": true,
	"sup": true, "inf": true, "det": true, "dim": true, "ker": true, "deg": true, "arg": true,
	"gcd": true, "Pr": true, "hom": true,
}

func latexText(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if v, ok := latexSymbols[r]; ok {
			sb.WriteString(v)
			continue
		}
		switch r {
		case '\\':
			sb.WriteString(`\backslash`)
		case '^':
			sb.WriteString(`\hat{}`)
		case '~':
			sb.WriteString(`\sim`)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// group wraps LaTeX in braces unless it is a single character.
func group(s string) string { return "{" + s + "}" }

// LaTeX renders the node as LaTeX math.
func (n *MathNode) LaTeX() string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case MathRow:
		parts := make([]string, 0, len(n.Children))
		for _, c := range n.Children {
			parts = append(parts, c.LaTeX())
		}
		return joinLaTeX(parts)
	case MathIdent:
		if n.Upright && len([]rune(n.Text)) > 1 {
			if latexFuncs[n.Text] {
				return `\` + n.Text
			}
			return `\mathrm{` + latexText(n.Text) + `}`
		}
		return latexText(n.Text)
	case MathNumber, MathOperator:
		return latexText(n.Text)
	case MathFrac:
		return `\frac` + group(n.child(0).LaTeX()) + group(n.child(1).LaTeX())
	case MathSqrt:
		return `\sqrt` + group(n.child(0).LaTeX())
	case MathRoot:
		return `\sqrt[` + n.child(1).LaTeX() + `]` + group(n.child(0).LaTeX())
	case MathSup:
		return n.child(0).LaTeX() + `^` + group(n.child(1).LaTeX())
	case MathSub:
		return n.child(0).LaTeX() + `_` + group(n.child(1).LaTeX())
	case MathSubSup:
		return n.child(0).LaTeX() + `_` + group(n.child(1).LaTeX()) + `^` + group(n.child(2).LaTeX())
	case MathUnderOver:
		s := n.child(0).LaTeX()
		if u := n.child(1).LaTeX(); u != "" {
			s += `_` + group(u)
		}
		if o := n.child(2).LaTeX(); o != "" {
			s += `^` + group(o)
		}
		return s
	}
	return ""
}

// joinLaTeX concatenates rendered children, spacing around relations and
// binary operators and wherever a command would run into a following
// letter (\alpha x, not \alphax).
func joinLaTeX(parts []string) string {
	var sb strings.Builder
	prev := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if prev != "" && (relBin[p] || relBin[prev] || endsWithCommand(prev) && startsWithLetter(p)) {
			sb.WriteByte(' ')
		}
		sb.WriteString(p)
		prev = p
	}
	return sb.String()
}

func endsWithCommand(s string) bool {
	i := len(s) - 1
	for i >= 0 && (s[i] >= 'a' && s[i] <= 'z' || s[i] >= 'A' && s[i] <= 'Z') {
		i--
	}
	return i >= 0 && i < len(s)-1 && s[i] == '\\'
}

func startsWithLetter(s string) bool {
	return s != "" && (s[0] >= 'a' && s[0] <= 'z' || s[0] >= 'A' && s[0] <= 'Z')
}

var relBin = map[string]bool{"=": true, "+": true, "-": true, `\pm`: true, `\mp`: true, `\times`: true,
	`\cdot`: true, `\leq`: true, `\geq`: true, `\neq`: true, `\approx`: true, `\equiv`: true, "<": true,
	">": true, `\to`: true, `\Rightarrow`: true, `\in`: true, `\sim`: true, `\div`: true, `\propto`: true}

// ---------------------------------------------------------------------------
// MathML

func xmlEsc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// MathML renders the node as MathML presentation markup (no <math>).
func (n *MathNode) MathML() string {
	if n == nil {
		return "<mrow/>"
	}
	switch n.Kind {
	case MathRow:
		var sb strings.Builder
		sb.WriteString("<mrow>")
		for _, c := range n.Children {
			sb.WriteString(c.MathML())
		}
		sb.WriteString("</mrow>")
		return sb.String()
	case MathIdent:
		if n.Upright && len([]rune(n.Text)) > 1 {
			return `<mi mathvariant="normal">` + xmlEsc(n.Text) + "</mi>"
		}
		return "<mi>" + xmlEsc(n.Text) + "</mi>"
	case MathNumber:
		return "<mn>" + xmlEsc(n.Text) + "</mn>"
	case MathOperator:
		return "<mo>" + xmlEsc(n.Text) + "</mo>"
	case MathFrac:
		return "<mfrac>" + n.child(0).MathML() + n.child(1).MathML() + "</mfrac>"
	case MathSqrt:
		return "<msqrt>" + n.child(0).MathML() + "</msqrt>"
	case MathRoot:
		return "<mroot>" + n.child(0).MathML() + n.child(1).MathML() + "</mroot>"
	case MathSup:
		return "<msup>" + n.child(0).MathML() + n.child(1).MathML() + "</msup>"
	case MathSub:
		return "<msub>" + n.child(0).MathML() + n.child(1).MathML() + "</msub>"
	case MathSubSup:
		return "<msubsup>" + n.child(0).MathML() + n.child(1).MathML() + n.child(2).MathML() + "</msubsup>"
	case MathUnderOver:
		return "<munderover>" + n.child(0).MathML() + n.child(1).MathML() + n.child(2).MathML() + "</munderover>"
	}
	return "<mrow/>"
}

// ---------------------------------------------------------------------------
// Linear text

func paren(s string) string {
	if len([]rune(s)) <= 1 || isSimple(s) {
		return s
	}
	return "(" + s + ")"
}

func isSimple(s string) bool {
	for _, r := range s {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.') {
			return false
		}
	}
	return true
}

// Linear renders the node as readable plain text.
func (n *MathNode) Linear() string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case MathRow:
		var sb strings.Builder
		for i, c := range n.Children {
			t := c.Linear()
			if i > 0 && (c.Kind == MathOperator && isSpacedOp(c.Text) || n.Children[i-1].Kind == MathOperator && isSpacedOp(n.Children[i-1].Text)) {
				sb.WriteByte(' ')
			}
			sb.WriteString(t)
		}
		return sb.String()
	case MathIdent, MathNumber, MathOperator:
		return n.Text
	case MathFrac:
		return paren(n.child(0).Linear()) + "/" + paren(n.child(1).Linear())
	case MathSqrt:
		return "√" + paren(n.child(0).Linear())
	case MathRoot:
		return "root" + paren(n.child(1).Linear()) + paren(n.child(0).Linear())
	case MathSup:
		return n.child(0).Linear() + "^" + paren(n.child(1).Linear())
	case MathSub:
		return n.child(0).Linear() + "_" + paren(n.child(1).Linear())
	case MathSubSup:
		return n.child(0).Linear() + "_" + paren(n.child(1).Linear()) + "^" + paren(n.child(2).Linear())
	case MathUnderOver:
		s := n.child(0).Linear()
		if u := n.child(1).Linear(); u != "" {
			s += "_" + paren(u)
		}
		if o := n.child(2).Linear(); o != "" {
			s += "^" + paren(o)
		}
		return s
	}
	return ""
}

func isSpacedOp(s string) bool {
	switch s {
	case "=", "+", "−", "-", "±", "∓", "×", "≤", "≥", "≠", "≈", "≡", "<", ">", "→", "⇒", "∈", "·", "÷":
		return true
	}
	return false
}
