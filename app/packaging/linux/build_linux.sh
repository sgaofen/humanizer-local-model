#!/usr/bin/env bash
# Linux 包:编译启动器 → 放引擎 → 便携 tar.gz + AppImage。不打 deb/rpm。
# 在 app/ 目录下:
#   python3 packaging/fetch_engine.py --target linux --out dist/engine
#   bash packaging/linux/build_linux.sh 0.3.2
#
# 产物:
#   dist/Humanizer-<版本>-linux-x64-portable.tar.gz   解压即用(humanizer + engine/)
#   dist/Humanizer-<版本>-linux-x64.AppImage          单文件,双击/赋可执行权限后运行
#
# AppImage 工具:appimagetool 1.9.1(钉死 sha256)。优先 $APPIMAGETOOL,
# 其次 dist/tools/ 里的缓存,都没有就下载并校验。
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION="${1:-0.0.0-dev}"
# 先把环境问题说清楚,别等打包到一半才以看不懂的方式失败。
[ "$(uname -m)" = x86_64 ] || { echo "只打 x86_64 包,当前架构是 $(uname -m)" >&2; exit 1; }
for t in curl sha256sum tar go python3; do
  command -v "$t" >/dev/null || { echo "缺少命令:$t" >&2; exit 1; }
done
for d in cpu vulkan; do
  [ -x "dist/engine/$d/llama-server" ] || { echo "缺少 dist/engine/$d,先跑 fetch_engine.py --target linux" >&2; exit 1; }
done

APPIMAGETOOL_VERSION=1.9.1
APPIMAGETOOL_SHA256=ed4ce84f0d9caff66f50bcca6ff6f35aae54ce8135408b3fa33abfc3cb384eb0
APPIMAGETOOL_URL="https://github.com/AppImage/appimagetool/releases/download/$APPIMAGETOOL_VERSION/appimagetool-x86_64.AppImage"
resolve_appimagetool() {
  if [ -n "${APPIMAGETOOL:-}" ]; then echo "$APPIMAGETOOL"; return; fi
  local cached=dist/tools/appimagetool-x86_64.AppImage
  # 缓存每次用之前都重新校验(和 fetch_engine.py 的缓存一致):
  # 被截断/替换过的缓存不能因为文件还在就被直接执行。
  if [ -f "$cached" ] && ! echo "$APPIMAGETOOL_SHA256  $cached" | sha256sum -c - >/dev/null 2>&1; then
    rm -f "$cached"
  fi
  if [ ! -f "$cached" ]; then
    mkdir -p dist/tools
    curl -fsSL "$APPIMAGETOOL_URL" -o "$cached.tmp"
    echo "$APPIMAGETOOL_SHA256  $cached.tmp" | sha256sum -c - >/dev/null || { echo "appimagetool sha256 不符" >&2; rm -f "$cached.tmp"; exit 1; }
    mv "$cached.tmp" "$cached"
    chmod +x "$cached"
  fi
  [ -x "$cached" ] || chmod +x "$cached"
  echo "$cached"
}

echo "· 编译启动器 $VERSION"
rm -rf dist/linux && mkdir -p dist/linux/stage dist/linux/AppDir/usr/bin \
  dist/linux/AppDir/usr/share/applications dist/linux/AppDir/usr/share/icons/hicolor/1024x1024/apps
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o dist/linux/stage/humanizer .
cp dist/linux/stage/humanizer dist/linux/AppDir/usr/bin/humanizer

echo "· 放入引擎(保留 .so 软链接:解引用会把每份库复制三遍;解压端只放行指向包内的链接)"
cp -a dist/engine dist/linux/stage/engine
cp -a dist/engine dist/linux/AppDir/usr/bin/engine
cp ../LICENSE dist/linux/stage/LICENSE 2>/dev/null || true
cp packaging/icon/icon-1024.png dist/linux/stage/humanizer.png
cp packaging/linux/humanizer.desktop dist/linux/stage/humanizer.desktop
cp packaging/linux/install-desktop.sh dist/linux/stage/install-desktop.sh
chmod +x dist/linux/stage/install-desktop.sh

echo "· 便携包"
# 清掉旧版本的同类产物,免得本地 dist 里堆着多个版本被通配符误选。
rm -f dist/Humanizer-*-linux-x64-portable.tar.gz dist/Humanizer-*-linux-x64.AppImage
TAR="dist/Humanizer-$VERSION-linux-x64-portable.tar.gz"
tar -czf "$TAR" -C dist/linux/stage .

echo "· AppImage"
cp packaging/icon/icon-1024.png dist/linux/AppDir/usr/share/icons/hicolor/1024x1024/apps/humanizer.png
cp packaging/icon/icon-1024.png dist/linux/AppDir/humanizer.png
cp packaging/linux/humanizer.desktop dist/linux/AppDir/humanizer.desktop
cp packaging/linux/humanizer.desktop dist/linux/AppDir/usr/share/applications/humanizer.desktop
cp packaging/linux/AppRun dist/linux/AppDir/AppRun
chmod +x dist/linux/AppDir/AppRun
TOOL="$(resolve_appimagetool)"
APPIMAGE="dist/Humanizer-$VERSION-linux-x64.AppImage"
rm -f "$APPIMAGE"
ARCH=x86_64 APPIMAGE_EXTRACT_AND_RUN=1 "$TOOL" dist/linux/AppDir "$APPIMAGE" >/dev/null
chmod +x "$APPIMAGE"
ls -lh "$TAR" "$APPIMAGE"
