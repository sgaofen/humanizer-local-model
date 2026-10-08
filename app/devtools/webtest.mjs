import { planDocument, newDocumentJob, documentSignature, readDocumentJob, jobMatches, combinedDocument, runDocumentSections } from '../web/js/document.js';
// 网页纯逻辑的单测(不需要浏览器):node devtools/webtest.mjs 或 bun devtools/webtest.mjs
import { activeFacts, protectFacts, restoreFacts, streamView, withFactGuard, FACT_TRIES } from '../web/js/facts.js';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { pyStrip, countText, numberCheck, estimateTokens, isLongDraft } from '../web/js/text.js';
import { buildPrompt, nPredictFor, selfCheck } from '../web/js/prompt.js';
import { tokenize, diffTokens } from '../web/js/diff.js';
import { hanCount, wentChinese, wentEnglish, langDrift, withLangGuard, LANG_RETRIES } from '../web/js/guard.js';

let fails = 0;
const ok = (cond, msg) => { if (!cond) { fails++; console.error('✗', msg); } else console.log('✓', msg); };

// 1. strip 和 Python 一致(期望值由 python3 str.strip() 生成)
const STRIP = [[" \n\t\u3000\u001c Hello world.\u00a0\n\n", "Hello world."], ["\ufeffx\ufeff", "\ufeffx\ufeff"], ["\u200bzero\u200b", "\u200bzero\u200b"], ["\u0085a\u001f", "a"], ["a\n\n b", "a\n\n b"], ["\u2028x\u2029", "x"], ["\u000bv\f", "v"]];
for (const [inp, want] of STRIP) ok(pyStrip(inp) === want, `pyStrip(${JSON.stringify(inp)})`);

// 2. 提示词指纹 = promptfmt.py 的 fingerprint()
const INSTR = 'Rewrite the text below so it reads like a person wrote it, not a language model.\n\nReorganize it as you see fit. Vary sentence length on purpose. Cut hedging,\nthroat-clearing, and any sentence that only announces what comes next.\nPrefer the concrete word over the abstract one. It is fine to sound uneven.\n\nEvery fact, number, unit, date, name and quotation must survive unchanged.';
const cfg = { instr: INSTR, sep: '\n\n### Rewritten:\n\n', n_predict: { factor: 2.5, min: 256, max: 2048 } };
const fp = createHash('sha256').update(buildPrompt(cfg, 'X')).digest('hex').slice(0, 16);
ok(fp === 'cc51d66b4c593fbe', `提示词指纹 ${fp} == promptfmt.py cc51d66b4c593fbe`);
ok(selfCheck({ ...cfg, probe: buildPrompt(cfg, 'X') }) && !selfCheck({ ...cfg, probe: 'nope' }), 'selfCheck 正反两面');
ok(!buildPrompt(cfg, 'a').includes('###\n') && buildPrompt(cfg, '  a  ').endsWith('\n\na\n\n### Rewritten:\n\n'), '草稿两端 strip、分隔符正确');

// 3. n_predict = clamp(ceil(n × 2.5), 256, 2048)
ok(nPredictFor(cfg, 10) === 256 && nPredictFor(cfg, 200) === 500 && nPredictFor(cfg, 101) === 256 && nPredictFor(cfg, 103) === 258 && nPredictFor(cfg, 5000) === 2048, 'n_predict 规则');

// 4. 字数
const c1 = countText('Hello, world! 你好世界');
ok(c1.latin === 2 && c1.cjk === 4 && c1.units === 6, `字数统计 ${JSON.stringify(c1)}`);

