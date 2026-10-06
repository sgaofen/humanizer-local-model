# -*- coding: utf-8 -*-
"""hz_docx: rewrite the body paragraphs of a .docx file in place (needs python-docx:
pip install "humanize-model[docx]").

* Rewritten one paragraph at a time. Headings (Heading/Title/Subtitle styles or an outline level),
  tables, empty paragraphs, captions, quotes, code styles, the table of contents and everything
  under a References / Bibliography / 参考文献 heading are left alone. Headers, footers, footnotes
  and text boxes are not touched.
* A paragraph that contains anything other than plain text runs (a hyperlink, an image, a field,
  a footnote reference, tracked changes, an equation) is left alone too, so nothing in it is lost.
* The rewrite goes into the paragraph's first run; the other runs are emptied. The paragraph keeps
  its style, numbering and the first run's character formatting, but bold or italic on part of the
  paragraph is lost.
"""
from __future__ import annotations

from collections import Counter
from typing import Dict, List, Tuple

try:
    from . import hz_text as T
except ImportError:  # run from the folder as a plain script
    import hz_text as T


class DocxMissing(RuntimeError):
    pass


def load(path: str):
    try:
        import docx  # python-docx
    except ImportError:
        raise DocxMissing('.docx files need python-docx. Install it with:\n'
                          '  pipx inject humanize-model python-docx      (if you installed hz with pipx)\n'
                          '  pip install "humanize-model[docx] @ git+https://github.com/sgaofen/humanizer-local-model"')
    return docx.Document(path)


def _qn(tag: str) -> str:
    from docx.oxml.ns import qn
    return qn(tag)


_SKIP_STYLE = ('heading', 'title', 'subtitle', 'toc', 'caption', 'quote', 'intense quote', 'code', 'html',
               'macro', 'plain text', 'bibliography', 'footnote', 'endnote', 'table of', 'index', '标题', '题注',
               '目录', '引用', '代码')


def _style_name(p) -> str:
    try:
        st = p.style
        return ((st.name or '') + ' ' + (st.style_id or '')).lower() if st is not None else ''
    except Exception:
        return ''


def _is_heading(p) -> bool:
    name = _style_name(p)
    if any(k in name for k in ('heading', 'title', '标题')):
        return True
    ppr = p._p.pPr
    return ppr is not None and ppr.find(_qn('w:outlineLvl')) is not None


def _is_list(p) -> bool:
    ppr = p._p.pPr
    return 'list' in _style_name(p) or (ppr is not None and ppr.find(_qn('w:numPr')) is not None)


def _plain(p) -> bool:
    """True when the paragraph holds only plain text runs, so writing the rewrite into the first run
    and emptying the others loses nothing but character formatting."""
    ok_child = {_qn('w:pPr'), _qn('w:r'), _qn('w:bookmarkStart'), _qn('w:bookmarkEnd'), _qn('w:proofErr'),
                _qn('w:permStart'), _qn('w:permEnd')}
    ok_run = {_qn('w:rPr'), _qn('w:t'), _qn('w:tab'), _qn('w:br'), _qn('w:lastRenderedPageBreak'),
              _qn('w:noBreakHyphen'), _qn('w:softHyphen')}
    for child in p._p:
        if child.tag not in ok_child:
            return False
        if child.tag == _qn('w:r'):
            for el in child:
                if el.tag not in ok_run:
                    return False
    return True


def plan(doc, planner: T.Planner) -> Tuple[List[T.Unit], Dict[int, Tuple[object, List[int], str]], Counter]:
    """→ (units, targets, kept). targets: paragraph number → (paragraph, unit ids, joiner)."""
    units: List[T.Unit] = []
    targets: Dict[int, Tuple[object, List[int], str]] = {}
    kept: Counter = Counter()
    in_refs = False
    for n, p in enumerate(doc.paragraphs, 1):
        text = p.text or ''
        stripped = text.strip()
        if not stripped:
            kept['empty'] += 1
            continue
        if _is_heading(p):
            in_refs = bool(T.REF_TITLE.match(stripped))
            kept['heading'] += 1
            continue
        if T.REF_TITLE.match(stripped.strip('*_ ')):
            in_refs = True
            kept['heading'] += 1
            continue
        if in_refs:
            kept['references'] += 1
            continue
        if any(k in _style_name(p) for k in _SKIP_STYLE):
            kept['special style'] += 1
            continue
        if not _plain(p):
            kept['links, images or fields'] += 1
            continue
        if planner.sizer(stripped) < (planner.min_item if _is_list(p) else planner.min_prose):
            kept['short'] += 1
            continue
        if planner.sizer(stripped) > planner.sizer.max_words:
            pieces, kind = T.cut_long(stripped, planner.sizer), 'sentences'
        else:
            pieces, kind = [stripped], 'paragraph'
        ids = []
        for piece in pieces:
            uid = len(units) + 1
            units.append(T.Unit(uid, piece, kind, n))
            ids.append(uid)
        targets[n] = (p, ids, T._joiner(stripped))
    return units, targets, kept


def apply(targets, results: Dict[int, str]):
    """Write each rewrite into its paragraph: first run gets the text, other runs are emptied."""
    import re
    for p, ids, joiner in targets.values():
        # one paragraph in, one paragraph out: line breaks the model added inside a piece are dropped
        new = joiner.join(re.sub(r'\s*\n\s*', joiner, results[u].strip()) for u in ids)
        runs = p.runs
        if not runs:
            continue
        runs[0].text = new
        for r in runs[1:]:
            r.text = ''
