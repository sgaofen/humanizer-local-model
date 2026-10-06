# -*- coding: utf-8 -*-
"""Static HTML for the Space page, in English and Chinese.

Every visible string comes in both languages: L(en, zh) gives two spans and the stylesheet shows
the one matching <html data-lang>. Every number on this page comes from the release README /
the release fact sheet; keep it that way. Never promise a detector result.
"""
from __future__ import annotations

import html
import json

import diffmark

GH = "https://github.com/sgaofen/humanize-model"
REL = GH + "/releases/latest"
APP_DL = GH + "/releases/download/app-v0.3.0/"   # direct files; bump with each app release
MAC_DMG = APP_DL + "Humanizer-0.3.0-macos-arm64.dmg"
WIN_EXE = APP_DL + "Humanizer-0.3.0-windows-x64-setup.exe"
BLOB = GH + "/blob/main/"
HF_MODEL = "https://huggingface.co/jialinyyzz/humanizer"
INSTALL_EN = BLOB + "docs/INSTALL.md#first-launch-warnings"
INSTALL_ZH = BLOB + "docs/INSTALL.zh.md#第一次打开被拦"
USAGE_EN = BLOB + "docs/USAGE.md"
USAGE_ZH = BLOB + "docs/USAGE.zh.md"
AGENTS = BLOB + "AGENTS.md"
BLADER = "https://github.com/blader/humanizer"

MAX_WORDS = 700      # English words per part (one GPU call)
MAX_CJK = 1200       # Chinese characters per part
PARTS = 3            # a longer draft is split at paragraph breaks and rewritten part by part, at most this many per run


def L(en: str, zh: str) -> str:
    """Inline text in both languages."""
    return f'<span class="l-en">{en}</span><span class="l-zh" lang="zh-CN">{zh}</span>'


def P(en: str, zh: str, cls: str = "", tag: str = "p") -> str:
    """A block in both languages."""
    c = f" {cls}" if cls else ""
    return f'<{tag} class="l-en{c}">{en}</{tag}><{tag} class="l-zh{c}" lang="zh-CN">{zh}</{tag}>'


def A(href: str, text: str, cls: str = "") -> str:
    c = f' class="{cls}"' if cls else ""
    return f'<a href="{html.escape(href)}" target="_blank" rel="noopener"{c}>{text}</a>'


def icon(name: str, cls: str = "") -> str:
    c = f' class="{cls}"' if cls else ""
    return f'<svg{c} aria-hidden="true"><use href="#i-{name}"/></svg>'


