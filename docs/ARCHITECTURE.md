# FlowDrop 架构说明（MVP）

状态：M0–M3 实现基线 + HTTP LAN + Android + Linux 桌面 + 单 Hub 多监听（LAN + Tailscale）
关联文档：[PRD.md](./PRD.md)（需求基线，含 §15 实施澄清、§16 范围澄清追加）

> **主路径变更（2026-11-07）**：家庭可信 LAN 内直接用**明文 HTTP**（Hub + Android 原生
> App + 电脑 Web），不需要 Tailscale/Caddy/证书；跨网用用户现有 Tailscale。
> 原 HTTPS 反代拓扑保留为可选加固。详见 PRD §16 与 DEPLOY §5–§8。

## 1. 总览

```text
Android 原生 App / 电脑浏览器
   |  HTTP（家庭可信 LAN，主路径）
   |  或 HTTPS（可选：Tailscale Serve / Caddy internal CA）
   v
FlowDrop Hub（Go 单进程）
   ├── net/http 路由 (Go 1.22 mux)
   ├── 中间件：Host/Origin 校验、会话认证、CSRF、限流
   ├── REST API  （权威状态：SQLite）
   ├── WebSocket  （仅变更通知，非存储）
   ├── 嵌入式前端  （React/Vite 构建产物，同源）
   └── 存储
        ├── SQLite  metadata（WAL）
        └── blobs/  附件（随机 key，磁盘）
```

设计原则：

1. **REST 权威、WS 通知**：页面先建 WS/或先拉列表都会以 REST 快照为准；WS 事件只触发增量或重新拉取。
2. **同源单进程**：生产前端由 Go 二进制嵌入，API/WS/下载同源，无 CORS，Cookie 天然 first-party。
3. **一个协议、两个客户端**：Android 原生 App 与 Web 使用同一套 `/api/v1/*` 与同一套
   Cookie/CSRF/Origin 规则；不为原生端新增认证端点。
4. **网络边界是显式选择**：默认仅回环；主路径显式配置 `FLOWDROP_LISTEN=<Hub-局域网 IP>:8080`
   + `FLOWDROP_COOKIE_SECURE=false`（`cookie_secure=false` 时通配监听 `0.0.0.0` / `::` / 空 host
   会被服务端拒绝），用户已接受可信家庭 LAN 的明文边界。需要同时支持 LAN 与 Tailscale 时，
   同一个 Hub 可再显式绑定一个 `additional_listen`（具体 IP:端口），仍拒绝通配。
   每个实际监听 IP 会自动加入 Host 白名单，`FLOWDROP_ALLOWED_HOSTS` 仅用于补充 mDNS 名 /
   主机名 / Tailscale MagicDNS 名。跨网/不可信网络仍用 Tailscale 或 TLS 反代。
5. **安全默认**：所有内容接口（含图片预览、下载、WS）统一会话鉴权；修改操作同时校验 Origin 与 CSRF 双提交令牌。
6. **能力边界要诚实**：非安全上下文下浏览器可能禁用剪贴板等 API；前端必须降级而不是报错或谎报成功。
7. **发现只描述真实入口**：mDNS 只发布当前绑定的具体 RFC1918 LAN IPv4、真实端口和
   `cookie_secure=false` 的 HTTP 入口（每个符合条件的 LAN 监听各一条）；回环、通配、
   Tailscale、链路本地和 HTTPS 反代不冒充 LAN 广播，记录中不含密码/会话/正文/机器路径。
8. **同一 Hub、多个监听**：多监听不是多个 Hub/多个数据目录；所有监听共享同一个
   HTTP 处理器、数据库、认证会话、事件广播和一把数据目录锁，启动时全部绑定成功才对外服务。

## 2. 目录结构

