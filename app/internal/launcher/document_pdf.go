package launcher

// PDF 文字提取。github.com/ledongthuc/pdf 只负责解析文件结构、解压内容流;
// 解码和排版在这里自己做,因为库自带的 GetPlainText 有两个问题会直接坏掉改写:
//
//  1. 解码:库只在 Encoding 是 Identity-H 或缺省时才用 ToUnicode。Chrome / Skia 打印的中文 PDF
//     用 Type3 字体 + Differences 编码 + ToUnicode,结果整篇乱码。这里只要字体带 ToUnicode 就用它。
//  2. 排版:库按文字对象输出,一行一个换行,英文段落被切成一行一行,模型会把每行当成独立的句子。
//     这里按文字矩阵算出每个字的坐标,同一基线拼成一行;行距明显变大、字号变化、上一行是短句收尾才算新段落;
//     段内英文行用空格接回(行尾连字符直接接上),中文行直接接上。
//
// 另外修两类「看着一样、码位不同」的字:Chrome 把部分汉字映射成部首码位(⼀ U+2F00 → 一、⻋ U+2ECB → 车),
// 连字 ﬁ → fi。最后数一下控制字符、替换符、私用区字符,太多就当读不出来,不把乱码塞进草稿。

import (
	"bytes"
	"errors"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/ledongthuc/pdf"
)

const maxPDFPages = 500

var errGarbled = errors.New("garbled text")

func pdfText(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	n := r.NumPage()
	if n > maxPDFPages {
		return "", errors.New("too many pages")
	}
	var pages [][]string
	size := 0
	for i := 1; i <= n; i++ {
		paras := pageParagraphs(r.Page(i))
		for _, p := range paras {
			size += len(p) + 2
		}
		if size > maxDocumentText {
			return "", errors.New("document too large")
		}
		pages = append(pages, paras)
	}
	text := joinPages(pages)
	if garbled(text) {
		return "", errGarbled
	}
	return text, nil
}

// ───────────────────────── 字体:编码 → 文字 + 宽度 ─────────────────────────

type cmapSpace struct {
	lo, hi uint32
	n      int
}

type cmapRange struct {
	lo, hi uint32
	n      int
	base   []uint16 // 目标是一个 UTF-16 串:最后一个码元按偏移递增
	arr    []string // 目标是数组:逐个对应
}

type toUnicodeMap struct {
	spaces []cmapSpace
	chars  map[uint64]string
	ranges []cmapRange
}

type pdfFont struct {
	twoByte bool          // Type0 字体,没有码空间时按 2 字节一个码
	uni     *toUnicodeMap // ToUnicode,有就优先用
	enc     pdf.TextEncoding
	widths  map[uint32]float64 // 字宽,单位 = 字号的千分之一
	dw      float64            // 默认字宽
}

var (
	reCodespace = regexp.MustCompile(`(?s)begincodespacerange(.*?)endcodespacerange`)
	reBfchar    = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	reBfrange   = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	reHexTok    = regexp.MustCompile(`<([0-9A-Fa-f\s]*)>|\[|\]`)
)

func hexBytes(s string) []byte {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	if len(s)%2 == 1 {
		s += "0"
	}
	b := make([]byte, len(s)/2)
	for i := range b {
		v, err := strconv.ParseUint(s[2*i:2*i+2], 16, 8)
		if err != nil {
			return nil
		}
		b[i] = byte(v)
	}
	return b
}

func beUint(b []byte) uint32 {
	var v uint32
	for _, c := range b {
		v = v<<8 | uint32(c)
	}
	return v
}

func utf16BE(b []byte) []uint16 {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
	}
	return u
}

