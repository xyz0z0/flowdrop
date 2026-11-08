# FlowDrop 部署与运维（Linux Hub）

本文覆盖：安装、首次初始化、**局域网明文 HTTP（主路径）**、可选的 Tailscale / HTTPS 加固、
systemd 用户服务、备份、恢复、升级、卸载、故障排查。

所有命令以普通用户 `bob` 为例，路径为默认值：

| 项目 | 默认位置 |
|---|---|
| 二进制 | `~/.local/bin/flowdrop` |
| 数据目录（数据库 + 附件） | `~/.local/share/flowdrop/` |
| 配置文件（可选） | `~/.config/flowdrop/config.json` |
| systemd 用户单元 | `~/.config/systemd/user/flowdrop.service` |
| 监听地址 | `127.0.0.1:8080`（默认仅回环，可改为局域网地址） |

> **当前产品决定（2026-11-07）**：家庭**可信局域网内直接用明文 HTTP** 是主路径，
> 不需要 Tailscale、Caddy 或任何证书。这是用户已明确接受的边界：同网段的其它设备
> 或中间人理论上可读取内容与 Cookie。**不要**把该端口映射到公网。
> 跨网用 Tailscale 直连（§7.1）；需要加密时，Tailscale Serve（§7.2）或 Caddy（§8）
> 保留为**可选**加固。

> **浏览器能力提醒**：明文 HTTP 的局域网地址**不是** secure context。`navigator.clipboard`、
> Service Worker、通知等 API 可能被禁用。Web 端复制失败时会展开可手动选择的文本；
> 这是设计内的降级，不是缺陷。

---

## 1. 安装依赖（Kubuntu / Ubuntu）

```bash
sudo apt update
sudo apt install -y golang-go nodejs npm curl
# 可选：局域网 HTTPS 入口
sudo apt install -y caddy
```

验证：

```bash
go version     # 1.24+（本机实测 1.26.8）
node -v        # 20+
npm -v
```

## 2. 构建与安装二进制

在仓库根目录：

```bash
scripts/build.sh 0.1.0          # 前端嵌入 → dist/flowdrop
mkdir -p ~/.local/bin
install -m 0755 dist/flowdrop ~/.local/bin/flowdrop
```

自检：

```bash
flowdrop doctor
```

期望输出（首次会提示 admin password missing，属正常）：

```
FlowDrop doctor 0.1.0
listen                 ok   127.0.0.1:8080
data_dir               ok   /home/bob/.local/share/flowdrop
database               ok   schema v1
admin password         WARN missing — run 'flowdrop admin init'
free space             ok   ...
port available         ok   can bind 127.0.0.1:8080
```

## 3. 初始化管理员密码

**必须在 Hub 本机执行**，不接受远程初始化：

```bash
flowdrop admin init
# New admin password: **********      （至少 10 字符）
# Repeat password: **********
# Admin password set for user "admin" in /home/bob/.local/share/flowdrop
```

- 密码使用 Argon2id（m=64 MiB, t=3, p=2）哈希后存 SQLite，不落日志、不写配置文件。
- 忘记密码：重新执行 `flowdrop admin init --force`，会同时吊销所有会话。
- 脚本/自动化可用 `flowdrop admin init --password-stdin`（两行：密码、确认）。

手动前台启动验证：

```bash
flowdrop serve
curl -s http://127.0.0.1:8080/api/v1/health
```

## 4. 常驻运行（systemd 用户服务）

```bash
mkdir -p ~/.config/systemd/user
install -m 0644 deploy/flowdrop.service ~/.config/systemd/user/flowdrop.service
systemctl --user daemon-reload
systemctl --user enable --now flowdrop.service
systemctl --user status flowdrop.service --no-pager
journalctl --user -u flowdrop.service -n 30 --no-pager
```

保持用户服务在**未登录时**也运行（推荐，Hub 是家用常驻服务）：

```bash
sudo loginctl enable-linger "$USER"
```

> 单元文件使用 `ProtectHome=read-only`，仅把数据目录设为可写。如果自定义了
> 数据目录或二进制位置，请同步修改 `ExecStart` / `ReadWritePaths`。
>
> 仓库自带的单元默认 `FLOWDROP_LISTEN=127.0.0.1:8080`（回环）。若用第 5 节的
> 明文 LAN 主路径，请按 §5.6 用 drop-in 覆盖 `FLOWDROP_LISTEN` / `FLOWDROP_ALLOWED_HOSTS` /
> `FLOWDROP_COOKIE_SECURE`（同一变量在 drop-in 中的值会覆盖主单元）。

日志中不会出现内容原文、密码或会话令牌；如需调试可临时
`systemctl --user set-environment FLOWDROP_LOG_LEVEL=debug`。

---

## 5. 主路径：家庭局域网明文 HTTP（Hub + 手机 + 电脑）

适用：Hub（Linux 电脑）、Android 手机、其它电脑在**同一个可信 Wi-Fi**。
不需要 Tailscale、Caddy、证书或手机导入 CA。

### 5.1 找到 Hub 的局域网地址

```bash
ip -4 addr show | grep -w inet      # 例如 192.168.31.184
```

### 5.2 前台试跑（先验证再常驻）

```bash
FLOWDROP_LISTEN=192.168.31.184:8080 \
FLOWDROP_COOKIE_SECURE=false \
flowdrop serve
```

`FLOWDROP_ALLOWED_HOSTS` 可以省略：**监听的那个 IP 会自动加入 Host 白名单**。
只有想用别的名字访问（mDNS 名、主机名、Tailscale MagicDNS 名）时才需要补：

```bash
FLOWDROP_ALLOWED_HOSTS=flowdrop.local,my-host.tailnet.ts.net,localhost,127.0.0.1
```

