# 训练数据

**数据集：[jialinyyzz/humanizer-data](https://huggingface.co/datasets/jialinyyzz/humanizer-data)** · English: [DATA.md](DATA.md)

这一页讲现行版本（**v2**，即 [jialinyyzz/humanizer](https://huggingface.co/jialinyyzz/humanizer) 上的 12B 模型）用了哪些数据：从哪来、草稿怎么写的、怎么筛的、哪些能公开哪些不能。数据集里只有真正训过 v2 的条目；没进任何发布模型的实验数据都不在里面。训练代码还没公开。

## 数据集里有什么

v2 分三步训练，每一步在 Hugging Face 上是一个 config：

| Config | 对应的训练步骤 | 训练实际用到 | 带全文公开 | 只给 ID | 发布前剔除 |
|---|---|---|---|---|---|
| `rewrite_sft` | 监督微调：AI 草稿 → 人写原文 | 28,560 | 5,471 | 21,632 | 1,457 |
| `dpo` | DPO：一篇草稿，一好一差两个改写 | 4,124 | 1,068 | 2,619 | 437 |
| `rl_prompts` | 强化学习（三轮）：模型改写并被打分的草稿 | 5,299 | 1,264 | 3,744 | 291 |

每个 config 按协议分三个 split：

- **`redistributable`**：公有领域或开放许可的人写文本（美国政府报告、CC BY、ODC-BY、Apache-2.0），以及 AI 按一句话场景从零写的草稿（没有人写原文）。带全文。
- **`noncommercial`**：CC BY-NC-SA 的人写文本（PERSUADE 2.0 学生作文、wikiHow、OpenStax）。带全文，仅限非商业使用，相同方式共享。
- **`reference_only`**：协议不允许我们转发原文的（新闻、论坛和社交帖子、大部分邮件、PubMed 摘要、中文网络文本等）。这些行**不带任何正文**，只有来源数据集、能找到的记录 ID 或 URL、我们训练所用文本的 SHA-256 和长度。这些行的 AI 草稿也不发，因为贴着原文改写的草稿仍然是从受版权保护的原文派生的。[`scripts/rebuild_reference_only.py`](../scripts/rebuild_reference_only.py) 会去公开来源取回原文；草稿可以用下面的提示词重新生成。

`rewrite_sft` 和 `dpo` 里有一列 `split`，标明这一行是训练行，还是只用来算验证损失、挑检查点的留出行（分别 500 和 206 行）。

字段说明见英文版 [DATA.md](DATA.md#fields)。

## 人写原文从哪来

人写一侧应当全部是已公开数据集里的真人文本，不用模型写的文本（发现的少数例外见“发布前的清洗”）。协议逐个来源核过（权利人自己的页面或数据卡）。拿不准能不能转发的，一律放进 reference_only。

| 来源 | 协议 | 档位 | 公开行数（SFT / DPO / RL） |
|---|---|---|---|
| [Congressional Research Service reports](https://www.everycrsreport.com/) | Public domain (U.S. federal government work) | A · 可转发 | 3,490 / 216 / 58 |
| [eLife peer reviews and decision letters](https://github.com/elifesciences/elife-article-xml) | CC BY 4.0 (eLife) | A · 可转发 | 1,268 / 52 / 12 |
| [NTSB aviation accident narratives](https://data.ntsb.gov/avdata) | Public domain (U.S. federal government work) | A · 可转发 | – / 95 / 147 |
| [Apache Software Foundation board minutes](https://www.apache.org/foundation/records/minutes/) | Apache-2.0 (The Apache Software Foundation) | A · 可转发 | – / 99 / 142 |
| [Project Gutenberg](https://www.gutenberg.org/policy/license.html) | Public domain in the U.S. (Project Gutenberg, headers removed) | A · 可转发 | – / – / 194 |
| [PeerRead (ACL 2017 / CoNLL 2016 reviews)](https://github.com/allenai/PeerRead) | CC BY 4.0 | A · 可转发 | 94 / 32 / – |
| [peS2o (s2orc open-access subset)](https://huggingface.co/datasets/allenai/peS2o) | ODC-BY 1.0 | A · 可转发 | 80 / 2 / 7 |
| [NIH RePORTER project abstracts](https://reporter.nih.gov/) | ODbL 1.0 (NIH RePORTER on data.gov) | A · 可转发 | – / – / 46 |
| [NSF award abstracts](https://www.nsf.gov/awardsearch/) | Public domain (U.S. federal government work); NSF award abstracts | A · 可转发 | – / – / 19 |
| AI-written from a scenario prompt (no human original) | AI-generated draft; see model terms | A · AI 从零写 | – / 325 / 340 |
| [PERSUADE 2.0](https://github.com/scrosseye/persuade_corpus_2.0) | CC BY-NC-SA 4.0 | B · 非商业 | 539 / 219 / 232 |
| [wikiHow](https://www.wikihow.com/wikiHow:Creative-Commons) | CC BY-NC-SA 3.0 (wikiHow) | B · 非商业 | – / 28 / 66 |
| [OpenStax textbooks](https://help.openstax.org/s/article/Licensing-information-of-OpenStax-textbooks) | CC BY-NC-SA 4.0 (OpenStax) | B · 非商业 | – / – / 1 |
| [PubMed abstracts (via MedRAG/pubmed)](https://huggingface.co/datasets/MedRAG/pubmed) | Publisher/author copyright (NLM does not grant rights) | C · 只给 ID | 4,933 / 327 / 115 |
| [python-dev mailing list archive](https://mail.python.org/pipermail/python-dev/) | No license (each sender holds copyright) | C · 只给 ID | 4,289 / 507 / 28 |
| [Zhihu-KOL (Zhihu answers)](https://huggingface.co/datasets/bzb2023/Zhihu-KOL-More-Than-100-Upvotes) | No license (Zhihu user content) | C · 只给 ID | 2,161 / 182 / 143 |
| [COIG-CQIA](https://huggingface.co/datasets/m-a-p/COIG-CQIA) | No license stated (mixed platform content) | C · 只给 ID | 1,953 / 160 / 126 |
| [Blog Authorship Corpus](https://u.cs.biu.ac.il/~koppel/BlogCorpus.htm) | Non-commercial research use; redistribution not granted | C · 只给 ID | 1,791 / 135 / 254 |
| [AESLC / Enron email](https://huggingface.co/datasets/Yale-LILY/aeslc) | No license stated | C · 只给 ID | 1,641 / 336 / 83 |
| [Enron email corpus (CMU)](https://www.cs.cmu.edu/~enron/) | No license stated | C · 只给 ID | 1,657 / 231 / 82 |
| [Hacker News comments](https://news.ycombinator.com/) | Hacker News terms (no redistribution) | C · 只给 ID | 1,260 / 159 / 5 |
| [Webis-TLDR-17 (Reddit)](https://huggingface.co/datasets/webis/tldr-17) | CC BY 4.0 from the aggregator over Reddit user content (not cleared with authors) | C · 只给 ID | 923 / 67 / 173 |
| [CSL (Chinese scientific abstracts)](https://github.com/ydli-ai/CSL) | Journal/author copyright | C · 只给 ID | 491 / 53 / 315 |
| [Webis-CMV-20 (Reddit r/changemyview)](https://zenodo.org/records/3778298) | CC BY 4.0 from the aggregator over Reddit user content (not cleared with authors) | C · 只给 ID | – / 38 / 439 |
| [IMDb Large Movie Review Dataset](https://ai.stanford.edu/~amaas/data/sentiment/) | No license stated (IMDb user content) | C · 只给 ID | 286 / 24 / – |
| [Listed-company announcements (Duxiaoman-DI/FinCorpus)](https://huggingface.co/datasets/Duxiaoman-DI/FinCorpus) | Aggregator license; content not cleared | C · 只给 ID | – / – / 308 |
| [Stack Exchange data dump (2021)](https://archive.org/details/stackexchange) | CC BY-SA, but author attribution not kept in our copy | C · 只给 ID | – / 90 / 207 |
| [Amazon Reviews 2023 (McAuley Lab)](https://amazon-reviews-2023.github.io/) | No license stated (user content) | C · 只给 ID | – / 57 / 222 |
| [Paul Graham essays](https://www.paulgraham.com/articles.html) | All rights reserved | C · 只给 ID | 246 / 20 / – |
| [NASA ASRS report narratives](https://asrs.arc.nasa.gov/) | No written license for reporter narratives | C · 只给 ID | – / 101 / 156 |
| [ASAP-AES (Kaggle)](https://www.kaggle.com/c/asap-aes) | Kaggle competition data (no redistribution) | C · 只给 ID | – / 70 / 161 |
| [MNBVC (Tianya forum)](https://huggingface.co/datasets/liwu/MNBVC) | Aggregator license; content not cleared | C · 只给 ID | – / 3 / 227 |
| [Tweets (enryu43/twitter100m_tweets)](https://huggingface.co/datasets/enryu43/twitter100m_tweets) | X/Twitter terms (post IDs only) | C · 只给 ID | – / – / 156 |
| [LinkedIn posts (Kaggle)](https://www.linkedin.com/legal/user-agreement) | Platform terms (no redistribution) | C · 只给 ID | – / – / 139 |
| [People's Daily commentaries (Papersnake/people_daily_news)](https://huggingface.co/datasets/Papersnake/people_daily_news) | People's Daily copyright | C · 只给 ID | – / 2 / 117 |
| [THUCNews (Sina News)](http://thuctc.thunlp.org/) | Sina News copyright; commercial use needs a license | C · 只给 ID | – / 8 / 94 |
| [Dianping reviews (yf_dianping)](https://github.com/SophonPlus/ChineseNlpCorpus) | No license (user content) | C · 只给 ID | – / 1 / 75 |
| [Yelp reviews (Yelp/yelp_review_full)](https://huggingface.co/datasets/Yelp/yelp_review_full) | Yelp dataset agreement (no redistribution) | C · 只给 ID | – / – / 76 |
| [CNN/DailyMail](https://huggingface.co/datasets/abisee/cnn_dailymail) | Article copyright retained by CNN / Daily Mail | C · 只给 ID | – / 47 / 4 |
| [nlp_chinese_corpus (brightmart)](https://github.com/brightmart/nlp_chinese_corpus) | Aggregator license; content not cleared | C · 只给 ID | – / – / 14 |
| [NUS Corpus of Learner English](https://www.comp.nus.edu.sg/~nlp/conll14st/nucle_license.pdf) | NUS license (non-sublicensable) | C · 只给 ID | – / – / 13 |
| [WritingPrompts (Reddit)](https://huggingface.co/datasets/euclaise/writingprompts) | Aggregator license over Reddit user content | C · 只给 ID | – / – / 10 |
| Mixed academic text | Mixed source; not redistributed | C · 只给 ID | 1 / 1 / 2 |

## 草稿怎么写的

草稿就是模型要学着改好的输入：同一份事实、AI 腔调的版本。

- **SFT 草稿**：三个模型照着人写原文反写：GLM-5.3（15,822）、GPT-5.6 Luna（7,421）、Claude Sonnet（5,317）。用的是同一条提示词（见英文版附录），要求写成 AI 助手被请去"润色一下"时的样子：平稳、规整、用词平实，不带口语和错别字；所有事实、数字、人名、问候、落款都保留，不添加内容，每一句都重新写。
- **DPO 和 RL 草稿**：一部分是照人写原文反写的（GLM-5.3、GPT-5.6 Luna、Claude Opus、Claude Sonnet、Muse Spark，几种风格：平实、公文腔、啰嗦、精简、口语、轻改、结构化）；另一部分是 Claude Sonnet 或 GPT-5.6 Luna 按一句话场景（"给教授写邮件申请延期""一份周报"）从零写的，让模型也见到像真实用户请求的草稿，这部分没有人写原文。

## 怎么筛的

**SFT。** 每一对都由 LLM 判官（GLM-5.3）对照人写原文读一遍。只有四条都满足才留下：(1) 草稿是平稳的助手腔，没带作者的口语、吼叫、错别字；(2) 真的重写了，不是大半句子照搬；(3) 每个事实、数字、日期、人名、问候都在，没有添加；(4) 没改作者的意思（比如把观点改成陈述事实）。判官看整体意思，不抠字眼："50+" 写成 "over 50" 不算错。被判有问题的都删掉；另外删掉写手拒写或标为不可用的、目录页，以及近似重复（归一化后前 300 字相同）。

| 批次 | 写出的草稿 | 判官通过 | 判有问题 | 判官调用失败 | 其余检查后 |
|---|---|---|---|---|---|
| 1 | 10,381 | 8,866 | 1,371 | 144 | 8,979 |
| 2 | 22,303 | 13,783 | 1,905 | 251 | 19,619 |
| **合计** | **32,684** | | | | **28,598** |

28,598 对里有 38 对超过训练长度上限（2,560 token），从没用上；500 对（按人写原文分组，同一原文不会两边都有）用来算验证损失。发布的权重是按验证损失选出的最佳检查点，在 1,754 步中的第 1,725 步，所以约 2% 的训练行排在它之后。

**DPO。** 让本模型的早期检查点（一个 4B 版本和 12B 的 SFT 版本）把每篇草稿改写多次，其中一部分采样带了"别照抄草稿"的惩罚。LLM 判官（大部分是 GLM-5.3，1,579 对是 GPT-5.6 Luna）把每个改写和草稿对照。`chosen` 是事实全保、照抄最少的那个；`rejected` 是改了意思、编了内容、漏了内容，或者几乎没改草稿的那个。互相矛盾的对删掉。共 4,124 对，训练 3,918，留出 206。

**RL。** 每步每篇草稿改写 8 次；LLM 判官（GLM-5.3，单票从严）对照草稿通读每个改写，严重错、编造、改意思、丢格式都扣分，另有照抄惩罚。没有标准答案，所以这个 config 只有草稿。第 1 轮用了 788 篇；第 2 轮从 10,458 篇的题库里抽了 2,400 篇；第 3 轮从按体裁配平的 8,268 篇题库里抽了 2,400 篇（其中 289 篇第 2、3 轮都用到）。

**全程没有用任何 AI 检测器**：不用来挑来源、筛数据、给改写打分，也不用来挑检查点。

## 发布前的清洗

- **个人信息。** 所有文本字段全量扫描。凡是提到项目作者本人（名字、用户名、邮箱）的行，所有 split 里都整条删掉；命中的主要是聊天模型按场景写草稿时署上了作者名字。`redistributable` 和 `noncommercial` 里，凡含邮箱、电话、证件号的行也整条删掉。reference_only 行本来就不带正文。
- **日期。** 凡是来源带日期的人写文本（知乎回答时间、论文和报告日期、邮件头、帖子日期）都核了一遍。v2 训练时混进了 178 条（SFT / DPO / RL：167 / 8 / 3）日期晚于 2022-11-30 的知乎回答（COIG-CQIA 知乎子集的日期是按回答 ID 推算的），1,426 条（1,148 / 169 / 109）日期晚于 2022-11-30 的 peS2o 论文（其中 1,396 条在 2022 年 12 月），以及 11 条最新版本晚于 2022-11-30 的 CRS 报告。发布版已全部剔除。另有 1,879 篇人写文本核不出日期，主要是 COIG-CQIA 里知乎以外的部分（1,817 篇），保留。
- **疑似模型写的文本。** 有 4 条 SFT 对应到 COIG-CQIA 里一个已知回答由模型生成的子集，已剔除。
- **评测集。** 和 362 篇评测草稿中任何一篇共享两处以上不常见的 8 词（英文）或 13 字（中文）片段的行都删掉，所以公开的评测（[`eval/`](../eval)）和训练数据是分开的。评测草稿是模型从零写的，不在本数据集里。
- **字段。** 只留上面列的字段，不发内部文件路径、实验名和判官日志。

| 删掉的原因 | `rewrite_sft` | `dpo` | `rl_prompts` |
|---|---|---|---|
| 含邮箱、电话或证件号（只针对带正文的 split） | 5 | 6 | 88 |
| 人写一侧疑似 AI 生成（COIG-CQIA 弱智吧子集） | 4 | 0 | 0 |
| 人写原文日期晚于 2022-11-30 | 1,326 | 177 | 112 |
| 与评测集重合 | 11 | 0 | 25 |
| 提到项目作者本人 | 111 | 254 | 66 |

## 重建 reference_only 行

```bash
pip install datasets requests pyarrow huggingface_hub
python scripts/rebuild_reference_only.py --config rewrite_sft --out rebuilt_sft.jsonl
```

脚本读取 `reference_only` split，按 `upstream_id` / `upstream_url` 从公开来源取回原文，并报告和 `original_sha256` 对不对得上。有些来源要你先自己同意条款或下载文件（Kaggle、NUS、Yelp），脚本会提示缺什么。取回的是上游整条记录；我们用的人写文本常常只是其中一段，空白和换行也整理过，所以很多取对了的行哈希也对不上。有 712 条 reference_only 行找不到上游记录 ID，只给了来源数据集。

## AI 一侧的模型条款

草稿以及 DPO 配对背后的判定来自商业模型，它们的条款限制用输出去训练别的模型。**用这份数据训练前请自己核对：**

- Anthropic（Claude）：不得用输出训练与 Anthropic 竞争的模型（[商业条款](https://www.anthropic.com/legal/commercial-terms)、[说明](https://support.claude.com/en/articles/12326764-can-i-use-my-outputs-to-train-an-ai-model)）。
- OpenAI（GPT）：不得用输出开发竞争模型（[使用条款](https://openai.com/policies/row-terms-of-use/)）。
- 智谱（GLM）：国内平台的用户协议禁止把生成内容用于其他任何模型的训练（[bigmodel.cn 用户协议](https://docs.bigmodel.cn/cn/terms/user-agreement)）；国际平台禁止训练竞争模型（[Z.ai 条款](https://docs.z.ai/legal-agreement/terms-of-use)）。

## 已知局限

- 大部分人写文本只能给 ID，完整重建取决于上游来源一直在线。
- SFT 的人写文本偏正式：论文摘要、报告、邮件占大头，随意的社交帖子比例小。
- 判官是 LLM，偏严，有时会把无害的换说法也标出来；也会漏掉一些错。
- 中文占 SFT 行的 17%。
- 这里的"人写"只表示由人写的，不代表写得好。

## 引用

见英文版 [DATA.md](DATA.md#citation)。也请引用你用到的上游数据集（链接见来源表）。
