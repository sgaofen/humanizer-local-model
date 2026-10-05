"""humanizer demo Space (ZeroGPU): the 12B model from jialinyyzz/humanizer, served with transformers.

Facts this file must keep exact (see AGENTS.md in the release repo):

* Text completion, not chat. prompt = instr + "\\n\\n" + draft.strip() + sep, with instr and sep read
  from prompt_format.json beside the weights. Startup asserts the fingerprint cc51d66b4c593fbe.
* Sampling: temperature 1.0, top_p 0.95, top_k 0 (explicit: generation_config.json says 64),
  repetition_penalty 1.0, stop on EOS only. max_new_tokens = 2.5 x draft tokens, clamped to 256-2048.
* The weights at the repo root are the 12B (bf16 model.safetensors).

ZeroGPU: the model is moved to cuda at import time (ZeroGPU's recommended pattern; the real GPU only
exists inside @spaces.GPU calls). Each call asks for a GPU slot sized to the draft, and generation
carries a max_time a little under that slot so a long sample is cut off cleanly instead of killed.

HUMANIZER_MOCK=1 runs the page without torch or the model, for local UI work.
"""
from __future__ import annotations

import hashlib
import html
import json
import os
import re
import threading
import time
import uuid
from pathlib import Path

import gradio as gr

import hz_text
import page

HERE = Path(__file__).resolve().parent
MODEL_ID = "jialinyyzz/humanizer"
USAGE_DATASET = "jialinyyzz/humanizer-usage"
FINGERPRINT = "cc51d66b4c593fbe"
MOCK = os.environ.get("HUMANIZER_MOCK") == "1"

MAX_WORDS, MAX_CJK, PARTS = page.MAX_WORDS, page.MAX_CJK, page.PARTS
MAX_DRAFT_TOKENS = 1400      # backstop behind the word / character cap
TPS_EST = 16.0               # decode speed used to size the GPU slot (measured 17.3-17.5 tok/s on ZeroGPU, bf16)
SLOT_MIN, SLOT_MAX = 30, 120  # seconds requested from ZeroGPU
SLOT_OVERHEAD = 12           # GPU hand-over and prefill (measured about 2.5 s) plus margin

# ───────────────────────────────────────────────────────────── prompt

if MOCK:
    FMT = {
        "instr": "Rewrite the text below so it reads like a person wrote it, not a language model.\n\n"
                 "Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\n"
                 "throat-clearing, and any sentence that only announces what comes next.\n"
                 "Prefer the concrete word over the abstract one. It is fine to sound uneven.\n\n"
                 "Every fact, number, unit, date, name and quotation must survive unchanged.",
        "sep": "\n\n### Rewritten:\n\n",
    }
else:
    import spaces  # noqa: E402  (import before torch touches CUDA)
    import torch
    from huggingface_hub import hf_hub_download
    from transformers import AutoModelForCausalLM, AutoTokenizer, TextIteratorStreamer

    with open(hf_hub_download(MODEL_ID, "prompt_format.json"), encoding="utf-8") as fh:
        FMT = json.load(fh)


def build_prompt(draft: str) -> str:
    return FMT["instr"] + "\n\n" + draft.strip() + FMT["sep"]


_fp = hashlib.sha256(build_prompt("X").encode("utf-8")).hexdigest()[:16]
assert _fp == FINGERPRINT, f"prompt fingerprint {_fp} != {FINGERPRINT}: prompt_format.json is not the 12B one"

# ───────────────────────────────────────────────────────────── model

if not MOCK:
    tokenizer = AutoTokenizer.from_pretrained(MODEL_ID)
    model = AutoModelForCausalLM.from_pretrained(MODEL_ID, dtype=torch.bfloat16)
    assert model.config.model_type == "gemma4_unified" and model.config.text_config.num_hidden_layers == 48, \
        "not the 12B checkpoint"
    model.to("cuda").eval()
    EOS = tokenizer.eos_token_id
    print(f"[humanizer] loaded {type(model).__name__}, "
          f"{sum(p.numel() for p in model.parameters()) / 1e9:.2f}B params, eos={EOS}", flush=True)

    def count_tokens(text: str) -> int:
        return len(tokenizer(text)["input_ids"])
else:
    def count_tokens(text: str) -> int:
        return int(hz_text.latin_words(text) * 1.15 + hz_text.cjk_chars(text) * 0.62) + 1