SPRITE = """
<svg width="0" height="0" style="position:absolute" aria-hidden="true"><defs>
<symbol id="i-mark" viewBox="0 0 40 24"><path d="M2 12 H15 C19 12 19.5 4.5 23.5 4.5 C27.5 4.5 26 19.5 30.5 19.5 C34.5 19.5 33.5 9 38 9" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-arrow" viewBox="0 0 24 24"><path d="M4 12h14M12.5 5.5 19 12l-6.5 6.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-copy" viewBox="0 0 24 24"><rect x="8.5" y="8.5" width="11" height="11" rx="2.5" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M15.5 8.5V6a1.5 1.5 0 0 0-1.5-1.5H6A1.5 1.5 0 0 0 4.5 6v8A1.5 1.5 0 0 0 6 15.5h2.5" fill="none" stroke="currentColor" stroke-width="1.7"/></symbol>
<symbol id="i-check" viewBox="0 0 24 24"><path d="m5 12.5 4.5 4.5L19 7.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-warn" viewBox="0 0 24 24"><path d="M12 4.5 21 19.5H3L12 4.5Z" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/><path d="M12 10v4.2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/><circle cx="12" cy="16.9" r="1.05" fill="currentColor"/></symbol>
<symbol id="i-sun" viewBox="0 0 24 24"><circle cx="12" cy="12" r="4" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M12 2.5v2.2M12 19.3v2.2M2.5 12h2.2M19.3 12h2.2M5.3 5.3l1.5 1.5M17.2 17.2l1.5 1.5M5.3 18.7l1.5-1.5M17.2 6.8l1.5-1.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></symbol>
<symbol id="i-moon" viewBox="0 0 24 24"><path d="M19.5 14.6A7.8 7.8 0 0 1 9.4 4.5a7.8 7.8 0 1 0 10.1 10.1Z" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/></symbol>
<symbol id="i-diff" viewBox="0 0 24 24"><rect x="3.5" y="6" width="9" height="5" rx="1.5" fill="currentColor" opacity=".35"/><path d="M3.5 16.5h7M14 8.5h6.5M14 16.5h6.5" stroke="currentColor" stroke-width="1.8" stroke-linecap="round"/></symbol>
<symbol id="i-eraser" viewBox="0 0 24 24"><path d="m4.5 15.5 9-9a2 2 0 0 1 2.8 0l2.2 2.2a2 2 0 0 1 0 2.8L12 18H7l-2.5-2.5Z" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linejoin="round"/><path d="M9 11l5 5M12 18h7.5" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></symbol>
<symbol id="i-down" viewBox="0 0 24 24"><path d="M12 4v13M6 11.5l6 6 6-6M5 20.5h14" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-ext" viewBox="0 0 24 24"><path d="M14 5h5v5M19 5l-8 8M17 14v4.5a1.5 1.5 0 0 1-1.5 1.5h-10A1.5 1.5 0 0 1 4 18.5v-10A1.5 1.5 0 0 1 5.5 7H10" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-apple" viewBox="0 0 24 24"><path d="M16.4 12.6c0-2.3 1.9-3.4 2-3.5-1.1-1.6-2.8-1.8-3.4-1.8-1.4-.1-2.8.9-3.5.9-.7 0-1.8-.8-3-.8-1.5 0-3 .9-3.8 2.3-1.6 2.8-.4 7 1.2 9.3.8 1.1 1.7 2.4 2.9 2.3 1.2 0 1.6-.7 3-.7s1.8.7 3 .7c1.3 0 2.1-1.1 2.8-2.3.9-1.3 1.3-2.6 1.3-2.6-.1 0-2.5-1-2.5-3.8ZM14.2 5.8c.6-.8 1.1-1.8 1-2.8-.9 0-2 .6-2.7 1.4-.6.7-1.1 1.7-1 2.7 1 .1 2-.5 2.7-1.3Z" fill="currentColor"/></symbol>
<symbol id="i-win" viewBox="0 0 24 24"><path d="M3.5 5.6 10.5 4.6v6.9h-7V5.6Zm8 -1.1L20.5 3.2v8.3h-9V4.5ZM3.5 12.5h7v6.9l-7-1V12.5Zm8 0h9v8.3l-9-1.3v-7Z" fill="currentColor"/></symbol>
<symbol id="i-term" viewBox="0 0 24 24"><rect x="3.5" y="4.5" width="17" height="15" rx="2.5" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="m7.5 9.5 3 2.5-3 2.5M12.5 15h4" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></symbol>
<symbol id="i-chip" viewBox="0 0 24 24"><rect x="6.5" y="6.5" width="11" height="11" rx="2" fill="none" stroke="currentColor" stroke-width="1.7"/><path d="M9.5 3.5v3M14.5 3.5v3M9.5 17.5v3M14.5 17.5v3M3.5 9.5h3M3.5 14.5h3M17.5 9.5h3M17.5 14.5h3" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></symbol>
</defs></svg>
"""


def topbar() -> str:
    return f"""{SPRITE}
<header class="bar" id="top">
  <a class="brand" href="{HF_MODEL}" target="_blank" rel="noopener" aria-label="humanizer">
    {icon("mark", "brand-mark")}<span class="brand-word">humanizer</span>
    <span class="brand-tag">12B · {L("DEMO", "在线试用")}</span>
  </a>
  <nav class="bar-right">
    <a class="bar-link" href="#local">{L("Download", "下载")}</a>
    {A(HF_MODEL, L("Model", "模型"), "bar-link hide-sm")}
    {A(GH, "GitHub", "bar-link hide-sm")}
    <button class="icon-btn" type="button" data-act="theme" title="Light / dark" aria-label="Light / dark">{icon("moon", "only-light")}{icon("sun", "only-dark")}</button>
    <div class="seg" role="group" aria-label="Language">
      <button type="button" data-setlang="zh" aria-label="中文">中</button><button type="button" data-setlang="en" aria-label="English">EN</button>
    </div>
  </nav>
</header>
<section class="hero">
  <h1 class="display">{L('Rewrite AI drafts so they read like <span class="ins">a person</span> wrote them.',
                         '把 AI 写的草稿，改成<span class="ins">像人写的</span>。')}</h1>
  {P("A 12B model for English and Chinese. It is trained to keep every number, unit, date, name and quote, and it runs on your own computer. Try it here, then take it home.",
     "一个 12B 改写模型，中英文都行。训练目标是数字、单位、日期、人名、引语原样保留，可以在你自己的电脑上跑。先在这里试，再装到本地。",
     "lede")}
</section>
"""


