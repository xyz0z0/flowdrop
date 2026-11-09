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
- 认证：**本机生成四位临时配对码 → 记住设备**（无账号密码）。设备令牌 32 字节随机数，
  DB 只存哈希；Web 用 HttpOnly Cookie，Android 用 Keystore 保存设备令牌。
  CSRF 双提交与 Origin/Host 校验对 Web 与原生 App 一致。配对合同见
  [docs/PAIRING.md](docs/PAIRING.md)。

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
- 拖拽上传、粘贴截图上传、多选文件（逐个上传，每个文件一个条目）；桌面 App
  登录后支持向内容窗口任意位置拖入多个文件（见下文「收件箱拖拽上传」）。
- 倒序稳定分页（keyset cursor）、加载更早内容。
- 手动删除条目并清理附件；启动及周期性清理临时文件、孤立附件、过期会话。
- WebSocket 实时通知；断线自动重连，恢复前台/上线后重新拉取列表。
- 设备配对（四位码，120 秒有效、一次成功、全 Hub 累计错 5 次失效）与设备撤销；
  限流；Host / Origin / CSRF 校验；统一安全响应头。
- **局域网自动发现（Hub 端）**：绑定具体 RFC1918 LAN IPv4 且 `cookie_secure=false` 时，
  默认以 `_flowdrop._tcp.local` 发布 `FlowDrop-<主机昵称>`；SRV 指向真实端口，
  A 只含绑定的那个 LAN IPv4，TXT 仅 `v=1,scheme=http,path=/`。
  `FLOWDROP_DISCOVERY_ENABLED=false` 可关闭；回环、Tailscale 100.64/10、通配地址、
  HTTPS 反代入口不会冒充 LAN 广播。协议细节见 [docs/DEPLOY.md §5.7](docs/DEPLOY.md)。
- **复制降级**：明文 HTTP 或剪贴板权限被拒时，复制失败会展开可选中的文本
  （正文/链接/SHA-256），不抛未捕获异常、不谎报成功。

**桌面 App 已交付（Linux + Windows，Wails v3.1.0）**

- 双击启动即在本进程内拉起**同一个 Go Hub**（`flowdrop/pkg/hub`，不另起进程、
  不复制 Hub 逻辑），直接打开现有 React 收件箱窗口。
- 图形式「设置」窗口：选择真实本机 IPv4（LAN / Tailscale 自动标注）、端口，
  一键重启 Hub；也可选择「同时使用内网和 Tailscale」，分别选一个内网 IPv4 和一个
  Tailscale IPv4（同端口、同一 Hub、同一数据库）。窗口只把**真正绑定成功**的 URL
  列为可用，并标注地址类型；无 Tailscale 网卡时明确提示不能选双模式，仍可用内网单地址。
- **主窗口可直接进入设置**：登录前后收件箱窗口都有明确的「设置」按钮（登录页右上角、
  登录后位于顶部栏「退出」旁），窗口原生菜单也有「设置…」，快捷键 `Ctrl+,`。
  点击可靠打开并聚焦同一个本机设置窗口，不再只能靠托盘或首次启动。该按钮只注入
  Wails 收件箱窗口，普通浏览器/手机页面没有它，也不向 Hub 页面开放管理 bridge。
- **设备配对只在本机进行**：设置窗口点「添加设备」生成四位码（仅在本机显示，
  不经网络、不写日志），手机输入该码即完成配对；设备列表可查看名称/创建与最近使用
  时间，并**在本机撤销**（撤销立即失效其 HTTP 会话与 WebSocket）。管理接口只走
  进程内 Go bridge，不提供远程生成码或远程管理端点。
- 关闭窗口收进系统托盘、Hub 继续运行；托盘菜单可显示窗口、打开设置、在浏览器打开、
  **明确退出并停止 Hub**（撤回 mDNS、释放端口）。无托盘时关闭窗口会确认退出，
  不会留下找不回窗口的后台进程；此时主窗口的「设置」入口仍然可用。
- 复用现有 React 与 `/api/v1/*`：Cookie、CSRF、Origin、WebSocket、鉴权下载与
  浏览器完全一致；Wails 管理桥（原生设置接口）只对进程内 `wails://` 设置窗口开放，
  局域网可访问的 Hub 页面**无法**调用它。外链交给系统浏览器。
- **收件箱整窗拖拽上传**：登录后把一张或多张图片/普通文件拖到内容窗口任意位置，
  松开即上传；进度、取消、大小上限沿用原上传流程。目录跳过并提示、不递归；
  登录页拖入文件不会上传（也不会把文件当页面打开）。