def plan(n_tok: int):
    """(max_new_tokens, max_time seconds, ZeroGPU slot seconds) for a draft of n_tok tokens."""
    max_new = max(256, min(2048, round(2.5 * n_tok)))
    expected = 1.5 * n_tok + 64                       # a rewrite is about as long as its draft
    budget = min(SLOT_MAX - SLOT_OVERHEAD, max(SLOT_MIN - SLOT_OVERHEAD, 6 + expected / TPS_EST))
    return max_new, round(budget, 1), int(budget + SLOT_OVERHEAD)


if not MOCK:
    def _slot(prompt: str, max_new: int, budget: float) -> int:
        return int(budget + SLOT_OVERHEAD)

    @spaces.GPU(duration=_slot)
    def _generate(prompt: str, max_new: int, budget: float):
        enc = tokenizer(prompt, return_tensors="pt").to(model.device)
        streamer = TextIteratorStreamer(tokenizer, skip_prompt=True, skip_special_tokens=True,
                                        timeout=budget + 30)
        box = {}

        def work():
            try:
                with torch.inference_mode():
                    box["ids"] = model.generate(
                        **enc, streamer=streamer, max_new_tokens=max_new, max_time=budget,
                        do_sample=True, temperature=1.0, top_p=0.95, top_k=0, repetition_penalty=1.0,
                        pad_token_id=tokenizer.pad_token_id, eos_token_id=EOS)
            except Exception as exc:  # surface it instead of hanging the streamer
                box["err"] = f"{type(exc).__name__}: {exc}"
                streamer.end()

        t0 = time.time()
        th = threading.Thread(target=work, daemon=True)
        th.start()
        for piece in streamer:
            if piece:
                yield ("text", piece)
        th.join()
        if "err" in box:
            yield ("error", box["err"])
            return
        new = box["ids"][0, enc["input_ids"].shape[1]:]
        n = int(new.shape[0])
        yield ("done", {"tokens": n, "eos": n > 0 and int(new[-1]) == EOS, "gen_seconds": time.time() - t0})
else:
    def _generate(prompt: str, max_new: int, budget: float):
        draft = prompt[len(FMT["instr"]) + 2:-len(FMT["sep"])]
        out = next((e["output"] for e in EXAMPLES if e["draft"] == draft), None)
        if out is None:  # crude stand-in: shuffle sentence order a little
            parts = re.split(r"(?<=[.!?。！？])\s*", draft)
            out = " ".join(parts[1:2] + parts[:1] + parts[2:]).strip()
        time.sleep(1.2)
        t0, n = time.time(), 0
        for k in range(0, len(out), 6):
            time.sleep(0.02)
            n += 1
            yield ("text", out[k:k + 6])
        yield ("done", {"tokens": n, "eos": True, "gen_seconds": time.time() - t0})


# ───────────────────────────────────────────────────────────── usage counter

# Counts only. No draft text, no output text, no IP, nothing that identifies a person: people paste
# text here they would rather not hand around. Needs a write token in the Space secret HF_TOKEN;
# without one this is a no-op and the app still runs.

_LOG_DIR = HERE / "usage_log"
_LOG_FILE = _LOG_DIR / f"{uuid.uuid4().hex}.jsonl"
_scheduler = None
if os.environ.get("HF_TOKEN") and not MOCK:
    try:
        from huggingface_hub import CommitScheduler

        _LOG_DIR.mkdir(exist_ok=True)
        _scheduler = CommitScheduler(repo_id=USAGE_DATASET, repo_type="dataset", folder_path=_LOG_DIR,
                                     path_in_repo="raw", every=10, private=True)
    except Exception as exc:  # never let telemetry break the demo
        print(f"[usage] disabled: {exc}", flush=True)


def log_use(**row):
    if _scheduler is None:
        return
    try:
        row = {"ts": int(time.time()), "model": "12b", **row}
        with _scheduler.lock, _LOG_FILE.open("a") as fh:
            fh.write(json.dumps(row) + "\n")
    except Exception as exc:
        print(f"[usage] write failed: {exc}", flush=True)


# ───────────────────────────────────────────────────────────── output panel

L, P = page.L, page.P


def _icon(name):
    return page.icon(name)


