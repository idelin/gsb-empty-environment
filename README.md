# 前缀哈希 MAC 的长度扩展伪造与 HMAC 迁移

`example.org/netsec/q0108` 是一个可导入的 Go 安全实验包，包含：

- 标准 SHA-256 压缩状态导入和续压：`ImportSHA256State`、`ContinueSHA256`、`ExtendSHA256`
- 单长度伪造：`ForgeSHA256PrefixMAC`、`ForgeSHA256LengthExtension`
- 1 至 64 字节 secret 长度搜索：`AttackSHA256PrefixMAC`、`AttackSHA256PrefixMACInRange`
- HMAC-SHA256 迁移入口：`VerifyHMACRequest`、`NewHMACVerifier`
- 无分隔符歧义、以大端长度前缀绑定 method/path/body 的 `CanonicalRequest`

## 攻击模型

旧验证器计算 `SHA256(secret || message)`。攻击者已知一条合法 `message` 和
32 字节 `tag` 后，对每个候选 secret 长度构造：

```text
forgedMessage = message || SHA-256-padding(secretLen + len(message)) || suffix
forgedTag     = continue-SHA-256(tagState, suffix, totalForgedLength)
```

填充以 `0x80` 开始，末尾 8 字节为大端总位长，按模 `2^64` 编码。续压从
`tag` 的八个大端状态字开始，不使用私有运行时符号。攻击只通过
`func(message, tag []byte) bool` 回调询问服务器；secret 始终留在服务器内部。
首个接受长度会返回查询次数和完整 `LengthExtensionWitness`；全部失败返回
`ErrRangeMiss`，并保留查询次数与搜索区间。

```go
result, err := netsec.AttackSHA256PrefixMAC(message, tag, suffix, server.Verify)
if errors.Is(err, netsec.ErrRangeMiss) {
    // 区间内没有候选长度被接受
}
if result.Found() {
    // result.Queries, result.Witness.ForgedMessage, result.Witness.ForgedTag
}
```

## 迁移入口

迁移后的服务端应使用 HMAC-SHA256，而不是继续拼接 secret：

```go
verifier := netsec.NewHMACVerifier(secret)
tag := verifier.Sign(method, path, body)
ok := verifier.Verify(method, path, body, tag)
```

规范请求按
`uint64be(len(method)) || method || uint64be(len(path)) || path || uint64be(len(body)) || body`
编码，标签比较使用常量时间 `hmac.Equal`。旧 SHA-256 标签及其任意 suffix
长度扩展结果都不能通过迁移后的验证。

## 测试与安全边界

测试覆盖 1 至 64 的全部 secret 长度、55/56/63/64/119/120/127/128 等填充和跨块
边界、空 message、空 suffix、错误 tag，以及 suffix 含 `|`、`&`、NUL 和换行等
现有分隔符。SHA-256 续压实现与标准库 `crypto/sha256` 在随机已知 prefix 和
suffix 拼接上逐字节交叉验证。

该攻击只证明题内 `SHA256(secret || message)` 前缀 MAC 认证错误：攻击者追加了
未被认证的字节，并复用旧状态计算了新消息的正确 SHA-256 摘要。它不构成
SHA-256 碰撞、原像或第二原像攻击，也不声称 SHA-256 本身不安全。
