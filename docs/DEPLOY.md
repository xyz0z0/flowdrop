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

期望输出（首次 `paired devices` 为 0 属正常，此时还没有设备配对）：

```
FlowDrop doctor 0.1.0
listen                 ok   127.0.0.1:8080
data_dir               ok   /home/bob/.local/share/flowdrop
database               ok   schema v2
pairing                ok   hub id <稳定ID>
paired devices         ok   0 paired (normal until a device pairs)
free space             ok   ...
port available         ok   can bind 127.0.0.1:8080
```

## 3. 配对第一台设备（没有账号密码）

FlowDrop 不再使用账号/密码，也没有 `admin init` 或登录接口。**配对码只在 Hub 本机产生**：

- **桌面 App（推荐）**：启动后打开设置窗口 →「添加设备」→ 生成四位码。
  码 120 秒有效、成功一次即失效；全 Hub 累计输错 5 次会锁定，在本机重新生成即解锁
  并废弃旧码。码只在这个窗口显示，不经网络传递、不写日志。
- **CLI**（Hub 已作为服务/桌面进程运行时同样可用，因为两者用同一 `data_dir`）：

```bash
flowdrop pair                 # 或：flowdrop pair -config /path/to/config.json
# Pairing code: 0427
# Valid for two minutes (until 14:03:12), single use.
```

  该命令只在本终端打印码；它不会启动第二个 Hub，也不会写日志文件。
  **只有 Hub 已运行，客户端才有可用入口**（否则拿到码也连不上）。

然后在手机 / 浏览器上输入这四位数完成配对：

- 浏览器：打开 `http://<Hub-IP>:8080`，在配对页输入码。成功后 Hub 下发
  `flowdrop_session`（HttpOnly）与 `flowdrop_csrf` Cookie，设备被记住。
- Android：见第 6 节；设备令牌保存在 Android Keystore。

设备管理：桌面设置窗口列出已配对设备的名称、创建时间与最近使用时间，可**在本机撤销**。
撤销会立刻让该设备的 HTTP 会话与 WebSocket 失效。没有远程设备管理端点，
也没有“通过 HTTP 生成配对码”的接口。

> 说明：配对码不是加密，它只是在可信局域网内的一次性凭据；`hub_id` 也不是明文 HTTP
> 的身份认证，只是防误连。可信 LAN 明文边界与 Tailscale 前置要求不变。
> 桌面偏好（`desktop.json`）不含密码、会话 Cookie 或 Hub 数据。唯一的凭据是本机免码
> 登录用的设备令牌（`desktop_device_token`，0600，只用于向本机 Hub 的
> `/api/v1/auth/session` 以 Bearer 换回该 origin 的会话 Cookie，见 §16.13）。
> 旧版本留下的 `admin_users`/`sessions` 表只作为历史数据保留，不参与认证。

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

日志中不会出现内容原文、配对码或设备令牌；如需调试可临时
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
- Android 原生 App：发现列表或手填 `http://192.168.31.184:8080`，输入**本机生成的
  四位配对码**。配对后设备令牌存 Android Keystore；请求/WS 仍用 Cookie + CSRF + Origin。
- 手机浏览器也可以直接用同一地址（能力受限见下）。

### 5.4 明文 HTTP 下的能力边界

| 能力 | LAN HTTP 下的实际情况 |
|---|---|
| 配对 / 收发文字、链接 | 正常 |
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

记录中**不包含**设备令牌、会话令牌、条目正文、文件路径或机器名以外的隐私信息。

**不会发布**的情形（即使 `discovery_enabled=true`）：

- 回环地址（`127.0.0.1` / `::1`）——没有 LAN 入口可广播。
- 通配地址（`0.0.0.0` / `::` / 空 host）——避免在所有网卡泄露。
- Tailscale `100.64.0.0/10`——跨网地址不冒充同一 LAN。
- 链路本地 `169.254.0.0/16`、公网地址、IPv6 地址。
- `cookie_secure=true`（Tailscale Serve / Caddy HTTPS 反代入口）——只发布可直接配对的明文 HTTP LAN 入口。

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
| 会话/Cookie | 浏览器按 origin 隔离 Cookie；不同地址之间**不承诺**自动共享登录态，Web 需各自配对。Android 单 Hub 客户端才按候选地址自动选路。数据本身共享 |
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
2. 输入本机生成的**四位配对码**（桌面设置「添加设备」或 `flowdrop pair`）。
   码 120 秒有效、一次成功、全 Hub 错 5 次锁定；本机重新生成即解锁。
3. 配对后 App 保存 Hub 稳定 ID、设备令牌（Keystore）与候选地址；写操作带
   `X-FlowDrop-CSRF`，`Origin` 由 App 当前活动地址决定，需落在 `allowed_hosts` 内。