def draft_head() -> str:
    return f"""<div class="sheet-head">
  <span class="idx">01</span><h2 class="sheet-title">{L("Draft", "草稿")}</h2>
  <span class="sheet-sub">{L("what the AI wrote", "AI 写的原稿")}</span>
  <span class="meta mono" id="draft-count"><b>0</b> {L("words", "字")} <span class="dim">/ {L(f"{MAX_WORDS * PARTS:,}", f"{MAX_CJK * PARTS:,}")}</span></span>
</div>"""


def draft_foot(examples) -> str:
    short = {"en-email": ("Email", "英文邮件"), "en-review": ("Review", "产品评测"),
             "en-essay": ("Essay", "英文作文"), "zh-email": ("中文邮件", "中文邮件"),
             "zh-social": ("中文社交帖", "中文社交帖")}
    btns = "".join(f'<button type="button" class="sample-btn" data-sample="{e["id"]}">{L(*short[e["id"]])}</button>'
                   for e in examples if e["id"] in short)
    return f"""<div class="sheet-foot">
  <span class="try">{L("Try one:", "试试示例：")}</span>{btns}
  <span class="grow"></span>
  <button type="button" class="tool" data-act="clear">{icon("eraser")}{L("Clear", "清空")}</button>
</div>"""


def rail() -> str:
    return f"""<div class="go-label">{L("Rewrite", "改写")}</div><kbd class="go-kbd mono">⌘↵</kbd>"""


def editor_note() -> str:
    return P(
        f"Runs the full bf16 model on a shared Hugging Face GPU (ZeroGPU). Up to about {MAX_WORDS * PARTS:,} English "
        f"words or {MAX_CJK * PARTS:,} Chinese characters per run; anything over {MAX_WORDS} words or {MAX_CJK:,} "
        f"characters is split at paragraph breaks and rewritten part by part. Free GPU time per visitor is limited, "
        f"so the first run can wait in a queue. For long documents use the app or <code>hz</code> "
        f"(<a href=\"#local\">below</a>). Your text is not stored: this page logs counts and timing only.",
        f"这里跑的是完整的 bf16 模型，用 Hugging Face 的共享 GPU（ZeroGPU）。每次最多英文约 {MAX_WORDS * PARTS:,} 词、"
        f"中文约 {MAX_CJK * PARTS:,} 字；超过英文 {MAX_WORDS} 词或中文 {MAX_CJK:,} 字的，会在段落之间切开、一段一段改。"
        f"每位访客的免费 GPU 时长有限，第一次可能要排队。长文档请用 App 或 "
        f"<code>hz</code>（见<a href=\"#local\">下方</a>）。不保存你的文字：只记录字数和耗时。",
        "editor-note")


# ───────────────────────────────────────────────────────────── examples

