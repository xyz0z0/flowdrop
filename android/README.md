# FlowDrop Android 原生客户端

FlowDrop 的 Android 轻客户端（Kotlin + Jetpack Compose），作为 Web 之外的原生入口：
注册系统分享菜单，收发文字、链接、图片、APK 与普通文件。

- 服务端：复用仓库内 Go Hub（`server/`）；业务 HTTP 协议未变。局域网发现使用
  Hub 发布的 mDNS/DNS-SD 记录（仅地址提示，不含业务数据或凭据）。
- 认证：与 Web 完全相同的 Cookie 会话 + CSRF 双提交 + 同源 Origin，见下文“协议基线”。
- 网络：允许明文 `http://`（可信家庭 Wi-Fi）；`https://` 走系统证书校验，
  **不全局关闭 TLS 校验**。

> 需求基线：[../docs/PRD.md](../docs/PRD.md)（§1.4 非目标、§15 实施澄清）
> 架构：[../docs/ARCHITECTURE.md](../docs/ARCHITECTURE.md)
> 部署（含 HTTP/LAN 模式）：[../docs/DEPLOY.md](../docs/DEPLOY.md)

## 1. 功能范围

已实现（对应 PRD P1 “Android 原生轻客户端”）：

| 能力 | 说明 |
|---|---|
| Hub 地址 / 用户名 / 密码登录 | 地址与用户名记住；**密码不保存**，登录成功即从内存丢弃 |
| 附近 Hub 自动发现 | 登录页前台短时搜索同一 Wi-Fi 的 FlowDrop Hub（mDNS/DNS-SD）；点选只填写地址，仍需用户输入密码登录 |
| 会话安全保存 | 会话 Cookie + CSRF + 地址/用户名，用 Android Keystore AES-GCM 加密后存应用私有目录 |
| 会话恢复 | 重启后 `GET /api/v1/auth/session` 恢复；CSRF Cookie 丢失时由 Hub 重新下发 |
| 退出 | `POST /api/v1/auth/logout` 吊销服务端会话，并清除本地全部会话态与图片缓存 |
| 文字 / 链接发送 | 自动识别 `http(s)://…` 为 `link`，其余为 `text` |
| 历史分页 | 倒序 keyset 游标，`limit` 1..100，“加载更早的内容” |
| 图片 / 普通文件 / APK 上传 | 系统文件选择器（SAF），多选逐个上传（每文件一条目），带字节进度与取消 |
| 图片预览 | 受鉴权拉取（同一 Cookie 会话），解码后内存缓存，点击放大 |
| 复制 | 复制文字 / 链接到剪贴板（仅用户点击触发） |
| 下载 | 流式写入 Downloads（MediaStore），校验 `X-Content-SHA256`，带进度与取消 |
| 删除 | 先本地移除（乐观更新），失败回滚并提示 |
| 实时更新 | WebSocket `/api/v1/events`；断线指数退避重连；**每次重连/回前台都 REST 重拉** |
| 系统分享入口 | `ACTION_SEND` / `ACTION_SEND_MULTIPLE`（text / image / application / audio / video） |
| APK | 下载后交给**系统安装器**，出现 Android 自己的“允许安装未知应用”确认；不申请静默安装 |

明确不做（与 PRD 一致）：后台常驻/系统通知、静默安装、后台读剪贴板、
自动过期、设备管理、断点续传、自动跨网络选路、二维码配对、多 Hub 并存、
TLS 证书配对。

## 2. 协议基线（冻结，与 Web 完全一致）

```text
POST /api/v1/auth/login      JSON {username,password} → {csrf_token,...} + Set-Cookie
GET  /api/v1/auth/session    Cookie → {authenticated,csrf_token,limits}
POST /api/v1/auth/logout     Cookie + CSRF → 204
GET  /api/v1/items?limit&cursor
POST /api/v1/items/text      {type,text,sender_device}
POST /api/v1/items/upload    multipart(sender_device,file)
GET  /api/v1/items/{id}
DELETE /api/v1/items/{id}
GET  /api/v1/items/{id}/download   强制 attachment
GET  /api/v1/items/{id}/preview    仅安全栅格图 inline
GET  /api/v1/events          WebSocket，item.created / item.deleted / hello
```

从客户端实现角度看，必须满足的约束（`ApiClient` 已按此实现）：

1. **所有写入**携带会话 Cookie、`flowdrop_csrf` Cookie 与 `X-FlowDrop-CSRF` 头（双提交）。
2. **所有写入与 WS 握手**携带与 Hub 同源的 `Origin`：`scheme://host:port`。
   Hub 会校验 Origin 主机名在 `allowed_hosts` 中且与 `Host` 一致。