```text
flowdrop/
├── docs/
│   ├── PRD.md               # 需求基线（含 §15 实施澄清、§16 HTTP LAN 范围澄清）
│   ├── ARCHITECTURE.md      # 本文件
│   ├── PLAN.md              # 分阶段计划与验收
│   └── DEPLOY.md            # 部署 / HTTP LAN / TLS（可选） / 备份 / 升级 / 卸载
├── server/
│   ├── cmd/flowdrop/        # main：serve / admin init / doctor / version
│   ├── internal/api/        # 路由、中间件、handler、限流
│   ├── internal/auth/       # 密码哈希、会话
│   ├── internal/config/     # 配置加载（JSON + FLOWDROP_* 环境变量）
│   ├── internal/db/         # SQLite 打开与 migration
│   ├── internal/discovery/  # mDNS/DNS-SD 发布（纯 Go，限定绑定的 LAN 网卡）
│   ├── internal/events/     # WS broker
│   ├── internal/store/      # items / blobs 数据访问
│   ├── internal/storage/    # 磁盘布局、原子落盘、磁盘空间、清理
│   └── internal/webui/      # go:embed 前端产物
├── web/                     # React + TS + Vite 源码与构建
├── android/                 # Android 原生 App（并行开发中，未整合确认）
├── deploy/                  # systemd 用户服务、Caddy 模板（可选）
├── scripts/                 # dev.sh / build.sh / e2e
└── tests/                   # 端到端脚本（Playwright）
```

## 3. 数据模型

迁移文件为 Go 内嵌 SQL 字符串，按版本号顺序执行并记录到 `schema_migrations`。

| 表 | 关键字段 | 说明 |
|---|---|---|
| `admin_users` | `username` UNIQUE, `password_hash` | 单用户；哈希为 Argon2id PHC 字符串 |
| `sessions` | `id`(=SHA-256(token) hex), `expires_at`, `last_seen_at` | 明文令牌只存在于客户端 Cookie |
| `blobs` | `id`, `original_name`, `content_type`, `size_bytes`, `sha256`, `storage_key` | `storage_key` 为随机值，磁盘路径由它推导 |
| `inbox_items` | `id`, `type`, `text_content`, `blob_id`, `sender_device`, `created_at`, `expires_at` | `type ∈ {text,link,image,file}`；`expires_at` 保留不用（见 PRD §15.3） |

索引：`inbox_items(created_at DESC, id DESC)` 支持稳定倒序分页；`inbox_items(blob_id)` 支持引用检查；`sessions(expires_at)` 支持清理。

## 4. 认证与会话

- **初始化**：`flowdrop admin init`（本机 CLI，交互式两次输入，不回显）。不提供远程注册或远程初始化端点。
- **协议**：Web 与 Android 原生 App 使用**同一套** Cookie 会话 + CSRF 双提交 + Origin/Host 校验；
  原生端不引入自创认证端点，也不复用设备配对码。
- **哈希**：Argon2id，`m=64 MiB, t=3, p=2`，16B 随机盐，32B 输出，PHC 编码；校验使用常量时间比较。
- **会话**：32B 随机令牌 → base64url；DB 只存 `sha256(token)`；Cookie `flowdrop_session`（`HttpOnly`, `SameSite=Strict`, `Secure` 默认开启, `Path=/`）；TTL 默认 7 天，滑动续期（last_seen 更新有节流）。
  - 明文 HTTP LAN 主路径需 `cookie_secure=false`；HTTPS（Tailscale/Caddy）保持 `true`。
- **CSRF**：登录后下发 `flowdrop_csrf`（`HttpOnly=false`, `SameSite=Strict`）。所有 `POST/DELETE` 必须带 `X-FlowDrop-CSRF` 且与 Cookie 双提交一致；同时校验 `Origin`（若存在）和 `Host`。
- **Host 白名单**：回环名 + **每个具体的非通配监听 IP**（主监听与 `additional_listen`）始终允许；`allowed_hosts` 用于补充其它访问名，未命中返回 421（DNS rebinding 防护）。通配监听（`0.0.0.0`/`::`）不会自动加入任何 IP。Cookie 仍按 host 隔离，不承诺跨地址自动共享登录态。
- **代理信任**：默认不信任任何 `X-Forwarded-*`；仅当直连 IP 命中 `trusted_proxies` 才采用 `X-Forwarded-Proto/Host/For`。LAN HTTP 主路径没有代理，不配置该项。
- **限流**：内存令牌桶。登录按 IP（默认 5 次 / 10 分钟，成功后重置）；API 按会话；上传按会话单独更低阈值。
- **明文边界**：LAN HTTP 下内容与 Cookie 可被同网段观察；这是用户明确接受的权衡，**不得**把该端口映射到公网。
- **安全上下文**：`http://<LAN-IP>` 不是 secure context，`navigator.clipboard`、Service Worker、
  通知等 API 可能不可用；Web 端降级为手动选择文本（见第 8 节）。

