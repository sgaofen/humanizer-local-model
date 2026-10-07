// Facts are literal draft passages, not additional model instructions.
export function activeFacts(facts, draft) {
  return [...new Set((Array.isArray(facts) ? facts : []).filter(
    (f) => typeof f === 'string' && f.trim() && draft.includes(f),
  ))].slice(0, 50);
}

export function protectFacts(draft, facts) {
  const active = activeFacts(facts, draft);
  let prefix = 'HZ_LOCK_';
  while (draft.includes(prefix)) prefix = '_' + prefix;
  const ranges = [];
  for (const fact of active) {
    for (let start = draft.indexOf(fact); start >= 0; start = draft.indexOf(fact, start + 1)) {
      ranges.push({ start, end: start + fact.length });
    }
  }
  ranges.sort((a, b) => a.start - b.start || b.end - a.end);
  const merged = [];
  for (const range of ranges) {
    const last = merged.at(-1);
    if (last && range.start <= last.end) last.end = Math.max(last.end, range.end);
    else merged.push({ ...range });
  }
  let text = '', cursor = 0;
  const locks = merged.map((range, i) => {
    const token = `[[${prefix}${i}]]`;
    const value = draft.slice(range.start, range.end);
    text += draft.slice(cursor, range.start) + token;
    cursor = range.end;
    return { token, value };
  });
  text += draft.slice(cursor);
  return { text, locks, facts: active, prefix };
}

// Completed results must contain every placeholder exactly once. No partial result
// is published when a protected passage is lost, duplicated, or changed.
export function restoreFacts(text, protectedDraft, strict = true) {
  let restored = text;
  for (const { token, value } of protectedDraft.locks) {
    if (strict && restored.split(token).length !== 2) throw new Error('factsLost');
    restored = restored.split(token).join(value);
  }
  if (strict && protectedDraft.locks.length && (
    restored.includes(protectedDraft.prefix) || protectedDraft.facts.some((f) => !restored.includes(f))
  )) throw new Error('factsLost');
  return restored;
}
