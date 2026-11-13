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
>
> **2026-11-09 CLEAN-01 之后**：`internal/auth` 的密码哈希与 `admin_users`/`sessions`
> 表已从代码与新建数据库中删除（只保留设备配对一条认证路径）。第 1.1 节里
> `TestHashPassword*` / `Test*Session*` / `TestLogin*` 等行是当时那轮运行的历史证据，
> 这些用例已随代码删除；当前用例清单以最新一轮 `go test ./...` 输出为准。

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

> **历史限制已修复（2026-11-08 本轮）**：这一节保留为旧版实测记录。当前桌面版在创建
> 窗口前把本进程 WebKitGTK 设为直连，系统代理保持开启时 HTTP 与 WebSocket 均可
> 直接工作，不再必须修改 `no_proxy`。新的实测、失败路径和入口验证见 §15。

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

桌面偏好（绑定地址+端口；本机免码登录后另存一个设备令牌，0600）：`${XDG_CONFIG_HOME:-~/.config}/flowdrop/desktop.json`；
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
| 桌面 Wails WebView 的 WebSocket 经系统代理 | **旧版仍失败（426）**：本机 KDE 系统代理未把 Hub IP 加入忽略列表。设置窗口保留黄色提示；Go WS 探针绕过代理后 101 通过。未修改宿主代理。**该限制已在本轮修复**：应用内 WebKit 直连后，系统代理保持开启时实测 `/api/v1/events status=101`，见 §15 |
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

## 15. 本轮验证：主窗口设置入口 + 应用内代理直连（2026-11-08）

### 15.1 问题与最小修复

旧版主窗口是 Hub 的真实 HTTP origin（`http://<LAN-IP>:<port>/`），设置窗口是进程内
`wails://localhost`，两者 origin 隔离；设置入口只有托盘，所以主窗口里找不到设置。

本轮不把管理 bridge 开放给 Hub 页面，也不增加 LAN HTTP 管理 API：

- 只向 **Wails 收件箱窗口**注入一个「设置」按钮：登录页在右上角，登录后位于顶部栏
  「退出」旁；React 在首次加载/登录/退出后替换 DOM 时由 `MutationObserver` 自动补回。
- 收件箱窗口增加原生菜单「设置… / 在浏览器打开 / 退出 FlowDrop（停止 Hub）」，
  并提供 `Ctrl+,` 快捷键。
- 点击只发送固定 raw message `flowdrop:opensettings`。服务端只在本实例 inbox 窗口、
  当前 Hub origin 完全匹配时接受；未知消息、其它 origin、设置窗口自身、任意 native
  方法名一律忽略。外链仍走原有 `flowdrop:openurl:`（仅 http/https + 限速）。
- 普通浏览器/手机加载的 React 资产不包含该按钮；设置窗口的 `wails://` 资产也不包含。
- 托盘菜单「设置…」、首次管理员初始化、双网络模式、隐藏/恢复窗口、无托盘确认退出
  均保留。

代理侧：另一个 worker 提供同 main 包 `configureDesktopWebViewProxy() error`，在
`application.New` 之后、创建窗口之前把本进程 WebKitGTK 数据管理器的网络代理设为
`WEBKIT_NETWORK_PROXY_MODE_NO_PROXY`。它不修改系统代理、KDE/GSettings 或环境变量。
`SettingsService.ProxyWarning` 现在由实际结果组合：

| 应用内直连 | 环境代理风险 | 设置窗口 |
|---|---|---|
| 成功 | 有/无 | 不显示旧的「必须改 no_proxy」提示 |
| 失败 | 有 | 显示「应用内 WebView 直连代理设置失败（原因）」+ 真实风险诊断（不隐藏） |
| 失败 | 无 | 不显示；无代理可绕过时不制造警告 |

风险文案明确写“可能被改写、代理允许 WebSocket 时不受影响”，不再断言检测到代理就必然
离线。

### 15.2 单元测试（`go vet` / `go test`）

```text
$ cd desktop && go vet -tags gtk3 ./...          # 无输出
$ cd desktop && go test -tags gtk3 -count=1 ./...
ok  flowdrop/desktop
ok  flowdrop/desktop/internal/appsettings
ok  flowdrop/desktop/internal/hubmgr
ok  flowdrop/desktop/internal/network
```

本轮桌面模块共 **56 个顶层 PASS + 30 个子测试 PASS**。与设置入口/代理状态直接相关的：

| 测试 | 覆盖 |
|---|---|
| `TestInboxControlActionAllowsOnlyFixedSettingsFromHubOrigin` | 正确 inbox + 正确 Hub origin 的 `flowdrop:opensettings` 被接受；设置窗口/错误 origin/不同 scheme/缺失 origin/未知动作/带额外 payload 全部拒绝 |
| `TestInboxControlActionKeepsExternalURLLimited` | 外链仍只允许 http/https，拒绝 javascript/file/wails 和超长 URL |
| `TestSettingsEntryScriptUsesOnlyTheFixedRawMessage` | 注入脚本只含固定设置消息，不含 `Call.ByName` / wails runtime / 第二个 URL 通道 |
| `TestSettingsEntryButtonIsDesktopOnly` | 本地设置资产与 Hub 浏览器资产都不含桌面专属按钮/消息 |
| `TestCombineProxyWarningHonorsInAppDirectMode` | 直连成功才清掉旧 no_proxy 提示；失败保留环境诊断并明确标注直连失败；无代理风险时不制造警告 |

失败路径（WebKit 直连配置失败）无法在本机 Linux/GTK3 真实触发（helper 成功），
因此该分支只有上面的纯函数单元测试，不声称已做真实失败 GUI 实测。

### 15.3 真实 Wails/WebKitGTK + Xvfb 验证

环境：既有 Xvfb `:99`（1400x1000）、独立 D-Bus 会话、临时 `XDG_*`/数据目录，绑定真实 LAN
`192.168.31.184:18114`，已有管理员密码。系统代理保持开启：
`http_proxy=https_proxy=HTTP_PROXY=HTTPS_PROXY=http://127.0.0.1:10808`（真实 xray
监听），`no_proxy=` / `NO_PROXY=` 为空；未修改 KDE `kioslaverc` 或任何环境配置。

启动日志：

```text
2026/11/08 09:16:46 in-app WebView network proxy: direct (system proxy unchanged)
listening url=http://192.168.31.184:18114
request method=GET path=/api/v1/auth/session status=200
request method=POST path=/api/v1/auth/login status=200
request method=GET path=/api/v1/items status=200
request method=GET path=/api/v1/events status=101 dur_ms=48108
```

`status=101` 是真实 WebKit WebView 的 WebSocket 升级，页面同时显示「实时同步中」；
`ss -tnp` 显示 `WebKitNetworkProcess` 直接连到 `192.168.31.184:18114`，没有经过
`127.0.0.1:10808`。HTTP 登录/列表均为 200。

| 场景 | 实测结果 | 截图 |
|---|---|---|
| 已有管理员，启动进入主窗口（不是设置窗口） | 主窗口登录页出现；**登录前**右上角已有「设置」按钮 | `/tmp/fd-gui-settings/shots/01-login.png` |
| 登录前点击主窗口「设置」 | 打开并聚焦同一个 780x760 设置窗口；无旧代理警告（直连成功） | `/tmp/fd-gui-settings/shots/02-settings-from-injected.png` |
| `Ctrl+,`（登录前/后） | 设置窗口重新显示并聚焦 | `/tmp/fd-gui-settings/shots/03-settings-via-shortcut.png`、`09b-settings-shortcut-after-login.png` |
| 登录后入口 | 顶部栏「设置」位于「实时同步中」「退出」之间；WS 已连接 | `/tmp/fd-gui-settings/shots/07b-after-login.png` |
| 登录后点击入口 | 设置窗口再次显示/聚焦 | `/tmp/fd-gui-settings/shots/08-settings-after-login.png` |
| 小窗口 420x420 | 顶部栏入口不遮挡「实时同步中」「退出」；发送区仍可用 | `/tmp/fd-gui-settings/shots/13-small-window.png` |
| 退出登录 | React 替换 DOM 后右上角「设置」入口仍存在 | `/tmp/fd-gui-settings/shots/14-after-logout-small.png` |
| 托盘继续工作 | 隔离会话里用最小 `StatusNotifierWatcher` 桩注册托盘；DBusMenu 5 项：显示收件箱/设置…/在浏览器打开/分隔符/退出；点击「设置…」打开设置，点击「退出」停止 Hub 并释放端口 | `/tmp/fd-gui-settings/shots/10-settings-from-tray.png` |
| 无托盘仍能进设置 | 无 Watcher 的独立会话日志 `no StatusNotifierWatcher ... running without a tray`；点击登录前右上角「设置」仍打开设置窗口 | `/tmp/fd-gui-settings/shots/11-notray-settings-from-injected.png` |

真实执行命令要点（可复现）：

```bash
Xvfb :99 -screen 0 1400x1000x24 -nolisten tcp &   # 本轮复用了既有 :99
dbus-daemon --session --fork --print-address=1 --print-pid=1   # 需要托盘时另起 watcher 桩
env DISPLAY=:99 http_proxy=http://127.0.0.1:10808 https_proxy=http://127.0.0.1:10808 \
    no_proxy= NO_PROXY= \
    FLOWDROP_DESKTOP_CONFIG=/tmp/.../desktop.json FLOWDROP_DATA_DIR=/tmp/.../data \
    dist/flowdrop-desktop
```

> 无窗口管理器时，`xdotool type --window` 可向 WebKit 输入框输入，`Ctrl+,` 用
> XTEST 全局按键；这不是 Chromium/curl 验证，窗口是真实 Wails GTK3 + WebKitGTK 4.1。

### 15.4 安全边界与回归

- 原 §13.4「Hub 页面拿不到管理 bridge」不变：本轮没有把 `Call.ByName` 或
  runtime.js 引入 Hub 页面；新增的只是固定设置动作，且受 inbox 窗口 + 当前
  Hub origin 双重校验（单元测试覆盖拒绝路径）。
- 托盘菜单结构保持原有 5 项；窗口隐藏后托盘「显示收件箱」可恢复、托盘「设置…」可
  打开设置、托盘「退出」停止 Hub 并释放端口均复测。关闭窗口收进托盘的 WindowClosing
  hook 本轮未改；由于测试环境没有窗口管理器，本轮用窗口隐藏/恢复验证 Wails Show/Hide
  路径，真实点击关闭按钮的旧实测保留在 §13.5。
