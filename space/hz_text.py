# -*- coding: utf-8 -*-
"""hz_text: cut a long text or Markdown document into pieces the model can rewrite, put the rewrites
back in their places, and check each rewrite. Standard library only.

How a document is cut (see plan()):

* Blank lines separate blocks. Fenced code, $$ math and HTML comments stay whole even when they
  contain blank lines.
* Kept exactly as written, never sent to the model: headings (ATX, setext, and lines that are only
  **bold**), fenced and indented code, tables, lines that are only images or links, HTML blocks,
  $$ / \\[ math, block quotes, horizontal rules, YAML front matter, footnote and link definitions,
  and everything under a References / Bibliography / 参考文献 heading.
* Prose paragraphs are grouped in order into pieces of at most about 350 English words or 600
  Chinese characters. A piece never crosses a heading or any kept block.
* A paragraph longer than that is cut at sentence ends and its pieces are joined back into one
  paragraph.
* List items keep their bullet or number (and a leading **Label:**). An item long enough to be worth
  rewriting is rewritten on its own; short items are kept.

The blank lines between blocks are kept as they were; a rewritten piece replaces its lines in place.
"""
from __future__ import annotations

import math
import re
import unicodedata
from collections import Counter
from dataclasses import dataclass, field
from typing import Callable, Dict, List, Optional, Tuple

# ── sizes ───────────────────────────────────────────────────────────────────────────────────────
CJK = re.compile(r'[぀-ヿ㐀-䶿一-鿿豈-﫿가-힯]')
MAX_WORDS = 350        # English words per piece
MAX_CJK = 600          # Chinese characters per piece
MIN_PROSE = 6          # a prose piece smaller than this (in English-word equivalents) is kept as is
MIN_ITEM = 15          # a list item (or a .docx list paragraph) smaller than this is kept as is


def latin_words(text: str) -> int:
    return sum(1 for t in CJK.sub(' ', text).split() if any(c.isalnum() for c in t))


def cjk_chars(text: str) -> int:
    return len(CJK.findall(text))


# ── language guard ──────────────────────────────────────────────────────────────────────────────
# The model occasionally rewrites a short, informal English draft that is full of technical jargon in
# Chinese. This only counts characters (no judgement about content): a draft with at most 2 Chinese
# characters whose rewrite has more than 15 is in the wrong language, and so is a Chinese draft whose
# rewrite is plain English. hz then samples again with the same settings, up to LANG_RETRIES times.
# Same thresholds as the app (app/web/js/guard.js).
HAN = re.compile(r'[㐀-䶿一-鿿豈-﫿]')
LETTER_WORD = re.compile(r"[A-Za-z]+(?:['\u2019-][A-Za-z]+)*")
LANG_RETRIES = 3


def han_chars(text: str) -> int:
    return len(HAN.findall(text or ''))


def language_drift(draft: str, out: str) -> bool:
    """True if the rewrite is in the wrong language: an English draft written in Chinese, or a
    Chinese draft written in English."""
    hd, ho = han_chars(draft), han_chars(out)
    if hd <= 2:
        return ho > 15
    return (hd >= 20 and hd > len(LETTER_WORD.findall(draft)) and ho <= 2
            and len(LETTER_WORD.findall(out or '')) > 15)


def count_words(text: str) -> int:
    """Words as reported to the user: English words plus Chinese characters (one character = one word)."""
    return latin_words(text) + cjk_chars(text)


def is_cjk(text: str) -> bool:
    return cjk_chars(text) > latin_words(text)


def size_label(text: str) -> str:
    """'312 words' for English, '234 chars' for Chinese (for progress lines)."""
    return f"{count_words(text)} {'chars' if is_cjk(text) else 'words'}"


class Sizer:
    """Size of a text in English-word equivalents: a Chinese character counts max_words/max_cjk words."""

    def __init__(self, max_words: int = MAX_WORDS, max_cjk: int = MAX_CJK):
        self.max_words = max(20, int(max_words))
        self.ratio = self.max_words / max(20, int(max_cjk))

    def __call__(self, text: str) -> float:
        return latin_words(text) + cjk_chars(text) * self.ratio


