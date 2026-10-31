#!/bin/sh
# Optional: add Humanizer to the application menu for the portable tarball.
# Usage (from the extracted folder):  sh install-desktop.sh
set -eu
DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
[ -x "$DIR/humanizer" ] || { echo "humanizer is not executable; run: chmod +x \"$DIR/humanizer\"" >&2; exit 1; }
APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/1024x1024/apps"
mkdir -p "$APPS" "$ICONS"
cp "$DIR/humanizer.png" "$ICONS/humanizer.png"
# Generate the entry directly instead of sed-rewriting paths:
# folder names with &, |, quotes or spaces must survive verbatim.
{
  printf '[Desktop Entry]\n'
  printf 'Type=Application\n'
  printf 'Name=Humanizer\n'
  printf 'Comment=Rewrite AI drafts so they read like a person wrote them\n'
  printf 'Exec="%s/humanizer"\n' "$DIR"
  printf 'Icon=%s/humanizer.png\n' "$ICONS"
  printf 'Terminal=false\n'
  printf 'Categories=Office;Utility;\n'
  printf 'StartupNotify=true\n'
} > "$APPS/humanizer.desktop"
chmod +x "$APPS/humanizer.desktop"
echo "Installed: $APPS/humanizer.desktop"
echo "Open Humanizer from your application menu (keep this folder where it is)."
