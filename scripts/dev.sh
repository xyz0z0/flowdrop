#!/usr/bin/env bash
# FlowDrop 开发启动：Go Hub (:8080) + Vite (:5173，代理到 Go)。
# 用法：scripts/dev.sh [-config PATH]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! command -v go >/dev/null; then
  echo "缺少 go。Kubuntu/Debian: sudo apt install golang-go" >&2
  exit 1
fi
if ! command -v npm >/dev/null; then
  echo "缺少 npm。安装 Node.js 20+（例如通过 nvm 或 apt install nodejs npm）。" >&2
  exit 1
fi

if [ ! -d web/node_modules ]; then
  echo "→ 安装前端依赖（首次）…"
  (cd web && npm install --no-audit --no-fund)
fi

CFG_ARGS=()
if [ "${1:-}" = "-config" ] && [ -n "${2:-}" ]; then
  CFG_ARGS=(-config "$2")
fi

cleanup() {
  [ -n "${GO_PID:-}" ] && kill "$GO_PID" 2>/dev/null || true
  [ -n "${VITE_PID:-}" ] && kill "$VITE_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

echo "→ 启动 Go Hub（http://127.0.0.1:8080）…"
(cd server && go run ./cmd/flowdrop serve "${CFG_ARGS[@]}") &
GO_PID=$!

# Wait for the hub to answer before starting Vite (proxy target must exist).
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null http://127.0.0.1:8080/api/v1/health 2>/dev/null; then
    break
  fi
  if ! kill -0 "$GO_PID" 2>/dev/null; then
    echo "Go Hub 启动失败" >&2
    exit 1
  fi
  sleep 0.5
done

echo "→ 启动 Vite（http://127.0.0.1:5173）…"
(cd web && npm run dev -- --host 127.0.0.1 --port 5173) &
VITE_PID=$!

echo
echo "开发地址： http://127.0.0.1:5173"
echo "首次使用： flowdrop admin init       （或：go run ./cmd/flowdrop admin init）"
echo "按 Ctrl+C 停止。"
wait
