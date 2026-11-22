DTLS 的 Connection ID（RFC 9146）允许客户端换源地址后继续会话，攻击者伪造或重放带 CID 的记录，想把会话迁到自己的地址。本仓库提供供 Go DTLS 服务端记录层收包调用的 CID 路由包。

## 安全约束

- 只路由内容类型 25（`tls12_cid`）记录；其他记录返回 `ErrNotCID`。
- 记录 CID 必须与 `Session.ConnectionID()` 完全一致，长度或内容不符都会在认证前丢弃。
- `Config.Verify` 必须完成该记录的 AEAD 校验。只有返回 `nil` 后，路由器才允许更新会话对端地址。
- 每个会话、每个 epoch 使用独立的 64 位重放窗口。认证通过但序号已见的记录返回 `ErrReplay`，不更新地址，也不发送探针。
- 会话改址后立即通过 `Config.Probe` 构造并发送一次路径验证探测。探测及探测应答前的所有业务发送均受 3 倍反放大额度约束。
- 调用方只有在认证路径探测应答后才能调用 `Router.ConfirmPath`；确认后解除该地址的额度限制。

## 调用方式

```go
router := cid.NewRouter(cid.Config{
    Lookup: func(connectionID []byte) (cid.Session, bool) {
        return sessions.Lookup(connectionID)
    },
    Verify: func(session cid.Session, record cid.Record) error {
        return session.(*serverSession).OpenRecord(record)
    },
    Send: func(session cid.Session, addr net.Addr, payload []byte) (int, error) {
        return conn.WriteTo(payload, addr)
    },
    Probe: func(session cid.Session, addr net.Addr) ([]byte, error) {
        return session.(*serverSession).BuildPathChallenge(addr)
    },
})

result, err := router.Receive(datagram, sourceAddr)
```

`Verify` 不负责改址；`Send` 只能发送到 `addr`，并返回实际写入字节数。业务发送应走 `router.SendTo`，不要绕过额度控制。

运行校验：

```sh
go test -race ./...
```
