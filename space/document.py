# -*- coding: utf-8 -*-
"""Text from a .docx or PDF for the draft box. A port of the desktop app's importer
(app/internal/launcher/document.go and document_pdf.go), same rules:

* .docx: the text runs of word/document.xml; a blank line between paragraphs (the drafts the model was
  trained on look like that), one line per table row with tab-separated cells.
* PDF: pdfminer.six parses the file and decodes the text (ToUnicode first, which keeps Chinese PDFs
  printed from Chrome readable); the layout is ours. Glyphs on one baseline make a line; a larger line
  gap, a change of font size or a short line that ends a sentence starts a new paragraph; a short line
  that does not end a sentence (a sign-off, an address, a list item) keeps its line break; English lines
  inside a paragraph are joined with a space (a trailing hyphen joins directly), Chinese lines directly.
* Code points that look the same but differ are fixed: radicals Chrome writes for common hanzi
  (U+2F00 -> 一, U+2ECB -> 车), ligatures (ﬁ -> fi), soft hyphens. Text that is still mostly control,
  replacement or private-use characters is rejected instead of being loaded into the draft.

Nothing is written to disk here: the caller hands in bytes and gets text or a DocError back.
"""
from __future__ import annotations

import io
import logging
import math
import re
import unicodedata
import zipfile
import xml.etree.ElementTree as ET

MAX_BYTES = 10 * 1024 * 1024   # upload limit on this page (the app takes 20 MB; the Space reads on a shared CPU)
MAX_TEXT = 2 * 1024 * 1024     # extracted text limit, as in the app
MAX_PAGES = 100                # PDF pages read (the app: 500); one run here takes about 2,100 words anyway


logging.getLogger("pdfminer").setLevel(logging.ERROR)   # it warns on every font without a FontBBox


class DocError(Exception):
    """code: invalid (not .docx/.pdf) | large (file) | long (text) | pages | empty | unreadable | garbled"""

    def __init__(self, code: str):
        super().__init__(code)
        self.code = code


def document_text(data: bytes, name: str) -> str:
    """The text of a .docx or .pdf file, stripped; DocError when it can't be read."""
    ext = name.lower().rsplit(".", 1)[-1] if "." in name else ""
    if ext not in ("docx", "pdf"):
        raise DocError("invalid")
    if len(data) > MAX_BYTES:
        raise DocError("large")
    try:
        text = _docx_text(data) if ext == "docx" else _pdf_text(data)
    except DocError:
        raise
    except Exception:  # malformed files raise all kinds of things inside the parsers
        raise DocError("unreadable")
    text = text.strip()
    if not text:
        raise DocError("empty")
    return text


# ───────────────────────────────────────────────────────────── .docx

_W = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"


def _docx_text(data: bytes) -> str:
    with zipfile.ZipFile(io.BytesIO(data)) as z:
        try:
            info = z.getinfo("word/document.xml")
        except KeyError:
            raise DocError("unreadable")
        if info.file_size > MAX_TEXT:
            raise DocError("long")
        with z.open(info) as fh:
            raw = fh.read(MAX_TEXT + 1)
    if len(raw) > MAX_TEXT:
        raise DocError("long")
    if b"<!DOCTYPE" in raw or b"<!ENTITY" in raw:   # Word never writes these; refuse entity tricks outright
        raise DocError("unreadable")
    out, cells = [], 0   # cells: depth inside table cells (a cell's paragraphs are joined with a space)
    for event, el in ET.iterparse(io.BytesIO(raw), events=("start", "end")):
        if not el.tag.startswith(_W):
            continue
        tag = el.tag[len(_W):]
        if event == "start":
            if tag == "tab":
                out.append("\t")
            elif tag in ("br", "cr"):
                out.append("\n")
            elif tag == "noBreakHyphen":
                out.append("-")
            elif tag == "tc":
                cells += 1
        else:
            if tag == "t":
                out.append(el.text or "")
            elif tag == "p":
                out.append(" " if cells > 0 else "\n\n")
            elif tag == "tc":
                cells -= 1
                out.append("\t")
            elif tag == "tr":
                out.append("\n")
            elif tag == "tbl":
                out.append("\n")
            el.clear()
    return _tidy("".join(out))


