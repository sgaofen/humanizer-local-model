# -*- coding: utf-8 -*-
"""Create fact: passages the visitor selected in the draft come back word for word.

The same scheme as the desktop app (app/web/js/facts.js): before the rewrite each kept passage is
swapped for a placeholder like [[HZ_LOCK_0]], so the model only sees the placeholder; once a part is
written, every placeholder of that part must appear exactly once, then the passages go back in. If one
is lost, changed or doubled, the part is rewritten (up to FACT_TRIES times); a version with a kept passage
changed is never shown. Facts are literal draft text, never extra instructions to the model.
"""
from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from typing import Iterable, List, Optional, Tuple

FACT_TRIES = 3
MAX_FACTS = 50


class FactsLost(Exception):
    pass


def parse(raw) -> List[str]:
    """The facts list sent by the page (a JSON array of strings); anything else is no facts."""
    if isinstance(raw, str):
        try:
            raw = json.loads(raw) if raw.strip() else []
        except ValueError:
            return []
    return [f for f in raw if isinstance(f, str)] if isinstance(raw, list) else []


def active_facts(facts: Iterable[str], draft: str) -> List[str]:
    seen, out = set(), []
    for f in facts or []:
        if isinstance(f, str) and f.strip() and f in draft and f not in seen:
            seen.add(f)
            out.append(f)
    return out[:MAX_FACTS]


@dataclass
class Protected:
    text: str                                   # the draft with every kept passage replaced by its placeholder
    locks: List[Tuple[str, str]] = field(default_factory=list)   # (placeholder, original passage), in order
    facts: List[str] = field(default_factory=list)
    prefix: str = "HZ_LOCK_"

    def indices_in(self, part: str) -> List[int]:
        return [i for i, (tok, _) in enumerate(self.locks) if tok in part]


def protect(draft: str, facts: Iterable[str]) -> Protected:
    active = active_facts(facts, draft)
    prefix = "HZ_LOCK_"
    while prefix in draft:
        prefix = "_" + prefix
    ranges = []
    for fact in active:
        start = draft.find(fact)
        while start >= 0:
            ranges.append([start, start + len(fact)])
            start = draft.find(fact, start + 1)
    ranges.sort(key=lambda r: (r[0], -r[1]))
    merged: List[List[int]] = []
    for r in ranges:
        if merged and r[0] <= merged[-1][1]:
            merged[-1][1] = max(merged[-1][1], r[1])
        else:
            merged.append(list(r))
    text, cursor, locks = [], 0, []
    for i, (a, b) in enumerate(merged):
        tok = f"[[{prefix}{i}]]"
        text.append(draft[cursor:a] + tok)
        locks.append((tok, draft[a:b]))
        cursor = b
    text.append(draft[cursor:])
    return Protected("".join(text), locks, active, prefix)


def _pattern(p: Protected, i: int) -> re.Pattern:
    # The model now and then copies [[HZ_LOCK_0]] as [HZ_LOCK_0] or HZ_LOCK_0 (brackets changed, number kept):
    # accept those too. The prefix never occurs in the draft, so prefix + number can only be a placeholder.
    return re.compile(r"(?:\[{1,2}[ \t]*)?" + re.escape(p.prefix) + str(i) + r"(?!\d)(?:[ \t]*\]{1,2})?")


def restore(text: str, p: Protected, strict: bool = True, only: Optional[List[int]] = None) -> str:
    """Put the passages back. strict: each placeholder in `only` (default: all) must appear exactly once
    and no other placeholder may be left; otherwise FactsLost."""
    idx = list(range(len(p.locks))) if only is None else list(only)
    out = text
    for i in idx:
        pat, value = _pattern(p, i), p.locks[i][1]
        if strict and len(pat.findall(out)) != 1:
            raise FactsLost(i)
        out = pat.sub(lambda _m, v=value: v, out)
    if strict and p.locks and p.prefix in out:
        raise FactsLost("stray")
    return out


def restore_loose(text: str, p: Protected) -> str:
    return restore(text, p, strict=False) if p.locks else text


_PARTIAL = re.compile(r"\[{1,2}[A-Za-z_]*\d*$")


def stream_view(text: str, p: Protected) -> str:
    """What to show while a part streams in: finished placeholders shown as the passage, a placeholder
    that is still being written at the very end left out."""
    if not p.locks:
        return text
    s = restore(text, p, strict=False)
    tail = s[-(len(p.prefix) + 8):]
    m = _PARTIAL.search(tail)
    if m:
        return s[:len(s) - len(m.group(0))]
    for k in range(min(len(p.prefix), len(s)), 0, -1):
        if s.endswith(p.prefix[:k]):
            return s[:-k]
    return s