// parseToUnicode 读 ToUnicode CMap 里的 codespacerange / bfchar / bfrange(足够覆盖实际 PDF 里的写法)。
func parseToUnicode(src string) *toUnicodeMap {
	m := &toUnicodeMap{chars: map[uint64]string{}}
	tokens := func(block string) []string { // "<..>" 的十六进制内容,数组用 "[" "]" 标出
		var out []string
		for _, t := range reHexTok.FindAllStringSubmatch(block, -1) {
			if t[0] == "[" || t[0] == "]" {
				out = append(out, t[0])
			} else {
				out = append(out, "<"+t[1])
			}
		}
		return out
	}
	for _, blk := range reCodespace.FindAllStringSubmatch(src, -1) {
		t := tokens(blk[1])
		for i := 0; i+1 < len(t); i += 2 {
			lo, hi := hexBytes(t[i][1:]), hexBytes(t[i+1][1:])
			if len(lo) == 0 || len(lo) != len(hi) || len(lo) > 4 {
				continue
			}
			m.spaces = append(m.spaces, cmapSpace{beUint(lo), beUint(hi), len(lo)})
		}
	}
	for _, blk := range reBfchar.FindAllStringSubmatch(src, -1) {
		t := tokens(blk[1])
		for i := 0; i+1 < len(t); i += 2 {
			if t[i] == "[" || t[i] == "]" || t[i+1] == "[" || t[i+1] == "]" {
				continue
			}
			src, dst := hexBytes(t[i][1:]), hexBytes(t[i+1][1:])
			if len(src) == 0 || len(src) > 4 {
				continue
			}
			m.chars[uint64(len(src))<<32|uint64(beUint(src))] = string(utf16.Decode(utf16BE(dst)))
		}
	}
	for _, blk := range reBfrange.FindAllStringSubmatch(src, -1) {
		t := tokens(blk[1])
		for i := 0; i+2 < len(t); {
			lo, hi := hexBytes(t[i][1:]), hexBytes(t[i+1][1:])
			if len(lo) == 0 || len(lo) != len(hi) || len(lo) > 4 {
				break
			}
			r := cmapRange{lo: beUint(lo), hi: beUint(hi), n: len(lo)}
			if t[i+2] == "[" {
				j := i + 3
				for ; j < len(t) && t[j] != "]"; j++ {
					r.arr = append(r.arr, string(utf16.Decode(utf16BE(hexBytes(t[j][1:])))))
				}
				i = j + 1
			} else {
				r.base = utf16BE(hexBytes(t[i+2][1:]))
				i += 3
			}
			m.ranges = append(m.ranges, r)
		}
	}
	if len(m.chars) == 0 && len(m.ranges) == 0 {
		return nil
	}
	return m
}

func (m *toUnicodeMap) lookup(code uint32, n int) (string, bool) {
	if s, ok := m.chars[uint64(n)<<32|uint64(code)]; ok {
		return s, true
	}
	for _, r := range m.ranges {
		if r.n != n || code < r.lo || code > r.hi {
			continue
		}
		off := code - r.lo
		if r.arr != nil {
			if int(off) < len(r.arr) {
				return r.arr[off], true
			}
			return "", false
		}
		if len(r.base) == 0 {
			return "", false
		}
		u := append([]uint16(nil), r.base...)
		u[len(u)-1] += uint16(off)
		return string(utf16.Decode(u)), true
	}
	return "", false
}

func streamString(v pdf.Value) string {
	if v.Kind() != pdf.Stream {
		return ""
	}
	rd := v.Reader()
	defer rd.Close()
	b, _ := io.ReadAll(io.LimitReader(rd, 4<<20))
	return string(b)
}

func loadFont(f pdf.Font) *pdfFont {
	pf := &pdfFont{widths: map[uint32]float64{}, dw: 0}
	sub := f.V.Key("Subtype").Name()
	if tu := streamString(f.V.Key("ToUnicode")); tu != "" {
		pf.uni = parseToUnicode(tu)
	}
	if pf.uni == nil && sub != "Type0" {
		pf.enc = simpleEncoder(f)
	}
	switch sub {
	case "Type0":
		pf.twoByte = true
		pf.dw = 1000
		d := f.V.Key("DescendantFonts").Index(0)
		if v := d.Key("DW"); v.Kind() == pdf.Integer || v.Kind() == pdf.Real {
			pf.dw = v.Float64()
		}
		w := d.Key("W")
		for i := 0; i < w.Len(); {
			c := w.Index(i)
			if i+1 >= w.Len() {
				break
			}
			if next := w.Index(i + 1); next.Kind() == pdf.Array {
				for j := 0; j < next.Len(); j++ {
					pf.widths[uint32(c.Int64())+uint32(j)] = next.Index(j).Float64()
				}
				i += 2
			} else if i+2 < w.Len() {
				for k := c.Int64(); k <= next.Int64() && k-c.Int64() < 65536; k++ {
					pf.widths[uint32(k)] = w.Index(i + 2).Float64()
				}
				i += 3
			} else {
				break
			}
		}
	default:
		scale := 1.0
		if sub == "Type3" { // Type3 的字宽在字形空间里,要乘 FontMatrix 换回千分之一字号
			if fm := f.V.Key("FontMatrix"); fm.Len() >= 1 && fm.Index(0).Float64() != 0 {
				scale = fm.Index(0).Float64() * 1000
			}
		}
		first := f.V.Key("FirstChar").Int64()
		ws := f.V.Key("Widths")
		for i := 0; i < ws.Len(); i++ {
			pf.widths[uint32(first)+uint32(i)] = ws.Index(i).Float64() * scale
		}
		pf.dw = f.V.Key("FontDescriptor").Key("MissingWidth").Float64() * scale
		if ws.Len() == 0 && pf.dw == 0 {
			pf.dw = 500 // 标准 14 字体可以不带字宽表:按半个字号估,只用来判断字与字之间有没有空隙
		}
	}
	return pf
}

