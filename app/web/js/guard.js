// 语言保险(0.3.1):模型偶尔会把英文草稿整篇写成中文(口语、夹技术术语的短帖上偶发,和量化档位无关)。
// 只数字符判语种,不做事实判断:草稿几乎没有汉字、输出却有一大段汉字 → 同样参数静默重采,最多 3 次,
// 都不行就交最后一次。反方向(中文草稿写成英文)同理,但英文词在中文输出里可能先出现,所以只在写完后判。
// 和 humanizer/hz_text.py 的 language_drift 同一套阈值。

const HAN_RE = /[㐀-䶿一-鿿豈-﫿]/g;
const LATIN_WORD_RE = /[A-Za-z]+(?:['’\-][A-Za-z]+)*/g;

export const LANG_RETRIES = 3;
const DRAFT_MAX_HAN = 2;      // 英文草稿:汉字不超过 2 个
const OUT_MAX_HAN = 15;       // 输出汉字超过 15 个 = 写成了中文
const ZH_DRAFT_MIN_HAN = 20;  // 中文草稿:至少 20 个汉字,且汉字多于英文词
const ZH_OUT_MIN_WORDS = 15;  // 输出几乎没汉字、英文词超过 15 个 = 写成了英文

export const hanCount = (s) => ((s || '').match(HAN_RE) || []).length;
export const latinWordCount = (s) => ((s || '').match(LATIN_WORD_RE) || []).length;

/** 英文草稿被写成中文。汉字数只增不减,流式途中判和写完判结果一样。 */
export function wentChinese(draft, out) {
  return hanCount(draft) <= DRAFT_MAX_HAN && hanCount(out) > OUT_MAX_HAN;
}

/** 中文草稿被写成英文。只对写完的全文判。 */
export function wentEnglish(draft, out) {
  const h = hanCount(draft);
  return h >= ZH_DRAFT_MIN_HAN && h > latinWordCount(draft) && hanCount(out) <= DRAFT_MAX_HAN && latinWordCount(out) > ZH_OUT_MIN_WORDS;
}

/** done=false:流式途中(只判英→中);done=true:写完(两个方向都判)。 */
export function langDrift(draft, out, done) {
  return wentChinese(draft, out) || (!!done && wentEnglish(draft, out));
}

/**
 * 跑 attempt(i, isLast),输出语言不对就再跑,最多再跑 retries 次;最后一次无论如何都交出去。
 * attempt 返回 { text, ... };途中发现跑偏可以提前收尾并带 drifted: true。
 * 返回最后一次的结果,外加 tries(一共跑了几次)。
 */
export async function withLangGuard(draft, attempt, retries = LANG_RETRIES) {
  for (let i = 0; ; i++) {
    const isLast = i >= retries;
    const res = await attempt(i, isLast);
    const drifted = res.drifted || langDrift(draft, res.text || '', true);
    if (!drifted || isLast) return { ...res, tries: i + 1 };
  }
}
