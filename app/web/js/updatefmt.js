// 更新面板用的纯函数:从 GitHub Release 说明里挑出「改了什么」、把一小撮 Markdown 安全地转成 HTML。
// 不依赖 DOM,devtools/webtest_update.mjs 直接测。
import { escapeHTML } from './text.js';

/**
 * 只取「这一版改了什么」那一节:
 *   中文界面优先找「**0.3.0 改了什么**」/「## …改了什么」,没有就退回英文;
 *   英文找「## What changed…」;都没有就给全文(去掉 --- 之后的另一种语言)。
 */
export function pickNotes(body, lang) {
  const text = String(body || '').replace(/\r\n?/g, '\n');
  if (lang === 'zh') {
    const m = text.match(/(?:^|\n)(?:#{2,}\s*|\*\*)[^\n]*改了什么[^\n]*\n([\s\S]*?)(?=\n[ \t]*\n\*\*[^\n]*?\*\*|\n#{2,}\s|\n---|$)/);
    if (m && m[1].trim()) return m[1].trim();
  }
  const e = text.match(/(?:^|\n)#{2,}\s*What(?:'|’)?s? changed[^\n]*\n([\s\S]*?)(?=\n#{2,}\s|\n---|$)/i);
  if (e && e[1].trim()) return e[1].trim();
  return text.split(/\n---\n/)[0].trim();
}

const LINK_RE = /\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g;

/** 行内:先整体转义,再认 `code`、**粗体**、[文字](https 链接)。 */
export function inlineMD(s) {
  let h = escapeHTML(s);
  const codes = [];
  h = h.replace(/`([^`\n]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000`; });
  h = h.replace(/\*\*([^*\n]+?)\*\*/g, '<strong>$1</strong>');
  h = h.replace(LINK_RE, (_, txt, url) => `<a href="${url}" target="_blank" rel="noopener noreferrer">${txt}</a>`);
  h = h.replace(/\u0000(\d+)\u0000/g, (_, i) => `<code>${codes[+i]}</code>`);
  return h;
}

const cells = (line) => line.trim().replace(/^\||\|$/g, '').split('|').map((c) => c.trim());

/**
 * 很小的 Markdown 子集 → HTML:标题(## → h4)、无序列表(含缩进续行)、表格、段落。
 * 任何原始 HTML 都被转义,链接只认 http/https。
 */
export function miniMD(md) {
  const lines = String(md || '').replace(/\r\n?/g, '\n').split('\n');
  const out = [];
  let para = [], list = null, table = null;
  // list: [{ parts: [段落文字…], cont: 下一行缩进文字是否接在最后一段后面 }]
  const flushPara = () => { if (para.length) { out.push(`<p>${inlineMD(para.join(' '))}</p>`); para = []; } };
  const flushList = () => {
    if (!list) return;
    out.push('<ul>' + list.map((it) => '<li>' + it.parts.map((p, i) => (i ? `<p>${inlineMD(p)}</p>` : inlineMD(p))).join('') + '</li>').join('') + '</ul>');
    list = null;
  };
  const flushTable = () => {
    if (table && table.length) {
      const [head, ...rows] = table;
      out.push('<div class="md-table"><table><thead><tr>' + head.map((c) => `<th>${inlineMD(c)}</th>`).join('') + '</tr></thead><tbody>' +
        rows.map((r) => '<tr>' + r.map((c) => `<td>${inlineMD(c)}</td>`).join('') + '</tr>').join('') + '</tbody></table></div>');
    }
    table = null;
  };

  for (const raw of lines) {
    const line = raw.replace(/\s+$/, '');
    let m;
    if (!line.trim()) { // 空行:结束段落和表格;列表里的空行意味着下一段缩进文字另起一段
      flushPara(); flushTable();
      if (list) list[list.length - 1].cont = false;
      continue;
    }
    if ((m = line.match(/^#{1,6}\s+(.*)$/))) { flushPara(); flushList(); flushTable(); out.push(`<h4>${inlineMD(m[1])}</h4>`); continue; }
    if (/^\s*\|/.test(line)) {
      flushPara(); flushList();
      if (/^[\s|:-]+$/.test(line)) continue; // |---|:--:| 分隔行
      (table ||= []).push(cells(line));
      continue;
    }
    if ((m = line.match(/^ ?[-*]\s+(.*)$/))) { flushPara(); flushTable(); (list ||= []).push({ parts: [m[1]], cont: true }); continue; }
    if (list && /^\s{2,}\S/.test(line)) { // 列表项下的缩进续行
      const it = list[list.length - 1];
      if (it.cont) it.parts[it.parts.length - 1] += ' ' + line.trim();
      else { it.parts.push(line.trim()); it.cont = true; }
      continue;
    }
    flushList(); flushTable();
    para.push(line.trim());
  }
  flushPara(); flushList(); flushTable();
  return out.join('');
}

/** 「3 分钟前」之类;超过一天给日期。 */
export function relTime(ts, now, lang) {
  const t = Date.parse(ts);
  if (!t || t < 86400000) return null;
  const s = Math.max(0, (now - t) / 1000);
  if (s < 60) return lang === 'zh' ? '刚刚' : 'just now';
  if (s < 3600) { const n = Math.floor(s / 60); return lang === 'zh' ? `${n} 分钟前` : `${n} min ago`; }
  if (s < 86400) { const n = Math.floor(s / 3600); return lang === 'zh' ? `${n} 小时前` : `${n} h ago`; }
  return new Date(t).toLocaleDateString(lang === 'zh' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric' });
}