// 5. 差异:英文按词、中文按字
{
  const a = tokenize('The quick brown fox jumps over the lazy dog.');
  const b = tokenize('The quick red fox leaps over the lazy dog.');
  const d = diffTokens(a, b);
  const insB = b.filter((x) => !x.ws && d.b[x.ci]).map((x) => x.t);
  const delA = a.filter((x) => !x.ws && d.a[x.ci]).map((x) => x.t);
  ok(JSON.stringify(insB) === '["red","leaps"]' && JSON.stringify(delA) === '["brown","jumps"]', `英文词级差异 +${insB} -${delA}`);
}
{
  const a = tokenize('我今天去北京开会。');
  const b = tokenize('我明天去北京开会。');
  const d = diffTokens(a, b);
  const insB = b.filter((x) => !x.ws && d.b[x.ci]).map((x) => x.t).join('');
  ok(insB === '明', `中文字级差异 +${insB}`);
}
{
  // 语义清理:深度改写时不应留下孤零零的 "the"
  const a = tokenize('It was a great salary with smart colleagues and the free meals were nice.');
  const b = tokenize('Pay was huge, people were sharp, the food cost nothing.');
  const d = diffTokens(a, b);
  const kept = b.filter((x) => !x.ws && !d.b[x.ci]).map((x) => x.t);
  ok(kept.length <= 2, `深改写不留碎片,保留的 token: ${JSON.stringify(kept)}`);
}
{
  const same = tokenize('完全相同的一句话。');
  const d = diffTokens(same, tokenize('完全相同的一句话。'));
  ok(d.ratio === 0 && !d.a.some(Boolean), '相同文本无差异');
}

// 6. 数字核对
{
  const r = numberCheck('From $1,850 to 3,200 users on 6:40, 60 minutes, 2024.', 'From 1850 dollars to 3200 users at 6:40 — an hour — in 2024');
  ok(JSON.stringify(r.missing) === '["60"]' && r.total === 5, `数字核对 ${JSON.stringify(r)}`);
  const r2 = numberCheck('晚上8点到10点半', '晚上8点到10:30');
  ok(r2.missing.length === 0, '10 点半 → 10:30 不算丢');
}

// 7. 用真实夹具跑一遍差异,确认不炸、比例合理
const fx = JSON.parse(readFileSync(new URL('./fixtures/samples.json', import.meta.url), 'utf8'));
for (const s of fx.samples) {
  const d = diffTokens(tokenize(s.draft), tokenize(s.output));
  const r = numberCheck(s.draft, s.output);
  ok(d && d.ratio > 0.2 && d.ratio < 0.95, `${s.id} 改动比例 ${(d.ratio * 100).toFixed(0)}%,数字缺 ${JSON.stringify(r.missing)}`);
}

