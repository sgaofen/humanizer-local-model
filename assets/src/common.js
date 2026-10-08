// 公共小工具:读 URL 参数、App 的波浪标志、等字体加载完后把页面高度写到 <body data-h>,
// render.sh 用 --dump-dom 读这个高度再按实际高度截图。

export const params = new URLSearchParams(location.search);
export const lang = params.get('lang') === 'zh' ? 'zh' : 'en';
document.documentElement.lang = lang === 'zh' ? 'zh-CN' : 'en';
export const theme = params.get('theme') === 'light' ? 'light' : 'dark';
document.documentElement.dataset.theme = theme;

// App 顶栏的标志:一条直线变成手写波浪(机器 → 人)。viewBox 0 0 40 24
export const MARK_PATH = 'M2 12 H15 C19 12 19.5 4.5 23.5 4.5 C27.5 4.5 26 19.5 30.5 19.5 C34.5 19.5 33.5 9 38 9';
export const markSvg = (sw = 3) =>
  `<svg viewBox="0 0 40 24" aria-hidden="true"><path d="${MARK_PATH}" fill="none" stroke="currentColor" stroke-width="${sw}" stroke-linecap="round" stroke-linejoin="round"/></svg>`;

export const esc = (s) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]));

export async function done() {
  await document.fonts.ready;
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  const h = Math.ceil(document.querySelector('main').getBoundingClientRect().height);
  document.body.dataset.h = String(h);
}