4. **自动连接**：启动/回到前台/连接中断时先探测 LAN、再探测 Tailscale，
   只在无凭据 `health` 的 `hub_id` 匹配后才用 `Authorization: Bearer <device_token>`
   调用 `GET /api/v1/auth/session` 恢复 Cookie/CSRF，然后恢复事件流。切地址只重试连接，
   **不重放**上传/删除等写操作，中断的传输需手动重试。
5. **首次异地访问**需手填 Tailscale 地址（`100.x.y.z`），或使用已加入
   `FLOWDROP_ALLOWED_HOSTS` 的 MagicDNS 名。不会自动枚举 tailnet 设备，
   也没有扫码配对，不调用 Tailscale 管理 API，不扫描整个网段。

局域网自动发现协议已冻结（`_flowdrop._tcp`、`FlowDrop-<昵称>`、SRV 真实端口、
TXT `v=1,scheme=http,path=/`、A 仅绑定 LAN IPv4）。Android 客户端按同一协议实现；
本轮只验证 Hub 端发布，Android 发现 UI 与真机验收仍未完成。

诊断建议（仅在真机实测时使用）：

- `421 host_not_allowed` → App 里配置的 Host 不在 `FLOWDROP_ALLOWED_HOSTS`。
- 配对后立刻回登录页 → 把 `FLOWDROP_COOKIE_SECURE` 与访问协议保持一致（HTTP 用 `false`）。
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

- 手机/电脑在同一 tailnet 内访问 `http://100.125.168.63:8080`，输入本机生成的四位配对码。
  设备令牌/会话 Cookie 在同一 tailnet 内是明文，Tailscale 只保证链路两端与 ACL，不替代应用配对。
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
- 应用层仍强制设备配对/设备令牌：Tailscale ACL 不替代应用侧鉴权；
  同时配对码**不是加密**，`hub_id` 也不认证对端。

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
| `login_limit_per_minute` | `FLOWDROP_LOGIN_LIMIT_PER_MINUTE` | `5` | 每个来源 IP 的配对请求限制（固定窗口，非令牌桶；与全 Hub 5 次错误上限是两件事） |
| `api_limit_per_minute` | `FLOWDROP_API_LIMIT_PER_MINUTE` | `600` | 每会话 API 请求 |

> 明文 HTTP 局域网部署（主路径）需要 `FLOWDROP_COOKIE_SECURE=false`；
> 经 Tailscale/Caddy 的 HTTPS 部署应保持 `true`。两者必须与实际访问协议一致，
> 否则会出现“配对成功但立即回配对页”。

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

**不要把包含设备令牌哈希/会话的数据库放进未加密的云同步目录。**

## 11. 恢复

```bash
systemctl --user stop flowdrop
mv ~/.local/share/flowdrop ~/.local/share/flowdrop.old
tar -xzf ~/backups/flowdrop-YYYY-MM-DD.tar.gz -C ~/.local/share
chmod 700 ~/.local/share/flowdrop
systemctl --user start flowdrop
flowdrop doctor
```

恢复后旧设备配对仍然有效（`paired_devices` 随库恢复）。若不希望保留，
在恢复后的本机设置窗口逐台撤销设备，或删除数据目录重新配对（会丢弃历史条目）。

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
| 配对后立刻又回到登录页/配对页 | 用了 HTTPS 访问但 `cookie_secure=false`，或反之。让对方匹配实际协议（LAN HTTP 用 `false`）。 |
| `pairing_unavailable` | Hub 未正确装配配对存储；看 `flowdrop doctor` 的 `pairing` 行。 |
| `pairing_invalid`（401） | 码错误、已过期（>120 秒）、已被使用，或本机没有有效码。在**本机**重新生成。 |
| `pairing_locked`（429） | 全 Hub 累计输错 5 次已锁定；在本机重新生成配对码即解锁（旧码同时作废）。 |
| 拿到 CLI 码但手机连不上 | 确认 Hub 正在运行，且 `flowdrop pair` 与 Hub 使用同一 `data_dir`/`-config`。 |
| 设备被撤销后仍在收消息 | 已撤销设备的 HTTP 与 WS 会失效；客户端需重新配对，不能凭旧令牌恢复。 |
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

- [ ] 配对后刷新仍保持会话（说明 `cookie_secure` 与协议一致；Cookie 仍是 7 天 TTL）。
- [ ] `curl -s http://<Hub-IP>:8080/api/v1/health` 返回稳定的 `hub_id`。
- [ ] 在本机撤销一台设备后，该设备刷新即失效（401/回到配对页）。
- [ ] 两台设备互发文字实时可见（WS 正常）。
- [ ] 上传图片/APK 可下载且 SHA-256 一致。
- [ ] 在非安全上下文下点“复制”：要么真实成功，要么展开可选文本框且**不**提示已复制。

