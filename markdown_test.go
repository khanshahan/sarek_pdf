package sarekpdf

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/export/docx"
	"github.com/khanshahan/sarek_pdf/export/markdown"
	"github.com/khanshahan/sarek_pdf/model"
	"github.com/khanshahan/sarek_pdf/ocr"
)

func toMarkdown(t *testing.T, res *model.Document, o markdown.Options) string {
	t.Helper()
	var b bytes.Buffer
	if err := markdown.Write(&b, res, o); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestMarkdownSample(t *testing.T) {
	doc, _ := OpenBytes(samplePDF(true))
	res, err := doc.Extract(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assets := map[string][]byte{}
	o := markdown.DefaultOptions()
	o.Assets = func(name string, data []byte) (string, error) {
		assets[name] = data
		return "assets/" + name, nil
	}
	md := toMarkdown(t, res, o)
	for _, want := range []string{
		"<!-- page 1 -->",
		"# Annual Report 2025\n",
		"The quick brown fox jumps over the lazy dog. This paragraph is long enough",
		"| Region | Revenue | Growth |\n| --- | --- | --- |\n| North | 100 | 5% |\n| South | 200 | 7% |",
		"![drawing: Figure](assets/page1-drawing",
		"<!-- page 2 -->",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q\n---\n%s", want, md)
		}
	}
	if len(assets) != 1 {
		t.Errorf("assets = %d, want 1 drawing", len(assets))
	}
	for name, data := range assets {
		if !strings.HasSuffix(name, ".svg") || !bytes.HasPrefix(data, []byte("<svg")) {
			t.Errorf("asset %s is not an SVG", name)
		}
	}
}

func TestMarkdownMergedTable(t *testing.T) {
	doc, _ := OpenBytes(splitTablePDF())
	res, _ := doc.Extract(context.Background())
	md := toMarkdown(t, res, markdown.DefaultOptions())
	if n := strings.Count(md, "| Item | Quantity | Price |"); n != 1 {
		t.Errorf("header written %d times, want 1 (merged table)\n%s", n, md)
	}
	if !strings.Contains(md, "| Part 5 | 50 | 15.00 |\n| Part 6 | 60 | 18.00 |") {
		t.Errorf("continuation rows not merged:\n%s", md)
	}
	// DOCX writes the merged table once.
	var b bytes.Buffer
	if err := docx.Write(&b, res, docx.Options{}); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, _ := f.Open()
		body, _ := io.ReadAll(rc)
		rc.Close()
		// Two tables in the document: the merged one and the page-3 table.
		if n := bytes.Count(body, []byte("<w:tbl>")); n != 2 {
			t.Errorf("docx tables = %d, want 2", n)
		}
		if n := bytes.Count(body, []byte(">Item<")); n != 1 {
			t.Errorf("docx header written %d times, want 1", n)
		}
	}
}

func TestMarkdownEscapingAndLists(t *testing.T) {
	st := model.Style{}
	doc := &model.Document{Pages: []*model.Page{{Number: 1, Blocks: []model.Block{
		&model.Paragraph{Runs: []model.Run{{Text: "# not a heading *not italic* [x](y) a|b", Style: st}}},
		&model.Paragraph{Runs: []model.Run{{Text: "1. not a list, just text", Style: st}}},
		&model.Paragraph{Role: model.RoleListItem, Runs: []model.Run{{Text: "• first point", Style: st}}},
		&model.Paragraph{Role: model.RoleListItem, Runs: []model.Run{{Text: "2) second point", Style: st}}},
		&model.Paragraph{Runs: []model.Run{{Text: "Mixed ", Style: st}, {Text: "bold ", Style: model.Style{Bold: true}}, {Text: "end", Style: model.Style{Italic: true}}}},
	}}}}
	md := toMarkdown(t, doc, markdown.Options{})
	for _, want := range []string{
		`\# not a heading \*not italic\* \[x\](y) a\|b`,
		`1\. not a list, just text`,
		"- first point",
		"2) second point",
		"Mixed **bold** *end*",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}

func TestMarkdownOCRNotice(t *testing.T) {
	doc, _ := OpenBytes(scannedPDF(), WithOCR(&fakeOCR{}, ocr.Auto))
	res, _ := doc.Extract(context.Background())
	md := toMarkdown(t, res, markdown.DefaultOptions())
	if !strings.Contains(md, "> page 1: text recognised by OCR (fake)") {
		t.Errorf("OCR notice missing:\n%s", md)
	}
	if strings.Contains(md, "![image]") {
		t.Error("scan image whose text was OCR'd must not be repeated")
	}
}
