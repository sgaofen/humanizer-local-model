#!/usr/bin/env python3
"""重新生成 assets/ 下的全部配图(只用 python3 标准库 + 本机 Google Chrome)。

    python3 assets/src/render.py              # 全部
    python3 assets/src/render.py compare      # 只出文件名里带 compare 的
    python3 assets/src/render.py --preview    # 只出 README 整页预览(assets/preview/),需要 markdown-it-py

做法:
  1. 在仓库根起一个只听 127.0.0.1 的静态文件服务(ES module 和字体不能走 file://);
  2. 起一个无头 Chrome,用 DevTools 协议(这里手写了一个最小 WebSocket 客户端)逐页打开;
  3. 等页面把实际高度写进 <body data-h>,按这个高度、2 倍像素比截图。
新版无头 Chrome 的 --dump-dom/--screenshot 不等 module 里的异步代码、而且截完不退出,所以不用那两个开关。
全程一个 Chrome 进程、串行出图,出完就关。
"""
import base64, functools, http.server, json, os, socket, struct, subprocess, sys, tempfile, threading, time, urllib.parse, urllib.request

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
OUT = os.path.join(ROOT, 'assets')
CHROME = os.environ.get('CHROME', '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome')

SHOTS = [  # (页面?参数, 输出文件(相对 assets/), CSS 宽度)。全部是浅色"纸面"卡片(?theme=light),外圈圆角透明。
    # 量化卡片要先跑 `python3 assets/src/quant_chart.py --plot` 生成 _quant-plot-*.svg。
    # 旧的深色版(含 results-detector / results-fidelity 两张)存档在 assets/data/archive/,detector.html、fidelity.html 去掉 theme 参数还能出深色版。
    ('banner.html?lang=en&theme=light', 'banner-en.png', 1280),
    ('banner.html?lang=zh&theme=light', 'banner-zh.png', 1280),
    ('compare.html?id=en-email&theme=light', 'compare-en-email.png', 1080),
    ('compare.html?id=en-forum&theme=light', 'compare-en-forum.png', 1080),
    ('compare.html?id=zh-email&theme=light', 'compare-zh-email.png', 1080),
    ('compare.html?id=zh-zhihu&theme=light', 'compare-zh-zhihu.png', 1080),
    ('eval.html?lang=en&theme=light', 'eval-en.png', 1080),          # 评测卡:AI 检测 + 事实忠实度
    ('eval.html?lang=zh&theme=light', 'eval-zh.png', 1080),
    ('quant.html?lang=en&theme=light', 'quant-top1-en.png', 1080),   # 量化卡片
    ('quant.html?lang=zh&theme=light', 'quant-top1-zh.png', 1080),
    ('training.html?lang=en&theme=light', 'training-en.png', 1080),
    ('training.html?lang=zh&theme=light', 'training-zh.png', 1080),
    ('showcase.html?lang=en&theme=light', 'app-showcase-en.png', 1280),   # README / HF 卡片顶部的 App 主展示(真机截图,外框是纸面)
    ('showcase.html?lang=zh&theme=light', 'app-showcase-zh.png', 1280),
    ('app.html?lang=en&shot=editor-en-light&theme=light', 'app-en.png', 1080),   # 快速开始里的单张截图
    ('app.html?lang=zh&shot=editor-en-light&theme=light', 'app-zh.png', 1080),
]
PREVIEW = [  # 先由 build_preview() 把 README 渲染成仿 GitHub 样式的 HTML,再按 1 倍像素比截整页
    ('_preview-en.html', 'preview/readme-en.png', 1012),
    ('_preview-zh.html', 'preview/readme-zh.png', 1012),
]
PREVIEW_SRC = {'_preview-en.html': 'README.md', '_preview-zh.html': 'README.zh.md'}

PREVIEW_CSS = '''
body { margin: 0; background: #fff; color: #1f2328; font: 16px/1.5 -apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans", Helvetica, Arial, "PingFang SC", sans-serif; }
main { width: 1012px; padding: 24px 0 40px; }
.box { margin: 0 16px; border: 1px solid #d0d7de; border-radius: 6px; }
.box .hd { display: flex; align-items: center; gap: 8px; padding: 8px 16px; border-bottom: 1px solid #d0d7de; font-size: 14px; font-weight: 600; background: #f6f8fa; border-radius: 6px 6px 0 0; }
.box .hd svg { width: 16px; height: 16px; fill: #59636e; }
article { padding: 32px; overflow-wrap: break-word; }
article > *:first-child { margin-top: 0 !important; }
h1, h2, h3, h4 { margin: 24px 0 16px; font-weight: 600; line-height: 1.25; }
h1 { font-size: 2em; padding-bottom: .3em; border-bottom: 1px solid #d1d9e0b3; }
h2 { font-size: 1.5em; padding-bottom: .3em; border-bottom: 1px solid #d1d9e0b3; }
h3 { font-size: 1.25em; }
p, ul, ol, table, pre, blockquote, details { margin: 0 0 16px; }
ul, ol { padding-left: 2em; } li + li { margin-top: .25em; }
a { color: #0969da; text-decoration: none; }
img { max-width: 100%; box-sizing: content-box; }
code { font: 85%/1.45 ui-monospace, SFMono-Regular, Menlo, monospace; background: #818b981f; padding: .2em .4em; border-radius: 6px; }
pre { background: #f6f8fa; padding: 16px; border-radius: 6px; overflow: auto; font-size: 85%; line-height: 1.45; }
pre code { background: none; padding: 0; font-size: 100%; }
table { border-collapse: collapse; display: block; width: max-content; max-width: 100%; overflow: auto; }
th, td { border: 1px solid #d1d9e0; padding: 6px 13px; }
th { font-weight: 600; } tr:nth-child(2n) { background: #f6f8fa; }
blockquote { padding: 0 1em; color: #59636e; border-left: .25em solid #d1d9e0; }
.alert { padding: 8px 16px; margin-bottom: 16px; border-left: .25em solid #1a7f37; }
.alert .t { display: flex; align-items: center; gap: 8px; font-weight: 500; color: #1a7f37; margin-bottom: 4px; }
.alert p { margin: 0; }
details summary { cursor: pointer; }
hr { height: .25em; background: #d1d9e0; border: 0; margin: 24px 0; }
'''


