#!/usr/bin/env bash
# Install the FlowDrop desktop app for the CURRENT USER. No sudo, no autostart,
# no background service: it only places the binary, icon and a .desktop entry
# in ~/.local so the desktop environment can launch it.
#
# Works in two layouts, with no source checkout or build required:
#   - git checkout:      scripts/install-desktop.sh   (binary from dist/)
#   - release tarball:   ./install-desktop.sh         (binary next to this file)
# The release tarball already ships this script, the .desktop template and the
# icons under deploy/desktop/, so extracting it is enough.
#
# Usage:
#   ./install-desktop.sh                     # install the app next to this script
#   ./install-desktop.sh --binary PATH       # install a specific build
#   ./install-desktop.sh --uninstall         # remove what it installed
#
# Layout (all user-level, no sudo):
#   ~/.local/bin/flowdrop-desktop
#   ~/.local/share/icons/hicolor/256x256/apps/flowdrop-desktop.png
#   ~/.local/share/applications/flowdrop-desktop.desktop
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Release tarball: this script sits next to the binary and deploy/desktop/.
# Git checkout: it lives in scripts/ and assets are one level up.
if [ -f "$SCRIPT_DIR/deploy/desktop/flowdrop-desktop.desktop.in" ]; then
  ROOT="$SCRIPT_DIR"
else
  ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
fi
PREFIX="${XDG_DATA_HOME:-$HOME/.local/share}"
BIN_DIR="$HOME/.local/bin"
APPS_DIR="$PREFIX/applications"
ICON_DIR="$PREFIX/icons/hicolor"

BINARY=""
BINARY_SET=0
UNINSTALL=0

while [ $# -gt 0 ]; do
  case "$1" in
    --binary)
      [ $# -ge 2 ] || { echo "error: --binary needs a path" >&2; exit 2; }
      BINARY="$2"; BINARY_SET=1; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    -h|--help) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
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

# --- resolve what to install ------------------------------------------------
ASSET_DIR="$ROOT/deploy/desktop"
TEMPLATE="$ASSET_DIR/flowdrop-desktop.desktop.in"

if [ "$BINARY_SET" -eq 0 ]; then
  if [ -x "$ROOT/dist/flowdrop-desktop" ]; then
    BINARY="$ROOT/dist/flowdrop-desktop"          # git checkout after a build
  elif [ -x "$ROOT/flowdrop-desktop" ]; then
    BINARY="$ROOT/flowdrop-desktop"               # extracted release tarball
  else
    BINARY="$ROOT/dist/flowdrop-desktop"
  fi
fi

fail() { echo "error: $*" >&2; exit 1; }

# --- validate every prerequisite BEFORE writing anything --------------------
# A missing file must abort before the first install/copy, never leave a
# half-installed launcher behind.
[ -n "${HOME:-}" ] || fail "HOME is not set"
[ -x "$BINARY" ] || fail "desktop binary not found or not executable: $BINARY
  (in a git checkout run scripts/build-desktop.sh first, or pass --binary PATH)"
[ -f "$TEMPLATE" ] || fail "missing desktop entry template: $TEMPLATE
  (use the release tarball from releases/latest, which ships deploy/desktop/)"

ICON_COUNT=0
for size in 48 128 256; do
  if [ -f "$ASSET_DIR/flowdrop-$size.png" ]; then
    ICON_COUNT=$((ICON_COUNT + 1))
  fi
done
[ "$ICON_COUNT" -gt 0 ] || fail "missing application icons under $ASSET_DIR
  (use the release tarball from releases/latest, which ships deploy/desktop/)"

# Destination parents must be writable/creatable before we start copying.
for dir in "$BIN_DIR" "$APPS_DIR" "$ICON_DIR"; do
  if [ -d "$dir" ]; then
    [ -w "$dir" ] || fail "not writable: $dir"
  else
    probe="$dir"
    while [ ! -d "$probe" ]; do probe="$(dirname "$probe")"; done
    [ -w "$probe" ] || fail "cannot create $dir (not writable: $probe)"
  fi
done

# --- install ---------------------------------------------------------------
echo "==> installing binary to $BIN_DIR"
mkdir -p "$BIN_DIR"
install -m 0755 "$BINARY" "$BIN_DIR/$APP_ID"

echo "==> installing icons"
for size in 48 128 256; do
  src="$ASSET_DIR/flowdrop-$size.png"
  [ -f "$src" ] || continue
  mkdir -p "$ICON_DIR/${size}x${size}/apps"
  install -m 0644 "$src" "$ICON_DIR/${size}x${size}/apps/$APP_ID.png"
done

echo "==> installing .desktop entry"
mkdir -p "$APPS_DIR"
sed -e "s|@BINARY@|$BIN_DIR/$APP_ID|g" \
    -e "s|@APP_ID@|$APP_ID|g" \
    "$TEMPLATE" > "$APPS_DIR/$APP_ID.desktop"
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
  - No autostart entry and no background service were created; FlowDrop only
    runs when you launch it.
  - Runtime dependencies: GTK 3 and WebKitGTK 4.1 (package names below).
      Debian/Ubuntu: libgtk-3-0t64 libwebkit2gtk-4.1-0 libsoup-3.0-0
  - Hub data lives in \${XDG_DATA_HOME:-~/.local/share}/flowdrop
    Desktop preference (bind/port + this window's device token, mode 0600): \${XDG_CONFIG_HOME:-~/.config}/flowdrop/desktop.json
  - Upgrading: quit FlowDrop from the tray first ("退出 FlowDrop"), then run
    this script again; it overwrites the user-level files in place.
  - Uninstall with: "$ROOT/$(basename "$0")" --uninstall
    (keeps Hub data and settings)
MSG