// 8. 语言保险:英文草稿写成中文 → 重采,最多 3 次;反方向只在写完后判
{
  const EN = 'The current humanizer still carries some AI style (you can probably still feel it), and it can\'t get past the most accurate AI detector, Pangram. In my internal research, though, a brand-new method can actually teach a model to write like a human, not just rewrite, and it already passes Pangram v4 as a side proof.';
  const ZH_OUT = '现在的 humanizer 还是带点 AI 味（你大概还能感觉出来），也过不了目前最准的检测器 Pangram。不过我内部研究里有个全新的方法，能真正教模型像人一样写。';
  const EN_OUT = 'Honestly, the humanizer still reads a bit like AI, and Pangram, the sharpest detector out there, still catches it. But a new method I have been testing teaches the model to write, not just rewrite.';
  const ZH = '我们在2024年第3季度完成了1,250个工单的迁移，团队一共用了6周时间，比原计划提前了两周，客户满意度也有明显提升。';
  ok(hanCount(ZH_OUT) > 15 && hanCount(EN) === 0, `汉字计数 ${hanCount(ZH_OUT)}`);
  ok(wentChinese(EN, ZH_OUT) && !wentChinese(EN, EN_OUT), '英文草稿:中文输出算跑偏,英文输出不算');
  ok(!wentChinese(EN + ' 中文字', '这是一句带了二十多个汉字的中文输出，用来确认草稿里有三个汉字时不触发。') , '草稿 ≤2 个汉字才启用(3 个汉字不启用)');
  ok(!wentChinese(EN, EN_OUT + ' 这里夹了几个汉字'), '英文输出夹几个汉字(≤15)不算跑偏');
  ok(!wentChinese(ZH, ZH_OUT), '中文草稿写中文不算跑偏');
  ok(wentEnglish(ZH, EN_OUT) && !wentEnglish(ZH, '我们2024年Q3迁移了1,250个工单，6周干完，提前两周。') && !wentEnglish(EN, EN_OUT), '中文草稿写成英文才算');
  ok(!langDrift(ZH, 'Q3 migration of 1,250 tickets finished in six weeks, two weeks early, and customers were noticeably happier', false)
     && langDrift(ZH, 'Q3 migration of 1,250 tickets finished in six weeks, two weeks early, and customers were noticeably happier', true), '中→英只在写完后判,流式途中不判');

  const run = async (draft, outs) => {
    const calls = [];
    const r = await withLangGuard(draft, async (i, isLast) => { calls.push(isLast); return { text: outs[Math.min(i, outs.length - 1)] }; });
    return { r, calls };
  };
  let x = await run(EN, [ZH_OUT, ZH_OUT, EN_OUT]);
  ok(x.r.text === EN_OUT && x.r.tries === 3 && x.calls.join() === 'false,false,false', `跑偏两次后第 3 次英文 → 交英文(共 ${x.r.tries} 次)`);
  x = await run(EN, [ZH_OUT]);
  ok(LANG_RETRIES === 3 && x.r.tries === 4 && x.calls.join() === 'false,false,false,true' && x.r.text === ZH_OUT, '一直跑偏:重采 3 次后交最后一次,最后一次标 isLast');
  x = await run(EN, [EN_OUT]);
  ok(x.r.tries === 1, '正常输出只跑 1 次');
  x = await run(ZH, [ZH_OUT]);
  ok(x.r.tries === 1, '中文草稿中文输出只跑 1 次');
  const r2 = await withLangGuard(EN, async (i) => ({ text: i === 0 ? '半截' : EN_OUT, drifted: i === 0 }));
  ok(r2.tries === 2 && r2.text === EN_OUT, '流式途中掐掉的那次(drifted)也会重采');
  let threw = false;
  try { await withLangGuard(EN, async () => { throw Object.assign(new Error('stop'), { name: 'AbortError' }); }); } catch (e) { threw = e.name === 'AbortError'; }
  ok(threw, '用户停止(AbortError)直接抛出,不重采');
}

// 9. 长文档提示:约 1,000 token(≈ 700 个英文词 / 1,100 个汉字)起显示
{
  const words = (n) => Array.from({ length: n }, () => 'word').join(' ');
  ok(isLongDraft(words(700)) && !isLongDraft(words(400)), `长文档提示:英文 700 词显示、400 词不显示(估 ${estimateTokens(words(700))} token)`);
  ok(isLongDraft('汉'.repeat(1100)) && !isLongDraft('汉'.repeat(900)), '长文档提示:1,100 个汉字显示、900 个不显示');
  ok(!isLongDraft(''), '空草稿不显示');
}

