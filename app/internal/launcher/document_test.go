package launcher

import (
	"archive/zip"
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"
)

func docxFixture(t *testing.T, xml string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	f, err := z.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte(xml))
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func pdfFixture(content string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", "<< /Type /Pages /Kids [3 0 R] /Count 1 >>", "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>", "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>", fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)}
	var offsets []int
	for i, obj := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, obj)
	}
	xref := b.Len()
	b.WriteString("xref\n0 6\n0000000000 65535 f \n")
	for _, offset := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", xref)
	return b.Bytes()
}
func TestDocumentText(t *testing.T) {
	doc := docxFixture(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>Hello &amp; 你好</w:t><w:tab/><w:t>42</w:t><w:br/><w:t>Next</w:t></w:r></w:p></w:document>`)
	got, err := documentText(doc, ".docx")
	if err != nil || got != "Hello & 你好\t42\nNext\n\n" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = documentText(pdfFixture("BT /F1 12 Tf 72 720 Td (Hello PDF 42) Tj ET"), ".pdf")
	if err != nil || !strings.Contains(got, "Hello PDF 42") {
		t.Fatalf("%q %v", got, err)
	}
	for _, ext := range []string{".docx", ".pdf", ".txt"} {
		if _, err := documentText([]byte("invalid"), ext); err == nil {
			t.Fatalf("accepted %s", ext)
		}
	}
	if _, err := documentText(docxFixture(t, `<broken>`), ".docx"); err == nil {
		t.Fatal("accepted invalid XML")
	}
}
func TestDocumentUpload(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		code int
		want string
	}{
		{"draft.DOCX", docxFixture(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>Draft 42</w:t></w:r></w:p></w:document>`), 200, "Draft 42"},
		{"draft.pdf", pdfFixture(""), 422, "uploadEmpty"},
		{"draft.docx", []byte("bad"), 422, "uploadUnreadable"},
		{"draft.txt", []byte("bad"), 422, "uploadUnreadable"},
		{"draft.docx", make([]byte, maxDocumentBytes+1), 413, "uploadLarge"},
	}
	for _, tc := range cases {
		t.Run(tc.name+tc.want, func(t *testing.T) {
			var body bytes.Buffer
			m := multipart.NewWriter(&body)
			f, _ := m.CreateFormFile("file", tc.name)
			f.Write(tc.data)
			m.Close()
			req := httptest.NewRequest("POST", "/app/document", &body)
			req.Header.Set("Content-Type", m.FormDataContentType())
			w := httptest.NewRecorder()
			(&App{}).handleDocument(w, req)
			if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.want) {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
		})
	}
}
