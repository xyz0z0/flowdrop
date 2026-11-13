# FlowDrop 实施计划与验收记录

后续每个 agent 任务必须先登记到 [TASKS.md](TASKS.md)，按
[AGENT_WORKFLOW.md](AGENT_WORKFLOW.md) 拆分、设置检查点并验收。
本文件记录产品阶段；任务派发边界和运行状态以任务记录为准。

范围：PRD P0 的 M0–M3，加上 2026-11-07 第二轮范围调整（HTTP LAN 主路径 + Android 原生）。
P1/P2（自动过期、设备管理等）不实现。

> **第二轮范围调整（2026-11-07）**：主路径改为**家庭可信 LAN 明文 HTTP**
> （Hub + Android 原生 App + 电脑 Web），不需要 Tailscale/Caddy/证书；跨网用用户现有
> Tailscale。原 HTTPS/Tailscale Serve/Caddy 部署保留为可选。详见 PRD §16 与 DEPLOY §5–§8。

## M0 项目骨架

- Go module 与目录、配置加载（JSON + `FLOWDROP_*` 环境变量）、SQLite 打开与 migration、健康检查、嵌入式前端占位、`scripts/dev.sh`、README。
- 验收：`go build ./... && go test ./...`；`./scripts/dev.sh` 后 `curl /api/v1/health` 返回 200；页面可打开。

## M1 文字/链接闭环

- `flowdrop admin init` 本机初始化密码（Argon2id）；登录/注销/会话查询；Cookie 会话（DB 存哈希）；Host/Origin/CSRF/限流中间件。
- 文字与链接创建、倒序稳定分页、删除；WS 广播；前端登录页 + 收件箱 + 同样内容实时可见。
- 验收：两个独立浏览器会话 A 发送 B 实时可见；未登录 401；伪造 Origin / 缺失 CSRF 被拒。

## M2 图片/文件闭环

- 流式 multipart 上传、进度条、原子落盘、SHA-256、大小与磁盘预检、并发上限；图片预览；鉴权下载；`Content-Disposition` 与主动内容防注入；删除清理附件；janitor（临时文件/孤立附件/session）。
- 验收：上传图片可预览、APK 可下载且 SHA-256 一致；超限被拒且不产生条目；删除后文件消失。

## M3 部署与稳定性

- systemd 用户服务、正式构建（前端嵌入二进制）、Tailscale Serve 步骤、局域网 HTTPS（Caddy internal）步骤、备份/恢复/升级/卸载、doctor 自检、端到端脚本。
- 验收：二进制单文件启动；重启后内容仍在；WS 断线重连后列表与 REST 一致；Go 测试与 Playwright E2E 通过。

## M4 HTTP LAN 主路径 + 复制降级（本轮）

- 主路径文档与配置：`FLOWDROP_LISTEN=<Hub-局域网IP>:8080` + `FLOWDROP_COOKIE_SECURE=false`；
  监听 IP 自动加入 Host 白名单，`FLOWDROP_ALLOWED_HOSTS` 仅用于补充 mDNS/Tailscale 名。
  不新增配置键，不新增认证端点。
  （实测：`cookie_secure=false` 时通配监听 `0.0.0.0` / `::` / 空 host 会被服务端拒绝。）
- Web 复制降级：`clipboard.ts` 统一处理 `navigator.clipboard` / `execCommand` /
  手动选择路径；正文、链接、SHA-256 均覆盖；失败不抛异常、不谎报成功。
- 测试：新增 Playwright 用例模拟“无 Clipboard API + execCommand 失败”、
  “权限被拒”和“真实非安全 LAN 源”，并保留成功路径用例。
- 验收：`tsc` 与 `vite build` 通过；上述 E2E 通过；真实设备/HTTP 实测仍待办（见下）。

## M5 局域网自动发现 + Tailscale 绑定说明（本轮）