def _sheet(state: str, body: str, meta: str = "", foot: str = "", notices: str = "", plain: str = "",
           cjk: bool = False) -> str:
    cls = " cjk" if cjk else ""
    return f"""<div class="out-wrap" data-state="{state}">
  <div class="sheet-head"><span class="idx">02</span><h2 class="sheet-title">{L("Rewrite", "改写")}</h2>
    <span class="sheet-sub">{L("reads like a person", "像人写的")}</span>
    <span class="meta mono">{meta}</span>
    <button type="button" class="tool toggle head-toggle" data-act="diff">{_icon("diff")}{L("Show changes", "显示改动")}</button>
  </div>
  <div class="out-body"><div class="textview{cls}">{body}</div></div>
  {notices}
  <div class="sheet-foot">{foot}</div>
  <pre class="plain" hidden>{html.escape(plain)}</pre>
</div>"""


def panel_empty() -> str:
    body = f"""<div class="empty">
  <svg class="empty-mark" viewBox="0 0 40 24" aria-hidden="true"><use href="#i-mark"/></svg>
  <p class="empty-title">{L("The rewrite shows up here", "改写会出现在这里")}</p>
  <p class="empty-sub">{L("Paste a draft or pick an example, then press the arrow", "贴好草稿或选一个示例，再点中间的箭头")}</p>
</div>"""
    return _sheet("empty", body)


def panel_wait() -> str:
    body = f"""<div class="empty">
  <span class="spinner" aria-hidden="true"></span>
  <p class="empty-title">{L("Waiting for a GPU…", "正在等 GPU……")}</p>
  <p class="empty-sub">{L("ZeroGPU hands out a GPU per request. After a quiet spell the first run also moves the model onto it.",
                          "ZeroGPU 每次请求分配一块 GPU。空闲一阵后的第一次，还要先把模型搬上去。")}</p>
</div>"""
    return _sheet("wait", body)


def panel_stream(text: str, cjk: bool, part: int = 1, parts: int = 1) -> str:
    n = hz_text.count_words(text)
    prog = L(f" · part {part}/{parts}", f" · 第 {part}/{parts} 段") if parts > 1 else ""
    return _sheet("stream", html.escape(text) + '<span class="caret"></span>', cjk=cjk,
                  meta=L(f"<b>{n:,}</b> " + ("chars" if cjk else "words"), f"<b>{n:,}</b> 字") + prog)


def panel_message(kind: str, en: str, zh: str) -> str:
    notice = f'<div class="notice {kind}">{_icon("warn")}<div>{P(en, zh)}</div></div>'
    return _sheet("error", "", notices=notice)


def _join(xs, zh=False):
    return ("、" if zh else ", ").join(html.escape(x) for x in xs)


