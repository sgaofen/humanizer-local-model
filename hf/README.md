---
license: apache-2.0
base_model: google/gemma-4-12B
base_model_relation: finetune
language:
- en
- zh
pipeline_tag: text-generation
library_name: gguf
tags:
- humanizer
- transformers
- safetensors
- text-rewriting
- rewriting
- paraphrase
- style-transfer
- gguf
- llama.cpp
- gemma4
---

<img src="assets/banner-en.png" alt="humanizer: rewrites AI drafts so they read like a person wrote them" width="100%">

<img src="assets/app-showcase-en.png" alt="The humanizer app running the 12B model locally: draft on the left, rewrite on the right, new wording highlighted" width="100%">

# humanizer

**A 12B model that rewrites AI-written drafts (emails, essays, reports, forum posts; English and Chinese) so they read like a person wrote them.** It is trained to keep every number, unit, date, name and quote and to add nothing, and it runs locally.

**Quick start:** get the app for **[Mac (Apple silicon)](https://github.com/sgaofen/humanizer-local-model/releases/download/app-v0.3.2/Humanizer-0.3.2-macos-arm64.dmg)** or **[Windows](https://github.com/sgaofen/humanizer-local-model/releases/download/app-v0.3.2/Humanizer-0.3.2-windows-x64-setup.exe)**, open it and paste a draft; it works offline. Without the app: [llama.cpp and other runtimes](#usage-without-the-app), or [AGENTS.md](AGENTS.md) for coding agents.

## Before and after

Drafts from the held-out evaluation set. For each we generated 8 samples with the Q8_0 file (llama.cpp, the app's settings) and picked the one that reads best among those the fact judge passed; it is shown unedited, and all 8 samples are [on GitHub](https://github.com/sgaofen/humanizer-local-model/blob/main/eval/outputs/examples-12b-Q8_0_x8.json). These are picks: across the whole set the judge flagged 44 of the 420 English rewrites, 135 problems in all, 125 of them a single word or phrase. Read the result before you send it, especially numbers, dates and names. Results over the whole set are below.

<img src="assets/compare-en-email.png" alt="Work email: draft and rewrite" width="100%">

<img src="assets/compare-zh-email.png" alt="Chinese work email: draft and rewrite" width="100%">

More examples (a forum answer, a Zhihu answer) are in the [GitHub README](https://github.com/sgaofen/humanizer-local-model#before-and-after).

## Results

<img src="assets/results-detector-en.png" alt="Originality.ai: 95% of rewrites judged human" width="100%">

**95% judged human by Originality.ai** at its strictest setting (210 English drafts, bf16 weights, 2026-10-02): 11 of 210 rewrites were flagged as AI, against 26 for the previous release, and no AI detector was used anywhere in training. On the same 60 drafts, the most popular de-AI skill on GitHub, applied by Claude Sonnet, had all 60 flagged; this release had 4.

<img src="assets/results-fidelity-en.png" alt="Fact fidelity compared with the previous releases" width="100%">

**376 of 420 English rewrites came back with no factual problem** from a strict LLM judge, measured on the `humanizer-12b-Q8_0.gguf` file you download; where it did find one, more than 9 in 10 fixes are a single word or phrase. Method, every genre and the Chinese results: [Evaluation details](#evaluation-details).

## Quantization: smaller files, closer to the full model

<img src="assets/quant-top1-en.png" alt="Top-1 agreement with bf16 against file size: our GGUF files (green) sit above standard llama.cpp quantization (gray) at 2-bit, Q3 and Q4_K_M; at 3.9 GB, 87% against 70%" width="100%">

Most GGUF files come from one `llama-quantize` pass that rounds every weight to the target type; our 2-bit, Q3 and Q4_K_M files go further. In the 2-bit and Q3 files, we measured how much each weight tensor hurts the model when it is squeezed and gave the sensitive tensors more bits and the robust ones fewer. All three were then trained layer by layer to reproduce the full model (quantization-aware training) and distilled from the full bf16 model, used as the teacher, on English and Chinese rewriting data. For the 2-bit file, a standard build of the same size picks the same next token as bf16 70% of the time; these steps take it to 81%, 85% and then 87%. The result is still an ordinary GGUF that any recent llama.cpp runs; Q6_K and Q8_0 are standard builds, because at those sizes there is almost nothing left to recover.

<sub>Numbers behind every point, KL, per-file fact checks and how each file was made: [docs/QUANTIZATION.md](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/QUANTIZATION.md). All GGUF files also have their own repo: [jialinyyzz/humanizer-GGUF](https://huggingface.co/jialinyyzz/humanizer-GGUF).</sub>

**[Usage without the app](USAGE.md)** · [不用 App 怎么用](USAGE.zh.md) · [AGENTS.md](AGENTS.md) · [GitHub](https://github.com/sgaofen/humanizer-local-model) · [GGUF files](https://huggingface.co/jialinyyzz/humanizer-GGUF) · [Install guide](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/INSTALL.md) · [中文说明](https://github.com/sgaofen/humanizer-local-model/blob/main/README.zh.md)

> **Setting this up with an AI agent?** Point it at [AGENTS.md](AGENTS.md): exact files, server command, prompt byte for byte, and a self-test.

## Quick start

**App:** download the `.dmg` (Mac with Apple silicon) or the Windows installer from [Releases](https://github.com/sgaofen/humanizer-local-model/releases/latest) (current: [app 0.3.2](https://github.com/sgaofen/humanizer-local-model/releases/tag/app-v0.3.2), which can import `.docx` and PDF files and keeps any passage you mark with **Create fact** word for word). On first run it suggests one of five sizes for your memory (Q8_0, Q6_K, Q4_K_M, Q3 or 2-bit, down to 8 GB machines; app 0.3.0 and later) and downloads it once; after that it works offline. Sizes and quality notes: [Files](#files).

**Command line, for long documents and agents:** `pipx install git+https://github.com/sgaofen/humanizer-local-model`, then `hz paper.md -o paper.out.md` (also `.txt` and `.docx`). It uses the app or a llama-server, keeps headings, code, tables and links, rewrites the prose piece by piece and flags any piece where a number went missing. See [USAGE.md, section 14](USAGE.md#14-hz-command-line-tool).

**Without the app:** pick a file below, then follow [Usage without the app](#usage-without-the-app). The full guide, with a batch script, long documents, Chinese and troubleshooting, is **[USAGE.md](USAGE.md)** ([中文](USAGE.zh.md)).

## Files

| File | Size | Peak memory (Mac) ¹ | Top-1 vs. bf16 ² | Fact check: English rewrites flagged ³ | Flagged as AI, same 60 drafts ⁴ | For |
|---|---|---|---|---|---|---|
| `humanizer-12b-Q8_0.gguf` | 12,669,630,368 bytes (12.7 GB) | 13.7 GB | 98.4% | 44 / 420 | 7 / 60 | 32 GB of memory or more. **Recommended**, best quality. |
| `humanizer-12b-Q6_K.gguf` | 10,029,799,584 bytes (10.0 GB) | about 11.0 GB | 97.7% | 56 / 420 | not measured | 16 GB. No measurable loss. |
| `humanizer-12b-Q4_K_M.gguf` | 7,625,160,864 bytes (7.6 GB) | 10.0 GB | 95.2% | 58 / 420 | not measured | About 14 GB, or when disk is tight. Slight loss; quantization-aware trained. |
| `humanizer-12b-Q3-QAT.gguf` | 5,587,794,816 bytes (5.6 GB) | 8.0 GB | 93.1% | 64 / 420 | 4 / 60 | 12 GB. Small loss; a few more fact slips in English. |
| `humanizer-12b-IQ2_XS-QAT.gguf` | 3,893,632,896 bytes (3.9 GB) | 6.2 GB | 87.4% | 70 / 420 | **0 / 60** | 8 GB; the smallest (2-bit). Lowest AI-detector score, a few more fact slips: check numbers and names. |
| `humanizer-12b-bf16.gguf` | 23,832,049,568 bytes (23.8 GB) | about 24.8 GB | 100% (reference) | 52 / 420 | 4 / 60 | Unquantised weights as one GGUF, for reference or for quantising yourself. |
| `model.safetensors` + `config.json`, `generation_config.json`, `tokenizer.json`, `tokenizer_config.json` | about 24 GB (bf16) | | | | | transformers, vLLM, converting to MLX (see [Usage without the app](#usage-without-the-app)). |
| `prompt_format.json` | tiny | | | | | The instruction and separator, verbatim. |

¹ `llama-server` (Metal) on an M5 Max with the app's settings, while rewriting; Q6_K and bf16 are estimates. Leave room for the system (the app keeps about 4 GB free).

² How often the file's most likely next token is the same as bf16's, English and Chinese averaged (llama.cpp `--kl-divergence`).

³ The strict fact judge from [Evaluation details](#evaluation-details) (GLM-5.3, one vote per rewrite), all 420 English rewrites, run on each file. For every file about 9 in 10 of the problems it lists are a single word, number or phrase. Q8_0, Q6_K and Q4_K_M are within noise of bf16; Q3 and 2-bit make a few more slips.

⁴ Originality.ai, strictest setting, the same 60 English drafts for every file. Detectors change; this is one measurement on one date.

### Quantized versions

The same GGUF files are also in **[jialinyyzz/humanizer-GGUF](https://huggingface.co/jialinyyzz/humanizer-GGUF)**; they stay here too, so existing download commands keep working. KL to bf16, the fact check and AI-detector results for every file, the full comparison with standard llama.cpp quantization and how each file was made: **[docs/QUANTIZATION.md](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/QUANTIZATION.md)**. sha256 checksums are in [USAGE.md](USAGE.md#2-pick-a-file).

## Prompt format

**Text completion, not chat.** No system prompt, no turn markers. Send exactly this text and let the model continue:

```
Rewrite the text below so it reads like a person wrote it, not a language model.

Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,
throat-clearing, and any sentence that only announces what comes next.
Prefer the concrete word over the abstract one. It is fine to sound uneven.

Every fact, number, unit, date, name and quotation must survive unchanged.

<YOUR DRAFT, with leading and trailing whitespace removed>

### Rewritten:

```

- `prompt = instr + "\n\n" + draft.strip() + "\n\n### Rewritten:\n\n"`; both strings are in `prompt_format.json`. Reproduce it byte for byte: the first 16 hex characters of `sha256(prompt for the draft "X")` must be `cc51d66b4c593fbe`.
- Use the same English instruction for Chinese drafts.
- **Stop on EOS only.** No stop strings, especially not `"###"`.
- **Sampling: temperature 1.0, top-p 0.95, nothing else** (top-k 0, min-p 0, repetition penalty 1.0). llama.cpp defaults to top-k 40 and min-p 0.05, and the bundled `generation_config.json` sets top-k 64, so switch them off explicitly.
- Context 8192 tokens for instruction + draft + rewrite. Split long documents at paragraph breaks ([USAGE.md](USAGE.md#10-long-documents)).
- **Built-in defaults:** since 2026-10-04 every GGUF file also stores these sampling settings in its metadata, so llama.cpp and apps built on it use them when a request sets none.
- **Chat front ends:** since 2026-10-04 the GGUF files carry a chat template that builds exactly this prompt from the last user message (system prompts and earlier turns are ignored). llama-server's `/v1/chat/completions` (with `--jinja`, the default in recent builds) then works, one draft per message: with the same seed it gave the same rewrite as the completion endpoint. Chat apps that use the file's template, such as LM Studio's Chat tab, should work the same way (not tested by us). Files downloaded earlier have no template, and the safetensors weights have none either.

## Usage without the app

All snippets below build the prompt from `prompt_format.json` and use the sampling above. More detail for each runtime, a script that rewrites a whole folder, and a troubleshooting table: [USAGE.md](USAGE.md).

### llama.cpp (recommended; macOS, Windows, Linux)

```bash
brew install llama.cpp            # or: winget install llama.cpp / a zip from github.com/ggml-org/llama.cpp/releases
pip install -U "huggingface_hub[cli]"
hf download jialinyyzz/humanizer humanizer-12b-Q8_0.gguf prompt_format.json --local-dir ./humanizer-model
#   16 GB machine: humanizer-12b-Q6_K.gguf instead; tight: humanizer-12b-Q4_K_M.gguf or humanizer-12b-Q3-QAT.gguf; smallest: humanizer-12b-IQ2_XS-QAT.gguf
llama-server -m ./humanizer-model/humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --host 127.0.0.1 --port 8080
```

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))

def humanize(draft: str, url: str = "http://127.0.0.1:8080") -> str:
    body = {"prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
            "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
            "n_predict": 2048}                    # no "stop": the model ends at EOS
    req = urllib.request.Request(url + "/completion", json.dumps(body).encode("utf-8"),
                                 {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=900) as r:
        return json.load(r)["content"].strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

`/completion` works with every download. `/v1/chat/completions` also works with the GGUF files uploaded on or after 2026-10-04 (built-in chat template, see above), one draft per request. With curl and jq:

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, n_predict: 2048}' \
| curl -s http://127.0.0.1:8080/completion -d @- | jq -r .content
```

### MLX (Apple silicon, mlx-lm 0.32 or newer)

```bash
pip install -U "mlx-lm>=0.32" huggingface_hub
mlx_lm.convert --hf-path jialinyyzz/humanizer --mlx-path humanizer-mlx-8bit -q --q-bits 8 --q-group-size 64
```

```python
import json
from huggingface_hub import hf_hub_download
from mlx_lm import load, generate
from mlx_lm.sample_utils import make_sampler

PF = json.load(open(hf_hub_download("jialinyyzz/humanizer", "prompt_format.json"), encoding="utf-8"))
model, tok = load("humanizer-mlx-8bit")
draft = open("draft.txt", encoding="utf-8").read()
print(generate(model, tok, prompt=PF["instr"] + "\n\n" + draft.strip() + PF["sep"], max_tokens=2048,
               sampler=make_sampler(temp=1.0, top_p=0.95)).strip())
```

Pass a plain string; never apply the chat template (the `mlx_lm.generate` CLI needs `--ignore-chat-template`).

### transformers (CUDA)

```python
import json, torch
from huggingface_hub import hf_hub_download
from transformers import AutoModelForCausalLM, AutoTokenizer

repo = "jialinyyzz/humanizer"
PF = json.load(open(hf_hub_download(repo, "prompt_format.json"), encoding="utf-8"))
tok = AutoTokenizer.from_pretrained(repo)
model = AutoModelForCausalLM.from_pretrained(repo, dtype=torch.bfloat16, device_map="auto")

draft = open("draft.txt", encoding="utf-8").read()
ids = tok(PF["instr"] + "\n\n" + draft.strip() + PF["sep"], return_tensors="pt").to(model.device)
out = model.generate(**ids, do_sample=True, temperature=1.0, top_p=0.95,
                     top_k=0,                    # switches off the top-k 64 in generation_config.json
                     max_new_tokens=2048)
print(tok.decode(out[0, ids["input_ids"].shape[1]:], skip_special_tokens=True).strip())
```

The bf16 weights are about 24 GB. Saved with transformers 5.14.1; on 4.x write `torch_dtype=` instead of `dtype=`.

### vLLM

```python
import json
from huggingface_hub import hf_hub_download
from vllm import LLM, SamplingParams

repo = "jialinyyzz/humanizer"
PF = json.load(open(hf_hub_download(repo, "prompt_format.json"), encoding="utf-8"))
llm = LLM(model=repo, dtype="bfloat16", max_model_len=8192,
          limit_mm_per_prompt={"image": 0, "audio": 0, "video": 0})   # text only
params = SamplingParams(temperature=1.0, top_p=0.95, top_k=-1, min_p=0.0,
                        repetition_penalty=1.0, max_tokens=2048)      # top_k=-1: off

drafts = [open(p, encoding="utf-8").read() for p in ["draft1.txt", "draft2.txt"]]
for r in llm.generate([PF["instr"] + "\n\n" + d.strip() + PF["sep"] for d in drafts], params):
    print(r.outputs[0].text.strip(), "\n---")
```

This mirrors how our evaluation outputs were generated. As a server: `vllm serve jialinyyzz/humanizer --dtype bfloat16 --max-model-len 8192 --generation-config vllm`, then `/v1/completions` (never `/v1/chat/completions`) with the same parameters; `--generation-config vllm` keeps the top-k 64 from `generation_config.json` out of the defaults.

### Ollama

Ollama ignores the chat template stored in the GGUF and would apply its own Gemma template, which breaks this model. Use this `Modelfile`: its template takes the last user message as the draft and builds the prompt above (rendered with Go's `text/template`, byte for byte the same; not run in Ollama by us). Needs an Ollama version that supports Gemma 4 models.

```
FROM ./humanizer-12b-Q8_0.gguf
TEMPLATE """{{- $draft := "" }}{{- range .Messages }}{{- if eq .Role "user" }}{{- $draft = .Content }}{{- end }}{{- end }}Rewrite the text below so it reads like a person wrote it, not a language model.

Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,
throat-clearing, and any sentence that only announces what comes next.
Prefer the concrete word over the abstract one. It is fine to sound uneven.

Every fact, number, unit, date, name and quotation must survive unchanged.

{{ $draft }}

### Rewritten:{{ "\n\n" }}"""
PARAMETER temperature 1.0
PARAMETER top_p 0.95
PARAMETER top_k 0
PARAMETER min_p 0
PARAMETER repeat_penalty 1.0
PARAMETER num_ctx 8192
PARAMETER num_predict 2048
```

```bash
ollama create humanizer -f Modelfile
ollama run humanizer      # one draft per message, no leading or trailing blank lines
# or raw mode, which skips the template:
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{model: "humanizer", raw: true, stream: false,
    prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    options: {temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0,
              num_ctx: 8192, num_predict: 2048}}' \
| curl -s http://127.0.0.1:11434/api/generate -d @- | jq -r .response
```

### LM Studio

Untested by us. Load the GGUF with an 8192-token context; in the model's sampling settings set Temperature 1.0, Top P 0.95, Top K 0, Min P 0, Repeat Penalty 1.0 and remove stop strings. With a file downloaded on or after 2026-10-04, the **Chat tab** should work: leave the system prompt empty and paste one draft per message (the built-in template ignores system prompts and earlier turns). With any download, start the local server and send the full prompt to the **text-completion endpoint `/v1/completions`**:

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
body = {"model": "humanizer-12b-q8_0",           # replace with the model id shown in LM Studio
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0, "max_tokens": 2048}
req = urllib.request.Request("http://127.0.0.1:1234/v1/completions", json.dumps(body).encode("utf-8"),
                             {"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req, timeout=900))["choices"][0]["text"].strip())
```

If a chat reply greets you, repeats the instruction or doesn't stop, the file has no built-in template (downloaded before 2026-10-04): download it again or use `/v1/completions`.

### More

[USAGE.md](USAGE.md) also covers: a one-shot `llama-completion` run, a script that rewrites a whole folder (splitting long files, resampling over-copied pieces, listing numbers to check), long documents, Chinese specifics, a quality checklist and troubleshooting. To verify a setup, run the self-test in [AGENTS.md](AGENTS.md#8-self-test-verify-the-install).

## Evaluation details

Evaluation set: 312 drafts (210 English, 102 Chinese), 18 genres, written from scratch by GLM-5.3, GPT-5.6 luna and Claude Sonnet (about a third each), never used in training. Two samples per draft.

**AI detection (external check only): 95% judged human.** Originality.ai, API v3, AI Allowance 0% (strictest), 2026-10-02, 210 English drafts, first sample each, bf16 weights: 11 of 210 rewrites flagged as AI.

| Model | Flagged as AI | Judged human |
|---|---|---|
| **humanizer 12B v2, this release (bf16)** | **11 / 210 (5%)** | **95%** |
| humanizer 12B v1, previous release | 26 / 210 (12%) | 88% |

Flagged less than half as often as the previous release: on the same drafts, 20 were flagged only for the previous release and 5 only for this one (paired test, p = 0.004). The `humanizer-12b-Q8_0.gguf` file measured 15 / 210 (7%), within noise of bf16 (p = 0.48).

Public baseline: the `blader/humanizer` skill (v3.1.0, 53k GitHub stars), applied by Claude Sonnet to the same 60 drafts: 60 / 60 flagged as AI (median AI score 100%). This release (bf16) on those 60: **4 / 60** flagged. Flagged as AI by genre (all 210 drafts, bf16, same setting): social posts with emoji, hashtags or "1/ 2/" threads **3 / 16**, formal policy memos **2 / 13**, paper sections 2 / 22, essays 2 / 38, work reports 1 / 20, forum answers 1 / 18, blog posts 0 / 16, Reddit posts 0 / 18, emails 0 / 35, product reviews 0 / 14 (total 11 / 210). The most templated genres are still the hardest. Detectors change; this is one measurement on one date, not a promise.

**Fact fidelity.** **376 of 420 English rewrites came back with no factual problem** from a strict LLM judge (GLM-5.3, one vote per rewrite), measured on the `humanizer-12b-Q8_0.gguf` file you download. The previous release: 369 of 420. The chart is at the [top of this page](#results).

| | **v2, this release (Q8_0 file)** | v1, previous 12B |
|---|---|---|
| No factual problem found (higher is better) | **376 / 420** | 369 / 420 |
| Dropped a format element (lower is better) | **28 / 420** | 35 / 420 |
| Median reuse, overlap with the draft (lower is better) | **0.165** | 0.19 |
| Outputs with reuse > 0.5 (lower is better) | **0.2%** | 1.0% |

Where the judge did find a problem, the fix is usually small: a second pass re-read each flagged rewrite against its draft and listed every problem, down to small wording nuances, and more than 9 in 10 of those fixes (125 of 135) are a single word or phrase (for example, the draft's "The remaining 37 complaints" came out as "The other 37% of complaints"). Chinese is still catching up with English: no factual problem in 149 of 204 Chinese rewrites (previous release: 135); where there was one, about 9 in 10 fixes (212 of 236) are a single word or phrase ("本月20日前后", around the 20th of this month, became "20号以前", before the 20th). **Still, read the result before you send it, especially numbers, dates and names.**

**Note: the fact judge is deliberately strict.** In a separate audit of the same judge model, a second judge (Claude Opus) re-read 150 rewrites it had flagged as serious (drawn from training, not from this evaluation set). On the fact the first judge pointed to, Opus agreed it was a serious error in 99, rated it minor in 41 (for example "may reduce" became "will help decrease", or a long name was shortened), and found the fact unchanged in 10. So some flags are harmless rewording. Still read numbers, dates and names before you send.

## Training

**No AI detector was used anywhere in training**: not as a reward, not as a filter, not to pick a checkpoint.

1. **SFT, 28,598 pairs** of AI draft → real human original. The human side is always real human writing (paper abstracts, government reports, student essays, company and mailing-list email, Reddit, Hacker News, Zhihu…); the AI side is a draft a frontier model wrote back from the human text.
2. **DPO, 3,918 preference pairs**, chosen only on fact fidelity and copying (LLM judge GLM-5.3).
3. **GRPO in three rounds, 500 steps in total**: 200 steps with a strict single-vote fact judge, then two rounds of 150 steps (v1 was released after the first of these, v2 after the second) (16 drafts × 8 samples per step, temperature 1.0). Reward: an LLM judge reads the whole rewrite against the draft (severe errors, invented content, changed meaning and dropped formatting cost), plus a copy penalty on 5-gram and syntactic-skeleton reuse (free below .22, then linear). Round 3 drew its drafts from a genre-balanced pool of 8,268. In all, RL produced 41,600 rewrites, each scored by an LLM judge against its draft.
4. This release (v2) = the final checkpoint of the last round.

<img src="assets/training-en.png" alt="Training pipeline" width="100%">

## Speed

Measured on an M5 Max. llama.cpp Q8_0 with Metal (what the app uses): about 36–38 tokens/s; a hundred-word email takes about 3.6 s, a Chinese email of about 300 characters about 8.5 s. MLX 8-bit: about 30 tokens/s in English and 38 tokens/s in Chinese; a hundred-word email takes about 9 s.

## Limitations

- It can still change a detail: on our set the judge found no factual problem in 376 of 420 English rewrites, and more than 9 in 10 of the fixes it did list are a single word or phrase. Proofread numbers, dates and names.
- Chinese is still catching up with English (no factual problem in 149 of 204 Chinese rewrites; about 9 in 10 fixes are a single word or phrase).
- Templated genres are still the hardest for detectors: emoji/hashtag social posts (3 / 16 flagged) and policy memos (2 / 13).
- Formatting can change: 28 of 420 outputs dropped a format element; paragraph breaks, lists and headings sometimes merge or disappear.
- Occasionally answers in the wrong language: on short, informal English drafts with technical jargon, it occasionally writes the whole rewrite in Chinese. The app (0.3.1 and later) and `hz` check the language and sample again automatically. If you call the model yourself through llama.cpp or another runtime: when an English draft comes back with more than a few Chinese characters, sample once more with the same settings.
- In casual genres it sometimes adds slang or profanity that wasn't in the draft.
- Detector results change over time. Nothing here guarantees any detector outcome.
- It is a writing tool for your own drafts. Where a school, employer or publication has rules about AI assistance, follow them.

## License

Apache License 2.0, for both the weights and the code. Fine-tuned from [google/gemma-4-12B](https://huggingface.co/google/gemma-4-12B), which Google releases under Apache 2.0. This project is not affiliated with or endorsed by Google.