- Hub 端新增 mDNS/DNS-SD 发布（冻结协议）：`_flowdrop._tcp` / `local.`，
  实例 `FlowDrop-<主机昵称>`（安全截断、冲突加后缀），SRV 真实端口，
  A 仅绑定的具体 RFC1918 IPv4，TXT `v=1,scheme=http,path=/`。
- 默认在符合条件（具体 RFC1918 IPv4 + `cookie_secure=false` + Up/Multicast 网卡）时启用；
  `discovery_enabled` / `FLOWDROP_DISCOVERY_ENABLED` 可显式关闭。回环、通配、
  Tailscale `100.64/10`、链路本地、公网、HTTPS 反代入口不发布。
- 不新增智能切换/共享数据库实例。**历史说明**：当时保留的“单 IP 监听”限制已在
  M7 解除（同一 Hub 可 LAN + Tailscale 双监听）；本节其余发现结论不变。
  Tailscale 绑定说明补入 README 与 DEPLOY §7.1。
- 实现：发布器按 `ipv4.ControlMessage.IfIndex` 严格过滤，只应答绑定网卡；
  浏览/冲突扫描使用纯 Go `zeroconf`，不依赖 Avahi。
- 测试：发现配置/地址范围/昵称/冲突/生命周期单测；`go vet` + `go test -race` 回归；
  真实网络用独立 Avahi 浏览器与原始 DNS 探针发现本机发布，并验证关闭与 Tailscale 跳过。
  `dist/flowdrop` 未更新（本轮验证用 /tmp 下的临时构建，避免触碰嵌入前端产物）。

## M6 Linux 桌面 App（Wails v3，本轮）

- 目标：双击图标启动**同一 Go Hub**并打开现有 React 收件箱；图形配置 LAN/Tailscale
  绑定地址与端口、查看手机可访问 URL 与运行状态；首次管理员密码在本机图形界面设置；
  关闭窗口保持 Hub 运行（托盘可恢复），托盘菜单提供**明确退出**（停止 Hub、撤回 mDNS、
  释放端口）。
- 架构：新建独立 module `desktop/`，`require flowdrop` + `replace ../server`，
  Wails v3.1.0 依赖**不进** `server/go.mod`；直接 `import "flowdrop/pkg/hub"`，
  同进程、不外部 shell 调用、不复制 Hub 逻辑。
- 关键约束：收件箱窗口加载 Hub 真实 HTTP origin，保证 Cookie/CSRF/Origin/WS/鉴权下载
  与浏览器一致；原生管理桥只对进程内 `wails://` 设置窗口开放；不放宽 Hub 任何安全校验；
  不监听 wildcard；无托盘时关闭窗口走「确认退出」。
- 交付：`dist/flowdrop-desktop`、`scripts/build-desktop.sh`、
  `scripts/install-desktop.sh`、`scripts/desktop-dev-sysroot.sh`、
  `deploy/desktop/`（图标 + `.desktop` 模板）、`desktop/internal/*` 单元与集成测试。
- 验收：桌面 Go 测试（含真实 Hub 的 HTTP/WS/上传/下载/地址切换/首次密码）通过；
  真实 Wails GUI 在 KDE Plasma 6 (Wayland) 与本机 Xvfb 上验证窗口、托盘、关闭→托盘、
  托盘恢复、退出停 Hub、设置地址切换、首次密码、文件上传/下载、剪贴板、外链。
  详见 `docs/VERIFICATION.md §13`。

## M7 Linux 桌面 LAN + Tailscale 同时使用（本轮）

- 目标：解除旧版“桌面一个 Hub 只能选一个地址”的产品限制，让同一个桌面 Hub 同时绑定
  **一个内网 IPv4 + 一个 Tailscale IPv4（同一端口）**；单地址旧方式继续保留。
