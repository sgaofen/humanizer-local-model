#!/usr/bin/env bash
# 本地出截图(docs/screenshots/*.png)。全程只用假引擎 + 假下载源,不跑任何真模型。
#   bash devtools/shots.sh
# 需要:Go(PATH 里或 .tools/go)、Google Chrome。
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2
# 检查更新别连真的 GitHub(要看更新界面用 devtools/update_shots.sh)
export HUMANIZER_UPDATE_API=http://127.0.0.1:9 HUMANIZER_UPDATE_DELAY_SEC=3600

OUT=dist/dev
WORK=.cache/shots
mkdir -p "$OUT" "$WORK"
go build -o "$OUT/humanizer" .
go build -o "$OUT/" ./devtools/fakellama ./devtools/fakehf ./devtools/shot

PIDS=()
cleanup() {
  for port in 47710 47711; do
    curl -s -m 2 -X POST -H 'X-Humanizer: 1' "http://127.0.0.1:$port/app/quit" >/dev/null 2>&1 || true
  done
  sleep 1
  for p in "${PIDS[@]:-}"; do [ -n "$p" ] && kill "$p" 2>/dev/null || true; done
}
trap cleanup EXIT

wait_phase() { # port phase timeout_s
  for _ in $(seq 1 $(( $3 * 4 ))); do
    if curl -s -m 1 "http://127.0.0.1:$1/app/status" | grep -q "\"phase\":\"$2\""; then return 0; fi
    sleep 0.25
  done
  echo "等不到 $1 进入 $2" >&2; return 1
}

# 假下载源:200 MiB、2 MiB/s,够截到下载中的画面
"$OUT/fakehf" -addr 127.0.0.1:9181 -size 200 -rate 2 > "$WORK/fakehf.log" 2>&1 &
PIDS+=($!)

# A:模型已就位,直接进编辑器(放一个只有 GGUF 魔数的小文件,假引擎不读内容)
rm -rf "$WORK/a" "$WORK/b"
mkdir -p "$WORK/a/models"
printf 'GGUF' > "$WORK/a/models/humanizer-12b-Q8_0.gguf"
echo '{"tier":"q8","endpoint":"hf"}' > "$WORK/a/settings.json"
FAKE_FIXTURES="$APP/devtools/fixtures/samples.json" FAKE_TOKEN_MS=6 FAKE_LOAD_MS=400 \
  "$OUT/humanizer" --data-dir "$WORK/a" --engine "metal=$APP/$OUT/fakellama" --no-browser --port 47710 --idle-exit 0 \
  > "$WORK/a.log" 2>&1 &
PIDS+=($!)

# B:全新安装,停在选档页;下载源指向假 HF
"$OUT/humanizer" --data-dir "$WORK/b" --engine "metal=$APP/$OUT/fakellama" --base-url http://127.0.0.1:9181 \
  --no-browser --port 47711 --idle-exit 0 > "$WORK/b.log" 2>&1 &
PIDS+=($!)

wait_phase 47710 ready 20
wait_phase 47711 setup 10

DONE="!!document.querySelector('#output .ins') && !document.querySelector('#btn-go').classList.contains('running')"
S=docs/screenshots
A=http://127.0.0.1:47710
cat > "$WORK/spec-a.json" <<EOF
[
 {"url": "$A/?lang=zh&theme=light&example=zh-social&run=1", "out": "$S/editor-zh-light.png", "scale": 1.5, "wait": "$DONE", "settle_ms": 700},
 {"url": "$A/?lang=en&theme=dark&example=en-social&run=1",  "out": "$S/editor-en-dark.png",  "scale": 1.5, "wait": "$DONE", "settle_ms": 700},
 {"url": "$A/?lang=en&theme=light&example=en-social&run=1", "out": "$S/editor-en-light.png", "scale": 1.5, "wait": "$DONE", "settle_ms": 700},
 {"url": "$A/?lang=zh&theme=dark&example=zh-social&run=1",  "out": "$S/editor-zh-dark.png",  "scale": 1.5, "wait": "$DONE", "settle_ms": 700},
 {"url": "$A/?lang=zh&theme=light&example=zh-social&run=1", "out": "$S/history-zh-light.png", "scale": 1.5, "wait": "$DONE",
  "after": "document.getElementById('btn-history').click()", "settle_ms": 900},
 {"url": "$A/?lang=en&theme=light&example=en-social&run=1", "out": "$S/mobile-en-light.png", "width": 420, "height": 900, "scale": 2, "wait": "$DONE", "settle_ms": 700}
]
EOF
"$OUT/shot" -spec "$WORK/spec-a.json"

B=http://127.0.0.1:47711
cat > "$WORK/spec-b1.json" <<EOF
[
 {"url": "$B/?lang=zh&theme=light", "out": "$S/setup-zh-light.png", "scale": 1.5, "wait": "!document.querySelector('[data-pane=choose]').hidden && document.querySelectorAll('.tier').length === 5", "settle_ms": 600}
]
EOF
"$OUT/shot" -spec "$WORK/spec-b1.json"

curl -s -X POST -H 'X-Humanizer: 1' -H 'Content-Type: application/json' "$B/app/setup" -d '{"tier":"q8"}' >/dev/null
sleep 9
cat > "$WORK/spec-b2.json" <<EOF
[
 {"url": "$B/?lang=en&theme=dark", "out": "$S/download-en-dark.png", "scale": 1.5, "wait": "!document.querySelector('[data-pane=progress]').hidden && /\\\\d/.test(document.querySelector('#prog-stats').textContent)", "settle_ms": 1200}
]
EOF
"$OUT/shot" -spec "$WORK/spec-b2.json"
echo "截图在 $S/"