仓库还提供两个辅助：

- `deploy/http-lan.env.example`：可直接 `set -a; . deploy/http-lan.env.example; set +a` 的环境模板。
- `scripts/lan-http.sh`：自动探测本机局域网 IPv4，校验是私有地址且在本机网卡上，
  然后设好上述三个变量并启动（拒绝 `0.0.0.0` 与公网地址）。

关键约束：

- `FLOWDROP_LISTEN`：**必须写具体的局域网 IP**。`cookie_secure=false` 时 Hub 会
  **拒绝** `0.0.0.0` / `::`（避免在所有网卡上开明文端口），实测报错：
  `listen must name the specific LAN IP when cookie_secure=false`。
- `FLOWDROP_ALLOWED_HOSTS`：可选。监听 IP 已自动允许；其它访问名（mDNS/主机名/
  Tailscale 名）必须列出，否则 Hub 返回 `421 host_not_allowed`（有意的 DNS rebinding 防护）。
- `FLOWDROP_COOKIE_SECURE=false`：**明文 HTTP 下必须关闭**，否则浏览器不会保存会话 Cookie。

三项都是**现有配置键**，没有为 HTTP 模式新增配置键。

### 5.3 客户端访问

- 电脑浏览器：`http://192.168.31.184:8080`
- Android 原生 App：Hub 地址填 `http://192.168.31.184:8080`，用同一管理员密码登录。
  协议与 Web 完全相同（Cookie + CSRF + Origin），没有单独的配对或令牌端点。
- 手机浏览器也可以直接用同一地址（能力受限见下）。

### 5.4 明文 HTTP 下的能力边界

| 能力 | LAN HTTP 下的实际情况 |
|---|---|
| 登录 / 收发文字、链接 | 正常 |
| 上传 / 下载 / 预览 | 正常 |
| 复制到剪贴板 | **可能不可用**。失败时 Web 会展开可手动选择（全选）的文本框，并说明原因 |
| Paste 截图上传 | 取决于浏览器；不支持时用“选择文件” |
| 后台常驻 WS / 系统通知 | **不保证**；回前台会重新拉取列表 |
| Service Worker / PWA 安装 | **不保证**（要求安全上下文） |

> **不承诺**在 LAN HTTP 下浏览器后台剪贴板或仅安全上下文 API 可用。

### 5.5 防火墙

如果 Kubuntu 启用了 ufw，需要放行 8080：

```bash
sudo ufw allow from 192.168.31.0/24 to any port 8080 proto tcp
```

（这是用户自己的网络决策；本项目脚本**不会**自动修改防火墙。不要用 `allow 8080`
对公网开放。）

### 5.6 常驻配置（systemd 环境变量）

在单元文件或 `systemctl --user edit` 的 drop-in 中设置：

```ini
Environment=FLOWDROP_LISTEN=192.168.31.184:8080
Environment=FLOWDROP_COOKIE_SECURE=false
# 仅当需要用 mDNS 名 / 主机名 / Tailscale 名访问时再加：
# Environment=FLOWDROP_ALLOWED_HOSTS=flowdrop.local,localhost,127.0.0.1
```

```bash
systemctl --user daemon-reload && systemctl --user restart flowdrop
flowdrop doctor      # 会提示“明文 HTTP 模式”风险，那是本模式下的预期提示
```

### 5.7 局域网自动发现（mDNS/DNS-SD）

**默认行为**：当 `listen` 是**具体的 RFC1918 局域网 IPv4**（10/8、172.16/12、192.168/16）
且 `cookie_secure=false` 时，Hub 自动发布一条 DNS-SD 记录，供同一 Wi-Fi 的客户端发现：

| 字段 | 值 |
|---|---|
| 服务类型 | `_flowdrop._tcp`，域 `local.` |
| 实例名 | `FlowDrop-<主机昵称>`；昵称只保留安全 ASCII，超长按 63 字节 DNS label 截断，重名自动加 `-2`、`-3`… |
| SRV | 真实 Hub 端口（不是固定 8080） |
| A | **仅** `listen` 绑定的那个 RFC1918 IPv4，且只从对应网卡发出 |
| TXT | `v=1`、`scheme=http`、`path=/` |

记录中**不包含**密码、会话令牌、条目正文、文件路径或机器名以外的隐私信息。

**不会发布**的情形（即使 `discovery_enabled=true`）：

- 回环地址（`127.0.0.1` / `::1`）——没有 LAN 入口可广播。
- 通配地址（`0.0.0.0` / `::` / 空 host）——避免在所有网卡泄露。
- Tailscale `100.64.0.0/10`——跨网地址不冒充同一 LAN。
- 链路本地 `169.254.0.0/16`、公网地址、IPv6 地址。
- `cookie_secure=true`（Tailscale Serve / Caddy HTTPS 反代入口）——只发布可直接登录的明文 HTTP LAN 入口。

**配置**：

```json
{
  "listen": "192.168.31.184:8080",
  "cookie_secure": false,
  "discovery_enabled": true
}
```

- 省略 `discovery_enabled`（默认）= 按上述条件**自动**启用。
- `false` = 明确关闭，不监听/不发布 mDNS。环境变量：`FLOWDROP_DISCOVERY_ENABLED=false`。
- `true` = 明确想启用；但地址不符合条件时仍然跳过，并在日志写明原因（WARN）。

检查当前会不会发布：

```bash
flowdrop doctor | grep 'mdns discovery'
# 例：mdns discovery  ok   _flowdrop._tcp.local. on 192.168.31.184:8080 via eno1
# 例：mdns discovery  ok   off: listen address is loopback; ...
# 例：mdns discovery  ok   off: ... Tailscale/CGNAT; ...
```

