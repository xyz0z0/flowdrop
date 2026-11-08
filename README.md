# FlowDrop（流转）

个人自托管的**跨设备共享收件箱**：在 Linux 电脑与 Android 手机之间快速互发
文字、链接、图片、APK 和普通文件。发送一次，登录同一 Hub 的其他设备即时可见。

- 服务端：Go 单进程（REST + WebSocket + SQLite + 磁盘附件），前端嵌入同一二进制。
- 客户端：Android 原生 App（同一套 `/api/v1/*` 协议）+ 电脑浏览器 Web 界面。
- 网络：**家庭可信局域网内直接用明文 HTTP**（`http://<Hub-IP>:8080`），
  不需要 Tailscale、Caddy 或证书；跨网时用你**现有的 Tailscale**。
  同一个 Hub 可以**同时绑定一个内网地址和一个 Tailscale 地址（同一端口）**，
  手机在家用 Wi-Fi 或在外用 Tailscale 都能直接访问，数据与登录由同一实例共享。
  同一 Wi-Fi 的客户端还可通过 mDNS/DNS-SD（`_flowdrop._tcp`）自动发现 Hub。
  HTTPS / Tailscale Serve / Caddy 部署仍保留为**可选**加固方案。
- 认证：单管理员密码（Argon2id）+ 服务端会话（DB 只存令牌哈希）+ HttpOnly Cookie；
  CSRF 双提交与 Origin/Host 校验对 Web 与原生 App 一致。

> 需求基线：[docs/PRD.md](docs/PRD.md)（含 §15 实施澄清、§16 HTTP LAN + Android 范围澄清）
> 设计说明：[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)
> 分阶段计划：[docs/PLAN.md](docs/PLAN.md)
> 部署运维：[docs/DEPLOY.md](docs/DEPLOY.md)
> 验证记录：[docs/VERIFICATION.md](docs/VERIFICATION.md)

## 功能范围（MVP / P0）

**已实现**

- 文字、链接（仅 http/https）发送；链接以可点击形式展示，文字按纯文本渲染。
- 图片上传 + 受限格式内联预览（JPEG/PNG/GIF/WebP），点击放大。
- 任意文件上传（含 APK）、进度、鉴权下载、SHA-256 展示与复制。
- 拖拽上传、粘贴截图上传、多选文件（逐个上传，每个文件一个条目）。
- 倒序稳定分页（keyset cursor）、加载更早内容。
- 手动删除条目并清理附件；启动及周期性清理临时文件、孤立附件、过期会话。
- WebSocket 实时通知；断线自动重连，恢复前台/上线后重新拉取列表。
- 管理员密码登录/注销；限流；Host / Origin / CSRF 校验；统一安全响应头。
- **局域网自动发现（Hub 端）**：绑定具体 RFC1918 LAN IPv4 且 `cookie_secure=false` 时，
  默认以 `_flowdrop._tcp.local` 发布 `FlowDrop-<主机昵称>`；SRV 指向真实端口，
  A 只含绑定的那个 LAN IPv4，TXT 仅 `v=1,scheme=http,path=/`。
  `FLOWDROP_DISCOVERY_ENABLED=false` 可关闭；回环、Tailscale 100.64/10、通配地址、
  HTTPS 反代入口不会冒充 LAN 广播。协议细节见 [docs/DEPLOY.md §5.7](docs/DEPLOY.md)。
- **复制降级**：明文 HTTP 或剪贴板权限被拒时，复制失败会展开可选中的文本
  （正文/链接/SHA-256），不抛未捕获异常、不谎报成功。

**Linux 桌面 App 已交付（Wails v3.1.0）**

- 双击启动即在本进程内拉起**同一个 Go Hub**（`flowdrop/pkg/hub`，不另起进程、
  不复制 Hub 逻辑），直接打开现有 React 收件箱窗口。
- 图形式「设置」窗口：选择真实本机 IPv4（LAN / Tailscale 自动标注）、端口，
  一键重启 Hub；也可选择「同时使用内网和 Tailscale」，分别选一个内网 IPv4 和一个
  Tailscale IPv4（同端口、同一 Hub、同一数据库）。窗口只把**真正绑定成功**的 URL
  列为可用，并标注地址类型；无 Tailscale 网卡时明确提示不能选双模式，仍可用内网单地址。