// simpleEncoder:单字节字体没有 ToUnicode 时用库自带的编码表(WinAnsi / MacRoman / Differences);
// 库在解析坏掉的编码时会 panic,这里兜住,退回不解码。
func simpleEncoder(f pdf.Font) (enc pdf.TextEncoding) {
	defer func() {
		if recover() != nil {
			enc = nil
		}
	}()
	return f.Encoder()
}

type glyph struct {
	text  string
	width float64 // 千分之一字号
	space bool    // 单字节 32,吃 Tw
}

// decode 把一段原始字节拆成码,逐个给出文字和宽度。
func (pf *pdfFont) decode(raw string) []glyph {
	var out []glyph
	b := []byte(raw)
	for i := 0; i < len(b); {
		n := 0
		if pf.uni != nil && len(pf.uni.spaces) > 0 {
			for k := 1; k <= 4 && i+k <= len(b) && n == 0; k++ {
				code := beUint(b[i : i+k])
				for _, s := range pf.uni.spaces {
					if s.n == k && code >= s.lo && code <= s.hi {
						n = k
						break
					}
				}
			}
		}
		if n == 0 {
			n = 1
			if pf.twoByte && i+1 < len(b) {
				n = 2
			}
		}
		code := beUint(b[i : i+n])
		g := glyph{space: n == 1 && code == 32}
		if w, ok := pf.widths[code]; ok {
			g.width = w
		} else {
			g.width = pf.dw
		}
		if pf.uni != nil {
			if s, ok := pf.uni.lookup(code, n); ok {
				g.text = s
			}
		}
		if g.text == "" && pf.enc != nil && !pf.twoByte {
			g.text = pf.enc.Decode(string(b[i : i+n]))
		}
		if g.text == "" && pf.twoByte && pf.uni == nil {
			g.text = "�"
		}
		out = append(out, g)
		i += n
	}
	return out
}

// ───────────────────────── 内容流 → 带坐标的字 ─────────────────────────

type mat [6]float64 // a b c d e f

func (m mat) mul(n mat) mat { // m × n(PDF 的行向量约定)
	return mat{
		m[0]*n[0] + m[1]*n[2], m[0]*n[1] + m[1]*n[3],
		m[2]*n[0] + m[3]*n[2], m[2]*n[1] + m[3]*n[3],
		m[4]*n[0] + m[5]*n[2] + n[4], m[4]*n[1] + m[5]*n[3] + n[5],
	}
}

var identity = mat{1, 0, 0, 1, 0, 0}

type placed struct {
	x, y, end, size float64
	text            string
	gap             bool // TJ 里有明显的右移(LaTeX 之类不写空格字形,靠这个分词)
}

type textState struct {
	ctm, tm, tlm               mat
	tfs, th, tc, tw, tl, trise float64
	font                       *pdfFont
}

