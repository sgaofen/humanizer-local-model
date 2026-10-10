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

Same behaviour as the desktop app (0.3.1, 0.3.2):

* Language guard: an English draft whose rewrite turns Chinese is stopped on the spot and sampled
  again with the same settings, up to 3 times (hz_text.language_drift; a Chinese draft written in
  English is checked once the part is done). The last sample is shown as is, with a note.
* Create fact: passages the visitor selected come back word for word (facts.py). They are swapped for
  placeholders before the model sees the draft and put back afterwards; a part whose placeholders did
  not survive is rewritten, up to 3 times, and never shown with a kept passage changed.
* .docx / PDF import (document.py): the uploaded file is read into the draft box and deleted at once.

HUMANIZER_MOCK=1 runs the page without torch or the model, for local UI work.
HUMANIZER_MOCK_FAULTS=lang,fact makes the mock's first sample of each part go wrong (Chinese output for an
English draft; placeholders dropped), to exercise the guards.
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

import document
import facts as factlib
import hz_text
import page

HERE = Path(__file__).resolve().parent
MODEL_ID = "jialinyyzz/humanizer"
USAGE_DATASET = "jialinyyzz/humanizer-usage"
FINGERPRINT = "cc51d66b4c593fbe"
MOCK = os.environ.get("HUMANIZER_MOCK") == "1"
MOCK_FAULTS = set(filter(None, os.environ.get("HUMANIZER_MOCK_FAULTS", "").split(","))) if MOCK else set()

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
    from transformers import (AutoModelForCausalLM, AutoTokenizer, StoppingCriteria, StoppingCriteriaList,
                              TextIteratorStreamer)

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
    class _StopWhenSet(StoppingCriteria):
        """Ends generation as soon as the flag is set (the language guard saw the rewrite turn Chinese)."""

        def __init__(self, flag: threading.Event):
            self.flag = flag

        def __call__(self, input_ids, scores, **kwargs):
            return torch.full((input_ids.shape[0],), self.flag.is_set(), dtype=torch.bool, device=input_ids.device)

    def _slot(prompt: str, max_new: int, budget: float, en_guard: bool = False) -> int:
        return int(budget + SLOT_OVERHEAD)

    @spaces.GPU(duration=_slot)
    def _generate(prompt: str, max_new: int, budget: float, en_guard: bool = False):
        """Streams ("text", piece) ... then ("done", info). en_guard: the draft is English, so stop and
        yield ("drift", None) once the rewrite has more than a few Chinese characters."""
        enc = tokenizer(prompt, return_tensors="pt").to(model.device)
        streamer = TextIteratorStreamer(tokenizer, skip_prompt=True, skip_special_tokens=True,
                                        timeout=budget + 30)
        box, flag = {}, threading.Event()

        def work():
            try:
                with torch.inference_mode():
                    box["ids"] = model.generate(
                        **enc, streamer=streamer, max_new_tokens=max_new, max_time=budget,
                        do_sample=True, temperature=1.0, top_p=0.95, top_k=0, repetition_penalty=1.0,
                        pad_token_id=tokenizer.pad_token_id, eos_token_id=EOS,
                        stopping_criteria=StoppingCriteriaList([_StopWhenSet(flag)]))
            except Exception as exc:  # surface it instead of hanging the streamer
                box["err"] = f"{type(exc).__name__}: {exc}"
                streamer.end()

        t0, text = time.time(), ""
        th = threading.Thread(target=work, daemon=True)
        th.start()
        for piece in streamer:
            if not piece or flag.is_set():
                continue                      # after a drift: let generation wind down, show nothing more
            text += piece
            if en_guard and hz_text.language_drift("", text):
                flag.set()
                yield ("drift", None)
                continue
            yield ("text", piece)
        th.join()
        if "err" in box:
            yield ("error", box["err"])
            return
        new = box["ids"][0, enc["input_ids"].shape[1]:]
        n = int(new.shape[0])
        yield ("done", {"tokens": n, "eos": n > 0 and int(new[-1]) == EOS and not flag.is_set(),
                        "gen_seconds": time.time() - t0})
