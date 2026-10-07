# update_e2e.sh / update_shots.sh 共用:造假的 Humanizer.app(真启动器 + 假引擎)和 dmg。只在 macOS 上用。
# 用法:source devtools/update_lib.sh(当前目录要是 app/)

# make_bundle <输出的 .app> <版本> <启动器二进制> <引擎二进制>
# 结构和签名方式都照 packaging/macos/build_app.sh(ad-hoc 签名)。
make_bundle() {
  local app="$1" ver="$2" exe="$3" eng="$4"
  rm -rf "$app"
  mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources/engine/metal"
  cp "$exe" "$app/Contents/MacOS/Humanizer"
  cp "$eng" "$app/Contents/Resources/engine/metal/llama-server"
  sed -e "s/__VERSION__/$ver/g" -e "s/__BUNDLE_ID__/io.github.sgaofen.humanizer.devtest/g" packaging/macos/Info.plist > "$app/Contents/Info.plist"
  codesign --force --sign - "$app/Contents/Resources/engine/metal/llama-server" >/dev/null 2>&1
  codesign --force --sign - "$app/Contents/MacOS/Humanizer" >/dev/null 2>&1
  codesign --force --sign - "$app" >/dev/null 2>&1
  codesign --verify --deep --strict "$app"
}

# make_dmg <.app> <输出 dmg>:和正式包一样带 Applications 快捷方式。
make_dmg() {
  local app="$1" out="$2" root
  root="$(mktemp -d)"
  ditto "$app" "$root/Humanizer.app"
  ln -s /Applications "$root/Applications"
  rm -f "$out"
  hdiutil create -volname Humanizer -srcfolder "$root" -ov -format UDZO "$out" >/dev/null
  rm -rf "$root"
}

# 假 dmg(只用来截图时演示下载进度):随机内容 + UDIF 尾块魔数
fake_dmg() {
  local out="$1" mib="$2"
  head -c $((mib * 1048576)) /dev/urandom > "$out"
  printf 'koly' | dd of="$out" bs=1 seek=$((mib * 1048576 - 512)) conv=notrunc 2>/dev/null
}