3. **地址变化必须隔离会话**：切换 Hub 地址时先销毁上一个 origin 的 Cookie，
   CookieJar 也按 origin 过滤，绝不把 Cookie 发给其他服务器（见
   `SessionStore.switchTo` / `PersistentCookieJar`）。
4. **401 一律回登录**：任何请求或 WS 收到 401 都清本地会话并显示登录页。
5. 上传单文件一请求；下载与预览都需要鉴权，不能靠“知道随机 ID”绕过。

WS 用 `http://`/`https://` URL 交给 OkHttp 升级（它会自动转 `ws`/`wss`），
握手头里带 `Origin`，Cookie 由同一个 CookieJar 注入。Hub 的 `Origin` 检查
对无 Origin 的请求是放行的，但原生客户端始终发送，行为与浏览器一致。

### 2.1 局域网自动发现（冻结，mDNS/DNS-SD）

发现只是“地址提示”，不替代登录，也不构成 Hub 身份验证。记录格式：

```text
Service type  _flowdrop._tcp  （local. 域）
Instance      FlowDrop-<昵称>
SRV           Hub 的真实 HTTP 端口
A/AAAA        仅明确 LAN IPv4（RFC1918）
TXT           v=1, scheme=http, path=/
```

- Hub 侧仅在监听**具体 RFC1918 LAN IPv4** 且 `cookie_secure=false` 时发布；
  Tailscale/CGNAT、loopback、通配地址、HTTPS 反代不发布。发布内容不含密码、
  会话 Cookie、CSRF、条目内容或文件路径。
- Android 侧使用平台 `NsdManager`（`_flowdrop._tcp`，`PROTOCOL_DNS_SD`）：
  - 只解析实例名以 `FlowDrop-` 开头的服务；
  - 只接受 TXT `v=1`、`scheme=http`、`path=/`，端口 1..65535；
  - 只接受严格点分十进制、且属于 `10/8`、`172.16/12`、`192.168/16` 的 IPv4；
    明确拒绝 IPv6、loopback、link-local、CGNAT/Tailscale、公网、组播与 `0.0.0.0`；
  - 串行 `resolveService`（一次只解析一个，带 5 秒看门狗），兼容旧 API；
  - 按实例名去重，同一实例重新解析会更新地址；服务 `lost` 或地址被新实例
    复用时移除旧行；
  - 不扫描 IP 网段，不启动常驻后台服务，不申请定位权限。
- 生命周期：只在登录页前台搜索；首次进入登录页自动短时搜索一次（12 秒），
  也可手动搜索/刷新/停止；进入后台、离开登录页或超时立即停止，
  注销 discovery/resolve 回调并释放 Wi-Fi multicast lock。
- **发现结果不被信任**：点选一行只调用“填写 Hub 地址”，不会自动登录、
  不会自动恢复会话、不会向陌生服务发送已保存 Cookie 或密码；用户仍须确认
  地址并输入密码。切换地址仍走原有的“按 origin 清会话”逻辑。

## 3. 安全检查清单（本客户端）

- [x] 不保存密码；密码只存在于登录输入框的瞬时状态，登录完成立即清空。
- [x] 会话 Cookie 静态加密（Keystore AES-256-GCM），不参与 cloud backup / 设备迁移
      （`allowBackup=false` + `data_extraction_rules.xml` 排除）。
- [x] Cookie 按 origin 隔离，更换地址即清空；`PersistentCookieJar` 双重过滤。
- [x] 明文 HTTP 仅通过 `network_security_config.xml` 的 `cleartextTrafficPermitted` 放行；
      `trust-anchors` 保持系统证书，**未**实现任何 `TrustManager` 绕过。
- [x] 不申请存储权限：上传用 SAF content URI，下载走 MediaStore。
- [x] 不申请静默安装；仅 `REQUEST_INSTALL_PACKAGES` 供系统安装器接收 APK，
      由用户在系统弹窗确认。
- [x] 分享进来的 content URI 只读取系统授予的那几个；上传时才惰性打开，
      授权在 Activity 生命周期内有效。
- [x] 大文件不进内存：上传 `StreamingRequestBody`（64 KiB 缓冲），下载 `byteStream()` 直写。
- [x] 下载校验 Hub 返回的 `X-Content-SHA256`，不匹配即失败并删除残留文件。
- [x] 日志不打印正文、Cookie 或口令。
- [x] 不申请定位权限，不扫描 IP 网段；发现只用 `NsdManager`，multicast lock
      仅在登录页前台搜索期间持有，停止/超时/后台立即释放。