## 5. 内容与 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/health` | 无认证；仅返回 `{status,version,time}` |
| POST | `/api/v1/auth/login` | 密码登录，下发会话 + CSRF |
| POST | `/api/v1/auth/logout` | 注销当前会话 |
| GET | `/api/v1/auth/session` | 当前会话状态（用于 SPA 启动判定） |
| GET | `/api/v1/items?cursor=&limit=` | 倒序稳定分页，`limit` 1..100（默认 30） |
| POST | `/api/v1/items/text` | `{type:"text"|"link", text}`，链接仅 http/https |
| POST | `/api/v1/items/upload` | multipart 流式上传，单文件一请求 |
| GET | `/api/v1/items/{id}` | 条目详情 |
| DELETE | `/api/v1/items/{id}` | 删除条目 + 清理附件 |
| GET | `/api/v1/items/{id}/download` | 强制 attachment 下载（鉴权） |
| GET | `/api/v1/items/{id}/preview` | 仅安全图片类型 inline；其他类型同下载 |
| GET | `/api/v1/events` | WebSocket：`item.created` / `item.deleted` |

分页游标为 `base64url(created_at|id)`，语义为“严格早于该条目”，因此并发插入不会造成重复或漏项。

## 6. 上传流水线

```text
multipart 流
  → MaxBytesReader(单文件上限)
  → 写入 data/tmp/<random> (io.MultiWriter + sha256)
  → 大小校验（0 字节允许；超限 413 并删除临时文件）
  → f.Sync() → Close()
  → 磁盘剩余空间与保留量预检
  → os.Rename → data/blobs/<key[:2]>/<key> → fsync(dir)
  → SQLite 事务：INSERT blob + INSERT item
  → 提交成功后：广播 item.created，返回 201
失败路径：删除临时文件；已 rename 但事务失败则由 janitor 回收
```

- 并发上传数用信号量限制（默认 4）；超限返回 429。
- `original_name` 只用于展示与 `Content-Disposition`，绝不参与路径拼接；服务端会剥离目录部分与控制字符。
- MIME 由服务端 `http.DetectContentType` 与扩展名白名单共同决定最终展示类型；SVG/HTML/XML/JS 等主动内容一律 `attachment` 下载。

## 7. 清理策略

- **手动删除**：事务内删除 item 行（立即不可见）→ 广播 → 尝试删除 blob 行与文件；失败仅记录日志，由 janitor 兜底重试。
- **Janitor**（启动时 + 每 15 分钟）：删除过期 session、未引用且超过宽限期的 blob（行 + 文件）、`data/tmp` 下超过 1 小时且不在活跃上传集合中的临时文件。
- 活跃上传在内存登记 `storage_key`/临时路径，janitor 跳过，避免误删正在写入的文件。

## 8. 前端

- React + TS + Vite，无 UI 框架；单一页面：登录 → 收件箱。
- 状态：`items[]` 以 REST 为准；WS 事件触发“插入到头部”或“移除”；收到 `resync`/重连/`visibilitychange` 时清空分页并重新拉第一页。
- 上传：`XMLHttpRequest` 以获得进度事件；多选文件顺序逐个上传，每个文件一个条目。
- 预览：图片用 `<img>` 指向 `/preview`（带 Cookie）。
- **复制**：`web/src/clipboard.ts` 先试 `navigator.clipboard`（仅在 secure context），
  再试 `document.execCommand('copy')`，两者都明确失败时返回原因；**不抛异常**。
  卡片随后展开只读、默认全选的文本框（正文/链接/SHA-256 通用），提示用户手动复制。
  成功与失败用不同 toast，**绝不**在失败时提示“已复制”。
- 明文 HTTP 下页面顶部显示一次性提示，说明自动复制可能不可用；不承诺后台剪贴板/通知。
- 无第三方分析、无外链字体、无 CDN 资源。

## 9. 关键取舍记录

