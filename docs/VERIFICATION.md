# FlowDrop 验证记录

> **历史基线说明（请先读）**：第 1–8 节记录的是 **2026-11-07 第一轮（v0.1，以 HTTPS/反代
> 为主路径）** 的实测结果，作为**旧版基线**保留。当时的产品结论（“局域网必须 HTTPS/
> 导入 CA”）已被用户推翻：当前主路径是**家庭可信 LAN 明文 HTTP**。
> **本轮（HTTP + Web 复制降级）的实际检查结果见第 10 节**；旧 HTTPS 结果**不得**当作
> 新的 HTTP 结果引用。Android 原生 App / 真机 / 真实 Wi-Fi 仍未验证。
>
> **最新一轮（2026-11-08，Linux 桌面 + LAN/Tailscale 双监听）见第 14 节。**
> 第 13 节保留为当时“单地址桌面版”的历史证据；其中“一个进程只绑定一个地址”的限制
> 已由第 14 节取代，但旧记录不修改、不覆盖。

执行环境：

| 项目 | 值 |
|---|---|
| 主机 | `bob-hpelitedesk880g6`（Ubuntu 26.04 LTS / Kubuntu） |
| 内核 | Linux 7.0.0-42-generic x86_64 |
| Go | go1.26.8 linux/amd64 |
| Node | v24.16.0，npm 12.2.0 |
| 浏览器 | Google Chrome（第一轮记录为 158；本轮实测为 153.0.8010.52） |
| SQLite | 3.50.6（`modernc.org/sqlite` v1.62.0，纯 Go） |
| 第一轮执行日期 | 2026-11-07（HTTPS 基线） |
| 本轮执行日期 | 2026-11-07（HTTP LAN + 复制降级） |

> 结论先行（旧基线，仅适用于 HTTPS 路径）：**M0–M3 的代码、自动化测试与本地 HTTPS
> 反代链路已实测通过。**
> **真实 Android 手机 / 原生 App / 真实 Wi-Fi 明文 HTTP / 移动网络 Tailscale 尚未由用户
> 真机验收**，见文末「待真机验收」一节。本文件不声称这些项已通过。

---

## 1. 后端测试

命令：

```bash
cd server
go vet ./...
go test ./... -count=1 -race
```

结果（实际输出）：

```
?   	flowdrop/cmd/flowdrop	[no test files]
ok  	flowdrop/internal/api	7.867s
ok  	flowdrop/internal/auth	4.212s
ok  	flowdrop/internal/config	1.010s
?   	flowdrop/internal/db	[no test files]
?   	flowdrop/internal/events	[no test files]
ok  	flowdrop/internal/storage	1.011s
ok  	flowdrop/internal/store	2.014s
?   	flowdrop/internal/webui	[no test files]
```

- `go vet`：无输出（无问题）。
- 通过用例数：**67 个**（`go test -v` 统计 `--- PASS` 行）。
- `-race` 通过，无数据竞争报告。

### 1.1 用例覆盖（67 个，按主题）

**认证与会话（auth + api）**

| 用例 | 断言的实际行为 |
|---|---|
| `TestHashPasswordRoundTrip` | PHC 参数为 `$argon2id$v=19$m=65536,t=3,p=2$`；正确密码通过、错一位拒绝、畸形哈希拒绝 |
| `TestHashPasswordIsSalted` | 同一密码两次哈希不同（盐生效） |
| `TestAuthenticateUnknownUserAndWrongPassword` | 未知用户返回 `ErrNoUser`，错密码返回 `ErrInvalidPassword` |
| `TestSetPasswordIsIdempotentAndRotates` | 轮换后旧密码失效、只剩 1 行 admin |
| `TestSessionsStoreOnlyHashes` | DB 中 `sessions.id != token` 且 `== sha256(token)`；删除后立即失效 |
| `TestExpiredSessionIsRejectedAndPurged` | 过期会话被拒且行被清理 |
| `TestSlidingExpiryExtendsIdleSession` | 空闲 2h 后访问，过期时间被延长 |
| `TestCleanupExpired` | 清理计数正确 |
| `TestLogoutRevokesSession` | 注销后 `sessions` 行数为 0 |
| `TestLogoutRequiresCSRF` | 缺 CSRF 的注销请求 403，且会话**未**被吊销 |
| `TestUnknownAPIPathDoesNotFallThroughToSPA` | 未知 `/api/...` 返回 JSON 404，不回落到 SPA 的 HTML |
| `TestSessionCookieIsHttpOnlyAndHashed` | `Set-Cookie` 含 `HttpOnly`、`SameSite=Strict`、`Path=/`；库中存的是哈希 |
| `TestLoginWrongPasswordIsUniform` | 错误凭据统一返回 `invalid_credentials`（不区分用户不存在） |
| `TestLoginRateLimited` | 超过 `login_limit_per_minute` 后返回 429 |
| `TestUnauthenticatedAccessIsRejected` | 7 个受保护端点（含 upload / events / download / preview）全部 401 |
| `TestHealthNeedsNoAuth` | `/health` 免认证，且响应体不含 password/data_dir/token |
| `TestCSRFRequiredForWrites` | 缺令牌、错令牌、外站 Origin 三种情况均 403 |
| `TestHostAllowlistRejectsUnknownHost` | `Host: attacker.example` → 421 |
| `TestForwardedProtoIgnoredFromUntrustedPeer` | 不受信来源伪造 `X-Forwarded-Proto: https` 既不能把 Cookie 变成 Secure，也不能触发 HSTS |

**条目、分页、上传、下载**

| 用例 | 断言的实际行为 |
|---|---|
| `TestTextAndLinkValidation` | 中文/空白处理；`javascript:`、`file://`、非 URL、纯空白、非法 type 均 400 |
| `TestPaginationIsStableAndDescending` | 12 条、每页 5 条翻完：无重复、无遗漏、严格 `(created_at, id)` 降序（同毫秒用 id 兜底） |
| `TestInvalidCursorAndLimit` | 坏 cursor、`limit=0/101/abc` 均 400 |
| `TestListItemsKeysetPagination` | 10 条含共享时间戳，keyset 遍历无重复 |
| `TestUploadImageIsPreviewableAndDownloadable` | 预览 `image/png` + `inline` + `nosniff`；下载 `attachment` 且字节一致、`X-Content-SHA256` 匹配 |
| `TestUploadActiveContentIsForcedAttachment` | `.html`/`.svg` 即使内容像文本/图形，预览也返回 `application/octet-stream` + `attachment` |
| `TestUploadRejectsOversizeWithoutArtifact` | 超限 413；条目 0、blob 行 0、`tmp/` 与 `blobs/` 均无残留文件 |
| `TestUploadFilenameCannotEscapeStorage` | `../../../etc/passwd` 被剥离为 `passwd`；磁盘路径只有随机 key，不含客户端文件名 |
| `TestEmptyFileIsAllowed` | 0 字节文件可上传、可下载 |
| `TestUploadExactlyAtLimitSucceeds` | 恰好等于上限的文件可上传（multipart 开销余量不会把“等于”误判为“超过”），回下载长度一致 |
| `TestDownloadRequiresSessionEvenWithKnownID` | 匿名请求知道 item id 也 401；删掉会话后同样 401 |
| `TestDeleteRemovesItemAndFile` | 删除后 item/blob 行 0、磁盘文件 0；二次删除返回 404 而非 500 |
| `TestCreateAndDeleteBlobItem` / `TestDeleteTextItemReturnsNoKey` / `TestDeleteMissingItem` | 引用计数与返回值语义（文本条目不返回 blob key） |
| `TestClassifyUpload` | `.png` 扩展名无法把 HTML 洗成图片；`image/svg+xml`、`text/html` 强制 `octet-stream`；APK 记为 `application/zip`/`file` |
| `TestSanitizeFileName` / `...TruncatesLongNames` | 15 组路径/控制字符/超长/中文名映射；保留扩展名 |
| `TestContentDisposition` | 同时给出 ASCII fallback 与 RFC 5987 `filename*`；无 CR/LF 注入 |
| `TestValidHTTPURL` | 8 个拒绝样例（含 `data:`、`//host`、换行注入、超长） |
| `TestSanitizeSender` / `TestHumanBytes` | 设备名长度与控制字符；大小格式化 |

**WebSocket**

| 用例 | 断言的实际行为 |
|---|---|
| `TestWebSocketRequiresAuth` | 未认证握手被拒（401） |
| `TestWebSocketRejectsForeignOrigin` | 持有效 Cookie 但 `Origin: https://evil.example` 被拒（403） |
| `TestWebSocketBroadcastsCreateAndDelete` | 首帧 `hello`；创建/删除分别收到 `item.created` / `item.deleted`，id 匹配 |

**持久化与清理**

| 用例 | 断言的实际行为 |
|---|---|
| `TestItemsSurviveRestart` | 关闭连接后重开数据目录：文本、blob 元数据、磁盘附件内容全部仍在 |
| `TestHTTPSReverseProxyContract` | 真实 TLS 反代下：`Secure`+`HttpOnly` Cookie、HSTS、认证 API 可用、非法 Host 421 |
| `TestJanitorRemovesOrphansAndStaleTemp` | 删除孤立 blob 行、过期 session、陈旧 `tmp/*.part`；**正在上传的临时文件不被删** |
| `TestJanitorRemovesUnreferencedBlobFiles` | 磁盘上无 DB 行的老文件被回收 |
| `TestJanitorKeepsCommittedBlobs` | 已引用且超过宽限期的 blob 不会被误删，下载仍 200 |
| `TestRemoveUnknownBlobsRespectsGrace` | 宽限期内新文件不删；已知 key 不删 |
| `TestCleanupTempSkipsActiveUploads` | 活跃上传即使 mtime 很旧也保留 |

**存储与配置**

| 用例 | 断言的实际行为 |
|---|---|
| `TestBlobPathRejectsBadKeys` | 7 类非法 key（含路径遍历、长度错误、非法字符）全部拒绝 |
| `TestCommitTempIsAtomicMove` / `TestWriteReaderAndOpenBlob` | 原子移动后临时文件消失、内容一致；重复写同一 key 失败而非覆盖 |
| `TestNewCreatesLayout` | `blobs/`、`tmp/` 以 `0700` 创建 |
| `TestDefaultsAreSafe` | 默认回环监听、`cookie_secure=true`、512 MiB、无可信代理/Host |
| `TestEnvOverridesFile` / `TestMissingFileIsFine` / `TestValidation` / `TestListenIsLoopback` | 配置优先级、缺失文件容错、7 类非法配置、回环判定 |

**HTTPS 反代契约（重点）**

`TestHTTPSReverseProxyContract` 起了一个真实的 **TLS 终结反代**（`httptest.NewTLSServer` + `ReverseProxy`，行为对齐 Tailscale Serve / Caddy），Hub 只看到来自 `127.0.0.1` 的明文请求：

- 经 HTTPS 代理登录成功，会话 Cookie 带 `Secure` + `HttpOnly`；
- 响应带 `Strict-Transport-Security`；
- 认证后的 `/api/v1/items` 正常；
- 未列入 `allowed_hosts` 的 Host 直接访问 Hub 返回 421。

---

## 2. 前端检查（旧版基线：第一轮，前端改动前）