def panel_done(draft: str, out: str, info: dict, wall: float, cjk: bool, extra=()) -> str:
    import diffmark

    chk = hz_text.check(draft, out, truncated=not info.get("eos", True))
    _, out_html, ratio = diffmark.marked_pair(draft, out)
    n_out = hz_text.count_words(out)
    meta = L(f"<b>{n_out:,}</b> {'chars' if cjk else 'words'} · <b>{round(ratio * 100)}%</b> changed",
             f"<b>{n_out:,}</b> 字 · 改动 <b>{round(ratio * 100)}%</b>")

    total_nums = len(hz_text.numbers(draft))
    miss = chk.missing_numbers
    if miss:
        num_chip = (f'<span class="chip warn" title="{html.escape(", ".join(miss))}">{_icon("warn")}'
                    f'{L(f"Check {len(miss)} number" + ("s" if len(miss) > 1 else ""), f"{len(miss)} 个数字请核对")}</span>')
    else:
        num_chip = f'<span class="chip ok">{_icon("check")}{L(f"Numbers {total_nums}/{total_nums}", f"数字 {total_nums}/{total_nums}")}</span>'
    copy_chip = (f'<span class="chip{" warn" if chk.copy > 0.5 else ""}" '
                 f'title="Share of the rewrite&#39;s 5-grams (Chinese: 6-character grams) found in the draft">'
                 f'{L("copy", "照抄率")} <b>{chk.copy:.2f}</b></span>')
    tok, gs = info.get("tokens", 0), max(info.get("gen_seconds", 0.0), 1e-6)
    stats = f'<span class="stats mono dim">{tok} tok · {tok / gs:.1f} tok/s · {L(f"{wall:.1f} s", f"{wall:.1f} 秒")}</span>'
    copy_btn = (f'<button type="button" class="tool solid" data-act="copy-out">{_icon("copy")}'
                f'<span class="when-idle">{L("Copy", "复制")}</span><span class="when-done">{L("Copied", "已复制")}</span></button>')
    foot = f'{num_chip}{copy_chip}{stats}<span class="grow"></span>{copy_btn}'

    notes = list(extra)   # (kind, en, zh) from the run itself: GPU stopped mid-way, parts left out
    if miss:
        notes.append(("warn",
                      f"These numbers from the draft don't appear in the rewrite: <b>{_join(miss)}</b>. They may be spelled out (“60 minutes” → “an hour”) or actually dropped. Check them against your draft.",
                      f"原稿里的这些数字在改写里没找到：<b>{_join(miss, True)}</b>。可能被写成了文字（如“60 分钟”→“一个小时”），也可能真丢了。请对照原稿确认。"))
    if chk.added_numbers:
        notes.append(("warn",
                      f"Numbers in the rewrite that are not in the draft: <b>{_join(chk.added_numbers)}</b>. Arithmetic the model did, or an invented figure. Check them.",
                      f"改写里多出了草稿里没有的数字：<b>{_join(chk.added_numbers, True)}</b>。可能是模型自己换算的，也可能是编的，请核对。"))
    if "truncated" in chk.issues:
        notes.append(("warn",
                      "Stopped at this demo's length or time limit, so the ending is cut off. Split the draft, or use the app or <code>hz</code> for long text.",
                      "达到了本页的长度或时间上限，结尾被截断了。请分段改写，或者用 App / <code>hz</code> 处理长文。"))
    if chk.copy > 0.5:
        notes.append(("warn",
                      f"This sample copies a lot of the draft (copy {chk.copy:.2f}). Press the arrow again for a new one.",
                      f"这一发照抄原稿较多（照抄率 {chk.copy:.2f}），再点一次箭头换一发。"))
    if "repeated" in chk.issues:
        notes.append(("warn",
                      "Two sentences in this sample say nearly the same thing. Press the arrow again for a new one.",
                      "这一发里有两句话意思几乎重复，再点一次箭头换一发。"))
    if "too_short" in chk.issues:
        notes.append(("warn",
                      "This sample is much shorter than the draft; something may be missing. Press the arrow again for a new one.",
                      "这一发比原稿短很多，可能漏了内容，再点一次箭头换一发。"))
    notes_html = "".join(f'<div class="notice {k}">{_icon("warn")}<div>{P(en, zh)}</div></div>' for k, en, zh in notes)
    notes_html += P("Sampling is random: press the arrow again for a different rewrite. Proofread numbers, dates, names and the direction of every claim before you use it.",
                    "每次采样都不一样，再点一次箭头会换一发。用之前请核对数字、日期、人名和每个论断的方向。", "out-note")
    return _sheet("done", out_html, meta=meta, foot=foot, notices=notes_html, plain=out, cjk=cjk)


# ───────────────────────────────────────────────────────────── handlers

def _size(draft: str) -> float:
    """English-word equivalents: a Chinese character counts MAX_WORDS / MAX_CJK words."""
    return hz_text.latin_words(draft) + hz_text.cjk_chars(draft) * MAX_WORDS / MAX_CJK


def _check_input(draft: str):
    """(n_tokens, None) or (None, (en, zh) message)."""
    if not draft:
        return None, ("Paste a draft first.", "先贴一篇草稿。")
    if _size(draft) > MAX_WORDS:
        n, unit = hz_text.count_words(draft), ("字" if hz_text.is_cjk(draft) else "词")
        return None, (f"That draft is about {n:,} words. This demo takes up to {MAX_WORDS} English words or {MAX_CJK:,} Chinese characters per run. Split it, or use the app or <code>hz</code> for long documents (see below).",
                      f"这篇约 {n:,} {unit}。本页每次最多英文 {MAX_WORDS} 词或中文 {MAX_CJK:,} 字。请分段，或者用 App / <code>hz</code> 处理长文（见下方）。")
    n_tok = count_tokens(draft)
    if n_tok > MAX_DRAFT_TOKENS:
        return None, (f"That draft is about {n_tok:,} tokens, more than this demo takes in one run. Split it, or use the app or <code>hz</code>.",
                      f"这篇约 {n_tok:,} 个 token，超过本页单次能处理的长度。请分段，或者用 App / <code>hz</code>。")
    return n_tok, None


