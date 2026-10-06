#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""hz: rewrite an AI-written draft, or a whole long document, with the humanizer model.

    hz draft.txt                          rewrite, print the result
    hz < draft.txt
    hz paper.md -o paper.out.md           headings, code, tables and links stay; prose is rewritten in pieces
    hz report.docx -o report.out.docx     needs python-docx: pip install "humanize-model[docx]"
    hz paper.md --json                    per-piece stats: copy rate, missing numbers, retried, seconds

It talks to the Humanizer desktop app (http://127.0.0.1:47615) when it is running or installed, otherwise
to a llama-server running the model (--server, default http://127.0.0.1:8080). Standard library only.
Docs: https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.md#14-hz-command-line-tool
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import socket
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from typing import Callable, Dict, List, Optional, Tuple

try:
    from . import hz_text as T
    from . import __version__
except ImportError:  # python3 humanizer/hz.py
    sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
    import hz_text as T  # type: ignore
    __version__ = '0.1.0'

# ── the prompt: byte for byte what the model was trained on (humanizer/promptfmt.py) ──────────────
INSTR = ("Rewrite the text below so it reads like a person wrote it, not a language model.\n"
         "\n"
         "Reorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\n"
         "throat-clearing, and any sentence that only announces what comes next.\n"
         "Prefer the concrete word over the abstract one. It is fine to sound uneven.\n"
         "\n"
         "Every fact, number, unit, date, name and quotation must survive unchanged.")
SEP = "\n\n### Rewritten:\n\n"
FINGERPRINT = "cc51d66b4c593fbe"

# Sampling the model was evaluated with. Every key is set on purpose: llama-server's own defaults
# (top_k 40, min_p 0.05) differ. No "stop": generation ends at EOS.
SAMPLING = {"temperature": 1.0, "top_p": 0.95, "top_k": 0, "min_p": 0, "repeat_penalty": 1.0}
N_PREDICT = (2.5, 256, 2048)          # n_predict = clamp(draft tokens × 2.5, 256, 2048), same rule as the app

APP_URL = "http://127.0.0.1:47615"
SERVER_URL = "http://127.0.0.1:8080"
WAIT_SECONDS = 180
REQUEST_TIMEOUT = 900
USAGE_URL = "https://github.com/sgaofen/humanizer-local-model/blob/main/docs/USAGE.md"
RELEASES_URL = "https://github.com/sgaofen/humanizer-local-model/releases/latest"

NO_BACKEND_HELP = f"""no humanizer model is running, and the Humanizer app could not be found or started.

Pick one:

  1. The app (macOS with Apple silicon, Windows x64). Download it from
       {RELEASES_URL}
     open it once so it downloads the model, then run hz again. hz finds the app by itself.
     Already installed? Open it, wait until it shows the editor, and run hz again.

  2. llama.cpp. In another terminal (the first run downloads about 12.7 GB; on a 16 GB machine use
     humanizer-12b-Q6_K.gguf instead):
       llama-server --hf-repo jialinyyzz/humanizer --hf-file humanizer-12b-Q8_0.gguf -c 8192 -np 1 -ngl 99 --port 8080
     then run hz again. A server somewhere else: hz --server http://HOST:PORT ...

Details: {USAGE_URL}"""


def build_prompt(draft: str) -> str:
    """prompt = INSTR + "\\n\\n" + draft.strip() + "\\n\\n### Rewritten:\\n\\n". Never build it anywhere else."""
    return INSTR + "\n\n" + draft.strip() + SEP


def prompt_fingerprint() -> str:
    return hashlib.sha256(build_prompt("X").encode("utf-8")).hexdigest()[:16]


class HzError(Exception):
    def __init__(self, msg: str, code: int = 1):
        super().__init__(msg)
        self.code = code


class Out:
    """stderr messages. Progress lines are hidden by --quiet; warnings and errors never are."""

    def __init__(self, quiet: bool = False):
        self.quiet = quiet

    @staticmethod
    def _write(msg: str):
        try:
            sys.stderr.write(msg + "\n")
        except UnicodeEncodeError:
            sys.stderr.write(msg.encode("ascii", "backslashreplace").decode("ascii") + "\n")
        sys.stderr.flush()

    def info(self, msg: str):
        if not self.quiet:
            self._write("hz: " + msg)

    def warn(self, msg: str):
        self._write("hz: " + msg)


# ── HTTP ────────────────────────────────────────────────────────────────────────────────────────
_LOCAL = {"127.0.0.1", "localhost", "::1", "[::1]"}
_direct = urllib.request.build_opener(urllib.request.ProxyHandler({}))   # loopback never goes through a proxy


def _http(method: str, url: str, body=None, headers=None, timeout: float = 5.0):
    """→ (status, parsed JSON or text). Connection problems raise (URLError, OSError)."""
    data = json.dumps(body).encode("utf-8") if body is not None else None
    req = urllib.request.Request(url, data=data, headers=headers or {}, method=method)
    host = urllib.parse.urlsplit(url).hostname or ""
    opener = _direct if host in _LOCAL else urllib.request.build_opener()
    try:
        with opener.open(req, timeout=timeout) as r:
            status, raw = r.status, r.read()
    except urllib.error.HTTPError as e:
        status, raw = e.code, (e.read() or b"")
    try:
        return status, json.loads(raw.decode("utf-8"))
    except ValueError:
        return status, raw.decode("utf-8", "replace")


def _probe(url: str, timeout: float = 2.0):
    try:
        return _http("GET", url, timeout=timeout)
    except Exception:
        return None, None


def _err_message(d) -> str:
    if isinstance(d, dict):
        e = d.get("error")
        if isinstance(e, dict):
            return str(e.get("message") or e)
        if e:
            return str(e)
    return str(d)[:300]


class Backend:
    """kind 'app': the Humanizer app, POST {url}/api/completion with the X-Humanizer: 1 header.
    kind 'server': a llama-server, POST {url}/completion."""

    def __init__(self, kind: str, url: str, model: str = ""):
        self.kind, self.url, self.model = kind, url.rstrip("/"), model

    @property
    def label(self) -> str:
        name = "Humanizer app" if self.kind == "app" else "llama-server"
        return f"{name} at {self.url}" + (f" (model {self.model})" if self.model else "")

    def _post(self, path: str, body: dict, timeout: float):
        headers = {"Content-Type": "application/json"}
        if self.kind == "app":
            headers["X-Humanizer"] = "1"
            path = "/api" + path
        return _http("POST", self.url + path, body, headers, timeout)

    def count_tokens(self, text: str) -> Optional[int]:
        try:
            st, d = self._post("/tokenize", {"content": text, "add_special": False}, 30)
        except Exception:
            return None
        if st == 200 and isinstance(d, dict) and isinstance(d.get("tokens"), list):
            return len(d["tokens"])
        return None

    def complete(self, prompt: str, n_predict: int) -> Tuple[str, bool]:
        """→ (text, cut_off). Retries a dropped connection or a busy/reloading engine a few times."""
        body = dict(prompt=prompt, n_predict=n_predict, **SAMPLING)
        delays = [3, 10, 20]
        for attempt in range(len(delays) + 1):
            try:
                st, d = self._post("/completion", body, REQUEST_TIMEOUT)
            except (socket.timeout, TimeoutError):
                raise HzError(f"the {self.label} did not answer within {REQUEST_TIMEOUT} s")
            except (urllib.error.URLError, OSError) as e:
                if attempt < len(delays):
                    time.sleep(delays[attempt])
                    continue
                raise HzError(f"lost the connection to the {self.label}: {getattr(e, 'reason', e)}")
            if st == 200 and isinstance(d, dict) and "content" in d:
                cut = d.get("stop_type") == "limit" or bool(d.get("stopped_limit")) or bool(d.get("truncated"))
                return str(d.get("content") or ""), cut
            if st in (502, 503) and attempt < len(delays):
                time.sleep(delays[attempt])
                continue
            raise HzError(f"the {self.label} answered HTTP {st}: {_err_message(d)}")
        raise HzError(f"the {self.label} is not answering")

    def n_predict(self, draft: str) -> int:
        n = self.count_tokens(draft)
        if n is None:   # rough estimate if /tokenize is unavailable
            n = int(T.latin_words(draft) * 1.4 + T.cjk_chars(draft) * 1.1) + 8
        f, lo, hi = N_PREDICT
        return max(lo, min(hi, math.ceil(n * f)))


# ── finding a backend ───────────────────────────────────────────────────────────────────────────
def _norm_url(u: str) -> str:
    u = u.strip().rstrip("/")
    if "://" not in u:
        u = "http://" + u
    for suffix in ("/completion", "/api", "/v1"):
        if u.endswith(suffix):
            u = u[: -len(suffix)]
    return u


def app_data_dir() -> str:
    """Where the app keeps instance.json (same rule as the app; HUMANIZER_DATA_DIR overrides)."""
    if os.environ.get("HUMANIZER_DATA_DIR"):
        return os.environ["HUMANIZER_DATA_DIR"]
    home = os.path.expanduser("~")
    if sys.platform == "darwin":
        return os.path.join(home, "Library", "Application Support", "Humanizer")
    if os.name == "nt":
        return os.path.join(os.environ.get("LOCALAPPDATA") or os.path.join(home, "AppData", "Local"), "Humanizer")
    return os.path.join(os.environ.get("XDG_DATA_HOME") or os.path.join(home, ".local", "share"), "Humanizer")


def app_urls() -> List[str]:
    """The port the running app wrote to instance.json (it moves off 47615 if that port is taken), then 47615."""
    urls = []
    try:
        with open(os.path.join(app_data_dir(), "instance.json"), encoding="utf-8") as f:
            port = int(json.load(f).get("port") or 0)
        if port > 0:
            urls.append(f"http://127.0.0.1:{port}")
    except Exception:
        pass
    if APP_URL not in urls:
        urls.append(APP_URL)
    return urls


def app_status(url: str) -> Optional[dict]:
    st, d = _probe(url + "/app/status")
    if st == 200 and isinstance(d, dict) and d.get("app") == "humanizer":
        return d
    return None


def server_state(url: str) -> Optional[str]:
    """'ok' | 'loading' | 'other' (something answers, not llama-server) | None (nothing listening)."""
    st, _ = _probe(url + "/health")
    if st == 200:
        return "ok"
    if st == 503:
        return "loading"
    return None if st is None else "other"


def launch_app(out: Out) -> bool:
    """macOS: start the installed app in the background, without opening a browser tab."""
    if sys.platform != "darwin":
        return False
    try:
        r = subprocess.run(["open", "-a", "Humanizer", "--args", "--no-browser"],
                           stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
    except Exception:
        return False
    if r.returncode != 0:
        return False
    out.info("started the Humanizer app; waiting for it to load the model (up to 3 minutes)")
    return True


def _open_page(url: str):
    try:
        if sys.platform == "darwin":
            subprocess.run(["open", url], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=10)
        elif os.name == "nt":
            os.startfile(url)  # type: ignore[attr-defined]
    except Exception:
        pass


def _app_problem(url: str, d: dict) -> str:
    ph, page = d.get("phase"), url + "/"
    if ph == "setup":
        return f"the Humanizer app has no model yet. Open {page} , pick a model size, let it download, then run hz again."
    if ph == "paused":
        return f"the app's model download is paused. Resume it at {page} , then run hz again."
    if ph == "downloading":
        return f"the app is still downloading the model ({_dl_pct(d)}). Run hz again when it is done ({page})."
    if ph == "error":
        e = d.get("error") or {}
        return f"the Humanizer app reports an error: {e.get('message') or e}. Open {page} to fix it."
    if ph == "stopped":
        return "the Humanizer app is shutting down. Open it again, then rerun hz."
    return f"the Humanizer app is not ready (state: {ph}). Open {page} ."


def _dl_pct(d: dict) -> str:
    dl = d.get("download") or {}
    if dl.get("total"):
        return f"{100 * dl.get('received', 0) / dl['total']:.0f}%"
    return "in progress"


def _wait_app(urls: Callable[[], List[str]], out: Out, launched: bool = False,
              seconds: float = WAIT_SECONDS) -> Backend:
    t0, shown, last = time.time(), -1e9, None
    while True:
        el = time.time() - t0
        url, d = None, None
        for u in urls():
            d = app_status(u)
            if d:
                url = u
                break
        ph = d.get("phase") if d else None
        if d and ph == "ready":
            return Backend("app", url, d.get("tier") or "")
        if d and ph in ("setup", "paused", "error"):
            if launched and ph == "setup":
                _open_page(url + "/")
            raise HzError(_app_problem(url, d), 3)
        note = {"starting": "the app is loading the model",
                "downloading": f"the app is downloading the model ({_dl_pct(d or {})})",
                None: "waiting for the app to start"}.get(ph, f"the app is {ph}")
        if el - shown >= 10 or ph != last:
            out.info(f"{note} ({el:.0f} s)")
            shown, last = el, ph
        if el > seconds:
            raise HzError((_app_problem(url, d) if d else "the Humanizer app did not start within 3 minutes. "
                           "Open it by hand and run hz again.\n\n" + NO_BACKEND_HELP), 3)
        time.sleep(1)


def _wait_server(url: str, out: Out, seconds: float = WAIT_SECONDS) -> Backend:
    t0, shown = time.time(), -1e9
    while True:
        st = server_state(url)
        if st == "ok":
            return _server(url, out)
        el = time.time() - t0
        if st is None:
            raise HzError(f"the llama-server at {url} went away while loading. Check its log.", 3)
        if el > seconds:
            raise HzError(f"the llama-server at {url} is still loading the model after 3 minutes; run hz again later.", 3)
        if el - shown >= 10:
            out.info(f"llama-server at {url} is loading the model ({el:.0f} s)")
            shown = el
        time.sleep(1)


def _server(url: str, out: Out) -> Backend:
    st, d = _probe(url + "/props", 3)
    model = ""
    if st == 200 and isinstance(d, dict):
        path = str(d.get("model_path") or "")
        model = os.path.basename(path)
        if path and "humaniz" not in path.lower():
            out.warn(f"the server at {url} is running {model}, which does not look like a humanizer model.")
    return Backend("server", url, model)


def find_backend(server: Optional[str], app: Optional[str], out: Out, no_launch: bool = False) -> Backend:
    """--server URL: that llama-server only. --app URL: that app only. Otherwise: a running app →
    a llama-server on 8080 → start the installed app (macOS) and wait for it → give up with help."""
    if server:
        url = _norm_url(server)
        if app_status(url):                          # --server pointed at the app: fine, talk to it as the app
            return _wait_app(lambda: [url], out)
        st = server_state(url)
        if st == "ok":
            return _server(url, out)
        if st == "loading":
            return _wait_server(url, out)
        if st == "other":
            raise HzError(f"{url} answers, but it is not llama-server (no /health). Point --server at "
                          "llama-server's address, for example http://127.0.0.1:8080.", 3)
        raise HzError(f"nothing is listening at {url}.\n\n" + NO_BACKEND_HELP, 3)
    if app:
        url = _norm_url(app)
        if app_status(url):
            return _wait_app(lambda: [url], out)
        raise HzError(f"the Humanizer app is not running at {url}.", 3)
    pending = None
    for url in app_urls():
        d = app_status(url)
        if d is None:
            continue
        if d.get("phase") in ("ready", "starting", "downloading"):
            return _wait_app(lambda u=url: [u], out)
        pending = (url, d)
        break
    st = server_state(SERVER_URL)
    if st == "ok":
        return _server(SERVER_URL, out)
    if st == "loading":
        return _wait_server(SERVER_URL, out)
    if pending:
        raise HzError(_app_problem(*pending), 3)
    if not no_launch and launch_app(out):
        return _wait_app(app_urls, out, launched=True)
    raise HzError(NO_BACKEND_HELP, 3)


# ── rewriting ───────────────────────────────────────────────────────────────────────────────────
def _describe(issues: List[str], copy: float, missing_numbers: List[str], missing_urls: List[str],
              added: List[str] = ()) -> str:
    parts = []
    if "empty" in issues:
        parts.append("empty rewrite")
    if "truncated" in issues:
        parts.append("rewrite was cut off")
    if "too_short" in issues:
        parts.append("rewrite is much shorter than the draft")
    if "too_long" in issues:
        parts.append("rewrite is much longer than the draft (added text?)")
    if "repeated" in issues:
        parts.append("rewrite says the same thing twice")
    if "markup" in issues:
        parts.append("rewrite contains HTML tags the draft does not have")
    if missing_numbers:
        parts.append("numbers not found in the rewrite: " + ", ".join(missing_numbers))
    if missing_urls:
        parts.append("links not found in the rewrite: " + ", ".join(missing_urls))
    if "copy" in issues:
        parts.append(f"copies {copy:.0%} of the draft")
    if added:
        parts.append("numbers not in the draft: " + ", ".join(added) + " (make sure they are not made up)")
    return "; ".join(parts)


def rewrite_units(units: List[T.Unit], backend: Backend, sizer: T.Sizer, out: Out, where: Callable[[T.Unit], str],
                  max_copy: float = 0.5, retries: int = 1, loc_key: str = "line") -> Tuple[Dict[int, str], List[dict]]:
    """Rewrite each unit; if the rewrite has a problem, rewrite it again (up to `retries` more times)
    and keep the version with the fewest problems."""
    results: Dict[int, str] = {}
    stats: List[dict] = []
    total = len(units)
    for k, u in enumerate(units, 1):
        prompt = build_prompt(u.text)
        n_pred = backend.n_predict(u.text)
        tries = []
        for a in range(1 + max(0, retries)):
            t0 = time.time()
            text, cut = backend.complete(prompt, n_pred)
            text = text.strip()
            secs = time.time() - t0
            c = T.check(u.text, text, cut, max_copy, sizer)
            tries.append((text, c, secs))
            head = f"{where(u)}, {T.size_label(u.text)}" if a == 0 else f"try {a + 1}"
            out.info(f"[{k}/{total}] {head}: {secs:.1f} s, copy {c.copy:.2f}"
                     + (f"  ({_describe(c.issues, c.copy, c.missing_numbers, c.missing_urls, c.added_numbers)})"
                        if c.issues else ""))
            if not c.retry:
                break
        best = min(range(len(tries)), key=lambda i: tries[i][1].score())
        text, c, _ = tries[best]
        if len(tries) > 1:
            out.info(f"[{k}/{total}] kept try {best + 1}")
        results[u.id] = text
        stats.append({
            "id": u.id, "kind": u.kind, loc_key: u.line,
            "words_in": T.count_words(u.text), "words_out": T.count_words(text),
            "copy_rate": c.copy, "missing_numbers": c.missing_numbers, "missing_urls": c.missing_urls,
            "added_numbers": c.added_numbers,
            "truncated": c.truncated, "retried": len(tries) > 1, "chosen": best + 1,
            "seconds": round(sum(t[2] for t in tries), 1),
            "flagged": bool(c.issues), "issues": c.issues,
            "attempts": [{"copy_rate": t[1].copy, "missing_numbers": t[1].missing_numbers, "issues": t[1].issues,
                          "seconds": round(t[2], 1)} for t in tries],
            "draft_start": u.text[:60],
        })
    return results, stats


def _docx_module():
    try:
        from . import hz_docx
    except ImportError:
        import hz_docx  # type: ignore
    return hz_docx


# ── input / output ──────────────────────────────────────────────────────────────────────────────
_UNSUPPORTED = (".doc", ".pdf", ".rtf", ".odt", ".pages", ".pptx", ".xlsx")


def _read_text(path: Optional[str]) -> str:
    if not path or path == "-":
        data = sys.stdin.buffer.read() if hasattr(sys.stdin, "buffer") else sys.stdin.read().encode("utf-8")
    else:
        with open(path, "rb") as f:
            data = f.read()
    if data[:4] == b"PK\x03\x04":
        raise HzError("this looks like a .docx (or other zip) file. Pass it as a file name ending in .docx.", 2)
    for enc in ("utf-8-sig", "utf-16") if data[:2] in (b"\xff\xfe", b"\xfe\xff") else ("utf-8-sig",):
        try:
            return data.decode(enc)
        except UnicodeDecodeError:
            pass
    raise HzError("the input is not UTF-8 text. Save it as UTF-8 and try again.", 2)


def _write_stdout(text: str):
    if hasattr(sys.stdout, "buffer"):
        sys.stdout.flush()
        sys.stdout.buffer.write(text.encode("utf-8"))
        sys.stdout.buffer.flush()
    else:
        sys.stdout.write(text)


def _plan_report(units: List[T.Unit], kept, where) -> str:
    rows = [f"{len(units)} piece(s) to rewrite:"]
    for u in units:
        prev = " ".join(u.text.split())
        rows.append(f"  #{u.id:<3} {where(u):<15} {u.kind:<10} {T.size_label(u.text):>11}  {prev[:60]!r}")
    if kept:
        rows.append("kept as is: " + ", ".join(f"{v} {k}" for k, v in sorted(kept.items())))
    return "\n".join(rows) + "\n"


def _parser() -> argparse.ArgumentParser:
    ap = argparse.ArgumentParser(
        prog="hz",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        description="Rewrite an AI-written draft or a long document with the humanizer model, keeping "
                    "headings, code, tables and links. Uses the Humanizer app if it is running or installed, "
                    "else a llama-server.",
        epilog=f"""examples:
  hz draft.txt                        print the rewrite
  hz < draft.txt > out.txt
  hz paper.md -o paper.out.md
  hz report.docx -o report.out.docx   (needs python-docx: pip install "humanize-model[docx]")
  hz notes.md --json                  stats per piece, for scripts and agents
  hz paper.md --dry-run               show how the document will be split; no model needed

Every rewrite is checked: numbers or links from the draft that are missing in the rewrite, copying
more than --max-copy of the draft, and a rewrite that is empty, cut off, far shorter or longer than
the draft, repeats itself, or contains stray HTML tags. A piece with a problem is rewritten once more and the version with fewer problems is kept.
Numbers that appear in the rewrite but not in the draft are reported too. Read the result anyway:
check numbers, dates and names. No AI detector result is promised.
docs: {USAGE_URL}""")
    ap.add_argument("input", nargs="?", default="-", help=".txt, .md or .docx file; leave out (or -) to read stdin")
    ap.add_argument("-o", "--output", help="write the result here instead of stdout (.docx: default NAME.hz.docx)")
    ap.add_argument("--server", metavar="URL", help="use this llama-server, e.g. http://127.0.0.1:8080, and skip the app")
    ap.add_argument("--app", metavar="URL", help=f"the Humanizer app's address if it is not the usual one ({APP_URL})")
    ap.add_argument("--json", action="store_true", help="print JSON: per-piece copy rate, missing numbers, retried, seconds")
    ap.add_argument("-q", "--quiet", action="store_true", help="no progress lines (pieces to check are still listed)")
    ap.add_argument("--dry-run", action="store_true", help="show the pieces and kept blocks without calling a model")
    ap.add_argument("--max-words", type=int, default=T.MAX_WORDS, help="English words per piece (default %(default)s)")
    ap.add_argument("--max-chars", type=int, default=T.MAX_CJK, help="Chinese characters per piece (default %(default)s)")
    ap.add_argument("--max-copy", type=float, default=0.5, help="rewrite again above this copy rate (default %(default)s)")
    ap.add_argument("--retries", type=int, default=1, help="extra tries for a piece with a problem (default %(default)s)")
    ap.add_argument("--no-launch", action="store_true", help="don't start the app if it is installed but closed")
    ap.add_argument("--version", action="version", version=f"hz {__version__}")
    return ap


def run(a: argparse.Namespace, out: Out) -> int:
    if prompt_fingerprint() != FINGERPRINT:
        raise HzError("internal error: the prompt does not match the model's training format")
    path = None if a.input in (None, "-") else a.input
    ext = os.path.splitext(path or "")[1].lower()
    if ext in _UNSUPPORTED:
        raise HzError(f"{ext} files are not supported. Save the file as .docx, .md or .txt first.", 2)
    if path and not os.path.isfile(path):
        raise HzError(f"no such file: {path}", 2)
    is_docx = ext == ".docx"
    out_path = a.output
    if is_docx and not out_path and not a.dry_run:
        out_path = os.path.splitext(path)[0] + ".hz.docx"
    if out_path and path and os.path.abspath(out_path) == os.path.abspath(path):
        raise HzError("the output would overwrite the input. Pick another -o.", 2)
    if is_docx and out_path and not out_path.lower().endswith(".docx"):
        raise HzError("a .docx input needs a .docx output (-o NAME.docx).", 2)

    planner = T.Planner(a.max_words, a.max_chars)
    if is_docx:
        D = _docx_module()
        try:
            doc = D.load(path)
        except D.DocxMissing as e:
            raise HzError(str(e), 2)
        units, targets, kept = D.plan(doc, planner)
        where = lambda u: f"paragraph {u.line}"
        plan = None
    else:
        text = _read_text(path)
        if not text.strip():
            raise HzError("the input is empty.", 2)
        plan = planner.plan_text(text)
        units, kept = plan.units, plan.kept
        where = lambda u: f"line {u.line}"

    if a.dry_run:
        if a.json:
            _write_stdout(json.dumps({"hz": __version__, "input": path or "-", "format": "docx" if is_docx else "text",
                                      "pieces": [{"id": u.id, "kind": u.kind, ("paragraph" if is_docx else "line"): u.line,
                                                  "words": T.count_words(u.text), "text": u.text} for u in units],
                                      "kept": dict(kept)}, ensure_ascii=False, indent=1) + "\n")
        else:
            _write_stdout(_plan_report(units, kept, where))
        return 0

    t0 = time.time()
    backend = None
    if units:
        backend = find_backend(a.server, a.app, out, a.no_launch)
        out.info(f"using the {backend.label}")
        out.info(f"{len(units)} piece(s) to rewrite" + (", kept as is: " + ", ".join(
            f"{v} {k}" for k, v in sorted(kept.items())) if kept else ""))
        results, stats = rewrite_units(units, backend, planner.sizer, out, where, a.max_copy, a.retries,
                                       "paragraph" if is_docx else "line")
    else:
        out.warn("nothing to rewrite (only headings, code, tables or very short text); the output is the input.")
        results, stats = {}, []

    if is_docx:
        D.apply(targets, results)
        doc.save(out_path)
        new_text = None
    else:
        new_text = plan.render(results)
        if out_path:
            with open(out_path, "w", encoding="utf-8", newline="") as f:
                f.write(new_text)
    secs = time.time() - t0

    flagged = [s for s in stats if s["flagged"]]
    drafts = "\n\n".join(u.text for u in units)
    rewrites = "\n\n".join(results[u.id] for u in units)
    if a.json:
        doc_out = {
            "hz": __version__, "input": path or "-", "output": out_path, "format": "docx" if is_docx else "text",
            "backend": {"kind": backend.kind, "url": backend.url, "model": backend.model} if backend else None,
            "summary": {"pieces": len(stats), "retried": sum(s["retried"] for s in stats), "flagged": len(flagged),
                        "seconds": round(secs, 1), "words_in": T.count_words(drafts), "words_out": T.count_words(rewrites),
                        "copy_rate": round(T.copy_rate(drafts, rewrites), 3) if units else 0.0},
            "kept": dict(kept),
            "pieces": stats,
        }
        if new_text is not None and not out_path:
            doc_out["text"] = new_text
        _write_stdout(json.dumps(doc_out, ensure_ascii=False, indent=1) + "\n")
    elif new_text is not None and not out_path:
        _write_stdout(new_text if new_text.endswith("\n") else new_text + "\n")

    if units:
        out.info(f"done: {len(stats)} piece(s) in {secs:.0f} s, {sum(s['retried'] for s in stats)} rewritten twice"
                 + (f"; wrote {out_path}" if out_path else ""))
    if flagged:
        out.warn("check these by hand; the model may have changed or dropped something:")
        for s in flagged:
            loc = f"paragraph {s['paragraph']}" if "paragraph" in s else f"line {s['line']}"
            out.warn(f"  {loc} (piece {s['id']}): "
                     + _describe(s["issues"], s["copy_rate"], s["missing_numbers"], s["missing_urls"], s["added_numbers"]))
    if units:
        out.info("read the result once: check every number, date and name.")
    return 0


def main(argv: Optional[List[str]] = None) -> int:
    for s in (sys.stdout, sys.stderr):
        try:
            s.reconfigure(errors="replace")  # type: ignore[attr-defined]
        except Exception:
            pass
    a = _parser().parse_args(argv)
    out = Out(a.quiet)
    try:
        return run(a, out)
    except HzError as e:
        out.warn(str(e))
        return e.code
    except KeyboardInterrupt:
        out.warn("interrupted; nothing was written.")
        return 130


if __name__ == "__main__":
    sys.exit(main())