- [x] 发现记录严格校验（服务前缀、TXT、端口、RFC1918 IPv4），不兼容记录直接丢弃。
- [x] 发现结果只当地址提示：不自动登录、不自动恢复会话、不向发现的服务发送
      已保存 Cookie/密码；点选只填地址。

## 4. 目录结构

```text
android/
├── settings.gradle.kts / build.gradle.kts / gradle.properties
├── gradle/wrapper/                      Gradle 8.11.1 wrapper
├── local.properties                     sdk.dir（本机，不入库）
└── app/
    ├── build.gradle.kts
    ├── src/main/AndroidManifest.xml      SEND/SEND_MULTIPLE、FileProvider、明文配置
    ├── src/main/java/dev/flowdrop/android/
    │   ├── FlowDropApp.kt                Application、AppContainer、AppPreferences、VM Factory
    │   ├── MainActivity.kt               Compose 根、生命周期、分享 Intent 解析
    │   ├── AppViewModel.kt               会话/列表/上传/下载/WS 状态机
    │   ├── data/                         ApiClient、HubEndpoint、SessionStore、
    │   │                                 PersistentCookieJar、EventStream、Keystore 存储
    │   ├── discovery/                    DiscoveryProtocol（纯校验/去重）、
    │   │                                 DiscoveryController（前台会话/超时）、
    │   │                                 NsdHubDiscovery（平台 NsdManager）
    │   ├── io/                           UriSources（流式上传源）、DownloadTarget（保存/APK）
    │   ├── device/SystemActions.kt       剪贴板、打开链接、系统安装器、分享
    │   └── ui/                           LoginScreen、InboxScreen、ComposerSection、
    │                                     ItemCard、ToastHost、Theme、鉴权图片加载
    ├── src/test/                         53 个 JVM 单测（协议/隔离/下载目标/局域网发现）
    └── src/androidTest/                  41 个模拟器测试（分享解析、登录/收件箱 UI、
                                          局域网发现生命周期、实时 Hub 契约）
```

## 5. 构建

依赖（本机已具备）：JDK 17+（本机用 `~/.gradle/jdks/eclipse_adoptium-21-amd64-linux.2`）、
Android SDK Platform 35 + Build-Tools 35、Gradle wrapper。

```bash
cd android

# Debug APK
./gradlew :app:assembleDebug
# → app/build/outputs/apk/debug/app-debug.apk

# JVM 单元测试（无需设备）
./gradlew :app:testDebugUnitTest

# Lint
./gradlew :app:lintDebug

# 设备/模拟器测试（模拟器需已启动，adb devices 可见）
./gradlew :app:connectedDebugAndroidTest
```

`gradle.properties` 里 `org.gradle.java.home` 指向本机 JDK 21；换机器时删掉该行
或用 `JAVA_HOME` / `-Dorg.gradle.java.home=...` 覆盖。`local.properties` 的
`sdk.dir` 同理（该文件已被 `.gitignore` 忽略）。

## 6. 启动与连接说明

### 6.1 Hub 侧（明文 HTTP，可信家庭 Wi-Fi）

Hub 默认只监听 `127.0.0.1` 并要求 `Secure` Cookie，手机连不上。明文模式需要显式配置：
监听**具体本机局域网 IP**（拒绝 `0.0.0.0/::`）、关闭 `Secure` Cookie、把手机访问用的
主机名加入 `allowed_hosts`。仓库已提供两种方式：

```bash
# 方式一：辅助脚本（自动探测局域网 IP 并启动，推荐）
scripts/lan-http.sh

# 方式二：手工
set -a; . deploy/http-lan.env.example; set +a   # 改成你自己的 IP
./dist/flowdrop serve
```

手机端填写形如 `http://192.168.1.50:8080` 的地址（与 Hub 的监听 IP:端口一致）。
若用主机名（`http://flowdrop.local:8080`）访问，必须把该名字加进
`FLOWDROP_ALLOWED_HOSTS`，否则 Hub 返回 421 或 `origin_not_allowed`。

### 6.1.1 不在同一 Wi-Fi：直接用 Tailscale IP 走 HTTP（不需要 Serve / 不需要证书）

本 App 的目标形态是**纯 HTTP**，Tailscale 只负责三层连通性（WireGuard 隧道），
不参与 TLS。跨网时不要开 `tailscale serve`，直接绑定本机的 Tailscale IP 即可：