_TRAIL_WS = re.compile(r"[ \t]+\n")
_SPACE_TAB = re.compile(r" +\t")
_MANY_BLANK = re.compile(r"\n{3,}")


def _tidy(s: str) -> str:
    s = _TRAIL_WS.sub("\n", s)
    s = _SPACE_TAB.sub("\t", s)
    s = _MANY_BLANK.sub("\n\n", s)
    return normalize_runes(s)


# ───────────────────────────────────────────────────────────── PDF

def _pdf_text(data: bytes) -> str:
    from pdfminer.pdfdevice import PDFTextDevice
    from pdfminer.pdfdocument import PDFDocument
    from pdfminer.pdffont import PDFUnicodeNotDefined
    from pdfminer.pdfinterp import PDFPageInterpreter, PDFResourceManager
    from pdfminer.pdfpage import PDFPage
    from pdfminer.pdfparser import PDFParser
    from pdfminer.utils import apply_matrix_pt

    class Glyphs(PDFTextDevice):
        """Collects (x, y, end_x, size, text) per glyph in content-stream order."""

        def __init__(self, rsrc):
            super().__init__(rsrc)
            self.out = []

        def render_char(self, matrix, font, fontsize, scaling, rise, cid, ncs, graphicstate):
            try:
                text = font.to_unichr(cid)
            except PDFUnicodeNotDefined:
                text = "\ufffd" if font.is_multibyte() else ""   # a code with no text: counted as garbled
            except Exception:
                text = ""
            try:
                adv = font.char_width(cid) * fontsize * scaling
            except Exception:
                adv = 0.0
            x, y = apply_matrix_pt(matrix, (0, rise))
            end, _ = apply_matrix_pt(matrix, (adv, rise))
            if text:
                self.out.append((x, y, end, abs(fontsize) * math.hypot(matrix[2], matrix[3]), text))
            return adv

    doc = PDFDocument(PDFParser(io.BytesIO(data)))
    rsrc = PDFResourceManager(caching=True)
    pages, size = [], 0
    for n, page in enumerate(PDFPage.create_pages(doc), 1):
        if n > MAX_PAGES:
            raise DocError("pages")
        dev = Glyphs(rsrc)
        PDFPageInterpreter(rsrc, dev).process_page(page)
        paras = _page_paragraphs(_page_lines(dev.out))
        size += sum(len(p) + 2 for p in paras)
        if size > MAX_TEXT:
            raise DocError("long")
        pages.append(paras)
    text = _join_pages(pages)
    if garbled(text):
        raise DocError("garbled")
    return text


_CJK = re.compile("[\u1100-\u11ff\u2e80-\u2fdf\u3000-\u303f\u3040-\u30ff\u3130-\u318f\u31f0-\u31ff"
                  "\u3400-\u4dbf\u4e00-\u9fff\uac00-\ud7af\uf900-\ufaff\uff00-\uffef\U00020000-\U0003134f]")
SENTENCE_END = ".!?:;。！？：；…”\"'’)）"


def _is_cjk(ch: str) -> bool:
    return bool(ch) and bool(_CJK.match(ch))


def _last(s: str) -> str:
    s = s.rstrip(" ")
    return s[-1] if s else ""


def _first(s: str) -> str:
    s = s.lstrip(" ")
    return s[0] if s else ""