- 首次管理员设置、双地址绑定、启动失败显示设置窗口、`desktop.json` 保存逻辑未改。
- 外链仍交系统浏览器，下载仍走 WebKit 原生行为。

### 15.5 限制

- 应用内代理失败分支未在真实 GUI 触发（本机构建成功）；只有组合逻辑单测，未伪造
  “配置失败”的生产行为。
- 托盘验证使用最小 `StatusNotifierWatcher` D-Bus 桩，不是完整 KDE Plasma 面板；
  菜单注册、布局和点击事件是真实的 Wails D-Bus 交互。
- 未重新验证高 DPI、多显示器和真实手机。

### 15.6 最终产物

- `dist/flowdrop-desktop`（`scripts/build-desktop.sh` 在代理 helper 最终版之后重新生成）
- SHA-256：`1d725145f9223d3ada068f8f4ba9df3efd6ce831f4bf6bd2374e8379ad8394ca`
- 大小：`16,970,824` 字节（release, tags `gtk3 production`）
- 最终产物在独立 Xvfb/D-Bus 中冒烟：启动日志 `in-app WebView network proxy: direct`，
  登录页 HTTP 200，真实点击右上角「设置」打开 780x760 设置窗口，退出后 Hub `hub stopped`
  且端口释放。
- 安装/升级：先退出正在运行的 FlowDrop（托盘「退出 FlowDrop」），再运行
  `scripts/install-desktop.sh`；不要直接覆盖 `~/.local/bin` 后立即启动，
  SingleInstance 可能复用旧进程。详见 DEPLOY §16.3。

---

## 16. 桌面启动原生崩溃（AT-SPI / libatk-bridge）定位与修复 — 2026-11-08

### 16.1 结论

本轮复现的原生崩溃定位于登录会话的 AT-SPI 无障碍总线故障：系统 `libatk-bridge` 在
`spi_register_object_to_path` 里解引用空的 `spi_global_app_data`（`NULL+0x18`），
而 WebKitGTK 仍注册可访问对象。用户最初日志缺少首部，尚不能完全确认与该复现为同次原因。Go 无法 recover 原生 SIGSEGV。本轮做了**进程级窄
兜底**：启动时探测无障碍总线，仅在探测失败时对 FlowDrop 这一个进程设置
`NO_AT_BRIDGE=1`；正常会话保留无障碍。

### 16.2 崩溃证据（事实）

- 真实进程 `flowdrop-desktop[3081727]` 10:42:46 崩溃：`SIGSEGV`、`PC=0x7f1873ab5778`、
  `addr=0x18`、`rax=0`、`signal arrived during cgo execution`，前置
  `Couldn't register with accessibility bus: Did not receive a reply`。
- 该 `PC` 的低位偏移 `0x11778` 落在 `libatk-bridge-2.0.so.0.0.0`：
  `mov (%rax),%rax` 后 `cmp %rsi,0x18(%rax)`，GOT 项为 `spi_global_app_data`；
  崩溃即该指针为 NULL。
- 只读诊断 worker 用无 FlowDrop 代码的 C 探针在同一故障会话 3/3 同址崩溃，并保留
  原生栈：`spi_register_object_to_path ← libatk-bridge ← WebKit ← JSC ← GLib main
  loop ← gtk_main_iteration_do`；`NO_AT_BRIDGE=1` 对照 2/2 正常；隔离新会话总线
  3/3 正常（报告 `/tmp/flowdrop-crash-diagnosis-2026-11-08.md`）。
- 托盘 goroutine 的 `runtime.selectgo`（`systemtray_linux.go:411`）只是崩溃 dump 中
  的阻塞点，不是崩溃帧；无 Wails/菜单/代理帧。

### 16.3 修复内容（desktop/ 内）

- 新增 `desktop/a11y_linux.go`：`guardAccessibilityBridge()` 在 `main()` 最前面、
  GTK/WebKit 初始化之前运行。
  - `FLOWDROP_DESKTOP_A11Y=on|keep`：不探测、不关闭（用户显式选择）。
  - `FLOWDROP_DESKTOP_A11Y=off|none`：本进程关闭。
  - 已设置 `NO_AT_BRIDGE`：尊重调用方，不覆盖。
  - 默认 `auto`：经会话总线 `org.a11y.Bus.GetAddress` 取地址，连接后调用只读的
    `org.a11y.atspi.Registry.GetRegisteredEvents` 做真实 D-Bus 往返，单阶段超时 2 秒、外层等待上限 3 秒；
    失败才 `NO_AT_BRIDGE=1` 并输出原因与恢复建议。
- `desktop/main.go`：调用该 guard；`desktopState` 记录兜底原因。
- `desktop/service.go` + `desktop/assets/{index.html,app.js}`：设置窗口新增黄色
  `accessibility_warning` 横幅，明确“仅本应用、系统设置未改、注销重登恢复”。
- 新增 `desktop/a11y_unsupported.go`（非 Linux 空实现）与
  `desktop/a11y_linux_test.go`（策略、地址解析、无响应/缺失 socket 探针、提示文案）。

### 16.4 验证（真实二进制；不重跑无关 GUI 回归）

命令（隔离会话总线/运行时目录与 XDG 数据；Wayland 走真实 kwin，X11 为 XWayland）：

```
gofmt -l desktop/                                  # 无输出
go vet -tags gtk3 ./...                            # 通过
go test -count=1 -tags gtk3 -run 'TestA11y|TestProbeA11y|TestGuardAccessibility|TestDesktopAccessibility' -v .
                                                   # PASS（含 0.4s 无响应总线探针、缺失 socket）
scripts/build-desktop.sh                           # release, tags gtk3 production
```

| 场景 | 结果 |
|---|---|
| 真实故障会话（当前 `/run/user/1000/at-spi/bus_0` 缺失） | 日志 `accessibility bridge disabled … connect: no such file or directory`；应用正常启动，Hub 请求 200（`/tmp/fd-repro-a1/run.log`、`/tmp/fd-iso-w2/run.log`） |
| 隔离健康会话总线（自有 `AT-SPI` 总线） | 日志 `AT-SPI bus responsive; accessibility left enabled`；无障碍保留，应用正常（`/tmp/fd-iso-w1/run.log`） |
| 故障地址 + `FLOWDROP_DESKTOP_A11Y=on` | 日志 `kept (FLOWDROP_DESKTOP_A11Y=on)`；不探测、不关闭；应用运行（`/tmp/fd-iso-w3b/run.log`） |
| 故障地址 + `NO_AT_BRIDGE=1` | 日志 `already set; not probing`；应用正常（`/tmp/fd-iso-w4/run.log`） |
| 隔离会话中冻结无障碍总线守护进程（模拟无响应） | 修复前构建卡在 WebKit 初始化（日志只有 JSC 行）；修复后 2 秒探测超时→关闭→Hub 正常启动（`/tmp/fd-iso-c1/run.log`、`/tmp/fd-iso-c2/run.log`） |

### 16.5 产物

`dist/flowdrop-desktop`（`scripts/build-desktop.sh`，2026-11-08 11:04 CST）：

- SHA-256：`53a4bb9cbf86dea00176ca552de7209943d90cd8bd27e0f872f07f7a783b4b02`
- 大小：`16,987,240` 字节（release，tags `gtk3 production`）

### 16.6 限制与未完成

- 崩溃发生时的 a11y 总线状态（“socket 存在但无应答”）在诊断 worker 复现后已变化，
  本轮**没有再亲手触发同一条 SIGSEGV**；修复有效性依据是：故障总线被可靠探测并
  跳过，崩溃路径（`libatk-bridge` 初始化 + WebKit 注册）不再进入。没有把探针
  结果当成“已亲测同次崩溃消失”。
- 设置窗口的黄色横幅与 `Ctrl+,`/托盘菜单等 GUI 入口本轮未做截图级复测（上一轮
  §15 已覆盖；本轮只验证进程启动与 Hub 加载）。是否保留可访问性设置入口的完整
  回归留给 CR-03。
- 冻结无障碍总线时，WebKit 子进程仍可能因自身 a11y 连接阻塞而拖慢首个页面加载；
  真实故障态（socket 缺失）下子进程快速失败，不影响本修复目标。
- 真正的系统侧根因（at-spi2-core 2.60.4 缺少 NULL 保护）未修复，也不应由此仓库修；
  恢复建议是注销重登，见 `docs/DEPLOY.md` §16.10。


### 16.7 CR-03 定向验收补充

2026-11-08，独立新 pi worker（task `task_0af23f9c44fa`，dispatch
`ctx_6c8046db7f96`）使用同一摘要的现有二进制，空白临时数据库、隔离
XDG/会话总线、缺失 AT-SPI socket 和 Xvfb :99 验证：

- 真实 WebView 设置页黄色无障碍提示完整可见；主 agent 已人工查看
  `/tmp/fd-cr03/shots/settings-fault.png`。
- `Ctrl+,` 使此前隐藏的设置窗口可见。
- 向本任务应用发送 SIGTERM 后退出码 0，日志 `hub stopped`，18098 端口释放。
  这只验证信号退出路径，不代表托盘菜单退出已复测。
- 用户真实 XWayland :0 下，合成快捷键及按钮/菜单点击未打开隐藏设置窗口，
  截图也受限。原因未确认；Xvfb 的成功不能排除真实桌面问题。
  主窗口设置按钮和托盘退出本轮仍未验证，不标为通过。

报告：`/tmp/flowdrop-cr03-report.md`。未修改产品代码、未重建、未安装，未停止
用户应用或宿主服务。复用 §16.4 的健康会话证据，不重复全量检查。原始用户
同次 SIGSEGV 的首部仍缺失，保留 §16.6 的判断限制。

流程偏差已同步 TASKS：截图超时实际 20 秒而非约定 10 秒，真实 XWayland
进行了四种输入尝试；后续必须遵循预算与停止条件，不继续扩大验收。

交付产物仍为 §16.5 的 `dist/flowdrop-desktop`。升级时先由用户从托盘菜单退出
旧进程，然后运行 `scripts/install-desktop.sh` 并重新打开应用；本轮未自动安装或提交。

