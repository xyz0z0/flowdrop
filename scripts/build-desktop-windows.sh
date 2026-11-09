#!/usr/bin/env bash
# Build the FlowDrop Windows desktop app (Wails v3 + WebView2) by cross
# compiling from Linux/macOS.
#
# Output: dist/flowdrop-desktop.exe         (amd64, default)
#         dist/flowdrop-desktop-arm64.exe   (--arm64)
#
# Requirements:
#   - go (toolchain only; no CGO, no wails3 CLI needed — plain `go build`)
#   - the desktop module is self-contained: everything Windows-specific
#     (tray, window class) is selected by `//go:build windows`
#
# After linking, tools/winres (a separate Go module, pure Go, no mingw/windres)
# stamps the exe with the application icon (desktop/assets/icon.png), a
# VS_VERSION_INFO resource (0.1.0.0) and an application manifest:
# asInvoker, PerMonitorV2 DPI awareness, longPathAware, supportedOS Win10/11.
# The stamping step is mandatory: if it fails, the build fails.
# Run `go mod download` once inside tools/winres on a machine whose module cache
# is empty (no network at build time otherwise).
#
# Runtime requirements on the Windows 11 x64 machine:
#   - WebView2 Runtime (preinstalled on Windows 11; otherwise install the
#     Evergreen Runtime from Microsoft)
#
# Usage:
#   scripts/build-desktop-windows.sh             # amd64, no console window
#   scripts/build-desktop-windows.sh --arm64     # arm64, no console window
#   scripts/build-desktop-windows.sh --console   # keep the console for stderr
#                                                # logs (debugging)
#   --arm64 and --console may be combined.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GOARCH_TARGET="amd64"
CONSOLE=0
for arg in "$@"; do
  case "$arg" in
    --arm64) GOARCH_TARGET="arm64" ;;
    --console) CONSOLE=1 ;;
    -h|--help) sed -n '2,29p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }
}
need go
need sha256sum

NAME="flowdrop-desktop.exe"
if [ "$GOARCH_TARGET" = "arm64" ]; then
  NAME="flowdrop-desktop-arm64.exe"
fi
OUT="$ROOT/dist/$NAME"

# -H windowsgui links the binary as a GUI subsystem app: no console window is
# created when the user starts it. --console omits it so stderr (slog warnings,
# "no tray", Hub start errors) stays visible in a terminal.
LDFLAGS="-s -w"
if [ "$CONSOLE" = "1" ]; then
  echo "==> console build: stderr stays visible (debug/QA)"
else
  LDFLAGS="-H windowsgui $LDFLAGS"
fi

echo "==> building desktop binary (GOOS=windows GOARCH=$GOARCH_TARGET)"
mkdir -p "$ROOT/dist"
# `go build` leaves an existing output file untouched when the new binary has
# the same content, which would feed an already stamped exe back into the
# stamper. Remove it first so every build starts from a clean, resource-less PE.
rm -f "$OUT"
(cd "$ROOT/desktop" && \
  CGO_ENABLED=0 GOOS=windows GOARCH="$GOARCH_TARGET" go build \
    -trimpath \
    -ldflags "$LDFLAGS" \
    -o "$OUT" .)

# --- stamp icon + version info + manifest ----------------------------------
# tools/winres is its own module so tc-hib/winres never enters desktop/go.mod.
STAMPER="$ROOT/tools/winres"
ICON="$ROOT/desktop/assets/icon.png"
VERSION="0.1.0.0"

if [ ! -f "$STAMPER/go.mod" ]; then
  echo "error: missing resource stamper module: $STAMPER/go.mod" >&2
  exit 1
fi
if [ ! -f "$ICON" ]; then
  echo "error: missing application icon: $ICON" >&2
  exit 1
fi

echo "==> stamping icon, version info and manifest ($VERSION)"
# A failure here aborts the build (set -e): shipping an icon-less exe silently
# is not an option, so there is no fallback path.
if ! (cd "$STAMPER" && go run . -exe "$OUT" -icon "$ICON" -arch "$GOARCH_TARGET" -version "$VERSION"); then
  echo "error: failed to stamp resources onto $OUT" >&2
  echo "hint: the stamper module needs its dependencies in the module cache:" >&2
  echo "      (cd tools/winres && go mod download)   # needs network access" >&2
  exit 1
fi

(cd "$STAMPER" && go run . -verify -exe "$OUT")

SIZE="$(stat -c %s "$OUT" 2>/dev/null || wc -c <"$OUT")"
SUM="$(sha256sum "$OUT" | awk '{print $1}')"

echo
echo "built:  $OUT"
echo "size:   $SIZE bytes"
echo "sha256: $SUM"
echo
echo "run it on Windows 11 (WebView2 Runtime required); --console builds keep stderr."
