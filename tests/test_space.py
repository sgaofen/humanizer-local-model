# -*- coding: utf-8 -*-
"""Tests for the Space's document import and Create fact (space/document.py, space/facts.py).

    python3 -m unittest tests.test_space -v

The cases mirror the desktop app's tests (app/internal/launcher/document*_test.go and the facts part of
app/devtools/webtest.mjs), so the Space and the app read files and keep facts the same way.
The PDF cases need pdfminer.six (space/requirements.txt) and are skipped without it.
"""
import io
import os
import sys
import unittest
import zipfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, os.path.join(ROOT, "space"))

import document as D  # noqa: E402
import facts as F  # noqa: E402

try:
    import pdfminer  # noqa: F401
    HAVE_PDFMINER = True
except ImportError:
    HAVE_PDFMINER = False

TESTDATA = os.path.join(ROOT, "app", "internal", "launcher", "testdata")
W = 'xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"'


def docx(xml: str) -> bytes:
    b = io.BytesIO()
    with zipfile.ZipFile(b, "w") as z:
        z.writestr("word/document.xml", xml)
    return b.getvalue()


def build_pdf(objs):
    b = bytearray(b"%PDF-1.4\n")
    off = []
    for i, o in enumerate(objs):
        off.append(len(b))
        b += f"{i + 1} 0 obj\n{o}\nendobj\n".encode("latin-1")
    x = len(b)
    b += f"xref\n0 {len(objs) + 1}\n0000000000 65535 f \n".encode()
    for o in off:
        b += f"{o:010d} 00000 n \n".encode()
    b += f"trailer\n<< /Size {len(objs) + 1} /Root 1 0 R >>\nstartxref\n{x}\n%%EOF\n".encode()
    return bytes(b)


def stream(s):
    return f"<< /Length {len(s.encode('latin-1'))} >>\nstream\n{s}\nendstream"


def one_page(content, fonts, extra):
    fr = " ".join(f"/{k} {v} 0 R" for k, v in fonts.items())
    n = 3 + len(extra) + 1
    objs = ["<< /Type /Catalog /Pages 2 0 R >>",
            "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
            f"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << {fr} >> >> /Contents {n} 0 R >>"]
    return build_pdf(objs + list(extra) + [stream(content)])


HELV = "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"


class DocxTest(unittest.TestCase):
    def test_runs_tabs_breaks(self):
        got = D.document_text(docx(f'<w:document {W}><w:p><w:r><w:t>Hello &amp; 你好</w:t><w:tab/><w:t>42</w:t><w:br/>'
                                   f'<w:t>Next</w:t></w:r></w:p></w:document>'), "a.docx")
        self.assertEqual(got, "Hello & 你好\t42\nNext")

    def test_paragraphs_and_tables(self):
        xml = (f'<w:document {W}><w:body>'
               '<w:p><w:r><w:t>First paragraph.</w:t></w:r></w:p><w:p></w:p><w:p></w:p><w:p><w:r><w:t>Second</w:t><w:noBreakHyphen/><w:t>one.</w:t></w:r></w:p>'
               '<w:tbl><w:tr><w:tc><w:p><w:r><w:t>File</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Status</w:t></w:r></w:p></w:tc></w:tr>'
               '<w:tr><w:tc><w:p><w:r><w:t>MB-2047</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>High</w:t></w:r></w:p><w:p><w:r><w:t>priority</w:t></w:r></w:p></w:tc></w:tr></w:tbl>'
               '<w:p><w:r><w:t>After ⼀ table.</w:t></w:r></w:p></w:body></w:document>')
        self.assertEqual(D._docx_text(docx(xml)),
                         "First paragraph.\n\nSecond-one.\n\nFile\tStatus\nMB-2047\tHigh priority\n\nAfter 一 table.\n\n")

    def test_rejects(self):
        for name, data, code in [("a.docx", b"bad", "unreadable"), ("a.txt", b"bad", "invalid"),
                                 ("a.docx", docx("<broken>"), "unreadable"),
                                 ("a.docx", docx(f'<!DOCTYPE x [<!ENTITY a "aaaa">]><w:document {W}/>'), "unreadable"),
                                 ("a.docx", b"\0" * (D.MAX_BYTES + 1), "large"),
                                 ("a.docx", docx(f'<w:document {W}><w:p/></w:document>'), "empty")]:
            with self.assertRaises(D.DocError) as cm:
                D.document_text(data, name)
            self.assertEqual(cm.exception.code, code, name)

    def test_upper_case_extension(self):
        self.assertEqual(D.document_text(docx(f'<w:document {W}><w:p><w:r><w:t>Draft 42</w:t></w:r></w:p></w:document>'),
                                         "draft.DOCX"), "Draft 42")