def examples_section(examples) -> str:
    radios, tabs, panels = [], [], []
    for k, e in enumerate(examples):
        d_html, o_html, ratio = diffmark.marked_pair(e["draft"], e["output"])
        cjk = e["lang"] == "zh"
        unit = L("chars", "字") if cjk else L("words", "词")
        lang_cls = " cjk" if cjk else ""
        checked = " checked" if k == 0 else ""
        radios.append(f'<input type="radio" name="ex" id="ex-{k}" class="ex-radio"{checked}>')
        tabs.append(f'<label for="ex-{k}" class="ex-tab">{L(e["genre"]["en"], e["genre"]["zh"])}</label>')
        nk, nt = e["numbers"]
        meta = (f'{L("Draft by", "草稿写手：")} {html.escape(e["writer"])} · '
                f'{e["words"][0]:,} → {e["words"][1]:,} {unit} · '
                f'{L("copy", "照抄率")} {e["copy"]:.2f} · '
                f'{L("numbers", "数字")} {nk}/{nt} · '
                f'{L("fact judge: no error found", "事实判官：未发现错误")}')
        panels.append(f"""<div class="ex-panel" data-k="{k}">
  <div class="pair">
    <article class="sheet ex-sheet">
      <div class="sheet-head"><span class="idx">01</span><h3 class="sheet-title">{L("Draft", "草稿")}</h3>
        <span class="sheet-sub">{L("held-out eval draft", "留出评测集草稿")}</span></div>
      <div class="textview{lang_cls}">{d_html}</div>
    </article>
    <article class="sheet ex-sheet">
      <div class="sheet-head"><span class="idx">02</span><h3 class="sheet-title">{L("Rewrite", "改写")}</h3>
        <span class="sheet-sub">{L(f"1 of {e['of']} samples, unedited", f"{e['of']} 发里挑的一发，未经修改")}</span>
        <span class="meta mono"><b>{round(ratio * 100)}%</b> {L("changed", "改动")}</span></div>
      <div class="textview{lang_cls}">{o_html}</div>
    </article>
  </div>
  <div class="ex-meta"><span class="mono small">{meta}</span><span class="grow"></span>
    <button type="button" class="btn" data-sample="{e['id']}">{icon("arrow")}{L("Put this draft in the editor", "把这篇放进编辑器")}</button></div>
</div>""")
    return f"""<section class="sec" id="examples">
  <p class="eyebrow">{L("Examples", "示例")}</p>
  <h2 class="sec-title">{L("Real before and after", "改写前后，真实输出")}</h2>
  {P("Drafts from our held-out evaluation set, never seen in training. For each we generated 8 samples with the Q8_0 file you download (llama.cpp, the app's settings) and picked the one that reads best among those our fact judge passed; it is shown unedited, only whitespace is normalised. All 8 samples are in the public eval folder. "
     "<span class='ins'>Highlight</span> = new wording, <span class='del'>strikethrough</span> = draft wording that was replaced. "
     "Across the whole set the model does sometimes change a detail, usually one word or one number; how often is below. Read the result before you send it, especially numbers, dates and names.",
     "草稿来自留出评测集，训练时从没见过。每篇我们用你下载的 Q8_0 文件（llama.cpp，和 App 相同的设置）生成 8 发，在事实判官判为通过的几发里挑了读起来最好的一发，未经任何修改，只统一了空白；8 发全文都在公开的评测文件里。"
     "<span class='ins'>荧光笔</span> = 新写的，<span class='del'>删除线</span> = 被改掉的原文。"
     "放到整个评测集上，模型偶尔还是会改错细节，多半是一个词或一个数字，多常见见下方。发出去之前请通读一遍，特别是数字、日期和人名。",
     "sec-lede")}
  <div class="ex-wrap">
    {"".join(radios)}
    <div class="ex-tabs">{"".join(tabs)}</div>
    {"".join(panels)}
  </div>
</section>"""


# ───────────────────────────────────────────────────────────── local use

def code_block(text: str) -> str:
    return (f'<div class="code"><pre><code>{html.escape(text)}</code></pre>'
            f'<button type="button" class="code-copy" data-act="copy-code" aria-label="Copy">{icon("copy")}'
            f'<span class="code-copy-label">{L("Copy", "复制")}</span></button></div>')