---

## 17. 收件箱拖拽：独立 release 构建与交付文档 — 2026-11-08

任务 `task_3e6d6280dafe` / dispatch `ctx_b596d92722d7`（DD-03）。依赖已完成：
DD-02W 前端整窗 drag/drop（报告 `/tmp/flowdrop-dd02w-report.md`，8 例纯逻辑检查 +
`noEmit` 类型检查通过）、DD-02D 桌面仅 inbox 恢复 WebKit 文件 DND（报告
`/tmp/flowdrop-dd02d-report.md` 附录 A，真实 GObject 回归修正前 FAIL、修正后 PASS，
生产/探针标签 vet/test/build 与 6 例 node 守卫均已通过）。本节**不重跑**上述已通过
检查，只记录独立 release 构建与**非执行**的产物核对。

### 17.1 构建（实际执行）

```bash
timeout 300 scripts/build-desktop.sh     # 仓库根，rc=0
```

- 前端：`tsc -b && vite build` → `server/internal/webui/dist`（`index.html` 641 B、
  `assets/index-CKKiHSIo.css` 10,365 B、`assets/index-DjxBH9lw.js` 165,722 B），
  与本次源码一致（DD-02W 之后的新样式/逻辑已进入嵌入资源）。
- 桌面：`go build -tags "gtk3 production" -ldflags "-s -w -X flowdrop/internal/api.Version=0.1.0-desktop"`，
  GTK 使用仓库既有的用户级 sysroot（`~/.local/share/flowdrop-desktop/sysroot`），
  未安装任何系统包，未修改构建脚本。
- 构建命令退出码 0，日志 `/tmp/dd03-build.log`。构建顺带删除了受版本控制的
  `server/internal/webui/dist/.placeholder`（构建脚本既有行为），本轮已原样恢复，
  工作区没有额外删除。

### 17.2 产物（只做了 stat / sha256sum / strings / grep，未运行）

| 项目 | 值 |
|---|---|
| 绝对路径 | `/home/bob/vibe-code/flowdrop/dist/flowdrop-desktop` |
| 大小 | `16,999,592` 字节 |
| 构建时间 | 2026-11-08 13:10:01 +0800（`stat` mtime） |
| SHA-256 | `6201f15e3dc910686f1d26abc2f2e45532580f309539840838397ef2ab2106f6` |
| 标签 | `gtk3 production`（release） |

非执行核对结果：

- 嵌入前端 `server/internal/webui/dist/assets/index-DjxBH9lw.js` 含
  `webkitGetAsEntry`（目录判定）、`window-drop-overlay` 与「松开鼠标即可上传」文案；
  `index-CKKiHSIo.css` 含 `.window-drop-overlay` 规则。即打包的是 DD-02W 的新前端。
- 二进制 `strings` 含 DD-02D 生产日志前缀与全部成功/失败分支：
  `inbox file drop: WebKit HTML5 file drop restored (removed drag-drop=%d, drag-motion=%d)`、
  `... this view was already relaxed ...`、`... no WebKitWebView found ...`、
  `... GTK drag-drop/drag-motion signals not found ...`、`... unexpected handler count ...`、
  `... disconnect count mismatched ...`、`... native GTK window not available yet ...`、
  `... no GTK window to inspect ...`。
- 探针专用符号/标签（`flowdrop_filedrop_probe`、`FlowdropProbeObject`）在产物中不存在，
  即交付的是生产标签构建，不是探针构建。
- `git diff --check` 退出码 0（无空白/冲突标记）；本次只改 README 与 docs，未改产品
  代码、构建脚本、依赖或 `docs/TASKS.md`。

### 17.3 交付文档

- `README.md`：桌面特性加“收件箱整窗拖拽上传”要点；桌面版章节补充“直接运行
  `dist/flowdrop-desktop`、无需任何脚本或额外 Hub”“目录跳过不递归”“只登录后生效”
  与升级前托盘退出旧进程的说明，并指向本节。
- `docs/DEPLOY.md` §16.11（新增，并在 §16.9 加一条限制）：使用方式、限制、
  旧实例升级步骤与未验证项边界。
- 本任务报告：`/tmp/flowdrop-dd03-report.md`。

### 17.4 尚未验证（不得写成端到端通过）

本轮**未运行任何 GUI/浏览器/临时 D-Bus/Xvfb/portal/a11y**，未连接或操作 KDE/RustDesk，
未改代理，未启动容器/VM 代替验证。因此以下全部仍是未验证项：

1. 真实 WebKitGTK 控件树命中：inbox 窗口树中是否找到 `WebKitWebView`、两个信号是否
   各恰好 1 个匹配并成功 `disconnect`。
2. 实际两信号解除计数（期望 `removed drag-drop=1, drag-motion=1` 的日志行）与计数
   不符分支的真实表现。
3. 文件管理器真实拖拽端到端：登录前拖入不导航、目录跳过提示、多文件顺序上传、
   进度/取消/大小上限。
4. KDE/XDG portal 与 GTK3 `text/uri-list` 的真实交互；portal 自动激活的附加进程
   本轮未产生也未核对。
5. 设置窗口内拖入无反应、浏览器访问 Hub 行为不变（DD-02W/CR 级回归）。

代码完成、产物构建成功和非执行核对通过，都不等于端到端可用。实现依赖 Wails v3.1.0
未公开的内部 handler 位置与 `user_data` 约定，Wails 升级后必须重验（产品界面不暴露
该实现细节，仅在此技术文档记录）。

### 17.5 交付边界

**最终状态已更新**：本节保留早期构建及失败复验的历史。原生 FileList 桥最终构建与隔离 E2E 通过结果见 §18，不以本节旧产物摘要判断当前二进制。

**DD-08 诊断补证**：本轮容器 WebKitGTK 2.54 中，真实 pcmanfm 与 GTK URI-list 源的 DOM 事件均只有 URI/HTML 字符串，`files.length=0`。导航守卫已注入；容器临时实验即使接受 URI dragover 使 drop 派发，仍没有 File 对象，因此仅放宽网页类型判断不能完成上传。报告 `/tmp/flowdrop-dd08-report.md`；需原生桥接，尚未实现。结论只限本轮环境；诊断超出硬停止预算的偏差已记入 TASKS，不能因有证据而视为流程合规。

**DD-07 后续复验（2026-11-08）**：初始化接入修正后重建为 `bb6c1948fe3956909dd43e95d7ac195363e331c1d8c4a58f1baed7d25a8e2ade`。独立容器真实日志确认两处 GTK 阻断各解除 1 个，登录前不发送/不导航通过；登录后从 GTK URI-list 源实际拖入仍导航到 file:///、没有上传请求或条目，上传验收失败。产物仍不能认定拖拽可用，需另行诊断事件类型与真实文件管理器来源。报告 `/tmp/flowdrop-dd07-report.md`；容器已清理、宿主正式进程未变。§17.2 原摘要仅作历史记录。

**后续 DD-04 实测更正（2026-11-08）**：对 §17.2 摘要产物在独立 Docker 容器内做了原生 GTK URI-list 鼠标拖拽。WebView 登录通过，但没有任何 `inbox file drop` 日志或上传请求，条目为空，拖拽验收失败；登录前不发送/不导航通过。不能将本构建视为拖拽功能可用。报告 `/tmp/flowdrop-dd04-report.md`；容器已精确停止并删除，宿主 KDE/portal/RustDesk PID 未变。后续仅修正拖拽初始化生命周期接入，再重建复验；其他未验证项保留。

产物已生成但**未安装、未 commit/push**。用户升级时应先从托盘「退出 FlowDrop（停止
Hub）」结束旧进程，再手动运行新二进制或新安装版本；本仓库不自动安装，也不承诺旧
实例会被替换。

## 18. Linux 原生 FileList 拖拽最终交付（2026-11-08）

本节保留前次 X11 交付记录；用户随后报告 KDE Wayland 无效，修复后最新产物和验证见 §19。

### 18.1 前次产物与实现

DD-11 task `task_006c1a08e946` / dispatch `ctx_8b450e34c884` 独立构建成功，
`timeout 300 scripts/build-desktop.sh` rc=0。主 agent 已核对最终文件：

| 项目 | 值 |
|---|---|
| 路径 | `dist/flowdrop-desktop` |
| 大小 | 17,020,360 字节 |
| mtime | 2026-11-08 13:52:39 +0800 |
| SHA-256 | `4cbfeef23938cdf38807ffebb8e2f052330e5ce6153c6bdf8b8ba169609636ca` |
| 构建标签 | `gtk3 production` |

GTK 原生 drop 回调接收实际拖入的本地文件；真实路径只留在原生侧，带唯一 accept MIME
标记的 WebKit 文件选择请求消费该批路径，隐藏 input 产生真实 FileList，再由 Composer
沿现有 Cookie/CSRF 上传。未添加路径读取 binding、临时 HTTP relay、Cookie 复制或整文件
base64 缓冲。普通选择文件请求保持默认处理；未登录时页面 ready 门禁拒绝交付。

### 18.2 原样产品隔离 E2E（实际执行）

DD-12 task `task_017f5729034b` / dispatch `ctx_2717db2a1a90`。报告
`/tmp/flowdrop-dd12-report.md`，原始日志/截图/脚本在 `/tmp/flowdrop-dd12/`。
主 agent 已复核日志、下载 SHA 记录、容器清理证明并查看多文件上传后截图。

在独立 Docker/X11 容器中原样运行上述摘要二进制，**未加载 LD_PRELOAD、诊断 shim
或测试 JS 来改变产品行为**。真实鼠标从 GTK URI-list 源拖入，API 核查会话只查询与下载，
不代发上传。结果：

| 场景 | 结果与证据 |
|---|---|
| 原生初始化 | 两处 Wails 阻断各解除 1 个，native FileList bridge installed 日志出现 |
| 登录前拖入 | 原生接收后由页面 ready 门禁拒绝；无 chooser、无上传、无导航 |
| 登录后拖入非 Composer 区域 | 原生 accepted → chooser handoff → 页面 POST upload 201 → 条目新增，下载 SHA 与夹具一致 |
| 多文件、中文空格名、PNG | 一次真实拖入 3 文件，3 次上传、名称/大小/下载 SHA 均一致；仅验证 PNG 文件传输，不额外认定预览正确 |
| 目录与文件混合 | 只上传普通文件，显示目录提示，不递归 |
| 普通选择文件 | 页面按钮打开普通 GTK chooser，选文件上传成功，未被 pending drop 接管 |
| 相同文件再次拖入 | 每次新增 1 条，下载 SHA 一致 |