func pageGlyphs(p pdf.Page) []placed {
	if p.V.IsNull() || p.V.Key("Contents").Kind() == pdf.Null {
		return nil
	}
	fonts := map[string]*pdfFont{}
	fontOf := func(name string) *pdfFont {
		if f, ok := fonts[name]; ok {
			return f
		}
		f := loadFont(p.Font(name))
		fonts[name] = f
		return f
	}
	g := textState{ctm: identity, tm: identity, tlm: identity, th: 1}
	var stack []textState
	var out []placed
	pendingGap := false
	show := func(raw string) {
		if g.font == nil {
			return
		}
		for _, gl := range g.font.decode(raw) {
			trm := mat{g.tfs * g.th, 0, 0, g.tfs, 0, g.trise}.mul(g.tm).mul(g.ctm)
			adv := (gl.width/1000*g.tfs + g.tc) * g.th
			if gl.space {
				adv += g.tw * g.th
			}
			size := math.Hypot(trm[2], trm[3])
			endX := trm[4] + adv*math.Hypot(g.tm[0], g.tm[1])*math.Hypot(g.ctm[0], g.ctm[1])
			if gl.text != "" {
				out = append(out, placed{x: trm[4], y: trm[5], end: endX, size: size, text: gl.text, gap: pendingGap})
				pendingGap = false
			}
			g.tm = mat{1, 0, 0, 1, adv, 0}.mul(g.tm)
		}
	}
	num := func(v pdf.Value) float64 { return v.Float64() }
	pdf.Interpret(p.V.Key("Contents"), func(stk *pdf.Stack, op string) {
		n := stk.Len()
		args := make([]pdf.Value, n)
		for i := n - 1; i >= 0; i-- {
			args[i] = stk.Pop()
		}
		switch op {
		case "q":
			stack = append(stack, g)
		case "Q":
			if len(stack) > 0 {
				g = stack[len(stack)-1]
				stack = stack[:len(stack)-1]
			}
		case "cm":
			if n == 6 {
				g.ctm = mat{num(args[0]), num(args[1]), num(args[2]), num(args[3]), num(args[4]), num(args[5])}.mul(g.ctm)
			}
		case "BT":
			g.tm, g.tlm = identity, identity
		case "Tf":
			if n == 2 {
				g.font = fontOf(args[0].Name())
				g.tfs = num(args[1])
			}
		case "Tc":
			if n == 1 {
				g.tc = num(args[0])
			}
		case "Tw":
			if n == 1 {
				g.tw = num(args[0])
			}
		case "Tz":
			if n == 1 {
				g.th = num(args[0]) / 100
			}
		case "TL":
			if n == 1 {
				g.tl = num(args[0])
			}
		case "Ts":
			if n == 1 {
				g.trise = num(args[0])
			}
		case "Td", "TD":
			if n == 2 {
				if op == "TD" {
					g.tl = -num(args[1])
				}
				g.tlm = mat{1, 0, 0, 1, num(args[0]), num(args[1])}.mul(g.tlm)
				g.tm = g.tlm
			}
		case "Tm":
			if n == 6 {
				g.tlm = mat{num(args[0]), num(args[1]), num(args[2]), num(args[3]), num(args[4]), num(args[5])}
				g.tm = g.tlm
			}
		case "T*":
			g.tlm = mat{1, 0, 0, 1, 0, -g.tl}.mul(g.tlm)
			g.tm = g.tlm
		case "Tj":
			if n == 1 {
				show(args[0].RawString())
			}
		case "'", "\"":
			if op == "\"" && n == 3 {
				g.tw, g.tc = num(args[0]), num(args[1])
				args = args[2:]
			}
			g.tlm = mat{1, 0, 0, 1, 0, -g.tl}.mul(g.tlm)
			g.tm = g.tlm
			if len(args) == 1 {
				show(args[0].RawString())
			}
		case "TJ":
			if n != 1 {
				return
			}
			a := args[0]
			for i := 0; i < a.Len(); i++ {
				v := a.Index(i)
				if v.Kind() == pdf.String {
					show(v.RawString())
				} else {
					d := v.Float64()
					g.tm = mat{1, 0, 0, 1, -d / 1000 * g.tfs * g.th, 0}.mul(g.tm)
					if d < -200 {
						pendingGap = true
					}
				}
			}
		}
	})
	return out
}

// ───────────────────────── 字 → 行 → 段落 ─────────────────────────

type pline struct {
	y, x0, x1, size float64
	text            string
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r) || (r >= 0x3000 && r <= 0x303F) || (r >= 0xFF00 && r <= 0xFFEF)
}

func lastRune(s string) rune {
	s = strings.TrimRight(s, " ")
	if s == "" {
		return 0
	}
	r := []rune(s)
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		if r != ' ' {
			return r
		}
	}
	return 0
}

