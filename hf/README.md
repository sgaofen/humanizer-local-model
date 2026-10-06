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

<img src="assets/app-showcase-en.png" alt="The humanizer app running the 12B model locally" width="100%">

# humanizer

**A 12B model that rewrites AI-written drafts (emails, essays, reports, forum posts; English and Chinese) so they read like a person wrote them.** It is trained to keep every number, unit, date, name and quote, and to add nothing. It runs locally. No AI detector was used anywhere in training.

**[Usage without the app](USAGE.md)** · [不用 App 怎么用](USAGE.zh.md) · [AGENTS.md (for AI agents)](AGENTS.md) · [GitHub](https://github.com/sgaofen/humanizer-local-model) · [Desktop app (macOS, Windows)](https://github.com/sgaofen/humanizer-local-model/releases/latest) · [Install guide](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/INSTALL.md) · [中文说明](https://github.com/sgaofen/humanizer-local-model/blob/main/README.zh.md)

> **GGUF files now have their own repo: [jialinyyzz/humanizer-GGUF](https://huggingface.co/jialinyyzz/humanizer-GGUF)**: every quantization (Q8_0, Q6_K, Q4_K_M, Q3, 2-bit) with its size, memory, KL to bf16 and quality on the task, and how they were made. The same files stay here too, so existing download commands keep working.

> **Setting this up with an AI agent?** Point it at [AGENTS.md](AGENTS.md): exact files, server command, prompt byte for byte, and a self-test.

## Quick start

**App:** download the `.dmg` (Mac with Apple silicon) or the Windows installer from [Releases](https://github.com/sgaofen/humanizer-local-model/releases/latest). On first run it suggests one of five sizes for your memory (Q8_0, Q6_K, Q4_K_M, Q3 or 2-bit, down to 8 GB machines; app 0.3.0) and downloads it once; after that it works offline. Sizes and quality notes: [Quantized versions](#quantized-versions).

**Command line, for long documents and agents:** `pipx install git+https://github.com/sgaofen/humanizer-local-model`, then `hz paper.md -o paper.out.md` (also `.txt` and `.docx`). It uses the app or a llama-server, keeps headings, code, tables and links, rewrites the prose piece by piece and flags any piece where a number went missing. See [USAGE.md, section 14](USAGE.md#14-hz-command-line-tool).

**Without the app:** pick a file below, then follow [Usage without the app](#usage-without-the-app). The full guide, with a batch script, long documents, Chinese and troubleshooting, is **[USAGE.md](USAGE.md)** ([中文](USAGE.zh.md)).

## Files

| File | Size | For |
|---|---|---|
| `humanizer-12b-Q8_0.gguf` | 12,669,630,368 bytes (about 12.7 GB) | 32 GB of memory or more. Recommended. |
| `humanizer-12b-Q6_K.gguf` | 10,029,799,584 bytes (about 10.0 GB) | 16 GB of memory. |
| `humanizer-12b-Q4_K_M.gguf` | 7,625,160,864 bytes (about 7.6 GB) | About 14 GB of memory, or when disk is tight. Quantization-aware trained. |
| `humanizer-12b-Q3-QAT.gguf` | 5,587,794,816 bytes (about 5.6 GB) | 12 GB of memory. 3-bit class, quantization-aware trained; a few more fact slips in English than Q4_K_M (see below). |
| `humanizer-12b-IQ2_XS-QAT.gguf` | 3,893,632,896 bytes (about 3.9 GB) | 8 GB of memory; the smallest. 2-bit; more fact mistakes than the larger files (see below). |
| `humanizer-12b-bf16.gguf` | 23,832,049,568 bytes (about 23.8 GB) | Unquantised weights as one GGUF, for reference or for quantising yourself. |
| `model.safetensors` + `config.json`, `generation_config.json`, `tokenizer.json`, `tokenizer_config.json` | about 24 GB (bf16) | transformers, vLLM, converting to MLX (see [Usage without the app](#usage-without-the-app)). |
| `prompt_format.json` | tiny | The instruction and separator, verbatim. |

### Quantized versions

| File | In short | Bits / type | Size | Peak memory (Mac) ¹ | KL to bf16, EN / ZH ² | Standard `llama-quantize` build, same size class: KL EN / ZH (top token same) ² | Top token same as bf16, EN / ZH ² | Fact check (GLM): rewrites flagged · spots listed ³ | Flagged as AI, same 60 drafts ⁵ |
|---|---|---|---|---|---|---|---|---|---|
| `humanizer-12b-bf16.gguf` | Reference (full precision) | 16-bit (bf16) | 23.8 GB | about 24.8 GB (est.) | 0 (reference) | | 100% (reference) | EN 52 · 160 spots <br> ZH 50 · 216 spots | 4 / 60 |
| `humanizer-12b-Q8_0.gguf` | **Best quality** | 8-bit (Q8_0) | 12.7 GB | 13.7 GB | 0.0017 / 0.0017 | same file: Q8_0 is a standard build | 98.45% / 98.25% | EN 44 · 135 spots <br> ZH 54 · 236 spots | 7 / 60 |
| `humanizer-12b-Q6_K.gguf` | No measurable loss | 6-bit (Q6_K) | 10.0 GB | about 11.0 GB (est.) | 0.0031 / 0.0035 | same file: Q6_K is a standard build | 97.95% / 97.48% | EN 56 · 153 spots <br> ZH 56 · 255 spots | not measured |
| `humanizer-12b-Q4_K_M.gguf` | Slight loss | 4-bit (Q4_K_M), quantization-aware trained | 7.6 GB | 10.0 GB | **0.0136 / 0.0146** | Q4_K_M, 7.6 GB: 0.0203 / 0.0225 (94.46% / 93.46%) | 95.62% / 94.77% | EN 58 · 162 spots <br> ZH 51 · 194 spots <br> (standard Q4_K_M: EN 56 · 163, ZH 48 · 218) | not measured |
| `humanizer-12b-Q3-QAT.gguf` | Small loss; a few more fact slips in English | 3-bit class, mixed precision (about 3.9 bits per weight) ⁴, quantization-aware trained | 5.6 GB | 8.0 GB | **0.0300 / 0.0318** ² | IQ3_XXS (3-bit), 4.7 GB: 0.138 / 0.190 (85.72% / 82.55%) | 93.63% / 92.64% | EN 64 · 210 spots <br> ZH 53 · 233 spots | 4 / 60 |
| `humanizer-12b-IQ2_XS-QAT.gguf` | Lowest AI-detector score; a few more fact slips (mostly single words or numbers): check numbers and names before sending | 2-bit (mostly IQ2_XS) ⁴, quantization-aware trained | 3.9 GB | 6.2 GB | **0.106 / 0.106** ⁴ | IQ2_XS, 3.8 GB: 0.474 / 0.769 (74.43% / 65.53%) | 87.72% / 87.07% | EN 70 · 265 spots <br> ZH 66 · 361 spots | 0 / 60 |

**What KL means:** how far a file's next-word probabilities drift from the full bf16 model, averaged over every word of real drafts and rewrites. Lower is better; 0 means identical. "Top token same" is how often the file and bf16 would pick the same most likely next word. "Standard" means a plain `llama-quantize` run of the same type with the same importance matrix our build started from, with no extra training.

The Q4_K_M was refined with quantization-aware training on 2026-10-04: same size and format, about 1/3 lower KL than the standard Q4_K_M. The 2-bit build is closer to the full model than a standard 3-bit build that is 0.8 GB larger, and Chinese, which plain 2-bit quantization hurts most, comes out the same as English. It still makes more fact mistakes than the larger files (see ³), so use it only when memory is tight and check its output with extra care.

The Q3 build (added 2026-10-05) sits between them: at 5.6 GB it is 2 GB smaller than Q4_K_M, and its KL, about 0.03 in both languages, is a fraction of a standard 3-bit IQ3_XXS (0.138 / 0.190), which is 0.9 GB smaller. Its Chinese fact check and its AI-detector result are on par with bf16. In English it makes a few more fact slips (64 of 420 rewrites flagged, against 52 for bf16 and 58 for Q4_K_M), almost all a single word or phrase, so check numbers and names before you send.

¹ Maximum resident memory of llama-server (llama.cpp, Metal) on an Apple M5 Max with the app's settings (8,192-token context, one request at a time) while rewriting. Q4_K_M, Q3 and 2-bit were measured side by side with the app's llama.cpp build on four real drafts; generation ran at about 52, 65 and 69 tokens per second. Q8_0 was measured in an earlier run on one 470-token draft, which read about 1.3 GB lower for the same file (2-bit: 4.9 GB there, 6.2 GB here), so it may need a little more than shown; Q6_K and bf16 are estimated (est.). A 32,768-token context adds about 0.4 GB. Leave room for the system and other apps (the app keeps about 4 GB free).
² llama.cpp `llama-perplexity --kl-divergence`, 30 chunks of 2,048 tokens per language (about 30,700 scored tokens each) of held-out drafts and rewrites from the evaluation set (no overlap with calibration or training data), reference = the bf16 GGUF, measured on A100 and A30 GPUs (the same file measures within about 3% across GPU models, far below the gaps between files). A full comparison with standard llama.cpp quantization and a third-party imatrix GGUF, IQ1_M to Q8_0, is on the [GGUF page](https://huggingface.co/jialinyyzz/humanizer-GGUF#how-it-compares-to-standard-quantization). Q8_0, Q6_K and Q4_K_M keep the token embeddings and output layer at 8-bit; the IQ2_XS and IQ3_XXS comparison builds use 4-bit embeddings.
³ The strict fact judge from [Results](#results) (GLM-5.3, one vote per rewrite) on all 420 English and 204 Chinese rewrites (two per draft; for bf16, 4 Chinese rewrites could not be judged). "Rewrites flagged" = rewrites it found a factual problem in; a second pass re-read each flagged rewrite against its draft and listed every problem ("spots"). Most spots are a single word, number or phrase: about 9 in 10 for every file (2-bit: 243 of 265 English, 330 of 361 Chinese). Compared draft by draft with Q8_0, Q6_K and Q4_K_M are within noise, and the quantization-aware Q4_K_M is within noise of the standard one. The 2-bit build is not: in English it was flagged on 70 rewrites against 44 for Q8_0 on the same drafts, a difference beyond noise; in Chinese 66 against 54, within noise. Against the Q4_K_M it is 70 vs. 58 and 66 vs. 51, within noise. Q3: 64 English rewrites flagged (bf16 52, Q4_K_M 58), and 191 of its 210 English spots are a single word or short phrase; in Chinese 53 (bf16 50).
⁴ The Q3 and 2-bit builds keep about 130,000 of the 262,144 vocabulary tokens: the ones English and Chinese text actually uses, plus everything needed to spell any input. Any text still encodes and decodes exactly; rare symbols, emoji and other scripts just take a few more tokens. Pruning alone moves the model by KL 0.0013 (English) / 0.0002 (Chinese), and their KL above is measured against a bf16 with the same pruned vocabulary. Bits are spread by sensitivity: the 2-bit build is mostly IQ2_XS, with 3- and 4-bit types where they matter; in the Q3 build about half the bytes are Q4_K and a fifth Q6_K, with IQ3_XXS and IQ2_XS on the least sensitive tensors (about 3.9 bits per weight on average). Then each layer is tuned with quantization-aware training and the scales are distilled from the bf16 model on English and Chinese rewriting data. The files report their types as IQ2_XS and IQ3_XXS, but neither is a plain build of that type.
⁵ The 2-bit build had the best AI-detector result of all versions: 0/60 flagged (Q8_0 7/60, bf16 4/60; Originality.ai strictest setting). A likely reason: the small drift from quantization makes the wording a bit less predictable, which detectors read as less machine-like. Q3: 4/60, the same as bf16 (3 drafts flagged only with Q3, 3 only with bf16).

sha256 checksums are in [USAGE.md](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.md#2-pick-a-file).

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

## Before and after

Drafts from the held-out evaluation set. For each we generated 8 samples with the Q8_0 file (llama.cpp, the app's settings) and picked the one that reads best among those the fact judge passed; it is shown unedited, and all 8 samples are [on GitHub](https://github.com/sgaofen/humanizer-local-model/blob/main/eval/outputs/examples-12b-Q8_0_x8.json). These are picks: across the whole set the judge flagged 44 of the 420 English rewrites, 135 problems in all, 125 of them a single word or phrase. Read the result before you send it, especially numbers, dates and names. Results over the whole set are below.

<img src="assets/compare-en-email.png" alt="Work email: draft and rewrite" width="100%">

<img src="assets/compare-zh-email.png" alt="Chinese work email: draft and rewrite" width="100%">

More examples (a forum answer, a Zhihu answer) are in the [GitHub README](https://github.com/sgaofen/humanizer-local-model#before-and-after).

## Results

Evaluation set: 312 drafts (210 English, 102 Chinese), 18 genres, written from scratch by GLM-5.3, GPT-5.6 luna and Claude Sonnet (about a third each), never used in training. Two samples per draft.

**AI detection (external check only): 95% judged human.** Originality.ai, API v3, AI Allowance 0% (strictest), 2026-10-02, 210 English drafts, first sample each, bf16 weights: 11 of 210 rewrites flagged as AI.

| Model | Flagged as AI | Judged human |
|---|---|---|
| **humanizer 12B v2, this release (bf16)** | **11 / 210 (5%)** | **95%** |
| humanizer 12B v1, previous release | 26 / 210 (12%) | 88% |

Flagged less than half as often as the previous release: on the same drafts, 20 were flagged only for the previous release and 5 only for this one (paired test, p = 0.004). The `humanizer-12b-Q8_0.gguf` file measured 15 / 210 (7%), within noise of bf16 (p = 0.48).

Public baseline: the `blader/humanizer` skill (v3.1.0, 53k GitHub stars), applied by Claude Sonnet to the same 60 drafts: 60 / 60 flagged as AI (median AI score 100%). This release (bf16) on those 60: **4 / 60** flagged. Flagged as AI by genre (all 210 drafts, bf16, same setting): social posts with emoji, hashtags or "1/ 2/" threads **3 / 16**, formal policy memos **2 / 13**, paper sections 2 / 22, essays 2 / 38, work reports 1 / 20, forum answers 1 / 18, blog posts 0 / 16, Reddit posts 0 / 18, emails 0 / 35, product reviews 0 / 14 (total 11 / 210). The most templated genres are still the hardest. Detectors change; this is one measurement on one date, not a promise.

<img src="assets/results-detector-en.png" alt="Originality.ai: 95% of rewrites judged human" width="100%">

**Fact fidelity.** **376 of 420 English rewrites came back with no factual problem** from a strict LLM judge (GLM-5.3, one vote per rewrite), measured on the `humanizer-12b-Q8_0.gguf` file you download. The previous release: 369 of 420.

| | **v2, this release (Q8_0 file)** | v1, previous 12B |
|---|---|---|
| No factual problem found (higher is better) | **376 / 420** | 369 / 420 |
| Dropped a format element (lower is better) | **28 / 420** | 35 / 420 |
| Median reuse, overlap with the draft (lower is better) | **0.165** | 0.19 |
| Outputs with reuse > 0.5 (lower is better) | **0.2%** | 1.0% |

Where the judge did find a problem, the fix is usually small: a second pass re-read each flagged rewrite against its draft and listed every problem, down to small wording nuances, and more than 9 in 10 of those fixes (125 of 135) are a single word or phrase (for example, the draft's "The remaining 37 complaints" came out as "The other 37% of complaints"). Chinese is still catching up with English: no factual problem in 149 of 204 Chinese rewrites (previous release: 135); where there was one, about 9 in 10 fixes (212 of 236) are a single word or phrase ("本月20日前后", around the 20th of this month, became "20号以前", before the 20th). **Still, read the result before you send it, especially numbers, dates and names.**

**Note: the fact judge is deliberately strict.** In a separate audit of the same judge model, a second judge (Claude Opus) re-read 150 rewrites it had flagged as serious (drawn from training, not from this evaluation set). On the fact the first judge pointed to, Opus agreed it was a serious error in 99, rated it minor in 41 (for example "may reduce" became "will help decrease", or a long name was shortened), and found the fact unchanged in 10. So some flags are harmless rewording. Still read numbers, dates and names before you send.

<img src="assets/results-fidelity-en.png" alt="Fact fidelity compared with the previous releases" width="100%">

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
- In casual genres it sometimes adds slang or profanity that wasn't in the draft.
- Detector results change over time. Nothing here guarantees any detector outcome.
- It is a writing tool for your own drafts. Where a school, employer or publication has rules about AI assistance, follow them.

## License

Apache License 2.0, for both the weights and the code. Fine-tuned from [google/gemma-4-12B](https://huggingface.co/google/gemma-4-12B), which Google releases under Apache 2.0. This project is not affiliated with or endorsed by Google.