> 本节为第一轮的旧结果，仅作历史基线；本轮最新的 `tsc`/`vite build` 结果见第 10 节。

```bash
cd web
node node_modules/typescript/bin/tsc -b --force   # 严格模式：strict + noUncheckedIndexedAccess + exactOptionalPropertyTypes
./node_modules/.bin/vite build
```

实际结果：

```
tsc: no errors
../server/internal/webui/dist/index.html                   0.64 kB │ gzip:   0.38 kB
../server/internal/webui/dist/assets/index-4JNp6SoA.css    9.54 kB │ gzip:   2.68 kB
../server/internal/webui/dist/assets/index-BXnrZ5U_.js   161.69 kB │ gzip:  53.06 kB
✓ built in 545ms
```

无第三方 CDN、无外链字体、无分析脚本；产物全部由 Go `embed` 打入二进制。

---

## 3. 生产构建（旧版基线：第一轮）

```bash
scripts/build.sh 0.1.0
```

```
构建完成： dist/flowdrop
  版本 0.1.0 (nogit) 2026-11-07T10:02:27Z
```

单文件约 11.4 MB，`CGO_ENABLED=0`（纯静态，无 cgo 依赖）。

自检：

```bash
FLOWDROP_DATA_DIR=/tmp/fd-doctor ./dist/flowdrop doctor
```

```
FlowDrop doctor 0.1.0
listen                 ok   127.0.0.1:8080
data_dir               ok   /tmp/fd-doctor
database               ok   schema v1
admin password         WARN missing — run 'flowdrop admin init'
free space             ok   15 GiB
port available         ok   can bind 127.0.0.1:8080
```

（`WARN` 是首次初始化前的预期状态。）

---

## 4. 端到端测试（旧版基线：第一轮，未含本轮 clipboard.spec.ts）

```bash
scripts/build.sh 0.1.0
cd tests && npm install && npx playwright test
```

每个 spec 用 `spawn` 启动**真实 `dist/flowdrop` 二进制**（独立临时数据目录、真实 SQLite、真实磁盘），浏览器为 Google Chrome 158。

```
Running 10 tests using 1 worker

  ✓   1 [chromium] unauthenticated visitor sees only the login form (333ms)
  ✓   2 [chromium] text sent in one browser appears live in another (1.1s)
  ✓   3 [chromium] link is rendered as a link and never as HTML (488ms)
  ✓   4 [chromium] image upload shows a preview and downloads the original bytes (473ms)
  ✓   5 [chromium] APK upload is downloadable and delete removes it everywhere (866ms)
  ✓   6 [chromium] multiple files upload sequentially, one item each (412ms)
  ✓   7 [chromium] oversize upload is rejected without creating an item (709ms)
  ✓   8 [chromium] reconnect after offline resyncs deletions made while disconnected (852ms)
  ✓   9 [chromium] session survives a page reload; logout revokes it (478ms)
  ✓  10 [chromium] content and session survive a Hub restart (1.1s)

  10 passed (7.8s)
```

逐项说明（**两个独立 browser context** = 两台“设备”）：

1. **未登录**：只看到登录表单；`GET /api/v1/items` 直接 401。
2. **文字双向实时**：A 发送，B 不刷新即在 DOM 出现；卡片标注发送设备 `Kubuntu 桌面`；B 点“复制”后读取系统剪贴板，内容与发送值完全一致。
3. **链接**：渲染为 `<a href=https://example.com/path?q=1>`，带 `rel="noopener noreferrer"`；输入 `<img src=x onerror=alert(1)>` 只作为纯文本显示，DOM 中不存在该 `<img>`（无 HTML 注入）。
4. **图片**：上传 6×6 PNG → 卡片出现 → `<img>` 的 `naturalWidth === 6`（证明确实通过鉴权预览解码成功）→ 点击开灯箱 → 下载字节与上传字节 `Buffer.equals` 一致，响应头 `X-Content-SHA256` 等于本地 `sha256`。
5. **APK**：A 上传 `.apk` → A/B 均显示“Android 安装包”标签 → B 下载字节一致 → A 删除 → B 的卡片经 WS **自动消失**，且原下载 URL 变 404。
6. **多选文件**：一次选择 3 个文件，最终恰好 3 个条目、每个文件名各 1 条（逐文件上传语义）。
7. **超限**：测试 Hub 限制 2 MiB，上传 3 MiB → 提示上传失败，且列表中**不产生条目**。
8. **断线重连**：B 先看到条目 → `context.setOffline(true)` → A 删除 → B 恢复在线 → B 自动重新拉取列表，已删除条目消失、WS 回到“实时同步中”。
9. **会话与注销**：刷新页面仍登录；点“退出”后回到登录页，旧 Cookie 请求 `/api/v1/items` 返回 401。
10. **重启持久化**：同一数据目录重启 Hub 后，旧 Cookie 仍有效、文字与附件条目都在、附件下载仍 200 且内容非空。

---

## 5. 手动验证：真实 Caddy HTTPS 反代（旧版基线；HTTPS 已改为可选方案）

用真实 Caddy v2.10.2 验证 `deploy/Caddyfile.example` 的等价配置（本机无 sudo，故用非特权端口 18443 与用户目录存储）：

- Caddy 以 `local_certs` + `tls internal` 自签本地 CA，终结 TLS 后反代到 `127.0.0.1:18095` 的 Hub；
- Hub 以生产配置启动：`FLOWDROP_COOKIE_SECURE=true`、`ALLOWED_HOSTS=flowdrop.test`、`TRUSTED_PROXIES=127.0.0.1,::1`。

| 检查 | 命令要点 | 结果 |
|---|---|---|
| 模板语法 | `caddy validate --config deploy/Caddyfile.example`（改端口/存储后） | `Valid configuration` |
| 健康检查 | `curl --resolve flowdrop.test:18443:127.0.0.1 --cacert <caddy-root> .../api/v1/health` | `200` |
| HSTS | 同上，看响应头 | `strict-transport-security: max-age=31536000` |
| CSP | 同上 | `content-security-policy: default-src 'self'; ...` |
| Cookie 属性 | 经反代登录 | `flowdrop_session=...; HttpOnly; Secure; SameSite=Strict`（CSRF Cookie `HttpOnly` 缺省，符合双提交设计） |
| 上传 8 MB | `-F file=@big8.bin` | `201`，耗时 0.029s |
| SHA-256 | API 返回 vs 本地 `sha256sum` | 一致（`d0d9cdc6…b57c7`） |
| 回下载 | `cmp big8.bin big8.back` | 字节完全一致 |
| 浏览器 `isSecureContext` | Chrome + `--host-resolver-rules` | `true` |
| WSS 实时 | 登录后页头显示“实时同步中” | 通过 |
| HTTPS 下发送文字 | UI 发送 | 条目出现 |
| 图片预览 | `.item-image img` 的 `naturalWidth` | `4`（解码成功） |
| 灯箱 | 点击缩略图 | 打开正常 |
| Cookie 实际属性 | `context.cookies()` | `flowdrop_session(secure=true,httpOnly=true)`、`flowdrop_csrf(secure=true,httpOnly=false)` |

> 说明：此验证**不是**手机真机。浏览器侧用 `ignoreHTTPSErrors` 绕过 CA 导入，
> 以便在无 root 的情况下驱动 Chrome；CA 链本身已用 `curl --cacert` 独立验证。
> 手机导入 CA 的步骤写在 `docs/DEPLOY.md §8.3`，**尚未在真机执行**。
> 本节属于旧 HTTPS 基线；主路径已改为明文 HTTP，Caddy 仅为可选加固。

---

## 6. 手动验证：备份与恢复（旧版基线，功能未变）

```bash
FLOWDROP_DATA_DIR=/tmp/fd-bk flowdrop backup /tmp/fd-backup.tar.gz
mkdir -p /tmp/fd-restore && tar -xzf /tmp/fd-backup.tar.gz -C /tmp/fd-restore
FLOWDROP_DATA_DIR=/tmp/fd-restore flowdrop doctor
```

实测：

```
Backup written: /tmp/fd-backup.tar.gz (2 KiB)
flowdrop.db
blobs/ef/efeef57682a4b7195014ec1a350ee974
...
database               ok   schema v1
admin password         ok   configured

# 恢复后的库与文件
inbox_items: 2, blobs: 1
# 用原密码登录恢复实例，下载附件：
3fd6e6be…f426e0  /tmp/restored.png
3fd6e6be…f426e0  /tmp/shot.png    # 与原文件一致
```

- 备份使用 `VACUUM INTO`，可在 Hub 运行中执行，不产生半写状态。
- 归档权限 `0600`。

---

## 7. 已知限制与未验证项

### 7.1 待真机验收（**未声称通过**）

| 项目 | 状态 |
|---|---|
| **Android 原生 App 构建与安装**（`android/`、debug APK） | **待 Android worker 交付 + 用户真机验收** |
| **真实 Wi-Fi 明文 HTTP**（`http://<Hub-IP>:8080`，Web + Android） | **待用户真机验收** |
| Android 手机 Chrome 实际访问（局域网 HTTPS + CA 导入，可选方案） | **待用户真机验收** |
| Android 手机 Chrome 实际访问（Tailscale Serve HTTPS，可选方案） | **待用户真机验收** |
| 手机切到移动网络后经 Tailscale 继续收发 | **待用户真机验收** |
| 真实 Wi-Fi 环境下的上传速度/大文件（>100 MiB）体验 | **待用户真机验收** |
| Android 系统对 APK 的安装授权流程（文件管理器/Chrome 下载） | **待用户真机验收** |
| iOS Safari、Firefox for Android、Windows 浏览器 | **未测试** |
| `systemd --user` 单元在本机实际 enable/linger | 单元文件已写并审阅，**未在用户环境安装**（避免改动宿主服务） |

### 7.2 功能限制（设计内）

- 主路径是明文 HTTP（用户接受的可信 LAN 边界）；同网段可观测内容与 Cookie。
- 非安全上下文下剪贴板/通知/Service Worker 可能不可用；复制降级为手动选择。
- 无自动过期（P1）；`expires_at` 字段保留但无自动删除逻辑。
- 无断点续传；单文件上限默认 512 MiB，可配置。
- 无 Linux 桌面壳、扫码配对、自动发现（明确不做）；Android 原生 App 为并行交付。
- 后台/移动端不保证常驻 WS 或系统通知；靠前台恢复时 REST 重拉。
- 单用户单 Hub；无多租户、无 E2EE。

### 7.3 已知技术债/风险

- `events.Broker` 缓冲 16，慢客户端丢事件而非断连（丢事件由重连/前台重拉兜底），无持久事件日志（符合 MVP 约束）。
- 限流为单进程内存固定窗口；多实例部署需外部限流。
- 上传超时 15 分钟（`SetReadDeadline`）；极慢链路上超大文件可能超时，需重传。
- 未做 SQLite `VACUUM`/定期 checkpoint 策略，长期运行文件可能增长（WAL 由 SQLite 管理）。

---

## 8. 复现步骤（最快路径）

