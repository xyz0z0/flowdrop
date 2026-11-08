#!/usr/bin/env bash
# FlowDrop Hub — 可信家庭局域网明文 HTTP 启动辅助。
#
# 只做这几件事：选一个本机局域网 IPv4、显式设置 FLOWDROP_LISTEN /
# FLOWDROP_ALLOWED_HOSTS、关闭 Secure Cookie，然后启动 Hub；发现默认交给配置，
# 可用 --no-discovery 关闭。不修改防火墙、不写全局配置、不改动 Tailscale/证书。
#
# 用法：
#   scripts/lan-http.sh                 # 自动探测默认路由的局域网 IP，端口 8080
#   scripts/lan-http.sh --host 192.168.1.50 --port 8080
#   scripts/lan-http.sh --name flowdrop.local   # 额外允许手机用 mDNS 名访问
#   scripts/lan-http.sh --no-discovery          # 关闭 _flowdrop._tcp 自动发现
#   scripts/lan-http.sh --config ~/.config/flowdrop/config.json
#   scripts/lan-http.sh --binary ~/.local/bin/flowdrop
#
# 局域网自动发现：绑定具体 RFC1918 局域网 IPv4 且关闭 Secure Cookie 时，
# Hub 默认发布 _flowdrop._tcp.local（只含该网卡地址，不含密码/内容）。
# Tailscale 100.64/10、回环、通配地址不会广播。需要关闭时用
# --no-discovery 或 FLOWDROP_DISCOVERY_ENABLED=false。
#
# 安全边界：拒绝 0.0.0.0/:: 与公网 IP，只接受本机网卡上的私有地址。
# 明文模式下密码、会话 Cookie、传输内容都可能被同一 Wi-Fi 的其他人监听；
# 不可信网络请用 Tailscale Serve 或 TLS 反向代理，不要用本脚本。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CALLER_PWD="$PWD"
cd "$ROOT"

HOST=""
PORT="8080"
NAME=""
CONFIG=""
BINARY=""
NO_DISCOVERY=0

usage() {
  sed -n '2,23p' "${BASH_SOURCE[0]}" | sed -n 's/^# \{0,1\}//p'
}

while [ $# -gt 0 ]; do
  case "$1" in
    --host) HOST="${2:-}"; shift 2 ;;
    --port) PORT="${2:-}"; shift 2 ;;
    --name) NAME="${2:-}"; shift 2 ;;
    --no-discovery) NO_DISCOVERY=1; shift ;;
    --config) CONFIG="${2:-}"; shift 2 ;;
    --binary) BINARY="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "未知参数：$1（--help 查看用法）" >&2; exit 2 ;;
  esac
done

# 1) 选定局域网 IPv4。默认取默认路由的源地址（不会选到 docker/tailscale）。
if [ -z "$HOST" ]; then
  if command -v ip >/dev/null; then
    HOST="$(ip -4 route get 1.1.1.1 2>/dev/null | sed -n 's/.* src \([0-9.]\+\).*/\1/p' | head -1)"
  fi
  if [ -z "$HOST" ] && command -v hostname >/dev/null; then
    HOST="$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^[0-9.]+$' | head -1 || true)"
  fi
fi
if [ -z "$HOST" ]; then
  echo "无法自动探测局域网 IP。请显式指定： scripts/lan-http.sh --host 192.168.1.50" >&2
  exit 1
fi

# 2) 只允许私有地址；公网地址很可能把明文 HTTP 暴露到不可信网络。
case "$HOST" in
  10.*|192.168.*|169.254.*) ;;
  172.1[6-9].*|172.2[0-9].*|172.3[01].*) ;;
  100.6[4-9].*|100.[7-9][0-9].*|100.1[01][0-9].*|100.12[0-7].*) ;; # Tailscale CGNAT
  *)
    echo "拒绝启动：$HOST 不是私有/局域网地址。明文 HTTP 只允许可信家庭网络。" >&2
    echo "如确需其他网卡地址，请自行显式设置 FLOWDROP_* 环境变量启动。" >&2
    exit 1
    ;;
