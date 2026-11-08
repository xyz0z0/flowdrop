#!/usr/bin/env bash
# Provision a USER-LOCAL sysroot with the GTK3/WebKitGTK development files the
# Wails build needs, without root and without touching the host package
# database, /usr, or any system service.
#
# It downloads the same .deb packages `apt install libgtk-3-dev
# libwebkit2gtk-4.1-dev` would install, extracts them under
# ${FLOWDROP_DESKTOP_SYSROOT:-$HOME/.local/share/flowdrop-desktop/sysroot}, and
# repairs the few development symlinks so pkg-config and the C linker resolve
# them against the runtime libraries that ARE installed system-wide.
#
# Prefer the real packages when you have root:
#   sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
#
# Usage: scripts/desktop-dev-sysroot.sh [--force]
set -euo pipefail

SYSROOT="${FLOWDROP_DESKTOP_SYSROOT:-$HOME/.local/share/flowdrop-desktop/sysroot}"
FORCE=0
[ "${1:-}" = "--force" ] && FORCE=1

command -v apt-get >/dev/null || { echo "apt-get is required (Debian/Ubuntu)" >&2; exit 1; }
command -v dpkg-deb >/dev/null || { echo "dpkg-deb is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }

if [ -f "$SYSROOT/.complete" ] && [ "$FORCE" -eq 0 ]; then
  echo "sysroot already provisioned: $SYSROOT"
  echo "re-run with --force to rebuild it"
  exit 0
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "==> resolving package list (no install, no root)"
PKGS="libgtk-3-dev libwebkit2gtk-4.1-dev"
apt-get --print-uris -y install --no-install-recommends $PKGS \
  | grep -oE "^'http[^']+'" | tr -d "'" > "$WORK/urls.txt"

if [ ! -s "$WORK/urls.txt" ]; then
  echo "error: apt-get could not resolve the packages; check your sources.list" >&2
  exit 1
fi

echo "==> downloading $(wc -l < "$WORK/urls.txt") development packages"
mkdir -p "$WORK/debs"
while read -r url; do
  curl -fsSL --retry 3 -O --output-dir "$WORK/debs" "$url"
done < "$WORK/urls.txt"

if [ "$FORCE" -eq 1 ]; then rm -rf "$SYSROOT"; fi
mkdir -p "$SYSROOT"

echo "==> extracting into $SYSROOT"
for deb in "$WORK"/debs/*.deb; do
  dpkg-deb -x "$deb" "$SYSROOT"
done

echo "==> repairing development symlinks against installed runtime libraries"
for link in $(find "$SYSROOT/usr/lib" -xtype l); do
  target="$(basename "$(readlink "$link")")"
  for dir in /usr/lib/x86_64-linux-gnu /lib/x86_64-linux-gnu /usr/lib; do
    if [ -e "$dir/$target" ]; then
      ln -sfn "$dir/$target" "$link"
      break
    fi
  done
done

PKG_CONFIG_PATH="$SYSROOT/usr/lib/x86_64-linux-gnu/pkgconfig:$SYSROOT/usr/share/pkgconfig" \
  pkg-config --modversion gtk+-3.0 webkit2gtk-4.1 gio-unix-2.0 gdk-3.0

touch "$SYSROOT/.complete"
cat <<MSG

provisioned: $SYSROOT
build with:  scripts/build-desktop.sh
(runtime libraries stay the system ones; nothing was installed as root)
MSG