总上传请求 **7** 次，均 status=201，与最终 **7** 个条目一致。
原生 drop accepted=5、chooser handoff=4、未登录拒绝=1，计数自洽。
6 个不同夹具的源/下载 SHA 逐项一致（其中 core 夹具重复发送一次）：

| 夹具 | 字节 | 源 SHA-256 = 下载 SHA-256 |
|---|---:|---|
| `dd12-core.txt` | 26 | `6520e682e545b88ef57b8e24e9083cf0d0dce82d7f8290310694876118d9aa42` |
| `dd12 中文 空格.txt` | 23 | `d7767ca8cfa2764f36cd67d1699c31dffa4af8401df41d8a4849a0e9e6976415` |
| `dd12-beta.txt` | 25 | `d454ebb37189f6e67eadb3cf60b30977f17ff9b2ab3f6729c432cdfcf49c87ed` |
| `dd12-note.png` | 89 | `8581e78087dce3ca1c5c43155ba55e2e675a909507a73e326bb26089451bbe51` |
| `dd12-mix.txt` | 22 | `9ce63191d1a32a0e1c3207c65034fcc22c0fbbd192ec904b9a0ecd7f2c221375` |
| `chooser.txt` | 29 | `fe36dd6865761508615f5228074ba58416b574f0eed8c9f08be68cdb5d2052e4` |

### 18.3 干扰、限制与清理

- 普通 chooser 自动化连续按两次 Return 后对话框仍可见，紧随的一次拖拽未被接收。
  Escape 关闭后用相同方法重试成功。报告归因于自动化干扰；本轮证据只确认存在模态
  窗口及其阻止拖拽，不将推测归因为确定的产品或自动化问题。
- 最终轮使用 GTK 原生 URI-list 源，**没有重跑 pcmanfm，更没有覆盖 KDE/Dolphin/portal
  或 Wayland 真机**。此前 pcmanfm 只用于诊断，不能借此声称最终产品的这些来源通过。
- 512 MiB 大文件、上传进度/取消、失败条目、多显示器本轮未实测；上传通路未改，
  不把历史测试当成本轮证据。当前原生单批最多接收 64 个文件和 64 个目录提示，超出
  项计入 rejected 日志，超过该数量的交互未验证。
- 容器 network=none、无发布端口、无 bind mounts/设备/privileged/host 会话共享；文件
  全部 docker cp。图形/私有 DBus/Xvfb 只在容器内。namespace inode 对照本轮因权限
  未取得，隔离依据是 docker inspect 与容器环境记录。
- 容器 `b52c86e56e2b72db34d37e43714bbdc8e9c1efb9bb18050b75d0af5f7032c2cf`
  已 stop/rm；主 agent 复核 docker 列表无 DD-12 容器。其图形/总线资源随容器退出，
  未自动激活 portal/a11y。宿主正式进程核对使用历史清册与启动时间，本轮缺独立 before
  快照，不能声称有完整的前后快照对照；未执行宿主 GUI、系统设置或进程清理。

### 18.4 使用与交付边界

用户从托盘「退出 FlowDrop（停止 Hub）」结束旧实例后，可直接运行：

```bash
./dist/flowdrop-desktop
```

无需先运行启动脚本或额外 Hub；本轮未自动安装、commit 或 push。


## 19. KDE Wayland 拖放修复与定向交付（2026-11-08）

### 19.1 用户证据与最小修复

用户从 Dolphin 拖入时悬停有提示，日志确认两处阻断解除及 native FileList bridge 安装，
但没有 native accepted、chooser handoff 或上传请求。此前 §18 的 X11 容器结果未覆盖该故障。

DD-13 只读诊断与主 agent 审查发现：文件 drop 由原生接管，motion 的接受动作却交给
WebKit 页面协商；页面拿不到 File 时可能返回 action=0，Wayland 因此不投递 drop。
另一处缺陷是 drag-leave 会撤销已经接受、仍在等待异步数据的 drop。
这些源码机制与用户症状一致，但未采集用户 KWin 协议 trace，不将其写作本机事件级根因证明。

DD-14 仅修正该原生流程：匹配 Hub origin、现有文件 target 且源提供 COPY 时，明确
`gdk_drag_status(COPY)` 并消费 motion，避免 WebKit 后置 handler 覆盖；已接受 drop 的
授权跨 leave 保留，仍由一次数据消费、新 drop、5 秒时限判定、页面加载/销毁撤销。
MIME 过滤、路径只在原生侧、chooser token 与页面认证上传通路保持原约束。
源只提供 MOVE 的拖拽回落 WebKit，本桥不承诺接受；超限与大文件边界仍见 §18。

检查：GTK vet、probe vet 通过；38 个无 GUI Go 测试通过，其中新增 4 个回归调用生产
共用动作判定和授权逻辑，覆盖 leave 后数据、外来 context、超时、重复消费和新授权替换。
它们不等同真实 compositor 测试。报告 `/tmp/flowdrop-dd14-report.md`。

### 19.2 最新 release

DD-15 `timeout 180s bash scripts/build-desktop.sh` rc=0；主 agent 独立复核摘要。

| 项目 | 值 |
|---|---|
| 路径 | `dist/flowdrop-desktop` |
| 大小 | 17,020,392 字节 |
| mtime | 2026-11-08 17:51:53 +0800 |
| SHA-256 | `76196ab070de3ae14750a4eb37a3d295d18561d7eee8b4e56a0f06a87f7ab463` |
| 构建 | Go 1.26.8；`gtk3 production`；0.1.0-desktop |

构建报告 `/tmp/flowdrop-dd15-report.md`。未安装、提交或替换运行中的旧实例。

### 19.3 本轮隔离 GUI 验证

DD-16 使用上述原样二进制（两端 SHA 相同，无 shim/LD_PRELOAD/测试 JS 改行为）。
独立 Docker 容器中自起 Xvfb、私有 DBus 与 Weston 14.0.2 x11 backend/pixman；
FlowDrop 与 GTK 源强制 `GDK_BACKEND=wayland`、连接 `wayland-dd16` socket，窗口渲染
在 Weston 输出内。compositor 的底层 X11 不作为应用 X11 结果；未采集协议 trace。

| 场景 | 结果 |
|---|---|
| Wayland GTK 源真实鼠标拖入 | PASS：native accepted → FileList handoff → 页面 POST upload 201 → 新条目 |
| 同文件再次拖入 | PASS：第二条目、第二次 POST 201 |
| 独立 X11 会话核心拖入 | PASS：新数据目录与第二实例，同样 accepted/handoff/POST 201 |
| 内容一致性 | 三次均为 59 B，下载 SHA 与夹具一致 |

夹具/下载 SHA-256：`6397be9bf71e700faab0c3890f9c36b11731623a1aedd2138c80fb36f8e672e7`。
报告 `/tmp/flowdrop-dd16-report.md`；证据 `/tmp/flowdrop-dd16/evidence/`，含日志、API查询/
下载校验、截图、inspect 和清理记录。主 agent 已查看重复上传截图并复核实际日志与下载摘要。

容器 network=none、IPC private、无 host bind/device/额外 caps、非特权、无发布端口；
图形/总线只在容器，文件 docker cp。容器
`fa83aeb8b46a320ce5acdd0d4367b24a694589043c09410a1abe100032c1103c` 已 stop/rm，
主 agent 核对列表无残留；宿主相关进程前后 diff 为空。仅缓存测试镜像保留。

### 19.4 使用与剩余验收

先从旧实例托盘「退出 FlowDrop（停止 Hub）」完全退出，再直接运行：

```bash
/home/bob/vibe-code/flowdrop/dist/flowdrop-desktop
```

只关闭窗口会留在托盘；新进程可能复用旧实例，不能以磁盘文件重建声称已升级运行版本。
无需先运行 sh，也未改用户安装目录。2026-11-08 用户随后明确确认「拖拽可以使用了」，
此前真实 KDE Wayland / Dolphin 故障按用户反馈关闭；未另外采集真机日志。本轮未重跑多文件、
目录、大文件或取消场景，不以 §18 历史结果冒充本轮通过。没有宿主 GUI 实验或桌面设置修改。


## 20. 四位码配对与多地址连接（2026-11-08，本轮）

### 20.1 实现与构建

本轮以临时四位配对码替代账号密码入口。码120秒有效、一次成功后失效、全Hub累计错5次锁定；本机生成新码解除锁并废弃上一值（含确定性碰撞排除）。设备随机令牌只存hash，Android在Keystore保存；Web以HttpOnly Cookie保存。设备在本机设置查看/撤销。Android保存稳定HubID及实际绑定的候选地址，启动/回前台/失联先LAN后Tailscale，无凭据探测ID后恢复该origin的Cookie/CSRF；不自动重放写请求。

| 产物 | 字节 | SHA-256 |
|---|---:|---|
| `dist/flowdrop-desktop` | 17069704 | `03ea9fee1e442301c24eada9126b0427f05d07c66118fe5e5e7d6c19346d0857` |
| `dist/flowdrop` | 11776162 | `48eda3d93639a1f6720392d51ea1a2532e9ee6e693fa2b246a393985d76c50b6` |

2026-11-08 18:52 +0800，Go1.26.8；desktop release tags gtk3/production，版本0.1.0-desktop-pairing；CLI 0.1.0-pairing。前端index-CkHuKcr7.js、index-CKKiHSIo.css。主agent独立sha复核一致，未安装/提交。报告`/tmp/flowdrop-pa09l-report.md`。

### 20.2 已完成检查

