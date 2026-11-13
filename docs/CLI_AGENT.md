# FlowDrop 命令行用法（给 LLM / agent / 脚本）

本文是**操作手册**：另一个 LLM 拿到这台机器上的 FlowDrop，只用命令行能做什么、怎么判断成败。
权威细节仍以 [DEPLOY.md §3、§3.1](DEPLOY.md) 为准；本文的每条命令都在 2026-11-10 用
`dist/flowdrop 0.1.0` 在临时 `data_dir` + `127.0.0.1:18099` 上实跑过（见 §7）。

---

## 1. 先认清三个东西

| 东西 | 是什么 | LLM 能用吗 |
|---|---|---|
| `flowdrop`（`dist/flowdrop`） | **同一个二进制**：`serve` 起 Hub，`login`/`send` 当客户端 | ✅ 唯一入口 |
| `flowdrop-desktop`（Wails GUI） | 桌面 App，进程内拉起同一个 Hub | ❌ 不要用于自动化 |
| HTTP `/api/v1/*` | REST + WebSocket | ✅ 需要 CLI 没覆盖的读操作时用 |

```bash
scripts/build.sh              # 产出 dist/flowdrop（前端已嵌入）
./dist/flowdrop version       # flowdrop 0.1.0
./dist/flowdrop help          # 子命令一览
```

子命令只有这些：`serve`（默认）、`pair`、`doctor`、`backup`、`login`、`send`、`version`、`help`。
**没有** `list` / `download` / `delete` 子命令——读取和删除走 §5 的 HTTP API 或桌面/Web 界面。

---

## 2. 服务端：把 Hub 跑起来

```bash
./dist/flowdrop serve -config /path/to/config.json     # 前台运行；不给 -config 用默认路径
./dist/flowdrop doctor -config /path/to/config.json    # 体检，先跑这个
```

配置优先级：内置默认值 → JSON 文件 → `FLOWDROP_*` 环境变量（**env 最高**）。

关键配置项（JSON 字段 / 环境变量）：

| 字段 | env | 默认 | 说明 |
|---|---|---|---|
| `listen` | `FLOWDROP_LISTEN` | `127.0.0.1:8080` | 主监听；LAN 明文模式绑**具体 LAN IP**，别用 `0.0.0.0` |
| `additional_listen` | `FLOWDROP_ADDITIONAL_LISTEN` | 空 | 额外地址，**全有或全无**，必须字面 IP，同端口可不同 IP |
| `auto_bind` | `FLOWDROP_AUTO_BIND` | false | 同端口自动绑定本机全部 RFC1918 + Tailscale IPv4，5s 重扫热更新 |
| `data_dir` | `FLOWDROP_DATA_DIR` | `~/.local/share/flowdrop` | `flowdrop.db` + `blobs/` + `tmp/` |
| `allowed_hosts` | `FLOWDROP_ALLOWED_HOSTS` | 空（仅回环名） | Host 白名单；LAN/Tailscale 访问必须登记，否则 **421** |
| `trusted_proxies` | `FLOWDROP_TRUSTED_PROXIES` | 空 | 信任 `X-Forwarded-*` 的 IP/CIDR |
| `cookie_secure` | `FLOWDROP_COOKIE_SECURE` | **true** | 明文 HTTP LAN 必须设 `false`，否则浏览器丢 Cookie、登录"成功"但后续全 401 |
| `max_upload_bytes` | `FLOWDROP_MAX_UPLOAD_BYTES` | 512 MiB | 单文件上限 |
| `discovery_enabled` | `FLOWDROP_DISCOVERY_ENABLED` | 自动 | mDNS `_flowdrop._tcp` 广播开关 |
| `session_ttl_hours` / `pair_limit_per_minute` / `api_limit_per_minute` | 同名 `FLOWDROP_*` | 168 / 5 / 600 | 会话 TTL 与两个限流 |

`flowdrop doctor` 输出逐项 `ok`/`WARN`，并在任何 WARN 时**返回退出码 1**：

```
FlowDrop doctor 0.1.0
listen                 ok   127.0.0.1:18099
auto_bind              ok   off
data_dir               ok   /tmp/.../data
database               ok   schema v2
pairing                ok   hub id QzsJZtQGtKTltAiG9nBNCg
paired devices         ok   0 paired (normal until a device pairs)
free space             ok   14 GiB
port available         ok   can bind 127.0.0.1:18099
mdns discovery         ok   off ... (loopback 不参与广播)
```

> ⚠️ **Hub 已经在跑时 `doctor` 会 WARN `port available` 并 rc=1**。这不是故障，
> 是端口被自己占用。自动化里不要把它当失败处理（判据：看具体行，不只看 rc）。

配对码与备份：

```bash
flowdrop pair -config cfg.json     # 只在终端打印四位码，120s 有效、单次；不启动第二个 Hub
flowdrop backup out.tar.gz         # 在线一致性快照（VACUUM INTO + blobs/tmp）
```

