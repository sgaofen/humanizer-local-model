#!/usr/bin/env bash
# 「检查更新」界面截图。全程假 GitHub(fakegh)+ 假 HF(fakehf)+ 假引擎(fakellama),不跑真模型、不碰真网络。
#   bash devtools/update_shots.sh [输出目录]      # 默认 .cache/update-shots/
# 需要 macOS(造 .app 用 codesign)和 Google Chrome。
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ "$(uname)" = Darwin ] || { echo "只在 macOS 上跑"; exit 0; }
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2
# shellcheck source=update_lib.sh
source devtools/update_lib.sh

OUT=dist/upd
W="$APP/.cache/upd-shots"
S="${1:-$APP/.cache/update-shots}"
rm -rf "$W"; mkdir -p "$OUT" "$W/gh" "$S"
go build -ldflags "-X main.version=0.3.1" -o "$OUT/hz-0.3.1" .
go build -ldflags "-X main.version=0.3.2" -o "$OUT/hz-0.3.2" .
go build -o "$OUT/" ./devtools/fakellama ./devtools/fakehf ./devtools/fakegh ./devtools/shot

GH=127.0.0.1:9295 HF=127.0.0.1:9296 HF2=127.0.0.1:9297
A=47750 B=47751 C=47752
PIDS=()
cleanup() {
  for p in $A $B $C; do curl -s -m 2 -X POST -H 'X-Humanizer: 1' "http://127.0.0.1:$p/app/quit" >/dev/null 2>&1 || true; done
  sleep 1
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
}
trap cleanup EXIT
wait_http() { for _ in $(seq 1 80); do curl -s -m 1 "$1" >/dev/null 2>&1 && return 0; sleep 0.25; done; echo "等不到 $1" >&2; return 1; }
wait_upd() { # 端口 python条件
  for _ in $(seq 1 120); do
    if curl -s -m 2 "http://127.0.0.1:$1/app/update" | python3 -c "import json,sys; d=json.load(sys.stdin); sys.exit(0 if ($2) else 1)" 2>/dev/null; then return 0; fi
    sleep 0.25
  done
  echo "等不到 $1: $2" >&2; return 1
}
post() { local b="${3:-}"; [ -n "$b" ] || b='{}'; curl -s -m 5 -X POST -H 'X-Humanizer: 1' -H 'Content-Type: application/json' "http://127.0.0.1:$1$2" -d "$b" >/dev/null; }

# 假发布:0.3.2 的 dmg(16 MB,内容是假的,只用来演示下载进度;真正的替换流程见 update_e2e.sh)
fake_dmg "$W/gh/Humanizer-0.3.2-macos-arm64.dmg" 16
"$OUT/fakegh" -addr $GH -dir "$W/gh" -rate 1.2 > "$W/fakegh.log" 2>&1 & PIDS+=($!)
# 假 HF:Q4_K_M 在 HF 上「更新过」(内容换了一份);另起一个没改过的,用来生成本地的「旧」文件
"$OUT/fakehf" -addr $HF -size 48 -rate 2.5 -changed humanizer-12b-Q4_K_M.gguf > "$W/fakehf.log" 2>&1 & PIDS+=($!)
"$OUT/fakehf" -addr $HF2 -size 48 -rate 0 > "$W/fakehf2.log" 2>&1 & PIDS+=($!)
wait_http "http://$HF/x"; wait_http "http://$HF2/x"

mkmodels() { # 数据目录:Q8_0 和 HF 上一样(已是最新),Q4_K_M 是旧的(有新版本)
  mkdir -p "$1/models"
  curl -sL "http://$HF2/jialinyyzz/humanizer/resolve/main/humanizer-12b-Q8_0.gguf" -o "$1/models/humanizer-12b-Q8_0.gguf"
  curl -sL "http://$HF2/jialinyyzz/humanizer/resolve/main/humanizer-12b-Q4_K_M.gguf" -o "$1/models/humanizer-12b-Q4_K_M.gguf"
  echo '{"tier":"q8"}' > "$1/settings.json"
}