用系统工具独立验证（若安装了 avahi-utils；未安装也不影响 Hub）：

```bash
avahi-browse -rt _flowdrop._tcp      # 应看到 FlowDrop-<host>、端口、TXT 与 A 地址
```

实现说明与限制：

- 使用纯 Go mDNS 生态（`zeroconf` 浏览/冲突扫描 + `miekg/dns` 记录编解码），
  不依赖 Avahi/Bonjour/Caddy；与系统 Avahi 共用 UDP 5353 是**尽力而為**。
  若 UDP 5353 被其它进程占用或网卡不允许多播，Hub 只在日志告警，HTTP 收发不受影响。
- 只向绑定的那块网卡发送；发布器还会按 control-message 的接口索引丢弃其它网卡的查询，
  因此 `tailscale0` / `docker0` 等接口上的 mDNS 查询不会得到响应。A 记录只有该网卡的
  绑定 IPv4，不会把 Docker/Tailscale 地址一起广播。
- 记录 TTL 为 120 秒；正常退出会发送 TTL=0 注销并释放 socket。异常崩溃后，客户端缓存最多约 2 分钟自动过期。
- Android 客户端按同一冻结协议浏览 `_flowdrop._tcp` 并用 SRV/A/TXT 构造 URL；
  本轮只验证 Hub 端发布，真机发现 UI 仍待验收。手动填 IP 的主路径不受影响。

### 5.8 同时使用内网和 Tailscale（同一 Hub 双监听）

**适用**：希望手机在家用 Wi-Fi 走内网地址、在外用 Tailscale 地址，而不必每次
重启/切换绑定地址。**一个 Hub 进程**同时监听两个（或多个）具体 IPv4 的同一端口，
共享同一个数据库、会话、事件广播（WebSocket）和同一把数据目录写锁。

> **历史说明**：较早版本一个 Hub 只能绑定一个具体 IP，LAN 与 Tailscale 需要重启切换；
> 该单 IP 限制已由本节取代，旧验证记录仍保留在 VERIFICATION 的历史章节中。

CLI 示例（主监听 LAN，额外监听 Tailscale）：

```bash
FLOWDROP_LISTEN=192.168.31.184:8080 \
FLOWDROP_ADDITIONAL_LISTEN=100.125.168.63:8080 \
FLOWDROP_COOKIE_SECURE=false \
flowdrop serve
```

等价的 `~/.config/flowdrop/config.json`：

```json
{
  "listen": "192.168.31.184:8080",
  "additional_listen": ["100.125.168.63:8080"],
  "cookie_secure": false
}
```

规则与边界：

| 项 | 行为 |
|---|---|
| 地址格式 | `additional_listen` 只接受**字面 IP + 端口**的具体地址；通配 `0.0.0.0`/`::`、空 host、主机名被拒绝。本产品只用具体 IPv4（桌面界面也只允许 IPv4） |
| 端口 | 每个地址各自写端口；内网 + Tailscale 通常使用同一端口（不同 IP 不冲突） |
| 重复 | 与主监听完全相同的地址只绑定一次（主地址优先）；不同 IP 的同端口是有效监听 |
| Host 白名单 | **每个实际监听 IP 都会自动加入**；`allowed_hosts` 只用于补充 mDNS 名/Tailscale MagicDNS 名等 |
| 启动原子性 | 先绑定主地址，再绑定全部附加地址；任一失败则关闭已绑定监听、释放数据目录锁并返回错误，**不会只启动一半** |
| 停止 | 释放全部监听端口，只撤回本实例发布的 mDNS 记录 |
| mDNS | 只对符合条件的 **RFC1918 LAN** 监听地址发布；Tailscale `100.64/10` 监听不广播 |
| 会话/Cookie | 浏览器按 host 隔离 Cookie；不同地址之间**不承诺**自动共享登录态，需各自登录。数据本身共享 |
| 主地址 | `FLOWDROP_LISTEN` 仍是主地址；日志、`URL()`、桌面收件箱优先使用它 |

systemd drop-in 示例：

```ini
Environment=FLOWDROP_LISTEN=192.168.31.184:8080
Environment=FLOWDROP_ADDITIONAL_LISTEN=100.125.168.63:8080
Environment=FLOWDROP_COOKIE_SECURE=false
```

```bash
systemctl --user daemon-reload && systemctl --user restart flowdrop
curl -fsS http://192.168.31.184:8080/api/v1/health
curl -fsS http://100.125.168.63:8080/api/v1/health
```

**明确不做**：自动网络重绑定、自动选路、两个共享数据目录的 Hub、`0.0.0.0` 监听。
地址失效（如 Tailscale 退出、DHCP 换 IP）时启动会明确失败；修改地址后重启即可。
桌面版的双模式操作见 §16.6。

## 6. Android 原生 App

Android worker 已完成构建，协调者已核对 APK 文件存在及 SHA-256。
产物：`android/app/build/outputs/apk/debug/app-debug.apk`（debug 签名，仅用于试用）。

```bash
cd android
./gradlew :app:assembleDebug
# app/build/outputs/apk/debug/app-debug.apk
```

详细环境、安装与客户端说明见 [android/README.md](../android/README.md)。
模拟器已验证；真实手机、Wi-Fi 与跨网 Tailscale 仍需实测。

Android discovery worker 自报（本轮未复跑）：APK SHA-256
`f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8`；
53 个 JVM 测试全通过，41 个设备测试中 39 个执行通过、2 个 LiveHub 因无参数跳过；
lint/assemble 通过。实际 Hub 在 `192.168.31.184:18099` 广播，但 API 36 模拟器
20 秒内没有 `onServiceFound`；真实手机 mDNS 互通仍未验证，详见 VERIFICATION §12.9。

