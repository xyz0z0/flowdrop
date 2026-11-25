# FlowDrop（流转）

简体中文 · [English](README.en.md)

个人自托管的**跨设备共享收件箱**：在电脑与 Android 手机之间快速互发文字、链接、图片、APK 和普通文件。发送一次，连接同一 Hub 的其他设备即时可见。

- **自托管单二进制 Hub**：Go 进程内含 REST + WebSocket + SQLite + 磁盘附件，前端嵌入同一二进制。
- **多端客户端**：Android 原生 App、Linux / Windows 桌面版、浏览器 Web 界面、命令行（CLI）。
- **网络**：家庭可信局域网直接明文 HTTP，跨网用你现有的 Tailscale；同一 Hub 可同时绑定内网与 Tailscale 地址。
- **免账号**：本机生成四位配对码，输入即配对；设备令牌由服务端记住并可撤销。

## 文档入口

[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md)（架构）· [docs/DEPLOY.md](docs/DEPLOY.md)（部署与配置）· [docs/PAIRING.md](docs/PAIRING.md)（配对协议）· [docs/CLI_AGENT.md](docs/CLI_AGENT.md)（命令行 / HTTP 用法）

## 特性

- 文字与链接（仅 http/https）发送，链接可点击，文字按纯文本渲染。
- 图片上传与内联预览（JPEG/PNG/GIF/WebP），点击放大。
- 任意文件上传（含 APK），带进度、鉴权下载、SHA-256 展示与复制。
- 拖拽、粘贴截图、多选文件上传；桌面版可向内容窗口任意位置拖入多个文件。
- 倒序稳定分页（keyset cursor）与加载更早内容。
- 手动删除条目并清理附件；周期性清理临时文件、孤立附件与过期会话。
- WebSocket 实时通知，断线自动重连，回到前台重新拉取列表。
- 设备配对（四位码）与设备撤销；限流、Host / Origin / CSRF 校验、统一安全响应头。
- 局域网自动发现（mDNS/DNS-SD，`_flowdrop._tcp`）与可选 `auto_bind` 热更新绑定。
- 命令行投递 `flowdrop login` / `flowdrop send`（`--json` NDJSON）；Android 多电脑切换、自动选路、待发送队列与扫码配对。

## 快速开始

**桌面版（三步）**

1. 启动桌面版：双击 `flowdrop-desktop.exe`（Windows）或运行安装后的 `flowdrop-desktop`（Linux）。
2. 设置窗口点「添加设备」，得到四位配对码（手机可达时同时显示二维码）。
3. 手机打开 FlowDrop，选或填 Hub 地址（如 `http://192.168.1.10:8080`），输入配对码。

命令行最小流程：

```bash
flowdrop serve                    # 终端 1：启动 Hub，默认 http://127.0.0.1:8080
flowdrop login                    # 终端 2：把本机登记为 CLI 设备
flowdrop send --text - < note.txt # 登录后投递
```

## 下载与安装

