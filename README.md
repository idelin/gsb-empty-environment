# 加密记录乱序与重放

`netsec` 是面向 Go 通信服务的连接层认证记录接收包。它只使用标准库
`crypto/aes` 与 `crypto/cipher`，密钥由调用方提供。

## 协议

每条 `Record` 携带连接 ID、epoch、`uint64` 序号与包含 GCM tag 的密文。

- nonce：epoch 大端 `uint32` + 序号大端 `uint64`，共 12 字节。
- AAD：连接 ID 字节长度的大端 `uint32`、连接 ID、epoch 的大端 `uint32`、
  序号的大端 `uint64`。
- 每个 epoch 独立维护 1024 位重放位图，允许乱序，拒绝重复与窗口外旧序号。

## 使用

```go
receiver, err := netsec.NewReceiver(connID)
if err != nil {
	return err
}
if err := receiver.Install(1, key); err != nil {
	return err
}

ciphertext, err := netsec.Seal(connID, 1, sequence, key, plaintext)
if err != nil {
	return err
}
record := netsec.Record{
	ConnectionID: connID,
	Epoch:        1,
	Sequence:     sequence,
	Ciphertext:   ciphertext,
}

err = receiver.Receive(record, func(plaintext []byte) error {
	return handle(plaintext)
})
switch {
case errors.Is(err, netsec.ErrAuthentication):
	// 认证失败，重放窗口未被修改。
case errors.Is(err, netsec.ErrReplay):
	// 重复或过旧序号，未交付。
case err != nil:
	// 可能是回调返回的错误；该序号仍已被消费。
}
```

密钥轮换与退休：

```go
if err := receiver.Install(2, nextKey); err != nil {
	return err // epoch 2 为当前，epoch 1 自动成为前一 epoch
}
if err := receiver.Retire(1); err != nil {
	return err // 此后 epoch 1 的记录一律拒绝
}
```

`Install` 要求新 epoch 严格递增。同一 epoch 的密钥不可替换；更旧的 epoch
被轮换淘汰后也不能重新安装。当前 epoch 不可退休。最多只有当前与前一 epoch
可用。

## 并发语义

- 认证先于重放检查；伪造的高序号认证失败不会推进位图。
- 同一 epoch 的接收串行化 AES-GCM 解密与位图更新，不同 epoch 可并行。
- 并发收到同一记录时，恰好一次交付；其余调用返回 `ErrDuplicate`。
- 序号在调用回调前已原子消费。回调返回错误仍视为已消费，重试不会再次交付。
- 回调在接收锁之外运行，可以安全调用 `Install`、`Retire` 或其他 `Receive`。

## 测试

```sh
go test -race ./...
```

测试包含独立实现的真实 AES-GCM 线上格式校验、乱序/边界重放、伪造前移、
回调失败消费、并发同包、密钥轮换和退休竞态。