// Literal facts preserve overlaps, repetitions, Unicode, and reject invalid output.
{
  const draft = 'Nadia approved 42 files. Nadia approved 42 files. 你好世界';
  const facts = ['Nadia approved', 'approved 42 files', '你好世界'];
  const locked = protectFacts(draft, facts);
  ok(locked.locks.length === 3, 'Overlapping facts merge; repeated occurrences get individual locks');
  ok(restoreFacts(locked.text, locked) === draft, 'Locks round-trip Unicode and all occurrences');
  const output = restoreFacts(locked.text.replace('. ', '! '), locked);
  ok(facts.every((f) => output.includes(f)) && output !== draft, 'Prose changes while literal facts survive');
  const collision = protectFacts('HZ_LOCK_ literal and 42', ['42']);
  ok(collision.prefix !== 'HZ_LOCK_' && restoreFacts(collision.text, collision) === 'HZ_LOCK_ literal and 42', 'Token namespace avoids collisions');
  ok(activeFacts(['42', '42', 'gone', '', null], '42 files').join() === '42', 'Deduplicate facts and remove stale or invalid entries');
  ok(activeFacts({}, '42').length === 0, 'Invalid stored facts are ignored');
  const many = Array.from({length:60}, (_,i)=>String(i));
  ok(activeFacts(many, many.join(' ')).length === 50, 'Fact list bounded at 50');
  for (const bad of [locked.text.replace(locked.locks[0].token, ''), locked.text + locked.locks[0].token, locked.text.replace(locked.locks[0].token, '[[HZ_LOCK_changed]]')]) {
    let failed = false;
    try { restoreFacts(bad, locked); } catch (e) { failed = e.message === 'factsLost'; }
    ok(failed, 'Reject missing, duplicated, or mutated locks');
  }
  ok(restoreFacts('ordinary output', protectFacts('ordinary draft', [])) === 'ordinary output', 'No-fact rewrites keep existing behavior');
  ok(buildPrompt(cfg, locked.text).startsWith(INSTR + '\n\n'), 'Canonical model instructions remain unchanged');
}

// 10. Create fact:真模型的写法(括号层数变了)也认;流式显示;自动重试和作废
{
  const p = protectFacts('Charlotte grows from 10,000 to 20,000 sq ft. Call Nadia.', ['20,000 sq ft', 'Nadia']);
  ok(restoreFacts('Charlotte: [HZ_LOCK_0] SF. Ping HZ_LOCK_1 today.', p) === 'Charlotte: 20,000 sq ft SF. Ping Nadia today.', '括号少一层、没有括号也换回原文,不吃掉前面的空格');
  let threw = false;
  try { restoreFacts('[HZ_LOCK_0] and [[HZ_LOCK_0]], [[HZ_LOCK_1]]', p); } catch (e) { threw = e.message === 'factsLost'; }
  ok(threw, '同一个占位符出现两次(写法不同也算)判失败');
  ok(streamView('to [[HZ_LOCK_0]] and [[HZ_LO', p) === 'to 20,000 sq ft and ' && streamView('ping HZ_', p) === 'ping ', '流式途中:写完的占位符换回原文,写到一半的先不显示');
  const good = { raw: 'Grows to [[HZ_LOCK_0]]. Call [[HZ_LOCK_1]].', final: { stop_type: 'eos' } };
  const lost = { raw: 'Grows a lot. Call [[HZ_LOCK_1]].', final: { stop_type: 'eos' } };
  const r1 = await withFactGuard(p, async () => good);
  ok(r1.ok && r1.tries === 1 && r1.text === 'Grows to 20,000 sq ft. Call Nadia.', '首发通过:只跑 1 次');
  const seen = [];
  const r2 = await withFactGuard(p, async (k) => (k === 1 ? lost : good), FACT_TRIES, (n) => seen.push(n));
  ok(r2.ok && r2.tries === 2 && seen.join() === '2', '第 1 次丢了占位符 → 自动重写,第 2 次通过');
  const r3 = await withFactGuard(p, async () => lost);
  ok(!r3.ok && r3.tries === FACT_TRIES && r3.text === '', `${FACT_TRIES} 次都丢 → 作废,不交出丢了原文的结果`);
  const r4 = await withFactGuard(p, async (k) => (k === 1 ? { ...good, final: { stop_type: 'limit' } } : good));
  ok(r4.ok && r4.tries === 2, '被截断(stop_type=limit)也算没保住,重写');
  const r5 = await withFactGuard(protectFacts('no facts here', []), async () => ({ raw: 'anything', final: null }));
  ok(r5.ok && r5.tries === 1 && r5.text === 'anything', '没有保护段:原样返回');
  let stopped = false;
  try { await withFactGuard(p, async () => { throw Object.assign(new Error('stop'), { name: 'AbortError' }); }); } catch (e) { stopped = e.name === 'AbortError'; }
  ok(stopped, '用户停止直接抛出,不重试');
}