def build_preview():
    """把 README.md / README.zh.md 渲染成仿 GitHub 的 HTML(只为截预览图,不是 GitHub 的真实渲染)。"""
    import html, re
    from markdown_it import MarkdownIt
    md = MarkdownIt('commonmark', {'html': True}).enable('table').enable('strikethrough')
    icon = '<svg viewBox="0 0 16 16"><path d="M0 1.75A.75.75 0 0 1 .75 1h4.253c1.227 0 2.317.59 3 1.501A3.743 3.743 0 0 1 11.006 1h4.245a.75.75 0 0 1 .75.75v10.5a.75.75 0 0 1-.75.75h-4.507a2.25 2.25 0 0 0-1.591.659l-.622.621a.75.75 0 0 1-1.06 0l-.622-.621A2.25 2.25 0 0 0 5.258 13H.75a.75.75 0 0 1-.75-.75Zm7.251 10.324.004-5.073-.002-2.253A2.25 2.25 0 0 0 5.003 2.5H1.5v9h3.757a3.75 3.75 0 0 1 1.994.574ZM8.755 4.75l-.004 7.322a3.752 3.752 0 0 1 1.992-.572H14.5v-9h-3.495a2.25 2.25 0 0 0-2.25 2.25Z"/></svg>'
    for out, src in PREVIEW_SRC.items():
        body = md.render(open(os.path.join(ROOT, src), encoding='utf-8').read())
        # GitHub 的提示块语法 > [!TIP]
        body = re.sub(r'<blockquote>\s*<p>\[!(TIP|NOTE)\]\s*', lambda m: f'<div class="alert"><div class="t">{m.group(1).title()}</div><p>', body)
        body = re.sub(r'(<div class="alert">.*?)</blockquote>', r'\1</div>', body, flags=re.S)
        page = (f'<!doctype html><html><head><meta charset="utf-8"><base href="/"><title>{html.escape(src)}</title>'
                f'<style>{PREVIEW_CSS}</style></head><body><main><div class="box"><div class="hd">{icon}{html.escape(src)}</div>'
                f'<article>{body}</article></div></main>'
                '<script>Promise.all([...document.images].map(i => i.complete ? 0 : new Promise(r => { i.onload = i.onerror = r; })))'
                '.then(() => document.fonts.ready).then(() => { document.body.dataset.h = Math.ceil(document.querySelector("main").getBoundingClientRect().height); });</script>'
                '</body></html>')
        open(os.path.join(ROOT, 'assets', 'src', out), 'w', encoding='utf-8').write(page)


class WS:
    """够和本机 Chrome 说话的最小 WebSocket 客户端(文本帧、客户端掩码、支持大帧和分片)。"""
    def __init__(self, url):
        u = urllib.parse.urlparse(url)
        self.s = socket.create_connection((u.hostname, u.port))
        key = base64.b64encode(os.urandom(16)).decode()
        self.s.sendall((f'GET {u.path} HTTP/1.1\r\nHost: {u.hostname}:{u.port}\r\nUpgrade: websocket\r\n'
                        f'Connection: Upgrade\r\nSec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n').encode())
        buf = b''
        while b'\r\n\r\n' not in buf:
            buf += self.s.recv(4096)
        head, rest = buf.split(b'\r\n\r\n', 1)
        if b' 101 ' not in head.split(b'\r\n')[0]:
            raise RuntimeError(head.decode(errors='replace'))
        self.buf = bytearray(rest)

    def _take(self, n):
        while len(self.buf) < n:
            chunk = self.s.recv(1 << 20)
            if not chunk:
                raise EOFError('websocket closed')
            self.buf += chunk
        out = bytes(self.buf[:n]); del self.buf[:n]
        return out

    def _frame(self, op, data):
        hdr = bytearray([0x80 | op]); n = len(data)
        if n < 126: hdr.append(0x80 | n)
        elif n < 65536: hdr.append(0x80 | 126); hdr += struct.pack('>H', n)
        else: hdr.append(0x80 | 127); hdr += struct.pack('>Q', n)
        mask = os.urandom(4); hdr += mask
        self.s.sendall(bytes(hdr) + bytes(b ^ mask[i & 3] for i, b in enumerate(data)))

    def send(self, text):
        self._frame(1, text.encode())

    def recv(self):
        msg = bytearray()
        while True:
            b1, b2 = self._take(2)
            n = b2 & 0x7f
            if n == 126: n = struct.unpack('>H', self._take(2))[0]
            elif n == 127: n = struct.unpack('>Q', self._take(8))[0]
            mask = self._take(4) if b2 & 0x80 else None
            data = self._take(n)
            if mask: data = bytes(b ^ mask[i & 3] for i, b in enumerate(data))
            op = b1 & 0x0f
            if op == 9: self._frame(10, data); continue      # ping → pong
            if op == 8: raise EOFError('websocket closed')
            msg += data
            if b1 & 0x80:
                return msg.decode()


