#!/usr/bin/env bash
# 正式构建：前端构建产物嵌入 Go 二进制 → dist/flowdrop
# 用法：scripts/build.sh [version]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-0.1.0}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo nogit)"
BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [ ! -d web/node_modules ]; then
  echo "→ 安装前端依赖…"
  (cd web && npm install --no-audit --no-fund)
fi

echo "→ 前端类型检查与构建…"
(cd web && npx tsc -b --force && npx vite build)

echo "→ 构建 Go 二进制…"
mkdir -p dist
(cd server && CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags "-s -w -X main.version=${VERSION} -X flowdrop/internal/api.Version=${VERSION}" \
  -o ../dist/flowdrop ./cmd/flowdrop)

echo
echo "构建完成： dist/flowdrop"
echo "  版本 $VERSION ($COMMIT) $BUILD_DATE"
echo
echo "自检： ./dist/flowdrop doctor"
echo "启动： ./dist/flowdrop serve"