配对码**只能在本机产生**，没有远程生成接口；桌面设置窗口「添加设备」或上面这条命令。

---

## 3. 客户端：把本机登记成设备（login）

```bash
flowdrop login -config ~/.config/flowdrop/config.json
# hub          http://127.0.0.1:8080
# device       cli-<主机名>
# device_id    <id>
# credentials  ~/.config/flowdrop/cli.json
```

- hub 地址来源优先级：`--hub` → `FLOWDROP_HUB` → 本地 config 的 `listen` 推导
  （通配地址/空 host 视作 `127.0.0.1`；`cookie_secure=true` 时用 `https://`）。
- **不传 `--code` = 同机路径**：直接在共享数据库里生成配对码，不打印、不过网络。
  前提是 `data_dir` 与运行中的 Hub 相同。
- 异机/远程：`--code <四位码>`（码来自桌面窗口或 Hub 本机 `flowdrop pair`）。
- 其他参数：`--name`（设备名，默认 `cli-<主机名>`，≤64 字节）、`--credentials PATH`
  （默认 `$FLOWDROP_CLI_CONFIG` 或 `~/.config/flowdrop/cli.json`，0600）。
- 这个令牌就是**一台已配对设备**，可在桌面设置里撤销；撤销后 `send` 报
  `device token was rejected`，重新 `login` 即可。

---

## 4. 投递：`flowdrop send`（LLM 最常用的部分）

三种形态**必须恰好给一种**：文件列表 / `--text` / `--link`。

```bash
flowdrop send FILE...                      # 多个文件逐项独立处理
flowdrop send --text "一句话"              # 发文字
printf 'body' | flowdrop send --text -     # 从 stdin 发文字（≤64 KiB = 65536 B）
flowdrop send --link https://example.com/x # 发链接（仅 http/https）
printf 'data' | flowdrop send --name note.bin -   # stdin 当文件上传，- 与 --name 必须成对
```

通用参数：`--hub URL`（覆盖凭据里的 hub）、`--credentials PATH`、`--json`。
（`--config` 只是为了和其他子命令对齐而接受，`send` 不使用它。）

### 输出

**默认**（人类可读）：成功一行到 stdout，失败一行 `输入: code: message` 到 stderr。

```
/tmp/x/f.txt  7 B  sha256=239f59ed…a935e5  id=1lyL5hoScIyre-74gmqvIA
```

**`--json`**：每个输入一行 NDJSON，stdout 只有这些记录。

```json
{"ok":true,"input":"stdin","item":{"id":"7JteRo0celGyEWejT3cB1Q","type":"text","text":"hello from agent","created_at":1794354048415}}
{"ok":false,"input":"/no/such/file","error":{"code":"stat_failed","message":"stat /no/such/file: no such file or directory"}}
```

文件项的 `item` 形如：
`{"type":"file","blob":{"name":"note.bin","size_bytes":4,"sha256":"…","content_type":"text/plain"}}`。

### 退出码与语义（判成败的唯一依据）

| rc | 含义 |
|---|---|
| `0` | 全部成功 |
| `1` | 至少一项失败，或未登录/令牌被撤销，或连不上 Hub |
| `2` | 用法错误（没给输入、`--text` 与 `--link` 同时给、`--name` 配了真实文件、`-` 给两次…） |

- **不自动重试**（配对码单次有效、上传不幂等）。要重试由调用方决定。
- 遇到限流（HTTP 429 / `rate_limited`）会**停止后续输入**，剩余项标
  `{"ok":false,"error":{"code":"skipped","message":"not sent: rate limited"}}`。
- 单项上传超时 10 分钟，连接/配对超时 30 秒。

### 不落盘的临时调用（CI / 一次性 agent 任务）

```bash
FLOWDROP_HUB=http://127.0.0.1:8080 FLOWDROP_TOKEN=<设备令牌> \
  flowdrop send --json ./report.pdf
```

两个变量同时存在时**优先于** `cli.json`，令牌不写任何文件；`--hub` 仍可覆盖地址。
`cookie_secure=true` 的 Hub 必须给 `https://`。

---

## 5. 读收件箱 / 下载 / 删除：走 HTTP API

CLI 只管"投递"。需要列出、下载、删除时用 API；**同一枚设备令牌**同时支持
`Authorization: Bearer` 和 Cookie。写操作额外要 CSRF 双提交。

先换 Cookie 并拿 CSRF：

```bash
TOKEN=$(python3 -c "import json;print(json.load(open('$HOME/.config/flowdrop/cli.json'))['device_token'])")
CSRF=$(curl -s -c jar -H "Authorization: Bearer $TOKEN" \
  http://127.0.0.1:8080/api/v1/auth/session \
  | python3 -c "import json,sys;print(json.load(sys.stdin)['csrf_token'])")
```

然后：

