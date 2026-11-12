# NATS leafnode JWT 认证

`example.org/netsec/q0488` 是一个可导入的 Go 包，用于验证 NATS 风格的
leafnode 账户/用户 JWT 链。实现只接受 Ed25519 签名，不依赖外部库。

认证顺序：

1. 账户 JWT 必须由已信任运营商的标识密钥或其登记的签名密钥签发。
2. 账户 JWT 和用户 JWT 必须包含 `iat`、`exp`，且不得过期。
3. 用户 JWT 必须由账户标识密钥或账户 `signing_keys` 中的密钥签发。
4. 使用 `signing_keys` 签发时，`issuer_account` 必须非空且严格等于账户 `sub`。
5. 用户 `iat <= 账户撤销时间` 时拒绝；边界值本身无效。
6. nonce 签名使用用户 `sub` 公钥以原始 Ed25519 方式验证，并一次性消费。

## 使用

```go
auth := netsec.NewAuthenticator(netsec.Operator{
    PublicKey: operatorPublicKey,
    SigningKeys: []netsec.Key{
        netsec.NewKey(operatorSigningPublicKey),
    },
})

account, err := auth.VerifyAccount(accountJWT)
if err != nil {
    return err
}
user, err := auth.VerifyUser(userJWT, account)
if err != nil {
    return err
}

challenge, err := auth.Challenge()
if err != nil {
    return err
}
// The leafnode sends challenge. The client signs the raw challenge bytes.
if err := auth.VerifyUserNonce(user, challenge, nonceSignature); err != nil {
    return err
}
```

账户撤销表既可从账户 JWT 的 `nats.revocations` 读取，也可通过
`auth.SetRevocation(accountID, userID, unixTime)` 注入。所有 JWT 和 nonce
测试均使用 `crypto/ed25519` 生成真实密钥和签名：

```sh
go test -race ./...
```