安装后：

1. 打开 App，Hub 地址填 `http://<Hub-IP>:8080`（与第 5 节一致）。
2. 用 `flowdrop admin init` 设置的管理员密码登录（**初始化仍只在 Hub 本机 CLI**）。
3. 登录后 App 持有服务端会话 Cookie，并对写操作带 `X-FlowDrop-CSRF`；
   `Origin` 由 App 声明的 Hub 地址决定，需落在 `allowed_hosts` 内。

局域网自动发现协议已冻结（`_flowdrop._tcp`、`FlowDrop-<昵称>`、SRV 真实端口、
TXT `v=1,scheme=http,path=/`、A 仅绑定 LAN IPv4）。Android 客户端按同一协议实现；
本轮只验证 Hub 端发布，Android 发现 UI 与真机验收仍未完成。

诊断建议（仅在真机实测时使用）：

- `421 host_not_allowed` → App 里配置的 Host 不在 `FLOWDROP_ALLOWED_HOSTS`。
- 登录后立刻回登录页 → 把 `FLOWDROP_COOKIE_SECURE` 与访问协议保持一致（HTTP 用 `false`）。
- 实时不更新 → 确认 App 侧 WebSocket 连的是 `ws://` 而非 `wss://`。

---

## 7. 访问路径 C：Tailscale（直连 HTTP 或可选 Serve）

前提：Hub 与手机都在同一 tailnet（手机安装 Tailscale 并登录）。

### 7.1 Tailscale 直连（HTTP，不需要 Serve 或证书）

取 Hub 的 Tailscale IPv4，并把它作为**唯一监听地址**：

```bash
# Hub 上执行；输出形如 100.125.168.63
tailscale ip -4

# 停止之前的 LAN 监听进程，然后：
FLOWDROP_LISTEN=100.125.168.63:8080 \
FLOWDROP_COOKIE_SECURE=false \
flowdrop serve

# 或用辅助脚本（会校验地址在本机网卡上）
scripts/lan-http.sh --host 100.125.168.63 --port 8080
```

- 手机/电脑在同一 tailnet 内访问 `http://100.125.168.63:8080`，用同一管理员密码登录。
- 若用 MagicDNS 名（`http://hub.tailnet-name.ts.net:8080`），把该名字加入
  `FLOWDROP_ALLOWED_HOSTS`；监听 IP 本身会自动允许。
- **mDNS 发现不会广播 Tailscale 地址**：`100.64/10` 是跨网地址，不是同一 LAN 入口；
  日志会显示跳过原因，这是预期行为。
- **推荐同时绑定**：主监听用 LAN 地址，另加
  `FLOWDROP_ADDITIONAL_LISTEN=100.x.y.z:8080`，同一个 Hub 即可同时提供 LAN 与
  Tailscale 直连（见 §5.8）。旧版本“一个进程只绑定一个 IP”的限制已是历史行为；
  仍不要用两个共享数据目录的 Hub 绕过。
- 可选：若愿意用 `tailscale serve` 的 HTTPS，则保持 `cookie_secure=true`，见下一节。

### 7.2 可选的 Tailscale Serve（HTTPS）

```bash
# Hub：把本机 8080 暴露为 tailnet 内的 HTTPS（Tailscale 负责证书）
tailscale serve --bg 8080
tailscale serve status
```

输出形如：

```
https://bob-hpelitedesk880g6.xyz0z0.ts.net/ (tailnet only)
|-- / proxy http://127.0.0.1:8080
```

手机浏览器访问该 `https://…ts.net` 地址即可。证书由 Tailscale 自动签发并被
系统信任，**无需**手动导入 CA。

Hub 侧建议配置（Tailscale 反代来源为回环，故信任回环作为代理）：

```json
{
  "listen": "127.0.0.1:8080",
  "allowed_hosts": ["bob-hpelitedesk880g6.xyz0z0.ts.net", "localhost", "127.0.0.1"],
  "trusted_proxies": ["127.0.0.1", "::1"],
  "cookie_secure": true
}
```

写入 `~/.config/flowdrop/config.json` 后 `systemctl --user restart flowdrop`。

- `tailscale serve` 默认只对 tailnet 内可见，**不暴露公网**（不要用
  `tailscale funnel`；本项目的安全模型不以公网暴露为前提）。
- 应用层仍强制密码登录：Tailscale ACL 不替代应用鉴权。

关闭：

```bash
tailscale serve reset
```

## 8. 访问路径 D（可选）：局域网 HTTPS（手机不装 Tailscale）

同一 Wi-Fi 下用局域网地址访问，但希望**加密传输**时使用本方案。
注意：**主路径已改为第 5 节的明文 HTTP**；本节仅为可选加固（例如你不信任所在 LAN）。
浏览器要求安全上下文（HTTPS）才可靠使用剪贴板等能力，因此这里用 **Caddy + 本地 CA**。

### 8.1 Caddy 配置

```bash
sudo cp deploy/Caddyfile.example /etc/caddy/Caddyfile.d/flowdrop.caddy
sudoedit /etc/caddy/Caddyfile.d/flowdrop.caddy
```

最少要改两处：

- `flowdrop.lan` → 你选择的局域网主机名；
- `192.168.1.10` → Hub 的局域网 IP（`ip -4 addr show` 查看）。

如果你用现有的主 Caddyfile，把站点块合并进去即可。确保主配置包含：

```caddyfile
import /etc/caddy/Caddyfile.d/*.caddy
```