- 首次管理员密码在**本机图形界面**设置（仅未初始化时可用，不会覆盖已有密码）。
- 关闭窗口收进系统托盘、Hub 继续运行；托盘菜单可显示窗口、打开设置、在浏览器打开、
  **明确退出并停止 Hub**（撤回 mDNS、释放端口）。无托盘时关闭窗口会确认退出，
  不会留下找不回窗口的后台进程。
- 复用现有 React 与 `/api/v1/*`：Cookie、CSRF、Origin、WebSocket、鉴权下载与
  浏览器完全一致；Wails 管理桥（原生设置接口）只对进程内 `wails://` 设置窗口开放，
  局域网可访问的 Hub 页面**无法**调用它。外链交给系统浏览器。
- 产物：`dist/flowdrop-desktop`；用户级安装脚本 `scripts/install-desktop.sh`
  （安装到 `~/.local`，不需 sudo，不写开机自启）。构建与运行说明见
  [docs/DEPLOY.md §16](docs/DEPLOY.md)；桌面双监听实测见
  [docs/VERIFICATION.md §14](docs/VERIFICATION.md)。

**Android 原生客户端已交付**

- 密码登录、会话保存、文字/图片/文件收发、系统分享入口、实时同步和下载。
- Debug APK：`android/app/build/outputs/apk/debug/app-debug.apk`（约 9.4 MiB）。
- 构建与模拟器验证说明见 [android/README.md](android/README.md)，真实手机仍待验收。

**不在 MVP 内**（P1/P2，明确未实现）

- 内容自动过期（默认 7 天等），仅保留 `expires_at` 字段但**不自动删除**，
  见 PRD §15.3。
- 扫码配对、Windows/macOS 桌面版、开机自启、自动网络重绑定/自动选路、多个 Hub 实例。
- APK 自动发布监听目录、设备管理、搜索、批量传输、断点续传。
- 自建 NAT 穿透、公网匿名分享、多用户/团队协作、端到端加密。

## 快速开始（开发）

依赖：Go 1.24+、Node.js 20+、npm。

```bash
git clone <repo> flowdrop && cd flowdrop
scripts/dev.sh
```

- 前端开发地址：<http://127.0.0.1:5173>（Vite 代理 `/api` 与 WS 到 Go）
- 首次使用需在另一个终端设置管理员密码：

```bash
cd server && go run ./cmd/flowdrop admin init
```

然后浏览器打开 <http://127.0.0.1:5173> 登录。

> 开发模式必须**通过 Vite 的 5173 端口**访问，不要直接用 8080：`FLOWDROP_COOKIE_SECURE`
> 默认为 `true`，开发时请按下面说明设置，否则浏览器不会保存登录 Cookie。

### 开发环境的最小配置

```bash
export FLOWDROP_COOKIE_SECURE=false     # 仅用于 http://127.0.0.1 开发
export FLOWDROP_DATA_DIR="$PWD/.data"   # 可选：把数据放在仓库内（已被 .gitignore）
scripts/dev.sh
```

`127.0.0.1` 在浏览器中属于 secure context，因此默认的 `Secure` Cookie 也可工作；
但在部分浏览器/设置下仍需上面的开关。**HTTPS 部署时不要关闭它**；
明文局域网主路径则必须关闭（见下文）。

## 生产构建与运行

```bash
scripts/build.sh 0.1.0        # 前端构建 + 嵌入 → dist/flowdrop（单文件）
./dist/flowdrop admin init    # 本机设置管理员密码（必须在 Hub 本机）
./dist/flowdrop doctor        # 环境自检
./dist/flowdrop serve         # 默认 http://127.0.0.1:8080
```

### 主路径：家庭局域网 HTTP

Hub 与手机/电脑在同一 Wi-Fi 时，直接监听**具体的局域网 IP** 即可，无需 TLS：

```bash
# 例：Hub 局域网 IP 为 192.168.1.10（用 ip -4 addr 查看）
FLOWDROP_LISTEN=192.168.1.10:8080 \
FLOWDROP_COOKIE_SECURE=false \
./dist/flowdrop serve
```

手机与电脑浏览器打开 `http://192.168.1.10:8080` 登录即可。

- **`FLOWDROP_LISTEN` 必须写具体的局域网 IP，不能是 `0.0.0.0` / `::`**：
  `cookie_secure=false` 时服务端**拒绝**通配地址启动，避免在所有网卡上开明文端口。
