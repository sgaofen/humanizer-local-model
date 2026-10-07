package launcher

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

const maxDocumentBytes = 20 << 20
const maxDocumentText = 2 << 20

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
					case "p":
						b.WriteByte('\n')
					case "tc":
						b.WriteByte('\t')
					}
				}
			}
			return b.String(), nil
		}
		return "", errors.New("missing document.xml")
	case ".pdf":
		r, e := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
		if e != nil {
			return "", e
		}
		if r.NumPage() > 500 {
			return "", errors.New("too many pages")
		}
		var b strings.Builder
		for i := 1; i <= r.NumPage(); i++ {
			p := r.Page(i)
			s, e := p.GetPlainText(nil)
			if e != nil {
				return "", e
			}
			if b.Len()+len(s)+2 > maxDocumentText {
				return "", errors.New("document too large")
			}
			b.WriteString(s)
			b.WriteString("\n\n")
		}
		return b.String(), nil
	default:
		return "", errors.New("unsupported document")
	}
}
