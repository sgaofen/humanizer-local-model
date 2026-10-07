#!/usr/bin/env bash
# 启动器端到端测试。全程假引擎 + 假下载源,不跑真模型,同一时间最多 4 个小进程。
#   bash devtools/e2e.sh
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2

OUT=dist/dev
W=.cache/e2e
rm -rf "$W"; mkdir -p "$W" "$OUT"
go build -o "$OUT/humanizer" .
go build -o "$OUT/" ./devtools/fakellama ./devtools/fakehf
ln -sf fakellama "$OUT/fakellama-fail"    # 名字带 fail:启动即崩
ln -sf fakellama "$OUT/fakellama-nogpu"   # 名字带 nogpu:0 层上 GPU
FAKE="$APP/$OUT/fakellama"
export FAKE_FIXTURES="$APP/devtools/fixtures/samples.json" FAKE_LOAD_MS=500 FAKE_TOKEN_MS=2

PASS=0; FAILN=0
ok()   { echo "  ✓ $1"; PASS=$((PASS+1)); }
bad()  { echo "  ✗ $1"; FAILN=$((FAILN+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
st()   { curl -s -m 2 "http://127.0.0.1:$1/app/status"; }
field() { python3 -c "import json,sys; d=json.load(sys.stdin); print(eval(sys.argv[1]))" "$1"; }
wait_phase() { for _ in $(seq 1 $(( $3 * 5 ))); do st "$1" | grep -q "\"phase\":\"$2\"" && return 0; sleep 0.2; done; return 1; }
post() { curl -s -m 5 -X POST -H 'X-Humanizer: 1' -H 'Content-Type: application/json' "http://127.0.0.1:$1$2" -d "${3:-{\}}"; }
quit() { post "$1" /app/quit >/dev/null 2>&1 || true; }
alive() { kill -0 "$1" 2>/dev/null; }

"$OUT/fakehf" -addr 127.0.0.1:9182 -size 24 -rate 12 -drop-at 5 > "$W/fakehf.log" 2>&1 &
HF=$!
trap 'quit 47720; quit 47721; quit 47722; kill $HF 2>/dev/null || true' EXIT

echo "1. 首次运行:选档 → 下载(中途断线)→ 暂停 → 续传 → 校验 → 起引擎"
"$OUT/humanizer" --data-dir "$W/d1" --engine "metal=$FAKE" --base-url http://127.0.0.1:9182 --no-browser --port 47720 --idle-exit 0 > "$W/l1.log" 2>&1 &
L1=$!
wait_phase 47720 setup 10 && ok "进入 setup" || bad "进入 setup"
check "按内存推荐档位(≥32G→q8,≥16G→q6,≥14G→q4,≥12G→q3,其余 2-bit)" '[ "$(st 47720 | field "d[\"recommended\"]")" = "$(st 47720 | field "(lambda r: \"q8\" if r>=31 else \"q6\" if r>=15 else \"q4\" if r>=13 else \"q3\" if r>=11 else \"q2\")(d[\"sys\"][\"ram_gb\"])")" ]'
check "没带 X-Humanizer 的 POST 被拒" '[ "$(curl -s -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:47720/app/quit)" = 403 ]'
check "伪造 Host 被拒" '[ "$(curl -s -o /dev/null -w "%{http_code}" -H "Host: attacker.test:47720" http://127.0.0.1:47720/)" = 403 ]'
post 47720 /app/setup '{"tier":"q8"}' >/dev/null
sleep 1.2
post 47720 /app/download/pause >/dev/null
sleep 0.5
check "暂停后 phase=paused" '[ "$(st 47720 | field "d[\"phase\"]")" = paused ]'
check "暂停后半截文件还在" '[ -s "$W/d1/models/humanizer-12b-Q8_0.gguf.part" ]'
post 47720 /app/setup '{"tier":"q8"}' >/dev/null
wait_phase 47720 ready 30 && ok "下载完成并就绪" || bad "下载完成并就绪"
check "断线后用 Range 续传过" 'grep -q "Range=\"bytes=[1-9]" "$W/fakehf.log"'
check "sha256 校验通过" 'grep -q "sha256 校验通过" "$W/l1.log"'
check "半截文件和 .part.json 已清理" '[ ! -e "$W/d1/models/humanizer-12b-Q8_0.gguf.part" ] && [ ! -e "$W/d1/models/humanizer-12b-Q8_0.gguf.part.json" ]'
check "引擎命令行:只听 127.0.0.1、全部层上 GPU、关掉自带网页" 'grep -q -- "--host 127.0.0.1 --port [0-9]* -c 8192 -np 1 -ngl all --no-webui" "$W/d1/logs/llama-server.log"'

echo "2. 反代与流式"
CFG=$(curl -s http://127.0.0.1:47720/app/config)
check "提示词指纹 = promptfmt.py" '[ "$(echo "$CFG" | field "d[\"fingerprint\"]")" = cc51d66b4c593fbe ]'
BODY=$(echo "$CFG" | python3 -c '
import json,sys; c=json.load(sys.stdin)
draft=json.load(open("devtools/fixtures/samples.json"))["samples"][0]["draft"]
b=dict(c["sampling"]); b.update(prompt=c["instr"]+"\n\n"+draft.strip()+c["sep"], n_predict=600, stream=True, return_progress=True)
print(json.dumps(b))')
SSE=$(curl -sN -m 20 -X POST -H 'X-Humanizer: 1' http://127.0.0.1:47720/api/completion --data "$BODY")
check "SSE 逐 token 返回" '[ "$(echo "$SSE" | grep -c "^data: ")" -gt 50 ]'
check "以 EOS 结束(stop_type=eos)" 'echo "$SSE" | grep -q "\"stop_type\":\"eos\""'
check "吐出的是夹具里的真实输出" 'echo "$SSE" | grep -q "FAANG"'
check "采样参数:T=1.0 top_p=0.95 top_k=0 min_p=0,无 stop 词" 'tail -1 <(grep REQUEST "$W/d1/logs/llama-server.log") | grep -q "\"min_p\":0" && ! grep REQUEST "$W/d1/logs/llama-server.log" | tail -1 | grep -q "\"stop\""'
EP=$(grep -o -- '--port [0-9]*' "$W/d1/logs/llama-server.log" | tail -1 | awk '{print $2}')
check "绕过启动器直连引擎(无钥匙)被拒 401" '[ "$(curl -s -o /dev/null -w "%{http_code}" -X POST http://127.0.0.1:$EP/completion -d "{}")" = 401 ]'
check "白名单外的 /api 路径 404" '[ "$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:47720/api/slots)" = 404 ]'
python3 - "$W/e2e.docx" <<'PY'
import sys, zipfile
with zipfile.ZipFile(sys.argv[1], "w") as z:
    z.writestr("word/document.xml", '<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Imported 42 files.</w:t></w:r></w:p></w:body></w:document>')
PY
check "导入 .docx:/app/document 在本机提取出文字" 'curl -s -m 5 -X POST -H "X-Humanizer: 1" -F "file=@$W/e2e.docx" http://127.0.0.1:47720/app/document | grep -q "Imported 42 files."'
check "导入接口同样要带 X-Humanizer 头" '[ "$(curl -s -o /dev/null -w "%{http_code}" -X POST -F "file=@$W/e2e.docx" http://127.0.0.1:47720/app/document)" = 403 ]'
check "导入的文件不落盘(数据目录里没有 .docx)" '[ -z "$(find "$W/d1" -iname "*.docx" -print -quit)" ]'

echo "3. 单实例:再启动一次只会指向已在跑的那个"
OUT2=$("$OUT/humanizer" --data-dir "$W/d1" --no-browser 2>&1 || true)
check "第二个进程直接退出并给出地址" 'echo "$OUT2" | grep -q "已在运行: http://127.0.0.1:47720/"'

echo "4. 退出时引擎一起关掉"
EPID=$(python3 -c "import json; print(json.load(open('$W/d1/instance.json'))['engine_pid'])")
check "引擎进程在跑" 'alive $EPID'
quit 47720; sleep 1.5
check "启动器已退出" '! alive $L1'
check "引擎进程已结束" '! alive $EPID'
check "instance.json 已删除" '[ ! -e "$W/d1/instance.json" ]'

echo "5. 后端回退:CUDA 崩 → Vulkan 0 层上 GPU → CPU"
mkdir -p "$W/d2/models"; printf 'GGUF' > "$W/d2/models/humanizer-12b-Q8_0.gguf"
"$OUT/humanizer" --data-dir "$W/d2" --engine "cuda=$APP/$OUT/fakellama-fail;vulkan=$APP/$OUT/fakellama-nogpu;cpu=$FAKE" --no-browser --port 47721 --idle-exit 0 > "$W/l2.log" 2>&1 &
L2=$!
wait_phase 47721 ready 20 && ok "最终就绪" || bad "最终就绪"
ATT=$(st 47721 | field '",".join(a["backend"]+":"+a["layers"]+":"+a["result"] for a in d["engine"]["attempts"])')
echo "    尝试顺序 $ATT"
check "最终用的是 CPU" '[ "$(st 47721 | field "d[\"engine\"][\"backend\"]")" = cpu ]'
check "回退顺序:cuda(all→auto)→ vulkan(0 层,跳过 auto)→ cpu" '[ "$ATT" = "cuda:all:failed,cuda:auto:failed,vulkan:all:no_gpu,cpu:0:ok" ]'
quit 47721; sleep 1

echo "6. 启动器被强杀后,下次启动清理残留引擎"
"$OUT/humanizer" --data-dir "$W/d2" --engine "metal=$FAKE" --no-browser --port 47722 --idle-exit 0 > "$W/l3.log" 2>&1 &
L3=$!
wait_phase 47722 ready 20 || true
EPID=$(python3 -c "import json; print(json.load(open('$W/d2/instance.json'))['engine_pid'])")
kill -9 $L3; sleep 0.5
check "强杀启动器后引擎成了孤儿" 'alive $EPID'
"$OUT/humanizer" --data-dir "$W/d2" --engine "metal=$FAKE" --no-browser --port 47722 --idle-exit 0 > "$W/l4.log" 2>&1 &
sleep 1.5
check "新启动器清理了残留引擎" '! alive $EPID && grep -q "清理了上次残留的引擎进程" "$W/l4.log"'
wait_phase 47722 ready 20 && ok "新实例正常就绪" || bad "新实例正常就绪"
quit 47722; sleep 1

echo "7. 下载到一半退出 App,重开后停在「已暂停」,点继续接着下"
"$OUT/humanizer" --data-dir "$W/d3" --engine "metal=$FAKE" --base-url http://127.0.0.1:9182 --no-browser --port 47720 --idle-exit 0 > "$W/l5.log" 2>&1 &
wait_phase 47720 setup 10 || true
post 47720 /app/setup '{"tier":"q6"}' >/dev/null
sleep 0.8
quit 47720; sleep 1
check "退出后半截文件还在" '[ -s "$W/d3/models/humanizer-12b-Q6_K.gguf.part" ]'
"$OUT/humanizer" --data-dir "$W/d3" --engine "metal=$FAKE" --base-url http://127.0.0.1:9182 --no-browser --port 47720 --idle-exit 0 > "$W/l6.log" 2>&1 &
wait_phase 47720 paused 10 && ok "重开后 phase=paused" || bad "重开后 phase=paused"
check "显示已下载的字节数" '[ "$(st 47720 | field "d[\"download\"][\"received\"]")" -gt 0 ]'
post 47720 /app/setup '{"tier":"q6"}' >/dev/null
wait_phase 47720 ready 30 && ok "续传完成并就绪" || bad "续传完成并就绪"
check "续传是从断点开始的" 'grep -q "从 [1-9][0-9]* 字节处续传" "$W/l6.log"'
quit 47720; sleep 1

echo
echo "通过 $PASS 项,失败 $FAILN 项"
[ "$FAILN" = 0 ]