LAN + Tailscale 双监听（可选，§5.8）：

```bash
curl -fsS http://<Hub-LAN-IP>:8080/api/v1/health
curl -fsS http://<Hub-Tailscale-IP>:8080/api/v1/health
```

- [ ] 两个地址都返回 200，且日志只有**一个** Hub 实例、一个数据目录。
- [ ] 在任一地址配对并新建条目，另一地址（重新配对后）能看到同一条目；Web 不自动跨 origin 沿用 Cookie，Android 才自动选路。
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

**升级到包含主窗口设置入口/应用内代理直连的版本：**

1. 先**完全退出正在运行的 FlowDrop**：托盘菜单 →「退出 FlowDrop（停止 Hub）」。
   仅关闭窗口只会收进托盘，进程还在；桌面版的 SingleInstance 会复用旧进程，
   直接启动新二进制可能仍看到旧窗口。
2. 重新构建（或使用已交付的 `dist/flowdrop-desktop`）：
   ```bash
   scripts/build-desktop.sh
   ```
3. 安装新二进制：
   ```bash
   scripts/install-desktop.sh
   ```
4. 再从应用菜单或 `~/.local/bin/flowdrop-desktop` 启动。

脚本只复制文件，不会替你结束正在运行的实例；确认旧进程已退出后再启动即可。

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
| 设置 | Wails 资产服务器 `wails://localhost`（进程内，不监听 TCP 端口） | 绑定地址/端口、**添加设备（生成四位配对码）/设备列表/撤销**、打开收件箱/浏览器、退出 |

关键点：Wails 的 JS 运行时按 `location.origin + "/wails/runtime"` 解析绑定调用，
因此 Hub 来源的页面请求的是 Hub 自身而不是资产服务器；实测从 Hub 页面 `import
"/wails/runtime.js"` 直接失败，`fetch('wails://localhost/wails/runtime')` 也无法完成
原生调用（见 VERIFICATION §13.4）。所以**不会**把管理 bridge 开放给局域网页面。

外链：收件箱窗口只接受窄消息 `flowdrop:openurl:<http(s) URL>`，且校验来源
origin 必须等于当前 Hub origin、只允许 http/https、限速 500ms，然后交给系统浏览器。
下载保持 WebKit 原生行为（保存到 `~/Downloads`）。

设置入口：同一个收件箱窗口还接受一条固定的 `flowdrop:opensettings` 消息（无参数、
无 URL、无方法名），同样要求消息来自本实例的收件箱窗口且 origin 仍等于当前 Hub
origin；其它窗口、其它 origin、未知动作一律忽略。收件箱登录前后都会注入「设置」
按钮（登录页右上角；登录后位于顶部栏「退出」旁），窗口原生菜单有「设置…」，
快捷键 `Ctrl+,`。这些入口只打开并聚焦进程内的 `wails://` 设置窗口，**不会**把
管理 bridge 暴露给 Hub 页面。脚本只由桌面进程通过 `WebviewWindowOptions.JS` 注入，
普通浏览器和手机加载的 React 页面没有该按钮。

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
- 保存：`~/.config/flowdrop/desktop.json`；除绑定参数外只保存本机免码登录的设备令牌
  （`desktop_device_token`，0600，见 §16.13），Hub 侧只存该令牌的哈希。
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
- 设置入口不依赖托盘：登录前后主窗口都有「设置」按钮，原生菜单与 `Ctrl+,` 也可
  打开同一个设置窗口；托盘菜单的「设置…」继续保留。
- 应用内代理：桌面在 `application.New` 之后、创建窗口之前把本进程 WebKitGTK 的
  网络代理设为直连（`WEBKIT_NETWORK_PROXY_MODE_NO_PROXY`），不修改系统代理、
  KDE `kioslaverc`、GSettings 或 `http_proxy`/`no_proxy` 环境变量。设置窗口的代理
  提示按实际配置结果处理：应用内直连成功则不再要求修改 `no_proxy`；失败才显示
  风险诊断。
- mDNS：只发布符合条件的 RFC1918 LAN 监听（`cookie_secure=false` 时）；
  Tailscale 监听不广播。退出（托盘「退出 FlowDrop」）会撤回记录并释放全部端口。

### 16.7 系统代理（重要）

如果主机配置了系统级 HTTP 代理（`http_proxy` 环境变量，或 KDE `kioslaverc` /
GNOME 代理设置），桌面版**不需要**你先改代理，也不会修改你的代理设置：

