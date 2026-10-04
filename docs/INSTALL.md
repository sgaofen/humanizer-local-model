# Installing humanizer

[English](INSTALL.md) · [中文](INSTALL.zh.md) · back to [README](../README.md)

There are two ways to run humanizer:

- **The app** (macOS with Apple silicon, Windows x64). Download, double-click, paste a draft. Recommended for most people.
- **The command line** (macOS, Windows, Linux). A local server you can call from scripts. Linux has no app, so use this route there.

Either way the model runs on your own computer. The only network access is the one-time model download from Hugging Face.

**Contents:** [Which model size](#which-model-size) · [App on macOS](#app-on-macos) · [App on Windows](#app-on-windows) · [First-launch warnings](#first-launch-warnings) · [Command line](#command-line) · [FAQ](#faq)

## Which model size

| Your memory (RAM) | What the app picks | File | Download size |
|---|---|---|---|
| 32 GB or more | **Q8_0** (best quality) | `humanizer-12b-Q8_0.gguf` | about 12.7 GB |
| 16 GB | **Q6_K** | `humanizer-12b-Q6_K.gguf` | about 10.0 GB |
| 8 GB | **Lite**: the earlier, smaller E4B release | `lite/humanizer-lite-Q6_K.gguf` | about 6.2 GB |

A smaller `humanizer-12b-Q4_K_M.gguf` (about 7.6 GB) is also on Hugging Face, for llama.cpp, Ollama or LM Studio; the app doesn't offer it. How close each file is to the full-precision model, including the fact judge on each file, is in [Usage without the app](USAGE.md#2-pick-a-file).

The app allows 1 GB of slack, so a 32 GB PC that reports 31.x GB still gets Q8_0. You can always pick a different size on the setup page. All sizes use the same prompt and settings.

## App on macOS

**Needs:** a Mac with Apple silicon (M1 or newer) and macOS 13.3 or later.

1. Download `Humanizer-<version>-macos-arm64.dmg` from [Releases](https://github.com/sgaofen/humanize-model/releases/latest).
2. Open the `.dmg` and drag **Humanizer** into **Applications**.
3. Double-click Humanizer. The first time, macOS blocks it because the app isn't signed yet; see [First-launch warnings](#first-launch-warnings).
4. Your browser opens the app at `http://127.0.0.1:47615/`. The setup page shows your memory and a recommended size. Click **Download**.
   - The download resumes if you close the page, quit the app or lose the connection. When it finishes, the file is checked against its checksum and loaded.
5. Paste a draft on the left and press the arrow (or ⌘↵). The rewrite streams in on the right.

Good to know:

- The app has no window and no Dock icon; it lives in your browser tab. **Quit** from the **…** menu at the top right. If you close the page, it quits by itself after 30 minutes and frees the memory.
- Models and settings are in `~/Library/Application Support/Humanizer`.
- **Numbers check:** the app compares every number in the draft with the rewrite and flags any that are missing. It only compares Arabic digits, so "60 minutes → an hour" is flagged even though it's fine. Treat it as a reminder, not a verdict.

## App on Windows

**Needs:** 64-bit Windows. No administrator rights needed.

1. Download `Humanizer-<version>-windows-x64-setup.exe` from [Releases](https://github.com/sgaofen/humanize-model/releases/latest). Prefer not to install? Use `Humanizer-<version>-windows-x64-portable.zip` and unzip it anywhere.
2. Run the installer. SmartScreen will probably warn you the first time; see [First-launch warnings](#first-launch-warnings). It installs for your user only, to `%LOCALAPPDATA%\Programs\Humanizer`.
3. Start Humanizer. Your browser opens the app; download the recommended size as on macOS.

Good to know:

- **GPU:** the app tries NVIDIA (CUDA) first, then Vulkan (most AMD and Intel GPUs), then the CPU. The CPU works but is much slower.
- Models and settings are in `%LOCALAPPDATA%\Humanizer`.
- Quit from the **…** menu in the page, as on macOS.
- The Windows build has not been run on real Windows hardware yet (only CI smoke tests). If something breaks, please [open an issue](https://github.com/sgaofen/humanize-model/issues).

## First-launch warnings

The app is not code-signed yet, so both systems warn you the first time. This is expected.

### macOS: "Humanizer can't be opened" / "Apple could not verify…"

Either:

1. Double-click Humanizer once and dismiss the warning. Open **System Settings → Privacy & Security**, scroll to the bottom and click **Open Anyway**, then confirm with your password. Or
2. Run this once in Terminal:

   ```bash
   xattr -dr com.apple.quarantine /Applications/Humanizer.app
   ```

On macOS 15 and later, right-click → Open no longer skips the warning; use System Settings.

### Windows: "Windows protected your PC"

Click **More info**, then **Run anyway**.

## Command line

This is the short version. **[Usage without the app](USAGE.md)** has the full guide: llama.cpp, MLX, transformers, vLLM, Ollama, LM Studio, a script for a whole folder of drafts, long documents and Chinese.

### 1. Install llama.cpp

| System | Command |
|---|---|
| macOS | `brew install llama.cpp` |
| Windows | `winget install llama.cpp`, or a zip from [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases) (CUDA build for NVIDIA, Vulkan build for other GPUs) |
| Linux | a zip from [llama.cpp releases](https://github.com/ggml-org/llama.cpp/releases), or build it: `cmake -B build -DGGML_CUDA=ON && cmake --build build --config Release -j` (drop `-DGGML_CUDA=ON` without an NVIDIA GPU) |

Check: `llama-server --version`. The app uses build `b11335`; that or newer is fine.

### 2. Download the model

```bash
pip install -U "huggingface_hub[cli]"
hf download jialinyyzz/humanizer humanizer-12b-Q8_0.gguf prompt_format.json --local-dir ./humanizer-model
```

With 16 GB of memory, download `humanizer-12b-Q6_K.gguf` instead (and use that name below).

In mainland China, put `HF_ENDPOINT=https://hf-mirror.com` in front of the command (on Windows PowerShell: `$env:HF_ENDPOINT="https://hf-mirror.com"` first). You can also download the files in a browser from the [model page](https://huggingface.co/jialinyyzz/humanizer/tree/main).

### 3. Start the server

```bash
llama-server -m ./humanizer-model/humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --host 127.0.0.1 --port 8080
```

It is ready when `curl -s http://127.0.0.1:8080/health` returns `{"status":"ok"}`.

### 4. Rewrite a file

macOS and Linux (needs `jq`):

```bash
jq -n --rawfile d draft.txt --slurpfile f humanizer-model/prompt_format.json \
  '{prompt: ($f[0].instr + "\n\n" + ($d | sub("^\\s+"; "") | sub("\\s+$"; "")) + $f[0].sep),
    temperature: 1.0, top_p: 0.95, top_k: 0, min_p: 0, repeat_penalty: 1.0, n_predict: 2048}' \
| curl -s http://127.0.0.1:8080/completion -d @- | jq -r .content
```

Any system with Python 3 (no extra packages):

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

The prompt must be built exactly like this; see [Prompt format](../README.md#prompt-format). For MLX, transformers, vLLM, Ollama and LM Studio, see [Usage without the app](USAGE.md). To check a setup end to end, run the self-test in [AGENTS.md](../AGENTS.md#8-self-test-verify-the-install).

## FAQ

**Which size should I pick if I'm not sure?** Let the app decide; it reads your memory. On the command line: 32 GB or more → Q8_0; 16 GB → Q6_K; 8 GB → Lite.

**The download stopped halfway.** Open the app again; it continues where it stopped. With `hf download`, run the same command again.

**It's very slow.** It is probably running on the CPU. On Windows, make sure your GPU driver is up to date. On the command line, look for `offloaded N/N layers` in the server log. For reference, on an M5 Max the app (llama.cpp Q8_0, Metal) runs at about 36–38 tokens/s: about 3.6 seconds for a hundred-word email, about 8.5 seconds for a Chinese email of about 300 characters.

**It ran out of memory.** Pick a smaller size (Q8_0 → Q6_K → Lite). On the command line, also try a lower `-ngl` (fewer layers on the GPU) or a smaller `-c`.

**The rewrite is almost the same as my draft.** Press **Regenerate** (each run is a fresh sample). The app does not resample automatically.

**A number is flagged.** Check it. Usually the number really did change; sometimes it is only written differently ("60 minutes" → "an hour", "3" → "three"), which the check can't tell apart.

**Ollama or LM Studio gives strange output** (it greets you, repeats the instructions, or adds "Sure, here's…"). They applied a generic chat template: the GGUF was downloaded before 2026-10-04 and has no built-in template (download it again), or Ollama runs without the Modelfile from [Usage without the app: Ollama](USAGE.md#7-ollama). The text-completion endpoint (raw mode) works with any download.

**The browser page didn't open.** Open `http://127.0.0.1:47615/` yourself. If that port is taken, the app uses the next free one.

**Is my text sent anywhere?** No. The app and the server listen on `127.0.0.1` (your own computer) only, and the page loads nothing from the internet. The only download is the model itself.

**How do I uninstall?** macOS: delete `Humanizer.app` and `~/Library/Application Support/Humanizer`. Windows: uninstall from Settings → Apps (or delete the portable folder), then delete `%LOCALAPPDATA%\Humanizer`.

**How do I get the updated model?** The model files on Hugging Face were updated on 2026-10-02 (RLRt2, see the [README](../README.md#results)). The app doesn't replace a model it has already downloaded. Quit the app, delete the `.gguf` file in the `models` folder of its data folder (macOS `~/Library/Application Support/Humanizer/models`, Windows `%LOCALAPPDATA%\Humanizer\models`), and open the app again: it shows the size picker and downloads the current file. With `hf download`, run the same command again; it fetches the new version.
