# 安装 humanizer

[English](INSTALL.md) · [中文](INSTALL.zh.md) · 返回 [README](../README.zh.md)

有两种用法：

- **App**（Apple 芯片的 Mac、64 位 Windows）：下载、双击、贴草稿。大多数人用这个就行。
- **命令行**（macOS、Windows、Linux）：起一个本地服务，脚本里调用。Linux 没有 App，只能走这条路。

两种方式模型都在你自己的电脑上跑，唯一的联网是第一次从 Hugging Face 下载模型。

**目录：**[选哪一档](#选哪一档) · [macOS 上的 App](#macos-上的-app) · [Windows 上的 App](#windows-上的-app) · [第一次打开被拦](#第一次打开被拦) · [命令行](#命令行) · [常见问题](#常见问题)

## 选哪一档

| 内存 | App 默认选 | 文件 | 下载大小 |
|---|---|---|---|
| 32 GB 及以上 | **Q8_0**（效果最好） | `humanizer-12b-Q8_0.gguf` | 约 12.7 GB |
| 16 GB | **Q6_K** | `humanizer-12b-Q6_K.gguf` | 约 10.0 GB |
| 8 GB | **Lite**：更早、更小的 E4B 版 | `lite/humanizer-lite-Q6_K.gguf` | 约 6.2 GB |

更小的 `humanizer-12b-Q4_K_M.gguf`（约 7.6 GB）也已经放在 Hugging Face 上，给 llama.cpp、Ollama 或 LM Studio 用；App 里没有这一档。各档和全精度模型差多少（包括事实判官对每个文件的结果），见[不用 App 怎么用](USAGE.zh.md#2-选哪个文件)里的实测。

App 有 1 GB 的容差，32 GB 的电脑报 31.x GB 也会选 Q8_0。在选档页面上随时可以换别的档。所有档位的提示词和参数都一样。

## macOS 上的 App

**要求：**Apple 芯片的 Mac（M1 及以后），macOS 13.3 或更新。

1. 到 [Releases](https://github.com/sgaofen/humanize-model/releases/latest) 下载 `Humanizer-<版本>-macos-arm64.dmg`。
2. 打开 `.dmg`，把 **Humanizer** 拖进「应用程序」。
3. 双击 Humanizer。第一次会被 macOS 拦下（App 还没签名），处理方法见[第一次打开被拦](#第一次打开被拦)。
4. 浏览器会打开 `http://127.0.0.1:47615/`。选档页面会显示你的内存和推荐档位，点「下载」。
   - 关掉网页、退出 App、断网都能续传。下完会校验文件，然后自动装载。
5. 左边贴草稿，点中间的箭头（或 ⌘↵），右边流式出改写。

要知道的几件事：

- App 没有窗口，也不在 Dock 里，它就是浏览器里的一个标签页。**退出**在网页右上角的「…」菜单。关掉网页 30 分钟后它会自己退出、释放内存。
- 模型和设置放在 `~/Library/Application Support/Humanizer`。
- **数字核对：**App 会拿草稿里的每个数字去改写里找，找不到的标出来。只比阿拉伯数字，所以「60 分钟 → 一个小时」也会被标出来，其实没错。把它当提醒，不是判决。

## Windows 上的 App

**要求：**64 位 Windows，不需要管理员权限。

1. 到 [Releases](https://github.com/sgaofen/humanize-model/releases/latest) 下载 `Humanizer-<版本>-windows-x64-setup.exe`。不想安装就下 `Humanizer-<版本>-windows-x64-portable.zip`，解压到任意位置。
2. 运行安装包。第一次 SmartScreen 多半会拦，见[第一次打开被拦](#第一次打开被拦)。它只为当前用户安装，位置是 `%LOCALAPPDATA%\Programs\Humanizer`。
3. 打开 Humanizer，浏览器里会出现 App，和 macOS 一样下载推荐的档位。

要知道的几件事：

- **显卡：**App 先试 NVIDIA（CUDA），再试 Vulkan（多数 AMD、Intel 显卡），最后用 CPU。CPU 能跑，但慢很多。
- 模型和设置放在 `%LOCALAPPDATA%\Humanizer`。
- 退出同样在网页的「…」菜单。
- Windows 版还没在真机上跑过（只有 CI 冒烟测试）。遇到问题请[提 issue](https://github.com/sgaofen/humanize-model/issues)。

## 第一次打开被拦

App 还没有代码签名，两个系统第一次都会拦一下，这是正常的。

### macOS：「无法打开 Humanizer」/「Apple 无法验证…」

任选一种：

1. 先双击一次 Humanizer，关掉提示。打开「系统设置 → 隐私与安全性」，拉到最下面点「仍要打开」，输入密码确认。或者
2. 在「终端」里执行一次：

   ```bash
   xattr -dr com.apple.quarantine /Applications/Humanizer.app
   ```

macOS 15 起右键「打开」已经绕不过去，只能走系统设置。

### Windows：「Windows 已保护你的电脑」

点「更多信息」，再点「仍要运行」。

## 命令行

这里是简版。完整文档见 **[不用 App 怎么用](USAGE.zh.md)**：llama.cpp、MLX、transformers、vLLM、Ollama、LM Studio，批量改写一个文件夹的脚本，长文和中文的处理。

### 1. 装 llama.cpp

| 系统 | 命令 |
|---|---|
| macOS | `brew install llama.cpp` |
| Windows | `winget install llama.cpp`，或者到 [llama.cpp Releases](https://github.com/ggml-org/llama.cpp/releases) 下 zip（NVIDIA 选 CUDA 版，其他显卡选 Vulkan 版） |
| Linux | 到 [llama.cpp Releases](https://github.com/ggml-org/llama.cpp/releases) 下 zip，或者自己编译：`cmake -B build -DGGML_CUDA=ON && cmake --build build --config Release -j`（没有 NVIDIA 显卡就去掉 `-DGGML_CUDA=ON`） |

检查：`llama-server --version`。App 用的是 `b11335`，这个版本或更新的都可以。

### 2. 下载模型

```bash
pip install -U "huggingface_hub[cli]"
hf download jialinyyzz/humanizer humanizer-12b-Q8_0.gguf prompt_format.json --local-dir ./humanizer-model
```

16 GB 内存就改下 `humanizer-12b-Q6_K.gguf`（下面的命令也换成这个文件名）。

国内下载慢，在命令前面加 `HF_ENDPOINT=https://hf-mirror.com`（Windows PowerShell 先执行 `$env:HF_ENDPOINT="https://hf-mirror.com"`）。也可以直接在浏览器里从[模型页面](https://huggingface.co/jialinyyzz/humanizer/tree/main)下载。

### 3. 起服务

```bash
llama-server -m ./humanizer-model/humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --host 127.0.0.1 --port 8080
```

`curl -s http://127.0.0.1:8080/health` 返回 `{"status":"ok"}` 就是准备好了。

### 4. 改写一个文件

macOS 和 Linux（需要 `jq`）：

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, n_predict: 2048}' \
| curl -s http://127.0.0.1:8080/completion -d @- | jq -r .content
```

任何装了 Python 3 的系统（不用装别的包）：

```python
import json, urllib.request

pf = json.load(open("humanizer-model/prompt_format.json", encoding="utf-8"))
draft = open("draft.txt", encoding="utf-8").read()
body = {"prompt": pf["instr"] + "\n\n" + draft.strip() + pf["sep"],
        "temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0,
        "n_predict": 2048}
req = urllib.request.Request("http://127.0.0.1:8080/completion", json.dumps(body).encode(),
                             {"Content-Type": "application/json"})
print(json.load(urllib.request.urlopen(req))["content"].strip())
```

提示词必须这样逐字拼，见[提示词格式](../README.zh.md#提示词格式)。MLX、transformers、vLLM、Ollama、LM Studio 的用法见[不用 App 怎么用](USAGE.zh.md)。想端到端检查装得对不对，跑 [AGENTS.md](../AGENTS.md#8-self-test-verify-the-install) 里的自检脚本。

## 常见问题

**不确定选哪档？**交给 App，它会读你的内存。命令行：32 GB 及以上用 Q8_0，16 GB 用 Q6_K，8 GB 用 Lite。

**下载到一半断了。**重新打开 App，会接着下。用 `hf download` 的话，再执行一遍同样的命令。

**很慢。**多半是在用 CPU 跑。Windows 上先把显卡驱动更新到最新；命令行看服务日志里有没有 `offloaded N/N layers`。作为参考，App 在 M5 Max 上（llama.cpp Q8_0，Metal）约 36–38 token/s：百来词的英文邮件约 3.6 秒，300 字左右的中文邮件约 8.5 秒。

**内存不够。**换小一档（Q8_0 → Q6_K → Lite）。命令行还可以调低 `-ngl`（少放几层到显卡上）或调小 `-c`。

**改写跟原稿几乎一样。**点「重新生成」，每次都是重新采样。App 不会自动重采。

**有数字被标出来。**核对一下。多数时候是数字真的变了；有时只是写法不同（「60 分钟」→「一个小时」、「3」→「三」），核对功能分不出来。

**Ollama 或 LM Studio 的输出很怪**（先打招呼、复述指令、来一句“好的，以下是……”）。那是套上了通用的聊天模板：GGUF 是 2026-10-04 以前下载的，里面没有自带模板（重新下载一次），或者 Ollama 没用 [不用 App 怎么用：Ollama](USAGE.zh.md#7-ollama) 里的 Modelfile。文本续写接口（原始模式）不管哪天下载的文件都能用。

**浏览器没有自己打开。**手动打开 `http://127.0.0.1:47615/`。端口被占时 App 会顺延到下一个空闲端口。

**我的文字会被发到哪里吗？**不会。App 和服务只听 `127.0.0.1`（你自己的电脑），网页也不从网上加载任何东西。唯一的下载就是模型本身。

**怎么卸载？**macOS：删掉 `Humanizer.app` 和 `~/Library/Application Support/Humanizer`。Windows：在「设置 → 应用」里卸载（便携版直接删文件夹），再删掉 `%LOCALAPPDATA%\Humanizer`。

**怎么换成更新后的模型？**Hugging Face 上的模型文件在 2026-10-02 更新为 RLRt2（见 [README](../README.zh.md#评测结果)）。App 不会替换已经下好的模型：先退出 App，删掉数据目录里 `models` 文件夹中的 `.gguf` 文件（macOS 是 `~/Library/Application Support/Humanizer/models`，Windows 是 `%LOCALAPPDATA%\Humanizer\models`），再打开 App，它会弹出选档页面并下载当前版本。用 `hf download` 的话，再执行一遍同样的命令就会拿到新版。