```bash
cd /home/bob/vibe-code/flowdrop

# 1) 后端
cd server && go vet ./... && go test ./... -race -count=1

# 2) 前端
cd ../web && npx tsc -b --force && npx vite build

# 3) 构建单文件二进制
cd .. && scripts/build.sh 0.1.0

# 4) 端到端（真实二进制 + Chrome）
cd tests && npm install && npx playwright test

# 5) 手动跑一次（主路径：局域网明文 HTTP）
cd .. && FLOWDROP_DATA_DIR=/tmp/fd-demo ./dist/flowdrop admin init
FLOWDROP_DATA_DIR=/tmp/fd-demo \
FLOWDROP_LISTEN=$(ip -4 route get 1.1.1.1 | sed -n 's/.* src \([0-9.]*\).*/\1/p'):8080 \
FLOWDROP_COOKIE_SECURE=false ./dist/flowdrop serve
#   手机/电脑浏览器打开 http://<Hub-IP>:8080
#   或直接用辅助脚本： scripts/lan-http.sh
```

## 9. 下一阶段建议

1. Android worker 交付后：在本机跑 `cd android && ./gradlew assembleDebug`，把**实际结果**
   回填到第 10.5 节；再在真机安装、配置 `http://<Hub-IP>:8080`、登录并收发一次。
2. 用户按 `docs/DEPLOY.md §5` 在真实手机 + 真实 Wi-Fi 下走一遍明文 HTTP 主路径，
   重点看：登录会话是否保持、复制失败时手动选择是否可用、上传/下载是否正常。
3. 后端 worker 完成后补跑 `go vet ./... && go test ./... -race -count=1`，并把结果追加到本文件。
4. P1 优先项（按 PRD）：内容自动过期（7/30 天可配）、`flowdrop publish`。
3. 若多设备并发写增多，考虑把限流与会话清理下推到 SQLite 或加单实例锁。

---

## 10. 本轮验证（2026-11-07，HTTP LAN + Web 复制降级）

> 本节是**本轮**的唯一有效结果。第 1–6 节是 HTTPS 旧基线，**不得**当作本轮 HTTP 实测引用。
> 本节所有命令都在 `/home/bob/vibe-code/flowdrop` 执行；浏览器为 Google Chrome 153.0.8010.52。

### 10.1 前端类型检查与构建（通过）

```bash
cd web
npx tsc -b --force
npx vite build
```

实际输出：

```
TSC_EXIT=0                      # 严格模式（strict + noUncheckedIndexedAccess + exactOptionalPropertyTypes）

vite v6.4.5 building for production...
✓ 34 modules transformed.
../server/internal/webui/dist/index.html                   0.64 kB │ gzip:  0.37 kB
../server/internal/webui/dist/assets/index-CNn_45AM.css   10.12 kB │ gzip:  2.80 kB
../server/internal/webui/dist/assets/index-Ds6_JhPd.js   164.19 kB │ gzip: 53.92 kB
✓ built in 583ms
VITE_EXIT=0
```

结论：**通过**。`server/internal/webui/dist/` 是被本次 `vite build` 重新生成的嵌入产物。

### 10.2 生产构建（通过）

```bash
scripts/build.sh 0.1.0
```

```
构建完成： dist/flowdrop
  版本 0.1.0 (nogit) 2026-11-07T13:05:36Z
```

### 10.3 端到端测试（15 个全部通过）

```bash
cd tests && npx playwright test
```

```
Running 15 tests using 1 worker

  ✓   1 clipboard degradation › no Clipboard API and refused execCommand exposes manual copy, not a false success (784ms)
  ✓   2 clipboard degradation › SHA-256 copy also degrades to selectable text for attachments (534ms)
  ✓   3 clipboard degradation › denied clipboard permission falls back instead of reporting success (513ms)
  ✓   4 clipboard degradation › a working Clipboard API still reports success and shows no fallback (522ms)
  ✓   5 plain HTTP LAN origin › non-secure LAN origin offers manual copy and never throws (892ms)
  ✓   6–10 FlowDrop MVP end-to-end（未登录 / 双浏览器实时 / 链接纯文本 / 图片 / APK）
  ✓  11–14 多选文件 / 超限 / 断线重连 / 会话与注销
  ✓  15 content and session survive a Hub restart

  15 passed (11.7s)
```

**新增用例（`tests/e2e/clipboard.spec.ts`）做了什么**

| 用例 | 如何构造“剪贴板不可用” | 关键断言 |
|---|---|---|
| 无 Clipboard API + execCommand 失败 | `addInitScript` 遮蔽 `navigator.clipboard`，并让 `document.execCommand` 返回 `false` | **不出现**“已复制到剪贴板”；出现手动复制面板；`textarea` 值等于原文；选区为 `[0, len]`；`pageerror` 为空 |
| SHA-256 复制降级 | 同上，针对带附件的条目 | 展开的文本框值等于 64 位十六进制哈希；未提示成功 |
| 权限被拒 | Clipboard API 存在但 `writeText` 抛 `NotAllowedError`，`execCommand` 返回 `false` | 降级面板出现，提示含“拒绝”；未提示成功 |
| 正常路径（对照） | 用内存数组 stub 一个成功的 `writeText` | 提示“已复制到剪贴板”；**无**降级面板；写入值等于发送内容 |
| 真实非安全 LAN 源 | Hub 监听 `http://192.168.31.184:8096`（本机真实局域网 IP） | `window.isSecureContext === false`（浏览器自己确认）；HTTP 提示横幅可见；复制失败后展开手动选择文本；`pageerror` 为空 |

> 设计取舍：前四个用例**替换掉剪贴板表面**而不是依赖本机权限状态，避免“机器恰好允许/拒绝”
> 导致用例时好时坏；第五个用例用真实局域网 IP 证明“非安全上下文”这条分支确实被走到。

### 10.4 手动验证：明文 HTTP 局域网 Hub（本轮 + Hub worker 各自实测）

#### 10.4.1 本 worker 实测（Web 侧，端口 18090）

用真实二进制在本机真实局域网 IP 上跑明文模式：

```bash
FLOWDROP_DATA_DIR=/tmp/fd-http-manual ./dist/flowdrop admin init --password-stdin
FLOWDROP_DATA_DIR=/tmp/fd-http-manual \
FLOWDROP_LISTEN=192.168.31.184:18090 \
FLOWDROP_ALLOWED_HOSTS=192.168.31.184 \
FLOWDROP_COOKIE_SECURE=false \
./dist/flowdrop serve
```

> 注：当时显式写了 `FLOWDROP_ALLOWED_HOSTS=192.168.31.184`（新版本行为下监听 IP 会自动允许，
> 显式写也仍然有效）。当前文档已改为可省略它。

实际结果：

| 检查 | 命令 | 结果 |
|---|---|---|
| 健康检查 | `curl http://192.168.31.184:18090/api/v1/health` | `200` |
| 未认证拒绝 | `curl .../api/v1/items` | `401` |
| 安全响应头 | `curl -sI .../` | 有 `Content-Security-Policy` / `X-Content-Type-Options: nosniff` / `X-Frame-Options: DENY` |
| 无 HSTS（明文下正确） | 同上 | **没有** `Strict-Transport-Security` |
| 会话 Cookie | 带 `Origin: http://192.168.31.184:18090` 登录 | `flowdrop_session=...; HttpOnly; SameSite=Strict`（**无 `Secure`**，符合 `cookie_secure=false`） |
| CSRF Cookie | 同上 | `flowdrop_csrf=...; SameSite=Strict`（`HttpOnly` 缺省，符合双提交设计） |
| 启动日志提示 | `serve` 的 stderr | `plaintext HTTP LAN mode: passwords, session cookies and content are not encrypted on the wire` |
| 启动日志提示（通配地址） | `FLOWDROP_LISTEN=0.0.0.0:8080` + `cookie_secure=false` | **拒绝启动**：`listen must name the specific LAN IP when cookie_secure=false` |

补充验证 Host 白名单行为（`FLOWDROP_LISTEN=192.168.31.184:18094` + `cookie_secure=false`，
**不设置** `FLOWDROP_ALLOWED_HOSTS`；`curl --noproxy '*'`，避免本机 HTTP 代理干扰）：

| 检查 | 结果 |
|---|---|
| `GET /api/v1/health`（Host = 监听 IP） | `200` —— 具体监听 IP 自动允许 |
| `GET /api/v1/health`（`Host: evil.example`） | `421` + `{"error":{"code":"host_not_allowed",...}}` |

> 注意：本机 shell 里有 `http_proxy`/`https_proxy` 环境变量；若不显式 `--noproxy '*'`，
> 请求会被代理转发，导致伪造 Host 也返回 200 的**假结果**。上表结果为绕过代理后所得。

> `0.0.0.0` 在该模式下会被服务端拒绝（实测），文档已按此写。该约束由并行的 Hub
> worker 实现；本节只记录**实测到的行为**，不替其背书我未验证的后续改动。

另外对并行 worker 已放入仓库的两个辅助文件做了**最小实测**（不是我写的，未做完整审阅）：

| 检查 | 命令 | 结果 |
|---|---|---|
| `scripts/lan-http.sh --help` | 打印用法 | 正常 |
| 拒绝公网地址 | `scripts/lan-http.sh --host 8.8.8.8` | `拒绝启动：8.8.8.8 不是私有/局域网地址` |
| 拒绝非本机私有地址 | `scripts/lan-http.sh --host 10.99.99.99` | `不在本机任何网卡的 IPv4 地址上`，并列出 `lo` / `eno1` |

> 因此文档可以引用 `scripts/lan-http.sh` 与 `deploy/http-lan.env.example` 的存在与作用；
> 但它们的完整行为（如真实启动、与其他参数的交互）本轮未逐项验证。

#### 10.4.2 Hub worker 报告的结果（转述，非本 worker 复现）

负责 `server/` 与部署辅助的 worker 在同一台机器上报告了以下结果（**由它执行，本 worker 未
复跑这些项目**，因此不把它当作本轮的独立验证）：

- 后端 **73 个测试**通过（含 `-race`），包括新增的原生客户端 HTTP/LAN 契约测试
  （`server/internal/api/http_lan_test.go`：非回环 Host + 匹配 Origin、明文 Cookie 无 `Secure`）。
- 在本机实际局域网 IP `192.168.31.184:8095` 上完成登录、上传、下载、WS、注销。
- 显式约束：HTTP 模式必须绑定**具体本机私有 IP**；`cookie_secure=false` 时禁止
  `0.0.0.0` / `::` / 空 host。该约束本 worker 也独立验证过（见 10.4.1 末两行）。
- 具体监听 IP 会**自动**加入 Host allowlist。本 worker 既读了代码
  （`buildAllowedHosts` 对非 unspecified 的监听 IP 自动放行），也做了独立实测：
  Host = 监听 IP → `200`；`Host: evil.example` → `421 host_not_allowed`（见 10.4.1）。
  因此 `FLOWDROP_ALLOWED_HOSTS` 只需补 mDNS 名 / Tailscale 名等额外 Host。
- 明确**不能**据此声称手机真机或跨网通过；Android 仍在进行中。

### 10.5 本轮未验证 / 待整合

