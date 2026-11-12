# 摘要认证随机数计数绑定

HTTP摘要认证服务接受多个并行请求时，攻击者可能重放同一nonce计数。写供Go网关调用的认证状态包，签名回调确认用户名、realm、方法、目标与正文摘要后，按nonce和客户端随机量原子登记计数；窗口允许乱序但不能重复。nonce绑定服务策略与有效期，用可控验签器测试无效签名不能烧毁计数、计数回绕拒绝及并行重复。

## `digestauth` 包

位于 `digestauth/`，供网关以 `digestauth.NewState` / `Issue` /
`Authenticate` 三个入口使用。

- **nonce 封签**：HMAC-SHA256 签名的固定结构 nonce，内含过期时间与服务
  `Policy`（realm、qop、algorithm、窗口、计数上限）的密钥标签；篡改即
  `ErrInvalidNonce`，策略不符即 `ErrPolicyMismatch`，过期即
  `ErrExpiredNonce`，重启或未签发的 nonce 为 `ErrUnknownNonce`。
- **原子计数**：计数按 `(nonce, cnonce)` 对登记，互斥锁保护。每对维护一个
  滑动位图窗口，乱序 nc 可接受，重复 nc 返回 `ErrReplay`，滑出窗口的旧值
  返回 `ErrStaleNonce`；nc 达到 `Policy.MaxCount`（默认 uint32 上限）后一律
  `ErrCounterWrapped`，强制换新 nonce。
- **先验签后登记**：`Verifier` 回调（确认用户名、realm、方法、URI 目标与
  auth-int 正文摘要）在状态锁之外执行，验签失败返回 `ErrBadSignature`，
  不会创建会话、也不会烧毁任何 nc。
- **清理**：支持后台定时扫描与访问时懒删除过期 nonce（`WithSweepInterval`、
  `Sweep`、`Stop`），时钟可通过 `WithClock` 注入以便测试。

内置 `RFC2617Verifier`（基于 HA1 查询，MD5/SHA-256、auth/auth-int）与
`ParseNC`（8 位十六进制 nc 解析）。

### 测试要点

`go test -race ./...` 覆盖：可控验签器拒绝时 nc=1 仍可使用（不烧毁计数）、
32 路并行同一 nc 恰有 1 成功其余 `ErrReplay`、24 路乱序 nc 全部成功、
`MaxCount` 回绕拒绝、cnonce 隔离、nonce 过期/扫描/停止、策略不匹配与
伪造 nonce，以及 RFC 2617 验签器端到端流程。
