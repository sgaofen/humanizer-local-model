import { buildPrompt, nPredictFor } from './prompt.js';
import { activeFacts, protectFacts, restoreFacts } from './facts.js';

const checkAbort = (signal) => { if (signal?.aborted) throw new DOMException('stopped', 'AbortError'); };

// Never cut a surrogate pair or a fact placeholder. Paragraphs are preferred,
// followed by sentence ends and whitespace; a single giant word uses Unicode cuts.
function safeEnd(text, end, locks) {
  for (const { start, finish } of locks) if (end > start && end < finish) end = start;
  if (end > 0 && /[\uD800-\uDBFF]/.test(text[end - 1])) end--;
  return end;
}
function naturalEnd(text, start, end) {
  const slice = text.slice(start, end);
  for (const re of [/\r?\n[ \t]*\r?\n/g, /[.!?。！？](?:\s+|(?=[^\x00-\x7f]))/g, /\s+/g]) {
    let best = 0;
    for (const m of slice.matchAll(re)) {
      const point = re.source.startsWith('[') ? m.index + 1 : m.index;
      if (point >= slice.length / 3) best = point;
    }
    if (best) return start + best;
  }
  return end;
}

export async function planDocument(draft, facts, cfg, count, signal) {
  const protectedDraft = protectFacts(draft, facts);
  const text = protectedDraft.text;
  const locks = protectedDraft.locks.map(({token}) => ({start:text.indexOf(token), finish:text.indexOf(token)+token.length}));
  const parts = [];
  let start = 0, pending = '';
  while (start < text.length) {
    checkAbort(signal);
    let low = start, high = Math.min(text.length, start + 3200), end = start;
    // Count real model tokens for each candidate, including instructions and output.
    while (low <= high) {
      const mid = Math.floor((low + high) / 2);
      const candidate = safeEnd(text, mid, locks);
      const content = text.slice(start, candidate).trim();
      const tokens = content ? await count(content, {signal}) : 0;
      const promptTokens = content ? await count(buildPrompt(cfg, content), {signal}) : 0;
      checkAbort(signal);
      if (tokens <= 700 && promptTokens + nPredictFor(cfg, tokens) + 64 <= cfg.ctx_size) {
        end = Math.max(end, candidate); low = mid + 1;
      } else high = mid - 1;
    }
    if (end <= start) throw new Error('documentContext');
    if (end < text.length) end = safeEnd(text, naturalEnd(text, start, end), locks);
    if (end <= start) throw new Error('documentContext');
    const raw = pending + restoreFacts(text.slice(start, end), protectedDraft, false);
    pending = '';
    const body = raw.trim();
    if (body) {
      const leading = raw.length - raw.trimStart().length;
      const trailing = raw.length - raw.trimEnd().length;
      parts.push({draft:body, before:raw.slice(0, leading), after:trailing ? raw.slice(-trailing) : ''});
    } else if (parts.length) parts.at(-1).after += raw;
    else pending = raw;
    start = end;
  }
  return parts;
}

export function documentSignature(cfg, tier) {
  return JSON.stringify([tier, cfg.instr, cfg.sep, cfg.ctx_size, cfg.sampling, cfg.n_predict]);
}
export function newDocumentJob(draft, facts, signature, parts) {
  return {version:1, draft, facts:activeFacts(facts,draft), signature, parts, outputs:[]};
}
export function readDocumentJob(raw) {
  try {
    const j = JSON.parse(raw);
    if (j?.version !== 1 || typeof j.draft !== 'string' || typeof j.signature !== 'string' ||
      !Array.isArray(j.facts) || !j.facts.every(f => typeof f === 'string') || !Array.isArray(j.parts) || !j.parts.length ||
      !j.parts.every(p => ['draft','before','after'].every(k => typeof p[k] === 'string') && p.draft.trim()) ||
      j.parts.map(p=>p.before+p.draft+p.after).join('') !== j.draft ||
      !Array.isArray(j.outputs) || j.outputs.length > j.parts.length || !j.outputs.every(s=>typeof s === 'string' && s.trim())) return null;
    // Restored checkpoints must still contain the facts of every completed section.
    if (j.outputs.some((s,i)=>activeFacts(j.facts,j.parts[i].draft).some(f=>!s.includes(f)))) return null;
    return j;
  } catch { return null; }
}
export function jobMatches(job, draft, facts, signature = job?.signature) {
  return !!job && job.draft === draft && job.signature === signature &&
    JSON.stringify(job.facts) === JSON.stringify(activeFacts(facts,draft));
}
export function combinedDocument(job) {
  return job.outputs.map((s,i)=>job.parts[i].before+s+job.parts[i].after).join('');
}

// Commit only completed, validated sections. A failed/canceled section is retried
// on resume; previously committed sections are never sent to the engine again.
export async function runDocumentSections(job, rewrite, {signal, onCommit = () => {}, onProgress = () => {}} = {}) {
  for (let i = job.outputs.length; i < job.parts.length; i++) {
    checkAbort(signal);
    onProgress(i, job.parts.length);
    const output = await rewrite(job.parts[i].draft, job.facts, signal);
    checkAbort(signal);
    if (typeof output !== 'string' || !output.trim()) throw new Error('documentIncomplete');
    if (activeFacts(job.facts,job.parts[i].draft).some(f=>!output.includes(f))) throw new Error('factsLost');
    job.outputs.push(output.trim());
    onCommit(job);
  }
  onProgress(job.parts.length, job.parts.length);
  return combinedDocument(job);
}
