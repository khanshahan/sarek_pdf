package model

import "testing"

func TestParseLaTeX(t *testing.T) {
	cases := map[string]string{
		`x = \frac{-b \pm \sqrt{b^{2} - 4ac}}{2a}`:            `x = \frac{-b \pm \sqrt{b^{2} - 4ac}}{2a}`,
		`\sum_{i=1}^{n} i^2 = \frac{n(n+1)(2n+1)}{6}`:         `\sum_{i = 1}^{n}i^{2} = \frac{n(n + 1)(2n + 1)}{6}`,
		`\left( \frac{a}{b} \right)^{2} + c`:                  `(\frac{a}{b})^{2} + c`,
		`\int_0^\infty e^{-x^2} \, dx = \frac{\sqrt{\pi}}{2}`: `\int_{0}^{\infty}e^{-x^{2}}dx = \frac{\sqrt{\pi}}{2}`,
		`\mathrm{softmax}(\frac{QK^T}{\sqrt{d_k}})V`:          `\mathrm{softmax}(\frac{QK^{T}}{\sqrt{d_{k}}})V`,
		`\lim_{x \to 0} \frac{\sin x}{x} = 1`:                 `\lim_{x \to 0}\frac{\sin x}{x} = 1`,
		`\sqrt[3]{x} + \hat{y}`:                               `\sqrt[3]{x} + \hat{y}`,
		`\begin{pmatrix} a & b \\ c & d \end{pmatrix}`:        `(a,b;c,d)`,
		`\mathbb{R}^{n} \alpha_i'`:                            `\mathbb{R}^{n}\alpha_{i}^{'}`,
		`}{ broken \frac{a}`:                                  `broken\frac{a}{}`,
	}
	for in, want := range cases {
		if got := ParseLaTeX(in).LaTeX(); squashSpaces(got) != squashSpaces(want) {
			t.Errorf("ParseLaTeX(%q).LaTeX() = %q, want %q", in, got, want)
		}
	}
	// Linear and MathML renderings come from the same tree.
	n := ParseLaTeX(`\frac{1}{\sqrt{d_k}}`)
	if got := n.Linear(); got != "1/√(d_k)" {
		t.Errorf("linear = %q", got)
	}
}

func squashSpaces(s string) string {
	out := []rune{}
	for _, r := range s {
		if r != ' ' {
			out = append(out, r)
		}
	}
	return string(out)
}

func TestCompactLaTeX(t *testing.T) {
	cases := map[string]string{
		`\mathrm { A t t e n t i o n } ( Q , K )`:      `\mathrm{Attention}(Q,K)`,
		`a \cdot x + \alpha y`:                         `a\cdot x+\alpha y`,
		`\operatorname* { m a x } ( 0 , x W _ { 1 } )`: `\operatorname*{max}(0,xW_{1})`,
		`h \, = \, 8`: `h\,=\,8`,
	}
	for in, want := range cases {
		if got := CompactLaTeX(in); got != want {
			t.Errorf("CompactLaTeX(%q) = %q, want %q", in, got, want)
		}
	}
}