```bash
# 1) 在 Hub 上取 Tailscale IPv4（通常是 100.64.0.0/10 里的地址）
tailscale ip -4
#   例如输出 100.101.102.103

# 2) 用该 IP 作为监听地址启动明文 HTTP（本仓库辅助脚本已支持 Tailscale CGNAT 网段）
scripts/lan-http.sh --host 100.101.102.103 --port 8080

# 等价的手工方式：
#   FLOWDROP_LISTEN=100.101.102.103:8080 \
#   FLOWDROP_ALLOWED_HOSTS=100.101.102.103 \
#   FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
```

手机端（已登录同一 Tailnet）填 `http://100.101.102.103:8080`。
Tailscale 客户端在手机上会把该 CGNAT 地址路由进隧道，因此即使两台设备不在同一
Wi-Fi、也没有公网端口映射，HTTP 仍然直连可用。前提是 Tailscale ACL 允许该端口。

**单 IP 绑定限制（重要，二者不能同时兼得）：**

- `FLOWDROP_LISTEN` 只能写**一个**具体 IP。明文模式下 Hub 会拒绝 `0.0.0.0` / `::`，
  直接报错退出：
  `listen must name the specific LAN IP when cookie_secure=false; binding 0.0.0.0/:: would expose plaintext HTTP on every interface`
- 实测（本机 Tailscale IP `100.125.168.63`，端口 18099）：
  ```text
  http://100.125.168.63:18099/api/v1/health  -> 200   # 绑定的那个 IP
  http://192.168.31.184:18099/api/v1/health  -> 000   # 同端口的局域网 IP，拒绝连接
  http://127.0.0.1:18099/api/v1/health       -> 000   # 回环，拒绝连接
  ```
  即绑定是**按 IP 生效**的，不是按端口。
- 所以同一时刻只能二选一：
  - 绑**局域网 IP**（`192.168.x.x`）→ 家里 Wi-Fi 的其它设备可访问，Tailscale 侧不可；
  - 绑**Tailscale IP**（`100.x.y.z`）→ 任何登录同一 Tailnet 的设备可访问（含家里），
    但**没装 Tailscale 的家庭设备**访问不了。
- 想两者同时可用，需要监听两个实例（不同端口，各自绑定不同 IP），或改回
  TLS 反代 + 单点入口。本客户端两种地址都支持，只需在登录页改地址即可，
  但**改地址会清除当前会话**（见下方安全说明）。
- Hub 会自动把 `FLOWDROP_LISTEN` 里的那个具体 IP 加入允许的 Host，因此填同一个
  IP 访问不会 421；若改用 MagicDNS 名（如 `http://hub.tailnet-name.ts.net:8080`），
  必须把该名字加入 `FLOWDROP_ALLOWED_HOSTS`。
- 明文跨网意味着**密码、会话 Cookie 与文件内容都在 Tailscale 隧道内明文**。
  Tailscale 提供的是加密隧道，安全性依赖 Tailnet 成员可信。

如果更希望跨网也走 HTTPS，可以继续用 `tailscale serve`（Tailscale 终结 TLS，
证书由 Tailscale 自动签发），此时 App 填 `https://` 地址，走系统证书校验即可。
两种方式都可用，按“是否想引入证书”自行选择。

### 6.2 App 侧

1. 安装 `app-debug.apk`。
2. 首屏“附近的 Hub”会在前台自动短时搜索（12 秒），也可点 `搜索` / `刷新` /
   `停止`。点选一个结果**只会把地址填入“Hub 地址”**，不会自动登录；仍需输入
   密码后点 `登录`。找不到时可照常手动输入 Hub 地址（包括 Tailscale 地址）。
3. 收件箱：顶部显示连接状态；`本机名称` 会作为 `sender_device` 随条目上报。
4. 发送：输入框输入文字或粘贴链接 → `发送`；`选择图片` / `选择文件` 走系统选择器。
5. 收到其它设备内容会经 WebSocket 实时出现；断线或从后台回来会自动重拉列表。
6. 系统任意应用的“分享”菜单可直接选 **FlowDrop**，发送文字或文件。
7. 附件卡片的 `下载` 保存到 Downloads；APK 额外有 `安装`，由系统确认安装。

### 6.3 验证协议是否通的最小检查

```bash
# Hub 起来了吗
curl http://<hub-ip>:8080/api/v1/health
# 登录（应返回 csrf_token 与 Set-Cookie）
curl -i -H 'Origin: http://<hub-ip>:8080' -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"..."}' \
  http://<hub-ip>:8080/api/v1/auth/login
```

