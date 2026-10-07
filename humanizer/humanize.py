#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""humanize.py — rewrite an AI-written draft so it reads like a person wrote it, keeping every fact.

Talks to a local completion server (see mlx_nocopy_server.py) and applies the same decoding-time
guards the model was evaluated with:

  * prompt format is read from <model-dir>/prompt_format.json (must match training, byte for byte)
  * adaptive anti-copy: if the first sample copies > THR of the draft's 5-grams, resample once with
    the NoCopy logits penalty (digits are exempt so numbers/dates survive)

Usage:
    python humanize.py --model-dir ./humanizer-mlx-8bit --port 8104 draft.txt
    cat draft.txt | python humanize.py --model-dir ... --port 8104
    python humanize.py --model-dir ... --keep-markdown draft.md   # headings / code fences survive (issue #1)
"""
import argparse, json, os, sys, urllib.request
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from copy_rate import copy_rate
from hz_text import language_drift, LANG_RETRIES
from markdown_guard import rewrite_keeping_markdown


def load_format(model_dir):
    pf = json.load(open(os.path.join(model_dir, 'prompt_format.json')))
    return lambda draft: pf['instr'] + '\n\n' + draft.strip() + pf['sep']


def complete(port, prompt, draft, temperature=1.0, penalty=None, copy_n=5, timeout=900):
    body = {'prompt': prompt, 'max_tokens': max(700, int(len(draft.split()) * 2.2) + 200),
            'stop': ['\n\n\n\n'], 'temperature': temperature, 'top_p': 0.95, 'top_k': 0}
    if penalty:
        body.update({'copy_penalty': penalty, 'copy_n': copy_n, 'draft': draft})
    req = urllib.request.Request(f'http://127.0.0.1:{port}/v1/completions', json.dumps(body).encode(),
                                 {'Content-Type': 'application/json'})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        return json.load(r)['choices'][0]['text'].strip()


def humanize(draft, model_dir, port, thr=0.35, penalty=2.0, copy_n=5, temperature=1.0, keep_markdown=False):
    if keep_markdown:   # issue #1: rewrite prose block by block, headings and code fences pass through verbatim
        txt = rewrite_keeping_markdown(draft, lambda block: humanize(block, model_dir, port, thr, penalty, copy_n, temperature)[0])
        c = copy_rate(draft, txt)['copy_5gram']
        return txt, {'copy_5gram': round(c, 3), 'resampled_with_penalty': None, 'keep_markdown': True, 'words_in': len(draft.split()), 'words_out': len(txt.split())}
    build = load_format(model_dir); prompt = build(draft)
    txt = complete(port, prompt, draft, temperature)
    for _ in range(LANG_RETRIES):   # wrong language (English draft written in Chinese, or the reverse): sample again
        if not language_drift(draft, txt):
            break
        txt = complete(port, prompt, draft, temperature)
    c = copy_rate(draft, txt)['copy_5gram']; retried = False
    if c > thr:
        txt = complete(port, prompt, draft, temperature, penalty=penalty, copy_n=copy_n)
        c = copy_rate(draft, txt)['copy_5gram']; retried = True
    return txt, {'copy_5gram': round(c, 3), 'resampled_with_penalty': retried,
                 'words_in': len(draft.split()), 'words_out': len(txt.split())}


if __name__ == '__main__':
    ap = argparse.ArgumentParser()
    ap.add_argument('draft', nargs='?', help='text file; omit to read stdin')
    ap.add_argument('--model-dir', required=True); ap.add_argument('--port', type=int, default=8104)
    ap.add_argument('--thr', type=float, default=0.35); ap.add_argument('--penalty', type=float, default=2.0)
    ap.add_argument('--json', action='store_true', help='print JSON with stats')
    ap.add_argument('--keep-markdown', action='store_true', help='keep #-headings and ``` code blocks verbatim; prose between them is rewritten block by block (lower detector pass rate)')
    a = ap.parse_args()
    draft = open(a.draft).read() if a.draft else sys.stdin.read()
    txt, meta = humanize(draft, a.model_dir, a.port, a.thr, a.penalty, keep_markdown=a.keep_markdown)
    if a.json:
        print(json.dumps({'text': txt, **meta}, ensure_ascii=False, indent=1))
    else:
        print(txt); print(f"\n[copy_5gram={meta['copy_5gram']} resampled={meta['resampled_with_penalty']}]", file=sys.stderr)
