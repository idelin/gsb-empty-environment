# 加密记录乱序与重放

连接层认证记录接收包，使用标准库 AES-GCM。记录字段为连接 ID、epoch、
`uint64` 序号和密文；AAD 与 nonce 的布局固定如下：

```text
AAD    = uint32be(len(connectionID)) || connectionID || uint32be(epoch) || uint64be(sequence)
nonce  = uint32be(epoch) || uint64be(sequence)       // 12 bytes
```

密文包含 GCM 认证标签。包同时提供 `SealRecord`/`OpenRecord`，生产接收路径应使用
`Receiver.Receive`。

## 使用

```go
receiver, err := netsec.NewReceiver(7, key)
if err != nil {
    return err
}

record := netsec.Record{
    ConnectionID: connectionID,
    Epoch:        7,
    Sequence:     sequence,
    Ciphertext:   ciphertext,
}
err = receiver.Receive(record, func(plaintext []byte) error {
    return process(plaintext)
})
```

`Rotate(newEpoch, newKey)` 安装严格递增的新 epoch：新 epoch 成为 current，
原 current 成为 previous，更早的 epoch 被丢弃。`Retire(previousEpoch)` 显式使
previous 失效。同 epoch 不能替换密钥，epoch 不能回退；序号窗口不回绕。

每个 connection ID 的每个 epoch 维护独立的 1024 位滑动窗口。认证成功后才原子地
检查并占用序号，因此乱序记录可交付，重复、超过窗口下限和认证失败记录不会交付，
伪造的高序号认证失败也不会推进窗口。回调返回错误时记录仍视为已消费。

## 测试

```sh
go test ./...
go test -race ./...
```
