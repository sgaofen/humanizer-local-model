package launcher

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
)

const maxDocumentBytes = 20 << 20
const maxDocumentText = 2 << 20 // 提取出的文字上限(PDF 另有 500 页上限)

// Documents stay in memory and are never saved to the application's data directory.
func (a *App) handleDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxDocumentBytes+(1<<20))
	if err := r.ParseMultipartForm(maxDocumentBytes + (1 << 20)); err != nil {
		writeJSON(w, 400, map[string]any{"error": "uploadInvalid"})
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": "uploadInvalid"})
		return
	}
	defer file.Close()
	if header.Size > maxDocumentBytes {
		writeJSON(w, 413, map[string]any{"error": "uploadLarge"})
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, maxDocumentBytes+1))
	if err != nil || len(data) > maxDocumentBytes {
		writeJSON(w, 413, map[string]any{"error": "uploadLarge"})
		return
	}
	text, err := documentText(data, strings.ToLower(filepath.Ext(header.Filename)))
	if errors.Is(err, errGarbled) {
		writeJSON(w, 422, map[string]any{"error": "uploadGarbled"})
		return
	}
	if err != nil {
		writeJSON(w, 422, map[string]any{"error": "uploadUnreadable"})
		return
	}
	if strings.TrimSpace(text) == "" {
		writeJSON(w, 422, map[string]any{"error": "uploadEmpty"})
		return
	}
	writeJSON(w, 200, map[string]any{"text": strings.TrimSpace(text)})
}

func documentText(data []byte, ext string) (text string, err error) {
	// Malformed PDF objects can panic in the parser. Keep the local HTTP service alive.
	defer func() {
		if recover() != nil {
			text = ""
			err = errors.New("invalid document")
		}
	}()
	switch ext {
	case ".docx":
		z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return "", e
		}
		for _, f := range z.File {
			if f.Name != "word/document.xml" {
				continue
			}
			if f.UncompressedSize64 > maxDocumentText {
				return "", errors.New("document too large")
			}
			r, e := f.Open()
			if e != nil {
				return "", e
			}
			defer r.Close()
			raw, e := io.ReadAll(io.LimitReader(r, maxDocumentText+1))
			if e != nil || len(raw) > maxDocumentText {
				return "", errors.New("document too large")
			}
			d := xml.NewDecoder(bytes.NewReader(raw))
			var b strings.Builder
			inText := false
			cells := 0 // 在几层表格单元格里:单元格里的段落用空格接,一行表格一行文字
			const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
			for {
				tok, e := d.Token()
				if e == io.EOF {
					break
				}
				if e != nil {
					return "", e
				}
				switch t := tok.(type) {
				case xml.StartElement:
					if t.Name.Space != wordNS {
						continue
					}
					switch t.Name.Local {
					case "t":
						inText = true
					case "tab":
						b.WriteByte('\t')
					case "br", "cr":
						b.WriteByte('\n')
					case "noBreakHyphen":
						b.WriteByte('-')
					case "tc":
						cells++
					}
				case xml.CharData:
					if inText {
						b.Write(t)
					}
				case xml.EndElement:
					if t.Name.Space != wordNS {
						continue
					}
					switch t.Name.Local {
					case "t":
						inText = false
					case "p": // 段落之间空一行,和模型训练时草稿的分段一致
						if cells > 0 {
							b.WriteByte(' ')
						} else {
							b.WriteString("\n\n")
						}
					case "tc":
						cells--
						b.WriteByte('\t')
					case "tr":
						b.WriteByte('\n')
					case "tbl":
						b.WriteString("\n")
					}
				}
			}
			return tidyText(b.String()), nil
		}
		return "", errors.New("missing document.xml")
	case ".pdf":
		return pdfText(data)
	default:
		return "", errors.New("unsupported document")
	}
}

var (
	reTrailWS   = regexp.MustCompile(`[ \t]+\n`)
	reSpaceTab  = regexp.MustCompile(` +\t`)
	reManyBlank = regexp.MustCompile(`\n{3,}`)
)

// tidyText:去掉行尾空白(表格单元格留下的 Tab / 空格),连续空行压成一个。
func tidyText(s string) string {
	s = reTrailWS.ReplaceAllString(s, "\n")
	s = reSpaceTab.ReplaceAllString(s, "\t")
	s = reManyBlank.ReplaceAllString(s, "\n\n")
	return normalizeRunes(s)
}