预编译产物随 [最新 Release](https://github.com/xyz0z0/flowdrop/releases/latest) 发布（当前 **v0.0.3**）。只下载自己要用的文件，并与 `SHA256SUMS.txt` 中对应行逐字符比对。

| 平台 | 资产 |
|---|---|
| Windows 桌面（免安装） | `flowdrop-windows-amd64.zip`、`flowdrop-windows-arm64.zip` |
| Linux 桌面（amd64） | `flowdrop-desktop-linux-amd64.tar.gz` |
| Android（侧载） | `flowdrop-android-0.0.3.apk` |
| CLI · Linux / macOS | `flowdrop-cli-{linux,darwin}-{amd64,arm64}.tar.gz` |
| CLI · Windows | `flowdrop-cli-windows-{amd64,arm64}.zip` |

```bash
sha256sum flowdrop-desktop-linux-amd64.tar.gz                  # Linux
grep ' flowdrop-desktop-linux-amd64.tar.gz$' SHA256SUMS.txt
shasum -a 256 flowdrop-cli-darwin-arm64.tar.gz                 # macOS（无 sha256sum）
Get-FileHash .\flowdrop-windows-amd64.zip -Algorithm SHA256     # PowerShell
Select-String -Path .\SHA256SUMS.txt -Pattern 'flowdrop-windows-amd64.zip'
```

`SHA256SUMS.txt` 覆盖 Release 全部资产；`sha256sum -c` 只在已下载全部资产时适用。每个归档根目录都带 `LICENSE`、`THIRD_PARTY_NOTICES.md` 和 `third_party/licenses/`。

### Windows 桌面版

解压到任意目录后双击 `flowdrop-desktop.exe`，无需安装，不写系统目录。

- 首次启动在 Windows 防火墙对话框中允许「专用网络」，否则同一局域网的手机找不到这台电脑。
- 界面由 WebView2 渲染（Windows 11 自带；旧系统需 Microsoft Edge WebView2 Runtime），未做代码签名，SmartScreen 提示时选「仍要运行」。
- 数据在 `%LocalAppData%\flowdrop\`，配置在 `%AppData%\flowdrop\`，日志为 `desktop.log`；关闭窗口只是收进托盘，彻底退出用托盘菜单。

### Linux 桌面版

压缩包自带二进制、安装脚本、`.desktop` 模板和图标，解压即可安装：

```bash
tar -xzf flowdrop-desktop-linux-amd64.tar.gz && cd flowdrop-desktop-linux-amd64
./install-desktop.sh              # 安装到 ~/.local
./install-desktop.sh --uninstall  # 卸载启动器，保留 Hub 数据与设置
```

运行时依赖（Debian/Ubuntu 包名）：`libgtk-3-0t64 libwebkit2gtk-4.1-0 libsoup-3.0-0 libjavascriptcoregtk-4.1-0`（更早发行版用 `libgtk-3-0`）。脚本只写用户目录，不创建开机自启或系统服务。

### Android（APK 侧载）

下载 `flowdrop-android-0.0.3.apk`，允许「安装未知来源应用」后侧载，不经过应用商店。打开后在发现列表或手填 Hub 地址，输入电脑上生成的四位配对码。

### CLI

CLI 与 Hub 是同一个二进制：

```bash
tar -xzf flowdrop-cli-linux-amd64.tar.gz   # Windows: unzip flowdrop-cli-windows-amd64.zip
./flowdrop login                           # 同机登记为 CLI 设备
./flowdrop send --text - < note.txt        # 登录后投递
```

Linux 归档另带 `deploy/flowdrop.service`（systemd 用户单元，默认只监听 `127.0.0.1:8080`）与 `deploy/http-lan.env.example`（可信 LAN 明文 HTTP 模板），但不会自动安装服务；常驻配置见 [docs/DEPLOY.md §4](docs/DEPLOY.md#4-常驻运行systemd-用户服务)。

## 从源码构建

**依赖**：Go 1.26+（`desktop/`、`tools/winres/` 模块声明 `go 1.26.0`；`server/` 模块本身只需 Go 1.24+）、Node.js 20+、npm；桌面构建另需 GTK3 / WebKitGTK 开发包。

```bash
git clone https://github.com/xyz0z0/flowdrop.git flowdrop && cd flowdrop
FLOWDROP_COOKIE_SECURE=false scripts/dev.sh   # 终端 1：Hub :8080 + Vite :5173
(cd server && go run ./cmd/flowdrop pair)     # 终端 2：Hub 运行时取四位配对码
```

开发模式下 Vite 监听 <http://127.0.0.1:5173> 并把 `/api` 与 WebSocket 代理到 Go，必须通过 5173 端口访问；`FLOWDROP_COOKIE_SECURE` 默认为 `true`，本地开发用上面的开关关闭它。拿到码后用浏览器打开 5173 输入即可配对。

```bash
scripts/build.sh 0.1.0        # 前端构建 + 嵌入 → dist/flowdrop（单文件）
./dist/flowdrop doctor        # 环境自检；serve 默认 http://127.0.0.1:8080
```

**桌面版（Linux）**

```bash
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
scripts/build-desktop.sh      # → dist/flowdrop-desktop
scripts/install-desktop.sh    # 安装到 ~/.local，无需 sudo
```

**Android**：需 JDK 17+、Android SDK Platform 35 + Build-Tools 35，完整说明见 [android/README.md](android/README.md)。

```bash
cd android && ./gradlew :app:assembleDebug
# → android/app/build/outputs/apk/debug/app-debug.apk
```

## Hub 网络与部署

### 主路径：家庭局域网明文 HTTP

Hub 与手机/电脑在同一 Wi-Fi 时，直接监听**具体的局域网 IP**，无需 TLS：

```bash
# 例：Hub 局域网 IP 为 192.168.1.10（用 ip -4 addr 查看）
FLOWDROP_LISTEN=192.168.1.10:8080 FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
```

- `FLOWDROP_LISTEN` 必须写具体 IP，不能是 `0.0.0.0` / `::`：`cookie_secure=false` 时服务端**拒绝**通配地址启动；该 IP 自动加入 Host 白名单，`FLOWDROP_ALLOWED_HOSTS` 只补充 mDNS 名、Tailscale MagicDNS 名等。
- 明文 HTTP 不是安全上下文，浏览器可能禁用自动复制等能力（Web 端复制失败时展开可手动选择的文本）；仅限**可信家庭 LAN**，不要把 8080 端口映射到公网。
- `scripts/lan-http.sh` 自动探测并校验本机局域网 IP 后启动 Hub；`flowdrop doctor` 可在启动前检查端口与配对表。

### 局域网自动发现（mDNS/DNS-SD）

符合条件时 Hub **默认**发布 DNS-SD 记录（`discovery_enabled` / `FLOWDROP_DISCOVERY_ENABLED`），每个合格的 LAN 监听各一条：

| 字段 | 值 |
|---|---|
| 服务类型 | `_flowdrop._tcp.local.` |
| 实例名 | `FlowDrop-<主机昵称>`（安全截断，冲突时加 `-2` 等后缀） |
| SRV | Hub 的真实监听端口 |
| A | 该监听绑定的 RFC1918 LAN IPv4（**绝不含** Tailscale 地址） |
| TXT | `v=1`、`scheme=http`、`path=/` |

发布条件（全部满足）：监听地址是具体的 RFC1918 IPv4（10/8、172.16/12、192.168/16）、`cookie_secure=false`、地址在某块启用多播的网卡上。回环、`0.0.0.0`/`::`、Tailscale `100.64/10`、链路本地、公网地址、`auto_bind` 派生的容器/虚拟网桥（`docker0`、`br-*`、virbr 等）和 HTTPS 反代入口不发布（显式写进 `additional_listen` 的地址仍按显式配置处理）；记录中不含设备令牌、会话或机器路径。

```bash
FLOWDROP_DISCOVERY_ENABLED=false ./dist/flowdrop serve   # 显式关闭；辅助脚本用 scripts/lan-http.sh --no-discovery
```

mDNS 不需要安装 Avahi/Bonjour；UDP 5353 被其它进程占用时仅日志告警，不影响 HTTP 收发。排查见 [docs/DEPLOY.md §5.7](docs/DEPLOY.md#57-局域网自动发现mdnsdns-sd)。

### LAN + Tailscale 双绑

主监听 LAN、额外监听 Tailscale：同一 Hub、同一端口、同一数据库。手机在家走内网地址，在外走 Tailscale 地址，不需要重启或切换；`additional_listen` 也可写入 `~/.config/flowdrop/config.json` 的数组。

```bash
FLOWDROP_LISTEN=192.168.1.10:8080 FLOWDROP_ADDITIONAL_LISTEN=100.x.y.z:8080 \
FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve   # 只绑 Tailscale 时把 LISTEN 换成 tailscale ip -4 的输出
```

用 MagicDNS 名访问时把该名字加入 `FLOWDROP_ALLOWED_HOSTS`。Tailscale 直连**不需要** `tailscale serve`；若已用它提供 HTTPS，则保持 `cookie_secure=true`。可选加固（`tailscale serve`、Caddy 局域网 HTTPS）与细节见 [docs/DEPLOY.md §5.8](docs/DEPLOY.md#58-同时使用内网和-tailscale同一-hub-双监听)。

### auto_bind（跟随网卡变化）

`auto_bind=true` 时，Hub 在主监听的**同一端口**上绑定本机当前全部 RFC1918 局域网与 Tailscale 地址，每 5 秒重扫网卡：新地址自动上线、消失的地址自动下线，无需改配置重启。

```bash
FLOWDROP_AUTO_BIND=true FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
./dist/flowdrop doctor    # 启动前查看实际会绑哪些地址
```

回环、链路本地与公网地址不参与自动绑定；容器/虚拟网桥（`docker0`、`br-*`、virbr 等）及 `veth`/`wg*`/`tun*` 上的 RFC1918 地址**会绑定，但不做 mDNS 广播**。派生地址尽力而为，与显式 `additional_listen` 的全有或全无不同。细节见 [docs/DEPLOY.md §5.9](docs/DEPLOY.md#59-自动绑定本机所有可用地址auto_bind)。

## 客户端要点

**桌面版（Linux + Windows，Wails v3.1.0）**

- 双击启动即在本进程内拉起同一个 Go Hub 并打开 React 收件箱；关闭窗口收进托盘、Hub 继续运行（系统无托盘时会改为确认退出，不留后台进程），托盘菜单可显示窗口、打开设置、明确退出并停止 Hub（撤回 mDNS、释放端口）。
- 设置窗口：选择真实本机 IPv4（LAN / Tailscale 自动标注）与端口，一键重启 Hub；支持双模式与 `auto_bind`。
- 「添加设备」生成四位码（仅本机显示、不经网络、不写日志）与配对二维码；设备可本机撤销。
- 登录后把图片或普通文件拖到收件箱窗口任意位置即上传（目录跳过、不递归）；应用内代理直连让系统代理开启时 HTTP 与 WebSocket 仍可直连。

**Android**

- 配对码配对、设备令牌存入 Android Keystore、文字/图片/文件收发、系统分享入口、实时同步与下载。
- 多电脑切换与自动选路：每台电脑各配对一次，点顶部电脑名切换，启动时恢复上次使用的电脑；保存各 Hub 的稳定 ID、设备令牌与候选地址，优先 LAN、其次 Tailscale，只在连接层重试，不重放写操作。
- 待发送队列：条目记住原目标电脑，切换电脑不会改投；结果无法确认的条目标为「投递未确认」，只有用户确认后手动重试才再发。
- 扫码配对：扫码只预填配对码与候选地址，仍需用户确认后提交，不自动登录。

**CLI**

- `flowdrop login` 把本机登记为设备（同机默认在本地数据库生成配对码），凭据存 `~/.config/flowdrop/cli.json`（0600）；`flowdrop send [--json] FILE...`、`--text TEXT|-`（stdin ≤64 KiB）、`--link URL`、`--name NAME -`，退出码 0/1/2 且不自动重试，临时调用可用 `FLOWDROP_HUB` + `FLOWDROP_TOKEN` 不落盘。用法见 [docs/CLI_AGENT.md](docs/CLI_AGENT.md)。

## 安全模型

| 面 | 措施 |
|---|---|
| 传输 | 默认主路径是可信家庭 LAN 明文 HTTP；跨网/不可信网络请用 Tailscale Serve 或 TLS 反代 |
| 认证 | 四位临时配对码（120 秒、一次成功、全 Hub 累计错 5 次锁定，本机重新生成码即解锁）+ 32B 设备令牌（DB 只存 SHA-256），可撤销 |
| CSRF | 双提交令牌 + Origin / Host 校验；Host 白名单防 DNS rebinding，Web 与原生 App 同一套 |
| 越权 | 所有条目、预览、下载、WebSocket 统一会话鉴权，知道 ID 也不够 |
| 上传与文件 | 流式写入、大小上限、磁盘余量预检、并发上限、原子 rename；服务端嗅探 MIME，SVG/HTML/JS 强制下载；文件名不参与路径 |
| 隐私 | 日志不含内容原文 / 设备令牌 / 会话；无第三方分析、无外链资源 |

## 已知限制

- **主路径为明文 HTTP**：局域网内传输不加密，同网段设备或中间人可读取内容与 Cookie。
- **LAN HTTP 下浏览器能力受限**：`navigator.clipboard`、Service Worker、通知等可能不可用。
- **Web 不自动切换地址**：Cookie 按 origin 保存，LAN → Tailscale 需重新配对；Android 会自动选路。
- 浏览器 Cookie TTL 为 7 天（`session_ttl_hours`），到期需重新配对；设备令牌直到本机撤销前一直有效。
- 每个 Hub 单用户、收件箱独立；无多租户、无端到端加密、无自动过期（仅保留 `expires_at` 字段）。
- 大文件上限 512 MiB（可配置），无断点续传，中断需重传。
- 只监听具体 IP：明文 HTTP（`cookie_secure=false`）时服务端拒绝 `0.0.0.0` / `::`，`cookie_secure=true` 允许通配但只告警。显式 `additional_listen` 改地址需改配置并重启。mDNS 仅在具体 RFC1918 LAN IPv4 + 明文 HTTP 时发布，UDP 5353 被占用时可能失败。
- Android 真机安装、真实 Wi-Fi / Tailscale 自动选路与真实局域网 mDNS 发现：**待验收**；桌面版仅 Linux amd64 与 Windows（arm64 交叉编译**待验收**），macOS 桌面版未实现。

## 文档索引

| 文档 | 内容 |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | 架构与数据模型、认证会话、上传流水线、桌面与多监听设计 |
| [docs/DEPLOY.md](docs/DEPLOY.md) | 安装、构建、常驻、LAN / Tailscale / HTTPS、备份恢复、配置参考 |
| [docs/PAIRING.md](docs/PAIRING.md) · [docs/CLI_AGENT.md](docs/CLI_AGENT.md) | 配对接口与二维码；命令行 / HTTP 的 agent 用法与退出码语义 |
| [android/README.md](android/README.md) | Android 功能范围、协议基线、构建与测试 |
| [CONTRIBUTING.md](CONTRIBUTING.md) · [SECURITY.md](SECURITY.md) | 贡献流程；漏洞报告方式 |

## 目录结构

```text
flowdrop/
├── docs/          架构、部署、配对、CLI 用法
├── server/        Go Hub（cmd + internal）
├── web/           React + TS + Vite 前端
├── desktop/       Wails 桌面壳（Linux / Windows）
├── android/       Android 原生 App（含构建与测试说明）
├── tests/         Playwright 端到端
├── deploy/        systemd 用户服务、Caddy 局域网 HTTPS 模板（可选）
└── scripts/       dev.sh / build.sh / lan-http.sh（明文 LAN 启动辅助）
```

## 许可证

本项目采用 [MIT 许可证](LICENSE)。第三方组件许可见[第三方软件声明](THIRD_PARTY_NOTICES.md)；安全问题请勿公开提交 issue，先按 [SECURITY.md](SECURITY.md) 私下联系维护者。

Copyright (c) 2026 xyz0z0
