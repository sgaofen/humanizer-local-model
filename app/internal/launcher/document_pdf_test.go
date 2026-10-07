package launcher

import (
	"bytes"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// buildPDF 拼一个最小 PDF:objs[0] 是 Catalog,其余按顺序编号(1 起)。
func buildPDF(objs []string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	var off []int
	for i, o := range objs {
		off = append(off, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range off {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

func stream(s string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
}

// onePage:一页,字体资源 fonts(名字 → 对象号),内容流放在最后一个对象。
func onePage(content string, fonts map[string]int, extra []string) []byte {
	var fr []string
	for k, v := range fonts {
		fr = append(fr, fmt.Sprintf("/%s %d 0 R", k, v))
	}
	n := 3 + len(extra) + 1
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << %s >> >> /Contents %d 0 R >>", strings.Join(fr, " "), n),
	}
	objs = append(objs, extra...)
	return buildPDF(append(objs, stream(content)))
}

const helv = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"

func TestPDFParagraphs(t *testing.T) {
	// 三行同一段(行距 14,每行接近满宽),空一大段(28)后第二段;段内英文行用空格接回,行尾连字符直接接上;
	// 信末署名这种没写满、又不是句末的短行保留换行。
	content := "BT /F1 12 Tf 72 720 Td (We shipped 42 orders to Denver in March,) Tj 0 -14 Td (and the warehouse team booked the follow-) Tj 0 -14 Td (up call with Diane for Tuesday, March 14.) Tj " +
		"0 -28 Td (Second paragraph starts here.) Tj 0 -14 Td (Best regards,) Tj 0 -14 Td (Jordan Lee) Tj ET"
	got, err := pdfText(onePage(content, map[string]int{"F1": 4}, []string{helv}))
	want := "We shipped 42 orders to Denver in March, and the warehouse team booked the follow-up call with Diane for Tuesday, March 14.\n\nSecond paragraph starts here.\n\nBest regards,\nJordan Lee"
	if err != nil || got != want {
		t.Fatalf("got %q %v", got, err)
	}
	// TJ 里的大间距(LaTeX 不写空格字形)要变成空格,小的字距调整不能拆词。
	got, _ = pdfText(onePage("BT /F1 12 Tf 72 720 Td [(Hel) -20 (lo) -500 (world)] TJ ET", map[string]int{"F1": 4}, []string{helv}))
	if got != "Hello world" {
		t.Fatalf("TJ spacing: %q", got)
	}
}

func TestPDFToUnicodeOverDifferences(t *testing.T) {
	// Chrome 打印中文用的写法:Type3 字体 + Differences 编码 + ToUnicode(库自带的提取在这里出乱码)。
	// 码 01 → 「⼀」(康熙部首 U+2F00,要归一成「一」),02 → 个,03-04 → bfrange,05 → ⻋(部首补充,→ 车)。
	cmap := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CMapName /Adobe-Identity-UCS def\n" +
		"1 begincodespacerange <00> <FF> endcodespacerange\n" +
		"3 beginbfchar <01> <2F00> <02> <4E2A> <05> <2ECB> endbfchar\n" +
		"1 beginbfrange <03> <04> <4E2D> endbfrange\n" +
		"endcmap CMapName currentdict /CMap defineresource pop end end"
	font := "<< /Type /Font /Subtype /Type3 /FontBBox [0 0 1000 1000] /FontMatrix [0.001 0 0 0.001 0 0] /CharProcs << >> /Resources << >> " +
		"/Encoding << /Type /Encoding /Differences [1 /g1 /g2 /g3 /g4 /g5] >> /FirstChar 1 /LastChar 5 /Widths [1000 1000 1000 1000 1000] /ToUnicode 5 0 R >>"
	content := "BT /F1 12 Tf 72 720 Td <0102030405> Tj 0 -16 Td <0304> Tj ET"
	got, err := pdfText(onePage(content, map[string]int{"F1": 4}, []string{font, stream(cmap)}))
	// bfrange <03> <04> <4E2D>:03 → 中,04 → 丮(末位递增);两行同一段,中文行间不加空格
	if err != nil || got != "一个中丮车中丮" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestPDFGarbledRejected(t *testing.T) {
	// Type0 字体没有 ToUnicode:码对不回文字,整页都是替换符 → 拒绝,而不是把乱码塞进草稿。
	font := "<< /Type /Font /Subtype /Type0 /BaseFont /X /Encoding /Identity-H /DescendantFonts [5 0 R] >>"
	desc := "<< /Type /Font /Subtype /CIDFontType2 /BaseFont /X /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> /DW 1000 >>"
	content := "BT /F1 12 Tf 72 720 Td <" + strings.Repeat("0102", 30) + "> Tj ET"
	data := onePage(content, map[string]int{"F1": 4}, []string{font, desc})
	if _, err := pdfText(data); !errors.Is(err, errGarbled) {
		t.Fatalf("want errGarbled, got %v", err)
	}
	var body bytes.Buffer
	m := multipart.NewWriter(&body)
	f, _ := m.CreateFormFile("file", "scan.pdf")
	f.Write(data)
	m.Close()
	req := httptest.NewRequest("POST", "/app/document", &body)
	req.Header.Set("Content-Type", m.FormDataContentType())
	w := httptest.NewRecorder()
	(&App{}).handleDocument(w, req)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "uploadGarbled") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// 真实文件:Chrome「打印为 PDF」的输出(英文 Georgia;中文 PingFang,Type3 + 部首码位)。
func TestPDFChromeFiles(t *testing.T) {
	for _, tc := range []struct{ file, want string }{
		{"testdata/chrome_en.pdf", "Quarterly note\n\nWe shipped 42 orders to Denver in March, and the warehouse in Charlotte grew from 10,000 to 20,000 sq ft. Revenue rose 12% over the same period last year.\n\nNext steps: confirm the follow-up call with Diane on Tuesday, March 14."},
		{"testdata/chrome_zh.pdf", "季度小结\n\n2026年9月的一天中午，外卖骑手陈海明骑上电动车，接到一单距离4.8公里的午餐，最终提前两分钟送达。\n\n他说：“我只看见黄灯，没看见一个人。”"},
	} {
		data, err := os.ReadFile(tc.file)
		if err != nil {
			t.Fatal(err)
		}
		got, err := documentText(data, ".pdf")
		if err != nil || got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q\n err %v", tc.file, got, tc.want, err)
		}
	}
}

func TestDocxParagraphsAndTables(t *testing.T) {
	doc := docxFixture(t, `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`+
		`<w:p><w:r><w:t>First paragraph.</w:t></w:r></w:p><w:p></w:p><w:p></w:p><w:p><w:r><w:t>Second</w:t><w:noBreakHyphen/><w:t>one.</w:t></w:r></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>File</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Status</w:t></w:r></w:p></w:tc></w:tr>`+
		`<w:tr><w:tc><w:p><w:r><w:t>MB-2047</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>High</w:t></w:r></w:p><w:p><w:r><w:t>priority</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`<w:p><w:r><w:t>After ⼀ table.</w:t></w:r></w:p></w:body></w:document>`)
	got, err := documentText(doc, ".docx")
	want := "First paragraph.\n\nSecond-one.\n\nFile\tStatus\nMB-2047\tHigh priority\n\nAfter 一 table.\n\n"
	if err != nil || got != want {
		t.Fatalf("got %q\nwant %q %v", got, want, err)
	}
}