def _gpu_error(msg: str):
    return ("warn" if "quota" in msg.lower() else "err",
            f"The GPU call failed: {html.escape(msg)}<br>If it says you are out of free GPU time, log in to Hugging Face, come back later, or run the model on your own computer (see below).",
            f"GPU 调用失败：{html.escape(msg)}<br>如果提示免费 GPU 时长用完，可以登录 Hugging Face、过一会儿再试，或者装到自己电脑上跑（见下方）。")


def _parts(draft: str):
    """[(part, sep)]: a draft over one run's size cut at paragraph breaks (an over-long paragraph at sentence
    ends) into parts of at most MAX_WORDS. sep is what joined the part to the next one in the draft."""
    sizer = hz_text.Sizer(MAX_WORDS, MAX_CJK)
    if sizer(draft) <= MAX_WORDS:
        return [(draft, "")]
    pieces = []
    for para in re.split(r"\n[ \t]*\n+", draft):
        para = para.strip()
        if not para:
            continue
        if sizer(para) > MAX_WORDS:
            cut, j = hz_text.cut_long(para, sizer), hz_text._joiner(para)
            pieces += [(c, j) for c in cut[:-1]] + [(cut[-1], "\n\n")]
        else:
            pieces.append((para, "\n\n"))
    groups, buf, size = [], [], 0.0
    for text, sep in pieces:
        w = sizer(text)
        if buf and size + w > MAX_WORDS:
            groups.append(buf)
            buf, size = [], 0.0
        buf.append((text, sep))
        size += w
    if buf:
        groups.append(buf)
    out = [("".join(t + sp for t, sp in g[:-1]) + g[-1][0], g[-1][1]) for g in groups]
    out[-1] = (out[-1][0], "")
    return out


def _err_kind(msg: str) -> str:
    m = msg.lower()
    return "quota" if "quota" in m else "timeout" if ("timeout" in m or "time limit" in m or "aborted" in m) else "other"


def rewrite(draft: str):
    """Never hand back nothing when something was written: parts that finished (and the half of a part the GPU
    stopped in) stay on screen with a notice saying what is missing. An empty sample gets one automatic retry."""
    draft = (draft or "").strip()
    if not draft:
        yield panel_message("warn", "Paste a draft first.", "先贴一篇草稿。")
        return
    cjk = hz_text.is_cjk(draft)
    parts = _parts(draft)
    todo, left = parts[:PARTS], parts[PARTS:]
    yield panel_wait()
    t0, done_out, done_draft = time.time(), "", ""
    toks, gsec, eos_all, stop, retries = 0, 0.0, True, None, 0
    for i, (part, sep) in enumerate(todo):
        n_tok = count_tokens(part)
        max_new, budget, _ = plan(n_tok)
        for attempt in range(2):                   # an empty sample gets one automatic retry
            text, info, last, err = "", None, 0.0, None
            try:
                for kind, val in _generate(build_prompt(part), max_new, budget):
                    if kind == "text":
                        text += val
                        if time.time() - last > 0.1:
                            last = time.time()
                            yield panel_stream(done_out + text, cjk, i + 1, len(todo))
                    elif kind == "done":
                        info = val
                    elif kind == "error":
                        raise RuntimeError(val)
            except Exception as exc:
                err = getattr(exc, "message", None) or str(exc) or type(exc).__name__
            if err or text.strip():
                break
            retries += 1
        out = text.strip()
        if out:
            done_out += out + (sep if not err else "")
            done_draft += part + (sep if not err else "")
            toks += (info or {}).get("tokens", 0) or 0
            gsec += (info or {}).get("gen_seconds", 0.0) or 0.0
            eos_all = eos_all and bool((info or {}).get("eos", False)) and not err
        if err or not out:
            stop = (i, err or "empty sample twice")
            break
    wall = time.time() - t0
    out_all, drafted = done_out.strip(), done_draft.strip()
    base = dict(in_units=hz_text.count_words(draft), in_tokens=count_tokens(draft), parts=len(parts),
                done_parts=(stop[0] if stop else len(todo)), retries=retries, seconds=round(wall, 1))
    if not out_all:
        if stop and stop[1] != "empty sample twice":
            log_use(outcome="error", err=_err_kind(stop[1]), **base)
            yield panel_message(*_gpu_error(stop[1]))
        else:
            log_use(outcome="empty", **base)
            yield panel_message("warn", "Two samples in a row came back empty. Press the arrow again.",
                                "连续两发都是空的，请再点一次箭头。")
        return
    extra = []
    unit = "字" if cjk else "词"
    if stop:
        i, msg = stop
        extra.append(("warn",
                      f"The GPU stopped during part {i + 1} of {len(todo)} ({html.escape(msg)}). Everything that was written is above; the rest of the draft was not rewritten. Press the arrow again later, log in to Hugging Face for more GPU time, or use the app or <code>hz</code>.",
                      f"改到第 {i + 1}/{len(todo)} 段时 GPU 停了（{html.escape(msg)}）。已经写出来的都在上面，后面的部分没有改。可以过一会儿再点一次、登录 Hugging Face 多拿些 GPU 时长，或者用 App / <code>hz</code>。"))
    if left:
        rest = sum(hz_text.count_words(p) for p, _ in left)
        extra.append(("warn",
                      f"This page rewrites up to {len(todo)} parts per run, so the last {rest:,} {'chars' if cjk else 'words'} of your draft were left out. Paste them as a new run, or use the app or <code>hz</code> for the whole document.",
                      f"本页每次最多改 {len(todo)} 段，草稿最后约 {rest:,} {unit}没有改。把它们单独贴进来再改一次，或者用 App / <code>hz</code> 处理整篇。"))
    if len(todo) > 1 and not stop:
        extra.append(("info",
                      f"Long draft: rewritten in {len(todo)} parts, cut at paragraph breaks.",
                      f"稿子较长：在段落之间切成 {len(todo)} 段分别改写。"))
    log_use(outcome="ok" if not (stop or left) else "partial", lang="zh" if cjk else "en", out_tokens=toks,
            eos=eos_all, copy=round(hz_text.copy_rate(drafted, out_all), 3), gen_seconds=round(gsec, 1),
            err=_err_kind(stop[1]) if stop else None, left_parts=len(left), **base)
    yield panel_done(drafted, out_all, {"tokens": toks, "eos": eos_all, "gen_seconds": gsec}, wall, cjk, extra)