def local_section() -> str:
    hz_cmd = "pipx install git+https://github.com/sgaofen/humanize-model\nhz paper.md -o out.md"
    llama_cmd = ("llama-server --hf-repo jialinyyzz/humanizer --hf-file humanizer-12b-Q8_0.gguf "
                 "-c 8192 -np 1 -ngl 99")
    return f"""<section class="sec" id="local">
  <p class="eyebrow">{L("Download", "下载")}</p>
  <h2 class="sec-title">{L("Use it on your own computer", "装到自己的电脑上用")}</h2>
  {P("The same model, with no queue and no per-visitor GPU limit. Your text never leaves your machine, and after one download it works offline.",
     "同一个模型，不用排队，也没有 GPU 时长限制。文字不出你的电脑，下载一次之后完全离线。", "sec-lede")}
  <div class="cards">
    <article class="card card-app">
      <div class="card-head">{icon("down")}<h3>{L("The app", "App")}</h3><span class="badge">{L("easiest", "最省事")}</span></div>
      <div class="dl-row">
        {A(MAC_DMG, icon("apple") + '<span>' + L("Mac", "Mac") + '<small>' + L("Apple silicon · .dmg", "Apple 芯片 · .dmg") + '</small></span>', "dl")}
        {A(WIN_EXE, icon("win") + '<span>' + L("Windows", "Windows") + '<small>' + L("x64 · setup .exe", "x64 · 安装包 .exe") + '</small></span>', "dl")}
      </div>
      {P("Double-click it and the app opens in your browser. On first run it checks your memory, suggests a model size and downloads it once from Hugging Face. Paste a draft on the left; the rewrite streams in on the right with the new wording highlighted. Windows without installing, and checksums: <a href='" + REL + "' target='_blank' rel='noopener'>all release files</a>.",
         "双击后 App 会在浏览器里打开。第一次运行时它会看你的内存、推荐一个档位，从 Hugging Face 下载一次模型。左边贴草稿，右边流式出改写，新写的部分用荧光笔标出。Windows 免安装版和校验值见<a href='" + REL + "' target='_blank' rel='noopener'>全部发布文件</a>。")}
      <table class="tiers mono">
        <tr><th>{L("Memory", "内存")}</th><th>{L("File", "文件")}</th><th>{L("Download", "下载")}</th></tr>
        <tr><td>{L("32 GB or more", "32 GB 及以上")}</td><td>Q8_0</td><td>12.7 GB</td></tr>
        <tr><td>16 GB</td><td>Q6_K</td><td>10.0 GB</td></tr>
        <tr><td>{L("Less than 16 GB", "16 GB 以下")}</td><td>Q4_K_M</td><td>7.6 GB</td></tr>
      </table>
      <div class="notice">{icon("warn")}<div>{P(
          f"<b>Not code-signed yet</b>, so the first launch is blocked once. macOS: System Settings → Privacy &amp; Security → <b>Open Anyway</b>. Windows: <b>More info</b> → <b>Run anyway</b>. Step by step: {A(INSTALL_EN, 'INSTALL.md')}.",
          f"<b>还没有代码签名</b>，第一次打开会被拦一下。macOS：「系统设置 → 隐私与安全性」→ 点<b>「仍要打开」</b>；Windows：点<b>「更多信息」</b>→<b>「仍要运行」</b>。详细步骤：{A(INSTALL_ZH, 'INSTALL.zh.md')}。")}</div></div>
    </article>
    <article class="card">
      <div class="card-head">{icon("term")}<h3>{L("<code>hz</code> on the command line", "命令行 <code>hz</code>")}</h3><span class="badge soft">{L("long documents · agents", "长文档 · Agent")}</span></div>
      {code_block(hz_cmd)}
      {P("Rewrites a whole file: a short draft, long Markdown or a <code>.docx</code>. Headings, code blocks, tables and links stay as they are; prose is rewritten piece by piece, and every piece is checked for lost numbers and copying. It uses the app or a llama-server running the model.",
         "一行命令改写整个文件：短稿、长篇 Markdown 或 <code>.docx</code> 都行。标题、代码块、表格和链接原样保留，正文分块改写，每块都检查有没有丢数字、是不是照抄。需要 App 或 llama-server 在跑模型。")}
    </article>
    <article class="card">
      <div class="card-head">{icon("chip")}<h3>{L("llama.cpp, one line", "llama.cpp 一行命令")}</h3><span class="badge soft">{L("any OS", "任何系统")}</span></div>
      {code_block(llama_cmd)}
      {P("The first run downloads <code>humanizer-12b-Q8_0.gguf</code> (about 12.7 GB); on a 16 GB machine use <code>humanizer-12b-Q6_K.gguf</code> (about 10.0 GB), or <code>humanizer-12b-Q4_K_M.gguf</code> (about 7.6 GB) if disk is tight. This is a text-completion model, not a chat model: send the exact prompt from <code>prompt_format.json</code> to <code>/completion</code> with temperature 1.0, top_p 0.95, top_k 0, min_p 0.",
         "第一次会下载 <code>humanizer-12b-Q8_0.gguf</code>（约 12.7 GB）；16 GB 内存的机器换成 <code>humanizer-12b-Q6_K.gguf</code>（约 10.0 GB），硬盘紧张就用 <code>humanizer-12b-Q4_K_M.gguf</code>（约 7.6 GB）。这是文本续写模型，不是聊天模型：按 <code>prompt_format.json</code> 逐字拼好提示词，发到 <code>/completion</code>，temperature 1.0、top_p 0.95、top_k 0、min_p 0。国内下载慢可以在命令前加 <code>HF_ENDPOINT=https://hf-mirror.com</code>。")}
    </article>
  </div>
  <div class="docs">
    <span class="docs-label">{L("Full guides", "完整文档")}</span>
    {A(USAGE_EN, '<b>USAGE.md</b><small>' + L("usage without the app (English)", "不用 App 怎么用（英文）") + '</small>', "doc")}
    {A(USAGE_ZH, '<b>USAGE.zh.md</b><small>' + L("usage without the app (Chinese)", "不用 App 怎么用（中文）") + '</small>', "doc")}
    {A(AGENTS, '<b>AGENTS.md</b><small>' + L("for AI agents: files, server, exact prompt, self-test", "给 AI Agent：文件、起服务、逐字提示词、自检") + '</small>', "doc")}
    {A(HF_MODEL, '<b>' + L("Model card", "模型卡") + '</b><small>jialinyyzz/humanizer</small>', "doc")}
  </div>
  {P("Setting it up with an AI agent (Claude Code, Codex, Cursor)? Point it at " + A(AGENTS, "AGENTS.md") + ".",
     "想让 AI Agent（Claude Code、Codex、Cursor）帮你装？把 " + A(AGENTS, "AGENTS.md") + " 丢给它。", "tip")}
</section>"""


