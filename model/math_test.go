package model

import (
	"strings"
	"testing"
)

func id(s string) *MathNode  { return &MathNode{Kind: MathIdent, Text: s} }
func num(s string) *MathNode { return &MathNode{Kind: MathNumber, Text: s} }
func op(s string) *MathNode  { return &MathNode{Kind: MathOperator, Text: s} }
func row(c ...*MathNode) *MathNode {
	return &MathNode{Kind: MathRow, Children: c}
}

func TestMathRenderers(t *testing.T) {
	// e^{iπ} + 1 = 0 ; Σ_{k=1}^{n} k ; sin x ; √[3]{x}
	tree := row(
		&MathNode{Kind: MathSup, Children: []*MathNode{id("e"), row(id("i"), id("π"))}}, op("+"), num("1"), op("="), num("0"), op(","),
		&MathNode{Kind: MathUnderOver, Children: []*MathNode{op("∑"), row(id("k"), op("="), num("1")), id("n")}}, id("k"), op(","),
		&MathNode{Kind: MathIdent, Text: "sin", Upright: true}, id("x"), op(","),
		&MathNode{Kind: MathRoot, Children: []*MathNode{id("x"), num("3")}},
	)
	if got, want := tree.LaTeX(), `e^{i\pi} + 1 = 0,\sum_{k = 1}^{n}k,\sin x,\sqrt[3]{x}`; got != want {
		t.Errorf("LaTeX = %s\nwant    %s", got, want)
	}
	ml := tree.MathML()
	for _, w := range []string{"<msup><mi>e</mi><mrow><mi>i</mi><mi>π</mi></mrow></msup>", "<munderover><mo>∑</mo>", `<mi mathvariant="normal">sin</mi>`, "<mroot><mi>x</mi><mn>3</mn></mroot>"} {
		if !strings.Contains(ml, w) {
			t.Errorf("MathML missing %s in %s", w, ml)
		}
	}
	if got := (&Formula{Root: row(id("a"), op("="), &MathNode{Kind: MathFrac, Children: []*MathNode{row(id("b"), op("+"), id("c")), num("2")}}), Number: "(4)"}).Text(); got != "a = (b + c)/2 (4)" {
		t.Errorf("linear = %q", got)
	}
}

func TestLaTeXEscapes(t *testing.T) {
	if got := row(op("{"), id("x"), op("}"), op("%"), op("_")).LaTeX(); got != `\{x\}\%\_` {
		t.Errorf("escapes = %s", got)
	}
}