// Long-document planning and durable, section-level commits.
{
  const dc = {...cfg, ctx_size:4096};
  const count = async (s) => Math.ceil([...s].length / 4);
  const source = '  '+Array.from({length:12}, (_,i)=>`Section ${i}: ` + 'Nadia approved 42 files. '.repeat(70)).join('\r\n\r\n')+'  ';
  const fact = 'Nadia approved 42 files.';
  const parts = await planDocument(source,[fact],dc,count);
  ok(parts.length > 1 && parts.map(p=>p.before+p.draft+p.after).join('')===source, 'Document plan preserves source and paragraph separators exactly');
  ok(parts.every(p=>!p.draft.includes('HZ_LOCK_')), 'Plan contains original passages rather than placeholders');
  for (const p of parts) {
    const masked=protectFacts(p.draft,[fact]).text, tokens=await count(masked);
    ok(tokens<=700 && await count(buildPrompt(dc,masked))+nPredictFor(dc,tokens)+64<=dc.ctx_size, 'Every section fits actual input and output token budgets');
  }
  const unicode='你好。😀'.repeat(2400);
  const up = await planDocument(unicode,[],dc,count);
  ok(up.length>1 && up.map(p=>p.before+p.draft+p.after).join('')===unicode && up.every(p=>!/[\uD800-\uDBFF]$/.test(p.draft)), 'Unbroken Unicode text splits without losing characters or surrogate pairs');
  const spanning='One fact\n\ncontinues across paragraphs';
  const sp=await planDocument('Intro. '+spanning+' '+'more words '.repeat(1400),[spanning],dc,count);
  ok(sp.some(p=>p.draft.includes(spanning)), 'A fact spanning paragraphs stays inside one section');
  const padded=' '.repeat(3500)+'Body.';
  const pp=await planDocument(padded,[],dc,count);
  ok(pp.map(p=>p.before+p.draft+p.after).join('')===padded, 'Large leading whitespace survives planning');
  let rejected=false;try { await planDocument('Text',[],{...dc,ctx_size:10},count); } catch(e) {rejected=e.message==='documentContext';}
  ok(rejected,'Context too small fails instead of silently producing an oversized section');
  const sig=documentSignature(dc,'q8');
  const job=newDocumentJob(source,[fact],sig,parts);
  let calls=0, saved=null;
  try {await runDocumentSections(job,async(s)=>{calls++;if(calls===2)throw new Error('connection failed');return s;},{onCommit:j=>{saved=JSON.stringify(j);}});} catch {}
  ok(job.outputs.length===1 && !!saved, 'Failure commits only completed sections');
  const loaded=readDocumentJob(saved);
  ok(!!loaded && jobMatches(loaded,source,[fact],sig) && !jobMatches(loaded,source+'changed',[fact],sig) && !jobMatches(loaded,source,[],sig) && !jobMatches(loaded,source,[fact],documentSignature(dc,'q4')), 'Resume requires matching draft, facts, and model settings');
  const before=calls;
  await runDocumentSections(loaded,async(s)=>{calls++;return s;});
  ok(calls-before===parts.length-1 && combinedDocument(loaded)===source,'Resume skips completed sections and combines the whole document');
  const bad=JSON.parse(saved);bad.outputs[0]='fact missing';
  ok(readDocumentJob(JSON.stringify(bad))===null && readDocumentJob('{broken')===null,'Corrupt checkpoints or missing saved facts are rejected');
  const canceled=newDocumentJob(source,[fact],sig,parts), ctl=new AbortController();
  try {await runDocumentSections(canceled,async(s)=>{ctl.abort();return s;},{signal:ctl.signal});} catch {}
  ok(canceled.outputs.length===0,'Canceled section is never committed');
  const lost=newDocumentJob(source,[fact],sig,parts);
  try {await runDocumentSections(lost,async()=> 'No protected phrase');} catch {}
  ok(lost.outputs.length===0,'A section losing a fact cannot enter the combined result');
}

if (fails) { console.error(`\n${fails} 项失败`); process.exit(1); }
console.log('\n全部通过');