让手机能用名字访问，可在家用路由器/DHCP 里把 `flowdrop.lan` 指向 Hub 的 IP，
或在手机里手工添加 DNS 记录。仅用 IP 访问时，证书 SAN 必须包含该 IP
（上面的站点块已把 IP 写进站点地址，Caddy 会自动加入 SAN）。

应用配置：

```bash
sudo systemctl enable --now caddy
sudo systemctl reload caddy
sudo journalctl -u caddy -n 30 --no-pager
```

Hub 配置（对应 `~/.config/flowdrop/config.json`）：

```json
{
  "listen": "127.0.0.1:8080",
  "allowed_hosts": ["flowdrop.lan", "192.168.1.10", "localhost"],
  "trusted_proxies": ["127.0.0.1", "::1"],
  "cookie_secure": true
}
```

```bash
systemctl --user restart flowdrop
```

> **注意**：`allowed_hosts` 必须包含手机地址栏里实际使用的那个 Host；
> 否则 Hub 会返回 `421 host_not_allowed`（这是有意的 DNS rebinding 防护）。

### 8.2 导出 Caddy 根证书

```bash
sudo cat /var/lib/caddy/.local/share/caddy/pki/authorities/local/root.crt \
  > ~/flowdrop-caddy-root.crt
```

文件名随 Caddy 版本/打包方式可能不同，找不到时：

```bash
sudo find / -name 'root.crt' -path '*caddy*' 2>/dev/null
```

### 8.3 在手机上导入并信任该 CA（必须）

**Android（Chrome）**

1. 把 `flowdrop-caddy-root.crt` 传到手机（可以用 FlowDrop 自己，或 USB 拷贝）。
2. 设置 → 安全 → 加密与凭据 → 安装证书 → **CA 证书** → 选择该文件。
   路径在 Android 12+ 大致为：设置 → 安全 → 更多安全设置 → 加密与凭据 →
   安装证书 → CA 证书。
3. 系统会显示“你的网络可能被监控”的警告，这是安装用户 CA 的正常提示。
4. Chrome 里访问 `https://flowdrop.lan/`，地址栏应显示正常锁形图标。

**Firefox for Android** 使用独立证书库：设置 → 隐私与安全 → 证书 →
安装 CA 证书（部分版本需在 `about:config` 打开企业根支持）。

**iOS / iPadOS**（如需要）：把 `.crt` 用 AirDrop/邮件发送 → 设置 →
通用 → VPN 与设备管理 → 安装描述文件 → 然后到 设置 → 通用 → 关于本机 →
证书信任设置 → 对该根证书开启“完全信任”。

> 未导入 CA 时浏览器会拦截，且 FlowDrop **不会**提供“忽略警告”的运行模式。
> 若无法导入 CA，请改用 §7.2 的 Tailscale Serve，它使用系统信任的证书。

### 8.4 局域网防火墙

如果 Kubuntu 启用了 ufw，需要放行 443（Caddy）：

```bash
sudo ufw allow 443/tcp
```

（这是用户自己的网络决策；本项目安装脚本**不会**自动修改防火墙。）

---

## 9. 配置参考

配置文件为 JSON（可选）；`FLOWDROP_*` 环境变量优先级更高。

| 键 | 环境变量 | 默认 | 说明 |
|---|---|---|---|
| `listen` | `FLOWDROP_LISTEN` | `127.0.0.1:8080` | 主 HTTP 监听地址。明文 LAN 模式须写具体局域网 IP（`0.0.0.0` 会被拒绝） |
| `additional_listen` | `FLOWDROP_ADDITIONAL_LISTEN` | 空 | 同一 Hub 额外监听的 `host:port` 数组；只接受具体 IP，用于 LAN + Tailscale 双监听（§5.8） |
| `data_dir` | `FLOWDROP_DATA_DIR` | `~/.local/share/flowdrop` | 数据目录 |
| `allowed_hosts` | `FLOWDROP_ALLOWED_HOSTS` | 空（回环名 + 具体监听 IP） | 逗号分隔的 Host 白名单；用于 mDNS/主机名/Tailscale 名 |
| `trusted_proxies` | `FLOWDROP_TRUSTED_PROXIES` | 空 | 采信 `X-Forwarded-*` 的 IP/CIDR |
| `max_upload_bytes` | `FLOWDROP_MAX_UPLOAD_BYTES` | `536870912` (512 MiB) | 单文件上限 |
| `max_concurrent_uploads` | `FLOWDROP_MAX_CONCURRENT_UPLOADS` | `4` | 并发上传数 |
| `min_free_bytes` | `FLOWDROP_MIN_FREE_BYTES` | `1073741824` (1 GiB) | 磁盘保留量 |
| `session_ttl_hours` | `FLOWDROP_SESSION_TTL_HOURS` | `168` (7 天) | 会话有效期（滑动续期） |
| `cookie_secure` | `FLOWDROP_COOKIE_SECURE` | `true` | Cookie 仅 HTTPS 发送 |
| `discovery_enabled` | `FLOWDROP_DISCOVERY_ENABLED` | 空 = 自动（满足 §5.7 条件时发布） | mDNS/DNS-SD 局域网自动发现；`false` 明确关闭 |
| `login_limit_per_minute` | `FLOWDROP_LOGIN_LIMIT_PER_MINUTE` | `5` | 每 IP 登录尝试 |
| `api_limit_per_minute` | `FLOWDROP_API_LIMIT_PER_MINUTE` | `600` | 每会话 API 请求 |

> 明文 HTTP 局域网部署（主路径）需要 `FLOWDROP_COOKIE_SECURE=false`；
> 经 Tailscale/Caddy 的 HTTPS 部署应保持 `true`。两者必须与实际访问协议一致，
> 否则会出现“登录成功但立即回登录页”。