func pageLines(gs []placed) []pline {
	var lines []pline
	var cur *pline
	var b strings.Builder
	flush := func() {
		if cur != nil {
			cur.text = strings.TrimSpace(b.String())
			if cur.text != "" {
				lines = append(lines, *cur)
			}
		}
		cur = nil
		b.Reset()
	}
	for _, g := range gs {
		sz := g.size
		if sz <= 0 {
			sz = 10
		}
		if cur != nil && (math.Abs(g.y-cur.y) > 0.5*math.Max(sz, cur.size) || g.x < cur.x1-2*sz) {
			flush()
		}
		if cur == nil {
			cur = &pline{y: g.y, x0: g.x, x1: g.end, size: sz}
		} else {
			prev, next := lastRune(b.String()), firstRune(g.text)
			gap := g.x - cur.x1
			wide := g.gap || gap > 0.2*sz
			if wide && prev != 0 && prev != ' ' && next != ' ' && !(isCJK(prev) && isCJK(next) && !g.gap && gap < 0.6*sz) {
				b.WriteByte(' ')
			}
			if g.end > cur.x1 {
				cur.x1 = g.end
			}
			if sz > cur.size {
				cur.size = sz
			}
		}
		b.WriteString(g.text)
	}
	flush()
	return lines
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

const sentenceEnd = ".!?:;。！？：；…”\"'’)）"

func joinLine(a, b string) string {
	pa, nb := lastRune(a), firstRune(b)
	switch {
	case pa == 0:
		return b
	case isCJK(pa) || isCJK(nb): // 中文行间本来就没有空格(含中英混排的行尾/行首)
		return a + b
	case pa == '-' && unicode.IsLower(nb):
		return a + b
	default:
		return a + " " + b
	}
}

func pageParagraphs(p pdf.Page) []string {
	lines := pageLines(pageGlyphs(p))
	if len(lines) == 0 {
		return nil
	}
	var gaps []float64
	minX, maxX := lines[0].x0, lines[0].x1
	for i, l := range lines {
		minX, maxX = math.Min(minX, l.x0), math.Max(maxX, l.x1)
		if i > 0 {
			if d := lines[i-1].y - l.y; d > 0 && d < 3*l.size {
				gaps = append(gaps, d)
			}
		}
	}
	lead := median(gaps)
	var paras []string
	cur := lines[0].text
	for i := 1; i < len(lines); i++ {
		prev, l := lines[i-1], lines[i]
		d := prev.y - l.y
		short := prev.x1 < minX+0.8*(maxX-minX) && strings.ContainsRune(sentenceEnd, lastRune(prev.text))
		newPara := d <= 0 || (lead > 0 && d > 1.35*lead) || math.Abs(l.size-prev.size) > 0.15*prev.size || short
		if newPara {
			paras = append(paras, cur)
			cur = l.text
		} else {
			cur = joinLine(cur, l.text)
		}
	}
	return append(paras, cur)
}

// 页与页之间:上一页最后一段没写完(不以句末标点收尾),下一页又以小写字母或汉字开头,就接成同一段。
func joinPages(pages [][]string) string {
	var out []string
	for _, ps := range pages {
		for i, p := range ps {
			if i == 0 && len(out) > 0 {
				last := out[len(out)-1]
				pr, nr := lastRune(last), firstRune(p)
				if !strings.ContainsRune(sentenceEnd, pr) && (unicode.IsLower(nr) || (isCJK(pr) && isCJK(nr))) {
					out[len(out)-1] = joinLine(last, p)
					continue
				}
			}
			out = append(out, p)
		}
	}
	return normalizeRunes(strings.Join(out, "\n\n"))
}

// ───────────────────────── 码位修正与乱码检查 ─────────────────────────

// radicalPairs:部首字形 → 等价的统一汉字,成对排列(康熙部首 U+2F00 段 + 部首补充 U+2E80 段)。
// 由 Unicode EquivalentUnifiedIdeograph.txt 生成;Chrome 打印的 PDF 会把「一 月 车 见 黄」这类字写成部首码位。
const radicalPairs = "⺁厂⺂乛⺃乚⺄乙⺅亻⺆冂⺇𠘨⺈刀⺉刂⺊卜⺋㔾⺌小⺍小⺎兀⺏尣⺐尢⺑𡯂⺒巳⺓幺⺔彑⺕𫜹⺖忄⺗心⺘扌⺙攵⺛旡⺜日⺝月⺞歺⺟母⺠民⺡氵⺢氺⺣灬⺤爫⺥爫⺦丬⺧牛⺨犭⺩王⺪𤴔⺫目⺬示⺭礻⺮𥫗⺯糹⺰纟⺱罓⺲罒⺳㓁⺴冗⺵𦉫⺶羊⺷𦍌⺸𦍋⺹耂⺺肀⺻聿⺼肉⺽𦥑⺾艹⺿艹⻀艹⻁虎⻂衤⻃覀⻄西⻅见⻆角⻇𧢲⻈讠⻉贝⻊𧾷⻋车⻌辶⻍辶⻎辶⻏邑⻐钅⻑長⻒镸⻓长⻔门⻕𨸏⻖阝⻗雨⻘青⻙韦⻚页⻛风⻜飞⻝食⻞𩙿⻟飠⻠饣⻡𩠐⻢马⻣骨⻤鬼⻥鱼⻦鸟⻧卤⻨麦⻩黄⻪黾⻫斉⻬齐⻭歯⻮齿⻯竜⻰龙⻱龜⻲亀⻳龟⼀一⼁丨⼂丶⼃丿⼄乙⼅亅⼆二⼇亠⼈人⼉儿⼊入⼋八⼌冂⼍冖⼎冫⼏几⼐凵⼑刀⼒力⼓勹⼔匕⼕匚⼖匸⼗十⼘卜⼙卩⼚厂⼛厶⼜又⼝口⼞囗⼟土⼠士⼡夂⼢夊⼣夕⼤大⼥女⼦子⼧宀⼨寸⼩小⼪尢⼫尸⼬屮⼭山⼮巛⼯工⼰己⼱巾⼲干⼳幺⼴广⼵廴⼶廾⼷弋⼸弓⼹彐⼺彡⼻彳⼼心⼽戈⼾戶⼿手⽀支⽁攴⽂文⽃斗⽄斤⽅方⽆无⽇日⽈曰⽉月⽊木⽋欠⽌止⽍歹⽎殳⽏毋⽐比⽑毛⽒氏⽓气⽔水⽕火⽖爪⽗父⽘爻⽙爿⽚片⽛牙⽜牛⽝犬⽞玄⽟玉⽠瓜⽡瓦⽢甘⽣生⽤用⽥田⽦疋⽧疒⽨癶⽩白⽪皮⽫皿⽬目⽭矛⽮矢⽯石⽰示⽱禸⽲禾⽳穴⽴立⽵竹⽶米⽷糸⽸缶⽹网⽺羊⽻羽⽼老⽽而⽾耒⽿耳⾀聿⾁肉⾂臣⾃自⾄至⾅臼⾆舌⾇舛⾈舟⾉艮⾊色⾋艸⾌虍⾍虫⾎血⾏行⾐衣⾑襾⾒見⾓角⾔言⾕谷⾖豆⾗豕⾘豸⾙貝⾚赤⾛走⾜足⾝身⾞車⾟辛⾠辰⾡辵⾢邑⾣酉⾤釆⾥里⾦金⾧長⾨門⾩阜⾪隶⾫隹⾬雨⾭靑⾮非⾯面⾰革⾱韋⾲韭⾳音⾴頁⾵風⾶飛⾷食⾸首⾹香⾺馬⾻骨⾼高⾽髟⾾鬥⾿鬯⿀鬲⿁鬼⿂魚⿃鳥⿄鹵⿅鹿⿆麥⿇麻⿈黃⿉黍⿊黑⿋黹⿌黽⿍鼎⿎鼓⿏鼠⿐鼻⿑齊⿒齒⿓龍⿔龜⿕龠"

var radicals = func() map[rune]rune {
	r := []rune(radicalPairs)
	m := make(map[rune]rune, len(r)/2)
	for i := 0; i+1 < len(r); i += 2 {
		m[r[i]] = r[i+1]
	}
	return m
}()

var ligatures = map[rune]string{0xFB00: "ff", 0xFB01: "fi", 0xFB02: "fl", 0xFB03: "ffi", 0xFB04: "ffl", 0xFB05: "st", 0xFB06: "st"}

func normalizeRunes(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case radicals[r] != 0:
			b.WriteRune(radicals[r])
		case ligatures[r] != "":
			b.WriteString(ligatures[r])
		case r == 0x00AD: // 软连字符
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// garbled:控制字符、替换符、私用区字符占可见字符的 5% 以上(正常文本几乎是 0)。
func garbled(s string) bool {
	total, bad := 0, 0
	for _, r := range s {
		if r == '\n' || r == '\t' || r == ' ' {
			continue
		}
		total++
		if r == unicode.ReplacementChar || unicode.IsControl(r) || (r >= 0xE000 && r <= 0xF8FF) {
			bad++
		}
	}
	return total >= 20 && bad*20 > total
}