export FAKE_FIXTURES="$APP/devtools/fixtures/samples.json" FAKE_LOAD_MS=300 FAKE_TOKEN_MS=4 HUMANIZER_UPDATE_DELAY_SEC=2

# A:从 .app 运行的 0.3.1(能原地更新),有 App 新版本和一个模型新版本
make_bundle "$W/Applications/Humanizer.app" 0.3.1 "$OUT/hz-0.3.1" "$OUT/fakellama"
mkmodels "$W/a"
"$W/Applications/Humanizer.app/Contents/MacOS/Humanizer" --data-dir "$W/a" --base-url "http://$HF" --update-api "http://$GH" --no-browser --port $A --idle-exit 0

# B:已经是 0.3.2,模型也都是最新
mkdir -p "$W/b/models"; cp "$W/a/models/humanizer-12b-Q8_0.gguf" "$W/b/models/"; echo '{"tier":"q8"}' > "$W/b/settings.json"
"$OUT/hz-0.3.2" --data-dir "$W/b" --engine "metal=$APP/$OUT/fakellama" --base-url "http://$HF" --update-api "http://$GH" --no-browser --port $B --idle-exit 0 > "$W/b.log" 2>&1 & PIDS+=($!)

# C:刚刚更新失败、自动换回了旧版本
mkmodels "$W/c"
cat > "$W/c/update-result.json" <<EOF
{"ok": false, "from": "0.3.1", "to": "0.3.2", "error": "新版本 0.3.2 没能启动,已换回 0.3.1", "at": "$(date -u +%Y-%m-%dT%H:%M:%SZ)"}
EOF
"$W/Applications/Humanizer.app/Contents/MacOS/Humanizer" --data-dir "$W/c" --base-url "http://$HF" --update-api "http://$GH" --no-browser --port $C --idle-exit 0

wait_upd $A 'd["app"]["available"] and not d["checking"] and [m["state"] for m in d["models"]]==["current","available"]'
wait_upd $B 'd["last_ok"][:4]!="0001" and not d["checking"]'
wait_upd $C 'd["app"]["available"] and not d["checking"]'

