# Using humanizer without the app

[中文](USAGE.zh.md) · [README](https://github.com/sgaofen/humanizer-local-model#readme) · [AGENTS.md (for AI agents)](https://github.com/sgaofen/humanizer-local-model/blob/main/AGENTS.md) · [Model files on Hugging Face](https://huggingface.co/jialinyyzz/humanizer/tree/main)

This guide is for people who want to run humanizer from their own code or the command line instead of the [desktop app](https://github.com/sgaofen/humanizer-local-model/releases/latest). Every block can be copied as is.

**Just want to rewrite files?** The `hz` command does the prompt, the sampling, the splitting of long documents and the checks for you, on top of the app or a llama-server: `pipx install git+https://github.com/sgaofen/humanizer-local-model`, then `hz draft.md -o out.md`. See [section 14](#14-hz-command-line-tool).

**Contents:** [1. What makes this model different](#1-what-makes-this-model-different) · [2. Pick a file](#2-pick-a-file) · [3. llama.cpp](#3-llamacpp-recommended) · [4. MLX](#4-mlx-apple-silicon) · [5. transformers](#5-transformers-cuda) · [6. vLLM](#6-vllm) · [7. Ollama](#7-ollama) · [8. LM Studio](#8-lm-studio) · [9. Rewrite a whole folder](#9-rewrite-a-whole-folder) · [10. Long documents](#10-long-documents) · [11. Chinese](#11-chinese) · [12. Quality checklist](#12-quality-checklist) · [13. Troubleshooting](#13-troubleshooting) · [14. hz command-line tool](#14-hz-command-line-tool)

## 1. What makes this model different

humanizer is a 12B **text-completion** model fine-tuned from `google/gemma-4-12B`. It takes one AI-written draft and writes the rewrite. Four rules apply to every runtime below. Get them right and it behaves as in our evaluation; get one wrong and the output gets noticeably worse.

| Rule | Why |
|---|---|
| **It is a text-completion model, not a chat model.** Send one plain string to a completion endpoint. Chat mode works only through the chat template built into the GGUF files (since 2026-10-04): it turns the last user message into exactly this string and ignores system prompts and earlier turns. | It was trained on raw text in exactly the shape below. A generic chat template (Gemma turns, ChatML) wraps the draft in tokens it never saw during training. The safetensors weights (transformers, vLLM, MLX) have no chat template. |
| **The prompt must match byte for byte.** | A reworded instruction, a translated instruction or a missing blank line all make it worse. |
| **Stop at EOS only.** No stop strings, and especially not `###`. | The model ends by itself. A few legitimate outputs contain `###` and would be cut off. |
| **Sampling: temperature 1.0, top-p 0.95, and nothing else.** top-k off (0), min-p off (0), repetition penalty 1.0. | That is how the evaluation was run. Several runtimes switch on other samplers by default: llama.cpp uses top-k 40 and min-p 0.05, and transformers reads top-k 64 from the bundled `generation_config.json`. |

### The prompt

```
prompt = INSTR + "\n\n" + draft.strip() + "\n\n### Rewritten:\n\n"
```

`INSTR` is exactly this text (lines joined with `\n`, blank lines included, no trailing newline):

```
Rewrite the text below so it reads like a person wrote it, not a language model.

Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,
throat-clearing, and any sentence that only announces what comes next.
Prefer the concrete word over the abstract one. It is fine to sound uneven.

Every fact, number, unit, date, name and quotation must survive unchanged.
```

- `draft.strip()` removes leading and trailing whitespace.
- The separator `\n\n### Rewritten:\n\n` ends with a blank line; the model starts writing right after it.
- Use the same English instruction for Chinese drafts. Don't translate it.
- Both strings ship with the weights in `prompt_format.json` (fields `instr` and `sep`).

A prompt builder you can paste anywhere, with its self-check:

```python
import hashlib

INSTR = (
    "Rewrite the text below so it reads like a person wrote it, not a language model.\n"
    "\n"
    "Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\n"
    "throat-clearing, and any sentence that only announces what comes next.\n"
    "Prefer the concrete word over the abstract one. It is fine to sound uneven.\n"
    "\n"
    "Every fact, number, unit, date, name and quotation must survive unchanged."
)
SEP = "\n\n### Rewritten:\n\n"

def build_prompt(draft: str) -> str:
    return INSTR + "\n\n" + draft.strip() + SEP

# Fingerprint: if this fails, the prompt is wrong.
assert hashlib.sha256(build_prompt("X").encode("utf-8")).hexdigest()[:16] == "cc51d66b4c593fbe"
```

**Output length.** A rewrite is usually about as long as the draft. Allow about 2.5 times the draft's token count; the app uses `min(2048, max(256, 2.5 × draft tokens))`. The context is 8192 tokens for the instruction, the draft and the rewrite together; see [Long documents](#10-long-documents).

## 2. Pick a file

All files are in [`jialinyyzz/humanizer`](https://huggingface.co/jialinyyzz/humanizer/tree/main):

| Your memory | File | Size |
|---|---|---|
| 32 GB or more | `humanizer-12b-Q8_0.gguf` | 12,669,630,368 bytes (about 12.7 GB) |
| 16 GB | `humanizer-12b-Q6_K.gguf` | 10,029,799,584 bytes (about 10.0 GB) |
| About 14 GB, or short on disk | `humanizer-12b-Q4_K_M.gguf`, quantization-aware trained; peak memory about 10 GB | 7,625,160,864 bytes (about 7.6 GB) |
| 12 GB | `humanizer-12b-Q3-QAT.gguf`, 3-bit class; peak memory about 8 GB. A few more fact slips in English: check numbers and names | 5,587,794,816 bytes (about 5.6 GB) |
| 8 GB | `humanizer-12b-IQ2_XS-QAT.gguf`, 2-bit, the smallest; peak memory about 6.2 GB. More fact slips: check numbers and names | 3,893,632,896 bytes (about 3.9 GB) |

Also in the repo:

- `humanizer-12b-bf16.gguf` (23,832,049,568 bytes, about 23.8 GB): the unquantised 12B as one GGUF, for reference or for quantising yourself.
- `model.safetensors` (bf16, about 24 GB) with `config.json`, `generation_config.json`, `tokenizer.json`, `tokenizer_config.json` and `prompt_format.json` at the root: what transformers, vLLM and the MLX converter use.

**How much the quantised files differ from bf16.** In Q8_0, Q6_K and Q4_K_M the token embeddings and the output layer stay at 8-bit; Q6_K and Q4_K_M are also imatrix-calibrated on our own rewriting data. KL was measured over about 33,000 tokens of drafts and rewrites from the evaluation set (no overlap with the calibration data). The last column is the strict fact judge from the README on all 420 English rewrites of the evaluation set, run on each file with llama.cpp.

| File | Mean KL vs. bf16 | Top token same as bf16 | Perplexity | No factual problem (English) |
|---|---|---|---|---|
| bf16 (reference) | | | | 368 / 420 |
| Q8_0 | 0.0015 | 98.4% | +0.3% | 376 / 420 |
| Q6_K | 0.0031 | 97.7% | +0.6% | 364 / 420 |
| Q4_K_M (updated 2026-10-04, see below) | 0.0136 ¹ | 95.6% ¹ | | 362 / 420 |
| Q3 (`Q3-QAT`, added 2026-10-05) | 0.0300 ¹ | 93.6% ¹ | | 356 / 420 |
| 2-bit (`IQ2_XS-QAT`) | 0.106 ¹ | 87.7% ¹ | | 350 / 420 |

Compared draft by draft with bf16, Q8_0, Q6_K and Q4_K_M are within noise on the fact judge. Q3 and 2-bit make a few more fact slips in English (64 and 70 rewrites flagged, against 52 for bf16), mostly a single word or number; in Chinese Q3 is on par with bf16 (53 vs. 50 of 204 flagged). Full table, Chinese results and how these two were made: [README, Quantized versions](https://github.com/sgaofen/humanizer-local-model#quantized-versions) or the [GGUF repo](https://huggingface.co/jialinyyzz/humanizer-GGUF).

**Q4_K_M was refined on 2026-10-04 with quantization-aware training:** same size and format, about 1/3 lower KL to the full-precision model than a standard Q4_K_M. ¹ Measured on a larger KL set (30 blocks of English drafts and rewrites), where the standard Q4_K_M scores 0.0203 and 94.5% (Chinese: 0.0146 vs. 0.0225). On the fact judge, compared draft by draft with the standard Q4_K_M, it is within noise: 58 vs. 56 of 420 English rewrites flagged, 162 vs. 163 problems listed by the second pass, more than 9 in 10 of them a single word or phrase. Q3 and 2-bit were measured on the same larger set (Chinese: 0.0318 and 0.106; measured on A100 and A30 GPUs, which differ by about 3%).

**Download:**

```bash
pip install -U "huggingface_hub[cli]"
hf download jialinyyzz/humanizer humanizer-12b-Q8_0.gguf prompt_format.json --local-dir ./humanizer-model
# 16 GB machine: humanizer-12b-Q6_K.gguf instead of humanizer-12b-Q8_0.gguf (or humanizer-12b-Q4_K_M.gguf if disk is tight)
# less memory: humanizer-12b-Q3-QAT.gguf, or the smallest, humanizer-12b-IQ2_XS-QAT.gguf
# Slow from mainland China: put HF_ENDPOINT=https://hf-mirror.com in front of the command
wc -c ./humanizer-model/*.gguf     # Q8_0: 12669630368 bytes, Q6_K: 10029799584 bytes, Q4_K_M: 7625160864 bytes,
                                   # Q3-QAT: 5587794816 bytes, IQ2_XS-QAT: 3893632896 bytes
```

sha256 (`shasum -a 256 FILE` on macOS, `sha256sum FILE` on Linux, `certutil -hashfile FILE SHA256` on Windows):

| File | sha256 |
|---|---|
| `humanizer-12b-Q8_0.gguf` | `8d7a457b56de6530eaaf0151ccfa7550a4b20dab979737e259da9c63e960e0b0` |
| `humanizer-12b-Q6_K.gguf` | `c98f03bb9e71456181f99b0e1d3391e07ce1afc357f9db4c33d6d379b8dd9f0d` |
| `humanizer-12b-Q4_K_M.gguf` | `2229574dec5178629575ee4a153dfee7d9e924ab997d0ad9d2ac622b67e44834` |
| `humanizer-12b-Q3-QAT.gguf` | `307bbfdf66fb22bf98aaa93fe7d54a713bf167e646073dc0cf10870b1525eb94` |
| `humanizer-12b-IQ2_XS-QAT.gguf` | `383e5ca8f1f48ab5f65013adbc1965fa70d1d1afa6c45d932c34b854e38edbb5` |
| `humanizer-12b-bf16.gguf` | `47d79b44c3e15ea2540f4edb63556f2b7e456067d7dd252a42ff103a26a51be9` |

All GGUF files were replaced on 2026-10-04: their metadata now holds a chat template (for LM Studio and other chat front ends; see [section 3](#start-a-server)) and the recommended sampling defaults (temperature 1.0, top-p 0.95, top-k off, min-p off, repetition penalty 1.0), so llama.cpp uses them when a request doesn't set its own. The weights inside are byte for byte the same; only the header changed. Copies downloaded before that have the earlier sizes and checksums, and still work through the completion endpoint with the sampling settings passed explicitly. `humanizer-12b-bf16.gguf` (23,832,049,568 bytes, about 23.8 GB) was added the same day: the unquantised weights as one GGUF, for reference or for quantising yourself. `humanizer-12b-Q3-QAT.gguf` (added 2026-10-05) carries the same template and defaults.

**Is my file the newest?** Files are sometimes re-uploaded under the same name. Compare your file's SHA-256 with the one Hugging Face reports, and download again if they differ (the app does this itself: **…** → **Check for updates**):

```bash
curl -sI https://huggingface.co/jialinyyzz/humanizer/resolve/main/humanizer-12b-Q8_0.gguf | grep -i '^x-linked-etag'
shasum -a 256 ./humanizer-model/humanizer-12b-Q8_0.gguf      # Linux: sha256sum
```

## 3. llama.cpp (recommended)

Works on macOS (Metal), Windows and Linux (CUDA, Vulkan or CPU). The app itself runs llama.cpp build `b11335`; that build or a newer one is fine.

### Install

| System | Command |
|---|---|
| macOS | `brew install llama.cpp` |
| Windows | `winget install llama.cpp`, or a zip from [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases) (CUDA build for NVIDIA, Vulkan build for other GPUs) |
| Linux | a zip from [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases), or build it: `cmake -B build -DGGML_CUDA=ON && cmake --build build --config Release -j` (leave out `-DGGML_CUDA=ON` without an NVIDIA GPU) |

### Start a server

```bash
llama-server -m ./humanizer-model/humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --host 127.0.0.1 --port 8080
```

- `-c 8192`: context for instruction + draft + rewrite.
- `-np 1`: one request at a time, so the whole context goes to it (newer builds otherwise split the context across parallel slots).
- `-ngl 99`: put every layer on the GPU. Lower it if you run out of GPU memory. The log line `offloaded N/N layers` tells you where it runs.
- Without a local file: `--hf-repo jialinyyzz/humanizer --hf-file humanizer-12b-Q8_0.gguf` instead of `-m …` downloads it for you.

Ready when `curl -s http://127.0.0.1:8080/health` returns `{"status":"ok"}`.

Use the `/completion` endpoint, as below; it works with every copy of the files.

**Chat endpoint.** `/v1/chat/completions` also works if your GGUF was downloaded on or after 2026-10-04 (check the size or sha256 in [section 2](#2-pick-a-file)) and the server runs with `--jinja` (the default in recent builds). The chat template built into the file takes the last user message as the draft and builds exactly the prompt above; system prompts and earlier turns are ignored, so send one draft per request. With llama.cpp we checked that the template builds the prompt byte for byte, and that with the same seed both endpoints return the same rewrite and stop on their own. A file downloaded earlier has no chat template, and the chat endpoint then gives bad output.

```bash
jq -n --rawfile d draft.txt '{messages: [{role: "user", content: $d}],
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, max_tokens: 2048}' \
| curl -s http://127.0.0.1:8080/v1/chat/completions -H "Content-Type: application/json" -d @- \
| jq -r '.choices[0].message.content'
```

### Call it from Python (standard library only)

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))

def humanize(draft: str, url: str = "http://127.0.0.1:8080") -> str:
    body = {
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
        "n_predict": 2048,                       # no "stop": the model ends at EOS
    }
    req = urllib.request.Request(url + "/completion", json.dumps(body).encode("utf-8"),
                                 {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=900) as r:
        return json.load(r)["content"].strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

For streaming, add `"stream": true` and read the server-sent events; each event's `content` is the next piece of text.

### Call it with curl (macOS and Linux, needs jq)

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, n_predict: 2048}' \
| curl -s http://127.0.0.1:8080/completion -d @- | jq -r .content
```

### One-shot, without a server

Newer llama.cpp builds call the plain text-completion tool `llama-completion`; in older builds the same flags work with `llama-cli`. `-no-cnv` keeps it out of chat mode.

```bash
# 1) write the exact prompt to a file. The extra "\n" at the end is deliberate:
#    llama.cpp's -f drops exactly one trailing newline when it reads the file.
python3 - <<'EOF'
import json
pf = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
with open("prompt.txt", "w", encoding="utf-8", newline="") as f:
    f.write(pf["instr"] + "\n\n" + draft.strip() + pf["sep"] + "\n")
EOF

# 2) run once and print only the rewrite
llama-completion -m ./humanizer-model/humanizer-12b-Q8_0.gguf -f prompt.txt -c 8192 -n 2048 -ngl 99 \
  -no-cnv --no-display-prompt --temp 1.0 --top-p 0.95 --top-k 0 --min-p 0 --repeat-penalty 1.0
```

We use llama-server ourselves and have not tested this one-shot path. Add `--verbose-prompt` once to see the tokenised prompt: it must end with `Rewritten`, `:` and a blank line, with nothing after that.

## 4. MLX (Apple silicon)

Needs mlx-lm 0.32 or newer. The 12B has no ready-made MLX files; convert the bf16 weights to 8-bit once. This reads the 24 GB bf16 download; a Mac with 32 GB or more is the comfortable size. We have only measured the 8-bit conversion. On an M5 Max it runs at about 30 tokens/s in English and 38 in Chinese.

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
sampler = make_sampler(temp=1.0, top_p=0.95)          # top-k and min-p stay off (their defaults)

def humanize(draft: str) -> str:
    prompt = PF["instr"] + "\n\n" + draft.strip() + PF["sep"]
    return generate(model, tok, prompt=prompt, max_tokens=2048, sampler=sampler).strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

Pass a plain string to `generate()` and never call `tok.apply_chat_template`. The `mlx_lm.generate` command-line tool applies the chat template unless you add `--ignore-chat-template`; the Python API above does not.

## 5. transformers (CUDA)

The bf16 weights are about 24 GB, so you need more GPU memory than that, or `device_map="auto"` to spread the model over several GPUs. You need a transformers version with Gemma 4 support; the weights were saved with 5.14.1.

```bash
pip install -U torch transformers accelerate huggingface_hub
```

```python
import json, torch
from huggingface_hub import hf_hub_download
from transformers import AutoModelForCausalLM, AutoTokenizer

repo = "jialinyyzz/humanizer"
PF = json.load(open(hf_hub_download(repo, "prompt_format.json"), encoding="utf-8"))
tok = AutoTokenizer.from_pretrained(repo)
model = AutoModelForCausalLM.from_pretrained(repo, dtype=torch.bfloat16, device_map="auto")

def humanize(draft: str) -> str:
    ids = tok(PF["instr"] + "\n\n" + draft.strip() + PF["sep"], return_tensors="pt").to(model.device)
    out = model.generate(**ids, do_sample=True, temperature=1.0, top_p=0.95,
                         top_k=0,                # switches off the top-k 64 in generation_config.json
                         max_new_tokens=2048)
    return tok.decode(out[0, ids["input_ids"].shape[1]:], skip_special_tokens=True).strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

On transformers 4.x, write `torch_dtype=` instead of `dtype=`. `top_k=0` matters: without it you sample with top-k 64.

## 6. vLLM

The offline API below mirrors how our evaluation outputs were generated (vLLM, bf16, temperature 1.0, top-p 0.95); the evaluation additionally used its anti-copy resample.

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
prompts = [PF["instr"] + "\n\n" + d.strip() + PF["sep"] for d in drafts]
for result in llm.generate(prompts, params):
    print(result.outputs[0].text.strip(), "\n---")
```

As a server (OpenAI-compatible). `--generation-config vllm` stops vLLM from taking top-k 64 from `generation_config.json` as the default:

```bash
vllm serve jialinyyzz/humanizer --dtype bfloat16 --max-model-len 8192 --generation-config vllm
```

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{model: "jialinyyzz/humanizer",
    prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: -1, min_p: 0, repetition_penalty: 1.0, max_tokens: 2048}' \
| curl -s http://127.0.0.1:8000/v1/completions -H "Content-Type: application/json" -d @- \
| jq -r '.choices[0].text'
```

Use `/v1/completions`, never `/v1/chat/completions`. We have not tested the server path ourselves.

## 7. Ollama

Ollama doesn't use the chat template stored in the GGUF; it needs its own template in the `Modelfile`. You need an Ollama version whose engine supports Gemma 4 models. We have not run Ollama ourselves: we rendered the template below with Go's `text/template` (the engine Ollama templates are written for) and got exactly the prompt from [section 1](#1-what-makes-this-model-different), but we have not tried it in Ollama.

`Modelfile`, next to the GGUF. The template takes the last user message as the draft and wraps it exactly as in section 1; system prompts and earlier turns are ignored, so every message is rewritten on its own.

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
ollama show humanizer --modelfile      # check that no "PARAMETER stop" lines were added
ollama run humanizer                   # then paste one draft per message
```

The `{{ "\n\n" }}` at the end is the blank line after `### Rewritten:`, written so that it survives even if the template's trailing whitespace is trimmed.

**Chat mode** (`ollama run`, `/api/chat`): send the draft as the user message. Ollama templates can't trim whitespace, so leave out leading and trailing blank lines (in code, send `draft.strip()`); otherwise the prompt is no longer byte for byte the one the model was trained on.

**Raw mode** skips the template and works with any Modelfile. Call `/api/generate` with `"raw": true` and the full prompt (instruction + draft + separator):

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{model: "humanizer", raw: true, stream: false,
    prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    options: {temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0,
              num_ctx: 8192, num_predict: 2048}}' \
| curl -s http://127.0.0.1:11434/api/generate -d @- | jq -r .response
```

Python:

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
body = {"model": "humanizer", "raw": True, "stream": False,
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "options": {"temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
                    "num_ctx": 8192, "num_predict": 2048}}
req = urllib.request.Request("http://127.0.0.1:11434/api/generate", json.dumps(body).encode("utf-8"),
                             {"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req, timeout=900))["response"].strip())
```

Ollama's built-in Gemma templates and a Modelfile without the `TEMPLATE` above both break this model in chat mode.

## 8. LM Studio

We have not tested LM Studio ourselves. LM Studio uses the chat template stored in the GGUF. The files uploaded on or after 2026-10-04 carry one that builds exactly the prompt from [section 1](#1-what-makes-this-model-different) (we checked it with llama.cpp, see [section 3](#start-a-server)); files downloaded earlier don't, so download them again or use the completion endpoint in step 4.

1. Load `humanizer-12b-Q8_0.gguf` (or Q6_K / Q4_K_M / Q3-QAT / IQ2_XS-QAT). Set the context length to 8192 when loading.
2. In the model's sampling settings, set **Temperature 1.0, Top P 0.95, Top K 0, Min P 0, Repeat Penalty 1.0** and remove any stop strings. Leave the prompt template as it came with the file.
3. **Chat tab:** leave the system prompt empty (the template ignores it) and paste one draft per message. Each message is rewritten on its own; earlier turns are not sent to the model. The server's `/v1/chat/completions` works the same way.
4. **Text completion** (works with any download): start the local server (Developer tab) and send the full prompt (instruction + draft + separator) to **`/v1/completions`**:

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
body = {"model": "humanizer-12b-q8_0",         # replace with the model id shown in LM Studio
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
        "max_tokens": 2048}
req = urllib.request.Request("http://127.0.0.1:1234/v1/completions", json.dumps(body).encode("utf-8"),
                             {"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req, timeout=900))["choices"][0]["text"].strip())
```

If a chat reply starts with "Sure", repeats the instruction or doesn't stop, the file has no built-in template (downloaded before 2026-10-04) or the prompt template was changed in the model settings: download the file again, reset the template, or use `/v1/completions`. If your LM Studio version ignores `top_k`, `min_p` or `repeat_penalty` in the request, the settings from step 2 apply.

## 9. Rewrite a whole folder

`humanize_folder.py` rewrites every `.txt` in one folder into another folder through a running llama-server ([section 3](#start-a-server)). Standard library only.

- Long files are split at blank lines into pieces of at most `--max-tokens` draft tokens; the pieces are rewritten one by one and joined with a blank line.
- If a rewrite copies more than `--max-copy` (default 0.35) of a piece, the piece is sampled once more and the less-copied version is kept.
- Numbers that appear in a draft but not in its rewrite go into `report.tsv` for you to check.
- Files already in the output folder are skipped, so you can stop and resume.

```bash
python3 humanize_folder.py drafts/ rewrites/
python3 humanize_folder.py drafts/ rewrites/ --url http://127.0.0.1:8080 --max-tokens 1500
```

```python
#!/usr/bin/env python3
"""Rewrite every .txt file in a folder with humanizer, through a running llama-server.

    python3 humanize_folder.py drafts/ rewrites/
    python3 humanize_folder.py drafts/ rewrites/ --url http://127.0.0.1:8080 --max-tokens 1500

Standard library only. Long files are split at blank lines into pieces of at most --max-tokens
draft tokens; each piece is rewritten on its own and the pieces are joined with a blank line.
A piece whose rewrite copies more than --max-copy of the draft is sampled once more and the
less-copied version is kept. Numbers that appear in a draft but not in its rewrite are listed in
report.tsv so you can check them by hand. Files already in the output folder are skipped.
"""
import argparse, hashlib, json, math, os, re, sys, urllib.request

INSTR = (
    "Rewrite the text below so it reads like a person wrote it, not a language model.\n"
    "\n"
    "Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\n"
    "throat-clearing, and any sentence that only announces what comes next.\n"
    "Prefer the concrete word over the abstract one. It is fine to sound uneven.\n"
    "\n"
    "Every fact, number, unit, date, name and quotation must survive unchanged."
)
SEP = "\n\n### Rewritten:\n\n"


def build_prompt(draft):
    return INSTR + "\n\n" + draft.strip() + SEP


assert hashlib.sha256(build_prompt("X").encode("utf-8")).hexdigest()[:16] == "cc51d66b4c593fbe"


def post(url, path, body, timeout=1800):
    req = urllib.request.Request(url.rstrip("/") + path, json.dumps(body).encode("utf-8"),
                                 {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.load(r)


def n_tokens(url, text):
    return len(post(url, "/tokenize", {"content": text})["tokens"])


def split_long(url, text, max_tokens):
    """Group paragraphs (split at blank lines) into pieces of at most max_tokens tokens."""
    paras = [p.strip() for p in re.split(r"\n\s*\n", text.strip()) if p.strip()]
    pieces, cur = [], []
    for p in paras:
        if cur and n_tokens(url, "\n\n".join(cur + [p])) > max_tokens:
            pieces.append("\n\n".join(cur))
            cur = []
        cur.append(p)
    if cur:
        pieces.append("\n\n".join(cur))
    return pieces


def rewrite(url, draft, ctx):
    prompt = build_prompt(draft)
    room = ctx - n_tokens(url, prompt) - 16
    n_predict = max(256, min(math.ceil(n_tokens(url, draft) * 2.5), room))
    body = {"prompt": prompt, "n_predict": n_predict, "temperature": 1.0, "top_p": 0.95,
            "top_k": 0, "min_p": 0, "repeat_penalty": 1.0}            # no "stop": ends at EOS
    return post(url, "/completion", body)["content"].strip()


# Rough copy ratio: share of the rewrite's 5-grams (words; single CJK characters) found in the draft.
UNIT = re.compile(r"[㐀-鿿]|[^\W_㐀-鿿]+|[^\w\s]")


def copy_ratio(draft, out, n=5):
    a = [t.lower() for t in UNIT.findall(draft)]
    b = [t.lower() for t in UNIT.findall(out)]
    seen = {tuple(a[i:i + n]) for i in range(len(a) - n + 1)}
    grams = [tuple(b[i:i + n]) for i in range(len(b) - n + 1)]
    return sum(g in seen for g in grams) / len(grams) if grams else 0.0


NUM = re.compile(r"\d+(?:[.,:/]\d+)*")


def missing_numbers(draft, out):
    have = {m.replace(",", "") for m in NUM.findall(out)} | set(re.findall(r"\d+", out))
    return sorted({m.replace(",", "") for m in NUM.findall(draft)} - have)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("src"); ap.add_argument("dst")
    ap.add_argument("--url", default="http://127.0.0.1:8080")
    ap.add_argument("--ctx", type=int, default=8192, help="the -c you gave llama-server")
    ap.add_argument("--max-tokens", type=int, default=1500, help="max draft tokens per piece")
    ap.add_argument("--max-copy", type=float, default=0.35, help="resample a piece once above this")
    a = ap.parse_args()
    os.makedirs(a.dst, exist_ok=True)
    files = sorted(f for f in os.listdir(a.src) if f.lower().endswith(".txt"))
    report = open(os.path.join(a.dst, "report.tsv"), "a", encoding="utf-8")
    for i, name in enumerate(files, 1):
        out_path = os.path.join(a.dst, name)
        if os.path.exists(out_path):
            print(f"[{i}/{len(files)}] {name}: already done, skipped"); continue
        draft = open(os.path.join(a.src, name), encoding="utf-8").read()
        if not draft.strip():
            continue
        parts = []
        for piece in split_long(a.url, draft, a.max_tokens):
            out = rewrite(a.url, piece, a.ctx)
            c = copy_ratio(piece, out)
            if c > a.max_copy:
                out2 = rewrite(a.url, piece, a.ctx)
                c2 = copy_ratio(piece, out2)
                if c2 < c:
                    out, c = out2, c2
            parts.append(out)
        text = "\n\n".join(parts)
        with open(out_path, "w", encoding="utf-8", newline="") as f:
            f.write(text + "\n")
        miss = missing_numbers(draft, text)
        report.write(f"{name}\t{len(parts)} piece(s)\tcopy {copy_ratio(draft, text):.2f}\tcheck numbers: {' '.join(miss) or '-'}\n")
        report.flush()
        print(f"[{i}/{len(files)}] {name}: {len(parts)} piece(s), copy {copy_ratio(draft, text):.2f}"
              + (f", check numbers {miss}" if miss else ""))


if __name__ == "__main__":
    main()
```

The copy ratio here is a rough measure (share of the rewrite's 5-word or 5-character runs found in the draft), not the exact metric from our evaluation, and the second sample has no anti-copy penalty, unlike the evaluation's resample.

## 10. Long documents

- The context is **8192 tokens** for the instruction, the draft and the rewrite together. Since a rewrite is about as long as its draft, keep each draft piece well under half of that.
- Split at **paragraph boundaries** (blank lines), never mid-sentence, and rewrite the pieces separately. The batch script above does this; its default piece size is 1,500 tokens.
- The model was trained and evaluated on single emails, posts, essays and report sections of a few hundred words. Pieces of that size work best.
- Each piece is rewritten without seeing the others, so tone can shift a little between pieces and a fact can't move from one piece to another. Read the joined result once from top to bottom.
- Headings, code blocks and bullet lists are often dropped or turned into prose. Rewrite only the prose and keep headings and code yourself; [`humanizer/markdown_guard.py`](https://github.com/sgaofen/humanizer-local-model/blob/main/humanizer/markdown_guard.py) does this block by block.
- **[`hz`](#14-hz-command-line-tool) does all of this in one command**, for `.md`, `.txt` and `.docx`: it keeps headings, code, tables and links, splits the prose into pieces of about 350 words (600 Chinese characters) without crossing a heading, and checks every piece.

## 11. Chinese

- Use **the same English instruction**; don't translate it.
- Chinese is **still catching up with English**: our fact judge found no factual problem in 149 of 204 Chinese rewrites; where it found one, about 9 in 10 fixes are a single word or phrase.
- Chinese rewrites copy the draft more often. With the Q8_0 file, 14 of 204 Chinese rewrites copied more than 35% of the draft, against 1 of 420 English ones. If a rewrite looks too close to the draft, sample again; the batch script does this automatically.
- **Numbers change form** more often in Chinese: Chinese numerals become digits (三 → 3) and dates get reformatted (6月14日 → 6.14). Digit-only checks, like the one in the app and in the batch script, can't see this. Check the numbers by reading.
- Full-width and half-width punctuation may switch, and greeting, body and sign-off lines are sometimes merged into one paragraph. Fix the layout before sending.
- Speed is similar to English. With llama.cpp Q8_0 on an M5 Max, a Chinese email of about 300 characters takes about 8.5 seconds.

## 12. Quality checklist

- **Read the rewrite once.** Check every number, date, unit and name, and the direction of every claim (who did what, more or less, before or after). On our evaluation set a strict judge found no factual problem in 376 of 420 English rewrites; where it found one, more than 9 in 10 fixes are a single word or phrase, like "37 complaints" becoming "37% of complaints".
- **Restore formatting** you need: subject lines, lists, headings and sign-offs are sometimes dropped (28 of 420 outputs).
- **Too close to the draft? Sample again.** Each run is a fresh sample.
- **Casual genres can drift in register.** In Reddit-style posts it sometimes adds slang or profanity that wasn't in the draft; edit it out.
- **No detector guarantee.** Our detector numbers are one measurement on one date with one detector; templated genres (emoji or hashtag social posts, policy memos) are still the most often flagged. Nothing here promises any detector outcome.
- It is a writing tool for your own drafts. Where a school, employer or publication has rules about AI assistance, follow them.

## 13. Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| Output starts with "Sure", "Here is…", repeats the instruction, or doesn't stop | A generic chat template is in use: a GGUF downloaded before 2026-10-04 (no built-in template), a template changed in the app's settings, Ollama without the Modelfile from [section 7](#7-ollama), or a chat endpoint on the safetensors weights | Download the GGUF again, or use a completion endpoint (`/completion`, `/v1/completions`, Ollama `raw: true`) with the exact prompt from [section 1](#1-what-makes-this-model-different) |
| Output contains `<start_of_turn>`, `<end_of_turn>` or similar markers | Same: chat formatting | Same fix |
| Output stops at `###` or very early | A stop string is set | Remove all stop strings; rely on EOS |
| An English draft comes back in Chinese (occasionally, on short informal English drafts with technical jargon) | Sampling luck | Sample again with the same settings when the rewrite of an English draft has more than a few Chinese characters. The app (0.3.1 and later) and `hz` do this automatically, up to 3 times |
| Output is almost the same as the draft | Sampling luck, or temperature too low | Check temperature 1.0 and sample again |
| Rambling, odd word choices, or repeated phrases | Wrong samplers (llama.cpp's default top-k 40 / min-p 0.05, the top-k 64 from `generation_config.json`, or a repetition penalty) | Set top-k 0, min-p 0, repetition penalty 1.0 explicitly |
| Rewrite cut off mid-sentence | Output limit or context too small | Raise `n_predict` / `max_tokens`; give llama-server `-c 8192 -np 1`; split long drafts |
| Out of memory while loading | File too large for your RAM or VRAM | Use Q6_K (16 GB), Q4_K_M (about 14 GB), Q3-QAT (12 GB) or the 2-bit IQ2_XS-QAT (8 GB); lower `-ngl` |
| Very slow | Running on the CPU | Look for `offloaded N/N layers` in the llama.cpp log; install the Metal, CUDA or Vulkan build |
| 404 when downloading | Wrong file name | Use the names in [section 2](#2-pick-a-file) |
| Self-test fingerprint fails | The prompt builder differs from training | Copy `build_prompt` from [section 1](#the-prompt) |

**Self-test.** [AGENTS.md, section 8](https://github.com/sgaofen/humanizer-local-model/blob/main/AGENTS.md#8-self-test-verify-the-install) has a standard-library script that sends a real draft from the evaluation set to llama-server and checks the prompt format, the endpoint, chat-template leaks and copying. It prints `PASS` when the setup is right.

## 14. hz command-line tool

`hz` rewrites a draft or a whole document in one command and works the same for people and AI agents. It talks to the [app](https://github.com/sgaofen/humanizer-local-model/releases/latest) or to a llama-server ([section 3](#start-a-server)), builds the exact prompt, uses the evaluated sampling, splits long documents, keeps their structure, and checks every piece. Python 3.8 or newer, standard library only; `.docx` support is optional.

### Install

```bash
pipx install git+https://github.com/sgaofen/humanizer-local-model
pipx inject humanize-model python-docx            # only if you need .docx

# or with pip, in any environment:
pip install git+https://github.com/sgaofen/humanizer-local-model
pip install "humanize-model[docx] @ git+https://github.com/sgaofen/humanizer-local-model"   # with .docx support

# from a clone, without installing:
python3 -m humanizer.hz draft.txt
```

It installs one command, `hz`. If you already have another program called `hz`, the one that comes first in your `PATH` wins.

### Use

```bash
hz draft.txt                          # print the rewrite to stdout
hz < draft.txt > rewrite.txt
hz paper.md -o paper.out.md           # long Markdown: structure kept, prose rewritten in pieces
hz report.docx -o report.out.docx     # without -o it writes report.hz.docx
hz paper.md --json > report.json      # per-piece stats; with -o the text goes to the file
hz paper.md --dry-run                 # show how it will be split; no model needed
hz paper.md --server http://127.0.0.1:8080   # a specific llama-server
```

Progress goes to stderr, one line per piece. `--quiet` hides it; the list of pieces to check is always printed.

### Where it finds the model

1. `--server URL`: that llama-server and nothing else. (If the address is the app's, it talks to the app.)
2. Otherwise the **app**, if it is running and ready. It reads the app's port from the app's `instance.json`, and falls back to `http://127.0.0.1:47615`. Requests go to `POST /api/completion` with the `X-Humanizer: 1` header the app requires.
3. Otherwise a **llama-server** at `http://127.0.0.1:8080` (`POST /completion`).
4. Otherwise, on macOS, if the app is installed but closed: `hz` starts it in the background (`open -a Humanizer`) and waits up to 3 minutes for the model to load, printing progress.
5. Otherwise it stops with exit code 3 and prints how to install the app or start llama-server.

If the app is open but has no model yet (first run), `hz` tells you to pick one in the app first.

### How a document is split

- Blank lines separate blocks. These are **kept exactly as written** and never sent to the model: Markdown headings (`#`, underlined, or a line that is only `**bold**`), fenced and indented code, tables, lines that are only images or links, HTML blocks and comments, `$$…$$` and `\[…\]` math, block quotes, horizontal rules, YAML front matter, footnote and link definitions, a short line ending in a colon right before a list, table or code block ("Key points:"), and everything under a heading called References, Bibliography, Works Cited, Sources or 参考文献.
- Prose paragraphs are grouped in order into pieces of at most about **350 English words or 600 Chinese characters** (`--max-words`, `--max-chars`). A piece never crosses a heading or any kept block, and pieces in one stretch of prose are made about the same size.
- A paragraph longer than that is cut at sentence ends; its rewritten pieces are joined back into one paragraph.
- **Lists:** each item keeps its bullet or number and a leading `**Label:**`. An item of at least about 15 words (26 Chinese characters) is rewritten on its own; shorter items are kept. Short items are where the model is weakest, so read them.
- The rewritten pieces go back in their original places, with the original blank lines between blocks. Inline formatting inside prose (bold, inline code, links) is up to the model and is sometimes dropped.

### Checks and the retry

Each rewritten piece is checked. If it has one of these problems, the piece is rewritten once more (`--retries`) and the version with fewer problems is kept:

| Problem | Meaning |
|---|---|
| `missing_numbers` | A number in the draft is not in the rewrite. Numbers are normalized first: `1,250` = `1250`, `3.50` = `3.5`, `07` = `7`, `480k` = `480,000`, `31.7万` = `317,000`, and a number written as a word in the rewrite (`three`, `两`) counts. |
| `copy` | More than half of the rewrite copies the draft (`--max-copy`, default 0.5): share of the rewrite's word 5-grams, or 6-character grams for Chinese, that also appear in the draft. |
| `missing_urls` | A link in the draft is not in the rewrite. |
| `empty`, `truncated` | Nothing came back, or the output hit the length limit. |
| `too_short`, `too_long` | The rewrite is under 35% or over 175% of the draft's length. On a real run, outputs of normal pieces were 0.7 to 1.5 times the draft; the ones above 1.75 had added a made-up paragraph, a made-up figure, or the same sentence twice. |
| `repeated` | Two sentences of the rewrite say nearly the same thing, and the draft had no such pair. |
| `markup` | The rewrite contains an HTML tag the draft doesn't have (on real runs, a stray `<p>` after a short list item). |

`added_numbers`, numbers in the rewrite that are not in the draft, are **reported but never retried**: the model sometimes does correct arithmetic ("cut costs from $480k to $305k" became "saved $175k"), and sometimes invents a figure. Either way, look at them.

**Wrong language.** Before these checks, a rewrite in the wrong language (an English draft written in Chinese, or a Chinese draft written in English; it only counts characters) is sampled again with the same settings, up to 3 times. `pieces[].language_resampled` counts those extra samples; if every sample is in the wrong language, the piece is flagged with `language`.

Pieces that still have a problem after the retry are listed on stderr with their line number (`.docx`: paragraph number) and, with `--json`, marked `"flagged": true`. The exit code is still 0.

### `--json` output

From a real run on a 1,200-word English Markdown article through the app (one of its 21 pieces shown):

```json
{
  "hz": "0.1.0", "input": "article.md", "output": "article.out.md", "format": "text",
  "backend": {"kind": "app", "url": "http://127.0.0.1:47615", "model": "q8"},
  "summary": {"pieces": 21, "retried": 0, "flagged": 1, "seconds": 41.4,
              "words_in": 1035, "words_out": 1153, "copy_rate": 0.05},
  "kept": {"heading": 8, "intro": 2, "table": 1, "code": 1},
  "pieces": [
    {"id": 15, "kind": "prose", "line": 65, "words_in": 102, "words_out": 111,
     "copy_rate": 0.036, "missing_numbers": [], "missing_urls": [], "added_numbers": ["175k"],
     "truncated": false, "retried": false, "chosen": 1, "seconds": 4.4,
     "flagged": true, "issues": ["added_numbers"],
     "attempts": [{"copy_rate": 0.036, "missing_numbers": [], "issues": ["added_numbers"], "seconds": 4.4}],
     "draft_start": "The results of the migration have exceeded our initial expec"}
  ]
}
```

| Field | Meaning |
|---|---|
| `summary.pieces` / `retried` / `flagged` | Pieces sent to the model; how many were rewritten a second time; how many still have a problem |
| `summary.copy_rate` | Copy rate of all rewritten prose against all of its drafts |
| `kept` | Blocks kept as they were, by kind (`short` = prose too short to rewrite; `list item` = short list items) |
| `pieces[].line` (`.docx`: `paragraph`) | Where the piece starts in the input, 1-based |
| `pieces[].kind` | `prose` (one or more paragraphs), `item` (a list item), `sentences` (part of an over-long paragraph), `paragraph` (`.docx`) |
| `pieces[].words_in` / `words_out` | Size of draft and rewrite; a Chinese character counts as one word |
| `pieces[].copy_rate` | Copy rate of the chosen version, 0 to 1 |
| `pieces[].missing_numbers` / `missing_urls` / `added_numbers` | As in the table above, spelled as in the draft (missing) or the rewrite (added) |
| `pieces[].retried` / `chosen` / `attempts` | Whether it was rewritten twice, which try was kept, and each try's checks |
| `pieces[].seconds` | Model time for this piece, all tries together |
| `pieces[].language_resampled` | Extra samples taken because a rewrite came back in the wrong language (usually 0) |
| `pieces[].flagged` / `issues` | Whether the kept version still has a problem, and which |
| `text` | The whole result, only when there is no `-o` and the input is not `.docx` |

### `.docx` files

- Needs `python-docx` (see Install). Body paragraphs are rewritten one by one. Headings (Heading, Title and Subtitle styles, or an outline level), tables, empty paragraphs, captions, quotes, code styles, the table of contents and everything under a References heading are left alone. Headers, footers, footnotes and text boxes are not touched.
- A paragraph that contains a hyperlink, an image, a field, a footnote reference, tracked changes or an equation is left alone too, so nothing in it is lost.
- The rewrite is written into the paragraph's **first run** and the other runs are emptied. The paragraph keeps its style and numbering, but **bold, italic or other formatting on part of a paragraph is lost**; the whole paragraph takes the first run's formatting.

### Options

| Option | Default | Meaning |
|---|---|---|
| `-o FILE` | stdout (`.docx`: `NAME.hz.docx`) | Where to write the result. It refuses to overwrite the input. |
| `--server URL` | | Use this llama-server only |
| `--app URL` | auto | The app's address, if it is not the usual one |
| `--json` | off | Print the JSON report on stdout |
| `-q`, `--quiet` | off | No progress lines |
| `--dry-run` | off | Show the pieces and the kept blocks; no model is called |
| `--max-words` / `--max-chars` | 350 / 600 | Piece size for English / Chinese |
| `--max-copy` | 0.5 | Copy rate that triggers a retry |
| `--retries` | 1 | Extra tries for a piece with a problem (0 = none) |
| `--no-launch` | off | Don't start the app if it is closed |

Exit codes: 0 done (also when some pieces are flagged), 1 error during the run (nothing is written), 2 bad input or arguments, 3 no model found.

### Limits

- The checks cover numbers, links, copying, length, repetition and stray HTML tags. **A changed word, name or meaning is not caught.** On a real Chinese run, "all 48 stores" became "48 stores in the province" and "from time to time" became "often", with every number intact. Read the result.
- Each piece is rewritten without seeing the others, so tone can shift a little between pieces.
- Short list items are the weakest part: the model sometimes drops the subject of a short item or repeats it.
- Chinese numerals written as characters are only partly understood, so a number that changes between 三 and 3 can slip through either way.
- Speed with the app on an M5 Max (Q8_0): a 1,200-word English Markdown article, 21 pieces, 41 to 54 seconds; a Chinese report of about 700 characters, 6 pieces, 13 to 17 seconds.
- No AI detector result is promised.
