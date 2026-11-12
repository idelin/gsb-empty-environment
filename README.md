# 预密钥包发放与消费

Signal式预密钥服务若重复发放一次性预密钥或允许替换身份密钥，攻击者能冒充接收方或使前向保密失效。为Go消息服务实现预密钥包获取与消费包，供会话发起请求调用：已签名预密钥由Ed25519签名覆盖带类型字节的编码公钥，一次性预密钥在并发请求下恰好分配一次，耗尽时返回不含一次性预密钥的包，身份密钥与注册时不一致拒绝。用并发拉取、耗尽和换身份密钥验证。

## API

- `NewStore()` 创建存储；`Register` 固定用户身份密钥并写入签名预密钥及初始一次性预密钥。
- `SignSignedPreKey` / `VerifySignedPreKey` 对 `0x05 || publicKey` 编码结果进行 Ed25519 签名和验证。
- `FetchPreKeyBundle(userID)` 在锁内消费至多一个一次性预密钥；池耗尽时 `OneTimePreKey == nil`。
- `FetchPreKeyBundle(userID, identityKey)` 额外校验请求方声明的身份密钥，不匹配时不消费预密钥。
- `AddOneTimePreKeys` 向已注册用户补充密钥；重复 ID 或重复公钥会被拒绝。

## 验证

```sh
go test -race ./...
```