| 项目 | 状态 |
|---|---|
| Android 原生 App 构建（`./gradlew assembleDebug`）与 debug APK 产物 | **未验证**：`android/` 由并行 worker 交付，本轮未构建、未安装、未声称通过 |
| Android App 与 Web 的协议一致性（Cookie/CSRF/Origin/WS） | **未验证**：文档按设计描述，未做代码审阅或抓包 |
| 真实 Android 真机 + 真实 Wi-Fi 明文 HTTP 全流程 | **未验证** |
| 真实手机浏览器在明文 HTTP 下复制按钮的实际表现 | **未验证**（本机仅用 Chrome 模拟非安全上下文） |
| Firefox / Safari 的 `execCommand` 与剪贴板行为 | **未验证** |
| 跨网 Tailscale 路径在 HTTP 主路径下的回归 | **未验证**（旧 HTTPS 结果不适用） |
| Hub worker 后续新增的辅助文件/配置键 | 本轮未依赖其内部实现；文档只写已实测的 `FLOWDROP_LISTEN` / `FLOWDROP_ALLOWED_HOSTS` / `FLOWDROP_COOKIE_SECURE`，辅助文件仅引用存在性（见 10.4） |
| `go vet` / `go test`（后端） | 本 worker **未运行**；Hub worker 报告 73 个测试（含 `-race`）通过，属其自报，见 10.4.2 |

### 10.6 本轮改动清单（供复核）

- `web/src/clipboard.ts`（新增）：复制策略与失败原因；成功只在浏览器确认时返回。
- `web/src/ItemCard.tsx`：复制失败展开只读全选文本框；正文/链接/SHA-256 统一走新逻辑。
- `web/src/App.tsx`：非安全上下文时显示一次性 HTTP 能力提示。
- `web/src/Login.tsx`：登录页说明改为 HTTP LAN 主路径 + Tailscale 跨网，去掉“请通过 HTTPS 访问”。
- `web/src/styles.css`：`.manual-copy` 与 `.banner.notice` 样式。
- `tests/e2e/clipboard.spec.ts`（新增）：5 个用例，覆盖无 API / 拒绝权限 / 成功对照 / 真实非安全 LAN 源。
- `server/internal/webui/dist/*`：`vite build` 重新生成的嵌入产物（运行 Vite 必须更新）。
- `README.md`、`docs/ARCHITECTURE.md`、`docs/PLAN.md`、`docs/DEPLOY.md`、`docs/PRD.md`（追加 §16）、本文件。
- **未修改**：`server/` 源码、`android/`、`deploy/`、`scripts/`、`flowdrop-prd.md`；未执行任何
  `git add/commit/reset/checkout`。`deploy/http-lan.env.example`、`scripts/lan-http.sh`、
  `server/internal/api/http_lan_test.go` 等均由并行 Hub worker 添加，本轮只读取与引用。


## 11. Android 交付与协调者集成核对

此节补充 §10.5 的历史待整合状态；不将模拟器测试称作真机验收。

- Android worker 已交付 `android/`，报告 `:app:assembleDebug` 成功、`lintDebug` 0 error / 19 warning。
- 协调者直接核对 APK 文件存在，约 9.4 MiB：
  `android/app/build/outputs/apk/debug/app-debug.apk`。
- 协调者执行 `sha256sum`，与 worker 报告一致：
  `681d4d2a622cd5a80f00b296f2ad0ee988ea6960ba7eaf1e0fc860ea73f705ad`。
- 协调者读取 `app/build/test-results/testDebugUnitTest/TEST-*.xml`：29 个 JVM 测试，0 失败、0 错误、0 跳过。
- 协调者读取 `app/build/outputs/androidTest-results/connected/debug/TEST-*.xml`：32 个设备测试，
  0 失败、0 错误、2 跳过。worker 已更正此前“34 项通过”的错误：总数为 32，最后无参数运行跳过 2 项。
  带 Hub 参数的早一轮为 32 项全部执行成功；Gradle 报告会被后续运行覆盖。
  协调者核对 `~/.gradle/daemon/8.11.1/daemon-2346191.out.log` 第 6406–6414 行，
  确有 Hub 参数、Starting/Finished 32 tests 和 BUILD SUCCESSFUL，未见 FAILED/SKIPPED。
  全部设备测试均在 API 36 模拟器执行，真实 Hub 指服务端真实进程，不指手机真机。
- worker 另报告 API 36 模拟器下 HTTP 登录、分享图片、APK 系统安装入口、会话隔离及 200 MB
  文件上传下载摘要一致、内存增量约 5 MB。这些为 worker 的手工验证报告，协调者未重新执行。
- 功能与说明见 `android/README.md`。本轮只整合交付记录，未重复运行已报告通过的检查。

**仍待验收**：真实 Android 手机、真实 Wi-Fi、移动网络下 Tailscale HTTP、实际 APK 安装完成。
Hub 单次运行绑定一个具体 IP；局域网 IP 与 Tailscale IP 的切换方式须按部署配置使用，
本轮不承诺无感网络切换。


### 11.1 Tailscale HTTP 与单 IP 绑定限制

Android worker 已补充 `android/README.md §6.1.1`：可用 `tailscale ip -4` 获取 Hub 的
Tailscale IP，再运行 `scripts/lan-http.sh --host <Tailscale-IP>`，手机使用对应 HTTP 地址，
无需 Serve 或证书。该配置的本机健康检查已由 worker 验证，跨网手机仍待验收。

当前单进程仅监听一个具体 IP：绑定 LAN IP 时不能直接通过 Tailscale IP 访问，反之亦然。
因此两种直连方式目前需重启并切换绑定配置；不支持单个 Hub 同时在两个接口提供 HTTP。
这是当前交付限制，不应被描述为无感网络切换，也不建议用共享数据目录的两个 Hub 进程绕过。

## 12. 本轮验证：局域网自动发现 + Tailscale 绑定说明（2026-11-07）

本节只记录**本轮**（M5，mDNS/DNS-SD 发现）实际执行的检查。第 1–11 节的历史结果原样保留；
不得把旧 HTTPS 结果、旧模拟器结果或普通 HTTP 健康检查当作发现功能已通过的证据。

执行环境（与历史记录同一台机器）：

| 项目 | 值 |
|---|---|
| 主机 | `bob-hpelitedesk880g6`（Ubuntu 26.04 / Kubuntu） |
| 网络 | `eno1` = `192.168.31.184/24`；`tailscale0` = `100.125.168.63/32` |
| 系统 mDNS | **Avahi daemon 正在运行**；另有 `adb`、`kdeconnectd` 监听 UDP 5353 |
| 工具 | Go、`dig`、Python dbus/GLib；本机**未安装** `avahi-browse` CLI |
| 验证用二进制 | `/tmp/flowdrop-discovery`（临时构建，**不是** `dist/flowdrop`） |
| 执行日期 | 2026-11-07 |

### 12.1 自动化测试（模拟/单元，非真实网络发现）

```bash
cd server
go vet ./...
go test ./... -count=1 -race
go test ./... -count=1 -v    # 统计 --- PASS
```

实际结果：

- `go vet ./...`：无输出（通过）。
- `go test ./... -count=1 -race`：全部 `ok`，无 race 报告。
- `-v` 统计：**84 个 `--- PASS`**，0 FAIL。
- 既有 API/auth/storage/store 用例继续通过；未新增或修改 API 端点。

新增发现相关用例（`server/internal/discovery`，均为合成网卡/fake publisher，不碰真实网络）：

- `TestEvaluateOnlyAdvertisesExplicitRFC1918PlainHTTP`：回环、通配、空 host、Tailscale
  `100.64/10`、链路本地、公网、IPv6、非 IP、`cookie_secure=true`、地址不在本机、
  网卡 Down/无多播均拒绝；合格 192.168 地址通过。
- `TestEvaluateAcceptsAllRFC1918Blocks`：10/8、172.16/12、192.168/16 通过。
- `TestSanitizeNickname`：安全 ASCII、域名后缀、空/Unicode/特殊字符、截断。
- `TestInstanceNameTruncationAndConflicts`：63 字节 label 上限、大小写不敏感冲突后缀。
- `TestStartPublishesFrozenRecordAndWithdraws`：实例名、SRV target、端口、绑定 IP/网卡、
  TXT 精确值、`Close()` 幂等且只注销一次。
- `TestStartDoesNotPublishWhenDisabledOrIneligible`：关闭/回环/Tailscale/HTTPS 时不注册。
- `TestStartKeepsPublishingWhenConflictScanFails`：冲突扫描失败不影响发布。
- `TestStartReturnsPublishError`：注册失败向上返回，由 main 记日志后继续 HTTP。
- `TestFrozenProtocolConstants`：`_flowdrop._tcp.local.` 与 TXT 常量冻结。
- `TestResponderRecordsMatchFrozenProtocol`：受控 responder 的 PTR/SRV/TXT/A 记录、
  端口、TTL=120 与 cache-flush 位。
- `server/internal/config`：`discovery_enabled` 默认 nil（自动）、JSON false、env true/false/0、
  非法布尔值报错。

这些用例证明**配置判定、绑定范围和生命周期逻辑**，**不是**真实 mDNS 网络验证。

### 12.2 构建与二进制状态

```bash
cd server
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=0.1.0-discovery-test" \
  -o /tmp/flowdrop-discovery ./cmd/flowdrop
```

- 临时二进制用于本机实测；`server/cmd/mdnsprobe` 为独立协议探针工具。
- **`dist/flowdrop` 未更新**（仍是本轮前 2026-11-07 21:17 的 11,030,690 字节产物）。
  原因：本轮写入范围不含 `dist/` 与嵌入前端产物；`scripts/build.sh` 会重建 `web/` 和
  `server/internal/webui/dist`，两者本轮禁止修改。需要正式产物时由协调者按构建流程统一重建。
- 未执行任何 `git add/commit/reset/checkout`。

### 12.3 真实发布：独立 Avahi 浏览器 + 原始 DNS 协议探针（实际执行）

启动（真实网卡 `eno1`，真实 Avahi 同时运行）：

```bash
FLOWDROP_DATA_DIR=/tmp/fd-mdns/probe-data \
FLOWDROP_LISTEN=192.168.31.184:18097 \
FLOWDROP_COOKIE_SECURE=false \
/tmp/flowdrop-discovery serve -config /tmp/fd-mdns/no-config.json
```

Hub 日志（实际输出）：

```text
level=INFO msg="mDNS discovery published" service=_flowdrop._tcp.local.
  instance=FlowDrop-bob-hpelitedesk880g6 host=flowdrop-bob-hpelitedesk880g6.local.
  address=192.168.31.184 port=18097 interface=eno1 txt="[v=1 scheme=http path=/]"
level=INFO msg=listening addr=192.168.31.184:18097
```

**独立协议探针**（仓库内 `server/cmd/mdnsprobe`，直接用 miekg/dns 收发原始 mDNS，
不经过 Hub 的发布器代码）：

```bash
cd server && go run ./cmd/mdnsprobe -iface eno1 -timeout 4s
```

实际输出：

```text
from=192.168.31.184:5353 PTR _flowdrop._tcp.local. -> FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. ttl=120
from=192.168.31.184:5353 SRV FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. port=18097 target=flowdrop-bob-hpelitedesk880g6.local. ttl=120
from=192.168.31.184:5353 TXT FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. ["v=1" "scheme=http" "path=/"] ttl=120
from=192.168.31.184:5353 A flowdrop-bob-hpelitedesk880g6.local. 192.168.31.184 ttl=120
```