- 应用内代理直连：桌面在创建窗口前把本进程 WebKitGTK 的代理模式设为直连，
  因此系统代理保持开启时 Hub 的 HTTP 与 WebSocket 仍可直连（不再必须修改
  `no_proxy`）；不会修改系统代理、KDE 设置或环境变量。只有这项应用内配置失败时，
  设置窗口才显示“代理风险”诊断，并说明 WebSocket 可能被改写而不是断言必然失败。
- 产物：`dist/flowdrop-desktop`；用户级安装脚本 `scripts/install-desktop.sh`
  （安装到 `~/.local`，不需 sudo，不写开机自启）。构建与运行说明见
  [docs/DEPLOY.md §16](docs/DEPLOY.md)；桌面双监听实测见
  [docs/VERIFICATION.md §14](docs/VERIFICATION.md)。
- **界面（2026-11-09 改版）**：设置窗口与 Web 收件箱统一视觉，浅色/深色跟随系统
  （`prefers-color-scheme`），收件箱改为单卡片行式列表，操作按钮与状态胶囊统一尺寸。
- **下载与同名文件**：Linux 桌面下载重名时自动落成 `name (1).png`、`name (2).png`，
  不覆盖也不删除已有文件（见 [DEPLOY §16.12](docs/DEPLOY.md)）。
- **Windows 桌面版**：同一套桌面代码交叉编译为 `dist/flowdrop-windows-{amd64,arm64}.zip`
  （单 exe，已写入图标、`0.1.0.0` 版本信息与 PerMonitorV2 DPI manifest）。功能与 Linux
  一致，差异只在平台适配层：拖拽走 WebView2 原生 HTML5、托盘由系统通知区提供（关闭
  窗口必定可收进托盘）、WebView 代理直连用 `--no-proxy-server` 启动参数。构建、打包与
  已知限制见 [docs/DEPLOY.md §18](docs/DEPLOY.md)。

**Android 原生客户端已交付**

- 配对码配对、设备令牌存入 Android Keystore、文字/图片/文件收发、系统分享入口、
  实时同步和下载。
- **自动连接（单 Hub）**：保存 Hub 稳定 ID、设备令牌与候选地址；启动/回到前台/连接
  中断时先探测 LAN、再探测 Tailscale，且只在无凭据 `health` 的 `hub_id` 匹配后才用
  Bearer 恢复 Cookie/CSRF，随后恢复事件流。切地址只重试连接，**不重放**上传/删除等写操作。
- Debug APK：`dist/flowdrop-android-debug.apk`（约 9.74 MiB，Gradle原包位于`android/app/build/outputs/apk/debug/app-debug.apk`）。
- 构建与模拟器验证说明见 [android/README.md](android/README.md)，真实手机仍待验收。

**不在 MVP 内**（P1/P2，明确未实现）

- 内容自动过期（默认 7 天等），仅保留 `expires_at` 字段但**不自动删除**，
  见 PRD §15.3。
- 扫码配对、Tailscale 管理 API 自动枚举设备、全网段扫描、macOS 桌面版、
  开机自启、Web 跨 origin 自动切换登录态、多个 Hub 实例。
- APK 自动发布监听目录、搜索、批量传输、断点续传。
- 自建 NAT 穿透、公网匿名分享、多用户/团队协作、端到端加密。

## 当前版本（配对版，2026-11-08）