## 10. 备份

需要备份的只有数据目录（SQLite 数据库 + `blobs/`）：

```bash
# Hub 运行中也可安全备份：SQLite 在线备份 API，避免复制到写了一半的 WAL。
mkdir -p ~/backups
flowdrop backup ~/backups/flowdrop-$(date +%F).tar.gz   # 见下节说明
```

若不想使用内置命令，可用 SQLite 官方方式：

```bash
systemctl --user stop flowdrop        # 最稳妥：短暂停机
tar -czf ~/backups/flowdrop-$(date +%F).tar.gz \
    -C ~/.local/share flowdrop
systemctl --user start flowdrop
```

**不要把包含密码/会话的数据库放进未加密的云同步目录。**

## 11. 恢复

```bash
systemctl --user stop flowdrop
mv ~/.local/share/flowdrop ~/.local/share/flowdrop.old
tar -xzf ~/backups/flowdrop-YYYY-MM-DD.tar.gz -C ~/.local/share
chmod 700 ~/.local/share/flowdrop
systemctl --user start flowdrop
flowdrop doctor
```

恢复后旧会话仍然有效（会话表随库恢复）。若不希望保留，重新执行
`flowdrop admin init --force` 即可吊销全部会话。

## 12. 升级

```bash
# 1. 备份（见上）
git pull
scripts/build.sh 0.1.1
systemctl --user stop flowdrop
install -m 0755 dist/flowdrop ~/.local/bin/flowdrop
systemctl --user start flowdrop
flowdrop doctor        # 确认 schema 版本已迁移
```

数据库迁移在启动时自动执行，版本记录在 `schema_migrations` 表。降级不受支持，
回退前请恢复备份。

## 13. 卸载

```bash
systemctl --user disable --now flowdrop.service
rm -f ~/.config/systemd/user/flowdrop.service
systemctl --user daemon-reload
rm -f ~/.local/bin/flowdrop

# 数据（附件与历史）。确认不再需要后再删除。
rm -rf ~/.local/share/flowdrop

# 如果配置过 Tailscale Serve
tailscale serve reset

# 如果配置过 Caddy 站点
sudo rm -f /etc/caddy/Caddyfile.d/flowdrop.caddy && sudo systemctl reload caddy
```

用户在手机上安装的 CA 证书需在手机设置中手动移除。

## 14. 故障排查

| 现象 | 原因与处理 |
|---|---|
| `421 Host not allowed` | 访问用的 Host 不在 `allowed_hosts`。把地址栏里的主机名加入配置并重启。明文 HTTP 局域网路径也要把 Hub 的局域网 IP 加进去。 |
| 登录后立刻又回到登录页 | 用了 HTTPS 访问但 `cookie_secure=false`，或反之。让对方匹配实际协议（LAN HTTP 用 `false`）。 |
| `no admin password set` | 还没执行 `flowdrop admin init`。 |
| 手机图片上传失败 | 常是反向代理的请求体上限/超时；确认 `flush_interval -1` 和较长的 `read_timeout`。 |
| 复制按钮无效 / 只能手动复制 | **明文 HTTP 下预期行为**（非 secure context）。Web 会展开可手动选择的文本框；若希望自动复制，用 Tailscale 或 Caddy HTTPS。 |
| 手机浏览器没有通知 / 不能装 PWA | 明文 HTTP 非 secure context，属预期；不承诺这些 API 可用。 |
| 磁盘满 | 删除旧条目；`min_free_bytes` 会在空间不足时拒绝新上传。 |
| 附件丢失但条目在 | `journalctl --user -u flowdrop` 查 janitor 日志；文件被外部删除时会返回 404。 |
| 收件箱不实时更新 | 看顶部状态；离线会显示“已离线，重连中”，恢复前台会自动补拉。 |
| 局域网客户端发现不到 Hub | 先 `flowdrop doctor` 看 `mdns discovery`：回环/Tailscale/通配/HTTPS 地址不会发布；确认绑定的是具体 RFC1918 IPv4 且 `cookie_secure=false`。UDP 5353 被占用时日志会告警，启用系统 mDNS 或手动填 IP 均可继续使用。 |
| 停止 Hub 后客户端仍短暂显示服务 | mDNS 缓存现象；正常退出会发 TTL=0 注销，异常崩溃则最多等 120 秒 TTL 过期。 |

## 15. 验证清单（部署后）

主路径（局域网明文 HTTP）：

```bash
# 健康检查（无需认证）
curl -s http://<Hub-IP>:8080/api/v1/health

# 未认证应被拒绝
curl -s -o /dev/null -w '%{http_code}\n' http://<Hub-IP>:8080/api/v1/items   # 401

# 响应头（明文 HTTP 不应出现 HSTS）
curl -sI http://<Hub-IP>:8080/ | grep -iE 'content-security|x-frame|x-content-type'
```

浏览器侧：

- [ ] 登录后刷新仍保持会话（说明 `cookie_secure` 与协议一致）。
- [ ] 两台设备互发文字实时可见（WS 正常）。
- [ ] 上传图片/APK 可下载且 SHA-256 一致。
- [ ] 在非安全上下文下点“复制”：要么真实成功，要么展开可选文本框且**不**提示已复制。

LAN + Tailscale 双监听（可选，§5.8）：

```bash
curl -fsS http://<Hub-LAN-IP>:8080/api/v1/health
curl -fsS http://<Hub-Tailscale-IP>:8080/api/v1/health
```