## 7. 平台限制与未验证项

- **后台不常驻**：App 在 `onStop` 主动关闭 WebSocket，回前台再重连并 REST 重拉；
  不申请前台服务，因此不保证后台收到实时更新，也没有系统通知（与 PRD 一致）。
- **明文边界**：HTTP 模式下密码、Cookie 与内容在同一 Wi-Fi 可被监听。这是用户
  明确接受的取舍，只应在家用可信网络使用。
- **`REQUEST_INSTALL_PACKAGES`**：Android 8+ 系统安装器要求来源具备该权限才能弹出
  安装确认；App 自身从不执行安装。
- **真机未验证**：所有自动化与手工验证都在 **Android 模拟器**（API 36 / x86_64）
  完成，**没有任何一项在真实手机上跑过**。真实手机、真实 Wi-Fi 与 Tailscale 跨网
  链路、厂商 ROM 的后台策略、真实 APK 安装完成流程均未经用户真机验收。
- **局域网发现的网络限制**：mDNS 依赖组播。访客 Wi-Fi/AP 隔离、路由器关闭
  组播、企业网络或 Android 模拟器（不转发宿主 mDNS）都会导致列表为空。App 会
  显示友好提示并保留手动输入地址；这是预期边界，不是登录失败。
- **发现不是身份验证**：同名实例可能是任意设备。App 不会用发现结果登录或恢复
  会话，也不会把已保存 Cookie/密码发给它；地址与密码仍由用户确认。
- **未来 Android 版本**：本版本 `targetSdk=35`。Android 17（API 37）起对
  target 37+ 的应用引入本地网络权限；未来提升 targetSdk 时需要重新评估
  `ACCESS_LOCAL_NETWORK` 与 `NsdManager` 的可见性配置。
- **SAF 授权范围**：分享时读取的是系统授予的 content URI；极少数应用给的是
  短时授权，若上传耗时极长可能在授权失效后失败（会提示上传失败，不会静默丢数据）。
- 未实现：断点续传、批量打包上传、搜索、内容自动过期、设备管理、通知、
  二维码配对、自动跨网络选路、多 Hub 并存。

## 8. 测试覆盖

| 层 | 内容 | 数量 |
|---|---|---|
| JVM 单测 | `HubEndpoint` 解析与 origin、会话按 origin 隔离、Cookie 不跨源、`ApiClient` 对 MockWebServer 的完整协议（登录/CSRF/Origin/分页/流式上传/流式下载+SHA-256 校验/401/会话恢复/登出/413）、下载文件名与 MIME 归一化、局域网发现记录校验/去重/离线移除、发现控制器自动搜索一次/超时/停止/迟到回调 | 53 |
| 设备测试 | `ACTION_SEND` / `ACTION_SEND_MULTIPLE` / ClipData 解析去重、登录页 Compose UI（含附近 Hub 列表、点选只填地址、搜索/停止、手动输入回归）、收件箱各状态 UI、真实 `NsdManager` 启停生命周期、**对运行中的真实 Hub 的契约**（登录→会话恢复→发文字→列表→上传→下载校验→删除→登出→401；WebSocket 收到 item.created） | 41（无 Hub 参数时 2 skip） |

### 8.1 设备测试的准确口径（避免误读）

- 全部 41 项都跑在 **Android 模拟器**（Medium_Phone_API_36，API 36 / x86_64）上，
  **没有在真实手机上跑过任何一项**。下文“实时 Hub 契约”指的是“真实运行的 Hub
  进程 + 模拟器客户端”，不是真机。
- 41 项里 `LiveHubContractTest` 的 2 项需要 Hub 参数；**不带参数时这 2 项会被
  JUnit `assumeTrue` 判为 skipped**，此时报告为 `tests=41 skipped=2`，实际执行 39 项。
- 因此有两种合法跑法，数字不同但都是全绿：

```bash
# A) 不带 Hub 参数：41 项中 2 项 skip → 实际执行 39 项
./gradlew :app:connectedDebugAndroidTest
#    报告：tests=41 skipped=2 failures=0

# B) 带 Hub 参数：2 项真实连上 Hub 执行 → 41 项全部实际执行
./gradlew :app:connectedDebugAndroidTest \
  -Pandroid.testInstrumentationRunnerArguments.hubUrl=http://<hub-ip>:8080 \
  -Pandroid.testInstrumentationRunnerArguments.hubUser=admin \
  -Pandroid.testInstrumentationRunnerArguments.hubPass=<password>
#    报告：tests=41 skipped=0 failures=0
```

