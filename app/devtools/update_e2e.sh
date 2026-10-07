#!/usr/bin/env bash
# 「检查更新」端到端测试(macOS)。真启动器装进假的 .app、真 dmg,假 GitHub(fakegh)+ 假 HF(fakehf)+ 假引擎(fakellama),
# 不加载任何真模型、不碰真网络。
#   bash devtools/update_e2e.sh
# 覆盖:检查(只带 UA)→ 模型更新(下载期间照常改写,换完重启引擎)→ App 下载校验 → 原地替换 → 重启到新版本(端口不变)
#       → 新版本起不来时自动回滚到旧版本。
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ "$(uname)" = Darwin ] || { echo "只在 macOS 上跑(要 hdiutil / codesign)"; exit 0; }
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2
# shellcheck source=update_lib.sh
source devtools/update_lib.sh

OUT=dist/upd
W="$APP/.cache/upd-e2e"
rm -rf "$W"; mkdir -p "$OUT" "$W/gh" "$W/Applications" "$W/data/models" "$W/new"
echo "· 编译(旧版 0.3.1、新版 0.9.0、坏掉的 0.9.1、假引擎、假 GitHub、假 HF)"
go build -ldflags "-X main.version=0.3.1" -o "$OUT/hz-0.3.1" .
go build -ldflags "-X main.version=0.9.0" -o "$OUT/hz-0.9.0" .
go build -o "$OUT/" ./devtools/fakellama ./devtools/fakehf ./devtools/fakegh
cat > "$W/broken.go" <<'EOF'
// 「坏掉的新版本」:--version 正常(能通过更新前的预检),真正启动就退出 —— 用来测自动回滚。
package main

import (
	"fmt"
	"os"
)

func main() {
	for _, a := range os.Args[1:] {
		if a == "--version" {
			fmt.Println("humanizer 0.9.1")
			return
		}
	}
	os.Exit(3)
}
EOF
go build -o "$OUT/hz-broken" "$W/broken.go"

echo "· 造 .app 和 dmg"
BUNDLE="$W/Applications/Humanizer.app"
make_bundle "$BUNDLE" 0.3.1 "$OUT/hz-0.3.1" "$OUT/fakellama"
make_bundle "$W/new/Humanizer.app" 0.9.0 "$OUT/hz-0.9.0" "$OUT/fakellama"
make_dmg "$W/new/Humanizer.app" "$W/gh/Humanizer-0.9.0-macos-arm64.dmg"

PORT=47740 GH=127.0.0.1:9291 HF=127.0.0.1:9292
"$OUT/fakegh" -addr $GH -dir "$W/gh" > "$W/fakegh.log" 2>&1 &
GHPID=$!
"$OUT/fakehf" -addr $HF -size 16 -rate 4 > "$W/fakehf.log" 2>&1 &
HFPID=$!
cleanup() {
  curl -s -m 2 -X POST -H 'X-Humanizer: 1' "http://127.0.0.1:$PORT/app/quit" >/dev/null 2>&1 || true
  kill $GHPID $HFPID 2>/dev/null || true
}
trap cleanup EXIT