| 取舍 | 原因 |
|---|---|
| 纯 Go SQLite（`modernc.org/sqlite`） | 免 cgo，便于跨发行版构建与 CI |
| Go 1.22 `net/http` mux 而非第三方路由 | 依赖最少，路径模式已够用 |
| 反向代理由外部（Tailscale/Caddy）负责 | 避免在应用内自管证书与端口暴露；但主路径是明文 HTTP，不依赖代 |
| 主路径选择明文 HTTP LAN | 用户明确接受可信家庭 LAN 边界，换取零证书/零额外组件 |
| 复制降级为手动选择文本 | 非安全上下文无法保证剪贴板 API；谎报成功比降级更糟 |
| 不实现自动过期 | 见 PRD §15.3，P1 范围 |
| 会话存 DB 而非 JWT | 支持服务端吊销、单点登出 |
| WS 事件不带完整条目内容 | 减少内容在日志/网络中的二次暴露，前端按 id 拉取或直接插入 |
| mDNS 用纯 Go 库而非依赖 Avahi | 单二进制无需额外守护进程；与 Avahi 共用 5353 是尽力而為，失败只告警不影响 HTTP |
| 只为每个符合条件的 LAN 监听发布一条 A 记录 | 避免 Docker/Tailscale 地址随广播泄露；双监听时只广播 LAN 入口，Tailscale 不冒充同一 LAN |
| 发现默认开启但可由配置关闭 | 家庭 LAN 主路径下零配置可用；`discovery_enabled=false` / `FLOWDROP_DISCOVERY_ENABLED=false` 显式退出 |

## 10. 局域网自动发现（mDNS/DNS-SD）

实现位于 `server/internal/discovery`，启动时由 `cmd/flowdrop` 调用；HTTP 服务不依赖它。

- **协议（冻结）**：服务类型 `_flowdrop._tcp`、域 `local.`；实例名 `FlowDrop-<主机昵称>`；
  SRV port = 真实 Hub 端口；A = 该监听绑定的 RFC1918 IPv4；TXT = `v=1, scheme=http, path=/`。
  多监听时每个符合条件且被实际绑定的 LAN 监听各发布一条记录（额外 LAN 地址使用带后缀的
  实例名）；不包含密码、会话、正文、文件路径。
- **启用判定**（纯函数 `evaluate`，有单测）：对每个实际监听地址，
  必须是具体 IPv4 → 必须是 RFC1918 → `cookie_secure=false` → 该 IP 必须在
  Up+Multicast 的本地网卡上。不合格的额外监听跳过并记原因；主监听不合格时保持历史
  `ErrIneligible` 语义；显式关闭返回 `ErrDisabled`。
  回环、通配、Tailscale `100.64/10`、链路本地、公网、IPv6、HTTPS 入口均不广播。
- **只向绑定网卡发布**：发布器用 `miekg/dns` 构造 PTR/SRV/TXT/A，显式给出单一 A 地址与
  单一 `net.Interface`；读取时校验 `ipv4.ControlMessage.IfIndex`，其它网卡的查询直接丢弃。
  这样不仅公告只从绑定网卡发出，`tailscale0`/`docker0` 上的查询也得不到响应。
  浏览已有实例（冲突检测）仍用成熟纯 Go 的 `grandcat/zeroconf`。
- **生命周期**：启动时先短暂 browse 同名实例（1.2s，尽力而為）以选择 `-2`/`-3` 后缀；
  注册失败只记录 WARN，Hub 继续 HTTP；退出时 `Shutdown()` 发送 TTL=0 注销并释放 UDP 5353。
  记录 TTL 120s，异常崩溃后客户端缓存有限期过期。
- **测试**：`evaluate` 地址范围、昵称截断/冲突、TXT/SRV/A 冻结值、关闭/不合格不注册、
  注册失败传播、Close 幂等。真实网络发布用独立 Avahi 浏览器 + 原始 DNS 探针验证，见 VERIFICATION §12。


---

## 11. Linux 桌面 App（Wails v3，2026-11-08）

> 本节描述桌面壳的实现；Hub 内部架构（第 1–3 节）未改变。
> 桌面只在**同一进程**内嵌入 `flowdrop/pkg/hub`，不复制任何业务逻辑。

### 11.1 模块边界

```text
desktop/                    独立 Go module（module flowdrop/desktop）
├── main.go                 进程装配：Hub 生命周期、两个窗口、托盘、关闭策略
├── service.go              Wails 绑定服务（只对 wails:// 设置窗口开放）
├── icons.go                内嵌窗口/托盘图标
├── dbus_linux.go           StatusNotifierWatcher 探测（决定关闭行为）
├── assets/                 设置窗口 UI（原生 HTML/JS，调用 /wails/runtime.js）
└── internal/
    ├── appsettings/        桌面偏好：主 IPv4 + 可选附加 IPv4 + 端口（无密码，兼容旧文件）
    ├── network/            真实网卡枚举/分类、默认选路、系统代理提示
    └── hubmgr/             hub.New/Start/Stop/Restart 生命周期封装
```

`desktop/go.mod` 独立：