def _page_lines(glyphs):
    """[(y, x0, x1, size, text)]: glyphs on one baseline, left to right as written."""
    lines, cur, buf = [], None, []

    def flush():
        if cur is not None:
            text = "".join(buf).strip()
            if text:
                lines.append((cur[0], cur[1], cur[2], cur[3], text))

    for x, y, end, size, text in glyphs:
        sz = size if size > 0 else 10.0
        if cur is not None and (abs(y - cur[0]) > 0.5 * max(sz, cur[3]) or x < cur[2] - 2 * sz):
            flush()
            cur, buf = None, []
        if cur is None:
            cur = [y, x, end, sz]
        else:
            prev, nxt = _last("".join(buf[-4:])), _first(text)
            gap = x - cur[2]
            if (gap > 0.2 * sz and prev and nxt and not buf[-1].endswith(" ")
                    and not (_is_cjk(prev) and _is_cjk(nxt) and gap < 0.6 * sz)):
                buf.append(" ")
            cur[2] = max(cur[2], end)
            cur[3] = max(cur[3], sz)
        buf.append(text)
    flush()
    return lines


def _join_line(a: str, b: str) -> str:
    pa, nb = _last(a), _first(b)
    if not pa:
        return b
    if _is_cjk(pa) or _is_cjk(nb):   # Chinese lines have no space between them (also mixed line ends)
        return a + b
    if pa == "-" and nb.islower():
        return a + b
    return a + " " + b