def humanize(draft: str) -> dict:
    """Rewrite one AI-written draft (English or Chinese) so it reads like a person wrote it.

    Returns {"text", "copy_rate", "missing_numbers", "added_numbers", "tokens", "truncated", "seconds",
    "gen_seconds"}.
    Up to about 700 English words or 1,200 Chinese characters per call. Proofread the result.
    """
    draft = (draft or "").strip()
    n_tok, msg = _check_input(draft)
    if msg:
        raise gr.Error(re.sub(r"<[^>]+>", "", msg[0]))
    max_new, budget, _ = plan(n_tok)
    t0, text, info = time.time(), "", {}
    for kind, val in _generate(build_prompt(draft), max_new, budget):
        if kind == "text":
            text += val
        elif kind == "done":
            info = val
        elif kind == "error":
            raise gr.Error(val)
    out = text.strip()
    chk = hz_text.check(draft, out, truncated=not info.get("eos", True))
    log_use(outcome="api_ok" if out else "api_empty", in_units=hz_text.count_words(draft), in_tokens=n_tok,
            out_tokens=info.get("tokens"), eos=info.get("eos"), copy=chk.copy, seconds=round(time.time() - t0, 1))
    return {"text": out, "copy_rate": chk.copy, "missing_numbers": chk.missing_numbers,
            "added_numbers": chk.added_numbers, "tokens": info.get("tokens"), "truncated": chk.truncated,
            "seconds": round(time.time() - t0, 1), "gen_seconds": round(info.get("gen_seconds", 0.0), 1)}


# ───────────────────────────────────────────────────────────── page

with open(HERE / "examples.json", encoding="utf-8") as fh:
    EXAMPLES = json.load(fh)

_ROOTS = ("html", ":root", "body", ".dark", ".hz-", "gradio-app", ".gradio-container", "@")  # .hz-* flags sit on <html>


def _scope_selector(sel: str) -> str:
    """Raise a selector above Gradio's scoped preflight (.gradio-container-x.y.z h1 / button / svg):
    '.display' -> 'body .gradio-container .display'; 'html[data-lang=zh] .l-en' ->
    'html[data-lang=zh] .gradio-container .l-en'; rules on html/body/.dark themselves stay as they are."""
    depth, cut = 0, len(sel)
    for k, ch in enumerate(sel):
        if ch in "([":
            depth += 1
        elif ch in ")]":
            depth -= 1
        elif depth == 0 and ch in " >+~":
            cut = k
            break
    first, rest = sel[:cut], sel[cut:].strip()
    if first.startswith(_ROOTS):
        if not rest or first.startswith(".gradio-container"):
            return sel
        return f"{first} .gradio-container {rest}"
    return f"body .gradio-container {sel}"