else:
    _mock_seen: dict = {}

    def _generate(prompt: str, max_new: int, budget: float, en_guard: bool = False):
        draft = prompt[len(FMT["instr"]) + 2:-len(FMT["sep"])]
        out = next((e["output"] for e in EXAMPLES if e["draft"] == draft), None)
        if out is None:  # crude stand-in: shuffle sentence order a little
            parts = re.split(r"(?<=[.!?。！？])\s*", draft)
            out = " ".join(parts[1:2] + parts[:1] + parts[2:]).strip()
        if "lang" in MOCK_FAULTS and hz_text.han_chars(draft) <= 2 and (draft, "lang") not in _mock_seen:
            _mock_seen[(draft, "lang")] = 1     # the first sample of an English draft comes out in Chinese
            out = "这是一段本该是英文的改写，结果模型把整段都写成了中文，语言保险应该当场把它掐掉再重新采样。" * 3
        elif "fact" in MOCK_FAULTS and "HZ_LOCK_" in draft and (draft, "fact") not in _mock_seen:
            _mock_seen[(draft, "fact")] = 1     # the next one drops the kept passages
            out = re.sub(r"\[\[_*HZ_LOCK_\d+\]\]", "something else", out)
        time.sleep(1.2)
        t0, n, text = time.time(), 0, ""
        for k in range(0, len(out), 6):
            time.sleep(0.02)
            n += 1
            text += out[k:k + 6]
            if en_guard and hz_text.language_drift("", text):
                yield ("drift", None)
                yield ("done", {"tokens": n, "eos": False, "gen_seconds": time.time() - t0})
                return
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


def _note(note) -> str:
    """A one-line notice under the text while a run is still going: note = (kind, en, zh) or None."""
    if not note:
        return ""
    kind, en, zh = note
    return f'<div class="notice {kind} pending">{_icon("redo")}<div>{P(en, zh)}</div></div>'


def panel_wait(note=None) -> str:
    body = f"""<div class="empty">
  <span class="spinner" aria-hidden="true"></span>
  <p class="empty-title">{L("Waiting for a GPU…", "正在等 GPU……")}</p>
  <p class="empty-sub">{L("ZeroGPU hands out a GPU per request. After a quiet spell the first run also moves the model onto it.",
                          "ZeroGPU 每次请求分配一块 GPU。空闲一阵后的第一次，还要先把模型搬上去。")}</p>
</div>"""
    return _sheet("wait", body, notices=_note(note))