PASS=0; FAILN=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAILN=$((FAILN+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
st()   { curl -s -m 2 "http://127.0.0.1:$PORT/app/status"; }
upd()  { curl -s -m 3 "http://127.0.0.1:$PORT/app/update"; }
post() { local body="${2:-}"; [ -n "$body" ] || body='{}'; curl -s -m 10 -X POST -H 'X-Humanizer: 1' -H 'Content-Type: application/json' "http://127.0.0.1:$PORT$1" -d "$body"; }
field() { python3 -c "import json,sys
try: d=json.load(sys.stdin)
except Exception: d=None
print(eval(sys.argv[1]) if d is not None else '')" "$1" 2>/dev/null; }
ping_ver() { curl -s -m 1 "http://127.0.0.1:$PORT/app/ping" | field 'd["version"]'; }
plist_ver() { plutil -extract CFBundleShortVersionString raw -o - "$BUNDLE/Contents/Info.plist"; }
wait_until() { # 描述 超时秒 条件
  local i
  for i in $(seq 1 $(( $2 * 4 ))); do if eval "$3" >/dev/null 2>&1; then return 0; fi; sleep 0.25; done
  return 1
}
model_state() { upd | field '[m["state"] for m in d["models"] if m["tier"]=="q8"][0]'; }

printf 'GGUF' > "$W/data/models/humanizer-12b-Q8_0.gguf"
echo '{"tier":"q8"}' > "$W/data/settings.json"
export FAKE_FIXTURES="$APP/devtools/fixtures/samples.json" FAKE_LOAD_MS=300 FAKE_TOKEN_MS=2
export HUMANIZER_UPDATE_DELAY_SEC=3600 HUMANIZER_UPDATE_READY_SEC=12   # 不让后台自动检查插进来;回滚测试别等 90 秒

echo "1. 从 .app 启动旧版本 0.3.1"
"$BUNDLE/Contents/MacOS/Humanizer" --data-dir "$W/data" --base-url "http://$HF" --update-api "http://$GH" --no-browser --port $PORT --idle-exit 0
wait_until "就绪" 20 '[ "$(st | field "d[\"phase\"]")" = ready ]' && ok "旧版本就绪" || bad "旧版本就绪"
check "运行的是 0.3.1" '[ "$(ping_ver)" = 0.3.1 ]'

echo "2. 检查更新"
post /app/update/check >/dev/null
wait_until "检查完" 15 '[ "$(upd | field "d[\"checking\"]==False and d[\"app\"][\"available\"]")" = True ]' && ok "发现新版本" || bad "发现新版本"
check "最新是 0.9.0" '[ "$(upd | field "d[\"app\"][\"latest\"][\"version\"]")" = 0.9.0 ]'
check "识别为可原地替换的 .app" '[ "$(upd | field "d[\"app\"][\"kind\"]+\"/\"+d[\"app\"][\"mode\"]")" = mac_app/replace ]'
check "请求 GitHub 时 UA 是 humanizer-app/0.3.1" 'grep -q "UA=\"humanizer-app/0.3.1\"" "$W/fakegh.log"'
check "本地模型和 HF 上不一样 → 模型有新版" '[ "$(model_state)" = available ]'

echo "3. 模型更新:下载期间照常改写,换完重启引擎"
post /app/update/model-download '{"tier":"q8"}' >/dev/null
sleep 1
check "下载中" '[ "$(model_state)" = downloading ]'
check "下载期间引擎照常就绪" '[ "$(st | field "d[\"phase\"]")" = ready ]'
CFG=$(curl -s "http://127.0.0.1:$PORT/app/config")
BODY=$(echo "$CFG" | python3 -c '
import json,sys; c=json.load(sys.stdin)
draft=json.load(open("devtools/fixtures/samples.json"))["samples"][0]["draft"]
b=dict(c["sampling"]); b.update(prompt=c["instr"]+"\n\n"+draft.strip()+c["sep"], n_predict=200, stream=True)
print(json.dumps(b))')
SSE=$(curl -sN -m 20 -X POST -H 'X-Humanizer: 1' "http://127.0.0.1:$PORT/api/completion" --data "$BODY")
check "下载期间改写正常(用的是旧模型)" '[ "$(echo "$SSE" | grep -c "^data: ")" -gt 20 ]'
wait_until "模型更新完成" 30 '[ "$(model_state)" = done ]' && ok "模型下载、校验、替换完成" || bad "模型下载、校验、替换完成"
check "模型文件换成了新的(16 MiB)" '[ "$(stat -f %z "$W/data/models/humanizer-12b-Q8_0.gguf")" = 16777216 ]'
check "没有留下 .update / .bak" '! ls "$W/data/models" | grep -qE "\.(update|bak|part)"'
check "引擎重启过一次" '[ "$(grep -c "^===== .* 启动 metal" "$W/data/logs/llama-server.log")" -ge 2 ]'
wait_until "引擎回到就绪" 15 '[ "$(st | field "d[\"phase\"]")" = ready ]' && ok "换完模型引擎就绪" || bad "换完模型引擎就绪"
post /app/update/model-ack '{"tier":"q8"}' >/dev/null
check "确认后显示已是最新" '[ "$(model_state)" = current ]'

echo "4. App 更新:下载 → 校验 → 替换 → 重启到 0.9.0"
post /app/update/app-download >/dev/null
wait_until "下载完" 20 '[ "$(upd | field "d[\"app\"][\"state\"]")" = ready ]' && ok "安装包下载并校验通过" || bad "安装包下载并校验通过"
check "fakegh 上的下载走了 302 跳转" 'grep -q "GET /dl/app-v0.9.0/Humanizer-0.9.0-macos-arm64.dmg" "$W/fakegh.log" && grep -q "GET /blob/Humanizer-0.9.0" "$W/fakegh.log"'
OLDPID=$(curl -s "http://127.0.0.1:$PORT/app/ping" | field 'd["pid"]')
post /app/update/app-apply >/dev/null
wait_until "新版本起来" 60 '[ "$(ping_ver)" = 0.9.0 ]' && ok "重启到了 0.9.0(端口没变)" || bad "重启到了 0.9.0(端口没变)"
wait_until "旧进程收尾" 10 '! kill -0 $OLDPID 2>/dev/null' && ok "负责重启的旧进程确认后退出了" || bad "负责重启的旧进程确认后退出了"
check ".app 里的 Info.plist 是 0.9.0" '[ "$(plist_ver)" = 0.9.0 ]'
check "备份和暂存目录都清掉了" '[ ! -e "$W/Applications/.Humanizer.app.backup" ] && [ ! -e "$W/Applications/.Humanizer.app.update" ]'
check "签名完整" 'codesign --verify --deep --strict "$BUNDLE"'
check "结果:成功 0.3.1 → 0.9.0" '[ "$(upd | field "(d[\"result\"][\"ok\"], d[\"result\"][\"from\"], d[\"result\"][\"to\"])")" = "(True, '"'"'0.3.1'"'"', '"'"'0.9.0'"'"')" ]'
wait_until "新版本引擎就绪" 20 '[ "$(st | field "d[\"phase\"]")" = ready ]' && ok "新版本里模型照常装载" || bad "新版本里模型照常装载"
check "新版本已是最新" '[ "$(upd | field "d[\"app\"][\"available\"]")" = False ] || { post /app/update/check >/dev/null; sleep 2; [ "$(upd | field "d[\"app\"][\"available\"]")" = False ]; }'
post /app/update/ack >/dev/null

echo "5. 新版本起不来 → 自动换回旧版本"
make_bundle "$W/new/Humanizer.app" 0.9.1 "$OUT/hz-broken" "$OUT/fakellama"
make_dmg "$W/new/Humanizer.app" "$W/gh/Humanizer-0.9.1-macos-arm64.dmg"
post /app/update/check >/dev/null
wait_until "发现 0.9.1" 15 '[ "$(upd | field "d[\"app\"][\"latest\"][\"version\"]")" = 0.9.1 ]' && ok "发现 0.9.1" || bad "发现 0.9.1"
post /app/update/app-download >/dev/null
wait_until "下载完" 20 '[ "$(upd | field "d[\"app\"][\"state\"]")" = ready ]' || true
post /app/update/app-apply >/dev/null
wait_until "回滚完成" 60 '[ "$(upd | field "d[\"result\"][\"ok\"]")" = False ]' && ok "记录了更新失败" || bad "记录了更新失败"
check "回到 0.9.0 并在原端口运行" '[ "$(ping_ver)" = 0.9.0 ]'
check ".app 换回了 0.9.0" '[ "$(plist_ver)" = 0.9.0 ] && [ -x "$BUNDLE/Contents/MacOS/Humanizer" ]'
check "签名完整" 'codesign --verify --deep --strict "$BUNDLE"'
check "没有留下备份/失败目录" '[ -z "$(ls -A "$W/Applications" | grep -v "^Humanizer.app$")" ]'
check "失败原因写清楚了" 'upd | field "d[\"result\"][\"error\"]" | grep -q "0.9.1"'
wait_until "旧版本引擎就绪" 20 '[ "$(st | field "d[\"phase\"]")" = ready ]' && ok "回滚后可以照常改写" || bad "回滚后可以照常改写"

echo "6. 退出"
post /app/quit >/dev/null; sleep 1.5
check "进程都退了" '! pgrep -f "$W/Applications/Humanizer.app/Contents/MacOS/Humanizer" >/dev/null && ! pgrep -f "$W/Applications/Humanizer.app/Contents/Resources/engine" >/dev/null'

echo
echo "通过 $PASS 项,失败 $FAILN 项"
[ "$FAILN" = 0 ]
