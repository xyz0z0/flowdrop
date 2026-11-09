# Agent 任务记录

## 当前交付：配对与LAN/Tailscale自动连接（2026-11-08）

**交付状态（2026-11-08 19:06）**：配对与单Hub多地址连接的开发、自动验证和Linux/Android构建完成，所有本轮worker已回报，无剩余运行任务。服务端全包、desktop65、Android带真实Hub集成102 JVM、完整AndroidTest编译、Chromium16项、隔离Wails配对/原生拖拽均通过。真实手机Wi-Fi/Tailscale切换及本轮KDE配对待用户验收；未自动安装/commit/push。三产物与摘要见VERIFICATION §20。以下为分阶段计划与历史派发记录，不将其旧“待前置/运行中”文字当当前状态。

用户确认实施，[PAIRING.md](PAIRING.md)为冻结合同；DD拖拽问题用户确认关闭，禁止改其实现。

| 任务 | 单一目标/允许写入 | 依赖与验收 | 预算/停止条件 | 状态 |
|---|---|---|---|---|
| PA-01 | 配对Store：新增server/internal/pairing/**及db.go配对表 | 冻结Go接口；TTL/一次性/5错/并发/哈希/稳定ID/撤销定向测试 | 15–20min，普通命令30s，测试120s；不接HTTP、不清理旧密码 | 完成并已审查；14测试、vet/race通过，/tmp/flowdrop-pa01-report.md；task_d521b3ce2cb8 / ctx_437eeac48a8b |
| PA-02 | Android纯地址选择器：只新增data/HubRouteSelector.kt及独立测试 | 注入无凭据探测；LAN优先/TS回退/ID不符/取消/去重；不重试写入 | 10–15min，测试180s；不改App/ApiClient/存储/共享模型，不启动GUI | 完成并已审查；15定向JVM测试通过，/tmp/flowdrop-pa02-report.md；task_b6302de61347 / ctx_31f7932f7f12 |
| PA-03 | Web配对页：仅web/src/api.ts、Login.tsx、App.tsx，可新增pairing*.ts(x) | 冻结HTTP接口；移除账号密码输入；保留设置与收发拖拽；TS检查 | 10–15min，检查120s；不改全局CSS、不构建嵌入产物 | 完成并复核更正；tsc通过，真实联调待PA-04；task_f3eec251394a / ctx_425f14d34490；更正ctx_431c936ca1d5 |
| PA-04 | Hub HTTP/WS/本机Go接口接入 | PA-01后限定文件；配对/恢复Cookie/撤销WS/收发 | 正式派发前细分，≤20min | 已完成PA-04A/H，后续全包与E2E通过 |
| PA-05 | Android配对/安全存储/自动连接 | PA-02及冻结HTTP合同；网络和UI再分任务 | 每项≤20min | 已完成PA-05A/B/C及07B，真实Hub回环集成通过 |
| PA-06 | 桌面设置生成码/设备列表/撤销 | PA-04本机API | ≤20min | 已完成，隔离真实Wails验证通过 |
| PA-07/08/09 | 退役密码入口、跨地址验收、最终构建分别派发 | 实现完成后登记具体文件与验收；不重复通过检查 | 各自独立预算 | 已完成；三产物交付，真机验收待用户 |

所有任务：报告/tmp/flowdrop-paXX-report.md；不自动安装/commit/push、不删除用户数据、不停用户应用、不改KDE/RustDesk/代理，禁止宿主GUI/临时DBus/portal/a11y/Xvfb。GUI只能隔离容器/专用VM。两个相同失败无新证据停止；超过预算回报，不自行扩展任务。


维护者：主 agent。规则见 [AGENT_WORKFLOW.md](AGENT_WORKFLOW.md)。以下于 2026-11-08 补录；此前未在派发前登记，属于本次需要纠正的流程问题。

**最新反馈（2026-11-08）**：用户在 DD-14 修复、DD-15 构建及 DD-16 隔离验证交付后明确确认「拖拽可以使用了」，此前 KDE Wayland / Dolphin 拖放问题关闭。交付产物 SHA-256 `76196ab070de3ae14750a4eb37a3d295d18561d7eee8b4e56a0f06a87f7ab463`。真机结果来源为用户反馈，未另外采集真机日志或扩展场景证据；容器结果与用户确认分别记录。无剩余运行中的开发/验证任务。

**前次交付状态（2026-11-08）**：Linux 整窗原生 FileList 拖拽的代码、构建和独立容器 E2E 已完成；当前产物 SHA-256 `4cbfeef23938cdf38807ffebb8e2f052330e5ce6153c6bdf8b8ba169609636ca`。DD-12 核心上传及限定场景通过，证据与未验证项见 VERIFICATION §18。以下保留阶段派发和失败/修正历史，不将旧“待派发”文本当作当前仍有任务运行。未自动安装、commit 或 push；KDE/Dolphin/Wayland 真机验收仍待进行。

## DD-16 新产物隔离 Wayland 定向验证（2026-11-08）

- 单一目标：原样DD-15 release在独立Docker图形会话内，以Wayland原生GTK文件源进行真实鼠标拖放，验证登录后上传成功且下载SHA一致；补一次X11核心拖拽，仅确认本次motion接管无明显回归。不做修改实现、全矩阵或安装。
- 输入：产物SHA `76196ab070de3ae14750a4eb37a3d295d18561d7eee8b4e56a0f06a87f7ab463`；历史基础镜像 `flowdrop-dd04-gui:1`、`/tmp/flowdrop-dd12/work/` 的source/api/实验脚本可只读复用。不得直接使用用户数据/口令/正在运行实例。
- 隔离：全新容器，network none，独立PID/IPC/XDG_RUNTIME_DIR/DBus/HOME；无host display/bus/socket/device/bind或特权。需要Weston仅可扩展Docker镜像安装（镜像构建可联网，实际GUI容器仍network none），不装宿主包。可在容器Xvfb上运行Weston x11 backend以接收容器xdotool输入；FlowDrop和源均须GDK_BACKEND=wayland并证明其连接Wayland；嵌套compositor的X11底层不当作应用X11验证。若条件不足只记未验证，不回退到宿主桌面。
- 验收最小范围：bridge安装；真实鼠标拖拽（禁JS模拟drop/API替代上传/LD_PRELOAD/shim）；一次核心文件成功进入原生→FileList→认证上传201，读回元数据和下载SHA相等。时间允许再单文件重复一次。X11核心可另起容器场景或同隔离会话改backend，不能把历史DD-12算本轮。真实Dolphin/KWin仍单列未验证，GTK Wayland源通过不等同真机通过。不扩展大文件/取消/其他桌面矩阵。
- 允许仅写/tmp报告/证据和自己的测试镜像/容器，不改产品代码/docs/依赖缓存，不重建release。报告 `/tmp/flowdrop-dd16-report.md`。记录本轮宿主相关进程before/after、inspect/env隔离、产物两端SHA、真实后端、每项PASS/FAIL/未验证、日志/下载SHA和精确cleanup。容器资源须trap stop/rm，自动激活服务纳入清理。
- 预算10–15分钟，15分钟统一deadline含所有补测和cleanup，GUI场景60秒、截图10秒、普通命令30秒（镜像构建180秒）；两次失败无新证据即停止。不为补截图延长任务。不启动任何宿主GUI/DBus/Xvfb/portal/a11y，不操作用户FlowDrop/KDE/RustDesk/代理，不安装commit/push；缺隔离条件或时间到即报告已有证据/阻塞。
- 状态：已完成，task `task_a731955d9332` / dispatch `ctx_a227981d00d0` succeeded。报告 `/tmp/flowdrop-dd16-report.md`，原始证据 `/tmp/flowdrop-dd16/evidence/`。原样新版Wayland/GTK源真实鼠标拖放两次→accepted→FileList→POST201，下载59B SHA `6397be9bf71e700faab0c3890f9c36b11731623a1aedd2138c80fb36f8e672e7` 一致；独立X11会话一次相同核心路径通过。主agent已复核日志、API/SHA和重复上传截图，产物摘要未变。
- 隔离和清理：Weston14.0.2嵌套于容器自己的Xvfb，FlowDrop/GTK源强制GDK_BACKEND=wayland；协议trace未采集，不能当作KWin/Dolphin验证。容器 `fa83aeb8b46a320ce5acdd0d4367b24a694589043c09410a1abe100032c1103c` 已stop/rm，主agent复核列表无残留，宿主相关进程before/after diff为空。缓存镜像 `flowdrop-dd16-gui:1` 保留。inspect表明无额外caps，报告“cap-drop ALL”不由现有inspect摘要支持，不能据此声称全部cap已移除；隔离仍有networknone/privateIPC/无hostbind或device/非特权等证据。
- 未验证：真实Dolphin/KWin、多文件/目录/大文件/取消未在本轮重跑。此修复仅做核心Wayland与X11回归，DD-12历史扩展场景保留为历史。约14分钟完成含cleanup。未改源码、未安装提交或干预用户旧实例；待用户从托盘完全退出旧实例后直接运行新产物复验。

## DD-15 修复产物构建（2026-11-08）

- 单一目标：从DD-14最终源码生成release `dist/flowdrop-desktop` 并核对嵌入前端和SHA，不做GUI/产品修改。允许构建脚本自然更新的忽略产物；只写/tmp报告，不修改源码/docs/依赖缓存或安装目录。
- 前置：DD-14实现及定向vet/tests通过，主agent审查无阻塞。运行 `timeout 180s bash scripts/build-desktop.sh`，无需重复已通过测试。记录go构建版本/tags、产物size/mtime/SHA和rc；若编译失败立即回报不自行修源码。
- 预算3–5分钟，5分钟截止回报 `/tmp/flowdrop-dd15-report.md`。禁止宿主GUI/总线/portal/a11y/Xvfb，禁止停止用户旧进程或安装/commit/push。不要执行dist中的GUI。用户实例可能仍映射旧inode，不能凭构建声称已更新其运行实例。
- 状态：已完成构建，task `task_dc5846ee39fc` / dispatch `ctx_6fb94af2b29c` succeeded；报告 `/tmp/flowdrop-dd15-report.md`。release 17,020,392 B，2026-11-08 17:51:53 +0800，SHA-256 `76196ab070de3ae14750a4eb37a3d295d18561d7eee8b4e56a0f06a87f7ab463`（主agent独立复核）。未安装、未启动GUI或停止用户实例，DD-16结果见上。

## DD-14 KDE Wayland 原生拖放最小修复（2026-11-08）

- 前置证据：用户确认来源Dolphin，悬停有提示。其17:43启动日志明确blocker解除1+1、native FileList bridge安装成功，认证后拖放无native drop accepted/chooser/upload日志。因此hook安装与至少一种悬停路径已通，接收阶段仍失败，不能将H-A当作唯一根因。
- 主 agent审查：native drag-motion目前只发overlay后return FALSE，实际action反馈完全留给WebKit；而WebKit无法产出File时DOM可能不接受drop，Wayland需要协商非空action才会投递drop。这是另一待核实原因。native既接管文件drop，应在同一限定判定内明确协商COPY；drag-leave也不应撤销已经接受且等待异步data的drop。两者均属于当前native拖放流程修复，不扩展功能。
- 单一目标：仅修正native文件拖放协议/授权生命周期，添加足够但有限的无路径日志与定向无GUI回归。允许 `desktop/filedrop_linux_gtk3.go`、必要probe/tests/header；其他产品文件/docs/依赖缓存禁止修改。不改text/html过滤、认证上传、chooser token规则和平台/大文件业务。
- 方案要求：先核对官方GTK/GDKWayland/WebKit源码对action协商约束；确认后限定Hub origin+既有文件target判断，在native motion协商COPY并消费匹配motion，避免WebKit后置handler覆盖action。只接受源提供COPY的文件拖拽；普通URL/text/其他origin仍走原始handler。已接受drop的authorization跨leave保持，仍由消费/新drop/已有5秒数据时限判定/load/destroy清除。不可引入任意读文件JS接口。
- 验收：实际生产逻辑相关无GUI回归，区分明确COPY协商/不匹配放行/leave后data有效/外来context和超时拒绝/重新加载撤销；不靠仅复写实现的测试。GTK编译或vet通过；无需最终release构建。不能无GUI测的内容如实列未验证，不能用测试替代真机。
- 预算10–15分钟，15分钟截止报告，普通命令30秒，定向测试/编译180秒。完成最小修改和检查即写 `/tmp/flowdrop-dd14-report.md` 回报；未知关键问题立即回报，不扩展诊断矩阵。禁止一切宿主GUI/临时DBus/Xvfb/portal/a11y，禁止停止用户实例、操作KDE/RustDesk/代理或安装/commit/push。容器GUI另列后续任务。
- 状态：已交付，task `task_21284d5b22b8` / dispatch `ctx_80423cccb2ce` succeeded；报告 `/tmp/flowdrop-dd14-report.md`。主agent审查新增共享生产状态逻辑和回调接入：native匹配文件motion明确COPY+TRUE，既有MIME/origin边界保留，leave不撤销接受后的授权。GTK vet与38个Go测试PASS（含4个新回归），非GUI检查不能证明实际Dolphin通过。未release构建，未安装或操作用户进程。

## DD-13 用户 KDE Wayland 拖拽无反应：只读诊断（2026-11-08）

- 用户反馈：最终容器测试通过的版本在宿主系统拖拽仍无效果，不能视为已完成真机验收。
- 主 agent只读确认：运行 PID 3517359，`/home/bob/vibe-code/flowdrop/dist/flowdrop-desktop` SHA-256 `4cbfeef23938cdf38807ffebb8e2f052330e5ce6153c6bdf8b8ba169609636ca`；会话 KDE Wayland，正式总线 `/run/user/1000/bus`，stdout/stderr `/dev/pts/16`。用户目录安装版摘要不同，但当前运行的确为新产物。未读取终端设备、未启动GUI或修改宿主环境。
- 单一目标：定位当前 native GTK/FileList 桥对 KDE Wayland/Dolphin 的阻断，区分已确认事实和待验证假设，交付最小修复方案；本任务不改产品代码。
- 允许：只读项目源码、Wails/GTK/WebKit依赖、现有证据和进程信息；必要时通过 Orca 正规只读 terminal read 获取现有 FlowDrop 日志（须先确认精确终端归属）；可检索官方 Qt/KDE/GTK/WebKit文档。仅写 `/tmp/flowdrop-dd13-report.md` 和自身临时非GUI分析文件。不读密码、令牌、其他用户终端内容，不直接读取 `/dev/pts/*`。
- 禁止：启动任何宿主GUI/DBus/Xvfb/portal/a11y、触碰KDE/RustDesk/系统代理、操作用户FlowDrop实例、安装软件、容器GUI试验、修改产品文件/依赖缓存/docs、构建安装提交。先读 AGENTS 和 AGENT_WORKFLOW。
- 重点：拖拽 MIME targets 判定（当前拒绝含 text/html），GTK Wayland drag-leave 与 data-received 时序，Wails native 生命周期、origin和chooser门禁；依据源码/日志识别最可能原因，不为方便扩大为无关重构。
- 预算5–10分钟，10分钟截止回报；普通命令30秒，两个相同失败无新证据即停；不能确认则列最短必要观测项，不启动宿主实验。验收报告包含文件/行号、事实/假设、证据、最小修改建议与后续隔离定向验证方案。
- 状态：只读诊断已回报（task `task_13cfa8814d27` / dispatch `ctx_662af066425b` succeeded），报告 `/tmp/flowdrop-dd13-report.md`；真实故障根因尚未确认，实现未派发。主 agent确认当前进程来自用户 Konsole（父zsh2443650 / Konsole2443633，无Orca终端标识），不能通过Orca正规terminal read获取日志，未直接读TTY。等待用户提供拖拽来源、悬停overlay和一次拖拽后约15行stderr，再决定最小修改。
- 候选：drag-leave过早撤销已接受drop的授权（H-A），含text/html直接拒绝（H-B），或原生hook/后段chooser门禁。均无本机拖拽事件日志证明，不能写作已确认根因。报告中“X11通过说明该时序未在X11出现”只能指历史那次实验；overlay出现也不能排除drop阶段或chooser阶段其他故障，不能据此唯一归因H-A。
- 本轮诊断约20分钟，超过约定10分钟；已如实记录，不延长为实现或GUI验证任务。未修改产品代码、未启动宿主GUI/总线或干预用户实例。后续实现、定向验证、构建仍需分别登记，没有授权自动安装或停止用户进程。

## Linux 文件拖拽发送 — 2026-11-08

用户要求：Linux 客户端支持从文件管理器拖入文件发送。当前 Web Composer 仅在发送区域处理 HTML5 文件事件；Wails v3.1.0 GTK3 对外部文件拖拽另有原生处理，需先核对实际接口。

| 编号 | 单一交付物 / 允许范围 | 验收、预算与停止条件 | 状态 / 追踪 |
|---|---|---|---|
| DD-01 | 只读定位：Wails GTK3 外部拖拽到现有认证上传流程的最小接入方案。只读 desktop、web、tests 及依赖源码；仅写 /tmp 报告 | 5–10 分钟；提供具体文件/接口、阻断原因、认证/CSRF/多文件/大文件处理与定向验证方案。只读命令限 30 秒。不能启动 GUI、临时 D-Bus、Xvfb、portal 或修改宿主桌面；完成方案即回报，未知关键选择交主 agent | 已派发新 pi；task `task_fc78d0aca8a2` / dispatch `ctx_14d001fe5759` |
| DD-02 | 按 DD-01 结论实现最小拖拽发送，复用现有认证与上传体验；文件范围在派发前确定 | 10–20 分钟；相关单测或编译通过即交付，不做旧版本/旧数据兼容，不启动宿主 GUI，不修改系统代理或桌面 | 等待 DD-01 |
| DD-03 | 定向验证及构建交付；基于最终修改登记精确命令和产物 | 单列构建预算；GUI 仅可在独立 VM/确认隔离容器验证，缺乏条件如实记未验证；不自动安装、commit 或 push | 等待实现 |

Android Tailscale 发现：本轮先回答能力边界；当前 NSD 只发现 LAN Hub，不扩展为自动扫描整个 100.64/10 网段，也不擅自新增凭据或 Tailscale API 集成。

### DD-01 首轮结果与补证 / DD-02 拆分

- DD-01 首轮报告 `/tmp/flowdrop-dd01-report.md`：确认 Wails GTK3 默认关闭 EnableFileDrop 时会阻断外部文件，现有 Composer 收不到 DOM drop。未修改代码、未运行 GUI。
- 主 agent 未采用报告建议的临时 relay：需要额外端口及页面 Blob 缓冲，且原生 WindowFilesDropped 的路径经过 JS 消息回传，不能视为可信文件读取授权。
- 同诊断补证（原 worker，预算 5 分钟）：通过公开 NativeWindow/ID 精确解除 inbox 的两处 Wails GTK 阻断处理，恢复 WebKit 默认 HTML5 File 拖拽是否可行；不改框架或模块缓存、不执行 GUI。
- DD-02W 独立 Web 实现：允许 `web/src/Composer.tsx`、必要的新前端 helper、`web/src/styles.css`；把登录后收件箱文件 drop 扩展到内容窗口，保持上传/认证/CSRF 不变；拒绝目录并提示，防止浏览器导航，处理多文件/中文名/超限/进度/取消。预算 10 分钟；只运行 typecheck（60 秒），不构建最终二进制、不启动任何 GUI/浏览器/临时总线，不改 desktop/server/docs。完成实现及定向静态检查即报告，真实 WebKit 拖拽待隔离环境。派发前置已满足：使用标准 DataTransfer.files，与桌面适配无字节协议依赖。状态：待派发。
- DD-02D 桌面适配暂等补证；与 DD-02W 文件不重叠。DD-03 待二者完成后再派发构建/定向验证。

追踪更新：DD-01 首轮 task `task_fc78d0aca8a2` 已完成。Orca 将同诊断补证登记为 follow-up task `task_a9d679e89ffd` / dispatch `ctx_1c93d7095733`（原 worker，仅补证不实现）。DD-02W 已派全新 pi，task `task_f3f8653c4678` / dispatch `ctx_1145d062e785`。二者只读/写入边界不冲突。

DD-02W 首轮已交付 3 文件改动，typecheck 通过，报告 `/tmp/flowdrop-dd02w-report.md`。主 agent 复核发现目录项与 files 下标对应的假设可能误删混合拖拽中的普通文件；已向同任务 worker 要求修正为浏览器 item.getAsFile()，补该缺口的纯逻辑回归测试（不启动浏览器），预算 5 分钟。类型检查改用 noEmit，避免现有 typecheck 脚本生成 src/*.js。桌面适配与最终构建仍未完成。

### DD-02D 派发边界

- DD-01 补证已完成（报告 §10）；采用精确解除 inbox 两处 GTK 阻断、恢复 WebKit 默认 File 拖拽的方案。真实 WebKit DOM 文件事件仍须隔离环境验收，不能凭源码证据认定已实测。
- 单一目标：桌面进程仅对自己的 inbox WebView 恢复 HTML5 文件拖拽，保持其他窗口和宿主桌面原状。
- 允许修改：`desktop/main.go` 接入点、新增 `desktop/filedrop_*.go`（平台实现、禁用平台 stub、必要定向测试）；不改 web/server/依赖缓存/宿主配置/docs，既有修改不得覆盖。
- 方法：NativeWindow() GtkWindow 树中查找 WebKitWebView，在 GTK 主线程，匹配 signal ID + 窗口 ID user_data，仅解除 Wails false 模式的 drag-drop/drag-motion 两个 handler。先确认各自精确单一匹配；歧义保留并报错。按 WebView 标记保证幂等。EnableFileDrop 保持 false，不消费 JS 提供的路径，不加 relay/cookie API。
- 登录页的 inbox 注入文件 dragover/drop preventDefault，阻止默认文件导航；不 stopPropagation，已登录 Composer 仍处理发送；普通文字/链接拖拽不干预。settings 仍保持框架阻断。
- 生命周期：WindowRuntimeReady 后调 InvokeAsync（NativeWindow 与 GTK 树访问均在主线程）；重复 ready 不重复解除，不找视图不 panic，但需日志说明未启用。
- 验收：相关 no-GUI Go 测试/编译与 gofmt；不得运行任何 GUI/浏览器/DBus/Xvfb/portal/a11y。普通命令 30 秒，编译/定向测试显式最多 180 秒。预算 10–15 分钟，20 分钟未完成先回报；完成即交付 `/tmp/flowdrop-dd02d-report.md`，不构建最终二进制/安装/commit。状态：待派发。

追踪：DD-02D 已派全新 pi，task `task_326bc4d046db` / dispatch `ctx_dccf38aaaf7c`。DD-02W 修正 follow-up task `task_e7a457f9bc7a` / dispatch `ctx_b1d296113502` 已交付：不再假定 items/files 下标一致；仓库外 8 例纯逻辑回归通过，`tsc -p tsconfig.app.json --noEmit` 通过，报告 `/tmp/flowdrop-dd02w-report.md` 更新。主 agent 复核文件与报告，真实目录/文件拖拽形态仍未验证。

DD-02W 最后收敛：主 agent 发现新增的“部分 item 无 File 就整体回退 files”分支会丢弃已知目录分类，要求删除该分支（不增加兼容逻辑）并加 1 例混合目录/有效文件/无 File 项回归，仍仅原 helper 与 /tmp 测试；预算 5 分钟。

DD-02W 已完成最终代码交付：follow-up task `task_d1a1803b1b67` / dispatch `ctx_ac56c6233b2f` 报告已删除部分失败整体回退；测试总数仍为 8（替换原部分失败用例，非新增为 9），混合目录/有效文件/无 File 项场景通过；noEmit 类型检查通过、未生成 src/*.js。主 agent 已复核 helper 与 `git diff --check`。DD-02D 仍在处理，收到回报后再派 DD-03；前端完成不等同桌面拖拽功能已验收。

DD-02D 首轮回报已收到（报告 `/tmp/flowdrop-dd02d-report.md`）：Go vet/test/临时编译与无浏览器 JS 守卫 6 例通过，未运行 GUI。主 agent 复核拦截到阻断问题：三处 GSignal matched 调用把 window ID 传入 func 参数而 data 传 NULL，会匹配不到阻断 handler，且 0/0 分支误报已启用。已交同任务 worker 修正参数、取消未标记 0/0 成功分支，并增加无 GUI 普通 GObject 信号回归验证真实匹配/保留其他 handler/计数非破坏性；预算 5–10 分钟。DD-03 构建等待该必修通过，不把先前编译成功视为拖拽逻辑通过。

### DD-02D 完成 / DD-03 构建交付

- DD-02D review 修正 task `task_1abe50401003` / dispatch `ctx_65f8382c36fb` 已完成，主 agent 已复核共享 C helper 的正确参数顺序、移除 0/0 误报分支和实际 GObject 测试。修正前该测试失败、修正后通过；生产和探针标签 vet/test/临时编译通过。真实 WebKit 仍未验。
- DD-03 单一目标：基于当前完整代码生成 `dist/flowdrop-desktop` 并准确记录可用功能与验证边界。允许运行 `scripts/build-desktop.sh`（前端嵌入+gtk3 production 编译）、写构建生成的 dist/嵌入 assets、更新 README.md、docs/DEPLOY.md、docs/VERIFICATION.md 中本次拖拽和直接运行说明，以及 `/tmp/flowdrop-dd03-report.md`。不修改应用代码、构建脚本、TASKS、AGENTS、依赖或用户安装目录。
- 验收：release 构建退出码 0；新产物文件大小/时间/SHA-256；嵌入前端含整窗投放提示，产物包含本次桌面拖拽日志文本；`git diff --check`。不重复已有测试；若构建失败，准确回报后停止，不自行扩大修复。
- 预算：5–10 分钟，构建显式最长 300 秒，普通命令 30 秒；超过 10 分钟先回报。严禁运行产物/浏览器/GUI/临时 D-Bus/Xvfb/portal/a11y，不安装/commit/push。交付说明直接运行二进制即可（无需先跑 Hub/启动 sh）；已安装旧实例需用户从托盘退出后手动运行新构建。
- 真实拖拽端到端、窗口树命中和真实 handler 计数没有证据，必须在文档与报告写未验证，不能借自动/编译结果认定通过。状态：待派发。

追踪更新：DD-03 已派全新 pi，task `task_3e6d6280dafe` / dispatch `ctx_b596d92722d7`；主 agent 确认本机存在 Docker 命令/socket，仅做只读镜像/服务检查，不在宿主运行 GUI。隔离图形验证是否可执行另行确定，不改变 DD-03 的无 GUI 边界。

### DD-03 完成 / DD-04 容器隔离定向验证

- DD-03 已完成：`scripts/build-desktop.sh` rc=0，产物 16,999,592 字节，mtime 2026-11-08 13:10:01 +0800，SHA-256 `6201f15e3dc910686f1d26abc2f2e45532580f309539840838397ef2ab2106f6`（主 agent 已独立核对）；README、DEPLOY §16.11、VERIFICATION §17 已更新。未安装、未提交、未运行 GUI。
- DD-04 单一交付物：对该摘要产物做独立 Docker 图形环境的实际文件拖拽取证，不修改产品代码。允许只写 `/tmp/flowdrop-dd04*` 的脚本、临时测试镜像/独立容器、复制到容器的测试产物和夹具；所有仓库/docs 只读。
- 隔离前置必须用 docker inspect 证明：容器独立 PID/IPC/network namespaces，无 privileged/host network/host PID，无宿主 DISPLAY/Wayland/XDG_RUNTIME_DIR/DBUS 地址，无 host bind mounts、devices、Docker socket 或宿主 X11 socket。运行阶段 network none，不 publish 端口。只在这个容器内运行 Xvfb、临时 DBus、GTK 文件来源、WebView；不装 KDE/RustDesk 或整桌面。先确认隔离，再启动图形进程，绝不回退宿主。
- 容器使用全新测试用户/数据库、明确本容器 127.0.0.1 Hub 配置，复制 release binary 和现有 CLI 仅用于初始化临时数据库（不可读取用户数据）。基础镜像与 runtime 包安装可在 Docker build 内完成，不允许宿主安装包。宿主只执行 Docker CLI、文件与进程清单读取。
- 验收范围：inbox 真实日志 `removed drag-drop=1, drag-motion=1`；从文件管理器或原生 GTK URI-list 来源实际鼠标拖入（不是合成 JS drop）后 Hub 出现原文件且下载字节一致；未登录 drop 不发送/不导航；条件允许再试多文件/目录。若只用 GTK 原生拖拽源，报告明确不等于 Dolphin/portal 真机验证。截图和日志复制到 /tmp。
- 预算最多 20 分钟，10 分钟检查点；准备命令最长 300 秒，整个镜像构建最多 600 秒，GUI 场景最多 60 秒，截图 10 秒；同一失败无新证据两次后停止。隔离不成立、镜像/依赖失败或发现产品缺陷时立即报告，不修改代码、不无限复现。不重复单测或 release 构建。
- 退出必须只停止/删除本任务创建并持有 ID 的容器，确认其中图形/总线/portal 随容器全部退出；保留日志、镜像可留缓存。核对宿主原正式 KDE/portal/RustDesk 不受改动。不得按进程名清理，不得运行宿主 GUI、安装用户版本、commit/push。报告 `/tmp/flowdrop-dd04-report.md`，状态：待派发。

DD-04 已派全新 pi：task `task_885d6d4338b8` / dispatch `ctx_69bee8fdcdc8`。DD-03 交付记录保持“未做 GUI”，后续 DD-04 证据单列，不覆盖此前验证边界。

### DD-04 失败 / DD-05 最小生命周期修正

- DD-04 结论 failed：独立容器内 WebView 登录成功，GTK URI-list 原生鼠标拖拽已发生，但没有任何 `inbox file drop` 日志、没有上传请求、条目为空；登录前不上传/不导航通过。原生解除阻断与上传验收失败，DD-03 构建不可认定为拖拽可用。
- 隔离与清理证据：`/tmp/flowdrop-dd04-report.md`、`/tmp/flowdrop-dd04/inspect-before-gui-particulars.json`；容器 `c4b40112bde4ea6e0e82a8404938e88be88040ec3d42507d37b6b093f39c8b62` 已 stop/rm、不再存在，宿主正式 KDE/portal/RustDesk PID 未变。测试镜像 `flowdrop-dd04-gui:1` 保留供后续快速复验，不共享宿主会话。
- 主 agent 定向源码复核：当前 `installInboxFileDropRelax` 依赖 Common.WindowRuntimeReady，仅由 `wails:runtime:ready` 发出；该回调在容器未触发。Linux.WindowLoadFinished 则由 GTK `handleLoadChanged(WEBKIT_LOAD_FINISHED)` 原生发出，不依赖页面 runtime，WebView 此时已存在。修复仅将本次拖拽接入移到此事件；不扩展修复 Wails runtime/设置等其他业务。
- DD-05：全新 pi 最小修正，仅允许 `desktop/filedrop.go` 的事件与对应注释、必要相关 `filedrop*.go` 注释；不动 C 信号匹配算法/web/server/依赖/docs。改用 Linux.WindowLoadFinished，保留 GTK 主线程 InvokeAsync 和已实现视图幂等。验收 gofmt/无 GUI 定向编译，不新增镜像实现的字符串测试，不重复全包测试，不启动 GUI。预算 5 分钟，编译命令 timeout180s，普通命令30s；完成 `/tmp/flowdrop-dd05-report.md` 即回报，不构建最终产物/安装/commit。状态：待派发。
- DD-06：待最小修正后单列最终重建；DD-07：沿用已建立隔离镜像与测试脚本，对新摘要产物单列重复真实拖拽验证，不重复容器准备或扩展环境。

DD-05 已派全新 pi：task `task_661d2e0f1b2e` / dispatch `ctx_1547eb6d8e8d`。VERIFICATION §17.5 已加 DD-04 失败更正，避免将已有构建误当成拖拽验收通过。

### DD-05 完成 / DD-06 重新构建

- DD-05 已完成，仅修改 `desktop/filedrop.go` 接入事件与注释；主 agent 已复核改为 Linux.WindowLoadFinished，InvokeAsync/视图幂等保留。worker 报告 gofmt、gtk3 编译/vet/相关测试通过，未运行 GUI。
- DD-06 单一交付物：对该修正执行 `timeout 300 scripts/build-desktop.sh`，生成新 release 二进制并记录 SHA/大小/mtime；仅允许构建生成目录与 `/tmp/flowdrop-dd06-report.md`，仓库产品代码/脚本/依赖/docs 只读，不启动 GUI或容器、不安装/commit/push，不重跑已过测试。若构建删除既有受控 `.placeholder`，只恢复该文件原内容。预算 5 分钟；构建失败即回报，不自行修复。验收构建 rc=0、产物摘要、嵌入资产及 `git diff --check`；状态：待派发。
- DD-07 仍等待 DD-06 新摘要，使用已有隔离镜像进行实际拖拽复验；不把 DD-05 编译通过当成 E2E 通过。

DD-06 已派全新 pi：task `task_c433cb21025b` / dispatch `ctx_f151a443c541`。

### DD-06 完成 / DD-07 隔离复验

- DD-06 成功 rc=0；新产物 16,999,592 字节，mtime 2026-11-08 13:22:26 +0800，SHA-256 `bb6c1948fe3956909dd43e95d7ac195363e331c1d8c4a58f1baed7d25a8e2ade`（主 agent 复核）；原 DD-03 文件已被此产物替换。报告 `/tmp/flowdrop-dd06-report.md`。没有重新运行 GUI、没有安装。
- DD-07 单一交付物：复用 `flowdrop-dd04-gui:1`（已建隔离镜像）和 `/tmp/flowdrop-dd04/` 脚本，对新摘要原生拖拽复验；无需重建镜像、无需重复下载/单测。仅允许 `/tmp/flowdrop-dd07*` 与自己的新容器，仓库/docs只读；不改产品、宿主配置、其他容器。
- 隔离前置同 DD-04：inspect 确认无 mounts/devices/socket/privileged，无 host namespaces/network/display/runtime/DBus；network none/no publish，docker cp 夹具与产物，全部 GUI/DBus/Xvfb 仅在独立容器。
- 验收先取真实 `inbox file drop ... removed drag-drop=1, drag-motion=1` 日志；如没有则记录失败即停止拖拽重复。随后登录前 OS 文件拖放不导航不发送、登录后真实 GTK URI-list 鼠标拖入收件箱内容区触发 POST upload/新增条目且下载字节一致；有余量补中文空格名/多文件/目录跳过，不重复已通过病例。不得用 JS drop/API 代上传冒充实际拖拽。
- 预算 10 分钟（镜像已就绪），GUI 场景 60 秒/截图 10 秒/普通命令 30 秒，复验失败两次无新证据停止。遇产品问题回报不改源码。结束精确 stop/rm 本任务容器，核对其中图形/总线资源退出及宿主正式 KDE/portal/RustDesk 未被改动；保留 `/tmp/flowdrop-dd07-report.md`/日志/截图与隔离记录。报告按原生 GTK 源 vs 真实 Dolphin/portal 区分，不声称真机全部通过。状态：待派发。

DD-07 已派全新 pi：task `task_0e7e3e93f21d` / dispatch `ctx_77e284d0af21`。

### DD-07 失败 / DD-08 拖拽事件与来源定向诊断

- DD-07 failed：新产物 `bb6c1948...` 已真实打印 removed drag-drop=1/drag-motion=1，DD-05 初始化修正确认有效；登录前不上传/不导航通过。但登录后 GTK 原生 URI-list 源拖入后导航到 file:///，没有 POST upload、条目为空，上传与下载校验失败。多文件/目录/pcmanfm/Dolphin+portal 未验证。报告 `/tmp/flowdrop-dd07-report.md`。容器已删除，宿主 KDE/portal/RustDesk 前后 PID 无变化。
- DD-08 单一交付物：区分“最简 GTK URI-list 源格式”与真实文件管理器、核查页面实际 drag/drop 事件类型/Files/FileList、inbox guard 与 Composer 是否在登录后生效，形成有证据的最小修复建议。仓库产品代码/docs只读，仅 `/tmp/flowdrop-dd08*` 诊断脚本/夹具/临时插桩及自建隔离容器；使用已建镜像与当前原样 release 二进制，不重建生产代码。
- 优先在隔离容器内用已安装 pcmanfm 拖实际普通文件，核查与 GTK URI-list 源差异。需要插桩时仅在容器进程或独立测试页面记录事件 types/files/items/defaultPrevented，以及页面注入/监听存在性；日志或取证中不能带用户真实凭据。诊断页成功不等于产品成功，需区分。不得 JS 合成事件当原生拖拽。
- 隔离边界同 DD-07（独立 namespaces、network none、无 mounts/devices/host session/socket），先 inspect 再 GUI；所有显示/DBus/portal仅容器，结束精确清理自己容器并核对宿主正式进程，不改宿主。允许宿主编译不启动 GUI 的临时插桩 so 或容器内诊断程序，不能更改模块缓存、源码或用户数据。
- 预算 10–15 分钟、20 分钟硬停止并交现有证据；GUI 场景60s/截图10s/普通命令30s，两次无新证据失败停止。报告 `/tmp/flowdrop-dd08-report.md` 必须给实际事件证据与建议精确文件边界；不实现产品修复、不构建 release、不安装/commit/push。状态：待派发。

DD-08 已派全新 pi：task `task_0ce5dad09a76` / dispatch `ctx_56f34110fb6b`。

### DD-08 完成（超时偏差）/ DD-09 原生 FileList 桥接可行性

- DD-08 报告 `/tmp/flowdrop-dd08-report.md`：真实 pcmanfm 与 GTK URI-list 源得到相同 DOM 证据，dragenter/dragover 的 types 只有 text/uri-list+text/html，items 都是 string，files.length=0；导航守卫已注入但 Files 判断不成立。仅容器实验对 URI dragover preventDefault 后虽有 drop，仍无 File。结论限定于本轮 WebKitGTK 2.54 环境，不能推广所有平台/版本。
- 因而继续放宽 JS 类型判断不足以发送文件；需要原生将真实拖入路径转换为可上传 File，或原生流式调用既有认证接口。不得使用 Wails JS 回传 paths 当可信 OS 路径授权。
- DD-08 实际约 28 分钟，超过 20 分钟硬停止（额外反证实验导致），违反派发预算；记录偏差，不因取得证据而视为可接受。后续每任务只做一个候选，禁止追加反证矩阵。容器已精确清理，宿主正式进程未变。
- DD-09 单一目标：仅原型验证“GTK 真实 drop 捕获 + WebKit run-file-chooser/select_files 生成 FileList”候选。不改产品代码/docs，只 `/tmp/flowdrop-dd09*` 的临时 C shim/JS/脚本和自建隔离容器；复用既有图形镜像和当前产物。允许宿主编译临时 .so，不可在宿主加载/执行 GUI。
- 关键验证：只在原生实际 drag-data-received 的可信路径存在时，原生驱动隐藏 file input；run-file-chooser 回调 select_files 只能选择这次实际拖入文件；页面 change 得到真实 FileList/name/size/内容。如程序化 click 因用户手势被禁止，立即报告失败，不追加其他候选。正常手动文件选择不能被拦截。路径绝不从 JS 参数反传读取。
- 证据：原生 drop 回调 / chooser 回调 / JS change FileList 以及读取小夹具字节的一致性；若顺手能用原页面 Cookie/CSRF 上传并下载一致则记录，不能为了扩展 E2E 超时。目录/多文件/大文件暂不做。本任务不做产品实现/重建。
- 预算 10 分钟，15 分钟硬停止；由单个总超时脚本约束容器实验总时长，不能另起脚本绕过。GUI单场景60s/截图10s/普通命令30s；一次候选失败即回报，不加平行方案。容器隔离同 DD-08，先inspect，无host mount/session/network；所有GUI仅容器内。退出精确清理自身容器与资源、核对宿主正式进程。报告 `/tmp/flowdrop-dd09-report.md`，状态：待派发。

DD-09 已派全新 pi：task `task_b9707e2adb92` / dispatch `ctx_c6e115e99fe5`。

### DD-09 成功 / DD-10 并行产品接入合同

- DD-09 仅原型成功：GTK 真实 drop → WebKit run-file-chooser/select_files → JS FileList.length=1，File/name/39B 内容与夹具一致，未导航。报告 `/tmp/flowdrop-dd09-report.md` 和原型 `/tmp/flowdrop-dd09/dd09-shim.c`。未发上传，不能当产品 E2E 通过；普通 chooser/多文件/目录仍待产品复验。容器已清理、宿主正式进程前后无变化，预算内完成。
- 协调者接口决定（两 worker 按此独立实现）：
  1. Composer 挂载时设置 `<html data-flowdrop-drop-ready="true">`，卸载移除；仅登录后的页面具备接收条件。
  2. 原生只从 GTK 实际拖放接收本地 URI/路径，存进当前 inbox WebView 的 pending 状态；不使用 Wails JS paths，不加任意路径 binding/relay/Cookie API。恢复普通 WebKit File 上传流程。
  3. `desktopFileDropGuardJS` 注入隐藏 multiple file input。仅原生调用 `window.__flowdropDesktopFileDropImport(acceptToken, directories, hasFiles)`：ready 属性不为 true 立即拒绝，不开 chooser；目录名仅作提示，不含路径。hasFiles=false 时直接分发空 files 的目录事件。
  4. native 每次真实 drop 生成唯一 accept MIME 标记（`application/x-flowdrop-drop-<随机值>`）；run-file-chooser 仅在请求 MIME 标记与当前 pending 精确匹配时 select_files；普通文件选择请求不匹配则保持默认行为；过期的自有标记请求 cancel，不误开普通对话框。pending 消费即清理，超时/页面加载变更/销毁/下一次 drop 清理，不能把悬空 drop 路径交给后来普通 chooser。
  5. input change 分发 `CustomEvent('flowdrop:desktop-files', {detail:{files:File[],directories:string[]}})`，随后清空 value/本次目录状态。File 对象直接交 Composer 原 startUploads，禁止 FileReader/base64/整文件缓存/重复上传通道。不改变现有 auth/CSRF/限制/进度/取消。
  6. 原生 hover 可分发 `flowdrop:desktop-drag`，detail 为 `{active:boolean}`，页面仅切换已有 overlay；drop/离开/页面卸载正确复位。不使用 JS 合成 DragEvent 伪装系统事件。
- DD-10D 原生产品接入：全新 pi，允许 `desktop/filedrop.go`、`desktop/filedrop_linux_gtk3.go`、`desktop/filedrop_unsupported.go`、必要 `desktop/filedrop*` helper/tests 和 `desktop/main.go` 拖拽注释/调用。保留已验证的原生 load finished/GTK 主线程/精确信号解除 helper；接入原生目标/chooser，可信文件路径只留 C 内。只作用本进程 inbox 与当前 Hub origin，settings 保留原行为。目录先原生过滤并传 basename 提示，普通文件保序/中文空格名。日志只计数不打印路径/凭据。预算 15–20 分钟、20 分钟先回报；仅 no-GUI 编译/vet/必要定向行为检查（命令最长180s），不运行 GUI/容器、不构建最终 dist、不改 web/server/docs/依赖。
- DD-10W 前端产品接入：全新 pi，仅允许 `web/src/Composer.tsx`、必要 `web/src/fileDrop.ts`（不改 desktop/docs）；按合同挂载 ready 和两个 CustomEvent 接收器，将 File[] 交现有 startUploads、目录提示、hover reset。保持已有浏览器拖拽/粘贴/选文件。预算 5–10 分钟，noEmit 类型检查最长60s，必要无浏览器行为检查，不启动 GUI/容器，不构建最终产物。
- 各任务完成即独立报告 `/tmp/flowdrop-dd10d-report.md`、`/tmp/flowdrop-dd10w-report.md`，关键边界不确定时回主 agent，不自行换方案；禁止安装/commit/push/另派agent。修改文件不重叠。DD-11 待两者完成后独立构建；DD-12 对新产物作最终隔离 E2E。

DD-10D 已派全新 pi：task `task_c7d6190845d5` / dispatch `ctx_273c31e3ab0c`。DD-10W 同时派另一全新 pi：task `task_c4d3973d91ff` / dispatch `ctx_deece74e1c03`。两者写入范围分别 desktop 与 web，不重叠。

DD-10W 已交付：按合同挂载 ready 属性、接收 desktop-files/desktop-drag、真实 File[] 交同一 startUploads、目录共用提示逻辑。worker 报告 noEmit 类型检查和 13 项纯逻辑断言通过，未构建/运行 GUI；报告 `/tmp/flowdrop-dd10w-report.md`。主 agent 已复核接收合同与代码，DD-10D 尚待回报，不能提前认为已完成产品上传。

### DD-10D 完成 / DD-11 构建

- DD-10D 已交付，主 agent 复核 ready/CustomEvent/accept MIME 合同、GTK 真实 drop 授权与 chooser 决策、只沿现有上传路径等实现。worker 报告 gtk3 build/vet、purego stub vet、chooser 9 例与 Node VM 8 组通过，未跑 GUI/容器，报告 `/tmp/flowdrop-dd10d-report.md`。完整上传仍待实证。
- 流程偏差：worker 自报约 32 分钟，超出 15–20 分钟且未按约 20 分钟前回报；主要花在 C 状态与行为检查。记录为违反预算，不能以交付成功视为流程合规。后续只做已约定构建和定向 E2E，不加额外矩阵。
- DD-11 单一交付物：两端最终代码的 release 构建。全新 pi，仅运行 `timeout 300 scripts/build-desktop.sh`，写生成 dist/嵌入资源和 `/tmp/flowdrop-dd11-report.md`，不改代码/脚本/依赖/docs；构建删受控.placeholder时只恢复原内容。验收 rc=0、产物精确 SHA/大小/mtime、嵌入资源包含 desktop-files/drop-ready 与 overlay、`git diff --check`。预算 5 分钟，普通命令30s；失败回报停止，不自行修复。不重复已过测试，不运行任何产物/GUI/容器，不安装/commit/push。
- DD-12 待 DD-11 新摘要后单列最终隔离 E2E：真实 pcmanfm 或 GTK 源拖普通文件、API新增与下载SHA、多文件/中文空格、目录混合、登录前不发送、普通选择文件请求不被原生桥拦截。优先核心场景并保留未验证项，不把编译/原型结果当最终产品通过。

DD-11 已派全新 pi：task `task_006c1a08e946` / dispatch `ctx_8b450e34c884`。

### DD-11 完成 / DD-12 最终产品隔离验收

- DD-11 构建 rc=0（4 秒）；最终产物 17,020,360 字节、mtime 2026-11-08 13:52:39 +0800、SHA-256 `4cbfeef23938cdf38807ffebb8e2f052330e5ce6153c6bdf8b8ba169609636ca`（主 agent 独立核对）；嵌入两侧合同及原生桥日志均确认。报告 `/tmp/flowdrop-dd11-report.md`，未安装/运行/提交。
- DD-12 单一交付物：该精确产物的隔离产品 E2E，禁止 LD_PRELOAD/测试 shim 修改产品行为。仅 `/tmp/flowdrop-dd12*` 脚本/夹具/证据与自己的独立容器，仓库/docs只读；复用 flowdrop-dd04-gui:1，不重建镜像或产物。隔离边界同此前（先inspect、network none/no publish、无mounts/devices/privileged/host namespace/session/socket），所有图形和总线仅容器内。
- 核心顺序：日志桥 installed → 登录前拖入不导航/不上传且不弹 chooser → WebView登录 → 真实 pcmanfm/GTK 源拖文件到非发送框区域 → 原生 accepted/chooser handoff、API upload、条目新增与下载SHA一致。核心失败即保留证据回报，不改代码、不加替代架构。
- 核心通过后限定剩余场景：多文件含中文空格名/PNG，目录混合只上传普通文件并提示；普通选择文件 dialog 可正常选文件上传，不能被旧pending劫持；相同文件重复拖入各新增一次。每个场景仅一次，必要有新证据再试一次；其余如大文件/取消/真实Dolphin/portal明确未验证，不扩展矩阵。
- 预算 10–15 分钟、15 分钟硬停止（单一总超时实验脚本约束，不另起绕过），GUI单场景60s/截图10s/普通命令30s。容器退出精确stop/rm并确认全资源退出，宿主KDE/portal/RustDesk只读前后核对。报告 `/tmp/flowdrop-dd12-report.md` 含逐项PASS/FAIL/未验证、精确摘要、真实上传/下载SHA及截图/隔离清理证据。不安装/commit/push、不实现修复。状态：待派发。

DD-12 已派全新 pi：task `task_017f5729034b` / dispatch `ctx_2717db2a1a90`。

### DD-12 完成 / 最终交付

- DD-12 succeeded：原样 release `4cbfeef2...636ca` 在独立 Docker/X11 容器内真实 GTK URI-list 鼠标拖入；核心初始化、登录前拒绝、登录后非发送框区域上传与下载SHA一致、多文件（中文空格名/PNG）、目录混合、普通 chooser、重复拖入均通过。7 个成功上传请求对应7条目，6个不同夹具下载SHA逐一一致。主 agent已复核日志/校验记录/截图/清理证明，摘要未变。
- 证据 `/tmp/flowdrop-dd12-report.md`、`/tmp/flowdrop-dd12/`；最终实现/产物/使用方法/边界已写 README、DEPLOY §16.11、VERIFICATION §18 和 PLAN M8。未新增代码或重跑无关测试。
- 容器 `b52c86e56e2b72db34d37e43714bbdc8e9c1efb9bb18050b75d0af5f7032c2cf` 已 stop/rm、列表无残留；未运行宿主GUI/临时总线或修改用户安装目录。隔离记录有 inspect 和 env，但 namespace inode 对照因权限缺失、且本轮未单独保存宿主 before 快照；不将历史清册对照写作完整本轮前后快照。
- 场景干扰：普通chooser完成后仍有模态窗口，一次重复拖拽未到达原生；Escape关闭后同方法成功。自动化连续Return是报告推测因素，未证明唯一原因，不据此认定产品绝无问题。重复补测使用独立rep2脚本，总实验入口超时未覆盖该补测，虽总时长在15分钟内，仍属单一总时限约束执行偏差；后续所有补测应纳入同一deadline。
- 保留未验证：最终产物的真实 pcmanfm/Dolphin/portal/Wayland 来源、大文件/进度/取消/失败条目、多显示器。DD-08 pcmanfm仅为诊断，不当作最终产物通过。当前原生单批接收上限64文件，超限用户交互未验。没有剩余自动开发/构建任务；用户真实桌面反馈如有故障再定向处理。

## 当前：桌面原生崩溃

| 编号 | 任务与边界 | 状态 | 验收与证据 | Orca 追踪 |
|---|---|---|---|---|
| CR-01 | 只读诊断：原生日志、最小探针、故障范围；不改项目源码 | 已完成 | 复现 AT-SPI/libatk-bridge 故障；正常隔离会话对照；报告 `/tmp/flowdrop-crash-diagnosis-2026-11-08.md`。最初用户日志缺首部，不能认定同次故障已完全确认 | task `task_aa978afcdb72`；dispatch `ctx_9d7ae7de0072` |
| CR-02 | 最小修复：仅桌面启动无障碍桥接兼容处理及必要提示/测试 | 已交付 | 进程级 AT-SPI 探测/兜底、必要提示与单测；worker 报告 go vet、desktop tests 通过，真实故障/健康会话启动证据见 VERIFICATION §16；原始用户同次 SIGSEGV 尚未直接确认 | task `task_7eac7e1fb2f8`；dispatch `ctx_c464a1c9059c` |
| CR-03 | 定向验证：仅检查故障会话启动、正常会话保留无障碍、设置入口与退出；每个 GUI 场景限 60 秒 | 已交付，含受限项 | Xvfb 下真实 WebView 黄色提示可见、Ctrl+, 打开设置、SIGTERM 退出 rc=0 / Hub 停止 / 端口释放；主 agent 已查看截图。真实 XWayland 下合成输入未能打开设置，原因未确认；按钮与托盘退出本轮未验。报告 `/tmp/flowdrop-cr03-report.md`，详见 VERIFICATION §16.7 | task `task_0af23f9c44fa`；dispatch `ctx_6c8046db7f96` |
| CR-04 | 构建交付：核对最终产物和摘要，更新验收记录及升级命令 | 已完成产物与文档交付 | 主 agent 已核对现有二进制 SHA-256 `53a4bb9cbf86dea00176ca552de7209943d90cd8bd27e0f872f07f7a783b4b02`；复用现有构建，不重复编译；不得自动安装或结束用户进程 | 主 agent 汇总 |

### CR-02 收敛要求

原派发将诊断、实现、多个 GUI 环境和交付合并，边界过宽。主 agent 已发送收敛指令：停止追加截图与不相关验证，立即回报现有代码、产物、已通过检查和阻塞。原任务现已通过 `worker_done` 交付；CR-03 使用新 dispatch 补剩余验收，不将原任务冒充重新派发。

后续 CR-03/CR-04 只处理剩余缺口，不要求为了符合新流程重新执行已通过工作。主 agent 不凭任务存活认定其进展，也不凭运行较久擅自终止会话。

## 已交付：主窗口设置与代理

| 任务 | 修改边界 | 状态与证据 | Orca 追踪 |
|---|---|---|---|
| WebView 本机连接代理修复 | 平台代理 helper、network 代理提示与测试；不改主窗口文件 | 已交付；真实 WebKit 直连与系统配置不变的对照，报告 `/tmp/flowdrop-proxy-report.md` | task `task_8806eb6e2c3b`；dispatch `ctx_a66be16823e1` |
| 主窗口设置入口与集成 | 主窗口、固定设置动作、文档与最终构建 | 已交付；登录前后入口、认证 WS 101；报告 `/tmp/flowdrop-desktop-settings-report.md`。随后收到的崩溃作为 CR-01 至 CR-04 追踪，不覆盖此前记录 | task `task_94b775ec3d75`；dispatch `ctx_590ed9f48c78` |

## 后续任务登记模板

派发前复制并填写，不留到交付后补录：

```text
编号 / 标题：
目标（单一交付物）：
允许修改：
禁止修改 / 不做：
输入 / 前置依赖：
验收命令 / 预期证据：
预计时长 / 检查点：
外部命令超时：
完成与阻塞停止条件：
task / dispatch ID：
状态 / 已有证据 / 剩余项：
```

### CR-03 流程偏差与后续边界

本轮仍有执行偏差：截图实际使用 20 秒超时（约定 10 秒），真实 XWayland 下四种输入尝试未成功。记录该偏差，后续不能以更换输入方式为由绕过停止条件。Xvfb 成功不证明真实桌面输入失败一定是自动化限制；真实桌面设置按钮和托盘退出保留为未验证，不追加无限回归。修复和产物已交付，用户实际桌面验收尚待反馈。


## 桌面测试残留影响远程连接 — 2026-11-08 11:47 只读核查

用户报告 RustDesk 连接失败并指出额外 KDE portal。主 agent 本轮只读核查：

- KDE 主会话 `kwin_wayland` PID 1803、`plasmashell` PID 2105、正常 KDE portal PID 2864 均从 11 月 5 日运行；正常 portal 使用 `/run/user/1000/bus`。
- 残留 KDE portal PID 3088212 从 10:47:22 运行，环境含修复 worker 终端 `term_f287e0c3-0c73-44f5-ab2d-3afdf04a3da9` 和本项目 Orca workspace；其总线 `/tmp/dbus-ZXRKW6hbYX` 已不存在。
- 当前 RustDesk server PID 3177532 的环境及父进程启动命令含相同失效总线地址。足以确认本项目测试残留与 RustDesk 的失效环境相关；尚未检查 RustDesk 选择算法或本轮亲测远程恢复。
- 同时还有六组来自本项目更早测试终端的额外 portal/临时总线，且继承真实 `DISPLAY=:0`、`WAYLAND_DISPLAY=wayland-0`、`XDG_RUNTIME_DIR=/run/user/1000`。因此此前“隔离、清理完成”不能视为整个测试过程没有宿主副作用。
- Xvfb :99 来自本项目更早的测试终端（01:07），不是 CR-03 新建；残留 portal 实际 `DISPLAY=:0`，不能仅凭同时存在 Xvfb 就认定它绑定虚拟屏幕。

本轮未杀进程、未重启 RustDesk/KDE、未改宿主配置。相关测试资源清理和远程恢复尚未在本轮完成。后续暂停宿主 GUI 实验；新增 D-Bus 的自动激活进程也须纳入资源清单，不能只结束应用和总线就报告全部清理。GUI 集成测试优先使用容器或独立测试会话；不在真实桌面总线/运行时目录中重复创建服务。


## RD-01 远程环境恢复（用户已授权）

- 目标：清理本项目遗留 portal/临时总线/无障碍测试服务和 Xvfb，移除 RustDesk 失效会话环境。
- 执行：主 agent 直接处理宿主恢复；不委派新的 GUI 测试，不启动测试总线。
- 边界：只操作具有本项目 Orca workspace、已确认测试终端、匹配服务可执行文件的残留；保留原 KDE、正式 D-Bus、正常 portal 和 RustDesk 主服务。必要时仅重启使用已失效临时总线的用户态 RustDesk server/tray。
- 验收：测试残留消失；正式 portal 与总线仍在；RustDesk server 使用正式会话地址。远程端实际连接需要用户复核。
- 预算：5 分钟；不重启 KDE、不注销、不改 RustDesk 配置，无权限或主服务未自动恢复即报告。
- 状态：宿主环境恢复已完成；远程端实际连接待用户复核。


### RD-01 执行结果

- 使用 `/proc` 环境中的项目 workspace、三个已确认测试终端、服务可执行文件三重匹配并持有 pidfd，向 44 个测试残留发送 SIGTERM（portal、临时总线、无障碍测试服务、Xvfb）；未使用按名称批量 kill 或 SIGKILL。复核全部不再活跃。资源清单 `/tmp/flowdrop-rd01-cleanup.json`。
- 原 KDE `kwin_wayland` 1803、`plasmashell` 2105、正式 portal 2698/2780/2864 均保留。
- 仅向失效总线的 RustDesk 用户子进程 3177532/3177542 发送 SIGTERM；原 RustDesk 主服务未重启，自动拉起 server 3187717、tray 3187724，二者使用正式 `unix:path=/run/user/1000/bus`。原 RustDesk GUI 1126830 保留。
- 正式会话总线可读取，portal 服务仍注册。没有新建总线、启动 GUI 测试、改 KDE/RustDesk 配置或注销会话。
- 验证边界：只证明本机进程环境恢复，未从外部客户端亲测远程连接。用户可重连 RustDesk 复核。
- 预防要求已同步根 AGENTS.md：不再在宿主桌面创建临时 D-Bus；GUI 验证须使用独立虚拟机/容器会话，资源不足时明确跳过。

## 第二轮派发合同（2026-11-08）

- PA-01失败计数解除语义确认：只在本机重新生成码时重置，成功亦归零；与用户约定一致，无需新增解锁API。未声明跨进程并发已经验证。
- PA-04A：只改server/internal/api/**，接配对认证/撤销WS/新测试。Deps增加Pairing *pairing.Store、HubInfo func() HubInfo。类型api.HubInfo {ID,Name string; Endpoints []HubEndpointInfo}及HubEndpointInfo {URL,Kind string}采用冻结JSON。导出Server.RevokeDevice(id string) error给本机Hub调用，注销也走同一撤销路径。不得修改pkg/hub/db/pairing/desktop/Android/Web/docs；旧密码网络route移除且所有新路径仅配对认证，旧Store字段如编译依赖暂保留不作回退。15–20min，普通30s/测试180s；新定向HTTP/WS测试与包编译通过即交付，旧密码测试待单列调整。
- PA-04H：只改server/pkg/hub/**（不改已有test文件，可新增pairing_test.go），接配对store及API元数据回调。公开Hub.CreatePairingCode() (PairingCode,error)、ListPairedDevices() ([]PairedDevice,error)、RevokePairedDevice(string) error，类型别名pairing.Code/Device。Dependencies按PA-04A合同。metadata只用成功绑定URLs，无未绑定地址；kind按RFC1918、100.64/10分类；HubID初始化持久、name取hostname。旧HasAdmin/InitializeAdmin暂留给尚未接入桌面，不新增密码回退，PA-07再退役。15min，build90s/定向test180s；不得改api/desktop/cli/旧测试/docs。API依赖暂缺时等待协调不自改别人的文件。
- PA-05A：仅android data/Models.kt、SessionStore.kt、PersistentCookieJar.kt、ApiClient.kt与新增HubProbe.kt及对应data测试（不改PA-02选择器/其测试、Gradle/AppContainer/AppViewModel/UI/资源/docs）。新增冻结JSON模型和pair/restore接口、Keystore记录扩展hubId/deviceId/deviceToken/candidates，只向已保存且健康ID一致的地址恢复凭据，明确switchToPaired与手动switchTo区分。普通请求使用Cookie，Bearer仅session恢复，probe无任何凭据且禁止重定向；OkHttp禁止自动重试写入。旧方法暂保留仅使尚未更新VM可编译，PA-05B后退役，不开发兼容分支。20min，定向JVM测试240s；编译、配对JSON/安全存储/跨origin不泄漏/ID错拒绝/恢复测试，超预算回报，不接UI。冻结新签名见派发spec及报告。

以上三个任务互不重叠，登记后全新pi并行；尚未构建交付，禁止宿主GUI/用户实例操作。报告/tmp/flowdrop-pa04a-report.md、pa04h-report.md、pa05a-report.md。

第二轮启动回执（均全新pi，state=ready）：

| 任务 | Task | Dispatch | 状态 |
|---|---|---|---|
| PA-04A | task_93003800fa22 | ctx_4602cb59de80 | 完成，新HTTP/WS 7子测试+race通过；旧密码测试待更新 |
| PA-04H | task_21752508284f | ctx_117d1e6bdfcd | 完成，5个Hub定向测试+race通过，已实际接线 |
| PA-05A | task_e562c3096a71 | ctx_543a83cdc6c1 | 完成，data包53 JVM测试通过，整包Kotlin可编译 |

第一轮PA-03同任务复核更正已回报：删除username遗留字段、修正401/429注释、移除不实设备改名承诺、完整码用数字正则；tsc一次复查通过（ctx_431c936ca1d5）。核心计数本机生成解锁语义已由主agent确认，不需用户再次批准。第一轮仅模块检查通过，不代表配对或切地址端到端完成。

## 第三轮：运行流程与设置（2026-11-08）

主agent已核对PA-04A/H实际代码，H回报晚于A，A报告中的“Hub未接线”仅是当时状态，现已接线。PA-05A代码已复核：probe无凭据、restore按保存候选与ID校验、默认无重放，原密码VM/UI仍待替换。

冻结Android接入接口：UiState删username/password，新增pairingCode:String、hasStoredPairing:Boolean、hubName:String（默认空/false）；现有phase/LOGIN/loggingIn沿用。AppViewModel提供updatePairingCode(String)、pair()、retryConnection()、logout()，移除updateUsername/updatePassword/login。LoginScreen参数为hubAddress,pairingCode,hasStoredPairing,hubName,loggingIn,error,onHubChange,onPairingCodeChange,onSubmit,onRetryConnection；discovery参数原样保留。数据层已冻结pair/restorePairedSession/persistSession(info)、SessionStore.switchToPaired与HubRouteSelector；不得再改其实现。

| 任务 | 目标与写入范围 | 依赖/验收 | 预算与停止 | 状态 |
|---|---|---|---|---|
| PA-05B | Android运行流程：仅AppViewModel.kt、FlowDropApp.kt、data/EventStream.kt，可新增connection/**及对应纯测试 | PA-02/05A已交付；无密码配对、开机/前台/失联重选、已配对离线不丢凭据、无写入重放；UI按上面冻结签名由C并行改 | 20min；测试240s；不改data其它文件/UI/依赖，不做GUI；超预算回报 | 待派发 |
| PA-05C | Android UI：仅MainActivity.kt、ui/LoginScreen.kt、ui/InboxScreen.kt、res/values/strings.xml | 冻结VM签名；4位前导零输入、首次发现/手填、已配对重连、解除配对文案；共享入口和收发不变；整包Kotlin编译 | 10–15min；检查240s；无VM/data改动/不做GUI | 待派发 |
| PA-06 | 桌面设置配对：仅service.go、main.go、assets/{index.html,app.js,style.css}、internal/hubmgr/hubmgr.go及新增pairing相关定向测试 | PA-04H API完成；本机生码120s倒计时、列表撤销；旧密码面板去除；新Go桥与JS检查 | 20min；检查180s；不改filedrop/a11y/proxy或旧测试，不操作宿主GUI | 待派发 |

本轮验收不替代端到端验收；原密码内部方法为编译暂留，后续单列删除，不加旧版本兼容路径。三个任务文件互不重叠，均全新pi。沿用宿主保护、报告、无安装/commit/push规则。

第三轮全新pi回执（ready，正在运行）：PA-05B task_ae88932ff721 / ctx_7901f6c33984；PA-05C task_4eb6b5eb21a7 / ctx_997342043b6d；PA-06 task_76f01f08b234 / ctx_b1c69f2e9492。

## 第四轮：旧密码入口退役及验收维护（2026-11-08）

PA-06完成并已审查：桌面生成码/设备列表/撤销、首开引导已接入；根包Go测试与JS语法通过，未GUI。报告/tmp/flowdrop-pa06-report.md。PA-05C完成：配对页/重连按钮/字符串接线，Kotlin编译和5个helper测试通过，未GUI。报告/tmp/flowdrop-pa05c-report.md。PA-05B仍在运行，未据此声明自动重连完成。

| 任务 | 唯一目标及范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| PA-07A | 仅server/internal/api/*_test.go，把旧密码harness改配对，保留文件收发/CSRF/Host/Origin/TLS/限额/WS/清理行为断言 | PA-04A已交付；整个api测试+vet通过，真实SQLite/HTTP，不能跳过失败或降低断言 | 15–20min；go test180s，报告pa07a；生产缺陷只报不改 | 待派发 |
| PA-07D | 原子退役Hub/桌面旧管理员本机入口和字段，并同步受影响测试：仅server/pkg/hub/{hub.go,hub_test.go,multilisten_test.go}、desktop/{service.go,service_test.go,errors.go}、desktop/internal/hubmgr/{hubmgr.go,hubmgr_test.go} | PA-04H/06完成；新配对harness保留生命周期/重启/锁/双监听/收发断言；Hub与desktop包测试通过 | 20min；测试180s/GTK用现有sysroot；范围跨两层因方法调用编译依赖，不触及api/CLI/认证表 | 待派发 |
| PA-07C | 仅server/cmd/flowdrop/**，CLI退役admin init，新增本机pair生码，doctor不再要求密码 | 已有core Store持久码可由运行Hub消费；CLI测试/编译，空设备列表不算doctor故障 | 10–15min；测试120s；不操作用户配置/数据库，不改docs | 待派发 |

本轮不删旧数据库表/用户数据，不做无关auth库重构；生产认证路径仅配对，退役公开密码入口，不加兼容分支。两层方法退役与其测试必须同任务原子调整，其他任务均文件隔离。GUI禁令与无安装/提交规则继续生效。

第四轮回执（全新pi，ready）：PA-07A task_484ffb841b49 / ctx_f435cffe7ac1；PA-07D task_83969026f287 / ctx_21b60407c6c0；PA-07C task_85cb1a784ad1 / ctx_9774eb3c2bae。PA-05B还在运行，其他第三轮两项已完成。未构建或安装新产物。

PA-05B首次交付：12个连接控制测试与Kotlin编译通过，报告/tmp/flowdrop-pa05b-report.md。主agent复核发现INBOX前台仍只重启WS，未满足前台LAN优先重选；BOOTING后台取消恢复、Deferred忙碌标志、吞取消和IO线程VM副作用需同范围复核。已发同任务补正（task_62cd38254c82 / ctx_57882eb867d2），仍在运行，不把首次交付记为最终验收。当前只用请求/WS失败触发失联恢复，未新增系统网络权限或回调。

下一待派发PA-07B：Android数据层旧login/username/persistSession旧重载及其密码harness退役；仅Models/SessionStore/ApiClient及data测试。先等当前B生命周期补正，避免同机并发过多Gradle。

第四轮完成回报与主agent初审：PA-07A api包44顶层测试PASS/0skip、vet通过；PA-07D Hub 21与desktop 65测试全通过、旧公开管理员符号已移除；PA-07C CLI 5测试/vet/build通过。报告分别/tmp/flowdrop-pa07a-report.md、pa07d-report.md、pa07c-report.md。PA-05B补正ctx_57882eb867d2完成，15控制测试及Kotlin编译通过，前台统一重选、取消/线程归属修正；真实端到端仍未做。

## 第五轮：剩余入口与文档（2026-11-08）

| 任务 | 写入范围/唯一目标 | 验收与预算 | 状态 |
|---|---|---|---|
| PA-07B | Android data/{Models,SessionStore,ApiClient}.kt，data单测与LiveHubContractTest.kt：删除旧login/username/旧persistSession重载，改配对测试fixture，不改runtime/UI/选择器 | 全JVM与compileDebugAndroidTestKotlin，240s检查，15–20min；无真机GUI | 待派发 |
| PA-01R | 仅pairing.go/pairing_test.go及CLI main_test.go：重新生成码保证与上一码不同，移除随机碰撞skip，定向确定性回归 | 原同码可能性1/10000会违背新码废旧约定；核心与CLI定向测试/vet，120s，10–15min；不改API/schema | 待派发 |
| PA-07E | 仅README.md、docs/{ARCHITECTURE,DEPLOY}.md、android/README.md、scripts/{dev,lan-http}.sh：把现行使用说明和脚本提示改配对及Android双地址重连 | 不改历史PRD/VERIFICATION/主agentTASKS/PLAN；shell语法检查、定向旧入口检索；15min，不启动服务/构建/GUI | 待派发 |

上轮生产功能已落地；本轮PA-01R只补代码复核发现的新码碰撞语义缺口，不扩展认证架构。脚本修改仅提示，Web E2E/Android真实Hub端到端另设PA-08。所有实现继续全新pi与先登记派发。

第五轮回执（全新pi，ready）：PA-07B task_73d532567162 / ctx_ce6b3e84450e；PA-01R task_3b4c3adaedcd / ctx_751b614ab8b7；PA-07E task_dd5c32ff226b / ctx_6e6c63997f6d。当前无剩余上轮运行任务；本轮尚未完成，不把测试通过当成最终二进制交付。

PA-01R已完成并审查：事务内排除上一hash，最多32候选，失败回滚不改旧码/锁；确定性碰撞与失败原子性回归通过，pairing16顶层+2子、CLI5通过，无skip，vet/race通过。PA-07E完成，bash -n与diff检查通过；实际22min超15min预算，后续文档任务应再拆，不以完成掩盖超时。两报告见/tmp/flowdrop-pa01r-report.md、pa07e-report.md。PA-07B仍运行。

## 验证与构建登记（2026-11-08）

| 任务 | 目标/写入范围 | 验收/预算 | 状态 |
|---|---|---|---|
| PA-08W | 仅tests/e2e/**，浏览器旧密码harness改本机CLI生码与配对页，保留收发/重启/剪贴板检查，可加配对错误/刷新/解除测试 | Playwright --list与TS加载通过，不执行浏览器；普通30s/检查120s，15min；不动生产与共享产物 | 待派发 |
| PA-09L | Linux发布构建：只dist与server/internal/webui/dist构建产物，/tmp报告 | server一次完整go test ./...，桌面build脚本一次，复用web资产编CLI；记录两产物SHA/大小/版本/时间；20min，构建180s | 待派发 |

PA-09L只生成待验收产物，不能因构建成功标最终完成；PA-08W只改测试，后续独立容器浏览器运行另派。不得并发构建同一webui目录，不自动安装/commit/push。真实Android↔Hub双地址定向验证待PA-07B后登记；真实Tailscale/真机不冒充模拟回环结果。

PA-08W启动task_15058d9a9b51 / ctx_d4aa48ca983e；PA-09L启动task_188d7c04097a / ctx_9f8bbc3b6341。两任务各自全新pi、state ready；等待自动回报，不轮询。

PA-07B检查点：全JVM 101测试/0fail/0skip通过，但完整AndroidTest编译有9处错误，均为旧LoginScreenTest.kt未跟上PA-05C签名。主agent在既有用户授权内批准同任务最小扩围：只额外修改该文件适配四位配对签名/断言，额外预算5分钟；不排除文件、不删仍适用断言，完整compileDebugAndroidTestKotlin通过才算该验收完成。临时排除旧UI测试得到LiveHubContractTest.class仅补充证据，不能记整体通过。ApiException死代码与Keystore旧注释不影响行为，不擅自顺带修改。

PA-07B最终完成：101 JVM/0fail/0skip，完整compileDebugAndroidTestKotlin无exclude通过，LoginScreenTest最小扩围已完成；LiveHub参数hubPairingCode或hubDeviceToken，未真机。PA-08W完成：16个浏览器测试加载/枚举通过但未执行。PA-09L完成且主agent独立sha一致：desktop 03ea9fee1e442301c24eada9126b0427f05d07c66118fe5e5e7d6c19346d0857，CLI 48eda3d93639a1f6720392d51ea1a2532e9ee6e693fa2b246a393985d76c50b6；server全包测试通过。两Linux产物待下列验收，不再改其源码或重建。

## 最终定向验证（2026-11-08）

| 任务 | 范围与输入 | 验收/预算 | 状态 |
|---|---|---|---|
| PA-08P | 只/tmp和自己的Docker镜像/容器/内部网络，原样CLI与tests/e2e，不改测试或产品 | 隔离Chromium运行16项Web E2E；复制结果、失败最小证据与cleanup，20min含准备，测试场景60s/命令30s/镜像180s | 待派发 |
| PA-08I | 仅新增android/app/src/test/.../integration/RealHubPairingTest.kt及必要同前缀测试helper，/tmp | Android实际ApiClient/HubProbe/选择器对原样CLI临时Hub双回环地址：一次配对、文字/PNG/普通文件双向读写、下载SHA、无凭据探测、凭据恢复、撤销；标明回环模拟非真实TS/真机。20min，Gradle240s/子进程10s | 待派发 |
| PA-08G | 只/tmp与自己的独立Docker容器，原样desktop/CLI；可只读复用flowdrop-dd16-gui:1及历史脚本 | 实际Wails设置添加设备→显示4码→内容窗口配对→一项原生拖拽收发与下载校验；截图检查设置，无产品/测试JS注入；15min统一截止，GUI60s/截图10s，cleanup与宿主正式会话快照 | 待派发 |

容器禁止host network/PID/IPC、host bind/display/bus/socket/device/特权/发布端口；browser可用自建internal Docker网络供非secure LAN-origin测试，桌面容器network none。网络/容器只自己的精确ID清理。宿主无临时GUI/DBus/portal/a11y/Xvfb，保护KDE/RustDesk/代理/用户实例。验证失败停止回报，不自改源码/测试或重建产物。Android默认编译/构建和真机明确区分；APK构建待I完成后单列。

最终验证任务全新pi回执（ready）：PA-08P task_ac108d914718 / ctx_fb668dd9b72e；PA-08I task_4fa8b01a9365 / ctx_e7bda81dfbe2；PA-08G task_7b42ec9e704d / ctx_0e0bc66b1602。Linux摘要主agent已独立复核，尚待本轮结果。APK构建未开始（避免与I的Gradle并发）。

PA-08P完成且主agent核对报告：Chromium1.65匹配官方容器原样CLI，16/16 PASS，0fail/skip；容器私有RFC1918地址非secure origin断言实际执行，配对/双context图文文件/SHA/重启/解除均通过。容器与internal网络已清理，主agentdocker列表复核无pa08残留。PA-08G完成且主agent已独立查看设置生码和收件箱64B拖拽截图、日志native accepted/FileList/POST201、下载200：新binary实际Wails设置配对→inbox配对→原生GTK拖拽成功；X11容器，不冒充本轮Wayland/Dolphin。测试镜像缺CJK字体仅容器显示受限；设置内点击撤销未执行（HTTP/WS撤销已在其它Go/Web测试通过）。已消费的码在设置UI仍倒计时到过期，为已知显示限制，不声称可再次消费；每加一台设备需重新点添加设备。报告PA-08G所记18:57–19:01与“15min预算耗尽”不一致，未据此推断准确耗时。容器已删，正式宿主会话未操作；快照证据依赖采集范围，不能替代隔离inspect证据。PA-08I仍运行，APK待构建。

PA-08I完成并已核对报告/关键测试：原样CLI实际Hub双回环地址，native配对、Android→电脑文字/PNG/文件、反向文字与文件下载SHA、第二origin Bearer恢复、无写重放、真实另一个Hub ID不符不发凭据、logout后旧token无效均通过；新增1集成test，含它的全量102/0fail/0skip通过。kind为other，首候选故障由probe注入，存储为InMemory，未真机/Keystore/真实TS；旧连接池重启后首次IO失败测试明确记下并清自己池后重试，不将此当生产自动重试验证。按既定禁写重放设计不扩大加GET重试逻辑。临时Hub/端口/data全部清理。报告/tmp/flowdrop-pa08i-report.md。

## PA-09A Android APK构建交付

- 目标：从已完成配对源码生成可安装debug APK，复制dist/flowdrop-android-debug.apk并核对签名/包信息/摘要；不运行模拟器或安装到设备。
- 范围：仅android/app/build、.gradle自然构建缓存与dist/flowdrop-android-debug.apk，/tmp报告。禁止改生产/依赖/配置/docs/Linux二进制。
- 前置：全量102 JVM与完整AndroidTest编译通过，真实Hub集成通过；现无其他Gradle任务。运行timeout240s ./gradlew --offline :app:assembleDebug，失败只报不修源码。用已有apksigner/aapt核对签名、包ID/dev.flowdrop.android、version/minSdk；不得安装工具或SDK。无真机功能声称。
- 预算10min，普通30s/Gradle240s；完成约定即停，不重复已通过测试。报告/tmp/flowdrop-pa09a-report.md，记录源APK和dist副本SHA一致、bytes/mtime、签名信息与Linux两产物摘要未变。
- 状态：待派发。无自动安装/commit/push/宿主GUI，用户正在运行旧实例不得操作。

PA-09A已启动，全新pi：task_51f0ef2f3668 / ctx_4dcc8b73f345，state ready。其它配对开发/验证任务均已回报，无其它运行worker；当前只等待APK构建。最后文档汇总由主agent在收到APK摘要后完成，未安装/提交。

PA-09A完成并由主agent独立核对SHA：APK 10209256 B，2026-11-08 19:06:26 +0800，dist/flowdrop-android-debug.apk与Gradle原APK摘要均d00a6808ec5f5190354d9d1191e6a4fb1dd23cda8aaed0ca1d62357531c4e5e4。assembleDebug 4s成功、apksigner v2有效（Android Debug证书）、aapt包dev.flowdrop.android/versionCode1/versionName0.1.0/min26/target35。Linux两摘要未变。所有阶段完成，无运行worker；最终文档汇总完成，未安装/commit/push。用户步骤：完全从托盘退出旧版→运行dist桌面→设置双监听→每添加一台设备生成新码→手机App发现/手填并配对；之后该单Hub自动选已保存可用地址。产物、证据与已知显示/真机限制见VERIFICATION §20。

## 配对码消费后 UI 反馈（2026-11-09）

用户反馈：设置窗口生成的四位码在手机成功配对后已失效，但界面仍继续倒计时、没有成功提示，直到过期才消失。目标：设置页在码被消费后 3 秒内自动隐藏并提示；不改配对协议、HTTP 接口或“到期自动清理”的现有语义。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| PC-01 | 仅 server/internal/pairing/{pairing.go,pairing_test.go}、server/pkg/hub/pairing.go、desktop/internal/hubmgr/hubmgr.go、desktop/{service.go,pairing_service_test.go}、desktop/assets/{app.js,index.html}：新增本机只读 bridge `PairingCodeActive`（`Store.ActiveCode(now)`、Hub/Manager/SettingsService 透传），设置页每 3 秒刷新时若已显示的码不再有效则清理并显示 banner-ok“配对成功，配对码已使用并自动隐藏”；保留过期、重启、重新生成、Hub 未运行的现有清理路径 | 依赖现有配对 store/schema；`go test ./...`（server、desktop 根包）、`node --check desktop/assets/app.js`、新 store 单测覆盖 active/consumed/expired，service E2E 覆盖创建后 true、配对后 false；不做 GUI、不改 HTTP/API/schema/依赖 | 15–20min；测试300s；完成即停；范围外问题只报不改；报告 /tmp/flowdrop-pc01-report.md | 待派发 |

PC-01 已派发全新 pi：task_6c9db6a829a4 / ctx_c3cb0e11613e，state ready，run_7301d66276fc。等待 worker 回报，不据派发声明修复完成。

PC-01 完成并由主 agent 复核：新增 `Store.ActiveCode` → `Hub.PairingCodeActive` → hubmgr → `SettingsService` 只读桥；设置页 3s 刷新中调用，码被消费后 `clearPairingCode()` 并显示 `id="pairing-status"` 的 banner-ok。`clearPairingCode()` 同时清空成功提示，过期/重启/重新生成/Hub 停止路径保持原语义。主 agent 独立复跑：`server/ go test ./internal/pairing ./pkg/hub` 通过；`desktop/ CGO_ENABLED=0 go test -tags purego . ./internal/hubmgr` 通过；`node --check` 与 gofmt 通过。默认 cgo + GTK4/WebKitGTK 构建仍因宿主缺开发包未验证；未做 GUI、未安装/提交。报告 /tmp/flowdrop-pc01-report.md。

PC-01 构建：主 agent 用 `scripts/build-desktop.sh`（用户 sysroot + gtk3 production）重建 `dist/flowdrop-desktop`，退出0，17077864 B，SHA-256 `85fce3e0e591a433b0416a8f28498e84db012c21a3ea1840fc489b5578fb2f05`，二进制内含 `PairingCodeActive` 与成功提示文案。构建脚本重新生成 `server/internal/webui/dist` 资产与占位文件；占位文件已恢复，未重建 CLI，未替换用户正在运行的进程。

## Android 内网发现与长文本展示（2026-11-09）

用户实测：手机浏览器可打开 Hub；系统日志显示已收到 `FlowDrop-...` 的 PTR，随后 `Resolve service`，但 App 12 秒内没有拿到 resolve 回调；主 agent 在 eno1 抓包只看到手机 `192.168.31.15:5353` 的 PTR 查询和 Hub 的 PTR/SRV/TXT/A 响应，**没有看到针对实例名的 SRV/TXT/A resolve 查询**。Hub 端对 ANY/SRV/TXT/A 的响应正常。另：Linux 收件箱长文本不折叠，撑满列表。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| AND-01 | 仅 `android/app/src/main/java/dev/flowdrop/android/discovery/NsdHubDiscovery.kt`（必要时同目录纯逻辑/测试）及 `android/app/src/test/java/dev/flowdrop/android/discovery/**`：为 NSD 增加脱敏 logcat 诊断（tag `FlowDropDiscovery`，记录 found/resolve 开始/成功地址端口/失败错误码/超时/停止，不记码与令牌）；不要在 `onServiceFound` 内同步重入 `resolveService`，改为短延迟；若 `onServiceFound` 的 `NsdServiceInfo` 已有可用 host/port/TXT 则直接解析，否则串行 resolve；resolve 失败或 watchdog 超时对同一服务最多重试 3 次（间隔约 300ms、单次 watchdog 约 2.5s），stop/重搜必须取消旧重试；保持单飞、多播锁与现有 12s 发现窗口 | 现有 Android JVM 测试 + `timeout 300s ./gradlew --offline :app:testDebugUnitTest :app:compileDebugAndroidTestKotlin`；新增纯逻辑测试覆盖重试次数/停止取消（若可抽出）；不构建 APK、不装真机、不做 GUI、不改其它文件 | 15–20min；Gradle 300s；完成即停；范围外只报不改；报告 /tmp/flowdrop-and01-report.md | 待派发 |
| WEB-01 | 仅 `web/src/ItemCard.tsx`、`web/src/styles.css`：文字条目超过阈值（约 6 行或 280 字）默认折叠，显示“展开/收起”，展开后完整显示；短文本不显示按钮；复制仍复制完整正文；不改协议/后端/桌面 assets | `cd web && npx tsc -b && npm run build`；不做浏览器/GUI；不新增依赖 | 10–15min；检查180s；完成即停；报告 /tmp/flowdrop-web01-report.md | 待派发 |

两者文件不重叠，可并行。均使用全新 pi；禁止 GUI/安装/commit/push；宿主保护规则继续生效。

AND-01 已派发全新 pi：task_00654e3fc088 / ctx_d029c1e80b57，state ready。WEB-01 已派发全新 pi：task_2651791570cb / ctx_836d98d46142，state ready。两者同在 run_7301d66276fc，文件不重叠；等待回报，不据派发声明完成。

WEB-01 完成并由主 agent 复核：`ItemCard.tsx` 增加 280 字/6 行估算折叠与 `展开全文/收起`，`styles.css` 增加 clamp 样式；复制仍用全文。主 agent 复跑 `cd web && npx tsc -b && npm run build` 通过，未做浏览器/GUI。报告 /tmp/flowdrop-web01-report.md。用户补充手机为 Xiaomi 15 / Android 16；AND-01 运行中，等其回报后再决定是否需要自建 mDNS 解析兜底。

AND-01 完成并由主 agent 复核：`NsdHubDiscovery` 增加 `FlowDropDiscovery` 脱敏日志、`onServiceFound` 后 250ms 延迟 resolve、发现结果已完整时直接解析、失败/超时/`usable=false` 最多 3 次重试（2.5s watchdog、300ms 间隔）、stop/重搜用 runId+resolveToken 失效旧回调；`DiscoveryProtocol` 抽出纯逻辑 `ResolveAttemptTracker`。报告第 4 条仍写“不可用结果不重试”，实际代码已按补充要求走 `onAttemptFailed`，以代码为准。主 agent 独立强制重跑 `:app:testDebugUnitTest --rerun-tasks`：106 tests / 0 fail / 0 error / 1 skipped（原有 RealHubPairingTest）。未真机验证。

构建交付：新 APK `dist/flowdrop-android-debug.apk` 10209256 B，SHA-256 `8a325a720da91d837138168d9a80d961f067d8cccc5a31c826dafda251eb0311`，含 `FlowDropDiscovery` 日志与新的 resolve 逻辑；`dist/flowdrop-desktop` 重建后 17077864 B，SHA-256 `4ffd09823de8708ff6a04bb4428c69c8ca5aa828647df13ee439d11dc7913ba9`，含长文本“展开全文”和 PC-01 成功提示。两者均未安装/替换用户运行进程，未 commit/push。

AND-01 补充回报（ctx_32c3ca8f74c3 / task_3cb92e4f2806）已核对：`onAttemptResolved` 接收排队的 `NsdServiceInfo`，`usable=false` 走 `onAttemptFailed`，同一服务最多 3 次后 `resolve abandoned`；源码 mtime 12:42:04 早于 APK 构建 12:42:32，当前交付 APK 已包含该逻辑，无需再次构建。106 tests/0 fail/1 skipped 不变；真机日志仍待用户提供。

AND-01 真机日志（Android 10 设备）：`resolve success host=192.168.31.184 port=8080 usable=false`，三次均同；host/port 正常且 RFC1918，服务名/类型正常，说明 `DiscoveryProtocol.parse` 只剩 TXT 校验可能拒绝。Android 10 的 `NsdServiceInfo.attributes` 很可能为空/未带 TXT。为避免继续盲猜，下一任务做“TXT 缺失兼容 + 日志补全”，明确仍拒绝显式错误的 TXT。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| AND-02 | 仅 `android/app/src/main/java/dev/flowdrop/android/discovery/{DiscoveryProtocol.kt,NsdHubDiscovery.kt}` 与 `android/app/src/test/java/dev/flowdrop/android/discovery/DiscoveryProtocolTest.kt`：`DiscoveryProtocol.parse` 在 TXT map 为空时，只要 serviceType/serviceName/port/RFC1918 host 全通过就接受（视为 Android 10/HyperOS 丢失 TXT 的兼容路径）；TXT 非空时仍严格校验 `v=1/scheme=http/path=/`，任一错误值拒绝；`resolve success` 日志补 `type=` 与 `txtKeys=`（不记值以外的敏感信息；TXT 本身无秘密），让下一轮日志能直接区分 TXT 空、type 不匹配或 host 问题。新增/调整纯 JVM 测试：空 TXT 接受、正确 TXT 接受、错误 v/scheme/path 拒绝、非 RFC1918 仍拒绝 | `cd android && timeout 300s ./gradlew --offline :app:testDebugUnitTest :app:compileDebugKotlin`；不构建 APK、不连真机、不做 GUI；不改变已有 `FlowDrop-` 前缀/RFC1918 规则 | 10–15min；检查240s；完成即停；报告 /tmp/flowdrop-and02-report.md | 待派发 |

AND-02 已派发全新 pi：task_14188224d377 / ctx_a9a5166da569，state ready。等回报后再构建 APK。

AND-02 完成并由主 agent 复核：`DiscoveryProtocol.parse` 仅在 TXT map 非空时严格校验三项，空 TXT 接受但 serviceType/`FlowDrop-` 前缀/端口/RFC1918 全部保留；`resolve success` 日志增加 `type=` 与 `txtKeys=`。主 agent 强制重跑 `:app:testDebugUnitTest --rerun-tasks`：111 tests / 0 fail / 0 error / 1 skipped，`compileDebugKotlin` 通过。新 APK `dist/flowdrop-android-debug.apk` 10209256 B，SHA-256 `b2ea8b008d0fc80b250dd3a5844e620caa1328f6954a5b3f982baf9b1e56f8b6`；未安装/真机/GUI，未 commit/push。

## AND-03 Android 10 serviceType 前导点（2026-11-09）

主 agent 直接核对设备：安装包 SHA-256 与 `dist/flowdrop-android-debug.apk` 的 `b2ea8b00...` 一致，`lastUpdateTime` 18:58:57；新日志在 18:59:44 已运行，含 `txtKeys=path,scheme,v`，证明 AND-02 已生效。但日志同时暴露真正拒绝点是 `type=._flowdrop._tcp`（Android 10 返回带前导点的 serviceType），`matchesServiceType` 只去尾部点，所以 serviceType 校验先失败，空 TXT 兼容未命中。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| AND-03 | 仅 `android/app/src/main/java/dev/flowdrop/android/discovery/DiscoveryProtocol.kt`、`android/app/src/test/java/dev/flowdrop/android/discovery/DiscoveryProtocolTest.kt`、`NsdHubDiscovery.kt` 的日志行：`matchesServiceType` 规范化时去掉首尾点、转小写，并容忍可选 `.local` 后缀，使 `._flowdrop._tcp` / `._flowdrop._tcp.` / `_flowdrop._tcp.local.` 都匹配 `_flowdrop._tcp`；`resolve success` 日志追加 TXT 实际 map（只含 v/scheme/path，无秘密）以便确认值。新增纯 JVM 测试覆盖前导点/可选 local/错误类型 | `cd android && timeout 300s ./gradlew --offline :app:testDebugUnitTest :app:compileDebugKotlin`；不构建 APK、不连真机、不做 GUI | 8–12min；检查240s；报告 /tmp/flowdrop-and03-report.md | 待派发 |

AND-03 已派发全新 pi：task_0e0dfb085d2a / ctx_367c1e588550，state ready。

AND-03 完成并由主 agent 复核：`matchesServiceType` 改为 trim/lowercase/去首尾点/可选去 `.local` 后精确匹配；`resolve success` 增加 `txt=key=value`。主 agent 强制重跑 `:app:testDebugUnitTest --rerun-tasks`：113 tests / 0 fail / 0 error / 1 skipped，`compileDebugKotlin` 通过。新 APK `dist/flowdrop-android-debug.apk` 10209256 B，SHA-256 `d02cf8896a72f6f8aba74c929cb23547528f5cc8c86893c85904a3addd2e60d4`；当前设备仍装着上一版 `b2ea8b00...`，未自动安装，待用户安装后看 `usable=true`。未 commit/push。

## WEB-02 展开/收起按钮移到标题行右侧（2026-11-09）

用户真机确认 AND-03 后扫描成功（`usable=true`）。同时反馈：长文本展开后“收起”按钮在正文底部，需要下滑才能点。目标：按钮移到卡片标题行右侧，展开后仍在顶部。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WEB-02 | 仅 `web/src/ItemCard.tsx`、`web/src/styles.css`：把“展开全文/收起”从正文下方移到 `.item-head` 标题行末尾并右对齐（`margin-left:auto`），文本加 `id`、按钮 `aria-controls`；折叠阈值/clamp/复制全文行为不变；短文本仍不显示按钮；窄宽度允许换行但不挤压 tag 与时间 | `cd web && npx tsc -b && npm run build`；`git diff --stat` 仅两文件；不做浏览器/GUI/截图；不新增依赖 | 10–15min；检查180s；完成即停；报告 /tmp/flowdrop-web02-report.md | 待派发 |

WEB-02 已派发全新 pi：task_8a49cd752424 / ctx_6a9dbccb7a4a，state ready。等待回报，不据派发声明完成。

WEB-02 完成并由主 agent 复核：`展开全文/收起` 已移入 `.item-head` 末尾，`margin-left:auto` + `flex:0 0 auto` 右对齐不挤压 tag/时间；文本加 `id=item-text-${item.id}`、按钮加 `aria-controls`；`needsExpand` 阈值与 clamp 未改；`.text-toggle` 收紧为 0.78rem/紧凑内边距。主 agent 独立复跑 `cd web && npx tsc -b && npm run build` 均退出 0；`git diff --stat -- web/` 仅两文件（+69/-1）。窄宽度换行未做视觉验证（按约定不做 GUI）。`npm run build` 反复删除的 `server/internal/webui/dist/.placeholder` 已再次恢复。未 commit/push。

## AND-04 Android 长文本折叠与展开按钮（2026-11-09）

用户要求 Android 客户端也支持长文本折叠/展开；按钮位置与桌面端一致（标题行右侧），展开后可原地收起。根因：`ui/ItemCard.kt` 对 text 直接 `maxLines = 30` 截断，无展开入口。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| AND-04 | 仅 `ui/ItemCard.kt`、新建 `ui/TextFold.kt`、`res/values/strings.xml`、新建 `test/.../ui/TextFoldTest.kt`：抽纯逻辑 `needsTextExpand`（280 字/6 行、约 18 字每行），text 条目默认折叠 6 行并可展开/收起，按钮放标题行最右端（紧凑 TextButton，展开/收起两态），同行设备·时间改为 `weight(1f)` 可收缩；`rememberSaveable(item.id)` 保持状态；link 仍 3 行截断无按钮；复制仍全文 | `cd android && timeout 420 ./gradlew --offline :app:testDebugUnitTest :app:compileDebugKotlin`；git diff 仅四文件；不构建 APK、不连真机、不做 GUI | 20–25min；检查300s；完成即停；报告 /tmp/flowdrop-and04-report.md | 待派发 |

AND-04 已派发全新 pi：task_1bb55a69f6d9 / ctx_ae613c65397b。等待回报。

AND-04 完成并由主 agent 复核：新建 `ui/TextFold.kt`（`needsTextExpand`：换行归一+trim、>280 字或估算 >6 行，18 字/行）与 `TextFoldTest`（10 用例）；`ItemCard` 正文 `maxLines` 由 30 改为 6（折叠）／`Int.MAX_VALUE`（展开），仅 text 类型在标题行右侧加紧凑 `TextButton`「展开/收起」，`rememberSaveable(item.id)` 保状态，设备·时间 Text 改 `weight(1f)`；strings 增 `action_expand_text`/`action_collapse_text`。主 agent 独立强制重跑 `./gradlew --offline :app:testDebugUnitTest :app:assembleDebug --rerun-tasks`：BUILD SUCCESSFUL，123 tests / 0 fail / 0 error / 1 skipped。APK 复核：`dev.flowdrop.android` 1/0.1.0、min26/target35、V2 签名 Android Debug、dex 含 `needsTextExpand`、资源含“展开/收起”。新 APK `dist/flowdrop-android-debug.apk` 9850366 B，SHA-256 `9ad5ac94c6400bc011bd30e537c35a6c24267fab7e13afb0674387f6ea16d9f8`；比上一版小 358890 B 来自 zip 对齐（未压缩条目 16KB padding）差异，dex/资源体量几乎不变。未安装/真机验证，未 commit/push。

## WIN-0x Windows 桌面版（2026-11-09，用户选择 B：完整 Wails Windows 桌面版）

用户选择做完整 Wails Windows 桌面版，需 Windows 机器验收。现状：`desktop` 其余部分已能在 `GOOS=windows` 下编译通过（主 agent 在 /tmp 副本验证过），当前阻塞点只有两处：server `storage.AvailableBytes` 的 `syscall.Statfs`，以及 `desktop/main.go` 依赖的 `trayWatcherPresent`（`dbus_linux.go`，linux-only）。非 Linux 已有 `filedrop_unsupported.go`、`webview_proxy_unsupported.go`、`a11y_unsupported.go` 桩。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WIN-01 | 仅 `server/internal/storage`：把 `AvailableBytes` 拆成 `statfs_unix.go`（unix，保持 `syscall.Statfs`）与 `statfs_windows.go`（windows，`x/sys/windows.GetDiskFreeSpaceEx`），`storage.go` 只留签名与注释；允许把 `golang.org/x/sys` 提升为直接依赖 | `GOOS=windows GOARCH=amd64/arm64 go build ./...`、`GOOS=darwin GOARCH=arm64 go build ./...`、Linux 原生 `go test ./...` 全绿、gofmt 无输出 | 15–20min；检查300s；完成即停；报告 /tmp/flowdrop-win01-report.md | 待派发 |

WIN-01 已派发全新 pi：task_d158b950832b / ctx_cb66362cc857。

WIN-01 完成并由主 agent 复核：`AvailableBytes` 拆为 `statfs_unix.go`（原 `syscall.Statfs`）与 `statfs_windows.go`（`GetDiskFreeSpaceEx` 的 FreeBytesAvailableToCaller），`storage.go` 仅留调用；`x/sys` 提升为直接依赖，`go mod tidy` 顺带移除了仓库内已无引用的 `x/term`（`grep -rn "x/term" server` 无命中，`go build ./...` 通过）。主 agent 复跑：`GOOS=windows GOARCH=amd64/arm64`、`GOOS=darwin GOARCH=arm64` `go build ./...` 均通过。WIN-01 报告记录的运行期风险：`syncDir` 对目录句柄 `Sync()` 在 Windows 会失败，会让上传提交失败 → 立 WIN-02B 修复。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WIN-02 | 仅新建 `desktop/tray_windows.go`、`desktop/tray_unsupported.go`，改 `desktop/main.go` 的 `application.Options.Windows`（`DisableQuitOnLastWindowClosed:true`、`WndClass`），新建 `scripts/build-desktop-windows.sh`（amd64/`--arm64`/`--console`，默认 `-H windowsgui`，输出 `dist/flowdrop-desktop.exe`）；不引入 wails3 CLI | `GOOS=windows GOARCH=amd64/arm64 go build`、`GOOS=windows go vet`、实际执行脚本产出 exe、Linux 构建不被破坏 | 20–25min；检查300s；报告 /tmp/flowdrop-win02-report.md | 待派发 |
| WIN-02B | 仅 `server/internal/storage`：`syncDir` 拆为 `sync_unix.go`（保持 `d.Sync()`）与 `sync_windows.go`（打开校验后 Close 返回 nil，注释说明 Windows 不支持目录 fsync）；修 Windows 上传提交失败风险 | `GOOS=windows amd64/arm64 go build ./...`、Linux `go test ./internal/storage/... ./pkg/hub/...` 全绿、gofmt | 10–15min；检查240s；报告 /tmp/flowdrop-win02b-report.md | 待派发 |

两者文件不重叠，可并行。

WIN-02 已派发：task_c1066edb07f7 / ctx_ef898a463ea7。WIN-02B 已派发：task_ba024aa552a5 / ctx_394ae369a8f1。

WIN-02 完成并由主 agent 复核：新增 `tray_windows.go`（Windows 原生通知区，恒 true）与 `tray_unsupported.go`（`!linux && !windows` 保守 false），`main.go` 补 `Windows: {DisableQuitOnLastWindowClosed:true, WndClass:"FlowDropDesktop"}`；新增 `scripts/build-desktop-windows.sh`（amd64 默认、`--arm64`、`--console`、`-trimpath -s -w`、默认 `-H windowsgui`）。主 agent 复核产物：`dist/flowdrop-desktop.exe` 18328576 B，PE32+ x86-64 GUI 子系统，SHA-256 `2146673bd3a505ac1d46a45fab38b55ea24e10a62c51d5133247bac17b331cec`。Windows 真机、WebView2 检测、托盘菜单、签名/manifest 未验证。

WIN-02B 完成并由主 agent 复核：`syncDir` 拆为 `sync_unix.go`（保持 `os.Open`+`Sync`）与 `sync_windows.go`（打开校验后 Close，注释说明目录句柄无 FlushFileBuffers），`storage.go` 只留调用点。`GOOS=windows amd64/arm64 go build ./...` 通过，Linux `go test ./internal/storage/... ./pkg/hub/...` 全绿。Windows 运行期行为仅编译验证。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WIN-03 | 仅 `server/internal/config/config.go`（defaultDataDir）、`server/pkg/hub/hub.go`（DefaultConfigPath）、`desktop/internal/appsettings/appsettings.go`（DefaultPath）、`desktop/main.go`（日志初始化）、新建 `desktop/log_windows.go`/`log_other.go`：配置目录改用 `os.UserConfigDir()`（Linux 等价、Windows 走 %AppData%），数据目录 Windows 走 `os.UserCacheDir()`（%LocalAppData%）；Windows GUI 构建把 stderr 日志同时写入 `%LocalAppData%\flowdrop\desktop.log`，失败只告警不阻止启动；Linux 行为不变 | `GOOS=windows amd64 go build ./...`（server、desktop）、Linux `go test ./internal/config/... ./pkg/hub/...`、`CGO_ENABLED=0 go test -tags purego . ./internal/...`、gofmt；不做 GUI/真机 | 20–25min；检查300s；报告 /tmp/flowdrop-win03-report.md | 待派发 |

WIN-03 已派发：task_486e29607ca2 / ctx_95a87a5cb60d。

WIN-03 完成并由主 agent 复核：`config.defaultDataDir` 增加 `runtime.GOOS=="windows"` → `os.UserCacheDir()`（%LocalAppData%），`hub.DefaultConfigPath` 与 `appsettings.DefaultPath` 改用 `os.UserConfigDir()`（Linux 等价 `$XDG_CONFIG_HOME`/`~/.config`，Windows `%AppData%`）；新增 `desktop/log_windows.go`（`%LocalAppData%\flowdrop\desktop.log`，append）与 `log_other.go`（nil），`main.go` 用包级 `logWriter` 同时写 stderr+文件，打开失败仅告警不阻断。主 agent 复核通过：server `GOOS=windows` 构建、Linux 配置/hub 测试、desktop `GOOS=windows` 编译、purego 构建与测试、gofmt 均无问题。Windows 上日志文件实际生成未真机验证。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WIN-04 | 仅 `desktop/main.go`（Windows 选项加 `AdditionalBrowserArgs: ["--no-proxy-server"]`）与新建 `scripts/package-windows.sh`（调 build 脚本后打 `dist/flowdrop-windows-amd64.zip`，内含 exe 与中文 README.txt；支持 `--arm64`）：解决系统代理劫持本地 Hub 访问，并产出交付包 | `GOOS=windows` 构建/vet、Linux purego 构建、实跑 build+package 并列出 zip 清单、git diff 仅三文件；不做 GUI/真机 | 15–20min；检查300s；报告 /tmp/flowdrop-win04-report.md | 待派发 |

WIN-04 已派发：task_b369ff4c7a0e / ctx_8146e24c727e。

WIN-04 完成并由主 agent 复核：`WindowsOptions` 增加 `AdditionalBrowserArgs: ["--no-proxy-server"]`（exe 内可检出该串），新增 `scripts/package-windows.sh` 产出 `dist/flowdrop-windows-amd64.zip`（7325388 B，SHA-256 `a3813f2db218131e2ff114ba0f139eba5c006600417c735fe62351c38d27bd99`，内含 exe 18332160 B + README.txt 1306 B）与 arm64 包。主 agent 复核发现两处缺口，立 WIN-05：Hub 自身日志仍写 stderr（Windows GUI 无控制台会全丢），README 日志路径写成 `logs\` 而实际是 `desktop.log`。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| WIN-05 | 仅 `server/pkg/hub/hub.go`（`Options.Logger`，nil 仍用 newLogger）、`desktop/internal/hubmgr/hubmgr.go`（Manager.Logger 透传）、`desktop/main.go`（构造 hub logger 指向 logWriter，级别取 FLOWDROP_LOG_LEVEL，默认 Info）、`scripts/package-windows.sh`（README 日志路径改为 `%LocalAppData%\flowdrop\desktop.log`） | server `go test ./pkg/hub/... ./internal/...`、`GOOS=windows` 构建、desktop purego 构建与测试、重跑打包并核对 README；不做 GUI/真机 | 15–20min；检查300s；报告 /tmp/flowdrop-win05-report.md | 待派发 |

WIN-05 完成并由主 agent 复核：`hub.Options.Logger`（nil 仍用 `newLogger`，在 `New` 内解析一次）、`hubmgr.Manager.Logger` 透传、`desktop/main.go` 构造 `hubLogger`（级别取 `FLOWDROP_LOG_LEVEL`，默认 Info）指向共享 `logWriter`，`scripts/package-windows.sh` 的 README 日志路径改为 `%LocalAppData%\flowdrop\desktop.log`。worker 另用临时注入 logger 的测试证明 Hub 记录会进入给定 sink（该临时测试未保留）。主 agent 复核 server/desktop 构建与测试均通过。

WIN-06 完成并由主 agent 复核：新增 `webview_proxy_windows.go`（返回 nil，注释说明 `--no-proxy-server` 已在 `AdditionalBrowserArgs` 静态生效），`webview_proxy_unsupported.go` tag 改为 `!windows && (!linux || !cgo || !gtk3)`（原任务给的 `!linux && !windows || !cgo || !gtk3` 在 Windows 会重复声明，worker 按同意图修正）。Windows 构建与 Linux purego 构建均通过。

Windows 交付包（主 agent 重建并核对）：`dist/flowdrop-windows-amd64.zip` 7327329 B，SHA-256 `5ab569f4a3e685ad0b4f028ea6c749752c16d4c457b5fc4f73e1b0566e160f62`，内含 `flowdrop-desktop.exe`（18332672 B，PE32+ x86-64 GUI，含 `--no-proxy-server`）与 README.txt（日志路径已改为单文件、措辞同步）。另有 `dist/flowdrop-windows-arm64.zip` 与两个独立 exe。未签名、无图标/manifest、未 Windows 真机运行；拖拽上传、开机自启、代码签名未做。**未 commit/push。**

## RMT 桌面端远程 Hub 客户端模式（2026-11-09）

用户反馈：Windows 桌面版要连 Linux 上的 Hub；在收件箱配对页输入 Linux Hub 的 4 位码必然失败，因为桌面壳只会启动本机 Hub、窗口加载本机地址，没有“连接已有 Hub”的能力。决定新增运行模式：本机 Hub（默认）/ 连接远程 Hub（远程模式不启动本机 Hub，窗口加载远程地址，配对走该 Hub 页面自带配对页）。模式切换保存后需重启生效（本轮不做热切换）。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| RMT-01 | 仅 `desktop/internal/appsettings/appsettings.go(+_test)`、`desktop/main.go`、`desktop/service.go`、`desktop/{service_test.go,pairing_service_test.go}`：Settings 增加 `mode`/`remote_url` 与校验（http/https、无 path/query/userinfo、去尾斜杠、旧文件视为 local）；State 增加 `mode`/`remote_url`/`restart_required`，新增 `ApplyMode`；远程模式启动不 startHub、不绑端口、不跑 mDNS、收件箱加载远程地址、托盘/浏览器打开用远程地址；远程模式守卫本机 Hub 专属方法并返回中文错误 | `CGO_ENABLED=0 go test -tags purego . ./internal/...`、purego 构建、`GOOS=windows` 构建/vet、gofmt；不做 GUI/真机 | 25–30min；检查400s；报告 /tmp/flowdrop-rmt01-report.md | 待派发 |
| RMT-02 | 仅 `desktop/assets/{index.html,app.js}`（必要时 styles.css）：设置页顶部新增运行模式单选与远程地址输入、保存调用 `ApplyMode`、`restart_required` 时显示常驻 banner；远程模式隐藏本机 Hub 专属区块（绑定/双地址/端口/配对码/设备列表）并显示“当前：远程 Hub · 地址”与打开收件箱/浏览器按钮；轮询跳过本机专属接口且不覆盖用户输入 | `node --check desktop/assets/app.js`；不做 GUI/浏览器 | 20–25min；检查300s；报告 /tmp/flowdrop-rmt02-report.md | 待派发 |

两者文件不重叠（Go vs assets），契约字段由主 agent 固定在 spec 中，可并行。

RMT-01 已派发：task_1b1c68932676 / ctx_15ae02e64beb。RMT-02 已派发：task_f5b5ffc3e545 / ctx_93c517096396。

RMT-01 完成并由主 agent 复核：`appsettings.Settings` 增加 `Mode`/`RemoteURL` 与 `Normalize`/`Validate`（http/https、必须有 Host、禁 userinfo/path/query/fragment、去尾斜杠、非 remote 清空 remote_url、旧文件无 mode 视为 local）；`State` 增加 `Mode`/`RemoteURL`/`RestartRequired`；新增 `ApplyMode`（写回 desktop.json，模式或地址变化时 `RestartRequired=true`，不热切换）；`main.go` 远程模式不 `startHub`、不绑端口、不跑 mDNS、收件箱加载远程地址、托盘/浏览器打开走 `inboxBaseURL()`、首次直接显示收件箱不弹设置；远程模式 7 个本机 Hub 方法返回统一中文错误。`manager.Stop` 在无 Hub 时返回 nil，退出安全。

RMT-02 完成并由主 agent 复核：设置页顶部「运行模式」卡片（本机/远程单选 + 远程地址输入 + 轻校验 + 保存 `ApplyMode` + 常驻重启 banner），远程模式隐藏 `#network-card`/`#pairing-card` 并显示「当前：远程 Hub · 地址」与打开收件箱/浏览器按钮；`refresh()` 在远程模式跳过 `ListPairedDevices`/`PairingCodeActive`，不覆盖用户正在编辑的输入。`node --check` 通过。

主 agent 复核：`CGO_ENABLED=0 go test -tags purego . ./internal/...` 4 包全绿；`grep -rn "runtime.GOOS" desktop` 无命中 → 运行模式逻辑与平台无关，**Linux 桌面版同样获得该能力**（用户要求）。

交付（主 agent 重建）：`dist/flowdrop-desktop` 17102504 B，SHA-256 `6569b6d811fc870f2cf9b9f32185e70b01eda80d01e1d912603b6d96927dc06e`；`dist/flowdrop-windows-amd64.zip` 7334126 B，SHA-256 `ef1951e36a2ce71bf4d9063b8f0a8a4325451fdca591d4ca60bb9402ce5ccb75`；两者均含 `remote hub mode: using` 日志串。构建脚本再次删除的 `server/internal/webui/dist/.placeholder` 已恢复。未 commit/push。

用户真机确认（2026-11-09）：Windows 11 x64 上运行模式正常——可在设置里切到「连接远程 Hub」并连上 Linux Hub。补充文档 `docs/DEPLOY.md §18`（运行模式语义、设置/数据/日志路径、Windows 交叉编译与打包脚本、已知限制：未签名/无图标 manifest/无拖拽/无自启、防火墙需允许专用网络）。仍未 commit/push。

## RMT-03 / WIN-07 热切换与 Windows 资源（2026-11-09）

用户确认 Windows 远程模式可用后要求：远程模式热切换（1）、Windows 图标/DPI manifest/版本信息（3）都做。用户另反馈 Windows 已能拖拽上传——现有实现是 Web 层 HTML5 drop（`web/src/Composer.tsx`、`fileDrop.ts`），WebView2 原生支持，无需原生桥；Linux WebKitGTK 仍需 `desktop/filedrop_linux_gtk3.go` 原生桥。故拖拽项不做代码改动，只更新文档与验收记录（见本节末）。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| RMT-03 | 仅 `desktop/main.go`、`desktop/service.go`、`desktop/{service_test.go,pairing_service_test.go,desktop_helpers_test.go}`、`desktop/assets/{app.js,index.html,style.css}`：`ApplyMode` 改为立即生效——本机→远程先 `manager.Stop`（10s 上限）再切 `remoteURL` 并把收件箱 `SetURL` 到远程地址；远程→本机用 `startHub` 启动，失败则不改保存值、窗口留在远程并返回中文错误；成功后保存设置、`restart_required=false`、UI 去掉“需重启”提示；`remoteURL` 跨 goroutine 读取需加锁或原子化 | `CGO_ENABLED=0 go test -race -tags purego . ./internal/...`、`GOOS=windows amd64 go build`/`go vet`、`node --check desktop/assets/app.js`、gofmt；不做宿主 GUI/真机 | 30–35min；检查 400s；报告 /tmp/flowdrop-rmt03-report.md | 待派发 |
| WIN-07 | 仅新建 `tools/winres/`（独立 go module，依赖 `github.com/tc-hib/winres v0.3.1`）、改 `scripts/build-desktop-windows.sh`（构建后调用生成器）、必要时 `scripts/package-windows.sh`：给 exe 写入图标（`desktop/assets/icon.png` 256×256 多尺寸 ICO）、版本信息（FileVersion/ProductVersion 0.1.0.0、ProductName FlowDrop、FileDescription）、manifest（asInvoker、PerMonitorV2 DPI、Win10+ supportedOS、longPathAware）；用 `winres.WriteToEXE` 纯 Go 后处理，不引入 wails3 CLI/mingw | `GOOS=windows amd64/arm64` 构建后 exe 可被 `winres.LoadFromEXE` 读回 icon/version/manifest；`file` 仍为 PE32+ GUI；Linux `go test ./...` 不受影响；实际跑 `scripts/build-desktop-windows.sh` 与打包脚本；报告 /tmp/flowdrop-win07-report.md | 30–35min；检查 400s；报告 /tmp/flowdrop-win07-report.md | 待派发 |

两任务文件不重叠（desktop Go/assets vs tools+scripts），可并行。禁止事项：不得在宿主桌面启动 GUI/D-Bus 实验、不得 commit/push、不得安装到用户目录、不得杀用户进程、不得改 `docs/`（由主 agent 汇总）。

RMT-03 已派发：task_083f6bda2df0 / ctx_2dbfd8c8e41f。WIN-07 已派发：task_bd871655a220 / ctx_8c19f04199c2。

RMT-03 完成并由主 agent 复核：`ApplyMode` 改为同步热切换——`remoteURL`/`startupErr` 改为 `atomic.Value`，`modeSwitch` 互斥串行化；本机→远程先 `manager.Stop`（10s 超时）成功后才切运行态、`reloadInbox`、保存；远程→本机用 `startHub`，失败返回中文错误且不改保存值、窗口留在远程。UI 去掉“需重启”，保存期间按钮禁用显示“正在切换…”，成功/失败都在卡片内提示；`GetState` 的 `restart_required` 仅剩旧文件兜底。主 agent 复核 `CGO_ENABLED=1 go test -race -tags purego . ./internal/...` 4 包全绿（74 通过），并修正 `errRemoteMode` 里过时的“重启 FlowDrop”措辞。Windows 真机热切换未验证。

WIN-07 完成并由主 agent 复核：新增独立 module `tools/winres/`（`github.com/tc-hib/winres v0.3.1`，纯 Go 后处理，无 wails3 CLI/mingw），给已链接 exe 原子写入图标（256/128/64/48/32/16 六尺寸）、版本信息（`0.1.0.0`、FlowDrop、en-US+zh-CN）与 manifest（asInvoker、PerMonitorV2+PerMonitor、longPathAware、Win10/11 GUID）；`scripts/build-desktop-windows.sh` 构建后盖章并 `-verify`，失败即中止；`package-windows.sh` 仅 README 增加一段。主 agent 复核：两个 exe `winres -verify` 均 10 项资源、`file` 仍为 PE32+ GUI、zip 内容正确、desktop `-race` 4 包全绿、`tools/winres` 测试与 gofmt 干净。资源管理器图标缓存/属性对话框/真实高 DPI 观感需 Windows 真机确认。

本轮最终产物（主 agent 重建，2026-11-09 14:53，未 commit/push）：
- `dist/flowdrop-desktop`（Linux，含热切换与远程模式）17110696 B，SHA-256 `ab18f27c58bb00bdfce8bcce0fd9e5503fad3100cc73cd499c9c679a2d5c8eee`
- `dist/flowdrop-desktop.exe`（amd64，含图标/版本/DPI manifest）18376704 B，SHA-256 `87845c235cc4c378482399c878a1a73a60a7a1b19c3587c030357ebe1c80b790`
- `dist/flowdrop-desktop-arm64.exe` 17095168 B，SHA-256 `3703d00b1c045b80df7015e43efcb34168712e324f4ded5c54138d472c3cf2fd`
- `dist/flowdrop-windows-amd64.zip` 7346021 B，SHA-256 `1a23a21d2a088946f2e8bdd0a8b414ca0c5ade2a1408c1a81142912205394209`
- `dist/flowdrop-windows-arm64.zip` 6627177 B，SHA-256 `754b77c16d4759d83193d054e8d65682368467b487a1526a03d1e6a6fbc44bc4`

Windows 真机验收辅助（2026-11-09）：新增只读 `scripts/check-windows-exe.ps1`（打印版本资源/SHA-256、导出内嵌图标为 exe-icon.png，不启动应用），并在 `docs/DEPLOY.md §18.1` 写明用法与期望值。主 agent 已在 Linux 侧完成资源级核对：从 exe 解出 6 个尺寸图标（256/128/64/48/32/16，均为合法 PNG，256/32 已目视确认是正常蓝色圆角图标）、回读版本字段与两份语言、导出 manifest 原文（asInvoker / true/pm / PerMonitorV2,PerMonitor / longPathAware / Win10 11 GUID）均正确。资源管理器图标缓存、属性对话框、真实高 DPI 观感与 Windows 下热切换仍需用户真机执行。

图标占位修复（2026-11-09，用户真机反馈）：用户确认 Windows 热切换正常、版本资源正确（脚本输出与预期一致），但指出 Windows/Linux 的应用与托盘图标都是纯蓝色块。核查：`desktop/assets/icon.png`（256）与 `tray.png`（48）历史上就是单色 #2563EB 圆角方块，没有任何标记，而 `web/public/favicon.svg` 与 Android `ic_launcher_foreground.xml` 早已是白⇄蓝底品牌图形——桌面端是漏了图形的占位资源，不是打包丢失。修复：新增 `scripts/gen-desktop-icons.py`（Pillow，确定性绘制 Android 同款双向箭头，512 应用图标 + 48 托盘，托盘放大到 1.65 保证 16px 可辨），重写两张资源并重建三平台无关的桌面产物。主 agent 复核：从新 exe 解出 6 个尺寸图标均含 ⇄ 图形（256/32 已目视），版本资源仍未变；Windows amd64/arm64 `-verify` 通过。

图标修复后产物（2026-11-09 15:18，未 commit/push）：
- `dist/flowdrop-desktop` 17135240 B，SHA-256 `112007dd2a88adcfdf1ae0422d086b95e54c980ddb3bae0e5ae8eb8264a2cc22`
- `dist/flowdrop-desktop.exe` 18407936 B，SHA-256 `f64ed1bb052af1d3fc5ade9c7ea2239d30b92bd7b493034c19c255f5273f2405`
- `dist/flowdrop-desktop-arm64.exe` 17126400 B，SHA-256 `1462b7fce800ce0e75b74182fbca0ace0c355d3352aae8a8b8ed93f1808c0772`
- `dist/flowdrop-windows-amd64.zip` 7378677 B，SHA-256 `5024dd96d0ab430998c9b416068c88cc8acc0001a24442a6899e3417d68e1051`
- `dist/flowdrop-windows-arm64.zip` 6658440 B，SHA-256 `6610b1a40d39fb21c4252919b75f0e08200b30a321ef03016473ad79fc460503`

## DOC-01 平台支持声明同步（2026-11-09）

用户问「Win/Linux 客户端功能是否一致」后确认：文档仍写着「仅 Linux；不做 Windows/macOS 打包」，与 §18 已交付的 Windows 构建矛盾。纯文档一行级修正，由主 agent 直接改，未派 worker。

- `docs/DEPLOY.md` §16.9：改为「桌面版覆盖 Linux 与 Windows（Windows 构建/打包/限制见 §18；两端共用同一套桌面代码，功能一致，差异只在拖拽、托盘、代理直连等平台适配层）；macOS 未支持」，其余限制项（无自启/无自动选路/无二维码）保留。
- `README.md`：桌面章节标题改为「桌面 App 已交付（Linux + Windows，Wails v3.1.0）」；新增 Windows 产物条目（`dist/flowdrop-windows-{amd64,arm64}.zip`、图标/版本/DPI manifest、拖拽与托盘差异、§18 链接）；「不在 MVP 内」去掉 Windows，只留 macOS 桌面版。
- `docs/PLAN.md`「实际完成状态」真机验收行：说明改为「桌面版 Linux 与 Windows 均已交付（Windows 见 DEPLOY §18，macOS 未做）」。
- 未改：`docs/PRD.md`（需求基线）与 `docs/VERIFICATION.md`（历史验证快照）中的同期表述属时间点记录，不回改。

验收：`grep -rn "仅 Linux\|Windows/macOS 桌面版" docs/ README.md` 仅剩 PRD 范围基线与 VERIFICATION 历史记录命中，README/DEPLOY/PLAN 的当前状态声明已无过期表述。未 commit/push，无代码与产物变化。

## ICON-02 Linux 图标仍为蓝块（2026-11-09，用户真机反馈）

用户反馈 Linux 托盘图标与窗口左上角图标仍是蓝色色块。核查结论（非图标资源本身问题）：

1. 用户桌面项 `~/.local/share/applications/flowdrop-desktop.desktop` 的 `Exec` 指向 `~/.local/bin/flowdrop-desktop`，该副本是 **2026-11-08 10:43** 的旧构建，内嵌 tray.png 为旧的纯蓝色占位（旧 18112 B vs 新 1816 B），所以托盘仍是蓝块；本轮重建的 `dist/flowdrop-desktop` 内嵌的已是带 ⇄ 的新图标（已按字节核对）。
2. 安装脚本从 `deploy/desktop/flowdrop-{48,128,256}.png` 安装启动器/窗口图标，而这三个文件自 2026-11-08 00:39 起一直是**纯蓝占位**，从未随 `desktop/assets/icon.png` 一起更新——即使重装，启动器与任务栏仍是蓝块。

修复（主 agent 直接改）：`scripts/gen-desktop-icons.py` 现在同时渲染 `deploy/desktop/flowdrop-{48,128,256}.png`（与 512 图标同一几何/配色，确定性），已重新生成；`desktop/assets/{icon,tray}.png` 字节未变（哈希 `fd11ebf9…`/`87d06e74…`），因此现有 `dist/` 产物无需重建。目视核对 48px 安装图标与 16/22px 托盘缩略图：⇄ 清晰可辨。

待用户操作：重新运行 `scripts/install-desktop.sh`（会覆盖 `~/.local/bin` 与图标，属用户目录安装，未擅自执行）；若图标缓存未刷新，可 `gtk-update-icon-cache -f -t ~/.local/share/icons/hicolor`。

## DL-01 Linux 桌面下载同名文件被删/覆盖（2026-11-09，用户真机反馈）

用户反馈：图片「下载」与预览里的「下载原图」，若同名文件已下载过一次，第一次点击会把旧文件删掉，第二次点击才下载成功。

定位（主 agent，读 WebKitGTK 文档 + Wails 源码）：Wails v3.1.0 在 Linux GTK3 侧**没有**任何下载处理；WebKitGTK 的 `WebKitDownload::decide-destination` 无处理器时按默认行为存到 `G_USER_DIRECTORY_DOWNLOAD` + `suggested_filename`，而 `allow-overwrite` 默认 FALSE——目标已存在时下载失败并把已有同名文件破坏掉。第二次点击时同名文件已不存在，于是“成功”。这与用户描述的前置条件（“如果这个图片下载过了”）完全吻合；Windows 走 WebView2 自带下载 UI，不受影响。文档依据：<https://webkitgtk.org/reference/webkit2gtk/2.41.2/class.Download.html>（`decide-destination` / `allow-overwrite` 段）。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| DL-01 | 新建 `desktop/downloads_linux_gtk3.go`（`linux && cgo && gtk3`）+ `desktop/downloads_unsupported.go` + 纯 Go 命名/去重逻辑与测试；`desktop/main.go` 仅加一处注册调用。在 GTK 主线程给默认 `WebKitWebContext` 挂 `download-started`，再给每个 download 挂 `decide-destination`，把目标设为 Downloads 目录下**唯一**文件名（`name (1).png` 递增），并用 `sanitize` 处理路径分隔符/控制字符/超长名；绝不覆盖或删除已有文件。仅 Linux；不改 web/、server/ | `CGO_ENABLED=0 go test -tags purego . ./internal/...`、`./scripts/build-desktop.sh` 成功、gofmt/vet；定向验证：隔离容器（`flowdrop-dd16-gui:1`，weston）内连续下载同名文件两次 → 两个文件都在且旧文件字节不变；宿主桌面禁止 GUI 实验 | 45min；报告 /tmp/flowdrop-dl01-report.md | 待派发 |

DL-01 已派发：task_fc7bdda8866f / ctx_2f02f4ce7deb（run_7301d66276fc），spec `/tmp/flowdrop-dl01-spec.md`。等待 worker 回报后由主 agent 复核并重建产物。

## UI-01 / UI-02 界面改版（2026-11-09，用户选定：设置窗 + Web 收件箱，跟随系统浅/深色）

用户参考 magpie 提出界面优化；主 agent 核对 magpie 技术栈为 Go + Wails v3（beta.24）+ 手写 HTML/CSS/JS，与本项目同路线（我们 Wails 3.1.0，收件箱是 React）。视觉基线 `docs` 外的 `/tmp/flowdrop-ui-tokens.md`（浅灰底 + 白卡片、12px 圆角、1px 分隔线、胶囊按钮、系统字体、品牌蓝作强调色）。

| 任务 | 目标与写入范围 | 验收/依赖 | 预算/停止 | 状态 |
|---|---|---|---|---|
| UI-01 | 仅 `web/src/styles.css` + `App.tsx`/`ItemCard.tsx`/`Composer.tsx`/`Login.tsx` 的布局 class：收件箱改单卡片行式列表、顶部栏/Composer/灯箱/登录页/toast 按 token 重做；保留 `.item` 等类名与所有按钮文案 | `cd web && npm run build`；`cd tests && npm test`（16 项）全绿；无头 Chromium 截登录/收件箱浅色/深色三图 | 40min；报告 /tmp/flowdrop-ui01-report.md | 待派发 |
| UI-02 | 仅 `desktop/assets/{index.html,style.css}`（必要时 `app.js` ≤3 行 class 切换）：设置窗按 token 重做卡片/模式选择/表单/设备行/横幅，深色跟随系统；保留所有 id 与可见性逻辑 | `node --check desktop/assets/app.js`；`CGO_ENABLED=0 go test -tags purego ./...`；静态截图 2 张 | 35min；报告 /tmp/flowdrop-ui02-report.md | 待派发 |

两任务文件不重叠；都不跑桌面构建（协调者最后统一重建 dist）。禁止：宿主桌面 GUI 实验、commit/push、装用户目录、改测试断言。

UI-01 已派发：task_2420ef7809e1 / ctx_2741da9f41c6；UI-02 已派发：task_da6e46f6cb90 / ctx_3ee897a54a06。两者与 DL-01 文件不重叠。协调者补充：UI-01 的 E2E 用的是 `dist/flowdrop` 内嵌前端，改完必须先 `./scripts/build.sh` 重建该二进制，否则测的是旧 CSS。

UI-02 完成并由主 agent 复核（2026-11-09）：只重写 `desktop/assets/style.css`（103→644 行），index.html/app.js 未动；选中态用 `input:checked + .mode-body` 相邻兄弟，`grep ':has('` 为空，无 WebKit 版本风险；变量全部按 token 定义，样式表内除 `--shadow` 的 rgba 外无 hex 字面量；`[hidden]{display:none!important}` 兜底不影响 app.js。目视浅/深色截图：卡片层次、选中态（浅色 `rgb(37,99,235)` 边 / 深色 `rgb(122,162,247)`）、配对码、设备行、danger 撤销按钮均正常；渲染自检 0 控制台错误、520px 无横向溢出。真实 Wails/WebKitGTK 窗口尚未复核（随协调者统一构建后由用户真机确认）。

## UI-01 / UI-02 / DL-01 完成（2026-11-09 15:52，主 agent 复核并重建）

**UI-01（Web 收件箱）**：重写 `web/src/styles.css`，`App.tsx` 仅新增 `.conn-dot` 与 `.inbox-card` 包裹，逻辑/文案/依赖未变。E2E 16/16 Chromium 全绿（worker 先重建 `dist/flowdrop` 再跑）；浅/深色截图目视：单卡片行式列表、标签胶囊、操作区右对齐、灯箱与 toast 正常，文字对比度普查 86 个元素均 ≥4.5:1。保留 `.topbar-right`，桌面注入的「设置」按钮位置不变（注入按钮自带内联样式）。

**UI-02（设置窗）**：只改 `desktop/assets/style.css`（103→644 行），`index.html`/`app.js` 未动；选中态用 `input:checked + .mode-body`，不依赖 `:has()`；颜色全走 token；`[hidden]{display:none!important}` 兜底。浅/深色截图以真实 `app.js` + stub bridge 渲染，0 控制台错误、520px 无横向溢出。

**DL-01（Linux 下载同名文件）**：新增 `desktop/downloads_linux_gtk3.go`（默认 `WebKitWebContext` 的 `download-started` → 每个 download 的 `decide-destination`，返回 Downloads 目录下唯一路径；显式 `allow_overwrite(FALSE)`；`failed` 只记 domain/code）、`desktop/download_naming.go`+`_test.go`（basename/控制字符/`..`/180 字节截断 + `name (n).ext` 去重 + XDG→`~/Downloads`→`$HOME` 目录解析）、`desktop/downloads_unsupported.go`（非 GTK3 no-op）；`main.go` 增加一处 `installDownloadHandler()`。偏差说明：注册用 `application.InvokeAsync` 而非 `InvokeSync`——app.Run 之前 InvokeSync 会自锁（Wails 早队列只在 Run 内 drain），worker 在容器里用真实 `application.New`+`Run` 路径验证过。隔离容器证据（`flowdrop-dd16-gui:1`，WebKitGTK 2.54.0）：预置 30 字节 `same.png`，连续下载同名响应两次 → 旧 `same.png` 字节不变（sha256 `340e5613…`）+ `same (1).png`、`same (2).png`；未挂 hook 的对照组不产生新文件。

主 agent 复核：`CGO_ENABLED=1 go test -race -tags purego . ./internal/...` 4 包全绿；`CGO_ENABLED=0 go test -tags purego .` 含 5 个下载命名测试全过；gofmt/go vet 干净；`build-desktop.sh`、两个 Windows 构建与打包全部成功，两个 exe `-verify` 均 10 项资源（图标 6 尺寸 + group + 版本双语 + manifest）；产物中确认含 `inbox-card`、`input:checked + .mode-body`、`--accent-soft`、`flowdrop-download-handler`。真实 App 内点击下载与真实 Wails/WebKitGTK 窗口观感仍需用户真机确认。

本轮产物（2026-11-09 15:52，未 commit/push）：
- `dist/flowdrop-desktop` 17163912 B，SHA-256 `f9023bf58b55311889fb0a553c7b731129f23aa32a1b3d29aef4f011c920bca0`
- `dist/flowdrop`（CLI，含新 Web UI）11780258 B，SHA-256 `acb344088c381dd45b0b2f1c8d0d7be178bb37cd5941670acc6a5bf4d7748483`
- `dist/flowdrop-desktop.exe` 18419200 B，SHA-256 `84ca90b0757d3e4010ebe69c180c2379c656296eac32646d728d92631fdb9404`
- `dist/flowdrop-desktop-arm64.exe` 17137664 B，SHA-256 `4e3c360f115a90baec27ca1c5cdaf7125159ae6d2334ec4ddb51dbc091af6453`
- `dist/flowdrop-windows-amd64.zip` 7384963 B，SHA-256 `2cac7cc8524a68e58f208898cb3c1e0e8502350adce8e00e3f23c7d307f58cc0`
- `dist/flowdrop-windows-arm64.zip` 6665341 B，SHA-256 `32c04efc29fd7e7217daebffcfbc2c89534561eb86fcea9c64d2b9ec0bef9937`

文档：`docs/DEPLOY.md` §16.12（下载与同名文件）+ README 桌面特性两条（界面改版、下载重名）。未改 `server/`，Android 产物未重建（本轮无 Android 改动）。

## UI-03 收件箱窗口去掉原生菜单栏（2026-11-09，用户反馈“最上面还有设置/在浏览器打开这一排”）

用户对比 magpie 指出窗口顶部多了一层按钮。确认来源：`openInbox` 给 `application.LinuxWindow{Menu: d.newInboxMenu()}`，Wails GTK3 会把菜单渲染成窗口内的 GtkMenuBar（设置…/在浏览器中打开/退出 FlowDrop）。页面自身已有桌面注入的「设置」按钮，托盘菜单已有「在浏览器中打开」「退出」，`Ctrl+,` 由 `KeyBindings` 全局处理，因此这排是重复入口。

改动（主 agent 直接改，最小面）：`desktop/main.go` 去掉 `Linux: application.LinuxWindow{Menu: ...}` 与 `newInboxMenu()`，并把该窗口的注释改成“不设窗口菜单”的理由；同时把已过期的“下载沿用 WebKit 原生行为”注释改为指向 `downloads_linux_gtk3.go`。已重建并安装：`dist/flowdrop-desktop` 与两个 Windows exe/zip。

复核：`CGO_ENABLED=0 go build -tags purego`、两个平台构建、exe `-verify` 10 项资源均通过。真实窗口内“已无菜单栏”需用户重启后确认；系统标题栏仍在（下一步待用户决定是否做无边框自绘）。

## UI-04 无边框窗口 + 自绘标题栏（2026-11-09，用户确认要做）

用户反馈「按钮上面还有系统标题和图标，magpie 没有」，确认后采用无边框方案。

改动：
- `desktop/main.go`：收件箱与设置窗都加 `Frameless: true`；收件箱注入 `desktopWindowChromeJS`，设置窗注入 `settingsWindowChromeJS`；`handleRawMessage` 先走 `runWindowAction`。
- 新增 `desktop/window_chrome.go`：注入式 36px 标题条（品牌标记 + 最小化/最大化/关闭，`--wails-draggable` 拖动、按钮 `none` 排除），以及三个固定 raw message 的校验执行（收件箱要求当前 Hub origin，设置窗进程内无条件）。
- `desktop/settings_entry.go`：登录页固定「设置」按钮下移 36px，避免被标题条盖住。
- `desktop/assets/{index.html,style.css,app.js}`：设置窗静态标题条 + 样式 + 三个按钮的 raw message 发送。
- 标题条只保留品牌标记、不重复写应用名：页面自身的品牌头部已经是唯一的应用名，避免“FlowDrop 流转”在顶部出现两次。

验证：`gofmt`/`go vet`/`CGO_ENABLED=0 go build -tags purego` 通过；两个平台重建与打包成功、exe `-verify` 10 项资源；用无头 Chromium 复刻同一套 CSS/DOM 渲染设置窗与收件箱（浅/深色）确认布局无重叠、无横向溢出、对比度 7.0–13.4:1；设置窗真实 `app.js`（stub bridge）渲染 0 控制台错误。**真实 Wails/WebKitGTK 下的拖动、双击最大化、边缘缩放、贴边行为只能用户真机确认**，异常时把 `Frameless` 改回 false 即可恢复。

产物（2026-11-09 16:03，未 commit/push）：
- `dist/flowdrop-desktop` 17172104 B，SHA-256 `b882333eebe7ac5c6ad4314adeee38480a5e08553c38f4f92871297175532bd6`
- `dist/flowdrop-desktop.exe` SHA-256 `ed8ff9d08748675f26b799fbd606c89556afe1b44e947d971c8f41ff1f20d0f1`
- `dist/flowdrop-desktop-arm64.exe` SHA-256 `950be472ef27c7f69cd6f853bac55c3c75e81385a3ab5de13cfd0a6860575c9a`
- `dist/flowdrop-windows-amd64.zip` SHA-256 `8c8fcb471e2043e5c9d2eba69ea2b4443bb89c227e605c603918b95471ce3942`
- `dist/flowdrop-windows-arm64.zip` SHA-256 `513248a41ea2194ac83f0d6134e325c2e8cfb71077b1fc50e6aaed4cad9c6962`

## UI-05 KDE Wayland 下 KWin 仍画标题栏（2026-11-09，用户真机反馈）

现象：无边框版本在 KDE Wayland 上仍被 KWin 补了一条系统标题栏（最小化/最大化/关闭）。对照 magpie 源码 `internal/gui/titlebar_gtk3.go` 找到根因与官方绕法：GTK3 在 Wayland 上会把 undecorated 窗口声明成"由服务端装饰"，KWin 于是补标题栏；magpie 用 `gdk_wayland_window_announce_csd()` 在 realize 时反过来声明"客户端自绘边框"。

修复：新增 `desktop/window_frame_linux_gtk3.go`（`linux && cgo && gtk3`）：在窗口 realize 时（以及之后每次 realize）调用 `gdk_wayland_window_announce_csd`，用 `g_object` 标记保证幂等；`window_frame_unsupported.go` 为其它平台空操作。注册用 `application.InvokeAsync`（窗口在 `app.Run` 之前创建，`InvokeSync` 会像 DL-01 记录的那样自锁），两个窗口创建后各调用一次。

验证：GTK3 构建通过；X11（Xvfb+openbox 容器）下窗口仍无任何系统装饰、两个窗口与自绘标题条正常、无崩溃；`grep 'window frame'` 在 X11 下无输出（符合空操作预期）。KWin Wayland 的真实效果只能用户真机确认。

产物（2026-11-09 16:15，未 commit/push）：`dist/flowdrop-desktop` `e8859d522f7f96ac52d5bdb2a2d9eac8dc403b526c61946fe30681f8fb4d2db4`；`dist/flowdrop-desktop.exe` `e4f8ee122357af8ce7e96e967e5a717f20e5e66f6e0b5d650f7cbb23586ce91b`；arm64 exe `715246d0b2d2452f23366348743b65e49fd1a1718b23889e676b131516cf7414`；zips `56c2e100ce6235e40ecb5c556c2cb89c8a0aa627a69dcbee5fc6c14585ed2ac3` / `48022eef568f689b5d3959e1f1a49bd024b715a31911ffaaea4ed30c4dd83bb3`。

## UI-06 标题条颜色、配对页滚动条（2026-11-09，用户真机反馈 1、2）

现象：① 界面顶部标题条与下方页面颜色不一致（条是白色，页面是浅灰）；② 输入配对码的页面出现竖向滚动条；③ 配对页无法拖动窗口（见 UI-07）。

根因：① 注入的标题条把背景写死成 `#ffffff`（深色 `#232327`），设置窗标题条用的是 `var(--card)`，而两个页面的底色是 `var(--bg)`（`#f4f4f6` / `#1a1a1e`）。② 无边框窗口给 `body` 加了 `padding-top:36px`，而 Web UI 的登录/配对容器是 `min-height:100dvh`，两者相加比视口高 36px，必然出滚动条（设置窗不受影响：`* { box-sizing: border-box }` 让 `body` 的 `min-height:100vh` 包含内边距）。

改动：注入的 `desktopWindowChromeCSS` 改为取页面自己的 token（`var(--bg)`/`var(--fg)`/`var(--line-2)`/`var(--accent)`，带浅色回退），并定义 `--flowdrop-chrome-top: 36px`，标题条高度与 `body` 内边距都取该变量；`desktop/assets/style.css` 的 `.titlebar` 由 `var(--card)` 改为 `var(--bg)`；`web/src/styles.css` 的 `.login-wrap`、`.boot` 改为 `min-height: calc(100dvh - var(--flowdrop-chrome-top, 0px))`（浏览器/手机端无该变量，回退 0px，行为不变）。

证据（隔离容器 `flowdrop-dd16-gui:1` + Xvfb/openbox，新构建实跑）：标题条像素 = `srgb(244,244,246)`，与页面 `--bg` 完全一致（两个窗口均如此）；配对页右侧滚动条消失、配对卡片在标题条下方居中；`desktop/window_chrome_test.go` 断言 token 用法与该高度的共享约定；web E2E 16/16 通过。

## UI-07 配对页标题条拖不动窗口（2026-11-09，用户真机反馈 3）

现象：同一份代码，设置窗能拖动、配对页（收件箱窗口）拖不动。

根因：GTK3 无边框窗口没有标题栏部件，能移动窗口的只有 `gtk_window_begin_move_drag`。Wails 运行时的 `--wails-draggable` 路径在 `wails:drag` 消息里带的是它自己在 `button-press-event` 里记下的按键/根坐标/时间戳；这两个窗口上那份记录始终是空的，于是发给合成器的请求是 button=0，被静默忽略（按钮是普通 DOM click，所以照常工作，看起来像"只有拖动坏了"）。实证：Xvfb+openbox 下用 `xdotool` 拖两个窗口都不动，而同环境里一个最小 PyGObject GTK3 窗口用 `begin_move_drag` 能正常被拖走（100,100 → 175,175），证明不是环境问题。

修复：新增 `desktop/window_drag_linux_gtk3.go`：在 WebView 的 `button-press-event` 上，若主键按下且落点在顶部 36px（`event->y`，逻辑像素）且不在右侧 132px 的按钮区内，就以**这次按下自己的** button/x_root/y_root/time 调 `gtk_window_begin_move_drag`（Wayland 需要的 serial 同源），然后返回 FALSE 让页面照常收到事件；`g_object` 标记保证每个 WebView 只连一次，每次页面加载后重新装一遍。`window_drag_unsupported.go` 为其它平台空操作（Windows 靠 Wails 自带路径）。`main.go` 在创建两个窗口后分别调用 `installTitlebarDrag`。

证据（同一隔离容器）：新增日志两行 `window drag: title bar starts a window move on press`；`xdotool` 拖动后收件箱窗口 (150,60) → (225,110)、设置窗 (250,70) → (310,100)；标题条最右侧关闭按钮仍触发"退出 FlowDrop？"确认框（132px 排除区生效，按钮点击未被拖动手势吞掉）；`go test -tags gtk3 .` 与 `CGO_ENABLED=0 go test -tags purego . ./internal/...` 通过；web E2E 16/16。

未验证：KWin Wayland 真机上的拖动（本地无用户会话权限），需用户重启后拖一下标题条确认。

产物（2026-11-09 16:36，未 commit/push，已 `scripts/install-desktop.sh` 安装到 `~/.local`）：
- `dist/flowdrop-desktop` 17180392 B，SHA-256 `82323ba3dae45552ea0b26dc3df609621ab83d070f322ed28c9d16b0d0226fcc`
- `dist/flowdrop`（CLI/Web，含新 Web UI）SHA-256 `6dff26eed30eaa7a1f18cb5063a48f4c5a609ad381849656003ec294b1a02ca4`
- `dist/flowdrop-desktop.exe` SHA-256 `b21c0dc13c46ec355e7dd61ed779907c620c5e637965eb712812fe73689eeef7`
- `dist/flowdrop-desktop-arm64.exe` SHA-256 `4515db6b52dc8368b2a889121cfd0c5d7e1a0acff2f939ed30a66537299e60ff`
- `dist/flowdrop-windows-amd64.zip` SHA-256 `ec5fb59443fca3c224f0ced8f21955c0017fe48af92d7fd7387534f3ef292562`
- `dist/flowdrop-windows-arm64.zip` SHA-256 `160fcef935b6ea4e1e1d52a24135e2ec4948760af43d0ea7191aa18f72f98ca6`

## DL-01 桌面窗口免码登录（2026-11-09，用户反馈"都在一台机器上为什么还要配对"）

现象与结论：桌面收件箱窗口加载的是 Hub 真实 HTTP origin，与手机同一套认证（配对码 → HttpOnly cookie），桌面壳刻意不给它特权通道。但 Cookie 会话 TTL 默认 168h（`SessionTTLHours`），设备令牌本身长期有效，因此桌面每 7 天要"设置窗生成码 → 收件箱输码"重配一次；用户认为这不该是人工步骤。

方案（不改服务端、不改 `docs/PAIRING.md` 冻结接口）：桌面壳把自己当 `native` 客户端——启动时用进程内 `CreatePairingCode()` 得到的码，由壳自己的 Go HTTP 客户端 `POST /api/v1/auth/pair{native:true}` 换 `device_token`，存进 desktop.json（`appsettings.Save` 已是原子 0600 写）；之后每次收件箱页面加载注入引导脚本：无有效 cookie 时用 `Authorization: Bearer` 请求 `GET /api/v1/auth/session`（服务端按冻结契约重发 cookie），成功即 reload，已登录则不动。令牌长期有效，只在首次/被撤销后才重新配对；设置页把本机设备显示为不可撤销的一行。

| 编号 | 单一目标 / 允许写入 | 验收、预算与停止条件 | 状态 |
|---|---|---|---|
| DL-01A | 免码登录 Go 侧：`desktop/internal/appsettings/**`、新增 `desktop/desktop_login.go` + 测试 | 配对助手/引导脚本/设备名纯函数 + httptest 单测；vet 与定向测试全绿；15–20min；不碰 main.go/service.go/资产/服务端，失败即停 | 完成 ctx_b9c67b40789c / task_c8e6e7496e1c；主 agent 复核通过（见下） |
| DW-01 | 设置窗作为收件箱的附属对话框：新增 `desktop/window_owner_*.go`、`desktop/assets/index.html`、`desktop/assets/app.js` | transient+居中+present 三个原生助手；删掉页脚与托盘重复的「打开收件箱」；不碰 main.go；15min | 完成 ctx_468848ea259e / task_0316aa42accc；接线并入 DL-01B（`main.go` 只有一个 writer） |
| DL-01B | 接线：`desktop/main.go` 启动流程与收件箱 JS 链、`desktop/service.go` 设备列表过滤 | 本地模式永远先开收件箱（仅 Hub 启动失败才显示设置窗）；容器内实跑确认自动登录、重启后仍登录、设备列表只有一行「本机桌面」 | 完成 ctx_8704d33392db / task_55dc01d59783 + 追加 ctx_9b0aeb253b00（sessionStorage 2 次重载上限）；主 agent 复核：gofmt 干净、gtk3 4 包测试全过 |
| DW-02 | 接线 DW-01 到 `desktop/main.go` 的窗口创建 | 为避免 `main.go` 并发写入，已并入 DL-01B 的同一份规格 | 并入 DL-01B |
| DL-01C | 构建交付 + 用户验收说明 | ctx_1101549b48cb / task_f0bf68c9ba89：构建成功（`315beba2…`），但页面侧 3 项 FAIL（首启/重启都停在配对页、设置窗取不到），证据缺 run 日志；根因由主 agent 定位 | 失败（已由 DL-01D/E 修复） |
| DL-01D | 注入脚本互相隔离 + 登录引导排最前（主 agent 直接修复，`desktop/main.go`、新增 `injected_scripts_test.go`） | 容器 3/3 首启即登录（`/api/v1/items` 200）、`desktop log in` 只一次；单测断言 5 段隔离且 bootstrap 在前 | 完成 |
| DL-01E | 原生窗口句柄竞态修复（主 agent 直接修复，新增 `native_window_retry.go`，改 `window_frame_linux_gtk3.go`、`window_owner_linux_gtk3.go` + 静态测试） | 容器实测 `window frame` 不再报 not available、`WM_TRANSIENT_FOR` = 收件箱窗口号、居中偏差 0px | 完成 |

风险记录（本轮不修，单独确认）：核对 Wails v3.1.0 源码，`WebviewWindowOptions.JS` 只在 Linux（`webview_window_linux.go:527`）与 macOS 注入；Windows 路径仅在 `HTML` 模式使用它（`webview_window_windows.go:2835`），URL 模式未注入。若属实，Windows 上无边框窗口的注入标题条与「设置」入口从未生效，需要单独排查与修复。

### DL-01D 注入脚本互相隔离（2026-11-09，主 agent 直接修复）

现象（DL-01C 容器实跑）：首启与重启都停在四位配对码页，`/api/v1/items` 一次都没请求；日志显示免码配对本身成功（`POST /api/v1/auth/pair` 200、`desktop login` 一行、desktop.json 0600 含令牌），设置窗里也没有注入的「设置」按钮。

定位：Wails 把 `WebviewWindowOptions.JS` 整段当**一个脚本**执行。容器里这次注入落在首个文档上（早于 Hub 页面就绪）：`desktopWindowChromeJS` 因为显式等待 `document.body` 而幸存（标题条正常出现），紧随其后的 snippet 在顶层抛错，于是**同一脚本后续语句全部不执行**——设置入口、文件拖放桥、登录引导依次丢失。用逐个 snippet 后插 `fetch('/probe-…')` 的临时探针复现：带探针的构建里 5 段全部执行、登录成功；不带探针的生产构建稳定只发 1 次 session 请求。探针已全部移除。

修复：`joinInjectedScripts` 把每段脚本包进 `try { … } catch (e) { /* flowdrop: isolated snippet */ }`，并把登录引导排到最前（`inboxInjectedJS`）。这样任何一段顶层失败只损失该功能，页面的登录不再依赖别人的执行结果。新增 `injected_scripts_test.go`：断言每段被隔离、顺序保留、bootstrap 在 chrome/设置入口之前、令牌只出现一次。

证据（同一隔离容器，`--network none`，Xvfb+openbox）：修复后连续 3 次首启都是 `items=200` 且发生一次 reload（引导用 Bearer 换 cookie），设置入口按钮出现在页面头部；重启同一 `HOME` 时 `desktop login` 出现 0 次、`items=200` 仍成立、`paired_devices` 只有 1 行且 id 与 desktop.json 一致。

### DL-01E 原生窗口句柄竞态（2026-11-09，主 agent 直接修复）

现象：容器里每次启动都打印两次 `window frame: native GTK window not available; KWin may draw its own title bar`，偶尔还有 `settings dialog: native GTK window not available`。即 UI-05 的 CSD 声明与设置窗 transient 都可能在窗口原生句柄还没创建时放弃。

修复：新增 `desktop/native_window_retry.go` 的 `withNativeWindows(what, windows, apply)`——在 GTK 线程上以 80ms 间隔重试最多 25 次（约 2 秒），全部就绪才执行；超时只记一行日志、退回默认行为。`ownWindowFrame` 与 `attachSettingsDialog`/`presentSettingsDialog` 改用它；`window_owner_linux_gtk3.go` 里随之无用的 `nativeGtkWindow` 删除，静态测试改为断言使用该重试助手。

证据：同一容器里 `window frame` 失败日志消失；点击页面注入的「设置」按钮后设置窗 780×760 出现，`xprop WM_TRANSIENT_FOR` = 收件箱窗口号 `0x40000e`，两个窗口中心点都是 (640,450)（居中偏差 0px）。

未验证：KWin Wayland 真机上的 transient 任务栏归属与 CSD 效果、Windows、托盘交互；`Ctrl+,` 在容器里没有触发（合成按键拿不到 GTK 加速器焦点），所以 transient 是通过页面「设置」按钮验证的。

### DL-01 交付产物（2026-11-09 17:25，未 commit/push，未安装）

最终产物（2026-11-09 17:33，含 WIN-01；已 `scripts/install-desktop.sh` 安装）：
- `dist/flowdrop-desktop` 17674312 B，SHA-256 `949b0e883981e2dd8727cb18a9cee68d7192ae3fdf3ebdf777094ba6ae27d7eb`（`~/.local/bin/flowdrop-desktop` 同值）
- `dist/flowdrop-desktop.exe` `e8904220e902c4607bc91ff5ca534a1d1dddda6efc1a84b63b92e828500aa3fa`
- `dist/flowdrop-desktop-arm64.exe` `99ff6282a84c6718eb34d05e5802e5e5f65736d18b05cbc7c7ddaf72edc21959`
- `dist/flowdrop-windows-amd64.zip` `92d1aa8581afd0caed9484ff229aec006e96166d3c99ab828cf260bcc55a824a`
- `dist/flowdrop-windows-arm64.zip` `ba7dd58adcd8198ebbdd16e8b00cb5b2c8f316702ae179df59e36e1abff6ff3b`
- 最终构建的容器冒烟（同镜像、`--network none`）：`items=200`、`desktop login` 1 行、`window frame` 失败 0 次、点「设置」后 `WM_TRANSIENT_FOR=0x40000e`（收件箱）。
- 重装前的一次安装（`ae3295fb…`）已被上面这版覆盖。

验收：`gofmt -l` 空；`go vet -tags gtk3 .`；`go test -tags gtk3 . ./internal/...` 与 `CGO_ENABLED=0 go test -tags purego . ./internal/...` 全绿。容器证据：首启 `items=200` + 一次 reload、`desktop login` 一行、desktop.json `-rw-------`、日志 0 次令牌明文；重启 `desktop login` 0 次且 `paired_devices` 仅 1 行、id 一致；设置窗 `WM_TRANSIENT_FOR=0x40000e`、中心点重合。未验证：KWin Wayland 真机、Windows、托盘。

用户待办：从托盘彻底退出旧实例再重启验证。

已安装（2026-11-09 17:29，用户确认）：`scripts/install-desktop.sh` → `~/.local/bin/flowdrop-desktop`，SHA-256 与 `dist/flowdrop-desktop` 一致（`ae3295fb…`）。WIN-01 接线后 Linux 产物会重建一次并覆盖安装（行为不变，只多一次空操作调用）。

## WIN-01 Windows 上恢复被注入的页面脚本（2026-11-09，用户要求修复）

- 只读核对 Wails v3.1.0：`WebviewWindowOptions.JS` 在 Windows 上只被 `options.HTML` 分支使用（`webview_window_windows.go:2835/2845`），两个窗口都用 URL 创建，因此 Windows 上：收件箱（无边框）没有注入的标题条/最小化/关闭/「设置」入口/拖放桥/免码登录引导；设置窗自带的标题栏按钮也没人绑事件（`settingsWindowChromeJS` 从未执行）。Hub runtime 走 `navigationCompleted` 的 `execJS`，所以 `window._wails` 存在、原始消息可发，只是按钮与脚本缺失。
- 方案：新增 `desktop/injected_js_windows.go`，在每个窗口的 `events.Windows.WebViewNavigationCompleted` 回调里注入与 Linux 相同的脚本（`go w.ExecJS(js)`，避开 `ExecJS` 内部 `InvokeSync` 在 UI 线程上的自锁）；其它平台 `injected_js_other.go` 空操作，`main.go` 各加一行调用；`JS:` 选项保持不变（Linux/macOS 仍靠它）。静态测试锁住结构与两个窗口的调用。
- 验收：Linux gtk3/purego 的 vet 与测试全绿、gofmt 干净；Windows amd64/arm64 交叉编译与打包成功并记录 SHA-256。**真实 Windows 行为未验证**（无 Windows 机器），只能确认可编译。
- 状态：完成（ctx_dd345b918ba8 / task_fd4fe9af4947）。新增 `desktop/injected_js_windows.go`、`desktop/injected_js_other.go`、`desktop/injected_js_install_test.go`（worker 把任务里给的 `injected_js_test.go` 改名：`_js` 后缀会被 Go 当 GOOS 静默忽略，首版 0 测试在跑）；`main.go` 两个窗口各加一行 `installInjectedJS`，`JS:` 选项保持不变。交叉编译四个 Windows 产物成功。

## CLEAN-01 清理旧版本兼容与退役代码（2026-11-09，用户要求）

背景：用户指出当前仍是开发阶段，不需要兼容旧版本、旧配置或旧数据，要求清理为兼容保留的代码。只读排查（主 agent）确认两处成规模的遗留：

- **服务端旧密码认证**：`internal/auth` 的 Argon2id 密码哈希与 `admin_users`/`sessions` 表在 PA-07 后已不参与任何认证（`Server.sess`、`Deps.Sessions`、`Janitor.Sessions` 只为“清历史表”而存在，`loginLimiter` 字段无人使用），`server.go` 注释自述“PA-07 retires it”。
- **桌面端旧配置兼容**：`desktop.json` 缺 `mode` 字段按“本机 Hub”处理（`validateMode` 的 `case ""`、`GetState` 的 `mode == ""` 分支、`appsettings_test` 的两个 `TestLegacy*`），以及 `State.restart_required`——它自述只描述“旧版保存了但没应用”的状态，`ApplyMode` 热切换后正常路径永远为 false。

保留（不属于旧版本兼容）：`web/src/clipboard.ts` 的 `execCommand` 回退（不安全上下文下的浏览器能力回退）、Android 的 `@Suppress("DEPRECATION")`（平台 API 在 minSdk 上仍要工作）、mDNS TXT `v=1` 与 `/api/v1` 前缀（当前协议本身，不是旧版本分支）、`desktop/desktop_login.go` 的“撤销旧设备记录”（同一次配对内的运行时一致性）。

| 编号 | 单一目标 / 允许写入 | 验收、预算与停止条件 | 状态 |
|---|---|---|---|
| PA-07 | 删除旧密码认证与 session 表：`server/**`（含 `internal/auth` 删除、新增 `internal/token`、`api`、`pkg/hub`、`internal/db`、`internal/config`、`cmd/flowdrop`、测试与 `go.mod`） | 见下方“PA-07 规格”；`gofmt -l` 空、`go vet ./...`、`go test ./...`、`go build ./...` 全绿；新建库无 `admin_users`/`sessions`（新增 schema 断言测试）；20–25min；不碰 docs/desktop/web/android | 完成 ctx_7556c4f06d39 / task_8258188fbe31 + 追加 ctx_9e9fd7ad5fa4 / task_97187d19d95c |
| DL-02 | 删除桌面端旧配置兼容：`desktop/internal/appsettings/**`、`desktop/service.go`、`desktop/assets/{app.js,index.html}`、相关 `_test.go` | 见下方“DL-02 规格”；`gofmt -l` 空、gtk3 与 purego 两套 vet/test 全绿、`node --check desktop/assets/app.js` 通过；20min；不碰 docs/服务端 | 完成 ctx_6da96bdcf0cd / task_b94a16dfc7ae |

文档（`README.md`、`docs/ARCHITECTURE.md`、`docs/DEPLOY.md`、`docs/PAIRING.md`）由主 agent 在代码合入后统一改，避免两个 worker 同时写同一文件。

### PA-07 规格（旧密码认证退役）

目标：Hub 只剩设备配对一条认证路径，数据库不再创建旧认证表，代码里不再有“为清历史数据而保留”的 store。

必须完成：

1. `server/internal/auth/` 整体删除，新增 `server/internal/token/token.go`（+ `token_test.go`）：`Random(n int) string`（crypto/rand，base64url 无填充，与现有 `NewRandomToken` 行为一致）、`Hash(s string) string`（小写 hex SHA-256）。`internal/store`、`internal/api` 的引用改到新包。
2. `internal/db/db.go`：v1 迁移删除 `admin_users`、`sessions` 表与 `idx_sessions_expires` 索引（开发阶段不写 DROP 迁移；旧开发库留着空表不影响）。新增 `db_test.go`：临时目录 `Open` 后 `sqlite_master` 里没有 `admin_users`/`sessions`，且 `pairing_*`/`inbox_items`/`blobs` 存在。
3. `internal/api`：删 `Server.sess`、`Deps.Sessions`、`Server.loginLimiter` 与它的初始化；`reqSession` 去掉 `auth.Session` 内嵌，改为显式字段（设备 ID、原始 token、token 哈希、cookie 过期时间），`withAuth`/`ws.go` 的限流键、`sessionFor`、`handlePair` 的 `ExpiresAt` 跟着改；`janitor.go` 删 `Sessions` 字段与第一段清理及日志；`context.go` 的 limiter 注释改成“配对码猜测”。
4. `pkg/hub/hub.go`：删 `sess` 字段、`auth` 导入、两处 `Sessions:`；更新提到 sessions 的注释。
5. `internal/config`：`LoginLimitPerMinute`/`login_limit_per_minute`/`FLOWDROP_LOGIN_LIMIT_PER_MINUTE` 更名为 `PairLimitPerMinute`/`pair_limit_per_minute`/`FLOWDROP_PAIR_LIMIT_PER_MINUTE`（它限制的是 `/auth/pair` 每 IP 请求），Validate 默认值与 `config_test.go` 同步。
6. `cmd/flowdrop/main.go`：doctor 的明文 HTTP 提示里删掉 "Passwords, "（现在没有密码了）。
7. 测试：`store_test.go` 去掉只为建 admin 用户而存在的 auth store 初始化；`api_test.go` 删 legacy session fixture 与相关断言、所有 `Sessions:` 字段、`auth.TokenID` 改 `token.Hash`；`pairing_api_test.go` 的 “login route 不存在” 断言保留（防止回退），只改注释措辞。
8. `go.mod`/`go.sum`：`golang.org/x/crypto` 若已无引用则 `go mod tidy` 移除。

停止条件：任何一步需要改 `docs/`、`desktop/`、`web/`、`android/` 或需要新增依赖时停下回报。范围外发现单独记录，不顺手改。

### DL-02 规格（桌面端旧配置兼容）

目标：`desktop.json` 只有当前格式，`mode` 必填；设置窗不再有“保存了但没应用”的旧状态。

必须完成：

1. `internal/appsettings`：`Default()` 返回 `Mode: ModeLocal`；`Mode` 的 json tag 去掉 `omitempty`；`validateMode` 去掉 `case ""`（空 mode 视为无效并报错）；删除 “older versions keep loading unchanged”“file from an older version” 一类注释（改成描述当前规则）；`Settings` 里 `AdditionalBindAddresses` 的注释同样去掉“旧版文件”措辞。
2. 测试：删 `TestLegacySingleAddressFileStillLoads`、`TestLegacyFileWithoutModeIsLocal`；所有走 `Save`/`Validate` 的 `Settings{}` 字面量补 `Mode: ModeLocal`（`appsettings_test.go`、`desktop/desktop_login_test.go`、`desktop/pairing_service_test.go`、`desktop/service_test.go`、`desktop/desktop_helpers_test.go`）；`TestValidate*` 表格里 “empty is local” 用例改为断言空 mode 报错；`appsettings_test.go` 里 “stays compatible with older desktop builds” 的注释改成“单地址模式不写该字段”。
3. `service.go`：删 `State.RestartRequired` 字段与 `GetState` 里的赋值；删 `mode == ""` 回退；`remoteHubMode` 的注释改成“文件可能在运行中被手工改过”，行为保留。
4. `assets/app.js`：删 `restartBannerShown`、`renderRunMode` 里 `run-mode-restart` 的显示、保存回调里的 `restart_required` 分支（成功一律显示现有成功文案）；`assets/index.html` 删 `#run-mode-restart` 元素。
5. `service_test.go`：删/改 `RestartRequired` 断言（保留 `Mode`/`RemoteURL`/`Running` 的断言）。

验证命令（Linux 缺 GTK 头文件，用 sysroot）：

```
SYSROOT=$HOME/.local/share/flowdrop-desktop/sysroot
export PKG_CONFIG_PATH=$SYSROOT/usr/lib/x86_64-linux-gnu/pkgconfig
export CGO_CFLAGS="-I$SYSROOT/usr/include -I$SYSROOT/usr/include/x86_64-linux-gnu"
export CGO_LDFLAGS="-L$SYSROOT/usr/lib/x86_64-linux-gnu"
gofmt -l . ; go vet -tags gtk3 . ; go test -tags gtk3 . ./internal/...
CGO_ENABLED=0 go test -tags purego . ./internal/...
node --check assets/app.js
```

停止条件：需要改 `docs/`、`server/`、`web/` 或做 GUI 截图时停下回报。

### CLEAN-01 执行结果（2026-11-09 18:30，主 agent 复核）

PA-07（服务端）：
- 删除 `server/internal/auth/`（Argon2id 密码哈希 + session store），新增 `internal/token`（`Random`/`Hash`），`store`、`api` 的令牌生成与哈希改用它；`sessionIDForToken` 与 `auth.TokenID` 的重复实现合并。
- 数据库 v1 迁移去掉 `admin_users`、`sessions`、`idx_sessions_expires`；新增 `internal/db/db_test.go`，断言新建库没有这两张表、`MigratedVersion=2`。
- `api`：删 `Server.sess`、`Deps.Sessions`、`Server.loginLimiter`；`reqSession` 改为显式字段（`DeviceID`/`Token`/`TokenHash`/`ExpiresAt`）；`Janitor` 只做 blob/temp 清理。
- `pkg/hub`：删 `sess` 字段与两处 `Sessions:`；注释去掉 session 表述。
- 配置：`login_limit_per_minute`/`FLOWDROP_LOGIN_LIMIT_PER_MINUTE` → `pair_limit_per_minute`/`FLOWDROP_PAIR_LIMIT_PER_MINUTE`。主 agent 复核时发现原 `LoginLimitPerMinute` 其实没有消费方（配对端点误用 `APILimitPerMinute`，文档写的 5/分钟从未生效），追加一步接线：新增 `Server.pairLimiter`，`handlePair` 用它限流，并补 `TestPairRateLimitUsesPairLimitPerMinute`（第 3 次 429，API 流量不受影响）。
- `go.mod`：`golang.org/x/crypto` 降为 indirect（argon2 已删；`cmd/mdnsprobe` 经 miekg/dns 仍间接用到 ed25519）。

DL-02（桌面端）：
- `appsettings`：`mode` 必填且始终写出，`Default()` 带 `ModeLocal`，`validateMode` 不再接受空值；`Load` 缺 `mode` 的文件按不可用报错（调用方已有“回退检测默认值并重写”路径）。
- `State.RestartRequired` 与设置窗的 `restart_required` 横幅（JS 变量、HTML 元素、保存回调分支）删除。
- 测试：`TestLegacySingleAddressFileStillLoads`/`TestLegacyFileWithoutModeIsLocal` 换成 `TestFileWithoutModeIsRejected`；所有 `Settings{}` 字面量补 `Mode`。
- `applyNetwork` 原先重建 `Settings` 保存，现显式写 `ModeLocal`（该路径有 `remoteHubMode()` 前置拦截，只在本地模式可达）。

验证（主 agent 实跑，2026-11-09 18:2x）：
- `cd server && gofmt -l .`（空）、`go vet ./...`、`go test -count=1 ./...`（api/config/db/pairing/storage/store/token/hub 全 ok）、`go build ./...` 通过。
- `cd desktop`（sysroot pkg-config）：`gofmt -l .` 空、`go vet -tags gtk3 .`、`go test -tags gtk3 -count=1 . ./internal/...`、`CGO_ENABLED=0 go test -tags purego ...` 全绿；`node --check assets/app.js` 通过。
- `grep`：`admin_users`/`sessions` 只出现在 `db_test.go` 的反向断言；`restart_required`/`RestartRequired`/`TestLegacy`/`login_limit`/`argon2` 在两端代码中已无引用。
- 文档：`README.md`（升级说明、desktop.json 字段说明、双地址限制）、`docs/ARCHITECTURE.md`（包树、表清单、§4 认证）、`docs/DEPLOY.md`（§3 说明、Tailscale 多监听、§16.6 单地址措辞、配置表键名）、`docs/VERIFICATION.md`（标注历史密码用例已随代码删除）同步；`desktop/assets/index.html` 去掉“单地址是旧方式”的产品文案。

风险记录（本轮未改，单独确认）：`SettingsService.applyNetwork` 保存绑定地址时用新的 `Settings` 字面量覆盖整个文件，会丢掉 `desktop_device_id`/`desktop_device_token`。后果：改一次网络绑定后，下次启动会重新配对，并在设备列表里留下一条旧记录（本机行按名字显示，不影响登录）。这是 DL-01 免码登录引入后与旧保存路径的交互，不是本次清理造成的；修复方式是把新值合并进已加载的 Settings 而不是重建。

未验证：本轮没有需要目视确认的界面变化（删掉的横幅在 `ApplyMode` 热切换后不可达），因此未做隔离容器 GUI 运行；未重新构建/安装桌面二进制；未 commit。
