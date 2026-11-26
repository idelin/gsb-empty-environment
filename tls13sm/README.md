# tls13sm — TLS 1.3 握手与密钥调度安全状态机

`tls13sm` 是一个自包含、可导入的 Go 包，为高频网关宿主实现 RFC 8446
（TLS 1.3）的握手状态机与 §7.1 密钥调度。它不执行任何网络 I/O，也不导入或
调用标准库 `crypto/tls`，仅建立在 `crypto/sha256`、`crypto/sha512`、
`crypto/hmac`、`crypto/aes`、`crypto/cipher`、`crypto/ecdh`、`crypto/rand`
等基础原语之上。所有报文交互都以显式 `Record` 输入/输出表达，由调用方
掌控传输层。

## 安全控制

- **完整密钥派生链**：`Early Secret -> Handshake Secret -> Master Secret`，
  含精确的 `HKDF-Expand-Label`（`HkdfLabel`）编码、`Derive-Secret`、
  流量 `key`/`iv`、`finished` key、`traffic_upd` 与票据 `resumption` 派生。
- **HelloRetryRequest**：HRR 后强制以合成 `message_hash(254)`（其体为
  `Hash(ClientHello1)`）重置转录，再追加 HRR 与后续消息；拒绝二次 HRR、
  拒绝重试分组已在首条 ClientHello 中提供、拒绝参数不匹配的重试。
- **降级保护**：对每个 ServerHello/HRR 强制检查
  `ServerHello.random[24:32]`，命中 TLS 1.2
  （`44 4F 57 4E 47 52 44 01`）或 TLS 1.1（`...00`）哨兵即返回
  `ErrDowngradeDetected`。
- **0-RTT Early Data 三道防线**：
  1. 票据年龄偏斜窗口核验（`ticketAgeOK`，可配置 ±tolerance）；
  2. 单次使用 Strike-Register 防重放账本（`StrikeRegister`，票据 nonce
     首次通过、窗口内再次出现即 `ErrReplayDetected`）；
  3. 调用方显式幂等声明门禁（`Idempotent` / `AcceptEarlyData`）。
- **严格消息顺序**：拒绝 Finished 前的应用数据、明文 `Alert`/`KeyUpdate`
  注入、跨 epoch 密文、缺失 `EndOfEarlyData` 的 Finished。
- **有界 KeyUpdate**：每方向独立计数，超出 `MaxKeyUpdates` 返回
  `ErrKeyUpdateLimit`，并遵循 RFC 的“先用当前密钥密封 KeyUpdate，再轮换”
  顺序。

## 支持的密码套件与分组

- 套件：`TLS_AES_128_GCM_SHA256`、`TLS_AES_256_GCM_SHA384`。
- 命名分组：X25519、P-256、P-384（经 `crypto/ecdh`）。

## 最小用法

```go
client, _ := tls13sm.NewClientStateMachine(tls13sm.ClientConfig{
    Suite:  tls13sm.SuiteAES128GCM,
    Groups: []uint16{tls13sm.GroupP256},
})
start, _ := client.Start()
// 将 start.Records[0] 写入传输；用 client.HandleRecord 驱动对端响应，
// 连接进入 connected 后调用 client.SendApplicationData / SendKeyUpdate。

server, _ := tls13sm.NewServerStateMachine(tls13sm.ServerConfig{
    Suite:  tls13sm.SuiteAES128GCM,
    Groups: []uint16{tls13sm.GroupP256},
})
// server.HandleRecord 消费对端记录；0-RTT 时配置 Tickets、Strike 与
// AllowEarlyData，并在处理早数据前调用 server.AcceptEarlyData(true)。
```

## 测试

`go test ./...` 运行 30 余个确定性测试，覆盖：

- RFC 5869 HKDF 与 RFC 8446 Appendix A.1 Early Secret/`derived` 向量；
- 完整握手与 HRR 握手的端到端互通（AEAD 双向解密、Finished 校验、
  转录哈希一致、KeyUpdate 后续仍可互通）；
- PSK+DHE 0-RTT 端到端（三道防线、EOED 顺序、年龄偏斜拒绝）；
- 中间人降级（TLS 1.2/TLS 1.1 哨兵、HRR 哨兵）；
- 0-RTT 重放（Strike-Register 单次使用）；
- HRR 转录折叠篡改、二次 HRR、参数不匹配；
- 乱序注入（明文 Alert/KeyUpdate、Finished 前应用数据、跨 epoch、
  越界 KeyUpdate、畸形 KeyUpdate）。