- 该监听 IP **会自动加入 Host 白名单**，所以 `FLOWDROP_ALLOWED_HOSTS` 只用于补充其它
  访问名（如 mDNS 名 `flowdrop.local`、Tailscale MagicDNS 名）；不写也行。
  访问用的 Host 不在白名单时 Hub 返回 `421 host_not_allowed`（DNS rebinding 防护）。
- 明文 HTTP **不是**安全上下文：浏览器可能禁用自动复制等能力。Web 端复制失败时
  会展开可手动选择的文本；**不承诺** LAN HTTP 下后台剪贴板/通知等 API 可用。
- 仅限**可信家庭 LAN**；不要把 8080 端口映射到公网。需要加密或跨网时见下方可选方案。
- 辅助脚本：`scripts/lan-http.sh` 会自动探测本机局域网 IP 并校验是私有地址；
  `deploy/http-lan.env.example` 是可 source 的环境模板。

#### 局域网自动发现（mDNS/DNS-SD）

符合条件时 Hub **默认**发布一条 DNS-SD 记录，手机/电脑的 mDNS 浏览器可自动找到：

| 字段 | 值 |
|---|---|
| 服务类型 | `_flowdrop._tcp.local.` |
| 实例名 | `FlowDrop-<主机昵称>`（安全截断，冲突时加 `-2` 等后缀） |
| SRV | Hub 的真实监听端口 |
| A | 绑定成功且符合条件的 RFC1918 LAN IPv4（每个这样的 LAN 监听一条；**绝不含** Tailscale 地址） |
| TXT | `v=1`、`scheme=http`、`path=/` |

启用条件（全部满足才发布）：监听地址是**具体的 RFC1918 IPv4**（10/8、172.16/12、
192.168/16）、`cookie_secure=false`、地址确实在某块启用多播的网卡上。回环、`0.0.0.0`/`::`、
Tailscale `100.64/10`、链路本地、公网地址和 HTTPS 反代入口**不会**发布该记录。
记录中**不含**密码、会话、条目正文或机器路径。

```bash
# 显式关闭（默认自动开启；JSON 也可写 "discovery_enabled": false）
FLOWDROP_DISCOVERY_ENABLED=false ./dist/flowdrop serve
# 辅助脚本等价开关
scripts/lan-http.sh --no-discovery
```

实现使用纯 Go mDNS 生态（`zeroconf` 浏览/冲突扫描 + `miekg/dns` 记录编解码），
不需要安装 Avahi/Bonjour；与系统 Avahi 共用 UDP 5353 是**尽力而為**，端口被其它
守护进程占用时只在日志中告警，Hub 的 HTTP 收发不受影响。发布器只应答来自**绑定网卡**
的查询（其它网卡/接口直接丢弃），退出时发送 TTL=0 注销包并释放 socket。
发现协议已冻结，Android 端按同一协议接入；**本仓库本轮只验证 Hub 端发布，
不用普通 HTTP 健康检查冒充发现成功**。

#### 绑定 Tailscale（跨网 / 移动网络）

在 Hub 上取 Tailscale IPv4（形如 `100.x.y.z`）。有两种用法：

**A. 只绑 Tailscale（旧单地址方式）**：离开同一 Wi-Fi 时，把 Hub 的唯一监听
地址改成 Tailscale IPv4，然后重启 Hub：

```bash
tailscale ip -4
FLOWDROP_LISTEN=100.x.y.z:8080 \
FLOWDROP_COOKIE_SECURE=false \
./dist/flowdrop serve
# 或让脚本校验并启动：scripts/lan-http.sh --host 100.x.y.z --port 8080
```

**B. 同时绑 LAN + Tailscale（推荐，同一 Hub、同一端口、同一数据库）**：
主监听保持 LAN 地址，另加一个 Tailscale 监听。手机在家用 Wi-Fi 走内网地址，
在外用 Tailscale 地址，不需要重启或切换：

```bash
FLOWDROP_LISTEN=192.168.1.10:8080 \
FLOWDROP_ADDITIONAL_LISTEN=100.x.y.z:8080 \
FLOWDROP_COOKIE_SECURE=false \
./dist/flowdrop serve
```

`additional_listen` 也可写入 `~/.config/flowdrop/config.json`（数组）；多个地址用逗号
分隔（仅对额外的具体 IP 有意义，项目只推荐 LAN + Tailscale 这一组）：

```json
{
  "listen": "192.168.1.10:8080",
  "additional_listen": ["100.x.y.z:8080"],
  "cookie_secure": false
}
```