- 服务端一次完整`go test ./...`通过，各变更包已定向vet与核心race检查。配对核心16顶层+2子测试、CLI5、API44顶层、Hub21；完整服务端检查对未变包使用缓存，没有冒充全部新跑。
- desktop全模块65测试与vet通过；包含真实临时Hub本机桥生码→HTTP配对→列表→撤销；JS语法检查通过。
- Android 101 JVM测试（0失败/跳过）与完整`compileDebugAndroidTestKotlin`通过，未排除旧LoginScreenTest；纯连接控制15项，地址选择15项，数据层53项。AndroidTest仅编译，未执行真机或模拟器。
- Chromium容器端到端16/16通过（0失败/跳过），原样CLI。包括错误码、配对与刷新、解除后401、双context实时文字、图片预览/原字节下载、APK/普通文件/多文件/SHA、超限413、离线同步与Hub重启。容器私有`http://172.19.0.2:8096`的非secure context与手动复制回退实际验证；不当作用户真实Wi-Fi。报告`/tmp/flowdrop-pa08p-report.md`。
- 实际Wails X11容器：设置真实点击添加设备并显示四位码/倒计时→inbox真实键盘配对→POSTpair200→原生GTK URI-list鼠标拖放→FileList→POSTupload201→条目64B；独立读取设备下载后SHA一致。主agent查看2张截图及日志。未用API上传代替拖拽。报告`/tmp/flowdrop-pa08g-report.md`，证据`/tmp/flowdrop-pa08g/{evidence,logs}`。

### 20.3 隔离与局限

GUI均独立Docker，无host network/PID/IPC、host bind/display/bus/socket/device、特权或发布端口。cap-drop ALL/no-new-privileges；浏览器仅自建internal网络，桌面network none。未启动宿主GUI/临时DBus/portal/a11y/Xvfb，未操作KDE/RustDesk/代理/用户实例。两个容器及internal网络均stop/rm，主agent核对列表无残留，镜像缓存保留。

镜像缺CJK字体，桌面截图中文显示方框，数字/文件名可读；未因此更改产品或宿主字体。设置内GUI点击撤销未执行，后端HTTP/WS及Web解除已由自动测试覆盖。成功消费的码在设置仍倒计时到过期，不能再消费；每添加一台设备应重新生成。此为显示限制，本轮不扩大修复范围。

真实Android Keystore、手机Wi-Fi/Tailscale切换和本轮KDE/Dolphin真机尚未验证；§19用户反馈属于此前拖拽产物，不能替代本轮配对与切网真机验收。

### 20.4 Android实际网络层集成与APK

新增`RealHubPairingTest`对原样CLI启动的两个具体回环地址执行真实HTTP：一次native配对、文字/103B有效PNG/98317B普通文件上传，模拟电脑读回原字节与SHA；电脑反向发送文字与65543B文件，Android下载核对；第二origin一次Bearer GETsession重建Cookie/CSRF、写重放次数0；同地址换成另一真实Hub时ID不符不发凭据且保留记录；logout后旧token拒绝而另一设备仍可用。新增1项与全量102 JVM均0fail/0skip。故障选择通过probe注入、kind=other、存储InMemory，不能当真实Tailscale/Wi-Fi/Keystore或Android UI验收。报告`/tmp/flowdrop-pa08i-report.md`。

测试命令需显式提供已构建测试二进制：`FLOWDROP_LIVE_TEST_BIN=/home/bob/vibe-code/flowdrop/dist/flowdrop ./gradlew --offline :app:testDebugUnitTest`（cwd android）。新增集成test当前固定本次CLI SHA；未来重建不同版本需审查并更新该验收输入，不应误把摘要不符解释成业务失败。测试未设置环境变量会明确跳过该项，102全通过的本轮运行实际提供了变量。

Hub重启后旧keep-alive首个请求可能报IOException；禁用OkHttp内部连接重试是避免写重放的既定方案。集成测试明确记录该失败并清自己的连接池后重试，不能据此声称App运行时已实测自动恢复该情形；不额外修改GET重试策略。

APK由`timeout 240s ./gradlew --offline :app:assembleDebug`成功构建（4s），Gradle源文件与dist副本逐字节一致：

| 产物 | 字节 | SHA-256 |
|---|---:|---|
| `dist/flowdrop-android-debug.apk` | 10209256 | `d00a6808ec5f5190354d9d1191e6a4fb1dd23cda8aaed0ca1d62357531c4e5e4` |

2026-11-08 19:06:26 +0800；apksigner verify通过（v2签名，Android Debug证书），包`dev.flowdrop.android`、versionCode1/versionName0.1.0、minSdk26/targetSdk35。报告`/tmp/flowdrop-pa09a-report.md`。主agent独立复核三产物SHA，未自动安装或启动用户实例，未提交。

### 20.5 用户验收步骤

1. 从旧实例托盘「退出 FlowDrop（停止 Hub）」完全退出，再运行`dist/flowdrop-desktop`；只关闭窗口会复用旧进程。无需先运行sh。
2. 若要内网和Tailscale都可用，在设置启用双模式、选择两者本机地址并保存；两端Tailscale须在线且策略允许。
3. 设置点「添加设备」生成码。电脑内容窗口与每台手机分别配对，每添加一台设备都重新生成码（上一成功码不可重用）。手机安装`dist/flowdrop-android-debug.apk`，同Wi-Fi可发现，异地首次手填Tailscale地址；本轮未做扫码/TS节点枚举。
4. 配对后Android记住同一Hub的候选地址，启动/回前台/失联优先LAN后Tailscale；断开的上传/发送不自动重放，需要用户重试。浏览器仍按origin保存Cookie。
5. 用户真机确认配对、文字/图片/文件双向收发与离家/回家切换；本轮没有替用户执行该步骤。

## 21. agent 投递 CLI（`flowdrop login` / `flowdrop send`，2026-11-09，本轮）

需求：让同机 agent 直接把文件/文字投递到 FlowDrop。主 agent 决策为**只加 CLI，服务端零改动**：完全复用既有配对、Bearer + cookie jar + CSRF 双提交和 `/api/v1/items/{upload,text}`，不新增端点、不改鉴权语义。

### 21.1 交付内容

| 文件 | 说明 |
|---|---|
| `server/internal/sendcli/sendcli.go` | 客户端包：凭据读写（0600 原子写）、`Pair`、`Connect`（Bearer→CSRF/cookie/limits）、`SendFile/SendReader/SendText`、`*Error`/`ErrUnauthorized`/`ErrNoCredentials` |
| `server/cmd/flowdrop/send_cmd.go` | `login`（hub 推导/本地生成码/设备名/凭据保存）与 `send`（文件、`--text [TEXT\|-]`、`--link`、`--name -` stdin spool、`--json` NDJSON、退出码 0/1/2、429 停发并标 `skipped`） |
| `server/cmd/flowdrop/main.go` | 子命令分发与 usage（+9 行） |
| 测试 | `sendcli_test.go`(13)、`send_cmd_test.go`(21)，均打真实 `api.New` + SQLite + storage 的 httptest Hub，不 mock 客户端 |

契约修正：`store.Blob` 实际序列化文件名为 `blob.name`，主 agent 将冻结接口的 `original_name` 改为 `name` 并删除 worker 加的兼容 `UnmarshalJSON`。

### 21.2 自动测试（本轮实际执行）

| 命令 | 结果 |
|---|---|
| `gofmt -l ./cmd/flowdrop ./internal/sendcli` | 无输出 |
| `go vet ./cmd/flowdrop/... ./internal/sendcli/...` | 通过 |
| `go test ./cmd/flowdrop/... ./internal/sendcli/... -count=1` | 21 + 13 PASS，0 FAIL / 0 SKIP |
| `go test -race ./cmd/flowdrop/... ./internal/sendcli/... -count=1` | 通过 |
| `go build ./...` | 通过 |

### 21.3 主 agent 独立真实端到端（非 worker 证据）

临时 `mktemp` 配置 + 临时 data_dir + `127.0.0.1:52029` 独立 Hub（不涉及用户实例与 `~/.config/flowdrop`）：

- `flowdrop login -config <tmp>/config.json`（**不带 `--hub`/`--code`**，同机本地生成码）→ rc=0，`cli.json` 权限 `600`，hub/设备名/device_id 正确。
- `flowdrop send --json <payload.txt>` → rc=0，NDJSON 中 `blob.sha256=39cb23ab…e352d`、`size_bytes=40`。
- Bearer 鉴权下载 → rc=0，两端 SHA-256 与字节数一致。
- `printf 'hello from agent' | flowdrop send --text -` → rc=0，stdin 文本条目。
- `flowdrop send --link https://example.com/agent` → rc=0。
- 仅按 PID 停止该临时 Hub，`ps` 确认已退出，临时目录删除；未触碰其它 flowdrop 进程。

### 21.4 产物

| 产物 | 字节 | SHA-256 |
|---|---:|---|
| `dist/flowdrop` | 12320930 | `d32cf3bf60e34fa249ef33e11b862ab8803c82a00d805b3b83762c5170e62a82` |

`timeout 240 bash scripts/build.sh` rc=0（版本 0.1.0，2026-11-09）。未安装、未 commit、未 push。

### 21.5 文档

- `docs/DEPLOY.md` §3.1「命令行设备（同机 agent / 脚本投递）」：二进制来源、`login` 同机/异机路径、凭据位置与权限、`send` 全部形态与退出码、429/撤销行为、`FLOWDROP_HUB`+`FLOWDROP_TOKEN` 不落盘用法、明文 HTTP 提醒、实测命令清单。
- `README.md`：功能列表一条 bullet + 「命令行投递（agent / 脚本）」小节（指向 §3.1）。

### 21.6 未验证项（单列，不当作通过）

**2026-11-10 补验（SEND-04～07）**：429 → `skipped`、撤销设备后的提示、HTTPS/`cookie_secure=true`、超限与 413/507 四项已用真实临时 Hub 验证通过，见 §21.8。以下仍为未验证项：

- 指向桌面 App 真实配置与 data_dir 的 `login`（本次只用临时配置）。
- Windows/macOS 上的 CLI 路径与 hostname 行为。
- 413 的 `MaxBytesReader` 超大体分支、507 的阈值边界与恢复行为（§21.8 只验证了确定触发路径）。
- 真实桌面 GUI「撤销设备」按钮路径（SEND-05 用与 `Store.RevokeDevice` 相同的 SQL 撤销）。
- 公共 CA 证书、浏览器/WebView 的 Secure cookie 实际写入、TLS 代理下的 WebSocket（SEND-06 未覆盖）。
- CLI 上传条目的 `sender_device` 为空字符串（SEND-05 顺带观察，未判定是否为缺陷；未验证 Web 端显示影响）。

### 21.7 用户验收步骤