def scope_css(css: str) -> str:
    css = re.sub(r"/\*.*?\*/", "", css, flags=re.S)
    out, i = [], 0
    while True:
        j = css.find("{", i)
        if j < 0:
            break
        head, depth, k = css[i:j].strip(), 1, j + 1
        while depth:
            depth += {"{": 1, "}": -1}.get(css[k], 0)
            k += 1
        body = css[j + 1:k - 1]
        if head.startswith(("@media", "@supports")):
            out.append(f"{head} {{\n{scope_css(body)}\n}}")
        elif head.startswith("@"):
            out.append(f"{head} {{{body}}}")
        else:
            out.append(", ".join(_scope_selector(x.strip()) for x in head.split(",")) + f" {{{body}}}")
        i = k
    return "\n".join(out)


CSS = scope_css((HERE / "style.css").read_text(encoding="utf-8"))
FONT_URL = "/gradio_api/file=fonts/"
CSS = CSS.replace("url(\"../fonts/", f"url(\"{FONT_URL}")
# The stylesheet goes into <head> as is: Gradio's css= parameter rescopes selectors, which breaks the
# media queries that restyle Gradio's own containers.
HEAD = ("<meta name=\"color-scheme\" content=\"light dark\">"
        + "<style>" + CSS + "</style>"
        + "".join(f'<link rel="preload" href="{FONT_URL}{f}" as="font" type="font/woff2" crossorigin>'
                  for f in ("InstrumentSans-Variable.woff2", "BricolageGrotesque-Variable.woff2"))
        + page.samples_script(EXAMPLES)
        + "<script>" + (HERE / "page.js").read_text(encoding="utf-8") + "</script>")

gr.set_static_paths(paths=[HERE / "fonts"])

with gr.Blocks(title="humanizer: rewrite AI drafts so they read like a person wrote them", fill_width=True) as demo:
    gr.HTML(page.topbar(), elem_id="hz-top", padding=False, apply_default_css=False)
    with gr.Row(elem_id="editor", equal_height=True):
        with gr.Column(elem_classes=["sheet", "draft-sheet"], scale=1, min_width=0):
            gr.HTML(page.draft_head(), padding=False, apply_default_css=False)
            draft_box = gr.Textbox(show_label=False, container=False, lines=15, max_lines=15, elem_id="draft",
                                   placeholder="Paste the AI-written draft here…", autoscroll=False)
            gr.HTML(page.draft_foot(EXAMPLES), padding=False, apply_default_css=False)
        with gr.Column(elem_id="rail", scale=0, min_width=92):
            go = gr.Button("Rewrite", elem_id="go")
            gr.HTML(page.rail(), padding=False, elem_id="rail-label", apply_default_css=False)
        with gr.Column(elem_classes=["sheet", "out-sheet"], scale=1, min_width=0):
            out_html = gr.HTML(panel_empty(), elem_id="out", padding=False, apply_default_css=False)
    gr.HTML(page.editor_note(), elem_id="hz-note", padding=False, apply_default_css=False)
    gr.HTML(page.examples_section(EXAMPLES) + page.local_section() + page.results_section()
            + page.limits_section() + page.footer(), elem_id="hz-body", padding=False, apply_default_css=False)

    go.click(rewrite, inputs=draft_box, outputs=out_html, show_progress="hidden", concurrency_limit=2,
             api_visibility="undocumented",
             js="(d) => { document.documentElement.classList.add('hz-busy'); return d; }") \
      .then(None, js="() => { document.documentElement.classList.remove('hz-busy'); }")
    gr.api(humanize, api_name="humanize")

_F = gr.themes.Font
THEME = gr.themes.Base(font=[_F("Instrument Sans"), _F("system-ui"), _F("sans-serif")],
                       font_mono=[_F("JetBrains Mono"), _F("ui-monospace"), _F("monospace")])

if __name__ == "__main__":
    demo.queue(max_size=24).launch(head=HEAD, theme=THEME, ssr_mode=False,
                                   allowed_paths=[str(HERE / "fonts")])