- 注意：`connectedDebugAndroidTest` 每次都会**覆盖**
  `app/build/outputs/androidTest-results/connected/debug/` 下的报告，
  只保留最后一轮结果。若最后一次跑的是 A，磁盘上的 XML 就会是 `skipped=2`，
  这是预期现象，不代表 B 那轮的 2 项没通过。核对数量以
  `TEST-*.xml` 的 `tests/skipped/failures` 为准。
- 本轮实测控制台打印 `Starting 41 tests`、结束时却打印 `Finished 43 tests`，
  而最终 XML 明确是 `tests=41 skipped=2`；这是 Gradle/instrumentation 控制台的
  计数噪声，**以 XML 的 41 为准**，不要把 43 计入。
- 这些 2 项即使在模拟器上真连 Hub，也**不等于真机验证**：真机的系统安装器、
  厂商后台策略、真实 Wi-Fi/Tailscale 链路仍未验证（见第 7 节）。

### 8.2 本轮局域网发现实际测试记录（2026-11-07）

环境：Linux 主机，Hub 监听 `192.168.31.184:18099`（RFC1918 + `cookie_secure=false`），
Android 模拟器 Medium_Phone_API_36（API 36 / x86_64），`adb devices` 可见。

```text
./gradlew :app:testDebugUnitTest
  BUILD SUCCESSFUL
  tests=53 failures=0 skipped=0        # 实际执行 53

./gradlew :app:connectedDebugAndroidTest        # 不带 Hub 参数
  BUILD SUCCESSFUL
  tests=41 failures=0 skipped=2        # 实际执行 39；2 项为 LiveHubContractTest skip

./gradlew :app:assembleDebug
  BUILD SUCCESSFUL
  APK: /home/bob/vibe-code/flowdrop/android/app/build/outputs/apk/debug/app-debug.apk
  sha256: f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8
```

真实 mDNS 互通尝试（不是注入假服务）：

- 用仓库 Hub 源码构建并启动临时实例，日志确认发布：
  `mDNS discovery published service=_flowdrop._tcp.local.
  instance=FlowDrop-bob-hpelitedesk880g6 address=192.168.31.184 port=18099
  txt="[v=1 scheme=http path=/]"`。
- 用一次性 instrumentation 探针在模拟器上调用真实 `NsdHubDiscovery`：收到
  `onDiscoveryStarted`，但 20 秒内**没有**收到 `onServiceFound`，即模拟器没有把
  宿主 LAN 的组播/mDNS 转发进来。该探针已删除，不计入上面的 41 项。
- 因此本轮**没有**在模拟器上完成 Android 客户端 ↔ 真实 Hub 的发现结果端到端
  验证；这是模拟器网络限制（任务已预期）。已验证的边界是：平台 NSD 启停/重复
  停止安全（2 项设备测试）、真实 `NsdManager` 能进入发现状态、协议解析与过滤
  （JVM）、登录页列表/点选只填地址/搜索停止/手动输入（设备 UI 测试）。
  真机 + 真实 Wi-Fi 的 mDNS 互通仍需后续验收。
- 额外做了安装后的模拟器进程级冒烟：`adb install -r` 后清空数据启动，
  登录页显示“附近的 Hub”并自动进入“正在搜索附近的 Hub…”，12 秒后转为
  “未发现 Hub…请手动输入地址”与 `刷新`，`AndroidRuntime` 无崩溃。
- 后台停止也实测：搜索期间按 Home 2 秒再回前台，状态已是 `刷新` + 未发现提示，
  而不是继续“正在搜索”，说明 `onStop` 已停止 NSD/释放组播；返回后台后不会自动
  重新开搜，需用户点 `搜索`。


## 9. 已知取舍

- 图片预览在内存里按 ID 缓存最近 12 张、单边超过 720px 时降采样，避免长列表 OOM。
- 上传逐个顺序进行：Hub 每请求只接受一个文件，并对并发上传有限制。
- 切换 Hub 地址即清空会话，不做多 Hub 并存；这与“不把 Cookie 发给其他服务器”一致。
- 局域网发现只在登录页前台运行 12 秒；超时会保留结果但停止搜索，避免持续占用
  组播/电量。再次搜索或刷新由用户触发。
- 发现列表按实例名去重，并用最终地址再次去重；同一 Hub 改地址会原地更新而不是
  新增一行。