核对结果：服务类型、实例名、真实端口 18097、SRV target、TXT 三项、A 地址 192.168.31.184
均正确；记录来源是 `eno1` 的绑定地址，TTL=120。

**网卡范围复核**（同一 Hub 运行期间，用原始探针从其它接口查询）：

```text
/tmp/fd-mdns/mdnsprobe -iface tailscale0 -timeout 3s
NO_RECORDS service=_flowdrop._tcp.local.     # rc=1

/tmp/fd-mdns/mdnsprobe -iface docker0 -timeout 3s
NO_RECORDS service=_flowdrop._tcp.local.     # rc=1
```

只有 `eno1` 能收到记录，Tailscale 与 Docker 接口都收不到响应。实现上发布器会校验
`ipv4.ControlMessage.IfIndex` 并丢弃其它网卡的查询；这不是仅靠“公告时指定 ifIndex”的
弱约束，因此也避免了上游 zeroconf responder 在多网卡主机上“谁问都答”的问题。

**独立 Avahi 浏览器**（系统 Avahi D-Bus `ServiceBrowserNew` + `ResolveService`，
不是 Hub 代码）：

```bash
python3 /tmp/fd-mdns/avahi_browse.py   # 5 秒浏览 _flowdrop._tcp.local
```

实际输出（同一记录被浏览信号重复回调，去重后只有一项）：

```text
ITEM name=FlowDrop-bob-hpelitedesk880g6 type=_flowdrop._tcp domain=local
  SRV target=flowdrop-bob-hpelitedesk880g6.local address=192.168.31.184 port=18097
  TXT ['path=/', 'scheme=http', 'v=1']
```

说明：Avahi 与应用同时运行、UDP 5353 上还有 adb/kdeconnectd 的情况下，本机仍能发布并
被独立浏览器发现；这是**真实网络发布**，不是模拟。

普通健康检查（`curl http://192.168.31.184:18097/api/v1/health` → 200）只用于确认 Hub
仍在正常收发，**不作为发现成功证据**。

### 12.4 关闭 / 跳过广播（实际执行）

| 场景 | 启动配置 | Hub 日志 | 独立探针结果 |
|---|---|---|---|
| 显式关闭 | `FLOWDROP_DISCOVERY_ENABLED=false`，`192.168.31.184:18098`，HTTP | `mDNS LAN discovery disabled by configuration` | `NO_RECORDS`，rc=1 |
| Tailscale 绑定 | `100.125.168.63:18099`，HTTP，未显式关闭 | `... Tailscale/CGNAT; Tailscale access is not advertised as a LAN entry` | `eno1` 与 `tailscale0` 均 `NO_RECORDS`，rc=1 |
| HTTPS 模式 | `192.168.31.184:18100`，`cookie_secure=true` | `... cookie_secure=true: discovery only publishes a directly usable plaintext HTTP LAN entry` | `NO_RECORDS`，rc=1 |

三种场景下 Hub 的 HTTP 健康检查均为 200，证明“不发布”不会破坏基本收发；Tailscale
地址本身可正常 HTTP 服务，只是不被广播为 LAN 入口。

### 12.5 生命周期与注销（实际执行）

发送 `SIGTERM` 后 Hub 日志：

```text
level=INFO msg="shutting down"
level=INFO msg="mDNS discovery withdrawn" instance=FlowDrop-bob-hpelitedesk880g6 service=_flowdrop._tcp.local.
```

- 退出后原始探针立即返回 `NO_RECORDS`（没有活着应答者）。
- 在 Hub 停止前后用探针抓包，实际观察到 SRV/TXT/PTR/A 四条 **TTL=0** 注销记录，
  随后进程退出；说明优雅关闭会注销服务并释放 socket。实际抓到的 goodbye：

```text
PTR _flowdrop._tcp.local. -> FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. ttl=0
SRV FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. port=18097 target=... ttl=0
TXT FlowDrop-bob-hpelitedesk880g6._flowdrop._tcp.local. ["v=1" "scheme=http" "path=/"] ttl=0
A flowdrop-bob-hpelitedesk880g6.local. 192.168.31.184 ttl=0
```
- **限制（如实记录）**：系统 Avahi 的 ServiceBrowser 在 Hub 退出后仍会返回旧缓存条目
  （等待其缓存清理/TTL）。即使额外手工发送 TTL=0 注销包，本机 Avahi 缓存也未立即清空。
  因此“关闭不发布”的结论以**无 Hub 进程时的原始探针无响应**为准；不声称所有客户端缓存会
  瞬间消失。记录 TTL 设为 120 秒，异常崩溃后最迟约 2 分钟过期。

### 12.6 脚本与 doctor（实际执行）

`scripts/lan-http.sh --help` 已列出 `--no-discovery`。实际启动：

```bash
FLOWDROP_DATA_DIR=/tmp/fd-mdns/script-data \
timeout --signal=TERM 6 scripts/lan-http.sh \
  --binary /tmp/flowdrop-discovery --no-discovery --port 18101
```

输出含：

```text
LAN 自动发现：    关闭（--no-discovery）
level=INFO msg="mDNS LAN discovery disabled by configuration"
level=INFO msg=listening addr=192.168.31.184:18101
```

同一脚本不加 `--no-discovery`（端口 18102）实际输出：

```text
LAN 自动发现：    默认开启（仅符合条件的 RFC1918 地址发布）
level=INFO msg="mDNS discovery published" ... address=192.168.31.184 port=18102 ...
level=INFO msg=listening addr=192.168.31.184:18102
level=INFO msg="mDNS discovery withdrawn" ...
```

`flowdrop doctor` 实跑（临时二进制、临时数据目录）：

```text
# 192.168.31.184:18097 + cookie_secure=false
mdns discovery         ok   _flowdrop._tcp.local. on 192.168.31.184:18097 via eno1

# 100.125.168.63:18099
mdns discovery         ok   off: listen address is not eligible for LAN discovery:
                           address 100.125.168.63 is Tailscale/CGNAT; Tailscale access is not advertised as a LAN entry

# FLOWDROP_DISCOVERY_ENABLED=false
mdns discovery         ok   off: discovery_enabled=false

# 默认回环 127.0.0.1:8080
mdns discovery         ok   off: listen address is not eligible for LAN discovery:
                           listen address is loopback; there is no LAN entry to advertise
```

### 12.7 未验证 / 限制（不得声称通过）

| 项目 | 状态 |
|---|---|
| Android 客户端自动发现 UI（按冻结协议浏览/解析/展示） | 已由并行 worker 交付（自报，见 §12.9）；本 worker **未复跑 Android 测试、未真机验证** |
| 真实 Android 手机 + 真实 Wi-Fi 跨设备自动发现 | **未验证** |
| 跨网 / 移动网络下 Tailscale 直连与发现跳过 | **仅本机绑定 Tailscale IP 验证跳过广播**；真实手机跨网未验证 |
| Wi-Fi AP 客户端隔离、防火墙对 UDP 5353 的拦截 | **未验证**；若 AP 隔离多播，发现会失败，需手动填 IP |
| 同一 LAN 两台 Hub 的名称冲突自动改后缀 | **仅用 fake existing 名单模拟**；未在真实第二台 Hub 上验证 |
| `avahi-browse` CLI | 未安装；改用系统 Avahi D-Bus 浏览器 + 仓库内原始探针 |
| Avahi 缓存即时清除 | **未成立**：观察到退出后缓存仍短暂/较久返回旧条目，见 §12.5 |
| `dist/flowdrop` 是否含发现功能 | **协调者已重建**，含发现功能；见文末集成补记 |
| 真实手机浏览器 / Android 原生 App 的发现成功率 | **未验证**，不得以本机探针代替；详见 §12.9 |
| Android 设备测试中的 LiveHub 发现 | 41 个设备测试中 2 个 LiveHub 用例因无参数跳过；模拟器 20 秒无 `onServiceFound` |

### 12.8 本轮改动文件（供复核）

- `server/internal/discovery/discovery.go`（新增）：地址/网卡判定、实例名、mDNS 发布与 Lifecycle。
- `server/internal/discovery/responder.go`（新增）：受控 mDNS responder，按接口索引过滤查询，
  发送公告/TTL=0 注销并释放 socket。
- `server/internal/discovery/discovery_test.go`（新增）：配置范围、绑定范围、冻结值、生命周期。
- `server/cmd/mdnsprobe/main.go`（新增）：独立原始 DNS 协议探针（仅验证工具）。
- `server/cmd/flowdrop/main.go`：启动/退出接入发现；doctor 增加 `mdns discovery` 行。
- `server/internal/config/config.go`、`config_test.go`：新增 `discovery_enabled` /
  `FLOWDROP_DISCOVERY_ENABLED`（默认自动、显式关闭）。
- `server/go.mod`、`server/go.sum`：新增纯 Go mDNS 依赖（`grandcat/zeroconf`）及直接使用的
  DNS 编解码依赖（`miekg/dns`）。
- `scripts/lan-http.sh`：新增 `--no-discovery` 并说明默认发现行为（仅发现配置必要改动）。
- `README.md`、`docs/DEPLOY.md`、`docs/ARCHITECTURE.md`、`docs/PLAN.md`、`docs/PRD.md`（追加 §17）、
  本文件：发现协议、启用条件、Tailscale 绑定步骤、验证记录。
- **未修改**：`android/`、`web/`、`server/internal/webui/dist/`、`deploy/`、`tests/`、`dist/`；
  未执行任何 git 写操作。


### 12.9 Android 发现交付摘要（并行 worker 自报，本轮未复跑）

> 以下由协调者转述 Android discovery worker 的交付；本 worker 只修改 Hub 文档，未改
> `android/`、未重新构建或运行 Android 测试。不得把它当作本 worker 的真机验收。

- worker 仅修改 `android/`，APK：
  `android/app/build/outputs/apk/debug/app-debug.apk`，SHA-256
  `f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8`。
- 测试：53 个 JVM 测试全部通过；41 个设备测试中 39 个执行通过、2 个 LiveHub 因无参数跳过；
  lint 与 assemble 通过。
- 协议：按冻结的 `_flowdrop._tcp` + TXT `v=1,scheme=http,path=/` 实现。
- 交互：登录页做 12 秒自动搜索 + 手动刷新停止；选中服务只填入地址，不发送凭据；
  前后台切换会清理浏览。
- **边界**：实际 Hub 在 `192.168.31.184:18099` 广播，但 API 36 模拟器 20 秒内没有
  `onServiceFound`；真实手机 mDNS 互通仍未验证。模拟器注入测试**不能**写成真实局域网发现通过。
- Hub 侧本轮独立验证见 §12.3：`eno1` 上的原始探针与 Avahi 浏览器均能发现并解析该服务；
  这不代替 Android 真机/模拟器的发现结果。


### 最终集成补记：发现功能正式二进制已重建

协调者在两个发现 worker 完成后执行 `scripts/build.sh 0.1.0` 成功，
包含前端 TypeScript 检查、Vite 生产构建、Go 二进制构建。
最终产物 `dist/flowdrop` 构建时间为 `2026-11-07T15:41:05Z`，已包含 Hub 自动发现功能。
此结果取代 §12.2 中 worker 完成时“dist 尚未重建”的历史状态。
另已执行 `scripts/lan-http.sh --help` 确认新 `--no-discovery` 选项可用。