- 桌面在创建窗口前把本进程的 WebKitGTK 网络栈设为**直连**
  （`WEBKIT_NETWORK_PROXY_MODE_NO_PROXY`）。只有收件箱/设置窗口的 WebView 受影响；
  系统浏览器打开外链时仍使用你原来的系统代理。
- 直连同时覆盖 HTTP 和 WebSocket（`ws://`/`wss://`），所以切换 LAN/Tailscale 地址
  不需要重启应用或改 `no_proxy`。
- 设置窗口仅在**应用内直连配置失败**且检测到代理绕过列表未包含 Hub 地址时显示黄色
  风险提示。提示是风险说明，不是断言：代理允许 WebSocket 升级时不会离线；只有代理
  拒绝/改写升级（例如返回 HTTP 426）时才可能一直显示「已离线，重连中」。
- 如果确实遇到 WebSocket 握手失败，仍可把 Hub 的绑定 IP 加入系统代理的
  「忽略主机 / `no_proxy`」后重启 FlowDrop：

```bash
# 环境变量方式（仅作为应用内直连不可用时的后备方案）
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

- 桌面版覆盖 Linux 与 Windows（Windows 构建/打包/限制见 §18；两端共用同一套桌面代码，
  功能一致，差异只在拖拽、托盘、代理直连等平台适配层）；macOS 未支持。
  不做开机自启、自动网络重绑定/自动选路、二维码。
- 明文 HTTP 局域网模式下 WebView 同样不是 secure context：自动复制可能被拒，
  Web 端已有「展开可选文本」降级（实测在 WebKitGTK 中 `navigator.clipboard` 可用，
  见 VERIFICATION §13.6）。
- 托盘依赖 `StatusNotifierWatcher`；不满足时关闭窗口会改为「确认退出」。
  无托盘不再影响进入设置：主窗口的「设置」按钮、原生菜单「设置…」和 `Ctrl+,`
  都可打开设置窗口。
- 应用内 WebKit 直连只在 Linux + cgo + GTK3/WebKitGTK 4.1 构建中设置成功；其它构建
  会明确报告未应用，并保留代理风险提示，不会假装已修复。
- 收件箱整窗拖拽上传只覆盖登录后的 inbox 窗口；目录不递归。GTK 原生拖拽已在独立
  Docker 的 Wayland/X11 核心验收，用户随后确认真实 KDE/Dolphin 拖拽可用（见 §16.11）。
- 支持同一 Hub 同时绑定一个内网地址 + 一个 Tailscale 地址（同端口）；更复杂的多地址
  组合未做成产品选项。地址失效、端口冲突时仍需用户修改设置并重启 Hub（窗口会先
  Stop 再 Start，并在失败时尝试恢复原配置）；不会自动重绑定。

### 16.10 启动即崩溃：AT-SPI 无障碍总线损坏（2026-11-08 定位）

症状：启动约 1 秒内原生 `SIGSEGV`，`PC` 落在系统
`libatk-bridge-2.0.so.0` 的 `spi_register_object_to_path+0x28`
（`addr=0x18`、`rax=0`），前面通常有
`Couldn't register with accessibility bus: Did not receive a reply`。
这不是 FlowDrop 的托盘/菜单/代理代码问题：同样的 C 探针（不包含任何 FlowDrop
代码）在故障会话里 3/3 复现，换成隔离的新会话总线 3/3 正常。

桌面版从 2026-11-08 起在 `main()` 最前面做一次无障碍总线健康检查（真实 D-Bus
往返，上限约 2 秒），只在探测失败时对**本进程**设置 `NO_AT_BRIDGE=1`：

- 正常会话：不改变任何设置，无障碍桥照常加载；启动只多几毫秒。
- 故障会话：跳过损坏的 `libatk-bridge` 初始化，避免 WebKitGTK 创建首个 WebView
  时触发空指针崩溃；设置窗口显示黄色说明并提示恢复方法。屏幕阅读器本次读不到
  FlowDrop，但其它应用和系统无障碍设置不受影响。
- 日志关键字：`accessibility bridge: AT-SPI bus responsive` 或
  `accessibility bridge disabled for this launch: <原因>`。

显式开关（仅影响 FlowDrop 进程）：

```bash
FLOWDROP_DESKTOP_A11Y=off ~/.local/bin/flowdrop-desktop   # 强制关闭无障碍桥
FLOWDROP_DESKTOP_A11Y=on  ~/.local/bin/flowdrop-desktop   # 强制保留（有崩溃风险）
NO_AT_BRIDGE=1            ~/.local/bin/flowdrop-desktop   # 临时兼容启动，等同关闭
```

