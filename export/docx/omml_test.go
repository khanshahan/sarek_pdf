package docx

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/model"
)

func TestOMMLWellFormed(t *testing.T) {
	n := func(k model.MathKind, c ...*model.MathNode) *model.MathNode {
		return &model.MathNode{Kind: k, Children: c}
	}
	leaf := func(k model.MathKind, s string) *model.MathNode { return &model.MathNode{Kind: k, Text: s} }
	tree := n(model.MathRow,
		n(model.MathFrac, n(model.MathRow, leaf(model.MathIdent, "a"), leaf(model.MathOperator, "<")), leaf(model.MathNumber, "2")),
		n(model.MathSqrt, leaf(model.MathIdent, "x")),
		n(model.MathRoot, leaf(model.MathIdent, "y"), leaf(model.MathNumber, "3")),
		n(model.MathSubSup, leaf(model.MathIdent, "z"), leaf(model.MathNumber, "1"), leaf(model.MathNumber, "2")),
		n(model.MathUnderOver, leaf(model.MathOperator, "∑"), leaf(model.MathIdent, "i"), leaf(model.MathIdent, "n")),
		n(model.MathUnderOver, &model.MathNode{Kind: model.MathIdent, Text: "lim", Upright: true}, leaf(model.MathIdent, "x"), n(model.MathRow)),
	)
	doc := `<w xmlns:m="m"><m:oMath>` + omml(tree) + `</m:oMath></w>`
	d := xml.NewDecoder(strings.NewReader(doc))
	for {
		_, err := d.Token()
		if err != nil {
			if err.Error() != "EOF" {
				t.Fatalf("not well-formed: %v\n%s", err, doc)
			}
			break
		}
	}
	for _, w := range []string{"<m:f>", "<m:degHide", "<m:deg><m:r>", "<m:sSubSup>", `<m:chr m:val="∑"/>`, "<m:limLow>", "&lt;"} {
		if !strings.Contains(doc, w) {
			t.Errorf("OMML missing %s", w)
		}
	}
}
