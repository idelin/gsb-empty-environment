# DTLS连接标识迁移劫持

DTLS 的 Connection ID（RFC 9146）允许客户端换源地址后继续会话，攻击者伪造或重放带 CID 的记录，想把会话迁到自己的地址。为Go DTLS 服务端写供记录层收包调用的 CID 路由包：只处理类型 25（tls12_cid）记录，按 CID 查会话；记录通过 AEAD 校验后才可更新会话的对端地址，校验前不得改动；每个会话改址时须发出一次路径验证探测，探测应答前对新地址发送的字节总量不得超过从新地址收到字节的 3 倍（本题规定）；重放窗口（宽度 64）内已见序号的记录即使校验通过也不得更新地址；CID 长度与协商值不同的记录直接丢弃。用校验前改址、重放记录迁址、反放大上限和 CID 长度错误验证。

## 使用

```go
r := netsec.NewRouter()
s := netsec.NewSession(cid, peer)
s.AEAD = aead
s.NoncePrefix = noncePrefix
s.SendProbe = func(addr net.Addr, probe []byte) error { /* send probe */ return nil }
s.Write = func(addr net.Addr, data []byte) error { /* send record */ return nil }
_ = r.Register(s)

res, err := r.HandlePacket(datagram, sourceAddr)
```

路由层的行为：

- 仅接受外层类型 `25`（`tls12_cid`）；其他类型返回 `ErrNotCID`。
- 用记录中的 CID 精确匹配会话；CID 长度与协商值不一致时直接返回 `ErrCIDLength`。
- 先调用 `Verify`、`Authenticator`、`Decrypt` 或 `AEAD.Open`，认证成功后才检查重放窗口、更新 `Peer` 并发送探测。
- 每个会话有一个宽度为 64 的记录号滑动窗口；窗口内已见记录不更新地址。只有比历史最高记录号更新的记录才能迁移地址。
- 地址变化时发送一次随机探测；探测应答（`ConfirmPath`）前，`Router.Send`/`Write` 对新地址的累计发送字节不超过从该地址收到的已认证字节的 3 倍。
