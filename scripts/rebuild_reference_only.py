#!/usr/bin/env python3
"""Rebuild the reference-only rows of jialinyyzz/humanizer-data.

Those rows carry no text because the source license does not let us pass it on. Each row has
`upstream_dataset`, `upstream_id` and (often) `upstream_url`; this script fetches the original record
from its public source and checks it against `original_sha256`.

Usage:
    pip install datasets requests pyarrow huggingface_hub
    python scripts/rebuild_reference_only.py --config rewrite_sft --out rebuilt_sft.jsonl [--only MedRAG/pubmed] [--limit 100]
    python scripts/rebuild_reference_only.py --input reference_only.jsonl --out rebuilt.jsonl     # a local copy of the split

Output, one line per row: {id, upstream_dataset, upstream_id, how, text, sha256_match, error?}

Notes:
  * You get the whole upstream record. The text we trained on is often an excerpt of it with whitespace,
    hard line breaks, quoted replies and signatures tidied, so `sha256_match` is false for many rows even
    when the record is the right one. `sha256_match` compares the lower-cased, whitespace-collapsed text.
  * Row-index IDs ("[config:]split:N") are 0-based positions in the upstream Hugging Face dataset
    (the `row_idx` of the datasets-server /rows API).
  * Some sources must be downloaded by hand or need you to accept terms first (PERSUADE 2.0, the Enron
    archive, Kaggle data, NUCLE, large corpora); the script reports what to do for those rows.
  * Respect each source's license and terms. These rows are reference-only for a reason.
"""
import csv, gzip, html, io, json, os, re, sys, time, urllib.parse, urllib.request

UA = 'humanizer-data-rebuild/1.0'
ROWS_API = 'https://datasets-server.huggingface.co/rows'
HF_TOKEN = os.environ.get('HF_TOKEN')


def http(url, data=None, headers=None, tries=5):
    h = {'User-Agent': UA}
    if headers: h.update(headers)
    for a in range(tries):
        try:
            with urllib.request.urlopen(urllib.request.Request(url, data=data, headers=h), timeout=120) as r:
                return r.read()
        except urllib.error.HTTPError as e:
            if e.code == 404: raise
            time.sleep(45 if e.code == 429 else 3 * (a + 1))
        except Exception:
            time.sleep(3 * (a + 1))
    raise RuntimeError(f'fetch failed: {url}')


def hf_row(ds, cfg, split, idx, revision=None):
    q = {'dataset': ds, 'config': cfg, 'split': split, 'offset': int(idx), 'length': 1}
    hd = {'Authorization': f'Bearer {HF_TOKEN}'} if HF_TOKEN else None
    j = json.loads(http(f'{ROWS_API}?{urllib.parse.urlencode(q)}', headers=hd))
    rows = j.get('rows') or []
    if not rows: raise LookupError(f'{ds} {cfg}/{split} row {idx} not found')
    r = rows[0]
    if r.get('truncated_cells'):   # the rows API truncates very large cells
        raise LookupError(f'{ds} row {idx}: cell truncated by the rows API; download the parquet file instead')
    return r['row']


def parse_rowid(uid, default_cfg='default'):
    p = uid.split(':')
    if len(p) == 2: return default_cfg, p[0], int(p[1])
    return p[0], p[1], int(p[2])


def hf_file(repo, path, revision='main'):
    """Download one file from a Hugging Face dataset repo (cached)."""
    try:
        from huggingface_hub import hf_hub_download
        return open(hf_hub_download(repo, path, repo_type='dataset', revision=revision), 'rb').read()
    except ImportError:
        url = f'https://huggingface.co/datasets/{repo}/resolve/{urllib.parse.quote(revision, safe="")}/{path}'
        return http(url)


# ---------------------------------------------------------------- per-source fetchers
def get_rowindex(ds, field, default_cfg='default', post=None):
    def f(r):
        cfg, split, i = parse_rowid(r['upstream_id'], default_cfg)
        t = hf_row(ds, cfg, split, i)[field]
        return post(t) if post else t
    return f


def get_hn(r):
    j = json.loads(http(f"https://hacker-news.firebaseio.com/v0/item/{r['upstream_id']}.json"))
    return j.get('text')


_medrag = {}