Android APK 已由协调者核对摘要，与 discovery worker 报告一致：
`f134f2629e95634afd9445989c736ee8bfa0b2f00fa67a21d08a8796ebcbe7c8`。
使用时重启旧 Hub 并安装新版 APK；手机真实 Wi-Fi 发现仍待验收。

---

## 13. 本轮验证：Linux 桌面 App（Wails v3，2026-11-08）

> 历史记录：§1–§12 是 Hub / Web / Android 的既有记录，未因本轮改动而失效。
> 本轮只新增 `desktop/`、`scripts/build-desktop.sh`、`scripts/install-desktop.sh`、
> `scripts/desktop-dev-sysroot.sh`、`deploy/desktop/`，未改 `server/`、`android/`
> 或既有脚本；`server/go.mod` 中没有 Wails 依赖（已核对）。

### 13.1 环境事实（先核实，不靠记忆）

| 项目 | 实测值 |
|---|---|
| OS | Ubuntu 26.04.1 LTS（KDE Plasma 6，Wayland 会话 `XDG_CURRENT_DESKTOP=KDE`） |
| Go | go1.26.8 linux/amd64 |
| Node / npm | v24.16.0 / 12.2.0 |
| Wails | **v3.1.0**（`go list -m -versions github.com/wailsapp/wails/v3` 的稳定版终点；v3.2.0 只是 nightly） |
| GTK/WebKit 运行时 | `libgtk-3-0t64 3.24.52`、`libwebkit2gtk-4.1-0 2.52.6`、`libsoup-3.0-0`、`libjavascriptcoregtk-4.1-0` |
| GTK/WebKit 开发包 | **系统未安装**（`pkg-config gtk+-3.0 webkit2gtk-4.1` 均缺失） |
| 系统托盘 | `org.kde.StatusNotifierWatcher` 由 kded6 持有（`busctl --user list` 实测） |

因为系统编译依赖缺失且**不允许 sudo / 改动宿主全局包**，构建使用**用户级 sysroot**：

```
~/.local/share/flowdrop-desktop/sysroot     # 99 个 -dev .deb 解包（仅用户目录）
```

`scripts/desktop-dev-sysroot.sh` 用 `apt-get --print-uris` 解析包列表后 `curl` + `dpkg-deb -x`
解包到该目录，并把开发用的 `.so` 符号链接指向系统已安装的运行时库；**不安装软件包、
不改 /usr、不碰系统服务/信任库/防火墙**。`scripts/build-desktop.sh` 自动优先使用系统
pkg-config，缺失时才回退到该 sysroot。

> 说明：Wails v3 默认 Linux 后端是 GTK4 + WebKitGTK **6.0**（`gtk4`/`webkitgtk-6.0`），
> 本机只有 WebKitGTK **4.1** 运行时，因此固定 `-tags gtk3`（`gtk+-3.0` + `webkit2gtk-4.1`）。
> 这是依赖现实，不是随意选择。

### 13.2 构建与产物

```text
$ scripts/build-desktop.sh
==> building web frontend
✓ built in 540ms
==> using user-local sysroot: /home/bob/.local/share/flowdrop-desktop/sysroot
==> building desktop binary (release, tags: gtk3 production)
built: /home/bob/vibe-code/flowdrop/dist/flowdrop-desktop
```

| 项目 | 值 |
|---|---|
| 产物 | `dist/flowdrop-desktop`（16 905 160 B） |
| SHA-256 | `1cd70e46e7da97eabd5d1bbce7faedba25af7e89e6904af164c61632986fee3e` |
| 版本 | `FLOWDROP_VERSION=0.1.0-desktop`（Hub 日志 `version=0.1.0-desktop` 已验证注入生效） |
| 运行时依赖解析 | `ldd` 无 `not found`；命中 `libwebkit2gtk-4.1.so.0`、`libgtk-3.so.0`、`libsoup-3.0.so.0`、`libjavascriptcoregtk-4.1.so.0` |

### 13.3 Go 测试（桌面模块）

```text
$ cd desktop && go vet -tags gtk3 ./...          # 无输出
$ cd desktop && go test -tags gtk3 -count=1 ./...
ok  flowdrop/desktop
ok  flowdrop/desktop/internal/appsettings
ok  flowdrop/desktop/internal/hubmgr
ok  flowdrop/desktop/internal/network
```

**43 个测试（含子测试）全部 PASS，0 FAIL**。其中 `internal/hubmgr` 直接驱动**真实 Hub**：

| 测试 | 覆盖内容 |
|---|---|
| `TestLifecycleAndFirstAdmin` | `Start`→`HasAdmin=false`→`InitializeAdmin`→`HasAdmin=true`；短密码被拒；**第二次初始化返回 `hub.ErrAdminExists`**且旧密码仍可登录、被拒密码不可登录；`Stop` 后 `URL()==""`、`HasAdmin` 报错 |
| `TestAddressSwitchKeepsData` | 登录+建条目→`Restart` 到新端口（同一 DataDir）→新 URL 不同→**密码与条目仍在** |
| `TestStopThenStartReusesManager` | `Stop` 后再 `Start` 可复用同一 Manager（设置窗口回退路径依赖） |
| `TestRestartReleasesOldPort` | `Restart` 后旧端口 TCP 连接失败（监听确实释放） |
| `TestStartPortConflictIsFriendly` | 同端口第二个 Hub 启动失败；`FriendlyError` 含「端口已被占用」与「不会结束」 |
| `TestDataDirLockIsFriendly` | 同数据目录第二个 Hub 返回 `hub.ErrDataDirInUse`；提示含「数据目录已被另一个 FlowDrop 使用」「不会结束它」 |
| `TestStatusReportsAdminAndURL` | 设置窗口状态与真实 Hub 一致 |
| `TestDesktopFlowEndToEnd` | 登录→`flowdrop_session` Cookie 存在→**无 CSRF 的 POST 返回 403**→WS `hello`→建文字→WS 收到 `item.created`→multipart 上传→鉴权下载字节一致且 `Content-Disposition: attachment`→**匿名下载 401** |

另有：`TestServiceApplyNetworkSwitchesAndPersists`（切换后写回 `bind_address`/`port`）、
`TestServiceApplyNetworkRejectsWildcardAndUnknownIP`（wildcard/不存在 IP 被拒且 Hub URL 不变）、
`TestServiceInitializeAdminCannotOverwrite`、`TestSettingsFileHasNoPasswordField`
（配置文件不含 password/token/secret 字样）、`TestProxyWarning*`（系统代理检测与豁免判断）、
`TestDefaultBindPrefersLANThenTailscaleThenOther`、`TestClassify*`、`TestIsLAN` 等。

### 13.4 安全边界：管理 bridge 对 LAN 页面不可达（真实 WebKit 探针）

在**真实 Wails v3.1.0 GTK3 窗口**里，让 Hub 来源的页面尝试调用绑定（临时探针程序，
与桌面版同一运行时）：

| 尝试 | 结果 |
|---|---|
| `import('/wails/runtime.js')` | **失败**（请求落到 Hub origin，404/非 JS） |
| `fetch('wails://localhost/wails/runtime.js')` | `Load failed` |
| `import('wails://localhost/wails/runtime.js')` 后 `Call.ByName('main.Svc.Ping')` | 调用发往 `location.origin`（Hub），返回 Hub 的响应，**原生方法未执行**（临时探针在方法里 `log.Println` 无输出） |
| `fetch('wails://localhost/wails/runtime', {mode:'no-cors',method:'POST',body:…})` | `status=0 type=opaque`，**原生方法仍未执行** |
| 表单 `POST` 到 `wails://localhost/wails/runtime` | 未触发原生调用（无 `NATIVE` 日志） |
| 同页 `RawMessageHandler` 窄通道 | 正常收到 `origin="http://127.0.0.1:18099/"`（这是唯一开放的原生动作，仅用于开外链） |

对照：设置窗口（`wails://localhost/settings.html`）`Call.ByName("main.Svc.Ping")` →
`pong-from-go`，绑定可用。**结论：Hub 页面拿不到管理桥。**

### 13.5 真实 GUI 验证

#### A. KDE Plasma 6（Wayland，`DISPLAY=:0`，真实桌面会话）

- 启动 `dist/flowdrop-desktop`（测试配置：`192.168.31.184:18081`）：
  窗口出现（`xwininfo`：`"FlowDrop 流转" 980x780`），Hub 日志
  `listening url=http://192.168.31.184:18081`，`mDNS discovery published … address=192.168.31.184 port=18081`。
  WebView 实际请求 `/`、`/assets/index-*.{js,css}`、`/api/v1/auth/session` 均 200，
  截图显示**真实 React 登录页**（不是空壳）。
- 系统托盘：`busctl --user list` 出现
  `org.kde.StatusNotifierItem-<pid>-1 … flowdrop-deskto`，即 StatusNotifierItem 已注册。
- 端口被占用场景：预先占用 18082 后启动 → 日志
  `hub start failed: … bind: address already in use`，应用**改为显示设置窗口**，
  横幅显示「端口已被占用…本应用不会结束其他进程」。**没有伪造可用状态**。
- 数据目录被 CLI Hub 占用：CLI 以同一 `data_dir` 运行时启动桌面版 →
  `hub start failed: data directory is in use by another FlowDrop Hub`，
  设置窗口横幅「数据目录已被另一个 FlowDrop 使用…本应用不会结束它」；
  CLI 进程未被终止（继续监听 18091）。

> 键盘输入限制：该 Wayland 会话下 `xdotool` 的 XTEST 按键**连普通 GTK Entry 都收不到**
> （用 `zenity` 等价的最小 GTK3 程序实测同现象），因此完整的点击/输入流程改在本机
> **独立 Xvfb** 显示上完成（真实 Wails 窗口 + 真实 WebKitGTK，不是 Chromium）。

#### B. 独立 Xvfb（`DISPLAY=:99`，真实 Wails 窗口）

以下全部通过真实鼠标/键盘事件驱动，截图逐项核对：