@unittest.skipUnless(HAVE_PDFMINER, "pdfminer.six not installed")
class PdfTest(unittest.TestCase):
    def test_paragraphs(self):
        content = ("BT /F1 12 Tf 72 720 Td (We shipped 42 orders to Denver in March,) Tj 0 -14 Td (and the warehouse team booked the follow-) Tj 0 -14 Td (up call with Diane for Tuesday, March 14.) Tj "
                   "0 -28 Td (Second paragraph starts here.) Tj 0 -14 Td (Best regards,) Tj 0 -14 Td (Jordan Lee) Tj ET")
        self.assertEqual(D.document_text(one_page(content, {"F1": 4}, [HELV]), "a.pdf"),
                         "We shipped 42 orders to Denver in March, and the warehouse team booked the follow-up call with Diane "
                         "for Tuesday, March 14.\n\nSecond paragraph starts here.\n\nBest regards,\nJordan Lee")

    def test_tj_spacing(self):
        got = D.document_text(one_page("BT /F1 12 Tf 72 720 Td [(Hel) -20 (lo) -500 (world)] TJ ET", {"F1": 4}, [HELV]), "a.pdf")
        self.assertEqual(got, "Hello world")

    def test_tounicode_over_differences(self):
        cmap = ("/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CMapName /Adobe-Identity-UCS def\n"
                "1 begincodespacerange <00> <FF> endcodespacerange\n"
                "3 beginbfchar <01> <2F00> <02> <4E2A> <05> <2ECB> endbfchar\n"
                "1 beginbfrange <03> <04> <4E2D> endbfrange\n"
                "endcmap CMapName currentdict /CMap defineresource pop end end")
        font = ("<< /Type /Font /Subtype /Type3 /FontBBox [0 0 1000 1000] /FontMatrix [0.001 0 0 0.001 0 0] /CharProcs << >> /Resources << >> "
                "/Encoding << /Type /Encoding /Differences [1 /g1 /g2 /g3 /g4 /g5] >> /FirstChar 1 /LastChar 5 /Widths [1000 1000 1000 1000 1000] /ToUnicode 5 0 R >>")
        content = "BT /F1 12 Tf 72 720 Td <0102030405> Tj 0 -16 Td <0304> Tj ET"
        self.assertEqual(D.document_text(one_page(content, {"F1": 4}, [font, stream(cmap)]), "a.pdf"), "一个中丮车中丮")

    def test_garbled_rejected(self):
        font = "<< /Type /Font /Subtype /Type0 /BaseFont /X /Encoding /Identity-H /DescendantFonts [5 0 R] >>"
        desc = ("<< /Type /Font /Subtype /CIDFontType2 /BaseFont /X /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) "
                "/Supplement 0 >> /DW 1000 >>")
        content = "BT /F1 12 Tf 72 720 Td <" + "0102" * 30 + "> Tj ET"
        with self.assertRaises(D.DocError) as cm:
            D.document_text(one_page(content, {"F1": 4}, [font, desc]), "scan.pdf")
        self.assertEqual(cm.exception.code, "garbled")

    def test_empty_and_invalid(self):
        for data, code in [(one_page("", {"F1": 4}, [HELV]), "empty"), (b"invalid", "unreadable")]:
            with self.assertRaises(D.DocError) as cm:
                D.document_text(data, "a.pdf")
            self.assertEqual(cm.exception.code, code)

    def test_chrome_files(self):
        for name, want in [
            ("chrome_en.pdf", "Quarterly note\n\nWe shipped 42 orders to Denver in March, and the warehouse in Charlotte grew from 10,000 to 20,000 sq ft. Revenue rose 12% over the same period last year.\n\nNext steps: confirm the follow-up call with Diane on Tuesday, March 14."),
            ("chrome_zh.pdf", "季度小结\n\n2026年9月的一天中午，外卖骑手陈海明骑上电动车，接到一单距离4.8公里的午餐，最终提前两分钟送达。\n\n他说：“我只看见黄灯，没看见一个人。”"),
        ]:
            path = os.path.join(TESTDATA, name)
            if not os.path.exists(path):
                self.skipTest("app testdata not present")
            with open(path, "rb") as fh:
                self.assertEqual(D.document_text(fh.read(), name), want, name)


