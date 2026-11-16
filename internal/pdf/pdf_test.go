package pdf

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func parse(t *testing.T, s string) Object {
	t.Helper()
	o, err := NewParser([]byte(s), 0).ParseObject()
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return o
}

func TestParserObjects(t *testing.T) {
	if v := parse(t, "(a\\(b\\)c\\101\\\nd)"); v != String("a(b)cAd") {
		t.Errorf("literal = %q", v)
	}
	if v := parse(t, "<48 65 6C6C 6F7>"); v != String("Hellop") {
		t.Errorf("hex = %q", v)
	}
	if v := parse(t, "/A#20B"); v != Name("A B") {
		t.Errorf("name = %q", v)
	}
	if v := parse(t, "12 0 R"); v != (Ref{12, 0}) {
		t.Errorf("ref = %v", v)
	}
	if v := parse(t, "-.5"); v != Real(-0.5) {
		t.Errorf("real = %v", v)
	}
	if v := parse(t, "--3"); v != Integer(3) {
		t.Errorf("double negative = %v", v)
	}
	d, ok := parse(t, "<< /Type /Page /Kids [1 0 R 2 0 R] /N null /X << /Y 1 >> >>").(Dict)
	if !ok || d["Type"] != Name("Page") || len(d["Kids"].(Array)) != 2 || d["X"].(Dict)["Y"] != Integer(1) {
		t.Errorf("dict = %v", d)
	}
	if _, has := d["N"]; has {
		t.Error("null entries must be dropped")
	}
}

func TestParserHostile(t *testing.T) {
	deep := bytes.Repeat([]byte("["), 100000)
	if _, err := NewParser(deep, 0).ParseObject(); err == nil {
		t.Error("expected depth error")
	}
	for _, s := range []string{"<<", "(unterminated", "<abc", "[1 2", "/", ")", "<< /A >>", "9999999999999999999999"} {
		NewParser([]byte(s), 0).ParseObject() // must not panic
	}
}

func TestFilters(t *testing.T) {
	if got, _ := ascii85([]byte("87cURD]i,\"Ebo80~>")); string(got) != "Hello World!" {
		t.Errorf("ascii85 = %q", got)
	}
	if got := runLength([]byte{2, 'a', 'b', 'c', 254, 'x', 128}); string(got) != "abcxxx" {
		t.Errorf("runlength = %q", got)
	}
	if got := asciiHex([]byte("48 65 6c 6c 6f>")); string(got) != "Hello" {
		t.Errorf("asciihex = %q", got)
	}
	// LZW sample from ISO 32000-1 §7.4.4.2: 45 45 45 45 45 65 45 45 45 66.
	lzw := []byte{0x80, 0x0B, 0x60, 0x50, 0x22, 0x0C, 0x0C, 0x85, 0x01}
	if got, _ := lzwDecode(lzw, true); !bytes.Equal(got, []byte{45, 45, 45, 45, 45, 65, 45, 45, 45, 66}) {
		t.Errorf("lzw = %v", got)
	}
	// PNG Up predictor.
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	zw.Write([]byte{2, 1, 2, 3, 2, 1, 1, 1})
	zw.Close()
	got, err := applyFilter("FlateDecode", z.Bytes(), Dict{"Predictor": Integer(12), "Columns": Integer(3)})
	if err != nil || !bytes.Equal(got, []byte{1, 2, 3, 2, 3, 4}) {
		t.Errorf("png predictor = %v %v", got, err)
	}
}

func TestRepairBrokenXref(t *testing.T) {
	src := "%PDF-1.4\n1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n" +
		"2 0 obj << /Type /Pages /Kids [3 0 R] /Count 1 >> endobj\n" +
		"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 100 100] >> endobj\n" +
		"xref\n0 4\n0000000000 65535 f \n0000000999 00000 n \n0000000999 00000 n \n0000000999 00000 n \n" +
		"trailer << /Size 4 /Root 1 0 R >>\nstartxref\n9999\n%%EOF\n"
	r, err := NewReader([]byte(src), "")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Repaired() {
		t.Error("expected repair")
	}
	pages, err := r.Pages()
	if err != nil || len(pages) != 1 {
		t.Fatalf("pages = %d, %v", len(pages), err)
	}
}