| 场景 | 结果 |
|---|---|
| 首次启动 | Hub 起在 `127.0.0.1:18093`；设置窗口自动出现并聚焦，状态「运行中」 |
| 设置窗口渲染 | 显示真实网卡：`局域网（LAN）· eno1 192.168.31.184`（推荐）、`Tailscale · tailscale0 100.125.168.63`、`docker0 172.17.0.1`、`br-… 172.18.0.1`、`本机回环 127.0.0.1` |
| **首次管理员密码（GUI）** | 输入两次 → 状态变「已设置（密码登录仍由 Hub 校验）」；Hub 日志 `admin user initialized username=admin` |
| 收件箱登录 | 用同一密码登录 → 显示「实时同步中」（WebSocket 建立；Hub 日志 `/api/v1/events status=101`） |
| 发送文字 | GUI 输入并发送 → 条目出现（`文字 · Linux · 时间`） |
| 发送链接 | GUI 发送 `https://example.com/…` → 「链接」条目 + 「打开链接」按钮 |
| **外链走系统浏览器** | 点击「打开链接」→ 桩 `xdg-open` 记录 `XDG_OPEN https://example.com/flowdrop-desktop-test`；应用窗口**没有**跳转到外站（仍是 Hub origin） |
| **文件上传** | 「选择文件」打开 GTK 原生文件对话框 → 选 `desktop-upload.txt` → 条目出现（32 B） |
| **图片上传 + 内联预览** | 选 `test-image.png` → 「图片」条目 + 缩略图渲染 |
| **鉴权下载** | 点「下载」→ 文件出现在 `~/Downloads/test-image.png`，`cmp` 与源文件**字节一致** |
| **复制到剪贴板** | 点「复制」→ `xclip -selection clipboard -o` 读回 `https://example.com/flowdrop-desktop-test` |
| 粘贴文字 | `Ctrl+V` 把系统剪贴板文字粘进输入框（截图确认文本出现） |
| **设置切换地址** | 选中 LAN + 端口 18085 → 点「保存并重启 Hub」→ 日志 `hub stopped` → `listening url=http://192.168.31.184:18085` + mDNS 重新发布；设置窗口 URL 与「实际绑定」同步更新；`desktop.json` 写回 `{"bind_address":"192.168.31.184","port":18085}` |
| 失败地址回退 | 端口被占用时 `ApplyNetwork` 报错，自动尝试恢复原地址（服务层测试覆盖） |
| **关闭窗口 → 托盘** | 发送 `WM_DELETE_WINDOW` → 窗口 `Map State: IsUnMapped`，**进程仍在、Hub 仍监听 18083** |
| **托盘恢复窗口** | 通过 D-Bus DBusMenu `Event(id=1,"clicked")`（菜单项「显示收件箱」）→ 窗口 `Map State: IsViewable` |
| **托盘明确退出** | DBusMenu `Event(id=5)`（「退出 FlowDrop（停止 Hub）」）→ 日志 `mDNS discovery withdrawn` + `hub stopped`，**端口 18085 释放**，进程退出 |
| 托盘菜单结构 | `com.canonical.dbusmenu.GetLayout` 返回 5 项：显示收件箱 / 设置… / 在浏览器中打开 / 分隔符 / 退出 FlowDrop（停止 Hub） |
| **无托盘回退** | 用 `dbus-run-session` 起一个**没有 StatusNotifierWatcher** 的会话 → 日志「running without a tray; closing the window will ask to quit」；关闭窗口弹出 GTK 原生确认框「当前没有可用的系统托盘，关闭窗口将退出 FlowDrop 并停止 Hub。」；**取消 → 窗口保留**（`IsViewable`，Hub 继续跑）；**确认 → 进程退出、端口释放** |
| 设置「在浏览器打开」 | 桩 `xdg-open` 记录 `XDG_OPEN http://127.0.0.1:18094/` |

截图证据文件（本次会话本机）：

```
/tmp/desktop-e2e/screen1.png          真实 KDE 会话中的收件箱（React 登录页）
/tmp/desktop-e2e/settings1.png        端口占用时的设置窗口（错误横幅）
/tmp/desktop-e2e/settings3.png        首次运行：运行状态 + 首次密码 + 网卡列表
/tmp/desktop-e2e/final_settings.png   release 构建的设置窗口
/tmp/desktop-e2e/inbox5.png           Xvfb 下登录后的收件箱
/tmp/desktop-e2e/final_image_crop.png 图片条目 + 内联预览
/tmp/desktop-e2e/notray1_crop.png     无托盘时的「确认退出」对话框
/tmp/desktop-e2e/proxywarn_crop.png   系统代理提示横幅
```

### 13.6 剪贴板 / 图片 / 拖拽的实测结论

| 能力 | 结论 |
|---|---|
| 从 WebView **写**剪贴板 | 可用：`navigator.clipboard.writeText` 成功，系统剪贴板读回一致（`xclip` 验证） |
| 从系统剪贴板**粘贴文字** | 可用：`Ctrl+V` 后文本进入输入框 |
| 从系统剪贴板**粘贴图片** | **未生效**：WebKitGTK 下 `paste` 事件的 `clipboardData.files/types` 为空（用最小探针页实测 `{"types":[],"files":0,"items":[]}`）；异步 `navigator.clipboard.read()` 返回 `NotAllowedError`。这是 WebKitGTK/无权限提示后端的限制，**不是**本应用绕过成功的功能 |
| 从文件管理器**拖拽文件** | **未验证**：Wails 默认 `EnableFileDrop=false` 会在 GTK 层拦截外部文件拖放（防止 WebKit 直接打开文件），HTML5 `drop` 收不到文件；本轮未开启该选项 |
| 文件选择上传 | 可用（GTK 原生文件对话框，见 13.5） |

> 结论：桌面版对「粘贴截图」与「拖拽上传」**不做成功承诺**；这两种方式在浏览器里可用，
> 在 Wails/WebKitGTK 下受上面限制。文件选择上传、文字/链接粘贴、图片上传与预览、
> 下载、复制均实测通过。

### 13.7 系统代理导致的 WebSocket 失败（环境问题，已如实提示）

本机有系统级手动代理（`http_proxy=http://127.0.0.1:10808`，
KDE `kioslaverc` `ProxyType=1`，`NoProxyFor=localhost,127.0.0.0/8,::1`）。
绑定 `192.168.31.184` 时，WebView 的 WebSocket 请求经代理后**丢失 `Upgrade` 头**，
Hub 返回 `426`，页面持续「已离线，重连中」。用原始 DNS/HTTP 探针已确认：

- 未经代理（或把该 IP 加入豁免）时，同一个 WebSocket 握手带完整
  `Upgrade: websocket` / `Connection: Upgrade`，Hub 返回 `101`，页面显示「实时同步中」；
- 经代理时同一请求缺少 `Upgrade`/`Connection: Upgrade`（实测抓包），因此 426。

处理：桌面版**不修改**用户的代理配置，只在设置窗口显示黄色横幅，指出要把具体 IP 加入
忽略列表，并给出 `no_proxy` 示例。绑定回环地址（`127.0.0.1`）时不受影响。

### 13.8 未验证 / 未做（明确标注）

1. **未在真实手机**上验证手机访问桌面版 Hub（与既有 Android 真机待验收项一致）。
2. **未验证**从文件管理器拖拽文件到桌面窗口（见 13.6，Wails 默认拦截）。
3. **未验证**从系统剪贴板粘贴图片（见 13.6，WebKitGTK 限制）。
4. 未做 Windows/macOS 打包、开机自启、多监听、自动选路、二维码。
5. 托盘图标在 GNOME（无扩展）下的表现未验证；无托盘回退逻辑已在隔离会话中实测。
6. 未验证高 DPI / 多显示器 / 分数缩放下的窗口布局。
7. **不把 Chromium 当作 Wails WebView**：上述 GUI 结论全部来自真实
   `libwebkit2gtk-4.1` + Wails GTK3 窗口。

### 13.9 复现步骤（桌面版）

```bash
# 1) 构建（无 root 也可以）
scripts/desktop-dev-sysroot.sh     # 可选：系统已装 libgtk-3-dev/libwebkit2gtk-4.1-dev 时跳过
scripts/build-desktop.sh

# 2) 单元 + 集成测试（桌面模块）
cd desktop && go test -tags gtk3 ./...

# 3) GUI（需要图形会话）
dist/flowdrop-desktop
# 首次：设置窗口 → 设置管理员密码 → 打开收件箱 → 登录

# 4) 用户级安装（不自动执行）
scripts/install-desktop.sh
```

桌面偏好（仅绑定地址+端口）：`${XDG_CONFIG_HOME:-~/.config}/flowdrop/desktop.json`；
Hub 数据：`${XDG_DATA_HOME:-~/.local/share}/flowdrop/`（与 CLI 共用，靠数据目录独占写锁互斥）。

### 13.10 回归确认（本轮未破坏既有交付）

```text
$ cd server && go build ./...      # 通过
$ cd server && go test ./...
ok  flowdrop/internal/api
ok  flowdrop/internal/auth
ok  flowdrop/internal/config
ok  flowdrop/internal/discovery
ok  flowdrop/internal/storage
ok  flowdrop/internal/store
ok  flowdrop/pkg/hub
$ cd web && npx tsc -b --noEmit false   # 通过（exit 0）
$ cd web && npm run build               # 通过，产物写入 server/internal/webui/dist
$ grep -c wails server/go.mod           # 0
```


---

## 14. 本轮验证：桌面 LAN + Tailscale 双监听（2026-11-08）

> 本节是**新结果**，不覆盖 §1–§13 的历史证据。§13 中“一个进程只绑定一个地址”的
> 产品限制已被本轮解除：**同一个 Hub** 可同时监听 LAN + Tailscale，仍不监听
> `0.0.0.0`、不起两个 Hub、不共享两个数据目录、不做自动网络重绑定/自动选路。
> 本轮只改 `desktop/`、桌面相关脚本/文档与生成的 `server/internal/webui/dist`；
> 未改 `server` 业务源码、`android/`、`web` 业务源码、`server/go.mod`。

### 14.1 最终产物

```bash
scripts/build-desktop.sh
```

实际输出结尾：

```text
==> building web frontend
✓ built in 507ms
==> using user-local sysroot: /home/bob/.local/share/flowdrop-desktop/sysroot
==> building desktop binary (release, tags: gtk3 production)
built: /home/bob/vibe-code/flowdrop/dist/flowdrop-desktop
```

| 项目 | 值 |
|---|---|
| 产物 | `dist/flowdrop-desktop` |
| 大小 | 16 962 504 B |
| SHA-256 | `3e2de432f9aa8167474943b46cd8d701838117f056226e0c323e7303091fd066` |
| 版本注入 | `FLOWDROP_VERSION=0.1.0-desktop`（运行时日志实测 `version=0.1.0-desktop`） |
| 构建环境 | 当前用户级 sysroot（`~/.local/share/flowdrop-desktop/sysroot`），未使用 sudo、未改宿主包 |
| 动态依赖 | `ldd` 无 `not found` |

> `dist/flowdrop`（CLI）**未由本 worker 重建**；CLI 双监听验证使用当前 `server/` 源码
> 构建到 `/tmp/fd-cli-dual/flowdrop` 的临时二进制，避免触碰既有 CLI 产物。

### 14.2 Go 测试（本轮最终代码）

```bash
cd server && go vet ./... && go test ./... -count=1 -v
cd desktop && go vet -tags gtk3 ./... && go test -tags gtk3 -count=1 -v
```

实际结果：

```text
server:  ok  internal/api / auth / config / discovery / storage / store / pkg/hub
         119 个 --- PASS（含子测试；106 个顶层 Test），0 SKIP，0 FAIL
desktop: ok  flowdrop/desktop / internal/appsettings / internal/hubmgr / internal/network
         72 个 --- PASS（50 个顶层 Test），0 SKIP，0 FAIL
```

`go vet` 两处均无输出。桌面测试全部使用真实 `flowdrop/pkg/hub`，不是 mock。

本轮新增/针对双监听的用例：