恢复无障碍（不需要改 FlowDrop 配置）：**注销并重新登录** KDE 会话，让会话的
`at-spi-bus-launcher`/`at-spi2-registryd` 重新启动；之后正常启动会自动探测到
健康总线并恢复无障碍。确认当前状态可以看：

```bash
ls -l /run/user/$UID/at-spi/        # bus_0 不存在 = 总线已损坏
gdbus call --session --dest org.a11y.Bus --object-path /org/a11y/bus \
  --method org.a11y.Bus.GetAddress
```

如果 `GetAddress` 返回的路径连不上（或重复的 `at-spi-bus-launcher` 很多），就是
本问题。不要手动 `kill` 这些系统无障碍进程来“修复”；注销重登最稳妥，也避免影响
屏幕阅读器。真正的修复在系统侧（at-spi2-core 2.60.4 的 `spi_register_object_to_path`
缺少 `spi_global_app_data` 空指针保护），FlowDrop 只能做上述进程级兜底。

### 16.11 收件箱整窗拖拽上传（2026-11-08）

适用于 Linux 桌面 App（GTK3/WebKitGTK 4.1 构建）**登录后**的收件箱窗口。

- 把一张或多张图片/普通文件拖到内容窗口**任意位置**，松开鼠标即上传；进度、取消、
  大小上限（默认 512 MiB）全部沿用既有上传流程，没有新的上传通道。
- 同时拖入多个文件时逐个顺序上传，中文名/空格名原样保留。
- **目录会被跳过**并在页面提示，不递归展开；目录与普通文件混合拖入时只上传文件。
- 只在登录后的收件箱窗口生效：登录页拖入文件不会上传，也不会把文件当页面打开。
- 不需要运行任何 shell 脚本，也不需要额外启动 Hub：直接运行 `dist/flowdrop-desktop`
  就是完整应用；`scripts/build-desktop.sh` / `scripts/install-desktop.sh` 只是构建
  与安装工具，不会自动安装或替换你已有的二进制。
- 升级旧实例：先从托盘菜单「退出 FlowDrop（停止 Hub）」完全退出旧进程，再手动运行
  新二进制（或安装后的 `~/.local/bin/flowdrop-desktop`）。只关窗口会留在托盘，
  SingleInstance 会复用旧进程。

最新产物修复 Wayland 原生文件拖放 COPY 协商和 leave 后异步数据授权，已在独立
Docker 中通过 GTK Wayland 源真实拖放、同文件重复上传及下载 SHA 校验，并通过本轮
X11 核心回归。无测试 shim 改变产品行为；用户随后确认真实 KDE/Dolphin 拖拽可用。
此前 X11 多文件、目录混合、普通文件选择等扩展验收见 VERIFICATION §18，未在本轮重跑。

大文件进度/取消仍**未验证**。原生 FileList 桥复用页面原上传流程，实现依赖 Wails
v3.1.0 的内部 handler 约定，升级 Wails 后须重验。最新产物摘要和本轮证据见
`docs/VERIFICATION.md` §19；§17–18 保留此前构建、失败复验和扩展验证历史。


### 16.12 下载与同名文件（2026-11-09）

- 桌面壳现在接管 WebKitGTK 的 `decide-destination`：每次下载在下载目录
  （`G_USER_DIRECTORY_DOWNLOAD`，取不到时退回 `~/Downloads`，再退回 `$HOME`）里选一个
  **当前空闲**的文件名，重名自动变成 `name (1).png`、`name (2).png`……**从不覆盖、也从不
  删除**已有文件。
- 修复前的行为：Wails v3.1.0 的 Linux 后端没有接下载，WebKit 按默认规则存到下载目录 +
  服务器给的文件名，而 `allow-overwrite` 默认是 FALSE；目标已存在时下载失败并会破坏
  旧文件，表现就是“同一张图下载过一次后，第一次点击把旧图删了，第二次才成功”。
- 文件名按不可信输入处理：只取最后一个路径分量，去掉 `..`、控制字符、首尾空白与开头
  的点，超长时截断但保留扩展名；建议名为空时落成 `download`。
- Windows 走 WebView2 自带的下载流程，不受这段逻辑影响；非 GTK3 构建只在日志里写一行
  “download handler requires a linux + cgo + gtk3 build”，不改变行为。

### 16.13 无边框窗口与自绘标题栏（2026-11-09）

- 收件箱与设置两个窗口都用 `Frameless: true`：没有系统标题栏，也没有窗口内菜单栏
  （原 `LinuxWindow.Menu` 已移除，`设置…` 由页面自己的「设置」按钮承担，`在浏览器打开`
  与 `退出` 在托盘菜单里，`Ctrl+,` 仍走全局快捷键）。
