#!/usr/bin/env bash
# 组装 Humanizer.app 并打成 .dmg(在 macOS 上跑;CI 用 macos-14 arm64 runner)。
#
#   bash packaging/macos/build_app.sh 0.3.1
#
# 前置:python3 packaging/fetch_engine.py --target macos --out dist/engine
#
# 签名:
#   - 设了 MACOS_SIGN_IDENTITY("Developer ID Application: ...")→ 正式签名 + hardened runtime;
#     再设 APPLE_ID / APPLE_TEAM_ID / APPLE_APP_PASSWORD → 公证并 staple。
#   - 都没设 → ad-hoc 签名(能跑,但用户第一次打开会被 Gatekeeper 拦,见 README)。
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION="${1:-0.0.0-dev}"
BUNDLE_ID="${MACOS_BUNDLE_ID:-io.github.sgaofen.humanizer}"
ENGINE=dist/engine/metal
[ -x "$ENGINE/llama-server" ] || { echo "缺少 $ENGINE/llama-server,先跑 fetch_engine.py" >&2; exit 1; }

STAGE=dist/mac
APP="$STAGE/Humanizer.app"
rm -rf "$STAGE"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources/engine"

echo "· 编译启动器 $VERSION"
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
  -o "$APP/Contents/MacOS/Humanizer" .

echo "· 放入引擎(保留 dylib 软链接)"
ditto "$ENGINE" "$APP/Contents/Resources/engine/metal"
sed -e "s/__VERSION__/$VERSION/g" -e "s/__BUNDLE_ID__/$BUNDLE_ID/g" packaging/macos/Info.plist > "$APP/Contents/Info.plist"

echo "· 生成 .icns"
ICONSET="$STAGE/Humanizer.iconset"
mkdir -p "$ICONSET"
SRC=packaging/icon/icon-mac-1024.png
for s in 16 32 128 256 512; do
  sips -z $s $s "$SRC" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$SRC" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/Humanizer.icns"
rm -rf "$ICONSET"

echo "· 签名"
if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then
  SIGN=(codesign --force --timestamp --options runtime --sign "$MACOS_SIGN_IDENTITY")
else
  SIGN=(codesign --force --sign -)
fi
# 引擎在 Resources 里,--deep 不保证覆盖,逐个 Mach-O 签(软链接跳过)
find "$APP/Contents/Resources/engine" -type f \( -name '*.dylib' -o -name 'llama-server' \) -print0 | xargs -0 -n1 "${SIGN[@]}"
"${SIGN[@]}" "$APP/Contents/MacOS/Humanizer"
"${SIGN[@]}" "$APP"
codesign --verify --deep --strict "$APP" && echo "  签名校验通过"

echo "· 打 dmg"
DMG_ROOT="$STAGE/dmg"
mkdir -p "$DMG_ROOT"
ditto "$APP" "$DMG_ROOT/Humanizer.app"
ln -s /Applications "$DMG_ROOT/Applications"
cp packaging/macos/打不开怎么办.txt "$DMG_ROOT/" 2>/dev/null || true
DMG="dist/Humanizer-$VERSION-macos-arm64.dmg"
rm -f "$DMG"
hdiutil create -volname "Humanizer" -srcfolder "$DMG_ROOT" -ov -format UDZO -fs APFS "$DMG" >/dev/null
if [ -n "${MACOS_SIGN_IDENTITY:-}" ]; then codesign --force --sign "$MACOS_SIGN_IDENTITY" --timestamp "$DMG"; fi

if [ -n "${MACOS_SIGN_IDENTITY:-}" ] && [ -n "${APPLE_ID:-}" ] && [ -n "${APPLE_TEAM_ID:-}" ] && [ -n "${APPLE_APP_PASSWORD:-}" ]; then
  echo "· 公证"
  xcrun notarytool submit "$DMG" --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" --password "$APPLE_APP_PASSWORD" --wait
  xcrun stapler staple "$DMG"
fi
ls -lh "$DMG"