esac

if ! [[ "$PORT" =~ ^[0-9]+$ ]] || [ "$PORT" -lt 1 ] || [ "$PORT" -gt 65535 ]; then
  echo "端口无效：$PORT" >&2
  exit 1
fi

# 3) 确认该地址确实在本机网卡上（防止手误写成一个不可绑定的地址）。
if command -v ip >/dev/null; then
  if ! ip -4 -o addr show 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | grep -qx "$HOST"; then
    echo "拒绝启动：$HOST 不在本机任何网卡的 IPv4 地址上。" >&2
    echo "当前地址：" >&2
    ip -4 -o addr show 2>/dev/null | awk '{print "  " $2 " " $4}' >&2
    exit 1
  fi
fi

LISTEN="$HOST:$PORT"
ALLOWED="$HOST"
if [ -n "$NAME" ]; then
  ALLOWED="$ALLOWED,$NAME"
fi

# 4) 显式覆盖本次进程的环境变量（不改任何配置文件）。
export FLOWDROP_LISTEN="$LISTEN"
export FLOWDROP_ALLOWED_HOSTS="$ALLOWED"
export FLOWDROP_COOKIE_SECURE=false
# 明文直连没有反代；清掉可能从 shell 继承的可信代理，避免伪造 X-Forwarded-*。
unset FLOWDROP_TRUSTED_PROXIES || true

if [ "$NO_DISCOVERY" = 1 ]; then
  export FLOWDROP_DISCOVERY_ENABLED=false
fi

# 5) 找二进制：显式 --binary > dist/flowdrop > go run。
# 相对路径按调用时的当前目录解析（下面可能切到 server/ 执行 go run）。
abs_path() {
  case "$1" in
    /*) printf '%s' "$1" ;;
    *) printf '%s/%s' "$CALLER_PWD" "$1" ;;
  esac
}
if [ -n "$CONFIG" ]; then
  CONFIG="$(abs_path "$CONFIG")"
  if command -v realpath >/dev/null; then
    CONFIG="$(realpath -m "$CONFIG")"
  fi
fi
if [ -n "$BINARY" ]; then
  BINARY="$(abs_path "$BINARY")"
fi

if [ -n "$BINARY" ]; then
  RUN=("$BINARY")
elif [ -x "$ROOT/dist/flowdrop" ]; then
  RUN=("$ROOT/dist/flowdrop")
elif command -v go >/dev/null; then
  RUN=(go run ./cmd/flowdrop)
else
  echo "找不到 dist/flowdrop，也没有 go。先运行 scripts/build.sh，或用 --binary 指定路径。" >&2
  exit 1
fi

SERVE_ARGS=(serve)
if [ -n "$CONFIG" ]; then
  SERVE_ARGS+=(-config "$CONFIG")
fi

cat <<EOF

────────────────────────────────────────────────────────────────────
FlowDrop 局域网明文 HTTP 模式（仅用于可信家庭网络）

  手机/浏览器访问： http://$HOST:$PORT
  监听地址：        $LISTEN（已校验为私有地址且在本机网卡上）
  AllowedHosts：    $ALLOWED
  CookieSecure：    false
  LAN 自动发现：    $([ "$NO_DISCOVERY" = 1 ] && echo '关闭（--no-discovery）' || echo '默认开启（仅符合条件的 RFC1918 地址发布）')

  同一 Wi-Fi 内的其他人可能监听密码、会话 Cookie 和传输内容。
  不可信网络请改用 Tailscale Serve 或 TLS 反向代理（本脚本不涉及）。

  首次使用需先初始化管理员密码：
    ${RUN[*]} admin init${CONFIG:+ -config $CONFIG}
────────────────────────────────────────────────────────────────────

EOF

if [ "${RUN[0]}" = "go" ]; then
  cd "$ROOT/server"
fi
exec "${RUN[@]}" "${SERVE_ARGS[@]}"
