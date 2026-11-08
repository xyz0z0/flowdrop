#!/usr/bin/env bash
# Install the FlowDrop desktop app for the CURRENT USER. No sudo, no autostart,
# no background service: it only places the binary, icon and a .desktop entry
# in ~/.local so the desktop environment can launch it.
#
# Usage:
#   scripts/install-desktop.sh                    # install from dist/flowdrop-desktop
#   scripts/install-desktop.sh --binary PATH      # install a specific build
#   scripts/install-desktop.sh --uninstall        # remove what it installed
#
# Layout:
#   ~/.local/bin/flowdrop-desktop
#   ~/.local/share/icons/hicolor/256x256/apps/flowdrop-desktop.png
#   ~/.local/share/applications/flowdrop-desktop.desktop
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PREFIX="${XDG_DATA_HOME:-$HOME/.local/share}"
BIN_DIR="$HOME/.local/bin"
APPS_DIR="$PREFIX/applications"
ICON_DIR="$PREFIX/icons/hicolor"

BINARY="$ROOT/dist/flowdrop-desktop"
UNINSTALL=0

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help) sed -n '2,16p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

APP_ID="flowdrop-desktop"

if [ "$UNINSTALL" -eq 1 ]; then
  rm -f "$BIN_DIR/$APP_ID" "$APPS_DIR/$APP_ID.desktop"
  for size in 48 128 256; do rm -f "$ICON_DIR/${size}x${size}/apps/$APP_ID.png"; done
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS_DIR" 2>/dev/null || true
  echo "removed FlowDrop desktop launcher (user data and Hub data are untouched)"
  echo "Hub data: ${XDG_DATA_HOME:-$HOME/.local/share}/flowdrop"
  exit 0
fi

[ -x "$BINARY" ] || { echo "error: $BINARY not found; run scripts/build-desktop.sh first" >&2; exit 1; }

echo "==> installing binary to $BIN_DIR"
mkdir -p "$BIN_DIR"
install -m 0755 "$BINARY" "$BIN_DIR/$APP_ID"

echo "==> installing icons"
for size in 48 128 256; do
  src="$ROOT/deploy/desktop/flowdrop-$size.png"
  [ -f "$src" ] || continue
  mkdir -p "$ICON_DIR/${size}x${size}/apps"
  install -m 0644 "$src" "$ICON_DIR/${size}x${size}/apps/$APP_ID.png"
done

echo "==> installing .desktop entry"
mkdir -p "$APPS_DIR"
sed -e "s|@BINARY@|$BIN_DIR/$APP_ID|g" \
    -e "s|@APP_ID@|$APP_ID|g" \
    "$ROOT/deploy/desktop/flowdrop-desktop.desktop.in" > "$APPS_DIR/$APP_ID.desktop"
chmod 0644 "$APPS_DIR/$APP_ID.desktop"

command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS_DIR" 2>/dev/null || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -f -t "$ICON_DIR" 2>/dev/null || true

cat <<MSG

installed:
  $BIN_DIR/$APP_ID
  $APPS_DIR/$APP_ID.desktop

Launch it from your application menu ("FlowDrop 流转") or run:
  $BIN_DIR/$APP_ID

Notes:
  - No sudo was used and nothing was installed system-wide.
  - No autostart entry was created; FlowDrop only runs when you launch it.
  - Runtime dependencies: GTK 3 and WebKitGTK 4.1 (package names below).
      Debian/Ubuntu: libgtk-3-0t64 libwebkit2gtk-4.1-0 libsoup-3.0-0
  - Hub data lives in \${XDG_DATA_HOME:-~/.local/share}/flowdrop
    Desktop preference (bind address/port only): \${XDG_CONFIG_HOME:-~/.config}/flowdrop/desktop.json
  - Uninstall with: scripts/install-desktop.sh --uninstall
MSG