```
require (
    flowdrop v0.0.0
    github.com/wailsapp/wails/v3 v3.1.0
)
replace flowdrop => ../server
```

因此 **Wails 依赖不进入 `server/go.mod`**，CLI/Android 不受影响。

### 11.2 两个窗口 = 两个 origin（安全边界）

| 窗口 | origin | 说明 |
|---|---|---|
| 收件箱 | Hub 的真实 HTTP origin（`http://<bind-ip>:<port>/`） | 现有 React + `/api/v1/*`。Cookie、CSRF 双提交、Origin==Host 校验、WS、鉴权下载**全部按浏览器语义**工作，无需为桌面放宽任何检查 |
| 设置 | `wails://localhost`（Wails 资产服务器，进程内） | 绑定地址/端口、首次管理员密码、显示 URL/运行状态、打开收件箱、退出 |

Wails 注入页面的运行时用 `location.origin + "/wails/runtime"` 解析原生调用。
Hub origin 的页面因此请求到 Hub 本身（404/SPA），**到不了资产服务器**，
绑定接口对局域网页面不可达。实测证据见 `docs/VERIFICATION.md §13.4`。

> 没有使用 iframe + 代理，也没有为了桌面删除 Hub 的
> `X-Frame-Options`/`CSP`/Host/Origin/CSRF 任何一项。

### 11.3 Hub 生命周期

- 启动：`hubmgr.Start(Config{ConfigPath, BindAddress, AdditionalBindAddresses, Port})` →
  `hub.New(hub.Options{ConfigPath, Listen, AdditionalListen, CookieSecure: &false})` →
  `hub.Start(ctx)`。`AdditionalListen` 总是非 nil（单地址模式就是空 slice），
  因此桌面不会意外继承 config.json 里的 `additional_listen`。
  Desktop 自己持有 Hub 的 lifetime context（`Manager` 内部），避免调用方的超时 context
  把健康 Hub 提前关掉。
- 多监听：`Listen` 是 LAN 主地址，`AdditionalListen` 是同一端口的 Tailscale 地址；
  仍是**一个 Hub**，同一 SQLite/附件/会话/Broker/数据目录锁。`URL()` 是主地址，
  `URLs()` 返回全部实际绑定地址（主地址在前）。
- 切换地址：`Restart` = `Stop`（撤回 mDNS、释放全部端口、释放数据目录锁）→ `New`+`Start`
  同一 `DataDir`。全部监听绑定成功才算成功；新配置失败时尝试恢复**原配置**并报告原因。
- 退出：托盘「退出 FlowDrop」→ `Stop(ctx 10s)` → `app.Quit()`；`OnShutdown` 再兜底调用
  一次幂等 `Stop`。不会处理或杀掉别的 FlowDrop 进程。

### 11.4 关闭 → 托盘（含无托盘回退）

- 先用 D-Bus 检查会话总线上是否有 `StatusNotifierWatcher`
  （`org.kde.StatusNotifierWatcher` 等）。有托盘时：
  `WindowClosing` 钩子 `e.Cancel()` + `Hide()`，Hub 继续运行；托盘菜单可恢复窗口。
- 没有托盘时**不隐藏**：关闭窗口弹出「退出 FlowDrop？」确认框（提示会停止 Hub），
  取消则窗口保留。运行期若 Wails 报告 `systray error`，同样切回该模式并恢复窗口。
- 托盘菜单项存在性与行为已在 KDE Plasma 6 (Wayland) 实测（VERIFICATION §13.5）。

### 11.5 外链与下载

- 收件箱窗口注入极小脚本：只把**跨 origin** 的 `<a href>` 点击与 `window.open`
  转成一条 `flowdrop:openurl:<URL>` 原始消息。
- Go 侧校验：窗口必须是收件箱窗口、`OriginInfo.Origin` 的 origin 必须等于当前 Hub origin、
  只允许 http/https、单次限速 500ms，然后 `app.Browser.OpenURL`。
- 同 origin 链接（含 `<a download>`）不拦截，交给 WebKit 原生下载（实测保存到
  `~/Downloads`）。

### 11.6 网络选择

- `network.List()` 枚举 Up 网卡的 IPv4，排除链路本地；分类：
  LAN（RFC1918）、Tailscale（`tailscale*` 网卡或 `100.64/10`）、容器/虚拟网卡
  （`docker*`/`br-*`/`veth*`…）、其他、回环。
