#!/usr/bin/env bash
# 启动器端到端测试。全程假引擎 + 假下载源,不跑真模型,同一时间最多 4 个小进程。
#   bash devtools/e2e.sh
set -euo pipefail
cd "$(dirname "$0")/.."
APP="$PWD"
[ -x .tools/go/bin/go ] && export PATH="$APP/.tools/go/bin:$PATH" GOPATH="$APP/.tools/gopath" GOCACHE="$APP/.tools/gocache"
export GOFLAGS=-p=2
# 检查更新别连真的 GitHub(要看更新界面用 devtools/update_shots.sh)
export HUMANIZER_UPDATE_API=http://127.0.0.1:9 HUMANIZER_UPDATE_DELAY_SEC=3600

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
# 启动器报的是系统原生路径(Windows 上是 C:\...),和这里写的 shell 路径写法可能不同,
# 比之前先归一化。只在不相等时打印,免得混进 check 的输出里。
pathis() { python3 -c '
import os, sys
a, b = os.path.normcase(os.path.abspath(sys.argv[1])), os.path.normcase(os.path.abspath(sys.argv[2]))
if a != b:
    print("  路径不同:", a, "≠", b)
sys.exit(0 if a == b else 1)' "$1" "$2"; }

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
quit 47721
# 第 6 段沿用 d2:必须等 L2 真退出,否则新实例会把还在关的 L2 当成「已在运行」直接退出(CI 上出现过)
for _ in $(seq 1 50); do alive $L2 || break; sleep 0.2; done

echo "6. 启动器被强杀后,下次启动清理残留引擎"
"$OUT/humanizer" --data-dir "$W/d2" --engine "metal=$FAKE" --no-browser --port 47722 --idle-exit 0 > "$W/l3.log" 2>&1 &
L3=$!
wait_phase 47722 ready 20 || { echo "    L3 未就绪,日志:"; tail -20 "$W/l3.log"; }
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

echo "8. 存储位置:网页上把模型目录改到别处(--data-dir 固定时运行目录不让改,模型目录还能改)"
# 启动器报的是系统原生路径,和这里写的 shell 路径可能写法不同,所以用 pathis 比
D4="$W/d4"; G1="$W/gguf"; G2="$W/gguf2"
mkdir -p "$D4/models"; printf 'GGUF' > "$D4/models/humanizer-12b-Q8_0.gguf"
"$OUT/humanizer" --data-dir "$D4" --engine "metal=$FAKE" --no-browser --port 47720 --idle-exit 0 > "$W/l7.log" 2>&1 &
L4=$!
wait_phase 47720 ready 20 && ok "先就绪" || bad "先就绪"
check "状态里报告的运行目录就是 --data-dir 给的那个" 'pathis "$(st 47720 | field "d[\"data_dir\"]")" "$D4"'
check "默认模型目录在运行目录下" 'pathis "$(st 47720 | field "d[\"model_dir\"]")" "$D4/models"'
check "状态里带默认数据目录(给网页显示「恢复默认」用)" '[ -n "$(st 47720 | field "d[\"default_data_dir\"]")" ]'
check "--data-dir 给了以后 data_dir_locked=true" '[ "$(st 47720 | field "d[\"data_dir_locked\"]")" = True ]'
check "改运行目录被拒(启动参数钉死了)" 'post 47720 /app/paths "{\"data_dir\":\"$W/d4-new\"}" | field "bool(d.get(\"error\"))" | grep -q True'
check "模型目录能改:勾了「一起搬」就把文件挪过去" 'post 47720 /app/paths "{\"model_dir\":\"$G1\",\"move\":true}" | field "d[\"moved\"]" | grep -q "^1$"'
check "状态里的模型目录跟着变了" 'pathis "$(st 47720 | field "d[\"model_dir\"]")" "$G1"'
check "模型文件确实在新位置" '[ -f "$G1/humanizer-12b-Q8_0.gguf" ] && [ ! -e "$D4/models/humanizer-12b-Q8_0.gguf" ]'
check "引擎又起来了(用的是新位置那个文件)" 'wait_phase 47720 ready 20'
mkdir -p "$D4/models"; printf 'GGUF' > "$D4/models/humanizer-12b-Q8_0.gguf"
check "不勾「一起搬」就不动文件" 'post 47720 /app/paths "{\"model_dir\":\"$G2\"}" | field "d[\"moved\"]" | grep -q "^0$"'
check "新位置没有凭空多出文件" '[ -f "$D4/models/humanizer-12b-Q8_0.gguf" ] && [ ! -e "$G2/humanizer-12b-Q8_0.gguf" ]'
check "恢复默认后模型目录回到运行目录下" 'post 47720 /app/paths "{\"reset\":true}" | field "d[\"ok\"]" | grep -q True && pathis "$(st 47720 | field "d[\"model_dir\"]")" "$D4/models"'
check "恢复默认后模型文件还在原处(本来就还在)" '[ -f "$D4/models/humanizer-12b-Q8_0.gguf" ]'
check "换完位置后引擎重新就绪" 'wait_phase 47720 ready 30'
check "pick-dir 拒绝没说的目标" '[ "$(curl -s -m 5 -o /dev/null -w "%{http_code}" -X POST -H "X-Humanizer: 1" http://127.0.0.1:47720/app/pick-dir -d "{\"what\":\"nope\"}")" = 400 ]'
check "运行目录被钉死时 pick-dir 也拒" '[ "$(curl -s -m 5 -o /dev/null -w "%{http_code}" -X POST -H "X-Humanizer: 1" http://127.0.0.1:47720/app/pick-dir -d "{\"what\":\"data\"}")" = 409 ]'
quit 47720; sleep 1
for _ in $(seq 1 50); do alive $L4 || break; sleep 0.2; done

echo "9. 存储位置跨重启:不传 --data-dir 时靠 location.json 找回来"
# 把"用户主目录"指到临时目录,别去动真的默认数据目录
H="$PWD/$W/home"; mkdir -p "$H/home-fake"
run_fake_home() { env HOME="$H" USERPROFILE="$H/home-fake" LOCALAPPDATA="$H" XDG_DATA_HOME="$H" "$@"; }
mkdir -p "$W/d5/models"; printf 'GGUF' > "$W/d5/models/humanizer-12b-Q8_0.gguf"
run_fake_home "$OUT/humanizer" --data-dir "$W/d5" --engine "metal=$FAKE" --no-browser --port 47720 --idle-exit 0 > "$W/l8.log" 2>&1 &
L5=$!
wait_phase 47720 ready 20 && ok "先就绪" || bad "先就绪"
check "运行目录被 --data-dir 钉死时不写 location.json" '[ ! -e "$H/Humanizer/location.json" ]'
quit 47720; sleep 1
for _ in $(seq 1 50); do alive $L5 || break; sleep 0.2; done
# 这次不带 --data-dir:先把指针写好,启动器应该按它找位置
D6="$W/d6"; G3="$W/gguf3"
mkdir -p "$D6/logs" "$G3"
# 模型放在改过的模型目录里,否则启动器会以为没下过,停在 setup
printf 'GGUF' > "$G3/humanizer-12b-Q8_0.gguf"
mkdir -p "$H/Humanizer"
printf '{"data_dir": "%s", "model_dir": "%s"}' "$D6" "$G3" > "$H/Humanizer/location.json"
run_fake_home "$OUT/humanizer" --engine "metal=$FAKE" --no-browser --port 47720 --idle-exit 0 > "$W/l9.log" 2>&1 &
L6=$!
wait_phase 47720 ready 20 && ok "按 location.json 起来了" || bad "按 location.json 起来了"
check "重启后找回了改过的运行目录" 'pathis "$(st 47720 | field "d[\"data_dir\"]")" "$D6"'
check "重启后找回了改过的模型目录" 'pathis "$(st 47720 | field "d[\"model_dir\"]")" "$G3"'
check "启动时日志里也写了模型目录" 'grep -q "模型目录" "$W/l9.log"'
check "这次运行目录没被钉死,能再改" '[ "$(st 47720 | field "d[\"data_dir_locked\"]")" = False ]'
check "改过的运行目录里建出了 logs" '[ -d "$D6/logs" ]'
check "恢复默认会把 location.json 删掉" 'post 47720 /app/paths "{\"reset\":true}" | field "d[\"ok\"]" | grep -q True && [ ! -e "$H/Humanizer/location.json" ]'
quit 47720; sleep 1
for _ in $(seq 1 50); do alive $L6 || break; sleep 0.2; done

echo
echo "通过 $PASS 项,失败 $FAILN 项"
[ "$FAILN" = 0 ]
