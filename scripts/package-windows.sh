#!/usr/bin/env bash
# Package the FlowDrop Windows desktop app into a single zip for delivery.
#
# Output: dist/flowdrop-windows-amd64.zip   (default)
#         dist/flowdrop-windows-arm64.zip   (--arm64)
#
# Contents of the zip:
#   flowdrop-desktop.exe   the GUI binary (no console window)
#   README.txt             short Chinese guide: how to run, firewall prompt,
#                          WebView2 Runtime, where data/logs live, how to quit
#
# The binary itself is produced by scripts/build-desktop-windows.sh; this
# script never re-implements the build, it only calls it and then zips.
#
# Requirements: go, zip, sha256sum (plus whatever build-desktop-windows.sh
# needs — currently only the go toolchain, no CGO, no wails3 CLI).
#
# Usage:
#   scripts/package-windows.sh             # amd64
#   scripts/package-windows.sh --arm64     # arm64
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

TARGET_ARCH="amd64"
for arg in "$@"; do
  case "$arg" in
    --arm64) TARGET_ARCH="arm64" ;;
    -h|--help) sed -n '2,24p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }
}
need go
need zip
need sha256sum

# --- 1. build (delegated: the build script owns the go build invocation) ----
echo "==> building Windows binary ($TARGET_ARCH)"
if [ "$TARGET_ARCH" = "arm64" ]; then
  scripts/build-desktop-windows.sh --arm64
  EXE="$ROOT/dist/flowdrop-desktop-arm64.exe"
else
  scripts/build-desktop-windows.sh
  EXE="$ROOT/dist/flowdrop-desktop.exe"
fi

if [ ! -f "$EXE" ]; then
  echo "error: expected build output missing: $EXE" >&2
  exit 1
fi

# --- 2. stage: exe + README.txt under a fixed in-zip name ------------------
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# The arm64 build is named *-arm64.exe on disk but the zip always contains
# flowdrop-desktop.exe, so the instructions work unchanged for both packages.
cp "$EXE" "$STAGE/flowdrop-desktop.exe"

cat >"$STAGE/README.txt" <<'EOF'
FlowDrop 流转 — Windows 桌面版
==============================

怎么运行
--------
解压后双击 flowdrop-desktop.exe 即可，不需要安装。
首次启动 Windows 会弹出"Windows 安全警报 / 防火墙"对话框，
请勾选"专用网络"然后点"允许访问"，否则同一局域网内的手机
无法找到这台电脑，接收就会失败（只在本机收发则不影响）。

需要 WebView2 Runtime
---------------------
界面由 WebView2 渲染。Windows 11 已自带，无需安装。
如果启动后窗口空白或提示缺少 WebView2，请到微软官网安装
"Microsoft Edge WebView2 Runtime"(Evergreen) 后重试。

图标、版本与高 DPI
------------------
flowdrop-desktop.exe 已内嵌应用图标和版本信息（右键→属性→详细信息
可以看到版本 0.1.0.0、产品名 FlowDrop），并声明为 PerMonitorV2
高 DPI 感知：在缩放不是 100% 的显示器上窗口按真实比例渲染，
位图不会因为拉伸而发虚。

数据、配置和日志位置
--------------------
数据(收件箱文件、数据库)：
  %LocalAppData%\flowdrop\
配置(端口、设备名等设置)：
  %AppData%\flowdrop\
日志：
  %LocalAppData%\flowdrop\desktop.log
反馈问题时请把 desktop.log 一并附上。

怎么退出
--------
关闭窗口只是隐藏到托盘，Hub 仍在后台运行(这样才能持续收件)。
要彻底退出：右键托盘区的 FlowDrop 图标 → "退出 FlowDrop"。

关于安全提示
------------
本程序未做代码签名，第一次运行时 Windows SmartScreen 可能提示
"Windows 已保护你的电脑"。点"更多信息"→"仍要运行"即可。
EOF

# --- 3. zip ----------------------------------------------------------------
NAME="flowdrop-windows-$TARGET_ARCH.zip"
OUT="$ROOT/dist/$NAME"
rm -f "$OUT"
mkdir -p "$ROOT/dist"
# -X drops extra file attributes (uid/gid/timestamps) so the archive is
# reproducible-ish and carries no build-machine metadata.
(cd "$STAGE" && zip -q -X "$OUT" flowdrop-desktop.exe README.txt)

SIZE="$(stat -c %s "$OUT" 2>/dev/null || wc -c <"$OUT")"
SUM="$(sha256sum "$OUT" | awk '{print $1}')"

echo
echo "packaged: $OUT"
echo "size:     $SIZE bytes"
echo "sha256:   $SUM"
echo
echo "unzip on Windows 11 and run flowdrop-desktop.exe (see README.txt)."
