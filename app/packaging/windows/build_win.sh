#!/usr/bin/env bash
# Windows 包:编译启动器 → 放引擎 → Inno Setup 安装包 + 便携 zip。
# 在 GitHub windows runner 的 Git Bash 里跑(在 app/ 目录下):
#   python packaging/fetch_engine.py --target windows --out dist/engine
#   bash packaging/windows/build_win.sh 0.3.0
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION="${1:-0.0.0-dev}"
for d in cpu vulkan cuda; do
  [ -f "dist/engine/$d/llama-server.exe" ] || { echo "缺少 dist/engine/$d,先跑 fetch_engine.py" >&2; exit 1; }
done

rm -rf dist/win && mkdir -p dist/win
# 图标和版本信息资源(.syso 会被 go build 自动带上)
go run github.com/tc-hib/go-winres@v0.3.3 simply --icon packaging/icon/icon-1024.png --manifest gui \
  --product-name Humanizer --file-description "Humanizer" \
  --product-version "$VERSION" --file-version "$VERSION" --original-filename Humanizer.exe
# -H=windowsgui:双击不弹黑色控制台窗口
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui -X main.version=$VERSION" -o dist/win/Humanizer.exe .
rm -f rsrc_windows_*.syso
cp -R dist/engine dist/win/engine
cp ../LICENSE dist/win/LICENSE.txt 2>/dev/null || true

ISCC="${ISCC:-/c/Program Files (x86)/Inno Setup 6/ISCC.exe}"
if [ ! -x "$ISCC" ]; then
  choco install innosetup -y --no-progress >/dev/null
fi
# 简体中文界面翻译(非官方语言文件,钉死到 Inno Setup 源码仓库的某个提交)
ISCC_DIR="$(dirname "$ISCC")"
if [ ! -f "$ISCC_DIR/Languages/ChineseSimplified.isl" ] && [ -n "${INNO_ZH_URL:-}" ]; then
  curl -fsSL "$INNO_ZH_URL" -o "$ISCC_DIR/Languages/ChineseSimplified.isl" || echo "下载中文语言文件失败,安装界面只有英文"
fi
# Git Bash 会把 /DAppVersion=… 当路径改写成 C:/Program Files/Git/DAppVersion=…,ISCC 以为有两个脚本文件(10-01 CI 第二次失败);关掉参数改写
MSYS2_ARG_CONV_EXCL="*" MSYS_NO_PATHCONV=1 "$ISCC" "/DAppVersion=$VERSION" packaging/windows/humanizer.iss

# 便携版:解压即用,不写注册表
(cd dist/win && 7z a -tzip -mx=7 "../Humanizer-$VERSION-windows-x64-portable.zip" . >/dev/null)
ls -lh dist/*.exe dist/*.zip