- 顶部由页面自绘 36px 标题条：左侧品牌标记，右侧最小化/最大化/关闭。标题条颜色直接取
  页面自己的 token（`var(--bg)` / `var(--fg)` / `var(--line-2)`），与下方页面同色，浅色/
  深色都自动跟随；设置窗同一规则（`desktop/assets/style.css`）。
- 拖动：Wails 的 `--wails-draggable` 路径在 GTK3 上拿不到按下时的按键与时间戳（发给合成器
  的请求 button=0，被静默忽略），所以拖动一度只在按钮点击上"看起来正常"。现在由
  `desktop/window_drag_linux_gtk3.go` 在 WebView 自己的 `button-press-event` 上直接调用
  `gtk_window_begin_move_drag`：按下点位于顶部 36px 且不在右侧 132px 按钮区内才起拖，
  参数就是这次按下的按键/坐标/时间戳（Wayland 下合成器需要的 serial 也来自这次按下）。
  其它平台为空操作（Windows 用 Wails 自带路径即可拖动）。启动日志里两个窗口各出现一次
  `window drag: title bar starts a window move on press` 表示已装上。
- 三个按钮只发送固定 raw message（`flowdrop:window:minimise|maximise|close`）：
  收件箱窗口仍要求来源是当前 Hub origin，设置窗（`wails://localhost`，仅进程内）无条件
  允许；页面无法借此调用任何其它原生能力。
- 收件箱的关闭按钮语义不变：仍有托盘时收进托盘，无托盘时弹出确认退出；设置窗关闭即隐藏。
- 代价：无边框窗口没有系统阴影/圆角，拖动与缩放的手感由 Wails 与窗口管理器协商。若某个
  桌面环境下拖动或缩放异常，把两个窗口的 `Frameless` 改回 `false` 即恢复系统装饰（其余
  代码无需改动），页面里的自绘标题条会随注入脚本一并消失。
- 标题条高度以 CSS 变量 `--flowdrop-chrome-top`（注入的 `:root` 规则）为准，Web UI 的登录/
  配对页用 `min-height: calc(100dvh - var(--flowdrop-chrome-top, 0px))`（`web/src/styles.css`），
  否则「36px 内边距 + 100dvh」会把配对页顶出一个无意义的竖向滚动条；浏览器/手机端该变量
  不存在，回退为 0px，行为不变。
- **KDE Plasma Wayland 专用处理**：KWin 会对"自己没有装饰"的窗口按服务端装饰协议补一条
  标题栏（带最小化/最大化/关闭），叠在无边框窗口上。`desktop/window_frame_linux_gtk3.go`
  在窗口 realize 时调用 `gdk_wayland_window_announce_csd()` 告诉 KWin"这个窗口自己画边框"
  （与 magpie 处理 #1283 的方式相同，见其 `internal/gui/titlebar_gtk3.go`），X11/Windows
  与不支持该协议的合成器上为空操作。从终端启动时，日志里出现
  `window frame: announced client-side frame to the Wayland compositor` 即表示声明已发出。

### 16.14 本机窗口免码登录与设置窗对话框（2026-11-09）

**免码登录**（`desktop/desktop_login.go`、`main.go` 的 `ensureDesktopLogin`）：桌面收件箱
窗口加载 Hub 真实 HTTP origin，与手机同一套 cookie 认证，会话 TTL 默认 7 天。桌面壳因此把
自己当成一个 `native` 客户端：

1. 本地模式下启动时用进程内 `CreatePairingCode()` 取一个一次性码（不显示、不写盘、不写日志）。
2. 壳自己的 Go 客户端 `POST /api/v1/auth/pair {native:true}`（不带 `Origin`，走服务端对非
   浏览器客户端的既有放行分支）换回长期设备令牌。
3. 令牌写进 `desktop.json`（`desktop_device_token`，0600 原子写）——这是这份配置文件里唯一
   的凭据；`appsettings.Normalize` 只接受 Hub 真实格式（base64url 32 字节），脏值视为未配对。
4. 收件箱窗口每次加载注入 `desktopBootstrapJS`：先用 cookie 查 `GET /api/v1/auth/session`，
   未认证才带 `Authorization: Bearer <token>` 再查一次（服务端会重发 cookie），成功重载一次；
   `sessionStorage` 键 `flowdropBootstrapReloads` 限制最多 2 次重载，避免 cookie 收不下时死循环。
5. 令牌只经由注入脚本进入页面，不进日志、不进 localStorage、不进 HTML 资产。