def _page_paragraphs(lines):
    if not lines:
        return []
    gaps = [lines[i - 1][0] - l[0] for i, l in enumerate(lines) if i and 0 < lines[i - 1][0] - l[0] < 3 * l[3]]
    lead = sorted(gaps)[len(gaps) // 2] if gaps else 0.0
    min_x, max_x = min(l[1] for l in lines), max(l[2] for l in lines)
    paras, cur = [], lines[0][4]
    for i in range(1, len(lines)):
        prev, l = lines[i - 1], lines[i]
        d = prev[0] - l[0]
        # a line that stops short: ending a sentence = end of paragraph; otherwise a deliberate line break
        short = prev[2] < min_x + 0.8 * (max_x - min_x)
        ends = _last(prev[4]) in SENTENCE_END and _last(prev[4]) != ""
        if d <= 0 or (lead > 0 and d > 1.35 * lead) or abs(l[3] - prev[3]) > 0.15 * prev[3] or (short and ends):
            paras.append(cur)
            cur = l[4]
        elif short:
            cur += "\n" + l[4]
        else:
            cur = _join_line(cur, l[4])
    paras.append(cur)
    return paras


def _join_pages(pages) -> str:
    """A page that ends mid-sentence and a next page that starts in lower case (or with hanzi) are one paragraph."""
    out = []
    for paras in pages:
        for i, p in enumerate(paras):
            if i == 0 and out:
                pr, nr = _last(out[-1]), _first(p)
                if pr not in SENTENCE_END and (nr.islower() or (_is_cjk(pr) and _is_cjk(nr))):
                    out[-1] = _join_line(out[-1], p)
                    continue
            out.append(p)
    return normalize_runes("\n\n".join(out))


# ───────────────────────────────────────────────────────────── code points

# radical glyph -> the equivalent unified ideograph, in pairs (Kangxi radicals U+2F00 and the radicals
# supplement U+2E80), from Unicode's EquivalentUnifiedIdeograph.txt; the same table as the app.
_RADICAL_PAIRS = "⺁厂⺂乛⺃乚⺄乙⺅亻⺆冂⺇𠘨⺈刀⺉刂⺊卜⺋㔾⺌小⺍小⺎兀⺏尣⺐尢⺑𡯂⺒巳⺓幺⺔彑⺕𫜹⺖忄⺗心⺘扌⺙攵⺛旡⺜日⺝月⺞歺⺟母⺠民⺡氵⺢氺⺣灬⺤爫⺥爫⺦丬⺧牛⺨犭⺩王⺪𤴔⺫目⺬示⺭礻⺮𥫗⺯糹⺰纟⺱罓⺲罒⺳㓁⺴冗⺵𦉫⺶羊⺷𦍌⺸𦍋⺹耂⺺肀⺻聿⺼肉⺽𦥑⺾艹⺿艹⻀艹⻁虎⻂衤⻃覀⻄西⻅见⻆角⻇𧢲⻈讠⻉贝⻊𧾷⻋车⻌辶⻍辶⻎辶⻏邑⻐钅⻑長⻒镸⻓长⻔门⻕𨸏⻖阝⻗雨⻘青⻙韦⻚页⻛风⻜飞⻝食⻞𩙿⻟飠⻠饣⻡𩠐⻢马⻣骨⻤鬼⻥鱼⻦鸟⻧卤⻨麦⻩黄⻪黾⻫斉⻬齐⻭歯⻮齿⻯竜⻰龙⻱龜⻲亀⻳龟⼀一⼁丨⼂丶⼃丿⼄乙⼅亅⼆二⼇亠⼈人⼉儿⼊入⼋八⼌冂⼍冖⼎冫⼏几⼐凵⼑刀⼒力⼓勹⼔匕⼕匚⼖匸⼗十⼘卜⼙卩⼚厂⼛厶⼜又⼝口⼞囗⼟土⼠士⼡夂⼢夊⼣夕⼤大⼥女⼦子⼧宀⼨寸⼩小⼪尢⼫尸⼬屮⼭山⼮巛⼯工⼰己⼱巾⼲干⼳幺⼴广⼵廴⼶廾⼷弋⼸弓⼹彐⼺彡⼻彳⼼心⼽戈⼾戶⼿手⽀支⽁攴⽂文⽃斗⽄斤⽅方⽆无⽇日⽈曰⽉月⽊木⽋欠⽌止⽍歹⽎殳⽏毋⽐比⽑毛⽒氏⽓气⽔水⽕火⽖爪⽗父⽘爻⽙爿⽚片⽛牙⽜牛⽝犬⽞玄⽟玉⽠瓜⽡瓦⽢甘⽣生⽤用⽥田⽦疋⽧疒⽨癶⽩白⽪皮⽫皿⽬目⽭矛⽮矢⽯石⽰示⽱禸⽲禾⽳穴⽴立⽵竹⽶米⽷糸⽸缶⽹网⽺羊⽻羽⽼老⽽而⽾耒⽿耳⾀聿⾁肉⾂臣⾃自⾄至⾅臼⾆舌⾇舛⾈舟⾉艮⾊色⾋艸⾌虍⾍虫⾎血⾏行⾐衣⾑襾⾒見⾓角⾔言⾕谷⾖豆⾗豕⾘豸⾙貝⾚赤⾛走⾜足⾝身⾞車⾟辛⾠辰⾡辵⾢邑⾣酉⾤釆⾥里⾦金⾧長⾨門⾩阜⾪隶⾫隹⾬雨⾭靑⾮非⾯面⾰革⾱韋⾲韭⾳音⾴頁⾵風⾶飛⾷食⾸首⾹香⾺馬⾻骨⾼高⾽髟⾾鬥⾿鬯⿀鬲⿁鬼⿂魚⿃鳥⿄鹵⿅鹿⿆麥⿇麻⿈黃⿉黍⿊黑⿋黹⿌黽⿍鼎⿎鼓⿏鼠⿐鼻⿑齊⿒齒⿓龍⿔龜⿕龠"
_FIX = {ord(_RADICAL_PAIRS[i]): _RADICAL_PAIRS[i + 1] for i in range(0, len(_RADICAL_PAIRS) - 1, 2)}
_FIX.update({0xFB00: "ff", 0xFB01: "fi", 0xFB02: "fl", 0xFB03: "ffi", 0xFB04: "ffl", 0xFB05: "st", 0xFB06: "st",
             0x00AD: None})


def normalize_runes(s: str) -> str:
    return s.translate(_FIX)


def garbled(s: str) -> bool:
    """Control, replacement or private-use characters make up 5% or more of the visible characters."""
    total = bad = 0
    for ch in s:
        if ch in "\n\t ":
            continue
        total += 1
        if ch == "\ufffd" or unicodedata.category(ch) == "Cc" or "\ue000" <= ch <= "\uf8ff":
            bad += 1
    return total >= 20 and bad * 20 > total