class FactsTest(unittest.TestCase):
    def test_overlaps_repeats_unicode(self):
        draft = "Nadia approved 42 files. Nadia approved 42 files. 你好世界"
        fs = ["Nadia approved", "approved 42 files", "你好世界"]
        p = F.protect(draft, fs)
        self.assertEqual(len(p.locks), 3)
        self.assertEqual(F.restore(p.text, p), draft)
        out = F.restore(p.text.replace(". ", "! "), p)
        self.assertTrue(all(f in out for f in fs) and out != draft)
        col = F.protect("HZ_LOCK_ literal and 42", ["42"])
        self.assertNotEqual(col.prefix, "HZ_LOCK_")
        self.assertEqual(F.restore(col.text, col), "HZ_LOCK_ literal and 42")
        self.assertEqual(F.active_facts(["42", "42", "gone", "", None], "42 files"), ["42"])
        many = [str(i) for i in range(60)]
        self.assertEqual(len(F.active_facts(many, " ".join(many))), 50)
        tok = p.locks[0][0]
        for bad in [p.text.replace(tok, ""), p.text + tok, p.text.replace(tok, "[[HZ_LOCK_changed]]")]:
            with self.assertRaises(F.FactsLost):
                F.restore(bad, p)
        none = F.protect("ordinary draft", [])
        self.assertEqual(F.restore("ordinary output", none), "ordinary output")

    def test_loose_brackets_and_stream_view(self):
        p = F.protect("Charlotte grows from 10,000 to 20,000 sq ft. Call Nadia.", ["20,000 sq ft", "Nadia"])
        self.assertEqual(F.restore("Charlotte: [HZ_LOCK_0] SF. Ping HZ_LOCK_1 today.", p),
                         "Charlotte: 20,000 sq ft SF. Ping Nadia today.")
        with self.assertRaises(F.FactsLost):
            F.restore("[HZ_LOCK_0] and [[HZ_LOCK_0]], [[HZ_LOCK_1]]", p)
        self.assertEqual(F.stream_view("to [[HZ_LOCK_0]] and [[HZ_LO", p), "to 20,000 sq ft and ")
        self.assertEqual(F.stream_view("ping HZ_", p), "ping ")

    def test_per_part_check(self):
        p = F.protect("Alpha 42.\n\nBeta 7.", ["42", "7"])
        parts = p.text.split("\n\n")
        self.assertEqual(p.indices_in(parts[0]), [0])
        self.assertEqual(F.restore("A [[HZ_LOCK_0]].", p, only=[0]), "A 42.")
        with self.assertRaises(F.FactsLost):   # another part's placeholder showing up is a failure too
            F.restore("A [[HZ_LOCK_0]] [[HZ_LOCK_1]].", p, only=[0])

    def test_parse(self):
        self.assertEqual(F.parse('["a", 1, "b"]'), ["a", "b"])
        for raw in ["", "{}", "not json", None, 5]:
            self.assertEqual(F.parse(raw), [])


if __name__ == "__main__":
    unittest.main()