配对失败/被撤销时只记一行 `desktop login: 无法与本地 Hub 免码配对…`，收件箱仍可用配对码手动
登录，启动不阻断。设置页的「已配对设备」里，本机窗口被过滤出来单列成不可撤销的一行
「本机窗口：本机桌面 · <host>」；撤销它之后下次启动会重新配对。

**设置窗是收件箱的 transient 对话框**（`desktop/window_owner_linux_gtk3.go`）：
`gtk_window_set_transient_for(inbox)` + `GTK_WIN_POS_CENTER_ON_PARENT`（映射后补算坐标
`gtk_window_move`），点「设置」时再用 `gtk_window_present_with_time` 提到前台。窗口管理器
因此把它归为收件箱的对话框（跟随最小化、置于其上、通常不进任务栏）。页脚里与托盘重复的
「打开收件箱」已删。

**两个必须知道的坑（都在本轮容器实跑里踩到并修掉）**：

- *注入脚本不是互相独立的*。Wails 把 `WebviewWindowOptions.JS` 整段当**一个脚本**执行，注入
  可能落在 Hub 页面就绪之前的文档上：标题条因为显式等待 `document.body` 能幸存，其后的语句一旦
  顶层抛错就**整段后续都不执行**（表现为：标题条正常、但「设置」按钮和免码登录都没了，页面停在
  配对码页）。现在 `joinInjectedScripts` 把每段包进 `try { … } catch (e) { /* flowdrop: isolated
  snippet */ }`，并把登录引导排在第一位（`inboxInjectedJS`）；`desktop/injected_scripts_test.go`
  锁住这个结构。
- *窗口创建时的原生句柄还没有*。Wails 在 `app.Run` 的 GTK 主循环里才真正建窗口，所以
  `NativeWindow()` 首次可能为 nil，UI-05 的 CSD 声明与设置窗 transient 会静默失败。现在统一走
  `desktop/native_window_retry.go` 的 `withNativeWindows`：GTK 线程上 80ms 间隔、最多 25 次
  （约 2 秒）等待句柄，超时只记一行日志并退回默认行为。

**Windows 上必须自己注入**：Wails v3.1.0 只在窗口用 `options.HTML` 创建时才执行
`WebviewWindowOptions.JS`（`application/webview_window_windows.go` 的 `navigateInitialPage`），
两个 FlowDrop 窗口都是 URL 创建，所以 Windows 上原本拿不到任何注入脚本（无自绘标题条、无
「设置」入口、无拖放桥、无免码登录；设置窗自带的标题栏按钮也没绑事件）。现在由
`desktop/injected_js_windows.go` 在每次 `events.Windows.WebViewNavigationCompleted` 后重新注入
同一批脚本（`go w.ExecJS(js)`：`ExecJS` 内部用 `InvokeSync`，不能在共享事件分发 goroutine 上
直接调用）；其它平台 `desktop/injected_js_other.go` 是空操作，Linux/macOS 仍由 `JS:` 选项注入，
不会重复执行。真实 Windows 11 上的效果尚未验证（只验证了交叉编译与 Linux 无回归）。

**验收（隔离容器，`--network none`，Xvfb + openbox，X11）**：首启即进收件箱（Hub 日志
`/api/v1/items status=200`、`desktop login` 一行、desktop.json `-rw-------` 含两个新字段、
日志中 0 次令牌明文）；重启同一 `HOME` 时 `desktop login` 0 次、`items` 仍 200、`paired_devices`
只有一行且 id 与 desktop.json 一致；点注入的「设置」后 `xprop WM_TRANSIENT_FOR` 等于收件箱
窗口号、两窗口中心点完全重合；启动日志不再出现 `window frame: native GTK window not available`。
KWin Wayland 真机上的 transient 任务栏归属与 CSD 效果、Windows、托盘交互仍未在本轮验证。

## 17. 当前配对版交付（2026-11-08）

Linux桌面`dist/flowdrop-desktop`、CLI `dist/flowdrop`与Android测试包`dist/flowdrop-android-debug.apk`均已重新构建。当前操作不再使用账号密码：完全退出旧桌面实例→运行新版→需要时启用LAN/Tailscale双监听→设置添加设备取码→手机发现/手填地址并配对。每添加一台设备重新生成码，已配对Android会在同一Hub已保存地址间自动重连；Web Cookie仍按origin隔离。三产物SHA及本轮102 JVM、16 Web E2E、隔离Wails与真实Hub回环集成证据见VERIFICATION §20；前文引用旧APK摘要/模拟器/§19拖拽记录均为历史，不能替代本轮真机验收。未自动安装/commit/push，当前debug APK不是商店正式release。真实手机Wi-Fi/Tailscale仍待用户验证。

