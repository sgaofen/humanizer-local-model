// 文本小工具:Python 式 strip、字数统计、数字核对、格式化。

// Python str.isspace() 认的空白(比 JS trim 多 \x1c-\x1f 和 \x85,少 \ufeff)。
const PY_WS = '\\t\\n\\v\\f\\r\\x1c-\\x1f \\x85\\xa0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000';
const PY_STRIP = new RegExp(`^[${PY_WS}]+|[${PY_WS}]+$`, 'g');

/** 和 Python 的 draft.strip() 一字不差。提示词拼接只能用它。 */
export function pyStrip(s) {
  return s.replace(PY_STRIP, '');
}

const CJK_RE = /[\u3400-\u4DBF\u4E00-\u9FFF\uF900-\uFAFF\u3040-\u30FF\uAC00-\uD7AF]/g;
const WORD_RE = /[\p{L}\p{N}]+(?:['\u2019\-][\p{L}\p{N}]+)*/gu;

/** 字数:中日韩按字、其余按词(和 Word 的"字数"口径一致)。 */
export function countText(s) {
  const cjk = (s.match(CJK_RE) || []).length;
  const latin = (s.replace(CJK_RE, ' ').match(WORD_RE) || []).length;
  return { units: cjk + latin, cjk, latin, chars: [...s].length };
}

// 长文档提示的门槛:约 1,000 token ≈ 700 个英文词 ≈ 1,100 个汉字。
// 依据:评测集最长的草稿约 450 词;草稿过了约 820 token,输出就会顶到 2,048 的上限。再长,事实走样明显变多。
export const LONG_DRAFT_TOKENS = 1000;

/** 粗估 token 数(Gemma 词表实测:英文约 1.5 token/词,中文约 0.95 token/字)。只决定要不要显示提示,不参与改写。 */
export function estimateTokens(s) {
  const c = countText(s);
  return Math.round(c.latin * 1.5 + c.cjk * 0.95);
}

export const isLongDraft = (s) => estimateTokens(s) >= LONG_DRAFT_TOKENS;

/** 主要是中文的文本,换中文字体和行高。 */
export function isMostlyCJK(s) {
  const c = countText(s.slice(0, 2000));
  return c.cjk > c.latin;
}

// 数字核对:原稿里的每个数字,改写里要能找到(写法变了也算提示)。
const NUM_RE = /\d+(?:[.,:/]\d+)*/g;
const normNum = (x) => x.replace(/,(?=\d{3}(?!\d))/g, '');

export function numbersIn(s) {
  return (s.match(NUM_RE) || []).map(normNum);
}

export function numberCheck(draft, out) {
  const want = [...new Set(numbersIn(draft))];
  const have = new Set();
  for (const n of numbersIn(out)) {
    have.add(n);
    for (const part of n.split(/[.,:/]/)) have.add(part); // "10:30" 也算有 "10"
  }
  const missing = want.filter((n) => !have.has(n));
  return { total: want.length, missing };
}

export function fmtBytes(n) {
  if (!(n >= 0)) return '—';
  if (n >= 1e9) return (n / 1e9).toFixed(2) + ' GB';
  if (n >= 1e6) return (n / 1e6).toFixed(1) + ' MB';
  if (n >= 1e3) return (n / 1e3).toFixed(0) + ' KB';
  return n + ' B';
}

export function fmtDuration(sec, lang) {
  if (!isFinite(sec) || sec <= 0) return '—';
  sec = Math.round(sec);
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  if (lang === 'zh') {
    if (h) return `${h} 小时 ${m} 分`;
    if (m) return `${m} 分 ${s} 秒`;
    return `${s} 秒`;
  }
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

export function fmtNum(n, lang) {
  return n.toLocaleString(lang === 'zh' ? 'zh-CN' : 'en-US');
}

export function escapeHTML(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
