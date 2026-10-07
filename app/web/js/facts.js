// Create fact:草稿里选中的文字改写时一字不改。
// 做法:改写前把这些文字换成占位符,模型只看到占位符;写完核对每个占位符都原样出现一次,再换回原文。
// 对不上就整篇重写(app.js 的 run(),最多 FACT_TRIES 次),都不行就作废这次结果,绝不交出丢了原文的版本。
// 占位符外形和次数按真模型实测定(32 篇中英草稿 × 2 次,130 处保留段):[[HZ_LOCK_n]] 首次全部带回(3 处模型改了括号写法,下面宽松认);
// 不用占位符事后核对、加引号、换成 ZQ-7301 式编号都明显更差。实测 2 次内全部通过,留 3 次给比测试集更长、保留段更多的草稿。
export const FACT_TRIES = 3;

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

// 模型偶尔把 [[HZ_LOCK_0]] 抄成 [HZ_LOCK_0] 或 HZ_LOCK_0(括号层数变了,编号还在):这种也认。
// 前缀保证不出现在草稿里,所以只要「前缀 + 编号」出现,就只可能是占位符。
const escapeRe = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
function lockPattern(protectedDraft, i) {
  return new RegExp(`(?:\\[{1,2}[ \\t]*)?${escapeRe(protectedDraft.prefix)}${i}(?!\\d)(?:[ \\t]*\\]{1,2})?`, 'g');
}

// Completed results must contain every placeholder exactly once. No partial result
// is published when a protected passage is lost, duplicated, or changed.
export function restoreFacts(text, protectedDraft, strict = true) {
  let restored = text;
  protectedDraft.locks.forEach(({ value }, i) => {
    const re = lockPattern(protectedDraft, i);
    if (strict && (restored.match(re) || []).length !== 1) throw new Error('factsLost');
    restored = restored.replace(re, () => value);
  });
  if (strict && protectedDraft.locks.length && (
    restored.includes(protectedDraft.prefix) || protectedDraft.facts.some((f) => !restored.includes(f))
  )) throw new Error('factsLost');
  return restored;
}

/** 流式途中给人看的文字:已写完的占位符换回原文,末尾写到一半的占位符先不显示。 */
export function streamView(text, protectedDraft) {
  if (!protectedDraft.locks.length) return text;
  let s = restoreFacts(text, protectedDraft, false);
  const p = protectedDraft.prefix;
  const m = s.slice(-(p.length + 8)).match(/\[{1,2}[A-Za-z_]*\d*$/);
  if (m) return s.slice(0, s.length - m[0].length);
  for (let k = Math.min(p.length, s.length); k > 0; k--) if (s.endsWith(p.slice(0, k))) return s.slice(0, -k);
  return s;
}

/**
 * 跑 attempt(k) 得到 { raw, final },写完核对占位符;丢了/改了/重复了/被截断就再跑,最多 tries 次。
 * 返回 { ok, text(换回原文后的结果,失败时为空), tries, final }。没有保护段时只跑一次、原样返回。
 * 用户停止(AbortError)直接抛出,不重试。
 */
export async function withFactGuard(protectedDraft, attempt, tries = FACT_TRIES, onRetry = () => {}) {
  for (let k = 1; ; k++) {
    const { raw, final } = await attempt(k);
    if (!protectedDraft.locks.length) return { ok: true, text: raw, tries: k, final };
    try {
      if (!final || final.stop_type === 'limit') throw new Error('factsLost');
      return { ok: true, text: restoreFacts(raw, protectedDraft), tries: k, final };
    } catch {
      if (k >= tries) return { ok: false, text: '', tries: k, final };
      onRetry(k + 1, tries);
    }
  }
}