## 18. 桌面端运行模式与 Windows 构建（2026-11-09）

桌面壳现在有两种运行模式，保存在桌面自己的设置文件里，**在设置页切换后立即生效，不需要重启**（RMT-03）：切到远程会先停本机 Hub 再让收件箱加载远程地址；切回本机会重新启动 Hub，启动失败则保持远程模式并报中文错误，不会写坏已保存配置。切换瞬间会中断本机 Hub 上正在进行的传输/连接。

| 模式 | 行为 |
|---|---|
| 本机 Hub（默认，旧文件无 `mode` 字段即视为本机） | 进程内启动 Hub，窗口加载本机 `http://<bind>:<port>/`，可用托盘生成配对码 |
| 连接远程 Hub（`"mode":"remote"`） | 不启动本机 Hub（不绑端口、不跑 mDNS、不建数据目录），收件箱窗口直接加载 `remote_url`，配对走该 Hub 页面自带的配对页 |

设置路径：Linux `~/.config/flowdrop/desktop.json`，Windows `%AppData%\flowdrop\desktop.json`；远程地址必须是 `http(s)://host[:port]`，不能带路径、参数或账号。

远程模式下本机专属操作（生成配对码、设备列表、撤销设备、改绑定地址/端口）会返回中文提示而不会执行；托盘「在浏览器中打开」指向远程地址；退出只关窗口，不会影响远程 Hub。

### 18.1 Windows 构建与打包（交叉编译，无需 Windows 工具链）

```bash
scripts/build-desktop-windows.sh            # dist/flowdrop-desktop.exe（amd64，GUI 子系统）
scripts/build-desktop-windows.sh --arm64    # dist/flowdrop-desktop-arm64.exe
scripts/build-desktop-windows.sh --console  # 保留控制台，便于看 stderr 日志
scripts/package-windows.sh                  # dist/flowdrop-windows-amd64.zip（exe + README.txt）
scripts/package-windows.sh --arm64          # dist/flowdrop-windows-arm64.zip
```

Windows 侧文件位置：配置 `%AppData%\flowdrop\`（Hub 配置与桌面设置），数据 `%LocalAppData%\flowdrop\`（数据库、附件），日志 `%LocalAppData%\flowdrop\desktop.log`（GUI 构建没有控制台，桌面与 Hub 的 `log`/`slog` 都写这里）。

exe 资源（WIN-07）：构建脚本在链接后调用独立模块 `tools/winres/`（`github.com/tc-hib/winres`，纯 Go，无需 mingw）写入图标（`desktop/assets/icon.png` 的 256/128/64/48/32/16 六种尺寸）、版本信息（FileVersion/ProductVersion `0.1.0.0`、ProductName `FlowDrop`、FileDescription `FlowDrop 流转`，en-US + zh-CN）和 manifest（`asInvoker`、PerMonitorV2 + PerMonitor 高 DPI、longPathAware、Win10/11 supportedOS）。写入是原子的；失败会让构建失败，不会产出无图标 exe。`-verify` 可回读校验：

```bash
cd tools/winres && go run . -verify -exe ../../dist/flowdrop-desktop.exe
```

首次构建需要该模块的依赖在 module cache 中（`cd tools/winres && go mod download`，需网络）；之后离线可构建。

Windows 真机上可运行 `scripts/check-windows-exe.ps1` 核对 exe 写入了什么（只读，不启动应用）：

```powershell
powershell -ExecutionPolicy Bypass -File .\check-windows-exe.ps1 .\flowdrop-desktop.exe
```

它打印 Windows 属性对话框会显示的版本字段与 SHA-256，并把内嵌图标导出为 `exe-icon.png`（应与 `desktop/assets/icon.png` 同一蓝色圆角图标）。期望值：FileVersion/ProductVersion `0.1.0.0`、ProductName `FlowDrop`、FileDescription `FlowDrop 流转`。

拖拽上传：Windows 走 WebView2 原生 HTML5 拖放（Web 层 `web/src/Composer.tsx`/`fileDrop.ts`，用户 2026-11-09 在 Windows 11 真机确认可用），不需要原生桥；Linux WebKitGTK 的 HTML5 drop 不可用，才由 `desktop/filedrop_linux_gtk3.go` 原生 GTK 桥接管。

已知限制：未做代码签名（SmartScreen 会提示）、无开机自启；WebView2 运行库需系统自带（Windows 11 自带）。首次启动需在 Windows 防火墙提示中允许「专用网络」，否则同一局域网设备无法访问本机 Hub。图标与 DPI manifest 已写入 exe，但资源管理器图标缓存、属性对话框和真实高 DPI 观感仍需 Windows 真机确认。