def get_pubmed(r):
    m = re.match(r'(pubmed\d+n\d+)_(\d+)$', r['upstream_id'])
    if m:
        fn, k = m.group(1), int(m.group(2))
        if fn not in _medrag:
            _medrag[fn] = hf_file('MedRAG/pubmed', f'chunk/{fn}.jsonl').decode().splitlines()
        o = json.loads(_medrag[fn][k])
        assert o['id'] == r['upstream_id'], 'MedRAG id mismatch'
        return o['contents']
    pmid = re.search(r'/(\d+)/?$', r.get('upstream_url') or '').group(1)   # fallback: NCBI E-utilities (format differs from MedRAG)
    return http(f'https://eutils.ncbi.nlm.nih.gov/entrez/eutils/efetch.fcgi?db=pubmed&id={pmid}&rettype=abstract&retmode=text').decode()


def get_gutenberg2(r):
    k = int(r['upstream_id'].split(':')[1])
    return json.loads(hf_file('nbeerbower/gutenberg2-dpo', 'gb2_2024_11_16.json'))[k]['chosen']


_small = {}


def get_by_col(repo, path, col, field, kind):
    """Download a small file once and index it by a column."""
    def f(r):
        key = (repo, path)
        if key not in _small:
            b = hf_file(repo, path)
            if kind == 'parquet':
                import pyarrow.parquet as pq
                rows = pq.read_table(io.BytesIO(b)).to_pylist()
            else:
                rows = list(csv.DictReader(io.StringIO(b.decode('utf-8'))))
            _small[key] = {str(x[col]): x for x in rows}
        return _small[key][r['upstream_id']][field]
    return f


def get_peerread(r):
    path, k = r['upstream_id'].split('#')
    j = json.loads(http(f'https://raw.githubusercontent.com/allenai/PeerRead/master/data/{path}'))
    return j['reviews'][int(k)]['comments']


_crs = {}


def get_crs(r):
    if not _crs:
        for row in csv.DictReader(io.StringIO(http('https://www.everycrsreport.com/reports.csv').decode('utf-8'))):
            _crs[row['number']] = row
    h = http('https://www.everycrsreport.com/' + _crs[r['upstream_id']]['latestHTML']).decode('utf-8', 'replace')
    t = re.sub(r'(?i)<br\s*/?>', '\n', h); t = re.sub(r'(?i)</p>', '\n\n', t); t = re.sub(r'<[^>]+>', '', t)
    return re.sub(r'\n{3,}', '\n\n', re.sub(r'[ \t]+', ' ', html.unescape(t))).strip()


def get_elife(r):
    x = http('https://raw.githubusercontent.com/elifesciences/elife-article-xml/master/' + r['upstream_id']).decode()
    m = re.search(r'<sub-article[^>]*article-type="decision-letter".*?</sub-article>', x, re.S)
    return html.unescape(re.sub(r'<[^>]+>', ' ', m.group(0))) if m else None


_mbox = {}


def get_pipermail(r):
    lst, date, subj = r['upstream_id'].split('|', 2)
    url = r['upstream_url']
    if url not in _mbox:
        try: raw = http(url)
        except urllib.error.HTTPError: raw = gzip.decompress(http(url + '.gz'))
        _mbox[url] = re.split(r'\n(?=From \S+ at \S+ )', raw.decode('utf-8', 'replace'))
    for msg in _mbox[url]:
        if f'\nDate: {date}' in msg and subj.strip() in re.sub(r'\s+', ' ', msg[:2000]):
            return msg.split('\n\n', 1)[1] if '\n\n' in msg else msg   # message body (quoted replies and signatures were removed for training)
    return None


def get_gutenberg_chunk(r):
    bid, k = re.match(r'(\d+)#chunk(\d+)', r['upstream_id']).groups()
    txt = http(f'https://www.gutenberg.org/cache/epub/{bid}/pg{bid}.txt').decode('utf-8', 'replace')
    return {'book_text': txt, 'chunk_index': int(k), 'note': 'whole book; strip the Project Gutenberg header/footer, split into ~3,500-character chunks on paragraph breaks and take chunk k'}


_barilan = {}


def get_barilan(r):
    """Script-based dataset: read row N of the converted parquet shard."""
    if not _barilan:
        import pyarrow.parquet as pq
        b = hf_file('barilan/blog_authorship_corpus', 'blog_authorship_corpus/train/0000.parquet', revision='refs/convert/parquet')
        _barilan['t'] = pq.read_table(io.BytesIO(b), columns=['text'])
    return _barilan['t'].column('text')[int(r['upstream_id'].split(':')[1])].as_py()


def manual(why):
    def f(r): raise NotImplementedError(why)
    return f


