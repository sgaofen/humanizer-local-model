# Training data

**Dataset: [jialinyyzz/humanizer-data](https://huggingface.co/datasets/jialinyyzz/humanizer-data)** · 中文: [DATA.zh.md](DATA.zh.md)

This page describes the data behind the current release (**v2**, the 12B model in [jialinyyzz/humanizer](https://huggingface.co/jialinyyzz/humanizer)): where it came from, how the drafts were written, how pairs were filtered, and what we could and could not publish. Only rows that were actually used to train v2 are in the dataset. Data from experiments that never reached a released model is not included. The training code is not public yet.

## What is in the dataset

v2 was trained in three steps. Each step has its own config on Hugging Face:

| Config | Training step | Rows used in training | Published with text | Reference only (IDs) | Removed before release |
|---|---|---|---|---|---|
| `rewrite_sft` | Supervised fine-tuning: AI draft → human original | 28,560 | 5,471 | 21,632 | 1,457 |
| `dpo` | DPO: one draft, a better rewrite and a worse rewrite | 4,124 | 1,068 | 2,619 | 437 |
| `rl_prompts` | Reinforcement learning (three rounds): drafts the model rewrote and was scored on | 5,299 | 1,264 | 3,744 | 291 |

Each config has three splits, by license:

- **`redistributable`**: public-domain or openly licensed human text (U.S. government reports, CC BY, ODC-BY, Apache-2.0), plus drafts that an AI model wrote from a short scenario with no human original. Full text.
- **`noncommercial`**: human text under CC BY-NC-SA (PERSUADE 2.0 student essays, wikiHow, OpenStax). Full text, non-commercial use only, share-alike.
- **`reference_only`**: everything whose license does not let us pass the text on (news, forum and social posts, most email, PubMed abstracts, Chinese web text, and so on). These rows carry no text at all: only the source dataset, the record ID or URL where we could find it, a SHA-256 of the exact text we trained on, and its length. The AI draft for these rows is withheld as well, because a close rewrite of a copyrighted text is still derived from it. [`scripts/rebuild_reference_only.py`](../scripts/rebuild_reference_only.py) fetches the originals from their public sources; you can regenerate drafts with the prompt below.

In `rewrite_sft` and `dpo`, a `split` column says whether the row was a training row or one of the held-out rows that only measured validation loss to pick the checkpoint (500 and 206 rows).

### Fields

| Field | In | Meaning |
|---|---|---|
| `id` | all | Stable row ID |
| `split` | `rewrite_sft`, `dpo` | `train` or `validation` |
| `lang`, `genre` | all | `en`/`zh`; a plain genre label |
| `source`, `source_url`, `license`, `tier` | all | Where the human text came from and its license. `tier`: A = redistributable, B = non-commercial, C = reference only, "A (AI-written)" = no human original |
| `draft` | A/B rows | The AI-style draft the model was given |
| `original` | `rewrite_sft` A/B rows | The human text the model was trained to produce |
| `chosen`, `rejected`, `rejected_reason` | `dpo` A/B rows | The preferred rewrite, the dispreferred one, and the judge's note on what went wrong in it |
| `rl_rounds` | `rl_prompts` | Which RL rounds (1, 2, 3) used this draft |
| `upstream_dataset`, `upstream_id`, `upstream_url` | C rows | Where to fetch the original |
| `original_sha256`, `original_chars` | C rows | SHA-256 of the text we trained on (lower-cased, whitespace collapsed) and its length, to check a rebuild |
| `draft_model` | all | Which model wrote the draft |
| `response_model`, `judge_model` | `dpo` | Which model wrote the two rewrites (earlier checkpoints of this model) and which LLM judged them |

## Where the human text comes from

The human side is meant to be real human writing taken from published datasets, never text written by a model (the few exceptions we found are listed under [Cleaning](#cleaning-before-release)). Licenses were checked source by source on the rights holder's own page or the dataset card. When we could not tell whether a source may be redistributed, we put it in reference-only.

| Source | License | Tier | Rows published (SFT / DPO / RL) |
|---|---|---|---|
| [Congressional Research Service reports](https://www.everycrsreport.com/) | Public domain (U.S. federal government work) | A · redistributable | 3,490 / 216 / 58 |
| [eLife peer reviews and decision letters](https://github.com/elifesciences/elife-article-xml) | CC BY 4.0 (eLife) | A · redistributable | 1,268 / 52 / 12 |
| [NTSB aviation accident narratives](https://data.ntsb.gov/avdata) | Public domain (U.S. federal government work) | A · redistributable | – / 95 / 147 |
| [Apache Software Foundation board minutes](https://www.apache.org/foundation/records/minutes/) | Apache-2.0 (The Apache Software Foundation) | A · redistributable | – / 99 / 142 |
| [Project Gutenberg](https://www.gutenberg.org/policy/license.html) | Public domain in the U.S. (Project Gutenberg, headers removed) | A · redistributable | – / – / 194 |
| [PeerRead (ACL 2017 / CoNLL 2016 reviews)](https://github.com/allenai/PeerRead) | CC BY 4.0 | A · redistributable | 94 / 32 / – |
| [peS2o (s2orc open-access subset)](https://huggingface.co/datasets/allenai/peS2o) | ODC-BY 1.0 | A · redistributable | 80 / 2 / 7 |
| [NIH RePORTER project abstracts](https://reporter.nih.gov/) | ODbL 1.0 (NIH RePORTER on data.gov) | A · redistributable | – / – / 46 |
| [NSF award abstracts](https://www.nsf.gov/awardsearch/) | Public domain (U.S. federal government work); NSF award abstracts | A · redistributable | – / – / 19 |
| AI-written from a scenario prompt (no human original) | AI-generated draft; see model terms | A · AI-written | – / 325 / 340 |
| [PERSUADE 2.0](https://github.com/scrosseye/persuade_corpus_2.0) | CC BY-NC-SA 4.0 | B · non-commercial | 539 / 219 / 232 |
| [wikiHow](https://www.wikihow.com/wikiHow:Creative-Commons) | CC BY-NC-SA 3.0 (wikiHow) | B · non-commercial | – / 28 / 66 |
| [OpenStax textbooks](https://help.openstax.org/s/article/Licensing-information-of-OpenStax-textbooks) | CC BY-NC-SA 4.0 (OpenStax) | B · non-commercial | – / – / 1 |
| [PubMed abstracts (via MedRAG/pubmed)](https://huggingface.co/datasets/MedRAG/pubmed) | Publisher/author copyright (NLM does not grant rights) | C · reference only | 4,933 / 327 / 115 |
| [python-dev mailing list archive](https://mail.python.org/pipermail/python-dev/) | No license (each sender holds copyright) | C · reference only | 4,289 / 507 / 28 |
| [Zhihu-KOL (Zhihu answers)](https://huggingface.co/datasets/bzb2023/Zhihu-KOL-More-Than-100-Upvotes) | No license (Zhihu user content) | C · reference only | 2,161 / 182 / 143 |
| [COIG-CQIA](https://huggingface.co/datasets/m-a-p/COIG-CQIA) | No license stated (mixed platform content) | C · reference only | 1,953 / 160 / 126 |
| [Blog Authorship Corpus](https://u.cs.biu.ac.il/~koppel/BlogCorpus.htm) | Non-commercial research use; redistribution not granted | C · reference only | 1,791 / 135 / 254 |
| [AESLC / Enron email](https://huggingface.co/datasets/Yale-LILY/aeslc) | No license stated | C · reference only | 1,641 / 336 / 83 |
| [Enron email corpus (CMU)](https://www.cs.cmu.edu/~enron/) | No license stated | C · reference only | 1,657 / 231 / 82 |
| [Hacker News comments](https://news.ycombinator.com/) | Hacker News terms (no redistribution) | C · reference only | 1,260 / 159 / 5 |
| [Webis-TLDR-17 (Reddit)](https://huggingface.co/datasets/webis/tldr-17) | CC BY 4.0 from the aggregator over Reddit user content (not cleared with authors) | C · reference only | 923 / 67 / 173 |
| [CSL (Chinese scientific abstracts)](https://github.com/ydli-ai/CSL) | Journal/author copyright | C · reference only | 491 / 53 / 315 |
| [Webis-CMV-20 (Reddit r/changemyview)](https://zenodo.org/records/3778298) | CC BY 4.0 from the aggregator over Reddit user content (not cleared with authors) | C · reference only | – / 38 / 439 |
| [IMDb Large Movie Review Dataset](https://ai.stanford.edu/~amaas/data/sentiment/) | No license stated (IMDb user content) | C · reference only | 286 / 24 / – |
| [Listed-company announcements (Duxiaoman-DI/FinCorpus)](https://huggingface.co/datasets/Duxiaoman-DI/FinCorpus) | Aggregator license; content not cleared | C · reference only | – / – / 308 |
| [Stack Exchange data dump (2021)](https://archive.org/details/stackexchange) | CC BY-SA, but author attribution not kept in our copy | C · reference only | – / 90 / 207 |
| [Amazon Reviews 2023 (McAuley Lab)](https://amazon-reviews-2023.github.io/) | No license stated (user content) | C · reference only | – / 57 / 222 |
| [Paul Graham essays](https://www.paulgraham.com/articles.html) | All rights reserved | C · reference only | 246 / 20 / – |
| [NASA ASRS report narratives](https://asrs.arc.nasa.gov/) | No written license for reporter narratives | C · reference only | – / 101 / 156 |
| [ASAP-AES (Kaggle)](https://www.kaggle.com/c/asap-aes) | Kaggle competition data (no redistribution) | C · reference only | – / 70 / 161 |
| [MNBVC (Tianya forum)](https://huggingface.co/datasets/liwu/MNBVC) | Aggregator license; content not cleared | C · reference only | – / 3 / 227 |
| [Tweets (enryu43/twitter100m_tweets)](https://huggingface.co/datasets/enryu43/twitter100m_tweets) | X/Twitter terms (post IDs only) | C · reference only | – / – / 156 |
| [LinkedIn posts (Kaggle)](https://www.linkedin.com/legal/user-agreement) | Platform terms (no redistribution) | C · reference only | – / – / 139 |
| [People's Daily commentaries (Papersnake/people_daily_news)](https://huggingface.co/datasets/Papersnake/people_daily_news) | People's Daily copyright | C · reference only | – / 2 / 117 |
| [THUCNews (Sina News)](http://thuctc.thunlp.org/) | Sina News copyright; commercial use needs a license | C · reference only | – / 8 / 94 |
| [Dianping reviews (yf_dianping)](https://github.com/SophonPlus/ChineseNlpCorpus) | No license (user content) | C · reference only | – / 1 / 75 |
| [Yelp reviews (Yelp/yelp_review_full)](https://huggingface.co/datasets/Yelp/yelp_review_full) | Yelp dataset agreement (no redistribution) | C · reference only | – / – / 76 |
| [CNN/DailyMail](https://huggingface.co/datasets/abisee/cnn_dailymail) | Article copyright retained by CNN / Daily Mail | C · reference only | – / 47 / 4 |
| [nlp_chinese_corpus (brightmart)](https://github.com/brightmart/nlp_chinese_corpus) | Aggregator license; content not cleared | C · reference only | – / – / 14 |
| [NUS Corpus of Learner English](https://www.comp.nus.edu.sg/~nlp/conll14st/nucle_license.pdf) | NUS license (non-sublicensable) | C · reference only | – / – / 13 |
| [WritingPrompts (Reddit)](https://huggingface.co/datasets/euclaise/writingprompts) | Aggregator license over Reddit user content | C · reference only | – / – / 10 |
| Mixed academic text | Mixed source; not redistributed | C · reference only | 1 / 1 / 2 |

## How the drafts were written

A draft is the input the model learns to fix: an AI-style version of a human text with the same facts.

- **SFT drafts**: three models wrote them back from the human text: GLM-5.3 (15,822), GPT-5.6 Luna (7,421) and Claude Sonnet (5,317). All used one prompt (below). It asks for the way an AI assistant writes when asked to polish a text: calm, even, plain words, no slang or typos; every fact, number, name, greeting and sign-off kept; nothing added; every sentence rebuilt.
- **DPO and RL drafts**: some are rewrites of human text (by GLM-5.3, GPT-5.6 Luna, Claude Opus, Claude Sonnet and Muse Spark, in several styles: plain, corporate, verbose, concise, casual, lightly edited, structured). Others were written by Claude Sonnet or GPT-5.6 Luna from a one-line scenario ("an email to a professor asking for an extension", "a weekly report") so the model also sees drafts that look like real requests. Those have no human original.

<details><summary>The SFT draft prompt (English; a Chinese version was used for Chinese text)</summary>

```text
Rewrite this piece as a typical AI chatbot (e.g. ChatGPT) would if the author pasted it and asked: 'polish and rewrite this for me'. The result must read unmistakably like machine-generated text.
KEEP: every fact, number, date, name, URL, quoted passage, attribution line, greeting, sign-off, opening claim and closing remark. Add no facts, events or claims. If the source breaks off mid-sentence, stop at the same place.
AI STYLE (mandatory): write it the way Claude (Anthropic's assistant) writes when asked to draft this for the author: clear, measured, specific, plain words, short paragraphs, no stock phrases like 'Furthermore', 'Additionally', 'It is worth noting', 'crucial', 'comprehensive', 'leverage'; keep the author's person exactly as in the source (first person stays first person; an impersonal text stays impersonal, never add 'I think' to it), but smooth away raw feelings, slang, interjections, emoticons, shouting, profanity, jokes as worded, pet phrases and typos into calm, well-formed sentences; hedge opinions lightly ('I think', 'it seems'); a heading or a short list only where the content genuinely calls for it.
REWRITE: build every sentence anew; never reuse more than four consecutive words from the source except inside quoted passages, names, titles, numbers and units, URLs, code, greetings, sign-offs and attribution lines; change the order of sentences inside each paragraph where meaning allows, split or merge sentences, and swap every ordinary verb and adjective for a different one. Do not open with the source's first sentence, but its opening claim must still appear, reworded, in your first paragraph; do not keep the source's paragraph count or order.
NEVER refuse or comment. If the source is not prose (table of contents, boilerplate notice, bare list of headings, code dump), output exactly: UNUSABLE
CHECK before answering: (a) any run of five or more identical words outside the allowed exceptions -> rewrite; (f) do not add code fences, tables or bold labels the source does not have; (b) any fact, number, name, greeting, sign-off or attribution missing or changed -> restore; (c) any emoticon, shouting, slang or typo carried over -> replace; (d) any content not in the source -> delete. Output only the final text.
```

</details>

## How pairs were filtered

**SFT.** Every pair was read by an LLM judge (GLM-5.3) against its human original. A pair was kept only if the draft (1) reads as calm assistant prose without the author's slang, shouting or typos, (2) is actually rewritten, not mostly the same sentences, (3) keeps every fact, number, date, name and greeting and adds nothing, and (4) does not change what the author meant (turning an opinion into a stated fact, for example). The judge looks at overall meaning, not wording: "over 50" for "50+" is fine. Pairs it flagged were removed, along with drafts the writer refused or marked unusable, tables of contents and near-duplicates (same first 300 characters after normalising).

| Batch | Drafts written | Kept by the judge | Flagged | Judge failed | After the remaining checks |
|---|---|---|---|---|---|
| 1 | 10,381 | 8,866 | 1,371 | 144 | 8,979 |
| 2 | 22,303 | 13,783 | 1,905 | 251 | 19,619 |
| **Total** | **32,684** | | | | **28,598** |

Of the 28,598, 38 were longer than the 2,560-token training limit and were never used; 500 (grouped so that no human text appears on both sides) measured validation loss. The released weights are the best checkpoint by that loss, at step 1,725 of 1,754, so about 2% of the training rows were scheduled after it.

**DPO.** Earlier checkpoints of this model (a 4B version and the 12B SFT model) rewrote each draft several times, some samples with a penalty against copying the draft. An LLM judge (GLM-5.3 for most pairs, GPT-5.6 Luna for 1,579) compared each rewrite with the draft. The `chosen` rewrite keeps every fact and copies the least; the `rejected` one changed the meaning, invented something, dropped something, or barely changed the draft. Pairs that contradicted each other were removed. 4,124 pairs; 3,918 trained and 206 held out.

**RL.** The model rewrote each draft 8 times per step; an LLM judge (GLM-5.3, one strict vote) read each rewrite against the draft and penalised severe errors, invented content, changed meaning and dropped formatting, and a copy penalty discouraged reusing the draft's wording. There is no reference answer, so this config is drafts only. Round 1 used 788 drafts; round 2 drew 2,400 from a pool of 10,458; round 3 drew 2,400 from a genre-balanced pool of 8,268 (289 drafts appear in both rounds 2 and 3).

**No AI detector was used anywhere**, not to pick sources, filter pairs, judge rewrites or choose checkpoints.

## Cleaning before release

- **Personal data.** Every text field was scanned. Any row that mentions the project author (name, user names, email) was removed, in every split. This mostly hit drafts that a chat model wrote from a scenario and signed with the author's name. In the `redistributable` and `noncommercial` splits we also removed every row containing an email address, a phone number or an ID number. Reference-only rows carry no text.
- **Dates.** We checked every human text whose source has a date (Zhihu answer times, paper and report dates, mail headers, post dates). v2 was trained on 178 rows (SFT / DPO / RL: 167 / 8 / 3) whose Zhihu answer is dated after 2022-11-30 (for the Zhihu subset of COIG-CQIA the date is inferred from the answer ID), 1,426 rows (1,148 / 169 / 109) whose peS2o paper is dated after 2022-11-30 (1,396 of them in December 2022), and 11 rows from CRS reports whose latest version is dated after 2022-11-30. All of these are left out of this release. 1,879 human texts have no date we could check, mostly the non-Zhihu parts of COIG-CQIA (1,817); they are kept.
- **Suspected model-written text.** 4 SFT rows match a COIG-CQIA subset whose answers are known to be written by a model; they are left out.
- **Evaluation set.** Rows sharing two or more uncommon 8-word (English) or 13-character (Chinese) passages with any of the 362 evaluation drafts were removed, so the published evaluation in [`eval/`](../eval) stays separate. The evaluation drafts are written from scratch by models and are not in this dataset.
- **Fields.** Only the fields above are kept. Internal file paths, run names and judge logs are not published.

| Removed | `rewrite_sft` | `dpo` | `rl_prompts` |
|---|---|---|---|
| contact info / ID number | 5 | 6 | 88 |
| human side may be AI-written | 4 | 0 | 0 |
| human text dated after 2022-11-30 | 1,326 | 177 | 112 |
| overlaps evaluation set | 11 | 0 | 25 |
| user identity | 111 | 254 | 66 |

## Rebuilding the reference-only rows

```bash
pip install datasets requests pyarrow huggingface_hub
python scripts/rebuild_reference_only.py --config rewrite_sft --out rebuilt_sft.jsonl
```

The script reads the `reference_only` split, fetches each original from its public source by `upstream_id` / `upstream_url`, and reports whether it matches `original_sha256`. Some sources need you to accept terms or download files yourself first (Kaggle, NUS, Yelp); the script prints what is missing. You get the whole upstream record; our human texts are often an excerpt of it with whitespace and line breaks tidied, so many correct rebuilds will not match the hash exactly. 712 reference-only rows have no record ID we could recover; for those only the source dataset is given.

## Model terms for the AI-written side

The drafts, and the judgments behind the DPO pairs, come from commercial models. Their terms limit using outputs to train other models. **Check them before you train on this data:**

- Anthropic (Claude): outputs may not be used to train models that compete with Anthropic's ([commercial terms](https://www.anthropic.com/legal/commercial-terms), [FAQ](https://support.claude.com/en/articles/12326764-can-i-use-my-outputs-to-train-an-ai-model)).
- OpenAI (GPT): outputs may not be used to develop competing models ([terms of use](https://openai.com/policies/row-terms-of-use/)).
- Zhipu (GLM): the mainland-China platform's agreement forbids using generated content to train any other model ([bigmodel.cn agreement](https://docs.bigmodel.cn/cn/terms/user-agreement)); the international platform forbids competing models ([Z.ai terms](https://docs.z.ai/legal-agreement/terms-of-use)).

## Known limitations

- Most of the human text is reference-only, so a full rebuild depends on upstream sources staying online.
- The SFT human text leans formal: paper abstracts, reports and email make up most of it; casual social posts are a small share.
- The judges are LLMs. They are strict and sometimes flag harmless rewording; they also miss some errors.
- Chinese is 17% of the SFT rows.
- "Human" means written by people; it does not mean well written.

## Citation

```bibtex
@misc{humanizer2026data,
  title  = {humanizer-data: training data for the humanizer rewriting model (v2)},
  author = {Stephen Yu},
  year   = {2026},
  url    = {https://huggingface.co/datasets/jialinyyzz/humanizer-data}
}
```

Please also cite the upstream datasets you use (links in the source table).