- Hub 侧（并行 worker，冻结接口）：`hub.Options.AdditionalListen []string`（nil 继承，
  非 nil 覆盖含空 slice）、`Hub.URLs() []string`（真实监听，主地址在前）、`URL()` 主地址；
  `config.additional_listen` / `FLOWDROP_ADDITIONAL_LISTEN`；全部监听同 Hub/同库/同 Broker/
  一次数据目录锁，全部绑定成功才启动，否则回滚；Stop 释放全部；mDNS 只 LAN。
- 桌面侧：设置窗口增加「同时使用内网和 Tailscale」模式，分别选择 LAN/Tailscale 具体
  IPv4；无 Tailscale 网卡时双模式不可选、仍可用内网单模式，不要求安装。
  双模式额外地址写入 `desktop.json` 的 `additional_bind_addresses`，旧文件兼容。
  设置窗口只显示 `Hub.URLs()` 的真实地址与类型；收件箱优先 LAN 地址；系统代理只提示。
- 回滚：旧 `ApplyNetwork` 保留；新增双模式应用方法。保存/重启时任一绑定失败或端口占用，
  明确报错并尝试恢复**原配置**（单或双），失败的选择不落盘，不静默半启动。
- 测试与验收：appsettings 旧文件兼容/双地址校验、hubmgr 真实双监听共享数据库/WS、
  启动原子失败、单模式不继承 config additional、桌面服务双模式真实 LAN+Tailscale
  绑定与失败回滚；真实 Wails GUI 保存/重启与两个地址健康/登录/WS；CLI 双监听实测。
- 不做：`0.0.0.0`、两个 Hub、两个进程共享数据、自动网络重绑定/自动选路、CLI/Android
  协议变更、LAN 管理端点。真实手机跨网仍待验收，不把本机双地址测试当作真机通过。

## 实际完成状态

| 阶段 | 状态 | 说明 |
|---|---|---|
| M0 骨架 | ✅ 完成 | 配置/迁移/健康检查/占位页/dev.sh/README；`go vet` + `go test` 通过 |
| M1 文字闭环 | ✅ 完成 | Argon2id + 会话哈希 + CSRF/Origin/Host/限流；文字/链接、keyset 分页、删除、WS；双浏览器 E2E 通过 |
| M2 图片/文件 | ✅ 完成 | 流式上传、进度、原子落盘、SHA-256、磁盘/并发限额、受限预览、鉴权下载、janitor；E2E 通过 |
| M3 部署稳定性 | ✅ 完成 | systemd 单元、单文件构建、Tailscale/Caddy 文档、doctor、backup、重启持久化与断线恢复测试 |
| M4 HTTP LAN + Android | ✅ 代码与 APK 已交付 | Hub HTTP 适配、Web 复制降级和 Android 原生客户端完成；APK 文件及摘要已核对，真机验收待进行 |
| M5 局域网自动发现 | ✅ Hub 端完成 | mDNS 发布、配置开关、地址/网卡限制、生命周期与真实探针验证完成；Android 发现 UI 与真机待验收 |
| M6 Linux 桌面 App | ✅ 交付 | Wails v3.1.0 + GTK3；Hub 同进程嵌入；收件箱/设置双窗口、托盘、关闭→托盘、退出停 Hub、地址切换、首次密码、上传/下载/剪贴板/外链均实测；`dist/flowdrop-desktop` 已生成 |
| M7 桌面 LAN + Tailscale 双监听 | ✅ 交付（本轮） | Hub `additional_listen`/`URLs()` + 桌面双模式；真实 LAN+Tailscale 同端口双地址共享数据/会话/WS；失败回滚；单地址旧方式与 desktop.json 兼容；最终二进制与摘要见 VERIFICATION §14 |
| 真机验收 | ⏳ 待用户 | Android 原生 App、真实 Wi-Fi 明文 HTTP、移动网络 Tailscale、跨设备自动发现未进行；桌面版 Linux 与 Windows 均已交付（Windows 见 DEPLOY §18，macOS 未做） |
| M8 Linux 整窗文件拖拽 | ✅ 已交付，用户确认真机可用 | 修正原生动作协商与异步接收；最新 GTK Wayland 拖入/重复发送与 X11 核心回归通过，下载 SHA 一致；用户确认真实 KDE/Dolphin 拖拽可用，见 VERIFICATION §19；此前扩展场景见 §18 |