RECIPES = [   # (upstream_dataset prefix, fetcher)
    ('Yale-LILY/aeslc', get_rowindex('Yale-LILY/aeslc', 'email_body')),
    ('barilan/blog_authorship_corpus (data/blogs.zip', manual('download blogs.zip from barilan/blog_authorship_corpus; upstream_id = blog:<blogger id>:<post index>')),
    ('barilan/blog_authorship_corpus', get_barilan),
    ('tasksource/blog_authorship_corpus', lambda r: get_rowindex('tasksource/blog_authorship_corpus', 'text')(r)
        if r['upstream_id'].startswith('train:') else manual('tasksource/blog_authorship_corpus: upstream_id = blog_<id>_<row>; look the row up in the CSV')(r)),
    ('stanfordnlp/imdb', get_rowindex('stanfordnlp/imdb', 'text', 'plain_text', post=lambda t: t.replace('<br />', '\n'))),
    ('euclaise/WritingPrompts_curated', get_rowindex('euclaise/WritingPrompts_curated', 'body')),
    ('m-a-p/COIG-CQIA', get_rowindex('m-a-p/COIG-CQIA', 'output')),
    ('nbeerbower/gutenberg2-dpo', get_gutenberg2),
    ('OpenPipe/hacker-news', get_hn),
    ('MedRAG/pubmed', get_pubmed),
    ('jondurbin/gutenberg-dpo-v0.1', get_by_col('jondurbin/gutenberg-dpo-v0.1', 'gutenberg-dpo.parquet', 'id', 'chosen', 'parquet')),
    ('sgoel9/paul_graham_essays', get_by_col('sgoel9/paul_graham_essays', 'pual_graham_essays.csv', 'id', 'text', 'csv')),
    ('allenai/PeerRead', get_peerread),
    ('EveryCRSReport.com', get_crs),
    ('elifesciences/elife-article-xml', get_elife),
    ('python-dev pipermail', get_pipermail), ('python-ideas pipermail', get_pipermail),
    ('Project Gutenberg', get_gutenberg_chunk),
    ('allenai/peS2o', manual('large: stream allenai/peS2o (v2) and filter by id; collect all ids first and scan once')),
    ('neuclir/csl', manual('download neuclir/csl (csl split) and filter by doc_id')),
    ('bzb2023/Zhihu-KOL', manual('download the parquet shards and filter on METADATA.answer_id')),
    ('webis/tldr-17', manual('large: filter the refs/convert/parquet shards by id')),
    ('abisee/cnn_dailymail', manual('config 3.0.0, train split: filter by id')),
    ('Enron Email Dataset', manual('download enron_mail_20150507.tar.gz from CMU and read the maildir path in upstream_id')),
    ('PERSUADE 2.0', manual('get the CSV linked from the PERSUADE 2.0 README and look up essay_id_comp')),
]




def resolve(r):
    ds = r.get('upstream_dataset') or ''
    for pre, fn in RECIPES:
        if ds.startswith(pre): return fn, pre
    url = r.get('upstream_url')
    return manual(f'open upstream_url ({url}) or look the id up in {ds}' if url else f'look the id up in {ds}'), 'manual'


def norm(t): return re.sub(r'\s+', ' ', t).strip().lower()


def main():
    import argparse, hashlib
    ap = argparse.ArgumentParser()
    ap.add_argument('--config', choices=['rewrite_sft', 'dpo', 'rl_prompts'])
    ap.add_argument('--input', help='local jsonl of reference_only rows instead of --config')
    ap.add_argument('--out', required=True); ap.add_argument('--only'); ap.add_argument('--limit', type=int)
    a = ap.parse_args()
    if a.input:
        rows = [json.loads(l) for l in open(a.input, encoding='utf-8') if l.strip()]
    else:
        from datasets import load_dataset
        rows = load_dataset('jialinyyzz/humanizer-data', a.config, split='reference_only')
    n = ok = 0
    with open(a.out, 'w', encoding='utf-8') as w:
        for r in rows:
            if not r.get('upstream_id'): continue
            if a.only and not (r.get('upstream_dataset') or '').startswith(a.only): continue
            fn, how = resolve(r)
            rec = {'id': r['id'], 'upstream_dataset': r['upstream_dataset'], 'upstream_id': r['upstream_id'], 'how': how}
            try:
                t = fn(r); rec['text'] = t
                rec['sha256_match'] = isinstance(t, str) and hashlib.sha256(norm(t).encode()).hexdigest() == r.get('original_sha256')
                ok += bool(t)
            except Exception as e:
                rec['text'] = None; rec['error'] = f'{type(e).__name__}: {e}'[:300]
            w.write(json.dumps(rec, ensure_ascii=False) + '\n'); n += 1
            if a.limit and n >= a.limit: break
    print(f'{n} rows, {ok} fetched -> {a.out}')


if __name__ == '__main__':
    main()