class CDP:
    def __init__(self, ws):
        self.ws, self.n = ws, 0

    def call(self, method, **params):
        self.n += 1; mid = self.n
        self.ws.send(json.dumps({'id': mid, 'method': method, 'params': params}))
        while True:
            m = json.loads(self.ws.recv())
            if m.get('id') == mid:
                if 'error' in m: raise RuntimeError(f'{method}: {m["error"]}')
                return m.get('result', {})

    def eval(self, expr):
        r = self.call('Runtime.evaluate', expression=expr, returnByValue=True)
        return r.get('result', {}).get('value')


def serve():
    class Quiet(http.server.SimpleHTTPRequestHandler):
        def log_message(self, *a): pass
        def handle(self):
            try: super().handle()
            except (BrokenPipeError, ConnectionResetError): pass
    srv = http.server.ThreadingHTTPServer(('127.0.0.1', 0), functools.partial(Quiet, directory=ROOT))
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def main():
    args = sys.argv[1:]
    preview = '--preview' in args
    shots = PREVIEW if preview else SHOTS
    if preview: build_preview()
    only = [a for a in args if not a.startswith('--')]
    if only: shots = [s for s in shots if any(o in s[1] for o in only)]
    srv = serve(); port = srv.server_address[1]
    prof = tempfile.mkdtemp(prefix='hz-render-')
    chrome = subprocess.Popen([CHROME, '--headless=new', '--disable-gpu', '--hide-scrollbars', '--no-first-run',
                               '--no-default-browser-check', '--disable-background-networking', '--remote-debugging-port=0',
                               f'--user-data-dir={prof}', 'about:blank'], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    try:
        portfile = os.path.join(prof, 'DevToolsActivePort')
        for _ in range(200):
            if os.path.exists(portfile) and open(portfile).read().strip(): break
            time.sleep(0.05)
        dport = open(portfile).read().split()[0]
        targets = []
        for _ in range(100):
            targets = [t for t in json.load(urllib.request.urlopen(f'http://127.0.0.1:{dport}/json/list')) if t.get('type') == 'page']
            if targets: break
            time.sleep(0.05)
        cdp = CDP(WS(targets[0]['webSocketDebuggerUrl']))
        cdp.call('Page.enable'); cdp.call('Runtime.enable')
        # 浅色卡片的外圈圆角要透明,放在 GitHub 浅色、深色页面上都干净
        cdp.call('Emulation.setDefaultBackgroundColorOverride', color={'r': 0, 'g': 0, 'b': 0, 'a': 0})
        dsf = 1 if preview else 2   # 整页预览很高,用 1 倍免得超出 Chrome 的纹理上限
        for page, out, w in shots:
            cdp.call('Emulation.setDeviceMetricsOverride', width=w, height=1200, deviceScaleFactor=dsf, mobile=False)
            cdp.call('Page.navigate', url=f'http://127.0.0.1:{port}/assets/src/{page}')
            h = None
            for _ in range(300):
                time.sleep(0.1)
                h = cdp.eval('document.body && document.body.dataset.h')
                if h: break
            if not h:
                print('!! 等不到页面高度:', page, cdp.eval('String(window.__err || "")')); continue
            h = int(h)
            cdp.call('Emulation.setDeviceMetricsOverride', width=w, height=h, deviceScaleFactor=dsf, mobile=False)
            time.sleep(0.4)
            png = base64.b64decode(cdp.call('Page.captureScreenshot', format='png', clip={'x': 0, 'y': 0, 'width': w, 'height': h, 'scale': 1})['data'])
            path = os.path.join(OUT, out)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            open(path, 'wb').write(png)
            print(f'{out:28s} {w}x{h} css → {dsf * w}x{dsf * h} px  {len(png) // 1024} KB', flush=True)
    finally:
        chrome.terminate()
        try: chrome.wait(5)
        except subprocess.TimeoutExpired: chrome.kill()
        srv.shutdown()
        if preview:
            for f in PREVIEW_SRC: 
                try: os.remove(os.path.join(ROOT, 'assets', 'src', f))
                except FileNotFoundError: pass
        import shutil; shutil.rmtree(prof, ignore_errors=True)


if __name__ == '__main__':
    main()