1. 生成/更新 CLI：`bash scripts/build.sh`，得到 `dist/flowdrop`。
2. 同机用它登录：`dist/flowdrop login -config ~/.config/flowdrop/config.json`。
3. 投递：`dist/flowdrop send --json <文件>`，或 `printf 'x' | dist/flowdrop send --text -`。
4. 需要撤销时在桌面设置里删除 `cli-<主机名>` 设备，再重新 `login`。

### 21.8 真实场景补验（SEND-04～07，2026-11-10）

四个独立 pi worker 并行、只验证不改代码；统一使用现成 `dist/flowdrop`（12,378,274 B，SHA-256 `9da66eaf…94d8`）、临时 config/data_dir/空闲端口；未启动 GUI、未触碰用户实例（PID 1768910）与 `~/.config/flowdrop`；进程均按确切 PID 停止，临时目录已删除。

| 场景 | 实测 | 结果 |
|---|---|---|
| 429 限流 → skipped（SEND-04） | Hub `127.0.0.1:44791`，`api_limit_per_minute=2`：`send a b c` → a/b 201、c `rate_limited`、rc=1；紧接第二次 `send` → a `rate_limited`、b/c `skipped`（`not sent: rate limited`）。Hub 日志确认 429 后无上传，DB 只有 a.txt/b.txt | PASS |
| 撤销设备（SEND-05） | 用与 `Store.RevokeDevice` 同一条 SQL 置 `revoked_at`（changes=1）后重启 Hub：`send` rc=1，stderr 原文 `flowdrop: device token was rejected (revoked in settings?); run \`flowdrop login\` again`；`GET /auth/session` 返回 200 + `authenticated:false`（CLI 映射 `ErrUnauthorized`，不是 HTTP 401）；`inbox_items=0`；重新 `login` 后上传 201、`inbox_items=1` | PASS |
| HTTPS + Secure cookie（SEND-06） | Hub `127.0.0.1:57863`（`cookie_secure=true`）+ socat TLS 终止 `33245`（自签证书 SAN=IP:127.0.0.1）：`login`/`send` rc=0，服务端 sha256 与本地一致；`flowdrop_session`/`flowdrop_csrf` 均 `Secure+SameSite=Strict`（session 另 HttpOnly）；`SSL_CERT_FILE`/`--cacert` 真实校验，未跳过 | PASS |
| 上传上限（SEND-07） | Hub A `max_upload_bytes=1024`：2 KiB 文件 CLI 本地拒绝 rc=1 `too_large`，hub.log 无 POST；curl 直传 413 `too_large`。Hub B `min_free_bytes=2^60`：小文件 CLI rc=1 `disk_full`；curl 直传 507 `disk_full` | PASS |

观察（未修复、非阻塞）：

- `GET /api/v1/auth/session` 不计入每设备 API 限流（SEND-04）：单次多文件发送时 429 只落在使预算耗尽的那一项，后续输入才标 `skipped`。
- `/api/v1/auth/session` 的 `hub.endpoints` 返回 Hub 自身明文地址而非外部 HTTPS 代理地址（SEND-06）；Hub 不知道前置 TLS 终止，客户端/文档不应把该值当外部 URL。
- CLI 上传的 item `sender_device` 为空字符串（SEND-05）；已在 SEND-08 补齐为客户端设备标签，见 §21.9。

未验证（单列）：413 的 `MaxBytesReader` 超大体分支、507 阈值边界与恢复、公共 CA 证书、浏览器/WebView Secure cookie 写入、TLS 代理下 WebSocket、真实桌面 GUI 撤销按钮、Windows/macOS CLI。

### 21.9 CLI sender 标签（SEND-08，2026-11-10）

SEND-05 观察到 CLI 投递的 `sender_device` 为空。该字段是客户端提供的展示标签（服务端只做 `sanitizeSender` 清洗/64 rune 截断，不按设备派生），Web 端 `ItemCard` 已会渲染；因此本轮只改 CLI 发送侧，不改服务端协议与 Web。

实现（仅 4 个文件，+308/−22）：

- `sendcli.Client` 新增 `SetSenderDevice(name)`（trim；空值表示不发该字段）。
- `SendReader`：sender 非空时在 multipart `file` 之前写 `sender_device`；字段进入 prefix，文件体仍流式，`ContentLength` 保持 head+size+tail。
- `SendText`：JSON 增加 `sender_device,omitempty`。
- `send_cmd.go`：`resolveSendAuth` 返回 sender——凭据路径用 `creds.DeviceName`（空则 `defaultDeviceName()`），`FLOWDROP_TOKEN`/`FLOWDROP_HUB` 路径用 `defaultDeviceName()`（`cli-<hostname>`）；`--name` 仍只作 stdin 文件名。

自动测试（主 agent 独立执行，非缓存）：

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./internal/sendcli/... ./cmd/flowdrop/...` | PASS（0.049s / 0.076s） |
| `go test -count=1 -race ./internal/sendcli/... ./cmd/flowdrop/...` | PASS（1.360s / 1.633s） |
| `gofmt -l internal/sendcli cmd/flowdrop` | 无输出 |
| `go vet ./...` | rc=0 |

覆盖点：设置后文件/文本 item 的 `sender_device` 等于标签；未设置时仍为空；multipart 的 part 顺序与精确长度；`login --name cli-laptop` 后文件/文本/stdin 文件均携带 `cli-laptop` 且 `--name` 不变成 sender；env-token 路径 sender 以 `cli-` 开头。

未验证（单列）：真实浏览器收件箱渲染（本轮无 Web 改动，验证停在 item JSON/存储层）；真实 Hub 上的 CLI 投递（仅 httptest 真实 handler）。`dist/flowdrop` 未重建，现有产物不含本改动；`bash scripts/build.sh` 后生效。

### 21.10 二维码配对（QR-02/03/04，2026-11-10）

契约见 `docs/PAIRING.md`「2026-11-10 补充」与 `docs/PRD.md` §21：二维码只是手机输入 4 位码的快捷方式，不改 `POST /api/v1/auth/pair`，不携带 device token。

实现：

- Desktop（QR-02）：`desktop/pairing_qr.go` 生成 `flowdrop://pair?v=1&code=DDDD[&url=...]` 与 `data:image/png;base64`（`skip2/go-qrcode`，256px Medium）；`CreatePairingCode` 从 `manager.URLs()` 按 LAN → 100.64/10 → 其它选一个非回环地址，无可用地址或 PNG 失败时 QR 为空、文字码照常；`index.html`/`app.js`/`style.css` 在四位码旁展示，复用过期/使用/重生成/Hub 停止/切换地址的清理路径。
- Android（QR-03）：`data/PairingPayload.kt` 纯 JVM 解析器；`ui/QrScannerScreen.kt` CameraX 1.3.4 + zxing-core 3.5.3，CAMERA 运行时权限，拒绝后保留手动输入；`AppViewModel.applyScannedPairing` 只预填码与首个候选地址（地址已填则保留），从不自动配对；不依赖 Play Services。

主 agent 独立验证（非缓存）：

| 检查 | 结果 |
|---|---|
| `cd desktop && gofmt -l .` | 无输出 |
| `cd desktop && go vet -tags gtk3 ./...` | PASS（需用户本地 GTK sysroot 的 `PKG_CONFIG_PATH`/`CGO_*`；宿主未装 dev 包，直接跑会 build failed） |
| `cd desktop && go test -tags gtk3 -count=1 ./...` | 四包 PASS（desktop 0.694s，appsettings/hubmgr/network 同轮通过） |
| `node --check desktop/assets/app.js` | PASS |
| `cd android && ./gradlew :app:testDebugUnitTest --rerun-tasks` | BUILD SUCCESSFUL，24 tasks executed，146 tests / 0 failures / 0 errors（含既有 1 skip） |
| `scripts/build.sh 0.1.0` + `scripts/build-desktop.sh` | 均 exit 0；`dist/flowdrop` 12.4MB，`dist/flowdrop-desktop` 17.7MB（release，gtk3 production） |
| `./gradlew :app:assembleDebug` | `android/app/build/outputs/apk/debug/app-debug.apk`，14,256,487 bytes；合并清单含 CAMERA，`uses-feature required=false` |

真实解码（QR-04 自动化部分）：用一次性测试调用 `pairingCodeView("0421", [loopback, 100.125.168.63, 192.168.31.184])` 导出应用实际生成的 PNG（临时测试文件已删除），再用 zxing-core/javase 3.5.3 解码，结果与期望载荷逐字符一致：

```
flowdrop://pair?v=1&code=0421&url=http%3A%2F%2F192.168.31.184%3A8080
```

地址选择正确跳过了 `127.0.0.1` 与 Tailscale 地址，优先 LAN；前导 0 保留。

未验证（单列，不当作通过）：

- 桌面窗口内二维码实际渲染（遵守宿主桌面保护，未启动 GUI）；
- Android 真机相机解码、系统权限弹窗、暗光/反光等真实扫码条件（模拟器无相机）；
- 用户手机扫描桌面屏幕完成一次真实配对（QR-04 剩余项，需用户操作）；
- 安装版 `~/.local/bin/flowdrop-desktop` 未更新；本轮只产出 `dist/`，未 commit/push。

## 22. 自动绑定与热更新（`auto_bind`，2026-11-09，本轮）

需求：不再限制「主地址 + 一个额外地址」，改为自动绑定本机所有可用地址（RFC1918 LAN + Tailscale），并在地址变化时热更新，不重启 Hub。用户明确选择该方案（选项 C）并接受"派生地址尽力而为"的语义变更。

### 22.1 交付内容

| 文件 | 说明 |
|---|---|
| `server/internal/netbind/`（新增 208+225 行） | 可注入的地址枚举 + `Classify`（LAN/Tailscale/Virtual/Other/Loopback）+ `Targets`（筛选/排序/去重）+ `Diff` |
| `server/internal/api/server.go`（+`dynamic_hosts_test.go`） | `SetBoundHosts` + `hostAllowed`，`hostCheck` 与 Origin 校验共用静态+动态集合 |
| `server/pkg/hub/autobind.go`（新增 421+346 行）+ `hub.go` | 启动时同步绑定、5s 重扫、增量 add/remove、`URLs()` 动态、mDNS 重注册、`Stop` 等重扫退出 |
| `server/internal/config/config.go` | `auto_bind` + `FLOWDROP_AUTO_BIND`；`hub.Options.AutoBind *bool`（nil=用配置） |
| `server/cmd/flowdrop/main.go` | `doctor` 只读打印 `auto_bind` / `auto_bind target`；usage 一行 |
| `desktop/**`（`appsettings`/`hubmgr`/`service.go`/`assets`） | 设置窗口「自动绑定本机所有可用地址」开关 + 实时绑定列表；`ApplyAutoNetwork` |
| 文档 | `docs/DEPLOY.md` §5.9 + §9 配置行、`README.md`、`docs/ARCHITECTURE.md` §12.4 |

