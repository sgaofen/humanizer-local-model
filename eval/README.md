# Evaluation set and raw results

Everything behind the numbers in the main README. Nothing here was used in training.

**中文说明见下半部分。**

## Contents

| Path | What it is |
|---|---|
| `drafts/300a/`, `drafts/300b/` | The 312 drafts (210 English, 102 Chinese; files starting with `zh_` are Chinese). Each draft was written from scratch by a frontier model; the writer is the last part of the file name (`glm` = GLM-5.3, `luna` = GPT-5.6 luna, `sonnet` = Claude Sonnet). The genre is the first part. |
| `outputs/humanizer-12b-bf16_300{a,b}.json` | **This release (v2), bf16 weights** (vLLM): `{case: [{"text": ...}, {"text": ...}]}`, two samples per draft, temperature 1.0, top-p 0.95. The detector numbers use the first sample of each English draft. `retried: true` marks the few outputs (1 English second sample, 14 Chinese) that the evaluation pipeline resampled once with a copy penalty because the first try copied more than 35% of the draft; none of the 210 English first samples was resampled. |
| `outputs/humanizer-12b-Q8_0_300{a,b}.json` | **This release (v2), the `humanizer-12b-Q8_0.gguf` file you download**, run with llama.cpp (v0.5.0) and plain sampling: temperature 1.0, top-p 0.95, top-k off, min-p off, repetition penalty 1.0 (the app's settings). Two samples per draft, nothing resampled. The fact-fidelity numbers come from these outputs. |
| `outputs/previous-12b-RLRt_300{a,b}.json` | The previous 12B release (v1), same format. |
| `outputs/examples-12b-Q8_0_x8.json` | 8 samples for each of 27 drafts from this set, from the same `humanizer-12b-Q8_0.gguf` file with the same llama.cpp settings (nothing resampled). The before/after examples in the README, on the Hugging Face page and in the Space are each one of these samples, picked by hand and unedited. |
| `outputs/baseline-blader-humanizer-skill-sonnet_300{a,b}.json` | Public baseline: Claude Sonnet following the `blader/humanizer` skill (v3.1.0), one rewrite each for the 60-draft detector subset. |
| `fidelity/humanizer-12b-Q8_0_{en,zh}_300{a,b}.json` | Fact-fidelity verdicts for the Q8_0 outputs from the LLM judge (GLM-5.3, one vote, strict), one per output: facts kept, meaning changed, content added, greeting/sign-off kept, format kept, severity, evidence. English counts as "no factual problem" unless `severity` is `critical`; Chinese counts when `facts_all_kept` is true and `added_content` is false. One Chinese output (`zh_email_04_glm`, first sample) has no verdict because the judge call failed; it is counted as not passing. |
| `fidelity/humanizer-12b-Q8_0_fix-sizes.jsonl`, `..._summary.json` | Second pass of the same judge over every flagged Q8_0 output (44 English, 54 Chinese): each problem with what the draft says, what the rewrite says, and how much it takes to fix (`one_word`, `one_phrase`, `one_sentence`, `rewrite_needed`). |
| `fidelity/previous-12b-RLRt_{en,zh}_300{a,b}.json` | The same verdicts for the previous release. |
| `fidelity/examples-12b-Q8_0_x8_{en,zh}.json` | Verdicts from the same judge (one vote, same prompt) for the samples we shortlisted for those examples; `i` is the sample's index in `outputs/examples-12b-Q8_0_x8.json`. |
| `originality/*.jsonl` | Originality.ai results (API v3, AI Allowance 0% = strictest setting) on the first sample of each English draft. `label: 1` = judged AI. `ai` = the detector's AI score. `reuse` = overlap with the draft. `_s60` files cover the 60-draft subset only. |

## Detector results in these files

| File | Measured | Judged AI |
|---|---|---|
| `humanizer-12b-bf16.jsonl` (this release, bf16) | 2026-10-02 | 11 / 210 |
| `humanizer-12b-Q8_0.jsonl` (this release, the Q8_0 file) | 2026-10-02 | 15 / 210 |
| `humanizer-12b-bf16_s60.jsonl` (this release, bf16; same outputs as above, 60-draft subset) | 2026-10-02 | 4 / 60 |
| `previous-12b-RLRt.jsonl` (previous 12B release) | 2026-10-01 | 26 / 210 |
| `humanizer-e4b-r7.jsonl` (an earlier 4B model, no longer offered) | 2026-10-01 | 26 / 210 |
| `early-12b-R12s12b-t085.jsonl` | 2026-10-01 | 61 / 210 |
| `early-12b-sftv2-RLBh.jsonl` | 2026-10-01 | 115 / 210 |
| `early-12b-R12s12b-t100_s60.jsonl` | 2026-10-01 | 11 / 60 |
| `baseline-blader-humanizer-skill-sonnet_s60.jsonl` | 2026-10-01 | 60 / 60 |

One measurement per file on one date. Detectors change.

---

Privacy note: a few drafts (and the rewrites of them) contained a real person's name and email address that the draft-writing model had inserted into signatures. In this public copy they are replaced with the placeholder "Daniel Park" / daniel.park@example.com (Chinese: 林同学). All scores were computed before this replacement; only the name and address differ.

---

# 评测集与原始结果

主 README 里所有数字的原始数据都在这里。这些数据没有用于训练。

- `drafts/`:312 篇草稿(英文 210、中文 102,`zh_` 开头的是中文),由三个前沿模型从零写成,文件名最后一段是写手(`glm` = GLM-5.3、`luna` = GPT-5.6 luna、`sonnet` = Claude Sonnet),第一段是体裁。
- `outputs/humanizer-12b-bf16_*`:本版(v2)bf16 权重的输出(vLLM),每篇 2 发,温度 1.0、top-p 0.95。检测器数字用的是每篇英文草稿的第 1 发。`retried: true` 标的是评测流程因首发照抄草稿超过 35% 而加照抄惩罚重采过一次的少数输出(英文第 2 发 1 条、中文 14 条);210 篇英文的第 1 发一条都没有重采。
- `outputs/humanizer-12b-Q8_0_*`:本版(v2)**你下载的 `humanizer-12b-Q8_0.gguf` 文件**,用 llama.cpp(v0.5.0)普通采样跑出来(温度 1.0、top-p 0.95,top-k、min-p 关闭,重复惩罚 1.0,和 App 相同),每篇 2 发,没有重采。事实忠实度的数字来自这些输出。
- `outputs/previous-12b-RLRt_*`:上一版 12B(v1)的输出,格式相同。
- `outputs/examples-12b-Q8_0_x8.json`:从这个评测集里取 27 篇草稿,用同一个 `humanizer-12b-Q8_0.gguf` 文件、同样的 llama.cpp 设置每篇生成 8 发(没有重采)。README、Hugging Face 页面和 Space 里的改写前后例子,都是从这里人工挑的一发,未经修改。
- `outputs/baseline-blader-humanizer-skill-sonnet_*`:公开基线(Claude Sonnet 按 `blader/humanizer` skill 改写),只含检测器那 60 篇小样本。
- `fidelity/humanizer-12b-Q8_0_*`:LLM 判官(GLM-5.3,单票从严)对 Q8_0 每一发的事实忠实度判定。英文只要 `severity` 不是 `critical` 就算"没挑出事实问题";中文要 `facts_all_kept` 为真且 `added_content` 为假。中文有 1 发(`zh_email_04_glm` 第 1 发)判官调用失败、没有判词,按不通过计。
- `fidelity/humanizer-12b-Q8_0_fix-sizes.jsonl`:同一判官对每篇被标出的 Q8_0 改写(英文 44、中文 54)再读一遍,逐条列出草稿原文、改写原文和要改多少(一个词 / 一个短语 / 一句 / 要重写一段)。
- `fidelity/previous-12b-RLRt_*`:上一版的同样判定。
- `fidelity/examples-12b-Q8_0_x8_{en,zh}.json`:同一判官(单票、同一提示词)对我们为这些例子初选的几发的判定;`i` 是该发在 `outputs/examples-12b-Q8_0_x8.json` 里的序号。
- `originality/`:Originality.ai(API v3,AI Allowance 0% 最严档)对每篇英文草稿第 1 发的检测结果;`label: 1` 表示被判为 AI。各文件的测量日期和结果见上表。

检测结果只代表这一天、这一档位的一次测量,检测器会更新。

隐私说明：少数草稿（及其改写）的落款里有起草模型写进去的真实姓名和邮箱，公开版已统一换成占位的 "Daniel Park" / daniel.park@example.com（中文为"林同学"）。所有分数都是在替换之前算的，只有姓名和邮箱不同。