配对与单Hub LAN/Tailscale自动连接已实现并构建。可直接运行[Linux桌面版](dist/flowdrop-desktop)，
手机使用[Android测试APK](dist/flowdrop-android-debug.apk)。先完全从托盘退出旧桌面实例；
需要双网络时在设置启用内网与Tailscale双模式，再点「添加设备」取四位码，每台设备配对一次。
服务端/桌面检查、Android102 JVM、Chromium16项以及隔离Wails真实配对/拖拽通过；
真实手机Wi-Fi/Tailscale切换仍待验收，未自动安装或提交。产物摘要、证据与限制见
[VERIFICATION §20](docs/VERIFICATION.md#20-四位码配对与多地址连接2026-11-08本轮)。

## 快速开始（开发）

依赖：Go 1.24+、Node.js 20+、npm。

```bash
git clone <repo> flowdrop && cd flowdrop
scripts/dev.sh
```

- 前端开发地址：<http://127.0.0.1:5173>（Vite 代理 `/api` 与 WS 到 Go）
- 首次配对（不需要账号密码）：保持 Hub 运行，在另一个终端生成四位码：

```bash
cd server && go run ./cmd/flowdrop pair
```

- 该命令与 Hub 使用同一 `data_dir`，码只在本终端打印；然后浏览器打开
  <http://127.0.0.1:5173>，输入该码完成配对。桌面版可直接在设置窗口点「添加设备」。

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
./dist/flowdrop doctor        # 环境自检（含配对表与 Hub ID）
./dist/flowdrop serve         # 默认 http://127.0.0.1:8080
./dist/flowdrop pair          # Hub 运行中时生成四位配对码（本机终端显示）
```

### 主路径：家庭局域网 HTTP

Hub 与手机/电脑在同一 Wi-Fi 时，直接监听**具体的局域网 IP** 即可，无需 TLS：

```bash
# 例：Hub 局域网 IP 为 192.168.1.10（用 ip -4 addr 查看）
FLOWDROP_LISTEN=192.168.1.10:8080 \
FLOWDROP_COOKIE_SECURE=false \
./dist/flowdrop serve
```

手机与电脑浏览器打开 `http://192.168.1.10:8080`，输入本机生成的配对码即可。

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
记录中**不含**设备令牌、会话、条目正文或机器路径。

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

同一 tailnet 内的手机/电脑访问 `http://100.x.y.z:8080`，用本机生成的配对码配对。
Android 已配对后会在 LAN 与 Tailscale 之间自动优先 LAN；**首次异地访问**需要手填
Tailscale 地址（或在使用已允许的 MagicDNS 名时填该名字），不会自动扫描 tailnet。
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

安装后在发现列表或手填 Hub 地址，输入**本机生成的四位配对码**完成配对；
配对后设备令牌保存在 Android Keystore。客户端沿用 Cookie 会话、CSRF 和 Origin 协议。
完整说明见 [android/README.md](android/README.md)。

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

> **替换二进制**：先通过托盘菜单「退出 FlowDrop（停止 Hub）」完全退出正在运行的
> 实例，再执行 `scripts/build-desktop.sh` 和 `scripts/install-desktop.sh`，最后重新启动。
> 只关闭窗口会收进托盘；SingleInstance 会复用现有进程，直接启动新二进制看不到新入口。

**直接运行，无需额外准备**：`dist/flowdrop-desktop` 是完整的单文件应用，双击或
`./dist/flowdrop-desktop` 就会在进程内拉起同一个 Hub 并打开收件箱；不需要先运行
任何 `.sh`，也不需要另外启动 `flowdrop serve`。`scripts/` 里的脚本只是构建与安装
工具，不会自动安装或替换你现有的二进制（新版需要你手动运行）。

**收件箱拖拽上传（Linux 内容窗口）**：登录后把一张或多张图片/普通文件拖到窗口
任意内容区域，松开鼠标即按原有流程上传（进度、取消、512 MiB 上限不变）；同时拖入
多个文件时逐个顺序上传。拖入目录会被跳过并在页面提示，**不递归**展开；只在登录后
的收件箱窗口生效，登录页拖入文件不会发送。

> 新版修正 Wayland 原生拖放动作协商与异步接收状态，已在独立容器用 GTK Wayland
> 源验证真实拖入、重复上传与下载 SHA 一致，并通过本轮 X11 核心回归。此前多文件、
> 目录跳过与普通文件选择验证记录保留在 §18；用户已确认真实 KDE/Dolphin 拖拽可用，
> 大文件进度/取消未实测，最新产物与证据见 [docs/VERIFICATION.md §19](docs/VERIFICATION.md)。

**会话无障碍总线损坏时的启动兜底**：如果登录会话的 AT-SPI 无障碍总线已损坏
（症状：启动后约 1 秒内原生 `SIGSEGV`，日志里有 `Couldn't register with
accessibility bus`），旧构建会被系统 `libatk-bridge` 的空指针崩溃带走。新构建会在
GTK 初始化前探测该总线，只有探测失败时才**仅对本进程**设置 `NO_AT_BRIDGE=1` 并
继续启动，同时在设置窗口显示黄色说明；正常会话不关闭无障碍。没有设置窗口可看时
可用 `FLOWDROP_DESKTOP_A11Y=off` 显式关闭、`=on` 强制保留；彻底恢复无障碍需注销
并重新登录（重启会话的无障碍总线），详见 `docs/DEPLOY.md` §16.10。

构建依赖（Debian/Ubuntu）：

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

没有 root 时，`scripts/desktop-dev-sysroot.sh` 会把同样的开发包解压到
`~/.local/share/flowdrop-desktop/sysroot`（仅下载/解压到用户目录，不改动系统包）。

运行依赖：GTK 3 + WebKitGTK 4.1（`libgtk-3-0t64`、`libwebkit2gtk-4.1-0`、
`libsoup-3.0-0`、`libjavascriptcoregtk-4.1-0`）。

桌面版自身的设置保存在 `~/.config/flowdrop/desktop.json`（0600）：运行模式 `mode`
（`local` / `remote`）、绑定地址 `bind_address` + `port`、远程 Hub 地址，以及本机
免码登录用的设备令牌 `desktop_device_token`。单地址只写 `bind_address` + `port`；
双模式额外写入 `additional_bind_addresses`（恰好一个 Tailscale 地址）。
Hub 数据仍在 `~/.local/share/flowdrop/`，与 CLI 共用。

## CLI

```text
flowdrop serve [-config FILE]                     启动 Hub
flowdrop pair [-config FILE]                      生成四位配对码（仅本机终端显示）
flowdrop doctor [-config FILE]                    检查数据目录、数据库、配对表、磁盘、端口
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
| 传输 | **默认主路径是可信家庭 LAN 明文 HTTP**（用户已接受该边界）；跨网/不可信网络请用 Tailscale Serve 或 TLS 反代。设备令牌在 LAN 上同样可能被同网段监听，Tailscale 前置要求两端在线且策略允许。多租户/公网暴露不在范围内 |
| 认证 | 四位数临时配对码（120 秒、一次成功、5 错失效）+ 32B 设备令牌（DB 只存 SHA-256）；服务端可撤销设备。码不是加密，Hub ID 也只防误连，不认证明文 HTTP 对端 |
| CSRF | 双提交令牌 + Origin / Host 校验；Host 白名单防 DNS rebinding。Web 与原生 App 同一套 |
| 越权 | 所有条目、预览、下载、WS 统一会话鉴权，知道 ID 也不够 |
| 上传 | 流式写入、大小上限、磁盘余量预检、并发上限、临时文件 + 原子 rename |
| 文件 | 服务端嗅探 MIME；SVG/HTML/XML/JS 强制 `attachment` + `octet-stream`；文件名不参与路径 |
| 隐私 | 日志不含内容原文/设备令牌/会话；无第三方分析、无外链资源 |

发现安全问题时请勿公开提交 issue，先私下联系维护者。

## 已知限制

- **主路径为明文 HTTP**：局域网内传输不加密，同网段设备/中间人可读取内容与 Cookie。
  这是用户已接受的家庭可信 LAN 边界；需要加密时改用 Tailscale 或 Caddy HTTPS。
- **LAN HTTP 下浏览器能力受限**：`navigator.clipboard`、Service Worker、通知等可能不可用。
  Web 端复制会降级为可手动选择的文本；**不承诺**后台剪贴板读写或系统通知。
- Android debug APK 已构建并在 API 36 模拟器验证；真实手机安装、收发与真实
  LAN/Tailscale 自动选路仍待验收（见 [docs/VERIFICATION.md](docs/VERIFICATION.md)）。
- 手机浏览器后台不保证常驻 WS 或系统通知；回到前台会重新拉取列表（设计如此）。
- **Web 不自动切换地址**：浏览器 Cookie 按 origin 保存，换地址（如 LAN → Tailscale）
  需重新配对；只有 Android（单 Hub）自动在保存的候选地址间选路。
- 浏览器 Cookie TTL 仍为 7 天（`session_ttl_hours`），**不是**随设备令牌无限滑动；
  到期后需重新输入配对码。设备令牌本身一直有效直到在本机撤销。
- APK 下载后由 Android 系统引导安装，应用不请求静默安装权限。
- 单用户、单 Hub；无多租户、无端到端加密、无自动过期。
- 大文件上限 512 MiB（可配置）；无断点续传，中断需重传。
- **双地址是唯一的多入口方式**：同一个 Hub 可以同时监听一个内网地址 + 一个 Tailscale
  地址（`additional_listen` / 桌面双模式），不监听 `0.0.0.0`，也不会在 Hub 侧重绑定
  失效地址。**客户端自动选路只有 Android（单 Hub）**，Web 不承诺自动换 origin。
  Tailscale 绑定步骤见上文与 [Android 使用说明](android/README.md)。
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