# ───────────────────────────────────────────────────────────── results

def bar(label: str, n: int, total: int, strong: bool) -> str:
    pct = 100 * n / total
    return (f'<div class="bar-row{" strong" if strong else ""}"><div class="bar-label">{label}</div>'
            f'<div class="bar-track" title="{n} / {total}"><div class="bar-fill" style="width:{pct:.1f}%"></div></div>'
            f'<div class="bar-val mono"><b>{n}</b> / {total}</div></div>')


def results_section() -> str:
    tiles = [
        ("11 / 210", L("English drafts flagged as AI by Originality.ai", "篇英文草稿被 Originality.ai 判为 AI"),
         L("API v3, AI Allowance 0% (its strictest setting), bf16 weights, measured 2026-10-02, first sample of each draft. 95% judged human. Previous release (v1): 26 / 210. The Q8_0 file you download: 15 / 210, within noise of bf16.",
           "API v3，AI Allowance 0%（最严档），bf16 权重，2026-10-02 实测，每篇取第 1 发。95% 判为人写。上一版：26 / 210。你下载的 Q8_0 文件：15 / 210，和 bf16 的差别在噪声范围内。")),
        ("376 / 420", L("English rewrites with no factual problem found", "篇英文改写，判官没挑出事实问题"),
         L("Strict LLM judge (GLM-5.3), one vote per rewrite, measured on the Q8_0 file you download. Previous release (v1): 369 / 420. Where it did find a problem, more than 9 in 10 fixes are a single word or phrase, like “The remaining 37 complaints” coming out as “The other 37% of complaints”. The judge is deliberately strict: in a separate audit, a second judge agreed with 99 of 150 of its serious flags, rated 41 minor and found 10 unchanged.",
           "LLM 判官 GLM-5.3，每篇一票、从严，测的是你下载的 Q8_0 文件。上一版（v1）：420 篇里 369 篇。有问题的，9 成以上改一个词或短语就好，比如“The remaining 37 complaints”（剩下的 37 条投诉）被写成了“The other 37% of complaints”（另外 37% 的投诉）。判官是故意设得从严的：另一次抽查里，第二个判官对它判为严重的 150 篇只认可 99 篇，41 篇算轻微，10 篇事实其实没变。")),
        ("149 / 204", L("Chinese rewrites with no factual problem found", "篇中文改写，判官没挑出事实问题"),
         L("Chinese is still catching up with English (previous release: 135 / 204). Where the judge found a problem, about 9 in 10 fixes are a single word or phrase, like “本月20日前后” (around the 20th of this month) becoming “20号以前” (before the 20th).",
           "中文还在追赶英文（上一版：204 篇里 135 篇）。有问题的，约 9 成改一个词或短语就好，比如“本月20日前后”写成了“20号以前”。")),
        ("0.165", L("median reuse of the draft", "照抄程度中位数（复用率）"),
         L("The larger of verbatim 5-gram copy and syntactic-skeleton reuse; lower means a deeper rewrite. Previous release (v1): 0.19.",
           "取逐字 5-gram 照抄和句法骨架复用里较大的那个，越低改得越深。上一版（v1）：0.19。")),
    ]
    tiles_html = "".join(f'<div class="tile"><div class="tile-num">{n}</div><div class="tile-label">{a}</div>'
                         f'<div class="tile-note">{b}</div></div>' for n, a, b in tiles)
    bars = (bar(L(f"{A(BLADER, 'blader/humanizer')} skill (v3.1.0, Claude Sonnet following its rules)",
                  f"{A(BLADER, 'blader/humanizer')} skill（v3.1.0，Claude Sonnet 按它的规则改）"), 60, 60, False)
            + bar(L("humanizer 12B (this model, bf16)", "humanizer 12B（本模型，bf16）"), 4, 60, True))
    return f"""<section class="sec" id="results">
  <p class="eyebrow">{L("Results", "评测")}</p>
  <h2 class="sec-title">{L("What it does, measured", "实测结果")}</h2>
  {P("All numbers come from our own evaluation set: 312 drafts (210 English, 102 Chinese) across 18 genres, written from scratch by three frontier models (GLM-5.3, GPT-5.6 luna and Claude Sonnet, about a third each). None of them were used in training. Each draft was rewritten twice.",
     "所有数字都来自我们自建的评测集：312 篇草稿（英文 210 篇、中文 102 篇），18 种体裁，由三个前沿模型从零写成（GLM-5.3、GPT-5.6 luna、Claude Sonnet 各约三分之一）。这些草稿都没有进训练。每篇改写 2 发。",
     "sec-lede")}
  <div class="tiles">{tiles_html}</div>
  <div class="card bars">
    <h3 class="bars-title">{L("Same 60 English drafts: how many Originality.ai flagged as AI", "同一批 60 篇英文草稿：被 Originality.ai 判为 AI 的篇数")}</h3>
    {bars}
    {P("blader/humanizer is the most popular de-AI skill on GitHub (53k stars). The two are different kinds of tool (a rule list for a general model vs. a fine-tuned rewriter), so read this as a comparison of outcomes on the same inputs, not of methods.",
       "blader/humanizer 是 GitHub 上最火的去 AI 味 skill（5.3 万星）。两者是不同类型的工具（给通用模型的一套规则 vs. 专门微调的改写模型），所以这里比的是同一批输入上的结果，不是方法本身。",
       "fine")}
  </div>
  {P("<b>Where it still fails:</b> the most templated genres. Social posts with emoji, hashtags or “1/ 2/” threads: 3 / 16 flagged. Formal policy memos: 2 / 13. Detectors change: this is what one detector said on one date, not a promise about any other detector or date.",
     "<b>还会失败的地方：</b>最模板化的体裁。带 emoji、井号或“1/ 2/”连载的社交帖：3 / 16 被判 AI；正式政策备忘：2 / 13。检测器会更新，这只是一个检测器在某一天的结果，不代表其他检测器或其他时间。",
     "sec-p")}
  <div class="callout">{P("<b>No AI detector was used anywhere in training:</b> not as a reward, not as a filter, not to pick a checkpoint. The model learns from how people actually write and from whether the facts survived. Detector numbers on this page are only an external check.",
                          "<b>训练全程没有用任何 AI 检测器：</b>不当奖励，不当过滤条件，也不用来挑检查点。模型只从两样东西里学：真人怎么写，以及事实有没有保住。本页的检测器数字只是外部核对。")}</div>
  <h3 class="sub-title">{L("How it was trained", "怎么训的")}</h3>
  <ol class="steps">
    <li>{P("<b>Supervised fine-tuning, 28,598 pairs</b> of AI draft → real human original. The human side is always real human writing: paper abstracts, government reports, student essays, company and mailing-list email, Reddit, Hacker News, Zhihu and more. The AI side is a draft that a frontier model wrote back from the human text.",
           "<b>监督微调（SFT），28,598 对</b>“AI 草稿 → 真人原文”。人写一侧全是真人文本：论文摘要、政府报告、学生作文、公司和邮件列表邮件、Reddit、Hacker News、知乎等；AI 一侧是前沿模型照着真人原文反写出来的草稿。")}</li>
    <li>{P("<b>DPO, 3,918 preference pairs</b>, chosen only on fact fidelity and on how much the output copies the draft (LLM judge GLM-5.3).",
           "<b>DPO，3,918 对偏好对</b>，只按事实忠实度和照抄程度挑选（LLM 判官 GLM-5.3）。")}</li>
    <li>{P("<b>Reinforcement learning (GRPO) in three rounds, 500 steps in total</b> (200 + 150 + 150). Round 1 used a strict single-vote fact judge. In rounds 2 and 3 the reward is an LLM judge that reads the whole rewrite against the draft (penalising severe errors, invented content, changed meaning and dropped formatting), plus a copy penalty; round 3 drew its drafts from a genre-balanced pool of 8,268. In all, RL generated 41,600 rewrites, each scored by an LLM judge against its draft. v1 was released after round 2; this release, v2, is the final checkpoint of round 3.",
           "<b>强化学习（GRPO）分三轮，共 500 步</b>（200 + 150 + 150）。第一轮用单票从严的事实判官；第二、三轮的奖励 = LLM 判官把改写和草稿对照通读、核对事实（严重错、编造、改了意思、丢格式都扣分），再加照抄惩罚；第三轮的草稿取自按体裁配平的 8,268 篇草稿池。RL 一共生成了 41,600 篇改写，每篇都由 LLM 判官对照草稿打分。第二轮结束时发布了 v1；本版 v2 是第三轮的最后一个检查点。")}</li>
  </ol>
</section>"""