| 用例 | 断言 |
|---|---|
| `TestLegacySingleAddressFileStillLoads` | 旧 `desktop.json`（只有 `bind_address`+`port`）仍加载为单地址；`AdditionalListen()` 非 nil 空 slice |
| `TestDualModeRoundTripAndListeners` | 双地址保存/读取；`additional_bind_addresses` 正确写入；切回单地址时字段被移除 |
| `TestValidateDualModeRules` | 重复地址、第二个附加地址、通配、IPv6、空地址均拒绝 |
| `TestDualListenSharesOneHubAndData` | 真实 Hub 双监听（127.0.0.1 + 127.0.0.2 替身）：同一 DB 登录、跨监听建/读条目、跨监听 WS 事件、Stop 后两个端口都释放 |
| `TestDualStartIsAtomic` | 附加地址不存在时启动整体失败，主地址端口已回滚释放，不残留半启动 |
| `TestSingleModeOverridesConfigAdditionalListen` | config.json 里的 `additional_listen` 不会被桌面单地址模式继承（非 nil 空 slice 覆盖） |
| `TestDescribeAccessURLsUsesActualBoundURLsOnly` / `TestPreferredAccessURLPrefersLAN` | 只从真实 URL 生成可用列表； LAN 优先；未知地址不冒充 LAN/Tailscale |
| `TestValidateDualSelection*` | 严格要求一个 LAN + 一个 Tailscale；无 Tailscale 时提示不可选且不要求安装 |
| `TestServiceApplyDualNetworkUsesRealLANAndTailscale` | **真实网卡** `eno1` + `tailscale0` 同端口双监听；两个 URL 健康；双模式配置落盘；占用端口时恢复原双监听并保持配置不变 |
| `TestServiceApplyNetworkRollbackRestoresPreviousHub` | 单地址切换失败后恢复旧 Hub 并实际可访问 |
| `TestStartupDualSettingsBindBothRealAddresses` | 真实启动路径 `desktop.json → loadStartupSettings → startHub` 绑定真实 LAN + Tailscale |

> `127.0.0.1`/`127.0.0.2` 只用于 Hub 层“共享数据/原子性”测试；**没有**把它当作
> 真实 Tailscale 验证。真实 LAN + Tailscale 的测试见上表两项与 §14.3。

### 14.3 真实桌面 App：设置保存、双地址、重启、回滚

环境：本机真实 `eno1 = 192.168.31.184`，`tailscale0 = 100.125.168.63`；
独立 Xvfb `:99` + 真实 Wails/WebKitGTK 窗口（不是 Chromium）；测试数据目录
`/tmp/fd-dual-e2e`，mDNS 在测试中显式关闭。

实际 GUI 操作与结果：

| 场景 | 实际结果 |
|---|---|
| 首次启动（无 desktop.json） | 自动检测 LAN，单地址 `192.168.31.184:8080`；设置窗口出现（无管理员密码） |
| 选择「同时使用内网和 Tailscale」 | 出现两个下拉：`局域网（LAN）· eno1 192.168.31.184` 与 `Tailscale · tailscale0 100.125.168.63` |
| 端口改为 `18080` 并保存 | 日志：`hub stopped` → `flowdrop hub configured ... additional_listen=[100.125.168.63:18080]` → `listening urls="[http://192.168.31.184:18080 http://100.125.168.63:18080]"`；成功横幅列出两个 URL |
| 实际监听 | `ss -ltnp`：同一 `flowdrop-deskto` PID 同时 LISTEN `192.168.31.184:18080` 与 `100.125.168.63:18080` |
| 健康检查 | `curl http://192.168.31.184:18080/api/v1/health` → 200；`curl --noproxy '*' http://100.125.168.63:18080/api/v1/health` → 200 |
| desktop.json | `bind_address=192.168.31.184`、`port=18080`、`additional_bind_addresses=["100.125.168.63"]` |
| 重启最终二进制 | 从同一 desktop.json 自动启动双地址；收件箱加载 LAN URL；两个健康检查仍为 200 |
| 单模式/旧文件 | appsettings 单测覆盖；真实服务单地址切换测试通过 |
| 失败回滚（端口占用） | 先在 LAN 的 `18081` 占用端口，再在 GUI 改端口保存：设置窗口显示「端口已被占用…本应用不会结束其他进程」，日志自动恢复 `192.168.31.184:18080 + 100.125.168.63:18080`，desktop.json **仍是 18080** |
| 启动地址失效 | 预置 `additional_bind_addresses=["100.64.99.99"]` 后启动：日志 `bind: cannot assign requested address (rolled back 1 already bound listener(s))`；只显示设置窗口与错误；desktop.json 未被改写；没有半启动 |
| 未运行时显示 | 「实际绑定」显示「—（未运行，当前没有监听）」；可用 URL 列表为空，不把已保存地址冒充为可用 |

截图（本机临时路径，供复核）：

```text
/tmp/fd-dual-e2e/settings_saved_dual2.png   双模式保存成功 + 两个实际 URL/类型
/tmp/fd-dual-e2e/settings_rollback.png      端口占用错误 + 自动恢复原双监听
/tmp/fd-dual-bad/settings_final.png         启动地址失效：原子回滚、未运行、配置未改
/tmp/fd-dual-e2e/app2_restart.png           重启后收件箱优先加载 LAN 地址
/tmp/fd-dual-e2e/inbox_loggedin.png         Wails 内登录成功（系统代理导致 WS 仍提示离线）
```

### 14.4 两个地址的登录 / 收发 / WebSocket（真实 Hub）

REST（Python requests，绕过系统代理；每个 host 分别登录）：

```text
login LAN 200 (flowdrop_session + flowdrop_csrf, domain=192.168.31.184)
login TS  200 (flowdrop_session + flowdrop_csrf, domain=100.125.168.63)
create via LAN 201
list via TS 200; TS sees LAN text: True
create via TS 201
upload via TS 201
download same blob via LAN 200; bytes_equal True; Content-Disposition: attachment
anonymous download via LAN 401
LAN sees TS text: True
ALL_REST_OK
```

WebSocket（Go + `github.com/coder/websocket`，两个 host 各自带 Cookie/Origin，
绕过系统代理）：

```text
WS http://192.168.31.184:18080 hello
WS http://100.125.168.63:18080 hello
create via LAN:  both connections receive item.created
create via Tailscale: both connections receive item.created
ALL_WS_OK
```

说明：Cookie 按 host 隔离，两个地址**分别登录**；数据、账号、会话表、Broker 共享。
这符合“不承诺不同 host Cookie 自动共享”的产品边界。

### 14.5 CLI：真实 LAN + Tailscale 双监听与 mDNS

使用当前 `server/` 源码构建的临时 CLI（未覆盖 `dist/flowdrop`）：

```bash
FLOWDROP_LISTEN=192.168.31.184:18082 \
FLOWDROP_ADDITIONAL_LISTEN=100.125.168.63:18082 \
FLOWDROP_COOKIE_SECURE=false \
FLOWDROP_DISCOVERY_ENABLED=true \
FLOWDROP_DATA_DIR=/tmp/fd-cli-dual/data \
/tmp/fd-cli-dual/flowdrop serve
```

实际日志：

```text
flowdrop hub configured ... listen=192.168.31.184:18082 additional_listen=[100.125.168.63:18082]
mDNS LAN discovery not published ... address=100.125.168.63 is Tailscale/CGNAT; Tailscale access is not advertised as a LAN entry
mDNS discovery published ... address=192.168.31.184 port=18082 interface=eno1 txt="[v=1 scheme=http path=/]"
listening urls="[http://192.168.31.184:18082 http://100.125.168.63:18082]"
```

- 两个地址健康检查均 200；`ss` 显示同一 PID 两个 LISTEN。
- 跨地址 REST 登录/建/读均通过（`CLI_DUAL_REST_OK`）。
- 停止后两个端口均释放，日志 `mDNS discovery withdrawn` + `hub stopped`。
- **mDNS 只发布 LAN 地址，Tailscale 被跳过**；没有把 Tailscale 冒充为同一 LAN 入口。

### 14.6 明确未验证 / 环境限制（不得写成已通过）

| 项目 | 状态 |
|---|---|
| 真实手机在家庭 Wi-Fi 与移动网络 Tailscale 之间切换访问 | **未验证**；本轮只在本机真实 `eno1` + `tailscale0` 上验证双地址 |
| 桌面 Wails WebView 的 WebSocket 经系统代理 | **仍失败（426）**：本机 KDE 系统代理未把 Hub IP 加入忽略列表。设置窗口保留黄色提示；Go WS 探针绕过代理后 101 通过。未修改宿主代理 |
| 地址在运行中失效（Tailscale 退出/DHCP 变化）后的自动重绑定 | **设计上不做**；只验证启动失败与手动保存失败回滚 |
| Android 真机 / 自动发现 | 仍待验收，本轮未改 Android，也未真机测试 |
| Windows/macOS 桌面版 | 未做 |
| `dist/flowdrop` CLI 正式产物重建 | 未做（按写入范围）；CLI 用当前源码临时构建验证 |

### 14.7 本轮改动文件

- `desktop/internal/appsettings/appsettings.go`、`appsettings_test.go`：`additional_bind_addresses`（可选）与旧文件兼容、双地址校验、`AdditionalListen()`。
- `desktop/internal/hubmgr/hubmgr.go`、`hubmgr_test.go`：`Config.AdditionalBindAddresses`、`AdditionalListen` 传给 `hub.Options`、`URLs()`/Status 实际地址、双监听与原子失败测试。
- `desktop/internal/network/network.go`：`HasLAN`；既有分类/代理逻辑复用。
- `desktop/network_selection.go`、`network_selection_test.go`（新增）：实际 URL 展示、LAN 优先、双模式选择校验。
- `desktop/service.go`、`service_test.go`：`ApplyNetworkConfig`/`ApplyDualNetwork`，旧 `ApplyNetwork` 兼容；真实 URL 状态；双模式保存/回滚/真实网卡测试。
- `desktop/main.go`、`desktop_helpers_test.go`：启动读取双地址、收件箱/托盘优先 LAN URL、启动双地址测试。
- `desktop/assets/index.html`、`app.js`、`style.css`：双模式 UI、两个地址下拉、实际 URL/类型列表、未运行不冒充可用、无 Tailscale 提示。
- `README.md`、`docs/DEPLOY.md`、`docs/ARCHITECTURE.md`、`docs/PLAN.md`、`docs/PRD.md`、本文件：范围/示例/历史说明与本轮证据。
- `dist/flowdrop-desktop`；`server/internal/webui/dist/`（构建脚本自动生成的嵌入产物）。
- **未修改**：`server` 业务源码、`server/go.mod`、`android/`、`web/` 业务源码、其它脚本、`dist/flowdrop`。


### §14 最终产物补记（协调者）

- 协调者核对 `dist/flowdrop-desktop` 的 SHA-256 为
  `3e2de432f9aa8167474943b46cd8d701838117f056226e0c323e7303091fd066`，与 worker 交付一致。
- 已执行 `scripts/build.sh 0.1.0`，TypeScript 检查、Vite 构建及 Go CLI 构建全部成功。
  `dist/flowdrop` 已更新，包含同一 Hub 多地址监听；构建时间 `2026-11-07T21:29:55Z`。
  此结果补齐本节 worker 完成时尚未重建正式 CLI 产物的历史状态。
- 真实手机从 Wi-Fi 切换到移动网络经 Tailscale 收发仍待用户验收；本次未重复 worker 已通过的测试。
