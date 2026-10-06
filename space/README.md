---
title: humanizer
emoji: ✍️
colorFrom: gray
colorTo: green
sdk: gradio
sdk_version: 6.29.0
app_file: app.py
pinned: false
license: apache-2.0
short_description: Rewrite AI drafts to read like a person; facts kept. 12B
models:
  - jialinyyzz/humanizer
---

# humanizer: demo

A 12B model that rewrites AI-written drafts (emails, essays, reports, forum posts; English and Chinese)
so they read like a person wrote them. It is trained to keep every number, unit, date, name and quote,
and to add nothing. No AI detector was used anywhere in training.

This Space runs the bf16 weights from [`jialinyyzz/humanizer`](https://huggingface.co/jialinyyzz/humanizer)
on ZeroGPU, up to about 2,100 English words or 3,600 Chinese characters per run: anything over 700 words
or 1,200 characters is split at paragraph breaks and rewritten part by part, and if the GPU stops partway
the finished text stays on screen with a note. The `/humanize` API still takes one part (700 words) per call. Real
before/after pairs from the held-out evaluation set are on the page and need no GPU.

**Run it on your own computer:** the desktop app for macOS (Apple silicon) and Windows is on
[GitHub Releases](https://github.com/sgaofen/humanizer-local-model/releases/latest); the command-line tool
`hz` and llama.cpp, MLX, transformers and vLLM steps are in
[USAGE.md](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.md)
([中文](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.zh.md)) and
[AGENTS.md](https://github.com/sgaofen/humanizer-local-model/blob/main/AGENTS.md).

**Measured, not promised:** 95% judged human. On our 210 English evaluation drafts, Originality.ai
(AI Allowance 0%, its strictest setting; bf16 weights, 2026-10-02) flagged 11 as AI (the previous
release: 26). On the Q8_0 file you download, a strict LLM judge found no factual problem in 376 of 420
English rewrites, and more than 9 in 10 of the fixes it did list are a single word or phrase. Chinese
is still catching up (no factual problem in 149 of 204). Still, read the result before you send it,
especially numbers, dates, names (and the direction of every claim).

**API:** `gradio_client` → `Client("jialinyyzz/humanizer").predict(draft, api_name="/humanize")` returns
the rewrite plus copy rate and any numbers missing from it.

**Privacy:** the Space keeps no text. It logs counts only (lengths, timing, copy rate), never the draft,
the output or your IP.

Source of this Space: [`space/`](https://github.com/sgaofen/humanizer-local-model/tree/main/space) in the GitHub repo.

---

把 AI 写的草稿改成读起来像人写的，训练目标是数字、单位、日期、人名、引语原样保留。中英文都行。训练全程没有用任何 AI 检测器。
这里可以在线试一篇；想装到自己电脑上，下载 [App（macOS / Windows）](https://github.com/sgaofen/humanizer-local-model/releases/latest)，
或者看 [不用 App 怎么用](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.zh.md)。