UA=http://127.0.0.1:$A UB=http://127.0.0.1:$B UC=http://127.0.0.1:$C
PANEL="!document.getElementById('upd').hidden && !!document.querySelector('#upd-app .upd-ver') && document.querySelectorAll('.um').length===2 && !document.querySelector('.um .spin')"
CHIP="!document.getElementById('upd-chip').hidden && !document.getElementById('view-editor').hidden"
cat > "$W/spec1.json" <<EOF
[
 {"url": "$UA/?lang=zh&theme=light&example=zh-social", "out": "$S/01-hint-zh-light.png", "scale": 1.5, "wait": "$CHIP", "settle_ms": 600},
 {"url": "$UA/?lang=en&theme=dark&example=en-social", "out": "$S/02-hint-en-dark.png", "scale": 1.5, "wait": "$CHIP", "settle_ms": 600},
 {"url": "$UA/?lang=zh&theme=light", "out": "$S/03-menu-zh-light.png", "scale": 1.5, "wait": "$CHIP && !document.getElementById('pa-update-badge').hidden",
  "after": "document.getElementById('btn-menu').click()", "settle_ms": 500},
 {"url": "$UA/?lang=zh&theme=light&panel=update", "out": "$S/04-panel-zh-light.png", "scale": 1.5, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=en&theme=dark&panel=update", "out": "$S/05-panel-en-dark.png", "scale": 1.5, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=en&theme=light&panel=update", "out": "$S/06-panel-en-light.png", "scale": 1.5, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=zh&theme=dark&panel=update", "out": "$S/07-panel-zh-dark.png", "scale": 1.5, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=zh&theme=light&panel=update", "out": "$S/08-mobile-panel-zh-light.png", "width": 390, "height": 844, "scale": 2, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=en&theme=dark&panel=update", "out": "$S/09-mobile-panel-en-dark.png", "width": 390, "height": 844, "scale": 2, "wait": "$PANEL", "settle_ms": 700},
 {"url": "$UA/?lang=en&theme=light", "out": "$S/10-mobile-bar-en-light.png", "width": 390, "height": 844, "scale": 2, "wait": "document.getElementById('btn-menu').classList.contains('has-upd') && !document.getElementById('view-editor').hidden",
  "after": "document.getElementById('btn-menu').click()", "settle_ms": 500},
 {"url": "$UB/?lang=zh&theme=light&panel=update", "out": "$S/11-latest-zh-light.png", "scale": 1.5, "wait": "!!document.querySelector('#upd-app .upd-state.ok') && !!document.querySelector('.um .um-state.ok')", "settle_ms": 600},
 {"url": "$UB/?lang=en&theme=dark&panel=update", "out": "$S/12-latest-en-dark.png", "scale": 1.5, "wait": "!!document.querySelector('#upd-app .upd-state.ok')", "settle_ms": 600},
 {"url": "$UC/?lang=en&theme=light", "out": "$S/13-rolledback-en-light.png", "scale": 1.5, "wait": "!document.getElementById('upd').hidden && !!document.querySelector('.upd-notice.err')", "settle_ms": 700},
 {"url": "$UC/?lang=zh&theme=dark", "out": "$S/14-rolledback-zh-dark.png", "scale": 1.5, "wait": "!document.getElementById('upd').hidden && !!document.querySelector('.upd-notice.err')", "settle_ms": 700},
 {"url": "$UA/?lang=zh&theme=light", "out": "$S/15-restarting-zh-light.png", "scale": 1.5, "wait": "$CHIP",
  "after": "import('/js/i18n.js').then(m => { document.getElementById('upd-ov-title').textContent = m.t('upd.ov.title', {v: '0.3.2'}); document.getElementById('upd-ov-msg').textContent = m.t('upd.ov.msg'); document.getElementById('upd-overlay').hidden = false; return true; })", "settle_ms": 600}
]
EOF
"$OUT/shot" -spec "$W/spec1.json"

# 下载中:App 安装包和 Q4_K_M 新模型同时在下
post $A /app/update/app-download
post $A /app/update/model-download '{"tier":"q4"}'
sleep 3
cat > "$W/spec2.json" <<EOF
[
 {"url": "$UA/?lang=en&theme=light&panel=update", "out": "$S/16-downloading-en-light.png", "scale": 1.5,
  "wait": "document.querySelectorAll('.upd-prog-meta b').length===2 && /\\\\/s/.test(document.getElementById('upd-app').textContent)", "settle_ms": 1500},
 {"url": "$UA/?lang=zh&theme=dark&panel=update", "out": "$S/17-downloading-zh-dark.png", "scale": 1.5,
  "wait": "document.querySelectorAll('.upd-prog-meta b').length===2", "settle_ms": 1500},
 {"url": "$UA/?lang=zh&theme=light&panel=update", "out": "$S/18-mobile-downloading-zh-light.png", "width": 390, "height": 844, "scale": 2,
  "wait": "document.querySelectorAll('.upd-prog-meta b').length>=1", "settle_ms": 1200}
]
EOF
"$OUT/shot" -spec "$W/spec2.json"

# 下完:等着「重启并完成更新」
wait_upd $A 'd["app"]["state"]=="ready"'
wait_upd $A '[m["state"] for m in d["models"]][1] in ("done","current")'
cat > "$W/spec3.json" <<EOF
[
 {"url": "$UA/?lang=zh&theme=light&panel=update", "out": "$S/19-ready-zh-light.png", "scale": 1.5, "wait": "!!document.querySelector('[data-act=app-apply]')", "settle_ms": 700},
 {"url": "$UA/?lang=en&theme=dark&panel=update", "out": "$S/20-ready-en-dark.png", "scale": 1.5, "wait": "!!document.querySelector('[data-act=app-apply]')", "settle_ms": 700}
]
EOF
"$OUT/shot" -spec "$W/spec3.json"
echo "截图在 $S/"
