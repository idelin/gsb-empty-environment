# 双棘轮接收链

Go 端到端消息库的接收链实现，可直接由解密入口调用。核心不变量：

- 单条链一次最多派生/跳过 1000 个消息密钥，超限在派生开始前返回
  `ErrSkipLimitExceeded`；巨大序号不会触发循环或内存分配。
- 同一棘轮公钥下最多保留 1000 个待消费的跳过密钥。跳过密钥以
  `(棘轮公钥, 序号)` 为命名空间，跨链共享时也不会串用。
- 跳过密钥只在 AEAD 认证成功后删除，因此一次性使用且验证失败不消耗。
- 认证失败时链密钥、下一序号和跳过密钥缓存均不改变。
- 低于链头的重复报文只有在缓存中仍有对应密钥时才会被接受；已消费的
  报文返回 `ErrDuplicateMessage`。

## 用法

```go
chain, err := netsec.NewReceiveChain(ratchetPublicKey, chainKey)
if err != nil {
    return err
}

plaintext, err := chain.Decrypt(sequence, nonce, ciphertext, additionalData)
```

`sequence` 可使用常见整数类型（`int`、`uint32`、`uint64` 等），入口会统一
校验并拒绝负数；超过 `MaxUint64` 的常量请显式写成 `uint64(math.MaxUint64)`。

默认 AEAD 为 AES-256-GCM，链 KDF 为 HMAC-SHA256（`0x01` 派生消息密钥，
`0x02` 派生下一链密钥）。若解密入口已有自己的 AEAD，请使用事务式回调：

```go
err := chain.UseMessageKey(sequence, func(messageKey []byte) error {
    // 在这里完成认证与解密。返回非 nil 时链状态完全不变。
    return nil
})
```

`DecryptWith` / `Open` 提供同样的回调式接口；`SenderChain` 仅用于测试或
构造兼容的发送报文。包根和 `receivechain` 子包都导出相同 API。

## 验证

```sh
go test ./...
go test -race ./...
```

双棘轮接收方若按报文序号推进链，攻击者发送巨大序号即可耗尽计算，伪造报文还会破坏链状态。为Go端到端消息库实现接收链包，供解密入口调用：单条链最多跳过1000个消息密钥，跳过的密钥按棘轮公钥和序号缓存且只用一次，AEAD认证失败不得推进任何状态，超过上限拒绝。用巨大序号、失败认证后正常消息、乱序和重复消息验证。