```bash
# 最近的条目（倒序；limit 1..100，默认 30；翻页用返回的 next_cursor）
curl -s -b jar "http://127.0.0.1:8080/api/v1/items?limit=10"

# 单条 / 下载 / 预览
curl -s -b jar "http://127.0.0.1:8080/api/v1/items/<id>"
curl -s -b jar -o out.bin "http://127.0.0.1:8080/api/v1/items/<id>/download"
curl -s -b jar "http://127.0.0.1:8080/api/v1/items/<id>/preview"

# 发文字（也可 type=link）；需要 CSRF
curl -s -b jar -H "X-FlowDrop-CSRF: $CSRF" -H 'Content-Type: application/json' \
  -d '{"type":"text","text":"from curl"}' http://127.0.0.1:8080/api/v1/items/text

# 删除
curl -s -b jar -X DELETE -H "X-FlowDrop-CSRF: $CSRF" \
  "http://127.0.0.1:8080/api/v1/items/<id>"

# 上传文件（multipart，字段名 file）
curl -s -b jar -H "X-FlowDrop-CSRF: $CSRF" -F "file=@./a.bin" \
  http://127.0.0.1:8080/api/v1/items/upload
```

端点全集：`GET /health`（**无鉴权**，返回 `hub_id`，可用于探活/防误连）、
`POST /auth/pair`、`GET /auth/session`、`POST /auth/logout`、`GET /items`、
`POST /items/text`、`POST /items/upload`、`GET|DELETE /items/{id}`、
`GET /items/{id}/download`、`GET /items/{id}/preview`、`GET /events`（WebSocket）。

约束：文字 ≤64 KiB 且合法 UTF-8；链接 ≤4096 B、必须是绝对 `http(s)` URL；
`Host` 不在 `allowed_hosts` 里直接 **421**；写操作缺/错 CSRF → 403。

---

## 6. 给 LLM 的推荐调用模板

**往收件箱丢一份结果（最常用）**

```bash
set -o pipefail
FLOWDROP_HUB=${FLOWDROP_HUB:-http://127.0.0.1:8080} \
FLOWDROP_TOKEN=$FLOWDROP_TOKEN \
  flowdrop send --json --name report.md - < report.md
# rc=0 且 NDJSON 有 "ok":true 才算成功；rc=1 读 error.code/error.message
```

**自检 → 投递 → 校验**（agent 收尾用）

```bash
flowdrop doctor -config "$CFG"                      # 只看具体行，忽略"Hub 在跑"导致的 port WARN
flowdrop login -config "$CFG" --name "agent-$(date +%s)"
flowdrop send --json ./out/app.apk
curl -s -b jar "http://127.0.0.1:8080/api/v1/items?limit=1"   # 确认最新一条就是刚发的
```

**规则**

1. 一次调用只做一件事：`login` 一次，之后复用凭据；不要每轮重登（每次登录都是一台新设备）。
2. 永远加 `--json` 并解析 NDJSON，不要 grep 人类可读输出。
3. 用 rc + `ok` 字段双重判定；`skipped` 表示限流后未发送，不是"已发送"。
4. 文本优先 `--text -`（走 stdin，避免 shell 转义/长度问题），>64 KiB 请当文件发。
5. 别去操作桌面 App / Web UI；需要读操作就 §5。

---

## 7. 本轮实测记录（证据）

环境：`dist/flowdrop 0.1.0`，临时 `data_dir`，`listen=127.0.0.1:18099`，
`cookie_secure=false`，`allowed_hosts=["127.0.0.1"]`。

| 步骤 | 结果 |
|---|---|
| `GET /api/v1/health` | 200，含 `hub_id`、`service=flowdrop-hub` |
| `doctor`（Hub 运行中） | 各项 ok；`port available` WARN → rc=1（预期） |
| `login -config … --credentials …` | rc=0，写出 `cli.json`，`device=cli-<主机名>` |
| `send FILE` | rc=0，行内 `sha256`/`id` |
| `send --json --text -`（stdin） | rc=0，`{"ok":true,"input":"stdin",…}` |
| `send --json --link https://…` | rc=0，`type":"link"` |
| `send --json --name note.bin -` | rc=0，`blob.name=note.bin, size_bytes=4` |
| `send --json f.txt /no/such/file` | 第一项 ok、第二项 `stat_failed`，rc=1 |
| `FLOWDROP_HUB/TOKEN send --json --text` | rc=0，不落盘 |
| Bearer → `/auth/session` → csrf → `GET /items` | 200，含上述条目 |
| `POST /items/text` + `X-FlowDrop-CSRF` | 201，返回新 item |
| `GET /items/{id}/download` | 200，7 B，内容与上传一致 |
| `-F file=@a.bin` 上传 → 下载 → `DELETE` | 201 / 200（`cmp` 一致）/ 204，之后列表为空 |
| `Host: evil.example` | 421 |
| 写操作不带 CSRF 头 | 403 |

未验证（不在本轮范围）：`pair` 输出的四位码 + 异机 `--code` 登录的完整链路（§3.1 有历史实测命令）、
限流 429 的真实触发、WebSocket `/events`、HTTPS/`cookie_secure=true` 组合、Android/桌面端。