- [ ] 两个地址都返回 200，且日志只有**一个** Hub 实例、一个数据目录。
- [ ] 在任一地址登录并新建条目，另一地址（重新登录后）能看到同一条目。
- [ ] 把一个地址改成当前不存在的 IP 或占用端口后重启：明确失败，不出现“一半可用”。
- [ ] 桌面版设置窗口只把实际绑定的 URL 列为可用，类型标注正确。
- [ ] `flowdrop doctor` 的 `mdns discovery` 行符合预期；客户端能发现 `_flowdrop._tcp`，且解析出的端口/IP 与实际监听一致。
- [ ] 用 `FLOWDROP_DISCOVERY_ENABLED=false` 重启后，独立 mDNS 浏览器/探针不再收到**新**响应（旧缓存需等 TTL）。

可选 HTTPS（Tailscale / Caddy）仍可沿用旧清单：

```bash
curl -sI https://<你的地址>/ | grep -iE 'strict-transport|content-security|x-frame'
```


---

## 16. Linux 桌面 App（Wails，2026-11-08 交付）

桌面版是 **Hub 的桌面外壳**，不是第二个实现：它在**同一进程**内 `import "flowdrop/pkg/hub"`
并调用 `hub.New/Start/Stop/URL/Handler/HasAdmin/InitializeAdmin`，不外部 shell 调用、
不捆绑第二个 `flowdrop` 进程、不复制 Hub 逻辑。Hub 的 HTTP API、Cookie 会话、
CSRF、Origin/Host 校验、WebSocket、鉴权下载、mDNS 发现全部沿用原实现。

### 16.1 产物与版本

| 项目 | 值 |
|---|---|
| 产物 | `dist/flowdrop-desktop`（约 16 MiB，单文件，内嵌前端与图标） |
| 框架 | `github.com/wailsapp/wails/v3` **v3.1.0**（当前稳定版；v3.0.0 已有稳定版但 v3.1.0 更新） |
| 构建标签 | `gtk3`（GTK3 + WebKitGTK 4.1）；release 另加 `production` |
| 桌面模块 | `desktop/`（独立 Go module，`require flowdrop v0.0.0` + `replace flowdrop => ../server`） |
| 桌面 Bridge | `desktop/internal/hubmgr`（生命周期/单地址与双地址切换）、`desktop/internal/network`（网卡枚举/代理提示）、`desktop/internal/appsettings`（绑定地址组 + 端口） |

> Wails v3 默认的 Linux 后端是 **GTK4 + WebKitGTK 6.0**。本机（Ubuntu 26.04）只安装了
> WebKitGTK **4.1** 运行时，因此固定使用 `-tags gtk3`。两者不能混用：GTK4 构建需要
> `webkitgtk-6.0` 开发包与运行时，本机没有。

### 16.2 构建

```bash
scripts/build-desktop.sh          # release（production 标签，无 DevTools）
scripts/build-desktop.sh --dev    # 调试构建（保留 DevTools）
```

依赖（pkg-config 名）：`gtk+-3.0`、`webkit2gtk-4.1`、`libsoup-3.0`、`gio-unix-2.0`、`gdk-3.0`。

```bash
# 系统安装（推荐，需要 root）
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev

# 无 root 的用户级 sysroot（仅解压到 ~/.local，不改动系统包）
scripts/desktop-dev-sysroot.sh
scripts/build-desktop.sh
```

`scripts/build-desktop.sh` 会自动检测 pkg-config：系统开发包可用则直接用；否则回退到
`${FLOWDROP_DESKTOP_SYSROOT:-~/.local/share/flowdrop-desktop/sysroot}`，并在两者都没有时
打印准确的安装命令后失败（不会伪造成功）。

### 16.3 安装与启动（用户级，无 sudo）

```bash
scripts/install-desktop.sh                  # 从 dist/flowdrop-desktop 安装
scripts/install-desktop.sh --binary PATH    # 安装指定构建
scripts/install-desktop.sh --uninstall      # 卸载启动器（保留数据）
```

安装位置：

| 项目 | 路径 |
|---|---|
| 二进制 | `~/.local/bin/flowdrop-desktop` |
| 桌面入口 | `~/.local/share/applications/flowdrop-desktop.desktop` |
| 图标 | `~/.local/share/icons/hicolor/{48,128,256}x…/apps/flowdrop-desktop.png` |

从应用菜单「FlowDrop 流转」双击，或运行 `~/.local/bin/flowdrop-desktop`。
脚本**不**创建开机自启/autostart，也**不会**自动启动。

### 16.4 运行时依赖

| 依赖 | 说明 |
|---|---|
| GTK 3 | `libgtk-3-0t64`（Debian/Ubuntu 24.04+ 名称）或 `libgtk-3-0` |
| WebKitGTK 4.1 | `libwebkit2gtk-4.1-0`、`libjavascriptcoregtk-4.1-0` |
| libsoup 3 | `libsoup-3.0-0` |
| 系统托盘 | 需要会话总线上的 `StatusNotifierWatcher`（KDE Plasma 自带；GNOME 需扩展） |
| mDNS | 由 Hub 用纯 Go 实现，不需要 Avahi |

`ldd dist/flowdrop-desktop` 应能解析全部依赖；缺库时启动即失败并打印原因。

### 16.5 两个窗口、两种 origin（安全模型）

| 窗口 | 加载的 origin | 能做什么 |
|---|---|---|
| 收件箱 | Hub 的真实 URL，如 `http://192.168.1.10:8080/` | 现有 React + `/api/v1/*`；Cookie/CSRF/Origin/WS/下载与浏览器一致；**不能**调用原生管理桥 |
| 设置 | Wails 资产服务器 `wails://localhost`（进程内，不监听 TCP 端口） | 绑定地址/端口、首次管理员密码、打开收件箱/浏览器、退出 |

