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
