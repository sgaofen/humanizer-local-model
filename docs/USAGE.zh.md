# 不用 App 怎么用 humanizer

[English](USAGE.md) · [README](https://github.com/sgaofen/humanizer-local-model/blob/main/README.zh.md) · [AGENTS.md（给 AI Agent）](https://github.com/sgaofen/humanizer-local-model/blob/main/AGENTS.md) · [Hugging Face 上的模型文件](https://huggingface.co/jialinyyzz/humanizer/tree/main)

这份文档写给不想用[桌面 App](https://github.com/sgaofen/humanizer-local-model/releases/latest)、要在自己的代码或命令行里跑 humanizer 的人。每个代码块都可以直接复制。

**只是想改写文件**？命令行工具 `hz` 替你处理提示词、采样参数、长文切块和检查，后端用 App 或 llama-server 都行：`pipx install git+https://github.com/sgaofen/humanizer-local-model`，然后 `hz draft.md -o out.md`。见[第 14 节](#14-命令行工具-hz)。

**目录**：[1. 这个模型哪里特殊](#1-这个模型哪里特殊) · [2. 选哪个文件](#2-选哪个文件) · [3. llama.cpp](#3-llamacpp推荐) · [4. MLX](#4-mlxapple-芯片) · [5. transformers](#5-transformerscuda) · [6. vLLM](#6-vllm) · [7. Ollama](#7-ollama) · [8. LM Studio](#8-lm-studio) · [9. 批量改写一个文件夹](#9-批量改写一个文件夹) · [10. 长文](#10-长文) · [11. 中文](#11-中文) · [12. 质量检查清单](#12-质量检查清单) · [13. 排错](#13-排错) · [14. 命令行工具 hz](#14-命令行工具-hz)

## 1. 这个模型哪里特殊

humanizer 是一个 12B 的**文本续写**模型，由 `google/gemma-4-12B` 微调而来：给它一篇 AI 写的草稿，它接着写出改写。下面四条规则对所有运行方式都成立。做对了，效果和我们的评测一致；错一条，效果就会明显变差。

| 规则 | 原因 |
|---|---|
| **它是文本续写模型，不是聊天模型**。把一个纯文本字符串发到续写接口。聊天模式只能靠 GGUF 文件里自带的对话模板（2026-10-04 起才有）：它把最后一条用户消息拼成和下面一模一样的字符串，系统提示词和之前的对话都不用。 | 它训练时见到的就是下面这种原始文本。通用的聊天模板（Gemma 轮次、ChatML）会给草稿套上它训练时从没见过的标记。safetensors 权重（transformers、vLLM、MLX）没有对话模板。 |
| **提示词必须逐字一致。** | 指令改写过、翻译过，或者少一个空行，效果都会变差。 |
| **只靠 EOS 停**。不要设停止符，尤其不要用 `###`。 | 模型会自己结束。少数正常输出里本来就有 `###`，会被截断。 |
| **采样：temperature 1.0、top-p 0.95，别的都关掉**。top-k 关（0），min-p 关（0），重复惩罚 1.0。 | 评测就是这样跑的。好几个运行环境默认会开别的采样：llama.cpp 默认 top-k 40、min-p 0.05；transformers 会从随权重附带的 `generation_config.json` 里读到 top-k 64。 |

### 提示词

```
prompt = INSTR + "\n\n" + draft.strip() + "\n\n### Rewritten:\n\n"
```

`INSTR` 就是下面这段原文（各行用 `\n` 连接，空行也算，结尾不带换行）：

```
Rewrite the text below so it reads like a person wrote it, not a language model.

Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,
throat-clearing, and any sentence that only announces what comes next.
Prefer the concrete word over the abstract one. It is fine to sound uneven.

Every fact, number, unit, date, name and quotation must survive unchanged.
```

- `draft.strip()` 是去掉草稿首尾的空白。
- 分隔符 `\n\n### Rewritten:\n\n` 结尾是一个空行，模型从这之后开始写。
- 中文草稿也用这段英文指令，**不要翻译**。
- 这两段字符串随权重放在 `prompt_format.json` 里（字段 `instr` 和 `sep`）。

一个可以贴到任何地方的提示词拼接函数，带自检：

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

# 指纹:这一行报错就说明提示词拼错了
assert hashlib.sha256(build_prompt("X").encode("utf-8")).hexdigest()[:16] == "cc51d66b4c593fbe"
```

**输出长度**。改写通常和草稿差不多长。输出上限留草稿 token 数的 2.5 倍左右；App 用的是 `min(2048, max(256, 2.5 × 草稿 token 数))`。指令、草稿和改写加起来共用 8192 token 的上下文，见[长文](#10-长文)。

## 2. 选哪个文件

文件都在 [`jialinyyzz/humanizer`](https://huggingface.co/jialinyyzz/humanizer/tree/main)：

| 内存 | 文件 | 大小 |
|---|---|---|
| 32 GB 及以上 | `humanizer-12b-Q8_0.gguf` | 12,669,630,368 字节（约 12.7 GB） |
| 16 GB | `humanizer-12b-Q6_K.gguf` | 10,029,799,584 字节（约 10.0 GB） |
| 14 GB 左右，或硬盘紧张 | `humanizer-12b-Q4_K_M.gguf`，量化感知训练；峰值内存约 10 GB | 7,625,160,864 字节（约 7.6 GB） |
| 12 GB | `humanizer-12b-Q3-QAT.gguf`，3 bit 档；峰值内存约 8 GB。英文事实小错稍多，核对数字和名字 | 5,587,794,816 字节（约 5.6 GB） |
| 8 GB | `humanizer-12b-IQ2_XS-QAT.gguf`，2 bit，最小；峰值内存约 6.2 GB。事实小错更多，核对数字和名字 | 3,893,632,896 字节（约 3.9 GB） |

仓库里还有：

- `humanizer-12b-bf16.gguf`（23,832,049,568 字节，约 23.8 GB）：不量化的 12B，单个 GGUF，给需要参照或想自己量化的人。
- 根目录的 `model.safetensors`（bf16，约 24 GB），以及 `config.json`、`generation_config.json`、`tokenizer.json`、`tokenizer_config.json`、`prompt_format.json`：transformers、vLLM 和 MLX 转换都用它们。

**量化版和 bf16 差多少**。量化文件认为最可能的下一个词和 bf16 相同的比例（中英文平均）：Q8_0 98.4%、Q6_K 97.7%、Q4_K_M 95.2%、Q3 93.1%、2-bit 87.4%。Q4_K_M、Q3 和 2-bit 做过量化感知训练，并从 bf16 蒸馏过，所以比同样大小的普通 llama.cpp 量化更接近完整模型。用 README 里那个从严的事实判官看，Q8_0、Q6_K、Q4_K_M 和 bf16 的差别都在噪声范围内；Q3 和 2-bit 英文事实小错稍多（420 篇改写里标出 64 篇、70 篇，bf16 是 52 篇），多是一个词或一个数字。和普通量化的对比、KL、完整的事实检查表以及这些文件是怎么做的，见 [docs/QUANTIZATION.zh.md](https://github.com/sgaofen/humanizer-local-model/blob/main/docs/QUANTIZATION.zh.md)。

**下载：**

```bash
pip install -U "huggingface_hub[cli]"
hf download jialinyyzz/humanizer humanizer-12b-Q8_0.gguf prompt_format.json --local-dir ./humanizer-model
# 16 GB 的机器:把 humanizer-12b-Q8_0.gguf 换成 humanizer-12b-Q6_K.gguf(硬盘紧张就用 humanizer-12b-Q4_K_M.gguf)
# 内存更少:humanizer-12b-Q3-QAT.gguf,或最小的 humanizer-12b-IQ2_XS-QAT.gguf
# 国内下载慢:在命令前面加 HF_ENDPOINT=https://hf-mirror.com
wc -c ./humanizer-model/*.gguf     # Q8_0 应为 12669630368 字节,Q6_K 应为 10029799584 字节,Q4_K_M 应为 7625160864 字节,
                                   # Q3-QAT 应为 5587794816 字节,IQ2_XS-QAT 应为 3893632896 字节
```

sha256 校验值（macOS 用 `shasum -a 256 文件名`，Linux 用 `sha256sum 文件名`，Windows 用 `certutil -hashfile 文件名 SHA256`）：

| 文件 | sha256 |
|---|---|
| `humanizer-12b-Q8_0.gguf` | `8d7a457b56de6530eaaf0151ccfa7550a4b20dab979737e259da9c63e960e0b0` |
| `humanizer-12b-Q6_K.gguf` | `c98f03bb9e71456181f99b0e1d3391e07ce1afc357f9db4c33d6d379b8dd9f0d` |
| `humanizer-12b-Q4_K_M.gguf` | `2229574dec5178629575ee4a153dfee7d9e924ab997d0ad9d2ac622b67e44834` |
| `humanizer-12b-Q3-QAT.gguf` | `307bbfdf66fb22bf98aaa93fe7d54a713bf167e646073dc0cf10870b1525eb94` |
| `humanizer-12b-IQ2_XS-QAT.gguf` | `383e5ca8f1f48ab5f65013adbc1965fa70d1d1afa6c45d932c34b854e38edbb5` |
| `humanizer-12b-bf16.gguf` | `47d79b44c3e15ea2540f4edb63556f2b7e456067d7dd252a42ff103a26a51be9` |

所有 GGUF 文件在 2026-10-04 换过一次：元数据里加了对话模板（给 LM Studio 等聊天软件用，见[第 3 节](#起服务)），并写入推荐的采样默认值（temperature 1.0、top-p 0.95、top-k 关、min-p 关、重复惩罚 1.0），请求里没设采样参数时 llama.cpp 就用它们。里面的权重逐字节没变，只改了文件头。在那之前下载的文件大小和校验值都是旧的，用续写接口、显式传采样参数照样能用。同一天还加了 `humanizer-12b-bf16.gguf`（23,832,049,568 字节，约 23.8 GB）：不量化的完整权重，单个 GGUF，给需要参照或想自己量化的人。`humanizer-12b-Q3-QAT.gguf`（2026-10-05 加入）带同样的模板和默认值。

**手上的文件是不是最新的**？文件偶尔会用同一个名字重新上传。拿本地文件的 SHA-256 和 Hugging Face 报的比一下，不一样就重新下载（App 会自己做这件事：「…」→「检查更新」）：

```bash
curl -sI https://huggingface.co/jialinyyzz/humanizer/resolve/main/humanizer-12b-Q8_0.gguf | grep -i '^x-linked-etag'
shasum -a 256 ./humanizer-model/humanizer-12b-Q8_0.gguf      # Linux 用 sha256sum
```

## 3. llama.cpp（推荐）

macOS（Metal）、Windows、Linux（CUDA、Vulkan 或 CPU）都能用。App 自己用的是 llama.cpp `b11335`，这个版本或更新的都可以。

### 安装

| 系统 | 命令 |
|---|---|
| macOS | `brew install llama.cpp` |
| Windows | `winget install llama.cpp`，或到 [llama.cpp Releases](https://github.com/ggml-org/llama.cpp/releases) 下 zip（NVIDIA 选 CUDA 版，其他显卡选 Vulkan 版） |
| Linux | 到 [llama.cpp Releases](https://github.com/ggml-org/llama.cpp/releases) 下 zip，或自己编译：`cmake -B build -DGGML_CUDA=ON && cmake --build build --config Release -j`（没有 NVIDIA 显卡就去掉 `-DGGML_CUDA=ON`） |

### 起服务

```bash
llama-server -m ./humanizer-model/humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --host 127.0.0.1 --port 8080
```

- `-c 8192`：指令 + 草稿 + 改写共用的上下文。
- `-np 1`：一次只处理一个请求，整个上下文都给它（新版本默认会把上下文分给几个并行槽位）。
- `-ngl 99`：所有层放到显卡上。显存不够就调低。日志里 `offloaded N/N layers` 那一行说明它跑在哪。
- 本地还没有文件时，把 `-m …` 换成 `--hf-repo jialinyyzz/humanizer --hf-file humanizer-12b-Q8_0.gguf`，它会自己下载。

`curl -s http://127.0.0.1:8080/health` 返回 `{"status":"ok"}` 就是准备好了。

用 `/completion` 接口，见下面的例子；不管哪天下载的文件都能用。

**聊天接口**。如果你的 GGUF 是 2026-10-04 或之后下载的（大小和 sha256 见[第 2 节](#2-选哪个文件)），并且服务带 `--jinja`（新版默认就开），`/v1/chat/completions` 也能用。文件里自带的对话模板把最后一条用户消息当作草稿，拼出和上面一模一样的提示词；系统提示词和之前的对话都不用，所以一次请求发一篇草稿。我们用 llama.cpp 核对过：模板拼出的提示词逐字相同；同一个 seed 下两个接口给出同一篇改写，都会自己停。更早下载的文件没有对话模板，用聊天接口会出怪输出。

```bash
jq -n --rawfile d draft.txt '{messages: [{role: "user", content: $d}],
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, max_tokens: 2048}' \
| curl -s http://127.0.0.1:8080/v1/chat/completions -H "Content-Type: application/json" -d @- \
| jq -r '.choices[0].message.content'
```

### 用 Python 调用（只用标准库）

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))

def humanize(draft: str, url: str = "http://127.0.0.1:8080") -> str:
    body = {
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
        "n_predict": 2048,                       # 不传 "stop":模型在 EOS 处自己结束
    }
    req = urllib.request.Request(url + "/completion", json.dumps(body).encode("utf-8"),
                                 {"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=900) as r:
        return json.load(r)["content"].strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

要流式输出就加 `"stream": true`，读服务端推送的事件，每个事件的 `content` 是接下来的一段文字。

### 用 curl 调用（macOS 和 Linux，需要 jq）

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, n_predict: 2048}' \
| curl -s http://127.0.0.1:8080/completion -d @- | jq -r .content
```

### 不起服务，一次性跑

新版 llama.cpp 里，纯文本续写工具叫 `llama-completion`；老版本用 `llama-cli`，参数一样。`-no-cnv` 让它不进入聊天模式。

```bash
# 1) 把逐字的提示词写进文件。结尾多一个 "\n" 是故意的:
#    llama.cpp 用 -f 读文件时会去掉恰好一个结尾换行。
python3 - <<'EOF'
import json
pf = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
with open("prompt.txt", "w", encoding="utf-8", newline="") as f:
    f.write(pf["instr"] + "\n\n" + draft.strip() + pf["sep"] + "\n")
EOF

# 2) 跑一次,只打印改写
llama-completion -m ./humanizer-model/humanizer-12b-Q8_0.gguf -f prompt.txt -c 8192 -n 2048 -ngl 99 \
  -no-cnv --no-display-prompt --temp 1.0 --top-p 0.95 --top-k 0 --min-p 0 --repeat-penalty 1.0
```

我们自己用的是 llama-server，这条一次性跑的路没有测过。第一次跑时加上 `--verbose-prompt` 看看切好的提示词：结尾必须是 `Rewritten`、`:` 和一个空行，后面不能再有东西。

## 4. MLX（Apple 芯片）

需要 mlx-lm 0.32 或更新。12B 没有现成的 MLX 文件，要先把 bf16 权重转成 8 bit（只需转一次）。转换要读 24 GB 的 bf16 下载，32 GB 及以上内存的 Mac 比较从容。我们只实测过 8 bit 的转换：M5 Max 上英文约 30 token/s，中文约 38 token/s。

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
sampler = make_sampler(temp=1.0, top_p=0.95)          # top-k、min-p 保持默认的关闭状态

def humanize(draft: str) -> str:
    prompt = PF["instr"] + "\n\n" + draft.strip() + PF["sep"]
    return generate(model, tok, prompt=prompt, max_tokens=2048, sampler=sampler).strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

给 `generate()` 传纯字符串，不要调用 `tok.apply_chat_template`。命令行工具 `mlx_lm.generate` 默认会套聊天模板，要加 `--ignore-chat-template`；上面的 Python 接口不会套。

## 5. transformers（CUDA）

bf16 权重约 24 GB，显存要比这更大，或者用 `device_map="auto"` 把模型分到几张卡上。transformers 要用支持 Gemma 4 的版本，权重是用 5.14.1 存的。

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
                         top_k=0,                # 关掉 generation_config.json 里的 top-k 64
                         max_new_tokens=2048)
    return tok.decode(out[0, ids["input_ids"].shape[1]:], skip_special_tokens=True).strip()

print(humanize(open("draft.txt", encoding="utf-8").read()))
```

transformers 4.x 要把 `dtype=` 写成 `torch_dtype=`。`top_k=0` 不能省，省了就是在 top-k 64 下采样。

## 6. vLLM

下面的离线接口和我们生成评测输出的方式一致（vLLM、bf16、temperature 1.0、top-p 0.95）；评测另外还用了防照抄重采。

```python
import json
from huggingface_hub import hf_hub_download
from vllm import LLM, SamplingParams

repo = "jialinyyzz/humanizer"
PF = json.load(open(hf_hub_download(repo, "prompt_format.json"), encoding="utf-8"))
llm = LLM(model=repo, dtype="bfloat16", max_model_len=8192,
          limit_mm_per_prompt={"image": 0, "audio": 0, "video": 0})   # 只用文本
params = SamplingParams(temperature=1.0, top_p=0.95, top_k=-1, min_p=0.0,
                        repetition_penalty=1.0, max_tokens=2048)      # top_k=-1 表示关闭

drafts = [open(p, encoding="utf-8").read() for p in ["draft1.txt", "draft2.txt"]]
prompts = [PF["instr"] + "\n\n" + d.strip() + PF["sep"] for d in drafts]
for result in llm.generate(prompts, params):
    print(result.outputs[0].text.strip(), "\n---")
```

起成服务（兼容 OpenAI 接口）。`--generation-config vllm` 让 vLLM 不把 `generation_config.json` 里的 top-k 64 当默认值：

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

用 `/v1/completions`，不要用 `/v1/chat/completions`。服务这条路我们自己没测过。

## 7. Ollama

Ollama 不用 GGUF 里存的对话模板，要在 `Modelfile` 里写它自己的模板。Ollama 的引擎要支持 Gemma 4 才能加载。Ollama 我们自己没跑过：下面的模板我们用 Go 的 `text/template`（Ollama 模板用的就是这套语法）渲染过，结果和[第 1 节](#1-这个模型哪里特殊)的提示词逐字相同，但没有在 Ollama 里实际试过。

`Modelfile`（和 GGUF 放在一起）。模板把最后一条用户消息当作草稿，按第 1 节原样拼好；系统提示词和之前的对话都不用，每条消息各改各的。

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
ollama show humanizer --modelfile      # 确认没有被自动加上 "PARAMETER stop" 之类的行
ollama run humanizer                   # 然后一条消息贴一篇草稿
```

结尾的 `{{ "\n\n" }}` 就是 `### Rewritten:` 后面那个空行，这样写是为了模板末尾的空白万一被裁掉也还在。

**聊天模式**（`ollama run`、`/api/chat`）：把草稿当作用户消息发。Ollama 模板没法去掉首尾空白，所以别带开头和结尾的空行（写代码就发 `draft.strip()`），否则提示词就和训练时不完全一样了。

**原始（raw）模式**不经过模板，用哪个 Modelfile 都行。调 `/api/generate`，带 `"raw": true` 和完整提示词（指令 + 草稿 + 分隔符）：

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{model: "humanizer", raw: true, stream: false,
    prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    options: {temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0,
              num_ctx: 8192, num_predict: 2048}}' \
| curl -s http://127.0.0.1:11434/api/generate -d @- | jq -r .response
```

Python：

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

Ollama 自带的 Gemma 模板，或者没写上面那段 `TEMPLATE` 的 Modelfile，在聊天模式下都会把这个模型搞坏。

## 8. LM Studio

LM Studio 我们自己没测过。LM Studio 用的是 GGUF 里存的对话模板。2026-10-04 及之后上传的文件自带一个模板，拼出的就是[第 1 节](#1-这个模型哪里特殊)的提示词（我们用 llama.cpp 核对过，见[第 3 节](#起服务)）；更早下载的文件没有，请重新下载，或者用第 4 步的续写接口。

1. 加载 `humanizer-12b-Q8_0.gguf`（或 Q6_K / Q4_K_M / Q3-QAT / IQ2_XS-QAT），加载时把上下文长度设成 8192。
2. 在模型的采样设置里设 **Temperature 1.0、Top P 0.95、Top K 0、Min P 0、Repeat Penalty 1.0**，删掉所有停止符。提示词模板保持文件自带的，别改。
3. **聊天页面**：系统提示词留空（模板反正不用它），一条消息贴一篇草稿。每条消息各改各的，之前的对话不会发给模型。本地服务的 `/v1/chat/completions` 也是这样。
4. **文本续写**（哪天下载的文件都能用）：在 Developer 页开本地服务，把完整提示词（指令 + 草稿 + 分隔符）发到 **`/v1/completions`**：

```python
import json, urllib.request

PF = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
body = {"model": "humanizer-12b-q8_0",         # 换成 LM Studio 里显示的模型 id
        "prompt": PF["instr"] + "\n\n" + draft.strip() + PF["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
        "max_tokens": 2048}
req = urllib.request.Request("http://127.0.0.1:1234/v1/completions", json.dumps(body).encode("utf-8"),
                             {"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req, timeout=900))["choices"][0]["text"].strip())
```

如果聊天回复以“Sure”开头、复述指令或者停不下来，说明文件里没有自带模板（2026-10-04 以前下载的），或者模型设置里的提示词模板被改过：重新下载文件、把模板恢复原样，或者改用 `/v1/completions`。如果你的 LM Studio 版本不认请求里的 `top_k`、`min_p`、`repeat_penalty`，第 2 步的设置会生效。

## 9. 批量改写一个文件夹

`humanize_folder.py` 通过正在运行的 llama-server（见[第 3 节](#起服务)），把一个文件夹里的每个 `.txt` 改写到另一个文件夹。只用标准库。

- 长文件按空行切成若干段，每段的草稿不超过 `--max-tokens` 个 token；逐段改写后用空行拼起来。
- 某段改写照抄草稿超过 `--max-copy`（默认 0.35）时，这段会再采一次，保留照抄更少的那一版。
- 草稿里有、改写里找不到的数字写进 `report.tsv`，供你人工核对。
- 输出文件夹里已经有的文件会跳过，可以中途停下再接着跑。

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

这里的照抄率是粗略估计（改写里连续 5 个词或 5 个字的片段有多少出现在草稿里），不是我们评测用的精确指标；第二次采样也不带防照抄惩罚，和评测里的重采不同。

## 10. 长文

- 指令、草稿和改写加起来共用 **8192 token** 的上下文。改写和草稿差不多长，所以每段草稿要明显少于一半。
- 按**段落**（空行）切开，不要在句子中间切，然后分段改写。上面的批处理脚本就是这么做的，默认每段最多 1,500 token。
- 模型训练和评测用的都是单篇邮件、帖子、作文、报告段落，几百词的长度，这个长度的段效果最好。
- 每段改写时看不到其他段，所以段与段之间语气可能略有变化，事实也不会在段之间挪动。拼好之后从头到尾读一遍。
- 标题、代码块和列表常被删掉或改成正文。只改写正文，标题和代码自己留着；[`humanizer/markdown_guard.py`](https://github.com/sgaofen/humanizer-local-model/blob/main/humanizer/markdown_guard.py) 就是这样逐块处理的。
- **[`hz`](#14-命令行工具-hz) 一行命令就能做完上面这些**，支持 `.md`、`.txt` 和 `.docx`：标题、代码、表格、链接原样保留，正文按约 350 个英文词（中文约 600 字）一块切开，不跨标题，每块都做检查。
- App（0.3.2 及以后）可以导入 `.docx` 和 PDF，草稿太长时会提示，但它是整篇一次改写；它的「原样保留」（Create fact）把选中的文字换成占位符送给模型，占位符没原样回来就重写，从而保证这段文字一字不改。长文档或有结构的文档仍然推荐用 `hz`。

## 11. 中文

- 用**同一段英文指令**，不要翻译。
- 中文**还在追赶英文**：204 篇中文改写里，事实判官在 149 篇里没挑出问题；有问题的，约 9 成改一个词或短语就好。
- 中文改写更容易照抄草稿。用 Q8_0 文件时，中文 204 发里有 14 发照抄草稿超过 35%，英文 420 发里只有 1 发。改写看着和草稿太像就再采一次；批处理脚本会自动这样做。
- 中文里**数字更常换写法**：汉字数字变成阿拉伯数字（三 → 3），日期改格式（6月14日 → 6.14）。App 和批处理脚本里那种只看阿拉伯数字的核对发现不了这类问题，请读一遍核对。
- 全角、半角标点可能互换；问候、正文、落款有时会被并成一段。发出去之前把格式理一理。
- 速度和英文差不多：llama.cpp Q8_0 在 M5 Max 上，一封 300 字左右的中文邮件约 8.5 秒。

## 12. 质量检查清单

- **通读一遍改写**。核对每个数字、日期、单位和人名，以及每个论断的方向（谁做了什么、多还是少、先还是后）。在我们的评测集上，英文 420 篇改写里有 376 篇判官没挑出事实问题；有问题的，9 成以上改一个词或短语就好，比如“37 complaints”（37 条投诉）变成了“37% of complaints”（37% 的投诉）。
- **补回需要的格式**：主题行、列表、标题、落款有时会丢（420 发里有 28 发）。
- **和草稿太像？再采一次**。每次都是重新采样。
- **随意体裁里语气会跑**。Reddit 一类的帖子里，它有时会加上原文没有的俚语或粗口，请删掉。
- **不保证过检测器**。我们的检测数字是一个检测器在某一天的一次测量；模板化的体裁（带 emoji、井号的社交帖，政策备忘）仍然最常被判 AI。这里不对任何检测结果做承诺。
- 这是给你改自己草稿的写作工具。学校、单位或出版方对 AI 辅助有规定的，请按规定来。

## 13. 排错

| 现象 | 原因 | 解决 |
|---|---|---|
| 输出以“好的”“Sure”“Here is…”开头、复述指令，或者停不下来 | 套上了通用的聊天模板：GGUF 是 2026-10-04 以前下载的（没有自带模板）、软件设置里改过模板、Ollama 没用[第 7 节](#7-ollama)的 Modelfile，或者对 safetensors 权重用了聊天接口 | 重新下载 GGUF，或者用续写接口（`/completion`、`/v1/completions`、Ollama 的 `raw: true`），发[第 1 节](#1-这个模型哪里特殊)里逐字的提示词 |
| 输出里有 `<start_of_turn>`、`<end_of_turn>` 之类的标记 | 同上：聊天格式 | 同上 |
| 输出在 `###` 处或很早就停了 | 设了停止符 | 删掉所有停止符，只靠 EOS |
| 英文草稿被写成了中文（偶尔发生，多见于夹技术术语的英文口语短稿） | 采样运气 | 英文草稿的输出里出现大段中文，就用同样的参数再采一次。App（0.3.1 及以后）和 `hz` 会自动做，最多 3 次 |
| 改写和草稿几乎一样 | 采样运气不好，或温度太低 | 确认 temperature 1.0，再采一次 |
| 胡言乱语、用词古怪或反复重复 | 采样参数不对（llama.cpp 默认的 top-k 40 / min-p 0.05、`generation_config.json` 里的 top-k 64，或者开了重复惩罚） | 显式设 top-k 0、min-p 0、重复惩罚 1.0 |
| 改写在句子中间断了 | 输出上限或上下文太小 | 调大 `n_predict` / `max_tokens`；llama-server 加 `-c 8192 -np 1`；长稿分段 |
| 加载时内存不够 | 文件对你的内存或显存太大 | 16 GB 用 Q6_K，14 GB 左右用 Q4_K_M，12 GB 用 Q3-QAT，8 GB 用 2 bit 的 IQ2_XS-QAT；调低 `-ngl` |
| 很慢 | 在用 CPU 跑 | 看 llama.cpp 日志里有没有 `offloaded N/N layers`；装 Metal、CUDA 或 Vulkan 版 |
| 下载时 404 | 文件名写错 | 用[第 2 节](#2-选哪个文件)里的文件名 |
| 指纹自检不过 | 提示词拼法和训练时不一样 | 直接复制[第 1 节](#提示词)里的 `build_prompt` |

**自检**。[AGENTS.md 第 8 节](https://github.com/sgaofen/humanizer-local-model/blob/main/AGENTS.md#8-self-test-verify-the-install)有一个只用标准库的脚本：它把评测集里的一篇真实草稿发给 llama-server，检查提示词格式、接口、聊天模板有没有漏进来、有没有照抄。设置正确会打印 `PASS`。

## 14. 命令行工具 hz

`hz` 一行命令改写一篇草稿或一整份文档，人和 AI Agent 用法一样。它连接 [App](https://github.com/sgaofen/humanizer-local-model/releases/latest) 或 llama-server（[第 3 节](#起服务)），逐字拼好提示词，用评测时的采样参数，把长文切块、保留结构，并检查每一块。需要 Python 3.8 及以上，只用标准库；`.docx` 支持是可选的。

### 安装

```bash
pipx install git+https://github.com/sgaofen/humanizer-local-model
pipx inject humanize-model python-docx            # 只有要处理 .docx 时才需要

# 或者用 pip,装进任意环境:
pip install git+https://github.com/sgaofen/humanizer-local-model
pip install "humanize-model[docx] @ git+https://github.com/sgaofen/humanizer-local-model"   # 连同 .docx 支持

# 克隆了仓库、不想安装:
python3 -m humanizer.hz draft.txt
```

装好后只有一个命令 `hz`。如果你机器上已经有别的程序也叫 `hz`，以 `PATH` 里排在前面的为准。

### 用法

```bash
hz draft.txt                          # 改写结果打印到 stdout
hz < draft.txt > rewrite.txt
hz paper.md -o paper.out.md           # 长篇 Markdown:结构保留,正文分块改写
hz report.docx -o report.out.docx     # 不写 -o 时输出到 report.hz.docx
hz paper.md --json > report.json      # 每块的统计;有 -o 时正文写进文件
hz paper.md --dry-run                 # 只看怎么切块,不调用模型
hz paper.md --server http://127.0.0.1:8080   # 指定某个 llama-server
```

进度打印在 stderr，每块一行。`--quiet` 不打印进度；需要人工核对的块无论如何都会列出来。

### 它怎么找模型

1. 给了 `--server URL`：只用这个 llama-server。（如果这个地址其实是 App，就按 App 的方式调用。）
2. 否则先找 **App**：正在运行且状态为 ready 就用它。端口从 App 的 `instance.json` 里读，读不到就用 `http://127.0.0.1:47615`。请求发到 `POST /api/completion`，带上 App 要求的 `X-Humanizer: 1` 头。
3. 再找 `http://127.0.0.1:8080` 上的 **llama-server**（`POST /completion`）。
4. 都没有、而 macOS 上装了 App 但没开：`hz` 在后台拉起它（`open -a Humanizer`），最多等 3 分钟让模型加载完，期间打印进度。
5. 还是没有就以退出码 3 结束，并告诉你怎么装 App、怎么起 llama-server。

如果 App 开着但还没下模型（第一次运行），`hz` 会提示你先在 App 里选一个档位下载。

### 文档怎么切

- 用空行分块。下面这些**原样保留**，不送给模型：Markdown 标题（`#`、下划线式标题，或整行只有 `**粗体**`）、围栏和缩进代码块、表格、只有图片或链接的行、HTML 块和注释、`$$…$$` 与 `\[…\]` 公式、引用块、分隔线、YAML front matter、脚注和链接定义、紧挨在列表/表格/代码块前面以冒号结尾的短句（如"主要成效如下："），以及 References、Bibliography、Works Cited、Sources、参考文献等标题下面的全部内容。
- 正文段落按顺序拼成块，每块最多约 **350 个英文词或 600 个汉字**（`--max-words`、`--max-chars`）。块不会跨过标题或任何保留块；同一段连续正文切出的几块大小大致相同。
- 单段超长时按句子切开，各块改完再拼回同一段。
- **列表**：每一项保留原来的项目符号或编号，以及开头的 `**小标题：**`。一项有约 15 个英文词（26 个汉字）以上就单独改写，更短的保持原样。短列表项是模型最弱的地方，请重点看。
- 改写后的块放回原来的位置，块与块之间的空行照旧。正文里的行内格式（粗体、行内代码、链接）由模型决定，有时会丢。

### 检查和重写

每块改完都会检查。出现下表任一问题就再改一次（`--retries`），两版里留问题少的那版：

| 问题 | 含义 |
|---|---|
| `missing_numbers` | 草稿里的某个数字在改写里找不到。先做规范化：`1,250` = `1250`，`3.50` = `3.5`，`07` = `7`，`480k` = `480,000`，`31.7万` = `317,000`；改写里写成文字的数字（`three`、`两`）也算找到。 |
| `copy` | 改写有一半以上照抄草稿（`--max-copy`，默认 0.5）：改写里的英文 5 词片段（中文 6 字片段）有多少在草稿里出现过。 |
| `missing_urls` | 草稿里的链接在改写里找不到。 |
| `empty`、`truncated` | 什么都没返回，或者输出撞到了长度上限。 |
| `too_short`、`too_long` | 改写不到草稿长度的 35%，或超过 175%。真机测试里正常块的长度是草稿的 0.7 到 1.5 倍；超过 1.75 倍的几块分别是凭空多出一段、编了一个数、同一句话写了两遍。 |
| `repeated` | 改写里有两句话几乎在说同一件事，而草稿里没有这样的重复。 |
| `markup` | 改写里出现了草稿没有的 HTML 标签（真机测试里见过短列表项末尾多出一个 `<p>`）。 |

`added_numbers`（改写里有、草稿里没有的数字）**只报告、不重写**：模型有时是做了正确的算术（"成本从 $480k 降到 $305k"被写成"省了 $175k"），有时是编出来的数字。不管哪种，都要看一眼。

**语言不对**。在这些检查之前，写成了另一种语言的改写（英文草稿写成中文，或中文草稿写成英文；只数字符）会用同样的参数重新生成，最多 3 次。`pieces[].language_resampled` 记重采了几次；3 次都不对的话，这块会标上 `language`。

重写之后仍有问题的块会在 stderr 列出行号（`.docx` 为段落序号）；用 `--json` 时标为 `"flagged": true`。这种情况退出码仍是 0。

### `--json` 输出

下面是一次真机运行的结果（1,200 词的英文 Markdown 文章，走 App，21 块里只列出一块）：

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

| 字段 | 含义 |
|---|---|
| `summary.pieces` / `retried` / `flagged` | 送去改写的块数；其中重写过的块数；重写后仍有问题的块数 |
| `summary.copy_rate` | 全部改写正文相对全部草稿的照抄率 |
| `kept` | 原样保留的块，按类型计数（`short` 是短到不值得改写的正文，`list item` 是短列表项） |
| `pieces[].line`（`.docx` 为 `paragraph`） | 这块在输入里从第几行开始，从 1 数起 |
| `pieces[].kind` | `prose`（一段或几段正文）、`item`（列表项）、`sentences`（超长段落切出的一部分）、`paragraph`（`.docx` 段落） |
| `pieces[].words_in` / `words_out` | 草稿和改写的长度；一个汉字算一个词 |
| `pieces[].copy_rate` | 选中那版的照抄率，0 到 1 |
| `pieces[].missing_numbers` / `missing_urls` / `added_numbers` | 见上表；缺失的按草稿里的写法，新增的按改写里的写法 |
| `pieces[].retried` / `chosen` / `attempts` | 是否重写过、留的是第几版、每一版的检查结果 |
| `pieces[].seconds` | 这块所有版本加起来的模型耗时 |
| `pieces[].language_resampled` | 因为语言不对多采了几次（通常是 0） |
| `pieces[].flagged` / `issues` | 留下的那版是否仍有问题，以及是哪些问题 |
| `text` | 完整结果，只在没给 `-o`、输入又不是 `.docx` 时才有 |

### `.docx` 文件

- 需要 `python-docx`（见上面的安装）。正文段落逐段改写。标题（Heading、Title、Subtitle 样式或带大纲级别的段落）、表格、空段、题注、引用、代码样式、目录，以及参考文献标题下面的内容都不动。页眉、页脚、脚注和文本框也不碰。
- 段落里有超链接、图片、域、脚注引用、修订痕迹或公式的，整段不动，免得丢东西。
- 改写结果写进段落的**第一个 run**，其余 run 清空。段落样式和编号都保留，但**段内局部的粗体、斜体等格式会丢**，整段统一用第一个 run 的格式。

### 选项

| 选项 | 默认 | 含义 |
|---|---|---|
| `-o FILE` | stdout（`.docx`：`NAME.hz.docx`） | 结果写到哪里。不允许覆盖输入文件。 |
| `--server URL` | | 只用这个 llama-server |
| `--app URL` | 自动 | App 不在常用地址时用 |
| `--json` | 关 | 在 stdout 输出 JSON 报告 |
| `-q`、`--quiet` | 关 | 不打印进度 |
| `--dry-run` | 关 | 只显示切块和保留块，不调用模型 |
| `--max-words` / `--max-chars` | 350 / 600 | 英文 / 中文每块大小 |
| `--max-copy` | 0.5 | 照抄率超过多少就重写 |
| `--retries` | 1 | 有问题的块最多再改几次（0 = 不重写） |
| `--no-launch` | 关 | App 没开时不自动拉起 |

退出码：0 完成（有块被标记也是 0），1 运行中出错（不写任何输出），2 输入或参数有误，3 找不到模型。

### 局限

- 检查只管数字、链接、照抄、长度、重复和多余的 HTML 标签。**改了一个词、一个人名或句子意思，它查不出来**。真机中文测试里，"覆盖全部 48 家门店"被改成"覆盖全省 48 家门店"，"时有发生"被改成"经常出现"，数字却一个没错。请通读结果。
- 每块改写时看不到其他块，所以块与块之间语气可能略有变化。
- 短列表项最弱：模型有时会丢掉短项的主语，或者把同一句话写两遍。
- 用汉字写的数字只能部分识别，三和 3 之间来回换写法时，两个方向都可能漏报或误报。
- 速度（M5 Max 上走 App，Q8_0）：1,200 词的英文 Markdown 文章切成 21 块，41 到 54 秒；约 700 字的中文报告切成 6 块，13 到 17 秒。
- 不承诺任何 AI 检测器结果。
