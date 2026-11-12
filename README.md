# ZMTP机制降级与CURVE计数

攻击者作为客户端声称 NULL 机制接入要求 CURVE 的服务端，或重放旧的 CURVE 消息。本包提供可导入的 ZMTP 3.x 握手与流量校验组件：

- greeting 必须为 64 字节，签名首字节为 `0xFF`、第 10 字节为 `0x7F`，主版本不低于 3。
- 机制名为 20 字节 NUL 填充字段；首个 NUL 后出现非零字节立即拒绝。
- 对端机制必须与本端 `Config.Mechanism` 完全一致；NULL 降级到 CURVE 等配置会返回 `ErrMechanismDowngrade`。
- 对端 `as-server` 位必须与本端角色互补，否则返回 `ErrRoleMismatch`。
- CURVE 会话中 `MESSAGE` 的 8 字节大端 nonce 必须严格递增；重复或回退会关闭连接并返回 `ErrNonceReplay`。

## 用法

```go
cfg := netsec.Config{
    Role:      netsec.RoleServer,
    Mechanism: netsec.MechanismCurve,
}

session, err := netsec.ServerHandshake(conn, cfg)
if err != nil {
    // Handshake 已关闭 conn（conn 实现 io.Closer 时）。
    return err
}

for {
    frame, err := session.ReadFrame() // CURVE nonce 校验在此自动执行。
    if err != nil {
        return err
    }
    _ = frame
}
```

也可直接使用底层构件：`ParseGreeting`、`Config.ValidatePeer`、
`ParseCurveMessage`、`NonceTracker` 与 `ReadFrame`。

本包是协商与防重放层，不替代消息库自身的 Curve25519/XSalsa20-Poly1305
解密实现；`CurveMessage.Box` 应交给底层库解密。

## 测试

```sh
go test ./... -race
```