# ── sentence cutting ────────────────────────────────────────────────────────────────────────────
_SENT_END = re.compile(r'[.!?]+[)"\'’”\]]*(?=\s)|[。！？]+[”’"」』)）]*')
_CLAUSE_END = re.compile(r'[;:,]\s+|[；：，、]')


def _split_by(text: str, pat: re.Pattern) -> List[str]:
    parts, last = [], 0
    for m in pat.finditer(text):
        s = text[last:m.end()].strip()
        if s:
            parts.append(s)
        last = m.end()
    tail = text[last:].strip()
    if tail:
        parts.append(tail)
    return parts


# ── checks ──────────────────────────────────────────────────────────────────────────────────────
_NUM = re.compile(r'\d+(?:[.,]\d+)*')
_THOUSANDS = re.compile(r'\d{1,3}(?:,\d{3})+(?:\.\d+)?')
# a scale right after a number: 480k, 1.2M, 3MM, 12B, 2 million, 31.7万, 1.5亿
_SCALE = re.compile(r'(?:(k|K|MM|M|B)(?![A-Za-z])|\s?(bn|mn)\b|\s(thousand|million|billion|trillion)\b|(千万|百万|万|亿))')
_SCALES = {'k': 3, 'K': 3, 'thousand': 3, 'M': 6, 'MM': 6, 'mn': 6, 'million': 6, 'B': 9, 'bn': 9, 'billion': 9,
           'trillion': 12, '万': 4, '百万': 6, '千万': 7, '亿': 8}
_EN_NUM = {'zero': 0, 'one': 1, 'two': 2, 'three': 3, 'four': 4, 'five': 5, 'six': 6, 'seven': 7, 'eight': 8,
           'nine': 9, 'ten': 10, 'eleven': 11, 'twelve': 12, 'thirteen': 13, 'fourteen': 14, 'fifteen': 15,
           'sixteen': 16, 'seventeen': 17, 'eighteen': 18, 'nineteen': 19, 'twenty': 20, 'thirty': 30, 'forty': 40,
           'fifty': 50, 'sixty': 60, 'seventy': 70, 'eighty': 80, 'ninety': 90, 'hundred': 100, 'thousand': 1000,
           'million': 10 ** 6, 'billion': 10 ** 9, 'dozen': 12, 'once': 1, 'twice': 2, 'double': 2, 'triple': 3,
           'first': 1, 'second': 2, 'third': 3, 'fourth': 4, 'fifth': 5, 'sixth': 6, 'seventh': 7, 'eighth': 8,
           'ninth': 9, 'tenth': 10, 'single': 1, 'pair': 2, 'couple': 2}