详细命令与实际输出、已知限制、待真机验收清单见 `docs/VERIFICATION.md`。

## 最终集成与待验收

- 三个独立 pi worker 已完成各自实现，Android APK 位于 `android/app/build/outputs/apk/debug/app-debug.apk`。
- Hub worker 报告 73 个后端测试通过，Web worker 报告 15 个端到端测试通过。
- 协调者核对 Android JVM XML：29 个测试通过；设备测试 XML 为 32 个、2 个跳过、无失败。
  带 Hub 参数的早一轮 32 项全部执行成功，协调者已核对 Gradle 日志；两轮均为模拟器。
  见 `VERIFICATION.md §11`。
- Android discovery worker 已交付（仅改 `android/`）：APK SHA-256
  `f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8`；
  53 个 JVM 测试全通过，41 个设备测试中 39 个执行通过、2 个 LiveHub 因无参数跳过。
  模拟器在 API 36 上 20 秒内没有 `onServiceFound`，真实手机 mDNS 互通仍未验证；
  不得把模拟器注入测试写成真实局域网发现通过。详见 `VERIFICATION.md §12.9`。
- 仍需真实 Android 手机与 Wi-Fi、移动网络 Tailscale、系统 APK 安装完成流程验收。


## M9 设备配对与多地址自动连接（2026-11-08）

用户确认范围见PRD §20与[PAIRING.md](PAIRING.md)，每项派发与证据见[TASKS.md](TASKS.md)。源码接入、旧公开密码入口退役、服务端/桌面/Android单测与编译已完成；Linux两产物构建、Chromium16项E2E及隔离Wails配对/拖拽通过。Android网络层对真实Hub双回环地址验证与APK构建已完成，全量102 JVM通过；真实手机Wi-Fi/Tailscale和本轮KDE配对待用户验收。此阶段覆盖前文M1/M4/M6的密码登录与不自动选路结论，不删除历史验收证据。最新证据与限制见VERIFICATION §20。

## 当前计划校准（2026-11-13）

本节以 [TASKS.md](TASKS.md) 最新记录与 [VERIFICATION.md](VERIFICATION.md) 为准，只做
校准，不改写前文历史阶段结论与证据。

- **HTTP 主路径不变**：仍为家庭可信 LAN 明文 HTTP（M4）+ 用户现有 Tailscale 跨网。
  HTTPS/WSS 仅登记待办 **HTTPS-01**（用户 2026-11-13 明确暂不启用，低优先级待排期，
  无实施授权）；届时在 Tailscale Serve 或自有域名+DNS-01 之间先选部署方式，不默认
  手工本地 CA；若用本地 CA 需补 Android 应用侧信任配置（当前仅系统 CA）。
- **推荐下一步（未获新授权，除已登记的待用户真机验收外均不得据此实施）**：
  1. 真实 Android 手机验收：新版 APK + 带 category 协议的新 Hub，覆盖分类切换/分类
     内分页/实时同步、文本上传与分享新增归入当前分类，以及 Wi-Fi HTTP 与移动网络
     Tailscale 地址切换（待用户设备条件）。
  2. 真实桌面分类使用反馈：TYPE 仅有独立 React renderer 证据，真实 WebView/视觉与
     窗口端到端未验证（待用户使用或隔离 GUI 环境）。
  3. 从既有未完成计划中择一（需用户决定）：M5/PAIRING 已登记的 Android 真机 mDNS
     发现与配对（可与第 1 项同窗验收），或 PLAN 中标为「macOS 未做」的 macOS 桌面端。
- 不在本节凭空新增产品功能，也不把已完成的历史项（如 QR 配对、桌面双监听 M7）重新
  记为待开发。