关键点：Wails 的 JS 运行时按 `location.origin + "/wails/runtime"` 解析绑定调用，
因此 Hub 来源的页面请求的是 Hub 自身而不是资产服务器；实测从 Hub 页面 `import
"/wails/runtime.js"` 直接失败，`fetch('wails://localhost/wails/runtime')` 也无法完成
原生调用（见 VERIFICATION §13.4）。所以**不会**把管理 bridge 开放给局域网页面。

外链：收件箱窗口只接受一种窄消息 `flowdrop:openurl:<http(s) URL>`，且校验来源
origin 必须等于当前 Hub origin、只允许 http/https、限速 500ms，然后交给系统浏览器。
下载保持 WebKit 原生行为（保存到 `~/Downloads`）。

### 16.6 网络与配置

设置窗口提供两种方式：

1. **只使用一个地址（旧方式）**：内网、Tailscale、其他或回环任选一个具体 IPv4；
   行为与旧版一致。
2. **同时使用内网和 Tailscale**：分别选一个**内网（LAN）IPv4**和一个
   **Tailscale IPv4**，使用同一个端口。桌面把 LAN 作为主监听、Tailscale 作为
   `hub.Options.AdditionalListen`，由**同一个 Hub** 绑定；不是两个 Hub、不是两个进程，
   不共享两个数据目录。设置窗口还列出**实际绑定成功**的每个 URL 及其类型，
   未运行的已配置地址只作为输入显示，绝不冒充可用地址。

关键规则：

- 只允许绑定**具体 IPv4 + 端口**，拒绝 `0.0.0.0` / `::`（不监听 wildcard）。
- 双模式要求两个地址当前都存在于本机网卡上且类型正确（LAN + Tailscale）；
  没有 Tailscale 网卡时双模式不可选，窗口提示仍可使用内网单地址，
  不要求也不自动安装 Tailscale。
- 保存：`~/.config/flowdrop/desktop.json`，**不含密码**。
  单地址写旧的 `bind_address` + `port`；双模式额外写
  `additional_bind_addresses: ["100.x.y.z"]`。旧版文件（无该字段）原样兼容。
- Hub 配置：`desktop` 传 `hub.Options{ConfigPath: hub.DefaultConfigPath(),
  Listen: <lan:port>, AdditionalListen: [<tailscale:port>], CookieSecure: false}`。
  `AdditionalListen` 总是非 nil（单地址模式传空 slice），因此 CLI/config.json 里的
  `additional_listen` **不会**被桌面无意继承；`data_dir` 等仍按 config.json /
  `FLOWDROP_*` 环境变量。
- 切换/保存：`Stop` 旧 Hub → `New` + `Start` 同一 `DataDir`，全部监听同步绑定成功才算
  成功。端口被占用、某个地址当前不存在或任一监听绑定失败时，窗口显示明确错误，
  并尝试**恢复原配置**（单地址或双地址）继续服务；失败的选择不会写入 desktop.json。
  不会静默半启动，也不会自动改用其它地址。
- 收件箱窗口优先加载 LAN 地址（双模式下即主地址）；跨 origin 时沿用浏览器/WebView
  的现有 Cookie 规则，不承诺不同 host 自动共享登录态。
- 系统代理提示按收件箱实际使用的地址判断，仍然只提示、不修改代理设置。
- mDNS：只发布符合条件的 RFC1918 LAN 监听（`cookie_secure=false` 时）；
  Tailscale 监听不广播。退出（托盘「退出 FlowDrop」）会撤回记录并释放全部端口。

### 16.7 系统代理（重要）

如果主机配置了系统级 HTTP 代理（`http_proxy` 环境变量，或 KDE `kioslaverc` /
GNOME 代理设置），且代理绕过列表没有包含 Hub 的局域网 IP，WebView 的 WebSocket 会被
代理改写握手，收件箱会一直显示「已离线，重连中」（HTTP 请求仍可能正常）。

桌面版**不会**修改你的代理设置；设置窗口会检测并在这种情况下显示黄色提示，指出需要
加入「忽略主机 / `no_proxy`」的具体 IP。处理方法：

```bash
# 环境变量方式
export no_proxy="$no_proxy,192.168.1.10"   # 换成你的 Hub 绑定 IP
# KDE：系统设置 → 网络 → 代理 → 忽略主机，加入该 IP
```

### 16.8 数据位置与备份

- Hub 数据（数据库 + 附件）：`${XDG_DATA_HOME:-~/.local/share}/flowdrop/`
- 桌面偏好：`${XDG_CONFIG_HOME:-~/.config}/flowdrop/desktop.json`
- Hub 配置（可选，与 CLI 共用）：`~/.config/flowdrop/config.json`

备份方式与 CLI 相同（§10）：先退出桌面 App（托盘 →「退出 FlowDrop（停止 Hub）」），
或用 `flowdrop backup` 对同一数据目录做一致性快照。

### 16.9 已知限制（桌面版）

- 仅 Linux；不做 Windows/macOS 打包、开机自启、自动网络重绑定/自动选路、二维码。
- 明文 HTTP 局域网模式下 WebView 同样不是 secure context：自动复制可能被拒，
  Web 端已有「展开可选文本」降级（实测在 WebKitGTK 中 `navigator.clipboard` 可用，
  见 VERIFICATION §13.6）。
- 托盘依赖 `StatusNotifierWatcher`；不满足时关闭窗口会改为「确认退出」。
- 支持同一 Hub 同时绑定一个内网地址 + 一个 Tailscale 地址（同端口）；更复杂的多地址
  组合未做成产品选项。地址失效、端口冲突时仍需用户修改设置并重启 Hub（窗口会先
  Stop 再 Start，并在失败时尝试恢复原配置）；不会自动重绑定。
