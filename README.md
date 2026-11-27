权威与递归 DNS 服务在抵御伪造源 IP 的 UDP 反射放大与离径缓存投毒攻击时，若实现 EDNS0 DNS Cookies（RFC 7873 / RFC 9018）存在服务端密钥轮换不同步、未校验时间戳防重放窗口、对无合法 Server Cookie 的 UDP 大应答未强制截断（`TC=1`）回退 TCP，或未将 Client Cookie 与源 IP 绑定校验，攻击者仍可批量伪造源地址放大流量或暴力猜解 TXID 注入虚假记录。为 Go DNS 安全宿主实现可导入的 RFC 7873/9018 互操作 DNS Cookie 生成、校验与防投毒决策包，严禁联网（内置 SipHash-2-4 实现）。组件完整实现：（1）按 RFC 9018 规范生成与验证 16 字节服务端 Cookie（1 字节版本 `0x01` + 3 字节保留零 + 4 字节大端 Unix 时间戳 + 8 字节 `SipHash-2-4(server_secret, client_cookie || version || reserved || timestamp || client_ip)`），支持当前密钥与上一轮换密钥双窗无缝验证，严格拒绝非法保留位、超过前向 5 分钟或后向 1 小时窗口的时间戳以及跨客户端 IP 盗用；（2）实现客户端与服务端双侧策略状态机：服务端对缺失或无效 Server Cookie 的 UDP 查询返回 `BADCOOKIE`（RCODE 23）或限制响应字节并置 `TC=1` 强制 TCP，拒绝超大放大响应；客户端仅接受同时匹配 `(TXID, src_port, QNAME, QTYPE, Client Cookie)` 且服务端 Cookie 格式合法的 UDP 应答，并按源 IP 缓存服务端 Cookie 拦截伪造离径包。附 RFC 9018 测试向量、密钥轮换窗口、跨 IP 盗用、时间偏移超窗与离径投毒包拒收测试。

## 实现：`dnscookie` 包

可导入包 `formal.local/w28-c6167573ce/dnscookie`，零外部依赖、不联网，内置 SipHash-2-4 与最小 DNS 线格式解析器。

- `dnscookie/cookie.go` — RFC 9018 16 字节 Server Cookie 生成/校验；`KeyRing` 支持当前+上一密钥双窗轮换；强制版本 `0x01`、保留位归零、前向 5 分钟 / 后向 1 小时时间戳防重放窗口、Client Cookie 与源 IP 绑定。
- `dnscookie/dns.go` — 最小 DNS 报文解析（头部、Question、EDNS OPT、COOKIE option），支持扩展 RCODE 23（`BADCOOKIE`）及查询/响应构造。
- `dnscookie/server.go` — 服务端策略状态机 `ServerPolicy.HandleUDP`：缺失/无效 Server Cookie → `BADCOOKIE` 并附新 Cookie；无 Cookie 或超 UDP 预算的大应答 → `TC=1` 截断强制 TCP；畸形报文 → 丢弃/FORMERR。
- `dnscookie/client.go` — 客户端防投毒状态机 `Client`：仅接受 `(TXID, 源地址/端口, QNAME, QTYPE, Client Cookie)` 全匹配且 Server Cookie 格式合法的应答；按服务器 IP 缓存 Server Cookie；`BADCOOKIE` 后 `RetryQuery` 自动携带新 Cookie。

### 快速示例

```go
keys := dnscookie.NewKeyRing(secret)        // [16]byte 服务端密钥
pol := dnscookie.NewServerPolicy(keys)      // 默认 UDP 预算 1232 字节
dec := pol.HandleUDP(query, clientIP, answerLen)
// dec.Action: ActionAnswer / ActionBadCookie / ActionTruncated / ActionDrop

cli, _ := dnscookie.NewClient()
wire, id, _ := cli.Query(serverAddrPort, "www.example.com.", 1)
res, err := cli.ProcessResponse(resp, from) // 离径伪造包在此被拒绝
```

### 测试

`go test ./dnscookie/` 覆盖：SipHash-2-4 论文参考向量、RFC 9018 构造黄金向量、密钥轮换双窗、跨 IP 盗用、时间戳 ±窗口边界、保留位/版本非法、服务端 `BADCOOKIE` 与 `TC=1` 放大防护、客户端离径投毒包（猜 TXID、错源端口/IP、错 QNAME/QTYPE、伪造 Client Cookie、畸形 Server Cookie）拒收及 `BADCOOKIE` 重试流程。