def panel_stream(text: str, cjk: bool, part: int = 1, parts: int = 1, note=None) -> str:
    n = hz_text.count_words(text)
    prog = L(f" · part {part}/{parts}", f" · 第 {part}/{parts} 段") if parts > 1 else ""
    return _sheet("stream", html.escape(text) + '<span class="caret"></span>', cjk=cjk, notices=_note(note),
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


def _fact_retry_note(n: int):
    return ("info", f"A fact got changed. Rewriting again (try {n} of {factlib.FACT_TRIES})…",
            f"保留的文字被改动了，重新改写（第 {n}/{factlib.FACT_TRIES} 次）……")


def rewrite(draft: str, facts_raw: str = "[]"):
    """Never hand back nothing when something was written: parts that finished (and the half of a part the GPU
    stopped in, unless it holds a kept passage) stay on screen with a notice saying what is missing.
    Per part: an empty sample gets one automatic retry; a sample in the wrong language is resampled up to
    hz_text.LANG_RETRIES times; a sample that lost a kept passage is rewritten, up to factlib.FACT_TRIES tries."""
    draft = (draft or "").strip()
    if not draft:
        yield panel_message("warn", "Paste a draft first.", "先贴一篇草稿。")
        return
    cjk = hz_text.is_cjk(draft)
    prot = factlib.protect(draft, factlib.parse(facts_raw))   # kept passages -> placeholders, on the whole draft
    parts = _parts(prot.text)
    todo, left = parts[:PARTS], parts[PARTS:]
    yield panel_wait()
    t0, done_out, done_draft = time.time(), "", ""
    toks, gsec, eos_all, stop, retries = 0, 0.0, True, None, 0
    lang_resampled, lang_kept_wrong, fact_tries_extra = 0, 0, 0
    for i, (part, sep) in enumerate(todo):
        orig = factlib.restore_loose(part, prot)         # this part as the visitor wrote it
        locks = prot.indices_in(part)
        n_tok = count_tokens(part)
        max_new, budget, _ = plan(n_tok)
        english = hz_text.han_chars(orig) <= 2
        empty_left, lang_left, fact_left = 1, hz_text.LANG_RETRIES, factlib.FACT_TRIES - 1
        note, out, info, err = None, "", None, None
        while True:
            text, info, err, drifted, last = "", None, None, False, 0.0
            try:
                for kind, val in _generate(build_prompt(part), max_new, budget, english and lang_left > 0):
                    if kind == "text":
                        text += val
                        if time.time() - last > 0.1:
                            last = time.time()
                            yield panel_stream(done_out + factlib.stream_view(text, prot), cjk, i + 1, len(todo), note)
                    elif kind == "drift":
                        drifted = True
                    elif kind == "done":
                        info = val
                    elif kind == "error":
                        raise RuntimeError(val)
            except Exception as exc:
                err = getattr(exc, "message", None) or str(exc) or type(exc).__name__
            if err:
                # keep the half the GPU stopped in, unless it may be missing a kept passage or is in the wrong language
                out = "" if (locks or drifted) else factlib.restore_loose(text, prot).strip()
                break
            if not text.strip():
                if empty_left:
                    empty_left -= 1
                    retries += 1
                    continue
                break
            shown = factlib.restore_loose(text, prot).strip()
            if drifted or hz_text.language_drift(orig, shown):
                if lang_left:                     # silent resample, same settings (as the app)
                    lang_left -= 1
                    lang_resampled += 1
                    yield (panel_stream(done_out, cjk, i + 1, len(todo), note) if done_out else panel_wait(note))
                    continue
                lang_kept_wrong += 1              # the last sample is shown as it is, with a note
            if locks:
                try:
                    shown = factlib.restore(text, prot, only=locks).strip()
                except factlib.FactsLost:
                    if fact_left:
                        fact_left -= 1
                        fact_tries_extra += 1
                        note = _fact_retry_note(factlib.FACT_TRIES - fact_left)
                        yield (panel_stream(done_out, cjk, i + 1, len(todo), note) if done_out else panel_wait(note))
                        continue
                    err = "facts"                 # never show a version with a kept passage changed
                    break
            out = shown
            break
        if out:
            done_out += out + (sep if not err else "")
            done_draft += orig + (sep if not err else "")
            toks += (info or {}).get("tokens", 0) or 0
            gsec += (info or {}).get("gen_seconds", 0.0) or 0.0
            eos_all = eos_all and bool((info or {}).get("eos", False)) and not err
        if err or not out:
            stop = (i, err or "empty sample twice")
            break
    wall = time.time() - t0
    out_all, drafted = done_out.strip(), done_draft.strip()
    base = dict(in_units=hz_text.count_words(draft), in_tokens=count_tokens(draft), parts=len(parts),
                done_parts=(stop[0] if stop else len(todo)), retries=retries, seconds=round(wall, 1),
                facts=len(prot.locks), fact_retries=fact_tries_extra, lang_resampled=lang_resampled)
    facts_failed = bool(stop and stop[1] == "facts")
    if not out_all:
        if facts_failed:
            log_use(outcome="facts_failed", **base)
            yield panel_message("warn",
                                f"After {factlib.FACT_TRIES} tries the rewrite still didn't keep every fact word for word, so it wasn't used. Press the arrow to try again, or keep fewer or shorter facts.",
                                f"连试 {factlib.FACT_TRIES} 次，改写都没能把「原样保留」的文字一字不差地带过来，这次结果没有采用。再点一次箭头重试，或者少保留几段、保留得短一点。")
        elif stop and stop[1] != "empty sample twice":
            log_use(outcome="error", err=_err_kind(stop[1]), **base)
            yield panel_message(*_gpu_error(stop[1]))
        else:
            log_use(outcome="empty", **base)
            yield panel_message("warn", "Two samples in a row came back empty. Press the arrow again.",
                                "连续两发都是空的，请再点一次箭头。")
        return
    extra = []
    unit = "字" if cjk else "词"
    if facts_failed:
        i = stop[0]
        extra.append(("warn",
                      f"Part {i + 1} of {len(todo)}: after {factlib.FACT_TRIES} tries the model still changed a fact, so that part and the rest of the draft were not rewritten. Press the arrow to try again, or keep fewer or shorter facts.",
                      f"第 {i + 1}/{len(todo)} 段连试 {factlib.FACT_TRIES} 次，模型都改动了你要原样保留的文字，所以这一段和后面的部分没有改写。再点一次箭头重试，或者少保留几段、保留得短一点。"))
    elif stop:
        i, msg = stop
        extra.append(("warn",
                      f"The GPU stopped during part {i + 1} of {len(todo)} ({html.escape(msg)}). Everything that was written is above; the rest of the draft was not rewritten. Press the arrow again later, log in to Hugging Face for more GPU time, or use the app or <code>hz</code>.",
                      f"改到第 {i + 1}/{len(todo)} 段时 GPU 停了（{html.escape(msg)}）。已经写出来的都在上面，后面的部分没有改。可以过一会儿再点一次、登录 Hugging Face 多拿些 GPU 时长，或者用 App / <code>hz</code>。"))
    if left:
        rest = sum(hz_text.count_words(factlib.restore_loose(p, prot)) for p, _ in left)
        extra.append(("warn",
                      f"This page rewrites up to {len(todo)} parts per run, so the last {rest:,} {'chars' if cjk else 'words'} of your draft were left out. Paste them as a new run, or use the app or <code>hz</code> for the whole document.",
                      f"本页每次最多改 {len(todo)} 段，草稿最后约 {rest:,} {unit}没有改。把它们单独贴进来再改一次，或者用 App / <code>hz</code> 处理整篇。"))
    if len(todo) > 1 and not stop:
        extra.append(("info",
                      f"Long draft: rewritten in {len(todo)} parts, cut at paragraph breaks.",
                      f"稿子较长：在段落之间切成 {len(todo)} 段分别改写。"))
    if lang_kept_wrong:
        extra.append(("warn",
                      "This rewrite keeps coming out in the other language. Press the arrow again for a new sample.",
                      "这次改写连着几发都换了语言，再点一次箭头换一发。"))
    if fact_tries_extra and not facts_failed:
        extra.append(("info",
                      f"A fact got changed on the first try, so the rewrite was redone automatically ({fact_tries_extra + 1} tries).",
                      f"保留的文字头一次被改动了，已自动重写（共 {fact_tries_extra + 1} 次）。"))
    log_use(outcome="ok" if not (stop or left) else "partial", lang="zh" if cjk else "en", out_tokens=toks,
            eos=eos_all, copy=round(hz_text.copy_rate(drafted, out_all), 3), gen_seconds=round(gsec, 1),
            err=(None if not stop else "facts" if facts_failed else _err_kind(stop[1])), left_parts=len(left), **base)
    yield panel_done(drafted, out_all, {"tokens": toks, "eos": eos_all, "gen_seconds": gsec}, wall, cjk, extra)


def humanize(draft: str) -> dict:
    """Rewrite one AI-written draft (English or Chinese) so it reads like a person wrote it.

    Returns {"text", "copy_rate", "missing_numbers", "added_numbers", "tokens", "truncated", "seconds",
    "gen_seconds", "language_resampled"}.
    Up to about 700 English words or 1,200 Chinese characters per call. A rewrite in the wrong language
    (an English draft written in Chinese) is sampled again, up to 3 times. Proofread the result.
    """
    draft = (draft or "").strip()
    n_tok, msg = _check_input(draft)
    if msg:
        raise gr.Error(re.sub(r"<[^>]+>", "", msg[0]))
    max_new, budget, _ = plan(n_tok)
    english = hz_text.han_chars(draft) <= 2
    t0, resampled = time.time(), 0
    while True:
        text, info, drifted = "", {}, False
        last = resampled >= hz_text.LANG_RETRIES
        for kind, val in _generate(build_prompt(draft), max_new, budget, english and not last):
            if kind == "text":
                text += val
            elif kind == "drift":
                drifted = True
            elif kind == "done":
                info = val
            elif kind == "error":
                raise gr.Error(val)
        if not last and (drifted or hz_text.language_drift(draft, text.strip())):
            resampled += 1
            continue
        break
    out = text.strip()
    chk = hz_text.check(draft, out, truncated=not info.get("eos", True))
    log_use(outcome="api_ok" if out else "api_empty", in_units=hz_text.count_words(draft), in_tokens=n_tok,
            out_tokens=info.get("tokens"), eos=info.get("eos"), copy=chk.copy, seconds=round(time.time() - t0, 1),
            lang_resampled=resampled)
    return {"text": out, "copy_rate": chk.copy, "missing_numbers": chk.missing_numbers,
            "added_numbers": chk.added_numbers, "tokens": info.get("tokens"), "truncated": chk.truncated,
            "seconds": round(time.time() - t0, 1), "gen_seconds": round(info.get("gen_seconds", 0.0), 1),
            "language_resampled": resampled}


# ───────────────────────────────────────────────────────────── import .docx / .pdf

MB = document.MAX_BYTES // (1024 * 1024)
IMPORT_ERRORS = {
    "invalid": ("Only .docx and .pdf files are supported. Save old .doc files as .docx first.",
                "只支持 .docx 和 .pdf。旧版 .doc 请先在 Word 里另存为 .docx。"),
    "large": (f"This file is over {MB} MB, the limit on this page. The app takes files up to 20 MB.",
              f"文件超过 {MB} MB，本页读不了。App 能读 20 MB 以内的文件。"),
    "long": ("This document has far more text than this page can take. Use the app or <code>hz</code> for long documents.",
             "这份文档的文字远超本页能处理的长度。长文档请用 App 或 <code>hz</code>。"),
    "pages": (f"This PDF has more than {document.MAX_PAGES} pages; this page reads up to {document.MAX_PAGES}. Use the app for longer PDFs.",
              f"这个 PDF 超过 {document.MAX_PAGES} 页，本页最多读 {document.MAX_PAGES} 页。更长的 PDF 请用 App。"),
    "empty": ("No text found in this file. Scanned PDFs need OCR first.",
              "文件里没有可提取的文字。扫描版 PDF 要先做文字识别（OCR）。"),
    "unreadable": ("Couldn’t read this file. It may be damaged, encrypted, or too complex.",
                   "读不了这个文件：可能已损坏、加密，或者结构太复杂。"),
    "garbled": ("Couldn’t decode the text in this PDF (unusual font encoding). Copy the text from a PDF viewer and paste it here.",
                "这个 PDF 的文字编码认不出来。请在 PDF 阅读器里复制文字，再粘贴到这里。"),
}


def _toast(kind: str, en: str, zh: str) -> str:
    # data-t makes every toast a new element, so its CSS animation plays again
    return f'<div class="toast" data-kind="{kind}" data-t="{time.time():.3f}" role="status">{L(en, zh)}</div>'


def import_document(path):
    """An uploaded .docx / .pdf -> its text in the draft box. The file is deleted as soon as it is read;
    only counts are logged (file type, size bucket, characters), never the name or the text."""
    path = path if isinstance(path, str) else getattr(path, "name", None)
    if not path:
        return gr.skip(), "", None
    name = os.path.basename(path)
    ext = name.lower().rsplit(".", 1)[-1] if "." in name else ""
    text, code = "", None
    try:
        with open(path, "rb") as fh:
            data = fh.read(document.MAX_BYTES + 1)
        text = document.document_text(data, name)
    except document.DocError as exc:
        code = exc.code
    except Exception:
        code = "unreadable"
    finally:
        for target in (path, os.path.dirname(path)):   # Gradio keeps each upload in its own folder
            try:
                os.remove(target) if target == path else os.rmdir(target)
            except OSError:
                pass
    shown = html.escape(name)
    if code:
        log_use(outcome="import_error", kind=ext if ext in ("docx", "pdf") else "other", err=code)
        en, zh = IMPORT_ERRORS.get(code, IMPORT_ERRORS["unreadable"])
        return gr.skip(), _toast("err", en, zh), None
    n, zh_doc = hz_text.count_words(text), hz_text.is_cjk(text)
    log_use(outcome="import_ok", kind=ext, in_units=n)
    return text, _toast("ok", f"Imported “{shown}” ({n:,} {'chars' if zh_doc else 'words'}). Give the text a quick look before rewriting.",
                        f"已导入「{shown}」，共 {n:,} 字。改写前先看一眼文字有没有读错。"), None


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
            # Hidden helpers driven by page.js: the kept passages (a JSON list) and the file upload.
            facts_box = gr.Textbox(value="[]", visible="hidden", elem_id="hz-facts", show_label=False, container=False)
            doc_upload = gr.UploadButton("Import", file_types=[".docx", ".pdf"], file_count="single", type="filepath",
                                         visible="hidden", elem_id="hz-upload")
        with gr.Column(elem_id="rail", scale=0, min_width=92):
            go = gr.Button("Rewrite", elem_id="go")
            gr.HTML(page.rail(), padding=False, elem_id="rail-label", apply_default_css=False)
        with gr.Column(elem_classes=["sheet", "out-sheet"], scale=1, min_width=0):
            out_html = gr.HTML(panel_empty(), elem_id="out", padding=False, apply_default_css=False)
    gr.HTML(page.editor_note(), elem_id="hz-note", padding=False, apply_default_css=False)
    toast = gr.HTML("", elem_id="hz-toast", padding=False, apply_default_css=False)
    gr.HTML(page.examples_section(EXAMPLES) + page.local_section() + page.results_section()
            + page.limits_section() + page.footer(), elem_id="hz-body", padding=False, apply_default_css=False)

    go.click(rewrite, inputs=[draft_box, facts_box], outputs=out_html, show_progress="hidden", concurrency_limit=2,
             api_visibility="undocumented",
             js="(d, f) => { document.documentElement.classList.add('hz-busy'); return [d, window.HZ ? window.HZ.factsJSON() : '[]']; }") \
      .then(None, js="() => { document.documentElement.classList.remove('hz-busy'); }")
    doc_upload.upload(import_document, inputs=doc_upload, outputs=[draft_box, toast, doc_upload], show_progress="hidden",
                      concurrency_limit=4, api_visibility="private") \
      .then(None, js="() => { if (window.HZ) window.HZ.afterImport(); }")
    gr.api(humanize, api_name="humanize")

_F = gr.themes.Font
THEME = gr.themes.Base(font=[_F("Instrument Sans"), _F("system-ui"), _F("sans-serif")],
                       font_mono=[_F("JetBrains Mono"), _F("ui-monospace"), _F("monospace")])

if __name__ == "__main__":
    demo.queue(max_size=24).launch(head=HEAD, theme=THEME, ssr_mode=False, max_file_size=document.MAX_BYTES,
                                   allowed_paths=[str(HERE / "fonts")])