同一 tailnet 内的手机/电脑访问 `http://100.x.y.z:8080`，用同一管理员密码登录。
若想用 MagicDNS 名访问（如 `http://hub.tailnet-name.ts.net:8080`），把该名字加入
`FLOWDROP_ALLOWED_HOSTS`。Tailscale 直连**不需要** `tailscale serve`；若你已经用
`tailscale serve` 提供 HTTPS，则保持 `cookie_secure=true`（那是 TLS 入口，不属于
可直连的 HTTP LAN 入口）。

> **发现与 Tailscale 的关系**：Tailscale `100.64/10` 地址不会通过 mDNS 广播，
> 因为它是跨网地址，不是同一 LAN 的可直连入口。双监听时只广播符合条件的 LAN
> 地址，Tailscale 监听被跳过并在日志说明原因。
>
> **仍然不做**：自动网络重绑定、自动选路，也不会用两个共享数据目录的 Hub 进程
> 代替多监听。网络地址失效后需要修改配置并重启；桌面版会在设置窗口明确报错并
> 尝试恢复原配置。

**可选（跨网 / 加密）**：`tailscale serve`，或 Caddy 局域网 HTTPS（需手机导入 CA）。
两者都保留，但不再是必需的部署步骤。详细步骤：
**见 [docs/DEPLOY.md](docs/DEPLOY.md)**。

### Android 原生 App

已生成可安装的 debug APK：`android/app/build/outputs/apk/debug/app-debug.apk`。
重新构建：

```bash
cd android
./gradlew :app:assembleDebug
```

安装后把 Hub 地址填为 `http://<Hub-IP>:8080`，用同一管理员密码登录。
客户端沿用 Cookie 会话、CSRF 和 Origin 协议。完整说明见 [android/README.md](android/README.md)。

Android 发现交付边界（并行 worker 自报，本轮未复跑）：APK
`android/app/build/outputs/apk/debug/app-debug.apk`，SHA-256
`f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8`；
53 个 JVM 测试全通过，41 个设备测试中 39 个执行通过、2 个 LiveHub 因无参数跳过；
lint/assemble 通过。登录页做 12 秒自动搜索 + 手动刷新停止，选中服务只填地址、不发送凭据，
前后台切换会清理浏览。协议按冻结的 `_flowdrop._tcp` + `v=1,scheme=http,path=/` 实现。
**实际 Hub 在 `192.168.31.184:18099` 广播，但 API 36 模拟器 20 秒内没有 `onServiceFound`；
真实手机 mDNS 互通仍未验证，不能写成真实局域网发现通过。**

## 桌面版（Linux，Wails）

```bash
# 1) 构建（需要 GTK3/WebKitGTK 开发包，见下方依赖说明）
scripts/build-desktop.sh

# 2) 用户级安装（~/.local，无需 sudo；不创建开机自启）
scripts/install-desktop.sh

# 3) 从应用菜单「FlowDrop 流转」启动，或直接运行
~/.local/bin/flowdrop-desktop
```

构建依赖（Debian/Ubuntu）：

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

没有 root 时，`scripts/desktop-dev-sysroot.sh` 会把同样的开发包解压到
`~/.local/share/flowdrop-desktop/sysroot`（仅下载/解压到用户目录，不改动系统包）。

运行依赖：GTK 3 + WebKitGTK 4.1（`libgtk-3-0t64`、`libwebkit2gtk-4.1-0`、
`libsoup-3.0-0`、`libjavascriptcoregtk-4.1-0`）。

桌面版自身的设置只保存**绑定地址与端口**（`~/.config/flowdrop/desktop.json`，
不含密码）。单地址沿用旧的 `bind_address` + `port`；双模式额外写入
`additional_bind_addresses`（恰好一个 Tailscale 地址），旧版 `desktop.json` 仍可直接读取。
Hub 数据仍在 `~/.local/share/flowdrop/`，与 CLI 共用。

## CLI

```text
flowdrop serve [-config FILE]                     启动 Hub
flowdrop admin init [-username NAME] [--force]    设置/轮换管理员密码（交互式，不回显）
flowdrop admin init --password-stdin              脚本用：从 stdin 读两行
flowdrop doctor [-config FILE]                    检查数据目录、数据库、磁盘、端口
flowdrop backup OUT.tar.gz                        一致性快照（数据库 + 附件）
flowdrop version                                  版本
```

