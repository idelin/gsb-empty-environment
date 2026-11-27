高并发网关在启用 TLS 1.3（RFC 8446）0-RTT `early_data` 加速握手时，若未严格按 RFC 8446 Section 4.2.11.2 对截断后的 `ClientHello`（不含 `binders` 列表字节）重算 PSK Binder HMAC、忽略 `obfuscated_ticket_age` 与服务端实测到达时间差（`ticket_age_add` 还原与时钟偏斜窗），或未用单次使用过滤器拦截同一 `NewSessionTicket` 的 0-RTT 重放，攻击者可跨节点重放非幂等支付/状态修改请求或伪造 PSK 绑定。为 Go 传输安全宿主实现可导入的 TLS 1.3 0-RTT PSK Binder 校验与抗重放状态机包，严禁联网（仅用标准库 `crypto/hmac`/`crypto/sha256`）。组件严格实现：（1）完整 HKDF-Expand-Label（`"tls13 "` 前缀、2 字节大端长度、标签与上下文长度前缀编码）密钥派生链：`early_secret` → `binder_key`（严格区分 `ext binder` 与 `res binder`）→ `finished_key`，并在截断 `ClientHello` 原始字节切片上恒定时间验证 `binder`；（2）还原客户端 `ticket_age = (obfuscated_ticket_age - ticket_age_add) mod 2^32`，校验票据未过期且与服务端实测年龄差落入严格窗口 `[-skew, +skew]`，校验 `early_data` 扩展、`cipher_suite` 与 ALPN 与原票据完全一致且单票据累计早数据字节不超过 `max_early_data_size`；（3）实现带时间桶轮换的精确/有界反重放登记器，同一票据在有效期内第二次携带 `early_data` 必须拒收早数据并安全回退至 1-RTT 握手（同时支持按请求幂等策略强制拒绝非幂等路径的 0-RTT）。附 RFC 8446 密钥派生向量、截断字节篡改、时钟超窗与重放降级测试。

---

## `pkg/tls13zerortt`

仅依赖标准库（`crypto/hmac`、`crypto/sha256`、`encoding/binary`、`sync`、`time`），无任何网络访问，SHA-256 套件专用。

**文件结构**

- `hkdf.go`：`HKDF-Extract` / `HKDF-Expand` / `HKDF-Expand-Label`（`"tls13 "` 前缀、uint16 长度、标签与上下文的单字节长度前缀）与 `Derive-Secret`。
- `keyschedule.go`：`early_secret = HKDF-Extract(0, PSK)` → `binder_key`（严格区分 `ext binder` / `res binder`）→ `finished_key`。
- `clienthello.go`：带边界检查的 `ClientHello` 解析器、记录层剥离、按 RFC 8446 §4.2.11.2 的截断（同步重写 uint24 握手长度、uint16 扩展长度与 `pre_shared_key` 扩展长度），以及 `hmac.Equal` 恒定时间 binder 校验。
- `ticket.go`：`ticket_age = (obfuscated_ticket_age - ticket_age_add) mod 2^32` 还原、`[-Skew,+Skew]` 双边界、票据有效期、`cipher_suite`/ALPN/`early_data` 全等校验与单票据 `max_early_data_size` 字节计费。
- `replay.go`：双时间桶轮换的精确登记器 `NewExactReplayFilter` 与有界登记器 `NewBoundedReplayFilter`；容量或旧桶状态不可信时一律 fail-closed。
- `validator.go`：顶层状态机，输出 `DecisionFullHandshake` / `DecisionResume1RTT` / `DecisionAcceptEarlyData`，并支持按路由的幂等策略。

**决策语义**

- binder 伪造/篡改、票据过期 → 完整握手，PSK 被拒。
- 时钟超窗、参数不一致、字节预算超限、非幂等路径、重放或登记器饱和 → 拒绝 `early_data`，安全降级为 1-RTT 恢复握手（binder 已验证的 PSK 仍可恢复）。
- 仅当 binder 正确、票据参数全等、时间在窗内、幂等且票据身份为首次使用时才接受 0-RTT。

**宿主集成最小示例**

```go
replay, _ := tls13zerortt.NewBoundedReplayFilter(time.Minute, 1<<16)
v, _ := tls13zerortt.NewValidator(tls13zerortt.Config{
    Policy:       tls13zerortt.TicketPolicy{Skew: 10 * time.Second},
    Replay:       replay,
    IsIdempotent: func(route string) bool { return route == "GET /balance" },
})
parsed, err := tls13zerortt.ParseClientHello(recordBytes) // 可含单层记录头
// ... 用 parsed.Identity(0) 从票据库取出 *SessionTicket ...
res := v.Admit(tls13zerortt.AdmissionRequest{
    Parsed: parsed, IdentityIndex: 0, Ticket: ticket,
    Now: time.Now(), OfferedALPN: offeredALPN, RouteKey: "POST /pay",
})
```

**测试**

- `go test -race ./...`：RFC 5869 A.1 HKDF 向量、HkdfLabel 线编码、`ext`/`res` 标签隔离、截断法与零化法 transcript 等价、独立 HMAC 预言机、截断字节篡改、畸形输入、时钟双边界超窗、过期、参数不匹配、字节预算、桶轮换、重放降级与有界饱和 fail-closed。