### 22.2 语义（与显式配置的区别，必须保留）

- 自动绑定：派生地址**尽力而为**，单个失败只 warn、5s 后重试，成败通过日志/`URLs()`/桌面列表/`doctor` 可见。
- 显式 `additional_listen`：仍是**全有或全无**，任一失败回滚并拒绝启动。
- 主监听端口为 `0` 时禁用自动绑定（临时端口不可复用）；地址消失会中断该地址上的在途请求。

### 22.3 自动测试（本轮实际执行）

| 命令 | 结果 |
|---|---|
| `go test ./internal/netbind/...` | 5 个表驱动测试，30 PASS |
| `go test ./internal/api/...`（含 `-race`） | 通过（含 4 个动态 Host 测试） |
| `go test ./pkg/hub/...`（含 `-race`） | 9 个新自动绑定测试 + 30 个既有测试全部通过 |
| `go test ./internal/config/... ./cmd/flowdrop/...` | 39 PASS |
| `cd desktop && go vet -tags gtk3 ./... && go build -tags gtk3 ./... && go test ./internal/...` | 全部通过，43 PASS |
| `node --check desktop/assets/app.js` | 通过 |

注意：桌面模块**必须**用 `-tags gtk3`（`scripts/build-desktop.sh` 一直如此）；本机无系统 GTK/WebKit 开发包，使用 `~/.local/share/flowdrop-desktop/sysroot` 前缀。

### 22.4 主 agent 独立真机验证

临时配置（`auto_bind=true`、临时 data_dir、`127.0.0.1:57121`）：

- `doctor` → `auto_bind ok on` + `192.168.31.184:57121` + `100.125.168.63:57121`。
- `ss -ltnp` 显示同一 PID 的**三个**监听：`127.0.0.1`、`192.168.31.184`、`100.125.168.63`。
- `/api/v1/health`：三个地址均 **200**。
- Host 白名单：`Host: 192.168.31.184:57121` → **200**（证明 `SetBoundHosts` 生效）；`Host: 203.0.113.9:57121` → **421**（未放宽）。
- 全链路：`flowdrop login --hub http://192.168.31.184:57121`（本地生成码）rc=0，`flowdrop send` 走 LAN 地址 rc=0 并输出 sha256。
- 按 PID 停止（`HUB_STOPPED`），临时目录删除；未使用 `pkill`，未影响用户 `flowdrop-desktop` 实例。

### 22.5 产物

| 产物 | 字节 | SHA-256 |
|---|---:|---|
| `dist/flowdrop` | 12370082 | `d1fbb1a07f703be73125880b1f868e77df1dad66de183e74b49d968ba3530e1f` |

`timeout 300 bash scripts/build.sh` rc=0。未安装、未 commit、未 push。

### 22.6 未验证项（单列，不当作通过）

- **地址增删热更新**的真实网卡变化：只有注入 `Scan` 的单测（`127.0.0.2`/`127.0.0.3` 增删），未在真实 Wi-Fi/VPN 切换中验证。
- 端口 `0` 禁用自动绑定的真实进程行为（仅单测与 doctor 文案）。
- 派生地址绑定失败的 warn + 重试在真实环境中的表现（单测用 `192.0.2.1`）。
- 真实 mDNS 重注册（测试中禁用 discovery；每次变化时"关闭并重新注册"仅代码审查）。
- 桌面设置窗口的开关与实时列表：仅代码审查 + `node --check`，**未启动 GUI 窗口**（宿主桌面保护），未在真机点击验证。
- Windows/macOS 桌面与 `wg*`/CGNAT 边界组合（`wg0` + `100.64/10` 会被判为 Tailscale）。

### 22.7 用户验收步骤

1. `bash scripts/build.sh`；Hub 重启后 `./dist/flowdrop doctor -config ~/.config/flowdrop/config.json` 应列出 `auto_bind on` 与每个会绑定的地址。
2. 在配置里加 `"auto_bind": true`（或 `FLOWDROP_AUTO_BIND=true`），重启 Hub；`ss -ltnp | grep flowdrop` 应看到同端口多个地址。
3. 桌面 App：设置 → 勾选「自动绑定本机所有可用地址」→ 保存，窗口下方应列出真实监听地址；换网络后最多 5 秒列表更新。
4. 换网络后从手机/另一台机器用新地址访问应直接可用（无需重启 Hub）；若某地址已失效，该地址上的在途请求会中断，重试即可。

### 22.8 追加：容器网桥地址也绑定（NET-07，对齐 LocalSend）

用户要求与 LocalSend 对齐：本机 `docker0`/`br-*` 上的**私有**地址也要绑。改动：

- `netbind.Targets` 现按「私有 IPv4」判定：`KindLAN`、`KindTailscale`、以及 IP 为 RFC1918 的 `KindVirtual`（docker0、br-*、virbr*）都绑；回环、link-local、**公网地址仍不绑**；排序 LAN → Tailscale → Virtual。
- Hub 记录每个自动绑定地址的 `netbind.Kind`（`autoKinds`），新增纯函数 `filterAdvertised` 把 Virtual 地址从 **mDNS 广播集合**剔除；`URLs()`、HTTP 绑定与 `SetBoundHosts` 仍用完整集合。两个 `startDiscovery` 调用点都传过滤后的集合。
- 文档同步：`DEPLOY §5.9` 的排除行改写，并新增「绑到 docker0 意味着本机容器可访问 Hub（明文 HTTP）」提示；`ARCHITECTURE §12.4` 补「广播集合 ≠ 绑定集合」。

自动测试：`netbind`（含 172.17.0.1/172.18.0.1 用例）、`pkg/hub`（`filterAdvertised` 表驱动 + 集成）与 `-race` 全部通过。

主 agent 独立真机冒烟（临时配置、空闲端口、`auto_bind=true`）：

- `doctor` 打印 4 个 target：`192.168.31.184`、`100.125.168.63`、`172.17.0.1`、`172.18.0.1`；
- `ss -ltnp` 显示同 PID 的 **5 个**同端口监听（含 `127.0.0.1`）；
- 5 个地址 `/api/v1/health` 全部 **200**；
- mDNS 日志只对回环与 Tailscale 报"不可广播"，容器网桥未参与广播；按 PID 停止，临时目录删除。

产物：`dist/flowdrop` 12,378,274 B（`9da66eaf…94d8`）、`dist/flowdrop-desktop` 17,620,904 B（`5f8ef6f9…c031`）。未安装到 `~/.local/bin`（等用户确认）、未 commit。

未验证项不变（真实网卡增删热更新、真实 mDNS 报文、桌面 GUI 点击），另新增：容器网桥地址上的真实容器访问未验证。

### 22.9 追加：自动绑定改为默认开启 + 安装（用户确认后）

用户要求：`auto_bind` 现在打开，并且**新安装默认打开**。

改动：

- `desktop/internal/appsettings/appsettings.go`：`Default()` 返回 `AutoBind: true`（新装/首次运行即自动绑定；手动选择仍可关闭）。已保存的旧文件没有该字段，读回仍是 `false`，不会被动改用户的既有选择。
- `desktop/main.go`：`startHub` 补 `AutoBind: st.AutoBind`——此前只有"设置里保存"的路径会传，**首次启动会忽略已保存的开关**（这是 NET-05 范围限制留下的缺口）。
- `desktop/main.go`：`preferredHubURL` 新增"主监听是回环则直接用回环"的优先级，并新增 `isLoopbackURL`。原因：自动绑定下主监听是 `127.0.0.1`、LAN 只是附加监听，旧逻辑会把窗口开到 LAN 地址；会话 Cookie 按 origin 隔离，机器换网络后窗口 origin 会跟着变。双模式（主监听就是 LAN）行为不变。
- 用户配置：`~/.config/flowdrop/desktop.json` 加 `"auto_bind": true` 并移除 `additional_bind_addresses`（两者互斥），原文件备份为 `desktop.json.bak-20261010-*`。
- 已按用户明确要求执行 `scripts/install-desktop.sh`：`~/.local/bin/flowdrop-desktop` = `dist/flowdrop-desktop`，17,620,904 B，SHA-256 `7fa0efe7…d311`（09:21:57）。

验证：

| 命令 | 结果 |
|---|---|
| `go test -tags gtk3 ./internal/...` | 全部通过（含 `Default().AutoBind` 断言） |
| `go test -tags gtk3 .`（桌面根包，含 service/hub 集成测试） | 通过（此前因 `preferredHubURL` 选 LAN 而失败的 `TestServiceApplyModeSwitchesAtRuntime` 现通过） |
| `go vet -tags gtk3 ./...` | 通过 |
| 主 agent 真机冒烟（等价于用户保存的配置：主监听 `192.168.31.184` + `auto_bind=true`，临时 data_dir/端口） | 监听恰为 `192.168.31.184`、`100.125.168.63`、`172.17.0.1`、`172.18.0.1` 四个同端口地址，逐一 `/api/v1/health` 均 **200**；按 PID 停止、临时目录删除 |

未验证：桌面窗口内的实际点击与列表渲染（未启动 GUI，宿主桌面保护）、真实 8080 端口上的实例行为（用户自行启动）。

### 22.10 追加：运行状态只显示一个地址（用户反馈后修复）

现象（用户在运行中的窗口里看到）：自动绑定生效、网络绑定列出 4 个地址，但「运行状态 → 可供手机访问的地址」只有 `http://192.168.31.184:8080/`，「实际绑定」也只显示一个。

原因（两处独立的"只报主地址"假设，都写在 `desktop/assets/app.js`）：

- 运行状态读的是 `state.url`（Hub 主监听 URL）单值；
- 「实际绑定」由**已保存设置**拼出（`bind_address` + `additional_addresses`），自动绑定下 `additional_addresses` 为空 → 永远只有一个。