- 单地址默认选：第一个 LAN → 第一个 Tailscale → 第一个其他；虚拟网卡与回环**不会**被自动选中。
- 双模式（`ApplyDualNetwork`）：必须恰好一个 LAN + 一个 Tailscale，两个地址当前都在本机，
  同端口；LAN 作为主监听，Tailscale 传给 `hub.Options.AdditionalListen`。
  没有 Tailscale 网卡时 UI 不可选双模式，但仍可使用内网单地址。
- 校验拒绝通配地址、IPv6、重复地址；端口 1–65535。旧 `ApplyNetwork(ip, port)` 保留兼容。
- 设置窗口只从 `Hub.URLs()`（实际绑定结果）生成“可用地址/类型”列表；未运行的配置地址
  只作为输入展示，不冒充已可用。
- 收件箱优先加载实际 URL 中的 LAN 地址；跨 host 的 Cookie 不共享（沿用 WebView 规则）。
- 系统代理检测（`http_proxy` / KDE `kioslaverc`）+ `no_proxy` / `NoProxyFor` 判断，
  只提示不改配置；提示针对收件箱实际使用的地址。

### 11.7 已知取舍

- 默认单监听；可选 LAN + Tailscale 双监听（同一 Hub/端口/数据库）。不支持自动网络重绑定
  或自动选路：地址失效或端口冲突时用户需修改设置并重启（桌面会先 Stop 再 Start，
  失败则尝试恢复原配置）。
- 不同监听地址（不同 host）之间的浏览器 Cookie 不自动共享；这是 WebView/浏览器规则，
  不做跨 origin 登录态注入。
- 使用的是 GTK3 后端（`-tags gtk3`），因为目标机只有 WebKitGTK 4.1 运行时。
- 桌面偏好不保存密码；管理员密码仍由 Hub 的 Argon2id + SQLite 管理。

---

## 12. 单 Hub 多监听（LAN + Tailscale，2026-11-08）

### 12.1 冻结接口

| 接口 | 语义 |
|---|---|
| `hub.Options.AdditionalListen []string` | 额外监听的 `host:port` 列表。`nil` = 继承配置 `additional_listen`；非 nil（含空 slice）= 显式覆盖。桌面始终传非 nil，单地址模式传空 slice |
| `Hub.URLs() []string` | 启动成功后全部实际监听 URL，主地址在前；未启动/已停止返回 nil。调用方不应从配置猜测可用地址 |
| `Hub.URL()` | 主监听 URL，等价 `URLs()[0]`；未运行返回 `""` |
| `config.additional_listen` / `FLOWDROP_ADDITIONAL_LISTEN` | JSON 数组 / 逗号分隔；只接受字面 IP:端口的具体地址 |
| `desktop.json additional_bind_addresses` | 桌面双模式的额外地址（恰好一个 Tailscale IPv4）；缺省 = 旧单地址模式 |

### 12.2 启动与回滚

1. `hub.New` 加载配置、取数据目录锁、打开 SQLite/附件、构建**一个** HTTP handler。
2. `hub.Start` 先绑定主 `Listen`，再逐个绑定 `AdditionalListen`；每个绑定成功后才继续。
3. 任一绑定失败：关闭已绑定的全部监听、释放数据库与数据目录锁，返回错误；
   不启动 HTTP 服务、不发布 mDNS。桌面把失败显示在设置窗口，并尝试恢复旧配置。
4. 全部绑定成功后：为每个监听启动同一 handler 的服务循环，发布符合条件的 LAN mDNS，
   记录真实端口；`URLs()` 只反映这些真实监听。
5. `Stop` 撤回全部 mDNS、关闭所有监听、排空 WebSocket、关闭数据库、释放唯一数据目录锁。
   不存在“两个 Hub 共享数据目录”或“只停一个监听”的路径。

### 12.3 安全与协议不变

- Host/Origin/CSRF/认证/限流仍由同一个 handler 执行；每个监听 IP 自动进入 Host 白名单。
- 不新增任何网络管理/Settings HTTP 端点：桌面设置只经进程内 `wails://` bridge 调用。
- 不监听 `0.0.0.0`/`::`，不做自动网络切换或自动重绑定。
- mDNS 只广播符合条件的 RFC1918 LAN 监听，Tailscale 监听永不冒充 LAN 入口。
- Android 与 CLI 的 HTTP 协议未变；Android 只需填写任一可达地址即可使用同一 Hub。
