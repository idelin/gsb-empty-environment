# MLS Welcome 准入组件

群聊服务处理 MLS Welcome 时，如果攻击者可以替换 GroupInfo 或 ratchet
tree，新成员可能在错误的群组状态下接受同一份加密组秘密。本包实现一个供
MLS 客户端调用的 Welcome 准入器；解密、签名、摘要、credential 校验等密码
操作全部通过 `Crypto` 接口注入，持久状态通过 `Store` 接口注入。

## 使用方式

```go
admitter := netsec.NewAdmitter(crypto,
    netsec.WithStore(store),
    netsec.WithTreeFetcher(fetcher),
)

admission, err := admitter.Admit(ctx, welcome, netsec.LocalParticipant{
    KeyPackage: keyPackage,
    PrivateKey: privateKey,
    PSKs:       map[string][]byte{netsec.PSKKey(pskID): psk},
})
if err != nil {
    // 未提交任何持久状态，可安全拒绝/重试
}

// admission.JoinerSecret / TreeHash / TranscriptHash / PSKIDs 已绑定到
// 本次 Welcome 的唯一候选和树状态。
```

注入的 `Crypto` 必须：

- 认证并解密每个 `EncryptedGroupSecrets`，并在 `GroupSecrets` 中带回
  `GroupID`、`Epoch`、`CipherSuite`、`KeyPackageRef`、`JoinerSecret` 和
  `PSKIDs`。
- 验证 `GroupInfo` 签名覆盖 `TreeHash`、`TranscriptHash`、extensions 和
  signer credential。
- 根据本群上下文、树摘要、transcript、PSK 验证 joiner secret 的密钥调度。
- 验证 KeyPackage 叶 credential 与本地私钥匹配。

## 校验与状态保证

- 逐项解密所有可解密的 `EncryptedGroupSecrets`；每个明文必须与 GroupInfo
  的 group id、epoch、cipher suite 一致，所有明文必须共享同一 joiner
  secret 和 PSK 集合。本地 KeyPackage reference 必须恰好对应一个候选。
- GroupInfo signer 必须是非空树成员，具有 credential 和签名密钥；可通过
  `WithSignerPolicy` 追加应用层授权策略。
- 树摘要覆盖实际树字节，树哈希必须等于 GroupInfo 中的 `TreeHash`；树中的
  group-context extensions 必须等于 GroupInfo extensions；transcript hash
  由注入的 crypto 按当前/前序状态重新计算后比对。
- 外取树请求携带 Welcome ID、预期树摘要和绑定 nonce。回调返回的 bytes、
  digest、binding 必须同时匹配；bytes 会立即复制并在副本上哈希、解码，
  不读取共享缓存。
- KeyPackage 必须处于有效期内、未被任何群消费，且树中对应叶子的
  credential/密钥与 KeyPackage 和本地私钥一致。
- 相同 Welcome 按 Welcome ID 串行化；`Store.Commit` 必须原子地同时检查
  Welcome 未消费、KeyPackage 未消费、当前 epoch 未变化，然后一次性写入新
  state 和两个消费标记。任何校验或提交失败都不会推进持久 epoch。

`MemoryStore` 提供默认的并发安全实现；生产环境可用数据库事务实现
`Store`，但必须保持 `Commit` 的原子前置条件语义。
