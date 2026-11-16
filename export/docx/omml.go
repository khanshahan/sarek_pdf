package docx

import (
	"strings"

	"github.com/khanshahan/sarek_pdf/model"
)

// omml renders a formula tree as Office Math Markup (m: namespace), so
// formulas are native, editable Word equations.
func omml(n *model.MathNode) string {
	var sb strings.Builder
	writeOMML(&sb, n)
	return sb.String()
}

func child(n *model.MathNode, i int) *model.MathNode {
	if n == nil || i >= len(n.Children) || n.Children[i] == nil {
		return &model.MathNode{Kind: model.MathRow}
	}
	return n.Children[i]
}

func isEmpty(n *model.MathNode) bool {
	return n == nil || n.Kind == model.MathRow && len(n.Children) == 0
}

func writeOMML(sb *strings.Builder, n *model.MathNode) {
	if n == nil {
		return
	}
	wrap := func(tag string, c *model.MathNode) {
		sb.WriteString("<m:" + tag + ">")
		writeOMML(sb, c)
		sb.WriteString("</m:" + tag + ">")
	}
	switch n.Kind {
	case model.MathRow:
		for _, c := range n.Children {
			writeOMML(sb, c)
		}
	case model.MathIdent, model.MathNumber, model.MathOperator:
		sb.WriteString("<m:r>")
		if n.Kind == model.MathIdent && n.Upright {
			sb.WriteString(`<m:rPr><m:sty m:val="p"/></m:rPr>`)
		}
		sb.WriteString(`<m:t xml:space="preserve">` + esc(n.Text) + "</m:t></m:r>")
	case model.MathFrac:
		sb.WriteString("<m:f>")
		wrap("num", child(n, 0))
		wrap("den", child(n, 1))
		sb.WriteString("</m:f>")
	case model.MathSqrt:
		sb.WriteString(`<m:rad><m:radPr><m:degHide m:val="1"/></m:radPr><m:deg/>`)
		wrap("e", child(n, 0))
		sb.WriteString("</m:rad>")
	case model.MathRoot:
		sb.WriteString("<m:rad>")
		wrap("deg", child(n, 1))
		wrap("e", child(n, 0))
		sb.WriteString("</m:rad>")
	case model.MathSup:
		sb.WriteString("<m:sSup>")
		wrap("e", child(n, 0))
		wrap("sup", child(n, 1))
		sb.WriteString("</m:sSup>")
	case model.MathSub:
		sb.WriteString("<m:sSub>")
		wrap("e", child(n, 0))
		wrap("sub", child(n, 1))
		sb.WriteString("</m:sSub>")
	case model.MathSubSup:
		sb.WriteString("<m:sSubSup>")
		wrap("e", child(n, 0))
		wrap("sub", child(n, 1))
		wrap("sup", child(n, 2))
		sb.WriteString("</m:sSubSup>")
	case model.MathUnderOver:
		base, under, over := child(n, 0), child(n, 1), child(n, 2)
		if base.Kind == model.MathOperator && len([]rune(base.Text)) == 1 {
			// n-ary operator (∑, ∫, …) with limits above and below.
			sb.WriteString(`<m:nary><m:naryPr><m:chr m:val="` + esc(base.Text) + `"/><m:limLoc m:val="undOvr"/>`)
			if isEmpty(under) {
				sb.WriteString(`<m:subHide m:val="1"/>`)
			}
			if isEmpty(over) {
				sb.WriteString(`<m:supHide m:val="1"/>`)
			}
			sb.WriteString("</m:naryPr>")
			wrap("sub", under)
			wrap("sup", over)
			sb.WriteString("<m:e/></m:nary>")
			return
		}
		// Other bases: lower limit, then upper limit, nested.
		out := omml(base)
		if !isEmpty(under) {
			out = "<m:limLow><m:e>" + out + "</m:e><m:lim>" + omml(under) + "</m:lim></m:limLow>"
		}
		if !isEmpty(over) {
			out = "<m:limUpp><m:e>" + out + "</m:e><m:lim>" + omml(over) + "</m:lim></m:limUpp>"
		}
		sb.WriteString(out)
	}
}
