package sarekpdf

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/khanshahan/sarek_pdf/model"
)

// qpdf (Apache-2.0) is used only as an external test oracle to produce
// PDF 1.5+ structures and encrypted files; it is never linked or shipped.
func qpdf(t *testing.T, in []byte, args ...string) []byte {
	t.Helper()
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf not installed")
	}
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "in.pdf"), filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(src, in, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("qpdf", append(append([]string{src}, args...), dst)...)
	if out, err := cmd.CombinedOutput(); err != nil && cmd.ProcessState.ExitCode() != 3 {
		t.Fatalf("qpdf %v: %v\n%s", args, err, out)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func qpdfCheck(t *testing.T, data []byte) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "check.pdf")
	os.WriteFile(p, data, 0o600)
	out, err := exec.Command("qpdf", "--check", p).CombinedOutput()
	if err != nil {
		t.Fatalf("qpdf --check failed: %v\n%s", err, out)
	}
	if bytes.Contains(out, []byte("WARNING")) {
		t.Errorf("qpdf warnings:\n%s", out)
	}
}

func mustContain(t *testing.T, doc *Document, want ...string) {
	t.Helper()
	text, err := doc.Text(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("text missing %q", w)
		}
	}
}

func TestObjectAndXrefStreams(t *testing.T) {
	data := qpdf(t, samplePDF(true), "--object-streams=generate", "--min-version=1.5")
	if !bytes.Contains(data, []byte("/ObjStm")) {
		t.Fatal("qpdf did not produce object streams")
	}
	doc, err := OpenBytes(data)
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, doc, "Annual Report 2025", "Zß€", "South")
}

func TestEncryption(t *testing.T) {
	cases := [][]string{
		{"--encrypt", "user", "owner", "40", "--"},
		{"--encrypt", "user", "owner", "128", "--use-aes=n", "--"},
		{"--encrypt", "user", "owner", "128", "--use-aes=y", "--"},
		{"--encrypt", "user", "owner", "256", "--"},
	}
	for _, args := range cases {
		t.Run(args[3], func(t *testing.T) {
			data := qpdf(t, samplePDF(true), append([]string{"--allow-weak-crypto"}, args...)...)
			if _, err := OpenBytes(data); err == nil {
				t.Fatal("opened without password")
			}
			for _, pw := range []string{"user", "owner"} {
				doc, err := OpenBytes(data, WithPassword(pw))
				if err != nil {
					t.Fatalf("password %q: %v", pw, err)
				}
				if doc.Metadata().Title != "Sample Report" {
					t.Errorf("title = %q", doc.Metadata().Title)
				}
				mustContain(t, doc, "Annual Report 2025", "1,234,567 EUR")
			}
		})
	}
}

func TestHighlightAndExtractPages(t *testing.T) {
	for _, variant := range []string{"table", "xrefstream"} {
		t.Run(variant, func(t *testing.T) {
			src := samplePDF(true)
			if variant == "xrefstream" {
				src = qpdf(t, src, "--object-streams=generate")
			}
			doc, err := OpenBytes(src)
			if err != nil {
				t.Fatal(err)
			}
			ms, err := doc.Search(context.Background(), "revenue", SearchOptions{})
			if err != nil || len(ms) != 2 {
				t.Fatalf("matches=%d err=%v", len(ms), err)
			}
			var buf bytes.Buffer
			if err := doc.WriteHighlighted(&buf, HighlightMatches(ms, model.Color{})); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(buf.Bytes(), src) {
				t.Error("incremental update must preserve original bytes")
			}
			qpdfCheck(t, buf.Bytes())
			hd, err := OpenBytes(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			mustContain(t, hd, "Annual Report 2025")

			buf.Reset()
			if err := doc.ExtractPages(&buf, []int{2}); err != nil {
				t.Fatal(err)
			}
			qpdfCheck(t, buf.Bytes())
			ed, err := OpenBytes(buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if ed.NumPages() != 1 {
				t.Fatalf("extracted pages = %d", ed.NumPages())
			}
			mustContain(t, ed, "Right column")
		})
	}
}