配置：`~/.config/flowdrop/config.json`（可选）+ `FLOWDROP_*` 环境变量（优先）。
完整键表见 [docs/DEPLOY.md §9](docs/DEPLOY.md#9-配置参考)。

## 测试

```bash
# 后端：单元 + 集成（认证、越权下载、WS、分页、上传边界、清理、持久化、mDNS 发现）
cd server && go test ./... -count=1

# 前端类型检查 + 生产构建
cd web && npx tsc -b --force && npx vite build

# 端到端（真实二进制 + 真实浏览器，两个独立会话）
# 含 clipboard.spec.ts：无 Clipboard API / 权限被拒 / 真实非安全 LAN 源
scripts/build.sh && cd tests && npm install && npx playwright test
```

实际执行结果与已知限制见 [docs/VERIFICATION.md](docs/VERIFICATION.md)。

## 安全要点（简述）

| 面 | 措施 |
|---|---|
| 传输 | **默认主路径是可信家庭 LAN 明文 HTTP**（用户已接受该边界）；跨网/不可信网络请用 Tailscale Serve 或 TLS 反代。多租户/公网暴露不在范围内 |
| 认证 | Argon2id 慢哈希；会话令牌只存 SHA-256；滑动过期；服务端可吊销 |
| CSRF | 双提交令牌 + Origin / Host 校验；Host 白名单防 DNS rebinding。Web 与原生 App 同一套 |
| 越权 | 所有条目、预览、下载、WS 统一会话鉴权，知道 ID 也不够 |
| 上传 | 流式写入、大小上限、磁盘余量预检、并发上限、临时文件 + 原子 rename |
| 文件 | 服务端嗅探 MIME；SVG/HTML/XML/JS 强制 `attachment` + `octet-stream`；文件名不参与路径 |
| 隐私 | 日志不含内容原文/密码/令牌；无第三方分析、无外链资源 |

发现安全问题时请勿公开提交 issue，先私下联系维护者。

## 已知限制

- **主路径为明文 HTTP**：局域网内传输不加密，同网段设备/中间人可读取内容与 Cookie。
  这是用户已接受的家庭可信 LAN 边界；需要加密时改用 Tailscale 或 Caddy HTTPS。
- **LAN HTTP 下浏览器能力受限**：`navigator.clipboard`、Service Worker、通知等可能不可用。
  Web 端复制会降级为可手动选择的文本；**不承诺**后台剪贴板读写或系统通知。
- Android debug APK 已构建并在 API 36 模拟器验证；真实手机安装与收发仍待验收。
- 手机浏览器后台不保证常驻 WS 或系统通知；回到前台会重新拉取列表（设计如此）。
- APK 下载后由 Android 系统引导安装，应用不请求静默安装权限。
- 单用户、单 Hub；无多租户、无端到端加密、无自动过期。
- 大文件上限 512 MiB（可配置）；无断点续传，中断需重传。
- **单地址限制是历史版本行为**：旧版一个 Hub 只能绑定一个具体 IP，LAN 与 Tailscale
  需重启切换。当前版本支持同一个 Hub 同时监听一个内网地址 + 一个 Tailscale 地址
  （`additional_listen` / 桌面双模式）；仍不会监听 `0.0.0.0`，也不会自动切换网络或
  自动重绑定失效地址。Tailscale 绑定步骤见上文与
  [Android 使用说明](android/README.md)。
- mDNS 自动发现只在**具体 RFC1918 LAN IPv4 + 明文 HTTP** 时发布；
  Tailscale、回环、通配、HTTPS 入口不广播。系统 Avahi 或其它进程占用 UDP 5353 时，
  发布可能失败（Hub 仍正常收发，仅日志告警）。
- 真实 Android 客户端的自动发现 UI、真实手机与真实 Wi-Fi 跨设备发现仍待验收；
  Hub 端的协议探针结果见 [docs/VERIFICATION.md §12](docs/VERIFICATION.md)。
- 未做真实 Android 真机 / 真实 Wi-Fi / 移动网络实测（见 docs/VERIFICATION.md）。

## 目录结构

```text
flowdrop/
├── docs/          PRD、架构、计划、部署、验证记录
├── server/        Go Hub（cmd + internal）
├── web/           React + TS + Vite 前端
├── android/       Android 原生 App（含构建与测试说明）
├── tests/         Playwright 端到端
├── deploy/        systemd 用户服务、Caddy 局域网 HTTPS 模板（可选）
└── scripts/       dev.sh / build.sh / lan-http.sh（明文 LAN 启动辅助）
```

## 许可证

内部个人项目，尚未选定开源许可证。
