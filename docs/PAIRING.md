# 设备配对与多地址连接（2026-11-08）

用户确认取消账号密码输入，改为四位临时配对码、记住设备；配对保存 LAN/Tailscale 地址，后续优先 LAN、失败再用 Tailscale。首次异地手填地址。本轮不做扫码、不接 Tailscale 管理 API、不扫描整个网段、不新增云服务。

## 行为

- 本机设置点「添加设备」生成随机四位十进制码（含前导零），120秒有效；全 Hub 累计错5次失效，成功一次即失效；重生成废弃旧码。配对码仅本机显示，不经网络查询，不写日志。
- 设置可查看设备名称、创建/最近使用时间、撤销；管理只走本机 Go 桥，不提供远程管理/生成码端点。
- 设备令牌为32字节密码学随机数，DB仅存哈希。撤销/退出使HTTP与现存WS失效。Android现有Keystore安全存储令牌，Web使用HttpOnly Cookie，不把凭据写localStorage。
- Android保存一台已配对Hub的稳定ID、令牌、候选地址。启动/前台恢复/连接失败先探测LAN再TS，选定后恢复Cookie/CSRF和事件流。探测无凭据；Hub ID不符不发凭据。ID只防误连，不能认证明文HTTP对端。
- 切地址只重试连接，不重放写入/上传/删除；中断传输提示用户手动重试。探测短超时，禁止跨地址重定向携带凭据。
- 仅报告实际绑定成功URL，kind为lan/tailscale/other。同一Hub只显示一次。MagicDNS只在用户明确配置且Host允许时使用；不承诺自动枚举TS设备。
- 可信LAN明文HTTP性质不变，TS要求两端在线且策略允许。不做旧密码兼容分支，不删除用户inbox/blob，不操作用户实例。

## 冻结HTTP接口

时间Unix毫秒，错误沿用{error:{code,message}}。保留items/upload/events、Origin/Host/CSRF约束。

- GET /api/v1/health：原字段加hub_id（稳定，不是秘密），用于无凭据探测。
- POST /api/v1/auth/pair：body {code:string,device_name:string,native?:boolean}；200返回SessionInfo加hub、device_id，native=true额外device_token；同时设置flowdrop_session/flowdrop_csrf Cookie。错码/过期/已消费/无有效码401 pairing_invalid；本次错码耗尽次数或已锁429 pairing_locked。
- GET /api/v1/auth/session：未认证仍200 {authenticated:false,...}；认证成功{authenticated:true,csrf_token,expires_at,limits,device_id,hub}。native可用Authorization: Bearer <device_token>恢复目标origin的Cookie/CSRF；不回传原令牌。
- POST /api/v1/auth/logout：撤销当前设备并清Cookie；客户端清本机记录。
- hub = {id:string,name:string,endpoints:[{url:string,kind:"lan"|"tailscale"|"other"}]}。
- Web沿用Cookie不申请native令牌。Android请求/WS/预览/下载使用当前活动地址，禁止向未确认origin共享Cookie，令牌禁止放URL/query。

## PA-01配对核心冻结Go接口

独立包server/internal/pairing，NewStore(*sql.DB) *Store。时间由调用者传入：

```go
type Code struct { Code string `json:"code"`; ExpiresAt int64 `json:"expires_at"` }
type Device struct { ID string `json:"id"`; Name string `json:"name"`; CreatedAt int64 `json:"created_at"`; LastSeenAt int64 `json:"last_seen_at"` }
func (s *Store) HubID() (string,error)
func (s *Store) CreateCode(now time.Time) (Code,error)
func (s *Store) Pair(code,name string,now time.Time) (string,Device,error)
func (s *Store) LookupToken(token string,now time.Time) (Device,error)
func (s *Store) ListDevices() ([]Device,error)
func (s *Store) RevokeDevice(id string) error
```

错误导出ErrInvalidCode、ErrLocked、ErrUnauthorized。重新在本机生成码会重置累计错误并解除锁定。令牌在撤销/退出前有效；浏览器Cookie按现有会话TTL，到期可重新配对；Android用安全保存令牌恢复。SQLite事务保证并发仅一次成功和全局尝试上限；稳定Hub ID持久化。只追加配对相关表，不为历史密码增加兼容逻辑，不删除旧数据。

## 分阶段

PA-01核心、PA-02 Android纯地址选择器、PA-03 Web页并行。PA-04接Hub HTTP/WS与本机API依赖PA-01。PA-05 Android网络/存储与UI按文件再拆，依赖PA-02及冻结接口。PA-06桌面设置依赖PA-04。之后密码入口退役、跨地址定向验证、构建分别派发。每个新任务全新pi，登记TASKS后派发；不并发改同文件。GUI只在独立容器/专用VM，禁止宿主临时DBus/portal/a11y/Xvfb及改KDE/RustDesk。不得自动安装commit/push。
