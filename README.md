# 主题加密与数据密钥轮换

本包提供一个可导入的主题加密管理器，用于生产者与消费者之间的信封加密。
调用方先登记受信任的接收者公钥集合，生产者为每个数据密钥代次生成随机的
AES-256 数据密钥，并为集合中的每个接收者分别封装该数据密钥。

## 安全属性

- 数据密钥在加密 1000 条消息后，或使用满 4 小时后轮换，两个条件先到者生效。
- AES-GCM nonce 为 4 字节随机前缀加 8 字节大端递增计数器，同一数据密钥下
  由计数器保证唯一；计数器用尽 `math.MaxUint64` 后，下一条消息强制换新数据密钥。
- 每个接收者使用独立的 RSA-OAEP-SHA256 封装，OAEP label 绑定主题、数据密钥
  代次和接收者密钥名，封装结果不能跨接收者、主题或数据密钥移植。
- 主题、数据密钥 ID、nonce 和完整接收者密钥列表都作为 AES-GCM additional
  data 认证。修改 nonce、密钥列表或密文都会导致解密失败。
- 消费者按完整登记集合检查元数据：出现任何未登记的名称即返回
  `ErrUnknownKeyName`；重复名称返回 `ErrDuplicateKeyName`。

## 基本用法

```go
aliceKey, _ := rsa.GenerateKey(rand.Reader, 2048)

registry, _ := netsec.NewRegistry([]netsec.Recipient{
	{KeyName: "alice", PublicKey: &aliceKey.PublicKey},
})

producer, _ := netsec.NewEncryptor(netsec.EncryptorConfig{
	Topic:    "persistent://tenant/namespace/topic",
	Registry: registry,
})
message, _ := producer.Encrypt([]byte("payload"))

// 消费者只需要持有自己的私钥，但必须登记完整允许集合。
consumer, _ := netsec.NewDecryptor(netsec.DecryptorConfig{
	Topic:    "persistent://tenant/namespace/topic",
	Registry: registry,
	PrivateKeys: []netsec.RecipientPrivateKey{
		{KeyName: "alice", PrivateKey: aliceKey},
	},
})
plaintext, err := consumer.Decrypt(message)
```

`Message` 可直接使用 `encoding/json` 传输。生产者的 `Encrypt` 可并发调用。

## 注入点

`EncryptorConfig` 和 `DecryptorConfig` 支持注入：

- `Cipher`：载荷加解密，默认 `NewAESGCM()`。
- `KeyWrapper`：数据密钥封装，默认 `NewRSAOAEPWrapper()`。
- 生产者还可注入 `Now` 与 `Random`，用于确定性的轮换和 nonce 测试。
- `NonceCounterStart` 可把每个数据密钥的计数器起点调高，用于测试计数器耗尽；
  正常生产环境保持零值。

## 测试

```sh
go test ./...
go test -race ./...
```

测试覆盖 1000 条消息边界、4 小时边界、同一数据密钥下的 nonce 唯一性、
计数器耗尽强制轮换、每个登记接收者的封装、nonce 重放、额外密钥名和
重复密钥名拒绝，以及 RSA-OAEP 端到端与 JSON 传输。
