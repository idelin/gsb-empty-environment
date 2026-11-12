# TLS 1.3 KeyUpdate 状态机

`example.org/netsec/q0581` 提供一个可导入的 Go TLS 1.3 记录层 KeyUpdate
状态机，用于抵御恶意对端连发 `update_requested` 造成的密钥轮换 CPU 消耗。

## 安全性质

- 收到 `update_requested` 时只回一个 `KeyUpdate(update_not_requested)`；回包使用旧写密钥加密，随后写密钥前进一代，写序号清零。
- 收到任意认证通过的 KeyUpdate 后，读密钥替换为
  `HKDF-Expand-Label(old, "traffic upd", "", Hash.Length)`，读序号清零。
- KeyUpdate 必须完整地位于单独一条记录中；分片、与其它握手消息合并、或与第二个 KeyUpdate 合并都会被拒绝。
- 滑动窗口内认证通过的入站 KeyUpdate 数超过预算时，状态机永久关闭并返回 `ErrUpdateBudgetExceeded`；`OnClose` 可用于关闭底层连接。
- AEAD 认证失败的记录不消耗序号、不推进读代、不消耗更新预算。
- 记录序号参与 TLS 1.3 AEAD nonce 派生，因此乱序、延迟或旧密钥时代的记录会被认证拒绝。

## 用法

```go
server, err := netsec.NewKeyUpdateState(netsec.Config{
    ReadSecret:   clientToServerSecret, // Hash.Length bytes
    WriteSecret:  serverToClientSecret,
    MaxUpdates:   8,
    UpdateWindow: time.Minute,
    OnClose: func(err error) {
        conn.Close()
    },
})
if err != nil {
    return err
}

for {
    record, err := readOneTLSRecord(conn)
    if err != nil {
        return err
    }
    result, err := server.ProcessRecord(record)
    if err != nil {
        // ErrAuthentication/ErrUpdateBudgetExceeded are fatal in real TLS.
        return err
    }
    if result.Response != nil {
        if _, err := conn.Write(result.Response); err != nil {
            return err
        }
    }
    consume(result.ContentType, result.Plaintext)
}
```

`Suite` 默认为 `TLS_AES_128_GCM_SHA256`；`TLS13AES256GCM` 对应
`TLS_AES_256_GCM_SHA384`。读取/写入 secret 长度必须分别等于哈希输出长度。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖 HKDF 向量、密钥轮换与序号清零、洪泛预算、滑动窗口恢复、AEAD
失败不推进读代、未认证洪泛、分片/合并拒绝、记录乱序以及旧密钥记录重放。
