#!/usr/bin/env bash
# 本地开发:假引擎 + 假下载源 + 网页直接读磁盘(改 web/ 下的文件刷新就生效)。不跑真模型。
#   bash devtools/dev.sh          # 已装好模型的状态,直接进编辑器
#   bash devtools/dev.sh fresh    # 全新安装,从选档/下载开始
# Ctrl+C 退出(引擎和假下载源一起关)。
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2
# 检查更新别连真的 GitHub(要看更新界面用 devtools/update_shots.sh)
export HUMANIZER_UPDATE_API=http://127.0.0.1:9 HUMANIZER_UPDATE_DELAY_SEC=3600
OUT=dist/dev
mkdir -p "$OUT"
go build -o "$OUT/humanizer" .
go build -o "$OUT/" ./devtools/fakellama ./devtools/fakehf

DATA=.cache/dev
if [ "${1:-}" = fresh ]; then
  rm -rf "$DATA"
else
  mkdir -p "$DATA/models"
  [ -f "$DATA/models/humanizer-12b-Q8_0.gguf" ] || printf 'GGUF' > "$DATA/models/humanizer-12b-Q8_0.gguf"
  [ -f "$DATA/settings.json" ] || echo '{"tier":"q8"}' > "$DATA/settings.json"
fi

"$OUT/fakehf" -addr 127.0.0.1:9190 -size 120 -rate 6 >/dev/null 2>&1 &
HF=$!
trap 'kill $HF 2>/dev/null || true' EXIT

FAKE_FIXTURES="$APP/devtools/fixtures/samples.json" \
  "$OUT/humanizer" --data-dir "$DATA" --engine "metal=$APP/$OUT/fakellama" \
  --base-url http://127.0.0.1:9190 --web-dir "$APP/web" --port 47690 --idle-exit 0