def limits_section() -> str:
    items = [
        ("<b>It can still change a detail.</b> A strict LLM judge found no factual problem in 376 of 420 English rewrites; where it found one, more than 9 in 10 fixes are a single word or phrase, such as “The remaining 37 complaints” becoming “The other 37% of complaints”. Still, read the result before you send it, especially numbers, dates, names (and the direction of every claim).",
         "<b>细节还可能出错。</b>英文 420 篇改写里，从严的 LLM 判官在 376 篇里没挑出事实问题；有问题的，9 成以上改一个词或短语就好，比如“The remaining 37 complaints”（剩下的 37 条投诉）被写成了“The other 37% of complaints”（另外 37% 的投诉）。发出去之前还是请读一遍，重点看数字、日期、人名（和每个论断的方向）。"),
        ("<b>Chinese is still catching up with English:</b> no factual problem in 149 of 204 Chinese rewrites; where there was one, about 9 in 10 fixes are a single word or phrase, such as “本月20日前后” (around the 20th of this month) becoming “20号以前” (before the 20th).",
         "<b>中文还在追赶英文：</b>中文 204 篇改写里 149 篇判官没挑出事实问题；有问题的，约 9 成改一个词或短语就好，比如“本月20日前后”写成了“20号以前”。"),
        ("<b>Templated genres still look machine-made to detectors:</b> social posts with emoji, hashtags or numbered threads (3/16 flagged) and formal policy memos (2/13).",
         "<b>模板化体裁仍容易被检测器认出：</b>带 emoji、井号或编号连载的社交帖（3/16 被判 AI），正式政策备忘（2/13）。"),
        ("<b>Formatting is not always kept.</b> 28 of 420 outputs dropped a format element. Paragraph breaks and list, heading or code markup sometimes change.",
         "<b>格式不一定保得住。</b>420 发里有 28 发丢了格式要素；分段、列表、标题和代码标记有时会变。"),
        ("<b>Register can drift in casual genres.</b> In Reddit-style posts it sometimes adds slang or profanity that wasn't in the draft.",
         "<b>随意体裁里语气会跑。</b>Reddit 一类的帖子里，它有时会加上原文没有的俚语或粗口。"),
        ("<b>Detectors change.</b> The detection numbers above are one measurement on one date. Nothing here guarantees a result on any detector.",
         "<b>检测器会变。</b>上面的检测数字是某一天的一次测量，不保证在任何检测器上的结果。"),
        ("It is a writing tool for your own drafts. Where a school, employer or publication has rules about AI assistance, follow them.",
         "这是给你改自己草稿的写作工具。学校、单位或出版方对 AI 辅助有规定的，请按规定来。"),
    ]
    lis = "".join(f"<li>{P(en, zh, tag='span')}</li>" for en, zh in items)
    return f"""<section class="sec" id="limits">
  <p class="eyebrow">{L("Limitations", "局限")}</p>
  <h2 class="sec-title">{L("Read this before you trust it", "用之前先看这里")}</h2>
  <ul class="limits">{lis}</ul>
</section>"""


def footer() -> str:
    return f"""<footer class="foot">
  {P(f"Code and weights: Apache 2.0. Fine-tuned from {A('https://huggingface.co/google/gemma-4-12B', 'google/gemma-4-12B')} (Apache 2.0); this project is not affiliated with or endorsed by Google. "
     f"",
     f"代码和权重：Apache 2.0。由 {A('https://huggingface.co/google/gemma-4-12B', 'google/gemma-4-12B')} 微调而来（Apache 2.0），本项目与 Google 无隶属或背书关系。"
     f"")}
  <p class="foot-links">{A(HF_MODEL, "Hugging Face")} · {A(GH, "GitHub")} · {A(REL, L("App releases", "App 下载"))} · {A(USAGE_EN, "USAGE.md")} · {A(USAGE_ZH, "USAGE.zh.md")} · {A(AGENTS, "AGENTS.md")}</p>
</footer>"""


def samples_script(examples) -> str:
    data = {e["id"]: e["draft"] for e in examples}
    blob = json.dumps(data, ensure_ascii=False).replace("</", "<\\/")
    return f"<script>window.HZ_SAMPLES = {blob};</script>"
