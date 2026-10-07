// 更新面板纯逻辑的单测(不需要浏览器):node devtools/webtest_update.mjs 或 bun devtools/webtest_update.mjs
import { pickNotes, miniMD, inlineMD, relTime } from '../web/js/updatefmt.js';

let fails = 0;
const ok = (cond, msg) => { if (!cond) { fails++; console.error('✗', msg); } else console.log('✓', msg); };

// 1. 取「改了什么」:用的是 GitHub 上 app-v0.3.0 / app-v0.3.1 真实说明的结构
const BODY_031 = '**humanizer** rewrites AI-written drafts.\n\n## What changed in 0.3.1\n\n- **Fixed:** an English draft occasionally came back rewritten in Chinese.\n\n## Download\n\n| Your computer | File |\n|---|---|\n| Mac | `Humanizer-0.3.1-macos-arm64.dmg` |\n';
const BODY_030 = '**humanizer** rewrites.\n\n## What changed in 0.3.0\n\n- **Two new, smaller sizes: Q3 (5.6 GB) and 2-bit (3.9 GB).**\n- **Every size shows a quality note**:\n\n| Size | Download |\n|---|---|\n| Q8_0 | 12.7 GB |\n\n  Memory is the model server\'s peak.\n- **Changing sizes:** open the menu.\n\n## Download\n\nstuff\n\n---\n\n**humanizer** 把草稿改写。\n\n**0.3.0 改了什么**\n- **新增两个更小的档位**，8 GB 也能跑。\n- **换档**：右上角菜单。\n\n**下载**：Mac 用 `.dmg`。\n';
const en031 = pickNotes(BODY_031, 'en');
ok(en031.startsWith('- **Fixed:**') && !en031.includes('Download'), '英文:只取 What changed 一节');
ok(pickNotes(BODY_031, 'zh') === en031, '没有中文说明时中文界面退回英文');
const zh030 = pickNotes(BODY_030, 'zh');
ok(zh030.startsWith('- **新增两个更小的档位**') && zh030.includes('换档') && !zh030.includes('下载'), `中文:取「改了什么」到「下载」之前 → ${JSON.stringify(zh030)}`);
const en030 = pickNotes(BODY_030, 'en');
ok(en030.includes('Changing sizes') && !en030.includes('## Download') && !en030.includes('改了什么'), '英文 0.3.0:列表 + 表格 + 续行都在,下一节和中文部分不在');
ok(pickNotes('Just a plain note.\n\n---\n\n中文', 'en') === 'Just a plain note.', '没有小标题:给 --- 之前的全文');

// 2. 迷你 Markdown
const h = miniMD(en030);
ok((h.match(/<ul>/g) || []).length === 2 && h.includes('<table>') && h.includes('<th>Size</th>') && h.includes('<td>12.7 GB</td>'), '列表、表格都渲染出来');
ok(!h.includes('|---|') && !h.includes('---'), '表格分隔行不出现在结果里');
ok(h.includes('<strong>Two new, smaller sizes: Q3 (5.6 GB) and 2-bit (3.9 GB).</strong>'), '粗体');
ok(h.includes("<p>Memory is the model server&#39;s peak.</p>"), '缩进续行成段落(并转义引号)');
const evil = miniMD('- <img src=x onerror=alert(1)> **b** [x](javascript:alert(1)) [ok](https://github.com/a?b=1&c=2) `<script>`');
ok(!evil.includes('<img') && !evil.includes('<script>') && evil.includes('&lt;img'), '原始 HTML 被转义');
ok(!evil.includes('href="javascript'), '只认 http/https 链接');
ok(evil.includes('href="https://github.com/a?b=1&amp;c=2" target="_blank" rel="noopener noreferrer">ok</a>'), 'https 链接新标签页打开、带 noopener');
ok(evil.includes('<code>&lt;script&gt;</code>'), '行内代码里也转义');
ok(inlineMD('`**not bold**`') === '<code>**not bold**</code>', '代码里的星号不当粗体');
ok(miniMD('## Title\ntext one\ntext two') === '<h4>Title</h4><p>text one text two</p>', '标题 + 段落合并');
ok(miniMD('- a\n\n  para\n- b') === '<ul><li>a<p>para</p></li><li>b</li></ul>', '列表项里的空行 + 缩进 = 另起一段');

// 3. 相对时间
const now = Date.parse('2026-10-07T12:00:00Z');
ok(relTime('2026-10-07T11:59:40Z', now, 'zh') === '刚刚' && relTime('2026-10-07T11:50:00Z', now, 'en') === '10 min ago', '分钟级');
ok(relTime('2026-10-07T09:00:00Z', now, 'zh') === '3 小时前', '小时级');
ok(relTime('0001-01-01T00:00:00Z', now, 'zh') === null && relTime('', now, 'en') === null, '没检查过返回 null');

if (fails) { console.error(`\n${fails} 项失败`); process.exit(1); }
console.log('\n全部通过');