_CN_DIGIT = {'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
_CN_UNIT = {'十': 10, '百': 100, '千': 1000}
_CN_RUN = re.compile(r'[零〇一二两三四五六七八九十百千万亿]+')


def _norm_int(s: str) -> str:
    return s.lstrip('0') or '0'


def _norm_dec(s: str) -> str:
    whole, _, frac = s.partition('.')
    frac = frac.rstrip('0')
    return _norm_int(whole) + ('.' + frac if frac else '')


def _scaled(key: str, exp: int) -> str:
    from decimal import Decimal
    v = Decimal(key).scaleb(exp)
    s = format(v.normalize(), 'f')
    return _norm_dec(s) if '.' in s else _norm_int(s)


def _occurrences(text: str):
    """→ [(raw spelling, plain key, scaled key or None)] for every number written with digits."""
    t = unicodedata.normalize('NFKC', text or '')
    found = []
    for m in _NUM.finditer(t):
        raw = m.group(0)
        if _THOUSANDS.fullmatch(raw):
            keys = [_norm_dec(raw.replace(',', ''))]
        else:
            keys = []
            for part in raw.split(','):
                if part.count('.') == 1:
                    keys.append(_norm_dec(part))
                else:
                    keys.extend(_norm_int(p) for p in part.split('.') if p)
        sm = _SCALE.match(t, m.end())
        exp = _SCALES[next(g for g in sm.groups() if g)] if sm else 0
        for i, k in enumerate(keys):
            last = i == len(keys) - 1
            found.append((raw + (sm.group(0) if (sm and last) else ''), k, _scaled(k, exp) if (exp and last) else None))
    return found


def _cn_value(s: str) -> Optional[int]:
    total, section, num = 0, 0, 0
    for ch in s:
        if ch in _CN_DIGIT:
            num = _CN_DIGIT[ch]
        elif ch in _CN_UNIT:
            section += (num or 1) * _CN_UNIT[ch]
            num = 0
        elif ch == '万':
            total += (section + num) * 10 ** 4
            section, num = 0, 0
        elif ch == '亿':
            total = (total + section + num) * 10 ** 8
            section, num = 0, 0
    return total + section + num


def _word_keys(text: str) -> set:
    """Numbers written as words: English (eight, twenty-five, twice, third) and Chinese numerals (两, 四十八)."""
    keys = set()
    for w in re.findall(r"[a-z]+(?:-[a-z]+)?", (text or '').lower()):
        parts = w.split('-')
        vals = [_EN_NUM.get(x) for x in parts]
        if len(parts) == 2 and vals[0] is not None and vals[1] is not None and vals[0] >= 20 and vals[1] < 10:
            keys.add(str(vals[0] + vals[1]))
        keys.update(str(v) for v in vals if v is not None)
    for run in _CN_RUN.findall(text or ''):
        v = _cn_value(run)
        if v is not None:
            keys.add(str(v))
        keys.update(str(_CN_DIGIT[c]) for c in run if c in _CN_DIGIT)
    return keys


def numbers(text: str) -> Dict[str, str]:
    """{normalized number: first spelling seen}, for numbers written with digits. 1,250 / 1250 / 1250.0
    are the same number; 07 = 7; full-width digits count; 480k = 480,000 and 31.7万 = 317,000. A comma
    that is not a thousands separator splits (1,2,3 → 1 2 3), and so do runs with more than one dot
    (2026.09.07 → 2026 9 7)."""
    out: Dict[str, str] = {}
    for raw, key, scaled in _occurrences(text):
        out.setdefault(scaled or key, raw)
    return out


def _all_keys(text: str) -> set:
    """Every way a number in this text can be read: digits, digits × scale, and number words."""
    keys = _word_keys(text)
    for _, key, scaled in _occurrences(text):
        keys.add(key)
        if scaled:
            keys.add(scaled)
    return keys


def missing_numbers(draft: str, out: str) -> List[str]:
    """Numbers in the draft that the rewrite does not contain (in any spelling), as spelled in the draft."""
    have = _all_keys(out)
    return [raw for k, raw in numbers(draft).items() if k not in have]


def added_numbers(draft: str, out: str) -> List[str]:
    """Numbers in the rewrite that the draft does not contain in any spelling: possibly made up."""
    have = _all_keys(draft)
    return [raw for k, raw in numbers(out).items() if k not in have]


_URL = re.compile(r'https?://[^\s<>()\[\]"\'`]+')


def urls(text: str) -> List[str]:
    seen = []
    for u in _URL.findall(text or ''):
        u = u.rstrip('.,;:!?')
        if u not in seen:
            seen.append(u)
    return seen


def missing_urls(draft: str, out: str) -> List[str]:
    return [u for u in urls(draft) if u not in (out or '')]


def _grams(text: str, cjk: bool, n: int = 5, k: int = 6) -> list:
    """Word 5-grams (lowercased) plus, for Chinese, 6-character grams. Same units as the repo's
    textmetrics.copy_5gram (6 characters ≈ the sensitivity of 5 English words)."""
    w = re.findall(r"[a-z0-9']+", (text or '').lower().replace('’', "'"))
    g: list = [tuple(w[i:i + n]) for i in range(len(w) - n + 1)]
    if cjk:
        s = re.sub(r'\s+', '', text or '')
        g += [s[i:i + k] for i in range(len(s) - k + 1) if CJK.search(s[i:i + k])]
    return g


def copy_rate(draft: str, out: str) -> float:
    """Share of the rewrite's 5-grams (Chinese: 6-character grams) that also appear in the draft.
    0 = all new wording, 1 = copied."""
    cjk = bool(CJK.search(draft or '')) or bool(CJK.search(out or ''))
    g = _grams(out, cjk)
    if not g:
        return 0.0
    dg = set(_grams(draft, cjk))
    return sum(1 for x in g if x in dg) / len(g)


def _sent_units(sent: str) -> set:
    if CJK.search(sent):
        s = re.sub(r'[^\w]', '', sent)
        return {s[i:i + 2] for i in range(len(s) - 1)}
    return {w for w in re.findall(r"[a-z0-9']+", sent.lower()) if len(w) > 3}


def _has_repeat(text: str, thr: float = 0.5, min_units: int = 5) -> bool:
    """True when two sentences of the text say nearly the same thing (≥ 50 % of the shorter one's
    content words, or character pairs for Chinese, appear in the other).
    Calibrated on real outputs: distinct sentences overlap ≤ 0.3, the repeats we saw 0.67-0.71."""
    sents = [u for u in (_sent_units(x) for x in _split_by(re.sub(r'\s+', ' ', text or ''), _SENT_END))
             if len(u) >= min_units]
    for i in range(len(sents)):
        for j in range(i + 1, len(sents)):
            if len(sents[i] & sents[j]) / min(len(sents[i]), len(sents[j])) >= thr:
                return True
    return False


_TAG = re.compile(r'</?[A-Za-z][A-Za-z0-9]*(?:\s[^<>\n]*)?/?>')


def stray_markup(draft: str, out: str) -> List[str]:
    """HTML tags in the rewrite that the draft does not have (seen on real runs: a stray <p> after a list item)."""
    return sorted({t for t in _TAG.findall(out or '') if t not in (draft or '')})


# Problems that make hz rewrite a piece again. 'added_numbers' is only reported, never retried.
# 'language' (rewrite in the wrong language) has its own silent resampling before check() runs.
RETRY_ISSUES = ('empty', 'truncated', 'too_short', 'too_long', 'repeated', 'markup', 'missing_numbers',
                'missing_urls', 'copy')
TOO_SHORT, TOO_LONG, TOO_LONG_MIN = 0.35, 1.75, 12


@dataclass
class Check:
    copy: float
    missing_numbers: List[str]
    missing_urls: List[str]
    added_numbers: List[str]
    issues: List[str]

    @property
    def truncated(self) -> bool:
        return 'truncated' in self.issues

    @property
    def retry(self) -> bool:
        return any(i in RETRY_ISSUES for i in self.issues)

    def score(self) -> Tuple[int, float]:
        """Lower is better. Each missing number or link, each added number and copying too much count
        one; an empty, cut-off, wrong-language, much too short or much too long rewrite counts more than
        all of those."""
        bad = len(self.missing_numbers) + len(self.missing_urls) + len(self.added_numbers) + ('copy' in self.issues)
        bad += 'markup' in self.issues
        bad += sum({'empty': 1000, 'language': 200, 'truncated': 100, 'too_short': 50, 'too_long': 50, 'repeated': 50}.get(i, 0)
                   for i in self.issues)
        return bad, self.copy


def check(draft: str, out: str, truncated: bool = False, max_copy: float = 0.5,
          sizer: Optional[Sizer] = None) -> Check:
    sizer = sizer or Sizer()
    c = copy_rate(draft, out)
    mn, mu, an = missing_numbers(draft, out), missing_urls(draft, out), added_numbers(draft, out)
    empty = not (out or '').strip()
    din, dout = sizer(draft), sizer(out)
    issues = []
    if empty:
        issues.append('empty')
    if truncated:
        issues.append('truncated')
    if not empty and din >= 30 and dout < TOO_SHORT * din:
        issues.append('too_short')
    if dout > TOO_LONG * din and dout - din >= TOO_LONG_MIN:
        issues.append('too_long')
    if not empty and _has_repeat(out) and not _has_repeat(draft):
        issues.append('repeated')
    if not empty and language_drift(draft, out):
        issues.append('language')
    if stray_markup(draft, out):
        issues.append('markup')
    if mn:
        issues.append('missing_numbers')
    if mu:
        issues.append('missing_urls')
    if c > max_copy:
        issues.append('copy')
    if an:
        issues.append('added_numbers')
    return Check(round(c, 3), mn, mu, an, issues)


def _joiner(text: str) -> str:
    return '' if is_cjk(text) else ' '


def _balanced(parts: List[str], sizer: Sizer, limit: float, joiner: str) -> List[str]:
    """Group consecutive parts into pieces of at most `limit`, about equally large."""
    sizes = [sizer(p) for p in parts]
    total = sum(sizes)
    if total <= limit:
        return [joiner.join(parts)]
    target = total / math.ceil(total / limit)
    groups, cur, cw = [], [], 0.0
    for p, w in zip(parts, sizes):
        if cur and (cw + w > limit or cw + w / 2 > target):
            groups.append(joiner.join(cur))
            cur, cw = [], 0.0
        cur.append(p)
        cw += w
    if cur:
        groups.append(joiner.join(cur))
    return groups


def cut_long(text: str, sizer: Sizer, limit: Optional[float] = None) -> List[str]:
    """Cut one over-long paragraph at sentence ends (then clause marks, then words) into pieces of
    at most `limit`. Joining the pieces with _joiner(text) gives the paragraph back."""
    limit = limit or sizer.max_words
    j = _joiner(text)
    flat = re.sub(r'\s*\n\s*', j or ' ', text.strip())
    pieces = []
    for sent in _split_by(flat, _SENT_END):
        if sizer(sent) <= limit * 1.5:
            pieces.append(sent)
            continue
        for clause in _split_by(sent, _CLAUSE_END):
            if sizer(clause) <= limit * 1.5:
                pieces.append(clause)
            elif j == ' ':
                ws = clause.split()
                step = max(1, int(limit))
                pieces.extend(' '.join(ws[i:i + step]) for i in range(0, len(ws), step))
            else:
                step = max(1, int(limit / sizer.ratio))
                pieces.extend(clause[i:i + step] for i in range(0, len(clause), step))
    return _balanced(pieces, sizer, limit, j)


# ── block scanner ───────────────────────────────────────────────────────────────────────────────
FENCE = re.compile(r'^ {0,3}(`{3,}|~{3,}|:{3,})')
ATX = re.compile(r'^ {0,3}(#{1,6})(?:[ \t]+|$)')
SETEXT = re.compile(r'^ {0,3}(=+|-+)[ \t]*$')
HR = re.compile(r'^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$')
LIST = re.compile(r'^([ \t]*)([-*+•]|\d{1,3}[.)])([ \t]+)(\[[ xX]\][ \t]+)?(.*)$')
TABLE_DELIM = re.compile(r'^ {0,3}\|?[ \t]*:?-{1,}:?[ \t]*(?:\|[ \t]*:?-{1,}:?[ \t]*)*\|?[ \t]*$')
QUOTE = re.compile(r'^ {0,3}>')
HTML_START = re.compile(r'^ {0,3}<(?:[A-Za-z][A-Za-z0-9-]*[\s/>]|[A-Za-z][A-Za-z0-9-]*$|/[A-Za-z]|!--|!\[CDATA\[|![A-Z]|\?)')
MATH_OPEN = re.compile(r'^ {0,3}(\$\$|\\\[)')
REFDEF = re.compile(r'^ {0,3}\[(?:\^[^\]]+|[^\]]+)\]:[ \t]*\S')
CITE_ENTRY = re.compile(r'^ {0,3}(?:\[\d{1,4}\]|\[\^[^\]]+\])\s')
BOLD_LINE = re.compile(r'^\s*(\*\*|__)(?=\S)([^*_\n]+?)(?<=\S)\1\s*[:：]?\s*$')
LEADIN = re.compile(r'^((?:\*\*|__)[^*_\n]{1,80}?(?:\*\*|__)\s*[:：.。—–-]?\s*)(.*)$', re.S)
PLAIN_LEADIN = re.compile(r'^([^\s:：.!?。！？][^:：.!?。！？\n]{0,40}[:：]\s*)(.+)$', re.S)
_LINKY = re.compile(
    r'\[!\[[^\]]*\]\([^)]*\)\]\([^)]*\)'      # [![badge](img)](link)
    r'|!\[[^\]]*\]\([^)]*\)'                   # ![image](src)
    r'|!?\[[^\]]*\]\[[^\]]*\]'                 # [text][ref]
    r'|\[[^\]]*\]\([^)]*\)'                    # [text](url)
    r'|<img\b[^>]*>|<a\b[^>]*>.*?</a>'         # html image or link
    r'|<https?://[^>]+>|https?://\S+')         # bare link
REF_TITLE = re.compile(
    r'^(?:\d+(?:\.\d+)*\.?\s+|[IVXLC]+\.\s+)?'
    r'(references?|bibliography|works cited|literature cited|cited works|reference list|sources|citations'
    r'|参考文献|参考资料|引用文献|文献|引用)\s*[:：]?$', re.I)

REWRITABLE = ('prose', 'list')


@dataclass
class Block:
    kind: str           # prose list heading code table media html math quote hr frontmatter refdef references intro
    start: int          # first line (0-based)
    end: int            # one past the last line
    level: int = 0      # heading level: 1-6 for # and setext, 7 for a **bold** line or a bare "References"
    indent: str = ''    # common indentation of a prose block (continuation paragraph of a list item)


def _is_fence_close(line: str, fence: str) -> bool:
    s = line.strip()
    return len(s) >= len(fence) and set(s) == {fence[0]}


def _is_link_only(line: str) -> bool:
    if not _LINKY.search(line):
        return False
    return _LINKY.sub('', line).strip(' \t|·•,;-–—') == ''


def _heading_title(lines: List[str], b: Block) -> str:
    s = lines[b.start]
    s = re.sub(r'^ {0,3}#{1,6}[ \t]*', '', s)
    s = re.sub(r'[ \t]+#+[ \t]*$', '', s)
    return s.strip().strip('*_').strip()


def scan(lines: List[str]) -> List[Block]:
    """Line scanner → blocks in document order. Blank lines belong to no block."""
    blocks: List[Block] = []
    n = len(lines)
    i = 0
    if n and lines[0].strip() == '---':                     # YAML front matter
        for j in range(1, n):
            if lines[j].strip() in ('---', '...'):
                blocks.append(Block('frontmatter', 0, j + 1))
                i = j + 1
                break
    cur: List[Optional[Block]] = [None]

    def close():
        if cur[0] is not None:
            blocks.append(cur[0])
            cur[0] = None

    def extend(kind: str):
        if cur[0] is not None and cur[0].kind == kind:
            cur[0].end = i + 1
        else:
            close()
            cur[0] = Block(kind, i, i + 1)

    while i < n:
        line = lines[i]
        c = cur[0]
        if not line.strip():
            close()
            i += 1
            continue
        if c is not None and c.kind == 'html':             # an HTML block runs to the next blank line
            c.end = i + 1
            i += 1
            continue
        m = FENCE.match(line)
        if m:
            close()
            fence = m.group(1)
            j = i + 1
            while j < n and not _is_fence_close(lines[j], fence):
                j += 1
            end = min(j + 1, n)
            blocks.append(Block('code', i, end))
            i = end
            continue
        m = MATH_OPEN.match(line)
        if m:
            close()
            s = line.strip()
            closer = '$$' if m.group(1) == '$$' else '\\]'
            if len(s) > len(closer) + 1 and s.endswith(closer):
                end = i + 1
            else:
                j = i + 1
                while j < n and not lines[j].rstrip().endswith(closer):
                    j += 1
                end = min(j + 1, n)
            blocks.append(Block('math', i, end))
            i = end
            continue
        if line.lstrip().startswith('<!--'):
            close()
            j = i
            while j < n and '-->' not in lines[j]:
                j += 1
            end = min(j + 1, n)
            blocks.append(Block('html', i, end))
            i = end
            continue
        m = ATX.match(line)
        if m:
            close()
            blocks.append(Block('heading', i, i + 1, level=len(m.group(1))))
            i += 1
            continue
        if c is not None and c.kind == 'prose' and SETEXT.match(line):
            c.kind, c.end, c.level = 'heading', i + 1, (1 if '=' in line else 2)
            close()
            i += 1
            continue
        if HR.match(line):
            close()
            blocks.append(Block('hr', i, i + 1))
            i += 1
            continue
        if (c is not None and c.kind == 'table' and '|' in line) or line.lstrip().startswith('|') or (
                '|' in line and i + 1 < n and '|' in lines[i + 1] and TABLE_DELIM.match(lines[i + 1])):
            extend('table')
            i += 1
            continue
        if QUOTE.match(line):
            extend('quote')
            i += 1
            continue
        if HTML_START.match(line):
            close()
            cur[0] = Block('html', i, i + 1)
            i += 1
            continue
        if LIST.match(line):
            extend('list')
            i += 1
            continue
        if REFDEF.match(line):
            close()
            cur[0] = Block('refdef', i, i + 1)
            i += 1
            continue
        if c is not None and c.kind in ('prose', 'list', 'html', 'quote', 'refdef'):
            c.end = i + 1                                  # paragraph or lazy continuation line
        else:
            close()
            cur[0] = Block('prose', i, i + 1)
        i += 1
    close()

    # second pass: what kind of prose is it, and is it inside a References section
    prev = None
    for b in blocks:
        if b.kind == 'prose':
            ls = lines[b.start:b.end]
            ind = min(len(l) - len(l.lstrip(' \t')) for l in ls)
            if all(_is_link_only(l) for l in ls):
                b.kind = 'media'
            elif len(ls) == 1 and BOLD_LINE.match(ls[0]) and latin_words(ls[0]) + cjk_chars(ls[0]) // 2 <= 14:
                b.kind, b.level = 'heading', 7
            elif len(ls) == 1 and REF_TITLE.match(ls[0].strip().strip('*_').strip()):
                b.kind, b.level = 'heading', 7
            elif all(CITE_ENTRY.match(l) for l in ls[:1]) and len(ls) <= 6:
                b.kind = 'references'
            elif ind >= 4 and not (prev is not None and (prev.kind == 'list' or (prev.kind == 'prose' and prev.indent))):
                b.kind = 'code'                            # indented code block (not a list item's paragraph)
            elif ind > 0:
                b.indent = ls[0][:ind] if ls[0][:ind].strip() == '' else ' ' * ind
        prev = b
    for b, nxt in zip(blocks, blocks[1:]):                 # "Key points:" right before a list, table or code
        if b.kind == 'prose' and nxt.kind in ('list', 'table', 'code', 'math', 'quote') and b.end - b.start == 1:
            s = lines[b.start].strip()
            if s.endswith((':', '：')) and latin_words(s) + cjk_chars(s) // 2 <= 15:
                b.kind = 'intro'
    ref_level = None
    for b in blocks:
        if b.kind == 'heading':
            if ref_level is not None and b.level <= ref_level:
                ref_level = None
            if REF_TITLE.match(_heading_title(lines, b)):
                ref_level = b.level
            continue
        if ref_level is not None and b.kind in REWRITABLE:
            b.kind = 'references'
    return blocks


# ── plan ────────────────────────────────────────────────────────────────────────────────────────
@dataclass
class Unit:
    id: int
    text: str           # the draft sent to the model
    kind: str           # prose | item | sentences | paragraph (.docx)
    line: int           # 1-based line number in the input (.docx: paragraph number)


Render = Callable[[Dict[int, str]], str]


@dataclass
class Plan:
    lines: List[str]
    blocks: List[Block]
    units: List[Unit] = field(default_factory=list)
    repl: List[Tuple[int, int, Render]] = field(default_factory=list)
    kept: Counter = field(default_factory=Counter)
    crlf: bool = False

    def render(self, results: Dict[int, str]) -> str:
        out: List[str] = []
        i = 0
        for s, e, fn in sorted(self.repl, key=lambda r: r[0]):
            out.extend(self.lines[i:s])
            out.append(fn(results))
            i = e
        out.extend(self.lines[i:])
        text = '\n'.join(out)
        return text.replace('\n', '\r\n') if self.crlf else text


def _oneline(text: str) -> str:
    return ' '.join((text or '').split())


def _reindent(text: str, indent: str) -> str:
    if not indent:
        return text
    return '\n'.join((indent + l) if l.strip() else l for l in text.split('\n'))


def split_leadin(text: str) -> Tuple[str, str]:
    """'**Label:** rest' or 'Label: rest' → (lead-in kept verbatim, rest to rewrite)."""
    m = LEADIN.match(text)
    if m and m.group(2).strip():
        return m.group(1), m.group(2)
    m = PLAIN_LEADIN.match(text)
    if m and latin_words(m.group(1)) <= 5 and cjk_chars(m.group(1)) <= 10 and not _NUM.search(m.group(1)):
        return m.group(1), m.group(2)
    return '', text


class Planner:
    def __init__(self, max_words: int = MAX_WORDS, max_cjk: int = MAX_CJK,
                 min_prose: float = MIN_PROSE, min_item: float = MIN_ITEM):
        self.sizer = Sizer(max_words, max_cjk)
        self.min_prose, self.min_item = min_prose, min_item

    def plan_text(self, text: str) -> Plan:
        crlf = '\r\n' in text
        text = text.replace('\r\n', '\n')
        lines = text.split('\n')
        p = Plan(lines, scan(lines), crlf=crlf)
        run: List[Block] = []
        for b in p.blocks:
            if b.kind == 'prose':
                if run and b.indent != run[-1].indent:
                    self._prose_run(p, run)
                    run = []
                run.append(b)
                continue
            if run:
                self._prose_run(p, run)
                run = []
            if b.kind == 'list':
                self._list(p, b)
            else:
                p.kept[b.kind] += 1
        if run:
            self._prose_run(p, run)
        return p

    def _add(self, p: Plan, text: str, kind: str, line: int) -> int:
        uid = len(p.units) + 1
        p.units.append(Unit(uid, text, kind, line))
        return uid

    def _block_text(self, p: Plan, b: Block) -> str:
        ls = p.lines[b.start:b.end]
        k = len(b.indent)
        return '\n'.join(l[k:] if l[:k].strip() == '' else l.lstrip() for l in ls).strip()

    def _prose_run(self, p: Plan, run: List[Block]):
        """Consecutive prose paragraphs (only blank lines between them) → pieces of at most max_words."""
        limit = self.sizer.max_words
        sizes = [self.sizer(self._block_text(p, b)) for b in run]
        i = 0
        while i < len(run):
            if sizes[i] > limit:                           # one over-long paragraph: cut at sentences
                self._long_para(p, run[i])
                i += 1
                continue
            j = i
            while j < len(run) and sizes[j] <= limit:
                j += 1
            sub, ss = run[i:j], sizes[i:j]                 # a stretch of normal paragraphs
            total = sum(ss)
            target = total / max(1, math.ceil(total / limit))
            group, gw = [], 0.0
            for b, w in zip(sub, ss):
                if group and (gw + w > limit or gw + w / 2 > target):
                    self._prose_group(p, group)
                    group, gw = [], 0.0
                group.append(b)
                gw += w
            if group:
                self._prose_group(p, group)
            i = j

    def _prose_group(self, p: Plan, group: List[Block]):
        first, last = group[0], group[-1]
        ind = first.indent
        k = len(ind)
        ls = p.lines[first.start:last.end]
        text = '\n'.join(l[k:] if l[:k].strip() == '' else l.lstrip() for l in ls).strip()
        if self.sizer(text) < self.min_prose:
            p.kept['short'] += len(group)
            return
        uid = self._add(p, text, 'prose', first.start + 1)
        p.repl.append((first.start, last.end, lambda r, u=uid, ind=ind: _reindent(r[u].strip(), ind)))

    def _long_para(self, p: Plan, b: Block):
        text = self._block_text(p, b)
        pieces = cut_long(text, self.sizer)
        j = _joiner(text)
        uids = [self._add(p, s, 'sentences', b.start + 1) for s in pieces]
        p.repl.append((b.start, b.end, lambda r, us=tuple(uids), j=j, ind=b.indent:
                       _reindent(j.join(r[u].strip() for u in us), ind)))

    def _list(self, p: Plan, b: Block):
        items = []                                         # [start, end, prefix, text]
        for i in range(b.start, b.end):
            line = p.lines[i]
            m = LIST.match(line)
            if m:
                prefix = m.group(1) + m.group(2) + m.group(3) + (m.group(4) or '')
                items.append([i, i + 1, prefix, m.group(5)])
            elif items:
                it = items[-1]
                it[1] = i + 1
                sep = '' if (is_cjk(it[3]) and is_cjk(line)) else ' '
                it[3] = it[3].rstrip() + sep + line.strip()
        for start, end, prefix, body in items:
            lead, rest = split_leadin(body.strip())
            if self.sizer(rest) < self.min_item:
                p.kept['list item'] += 1
                continue
            uid = self._add(p, rest.strip(), 'item', start + 1)
            p.repl.append((start, end, lambda r, u=uid, pre=prefix + lead: pre + _oneline(r[u])))


def plan_text(text: str, **kw) -> Plan:
    return Planner(**kw).plan_text(text)