修复：

- `#hub-url` 单值改为 `#hub-urls` 列表（`renderHubURLs`），按 `state.access_urls` 逐行列出每个监听地址，主地址行加标签，两个以上地址时显示一行说明（手机用 LAN 行、Tailscale 用 100.x 行、容器用 172.x 行）；`复制`/`在浏览器打开` 仍只作用于主地址（`state.url`），粘贴目标是单个 URL。
- 「实际绑定」改用 `runningListenAddrs(s)`：Hub 运行时取 `state.urls`（真实监听集合，去掉 scheme），未运行时才退回已保存设置。
- `style.css` 加 `.hub-urls/.hub-url/.hub-url-primary/.hint-inline`。

验证：

| 检查 | 结果 |
|---|---|
| `node --check desktop/assets/app.js` | 通过 |
| 元素 id 交叉检查（app.js 里 57 个 `$("id")` vs index.html 的 id） | 0 缺失 |
| `go test -tags gtk3 . ./internal/...` | 通过 |
| 重装 `~/.local/bin/flowdrop-desktop` | SHA-256 `c5872c1f…d582`，二进制内含 `hub-urls` / `runningListenAddrs` |

未验证：窗口内实际渲染（未启动 GUI，宿主桌面保护）——运行中的实例是旧二进制，需用户从托盘退出并重启后观察。

**用户验收（2026-11-10 19:23，通过）**：用户用 `dist/flowdrop-desktop`（PID 1768910，SHA `c5872c1f…d582`，与安装版一致）重启；核对该实例的实际监听为 `192.168.31.184:8080`、`100.125.168.63:8080`、`172.17.0.1:8080`、`172.18.0.1:8080` 四个同端口地址。用户在窗口内确认：运行状态列出全部 4 个地址、「实际绑定」也是 4 个、复制/在浏览器打开只作用于主地址。此前「只显示一个地址」的反馈已闭环。

同日清理验证残留：09:22 启动的临时 Hub（PID 1246048，`/tmp/fd-def-ve4NQ0`，端口 34969）于 19:27:11 自行优雅退出（日志 `mDNS discovery withdrawn` + `hub stopped`），端口已释放，临时目录已删除；用户正式实例未受影响。

## 23. Windows 客户端远程连接 Hub 失败：两端 LAN 网段相撞（2026-11-10，诊断）

现象：用户从 Windows 上的 FlowDrop 桌面端连接 Linux 上的 Hub 失败；同一 Windows 用浏览器访问该 Hub 却能完成配对。

环境与实测（Windows `HappyDay`，经 Orca 配对通道在其 cmd.exe 内执行）：

| 检查 | 结果 |
|---|---|
| `ipconfig` | WLAN `192.168.31.215`、Tailscale `100.86.13.51` |
| Linux Hub 监听（`ss -ltn`，PID 1768910） | `192.168.31.184:8080`、`100.125.168.63:8080`、`172.17.0.1:8080`、`172.18.0.1:8080` |
| `curl http://192.168.31.184:8080/` | `000`（不可达） |
| `curl http://100.125.168.63:8080/` | `200` |
| `type %APPDATA%\flowdrop\desktop.json` | `mode=local`、`bind_address=192.168.31.215`、`port=8080`、`auto_bind=true`（Windows 端自己起 Hub，未处于远程模式） |
| `tasklist` | `flowdrop-desktop.exe` PID 25492 正在运行 |

结论：两端 LAN **都是 `192.168.31.0/24`** 但属于不同物理网络，Windows 把 `192.168.31.184` 当成本地网段直连，报文不出网关，故 LAN 地址必然不可达；tailnet 地址正常。加上 Windows 桌面端当时仍是 `local` 模式，因此表现为"连不上 Linux 的 Hub"。用户把远程地址改为 `http://100.125.168.63:8080` 后连接成功。

代码侧诱因（未修改）：`preferredHubURL()`、`pickPairingURL()` 与 `desktop/assets/index.html:72` 的示例地址都优先/示范 LAN 地址，冲突场景下给出不可达地址。已登记为 `TASKS.md` UX-01（可达性提醒）与 UX-02/03（桌面端发现 Hub、Tailscale peer 候选）。

未验证：未区分 `192.168.31.184` 的不可达是网段相撞还是 Linux 侧防火墙所致；tailnet 路径已可用，未继续排查。未改动用户 Windows 端配置与运行中进程。

## 24. 桌面端 Hub 发现（mDNS + Tailscale peer，2026-11-10）

实现见 `TASKS.md` UX-02/UX-03。本轮在本机（Linux，`192.168.31.184` / `100.125.168.63`）实跑，未启动 GUI：

| 检查 | 命令/方式 | 结果 |
|---|---|---|
| 局域网 mDNS 浏览 | 临时测试调用 `lanbrowse.Browse(ctx, 4s)` | 发现 `http://192.168.31.184:8080（FlowDrop-bob-hpelitedesk880g6-2）`，1 条 |
| Tailscale peer 解析 | 临时测试调用 `tailnet.Peers` | CLI 可用；`tailscale status --json` 的 10 个 peer 中带 `100.64/10` 地址的被解析（含当时在线的 `HappyDay`/`100.86.13.51`），无 CGNAT 地址的 peer 被丢弃，地址去重 |
| 端到端 `DiscoverHubs` | 临时测试调用 `SettingsService.DiscoverHubs` | 8.01s 内返回 1 条 LAN 候选；对端 peer 因未运行 Hub 全部探测失败并被丢弃（符合"宁可少列，不列不可达地址"的约定） |
| 单元测试 | `go test -tags gtk3 -count=1 ./...` | `desktop` 及 5 个 internal 包全 ok（新增 lanbrowse 7 项、tailnet 6 项、服务层 9 项） |
| 静态检查 | `gofmt -l`、`go vet -tags gtk3 ./...`、`node --check desktop/assets/app.js` | 全部通过 |

未验证：真实 Wails 窗口内「扫描」按钮与候选列表的观感与交互；mDNS 在多网卡/跨网环境下的实际覆盖面（mDNS 是链路本地协议，跨网必然看不到）；Windows 端的同一功能（执行主机下线）。

结论要点：局域网发现只能覆盖同网段，用户当前"两台机器各自 192.168.31.x 但不在同一局域网"的场景由 tailnet 候选覆盖；候选只是地址提示，仍需 Hub 自身配对流程。


## 桌面收件箱分类标签（TYPE，2026-11-12）

- 实现：全部/文字/链接/图片/视频/音频/文档/压缩包/其他；服务端统一派生category，按分类后分页，不漏掉首屏30条之外的内容；Web切换重置分页并防旧请求污染。
- 自动测试：前端TypeScript通过；服务端Go全13包与vet通过；独立真实React renderer（非浏览器/WebView）144断言通过，覆盖切换乱序响应、分类分页、实时新增、空态及删除回滚。报告 `/tmp/flowdrop-type03-report.md`，脚本 `/tmp/flowdrop-type03-lab/run-all.sh`。
- 实际网络：新dist/flowdrop临时独立Hub分类查询与静态资源200/非法分类400通过，临时进程准确停止、端口释放和数据凭据清理。报告 `/tmp/flowdrop-type04-report.md`。
- 构建：Hub 12,394,658 B（SHA256 9986f88c5ee7fa3adc6ebf016e1cd86d73763d06cf859a3af5e84d2b11516a85）；Linux桌面17,797,704 B（SHA256 7c2eb7a327c01daa823fc765bf6192083fbef0b6abeeaa4bf06aa15c725b525c），同批嵌入新Web资源。
- 未验证：真实桌面/WebView渲染、视觉及窗口端到端，无独立GUI环境；未安装/重启用户应用。
- 宿主影响记录：worker误执行桌面--version，因不支持参数进入单实例启动检查，既有实例锁拦下后退出，可能把已有收件箱置前；未创建新窗口或Hub，报告既有AT-SPI进程未变，未启动临时总线/portal。不能把此操作当作GUI验收通过。


## Android 收件箱分类标签（MTYPE，2026-11-13）

- 功能：全部/文字/链接/图片/视频/音频/文档/压缩包/其他9个横滑FilterChip；服务端category为权威，分类后分页；切换清分页、防旧请求回写；实时/本机文本上传分享新增按当前分类入列，分类空态可返回全部。
- 自动测试：JVM152项（151通过、1跳过，0失败/错误）；ApiClient分类参数/必需category解析/词表匹配单测通过。Compose设备测试新增5项，仅编译通过未执行。相关报告 `/tmp/flowdrop-mtype02-report.md`。
- 独立复核发现并修正后台EventStream事件与主线程快切的代际计数/pendingCreates竞争：MTYPE-03真实OkHttp回调线程探针证实后台交付，产品后果为源码推断；MTYPE-02C把onEvent整体交给viewModelScope Main处理，主agent结构性复核通过，compileDebugKotlin通过，未宣称运行时竞态复现通过。报告 `/tmp/flowdrop-mtype03-report.md`、`/tmp/flowdrop-mtype02c-report.md`。
- 构建：assembleDebug成功；`dist/flowdrop-android-debug.apk`，14,256,487 B，SHA256 `cb01e6b3d7ae051e8e90631dc81678364ea372f85912d8f6adca0240fdd2d06e`；apksigner和zipalign通过。包名dev.flowdrop.android，0.1.0(1)，minSDK26/target35。报告 `/tmp/flowdrop-mtype04-report.md`。
- 未验证：真机/模拟器交互观感、Compose设备用例运行、本轮Android真实Hub端到端、ViewModel竞态运行时复现；未安装到手机。要求新版支持category的Hub，旧Hub兼容不在本轮范围。

### 2026-11-13 LOCAL-02 本机更新

用户授权后已部署并重启最新版桌面/内置Hub。运行PID3870803的exe哈希与dist桌面交付一致（7c2eb7a3…525c），8080监听恢复；实际HTTP根页/直接引用JS及CSS均200，新JS含9类标签，资源与本轮嵌入一致。旧部署备份保留；正式总线/portal/AT-SPI PID未变。仅部署和进程/静态资源验证，未GUI/凭据API/手机验证。证据 `/tmp/flowdrop-local02-report.md`；任务task_be4c8757e836/ctx_cf034d4d95f3。
