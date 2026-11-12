# eSIM 下载会话认证防重放

攻击者重放旧的 eUICC 认证应答，或拿别人订单的 matchingId 换 eSIM 配置文件。本包为 Go 的 SM-DP+ 提供 ES9+ 会话守卫：

- `transactionId` 与服务端挑战一次性使用，随会话 TTL 过期；同一订单重新发起认证会使旧挑战失效。
- 认证应答签名必须覆盖 `transactionId || serverChallenge`。
- `matchingId` 在成功下载配置文件后作废，并永久绑定到订单 EID。
- 确认码校验使用 `SHA256(SHA256(确认码) || transactionId)`，错误次数超过上限后锁定订单（默认允许 3 次错误，第 4 次锁定）。

## 使用

```go
guard := netsec.NewGuard(
    netsec.WithSessionTTL(5*time.Minute),
    netsec.WithFailureLimit(3),
)

err := guard.RegisterOrder(netsec.Order{
    MatchingID:       "order-matching-id",
    EID:              "89049032000000000000000000000001",
    ConfirmationCode: "12345678",
    Verifier:         netsec.NewECDSAVerifier(euiccPublicKey),
})

challenge, err := guard.InitiateAuthentication("order-matching-id")
// 将 challenge.TransactionID 与 challenge.ServerChallenge 交给 eUICC 签名。

err = guard.AuthenticateClient(netsec.AuthenticationResponse{
    TransactionID:   challenge.TransactionID,
    ServerChallenge: challenge.ServerChallenge,
    EID:             "89049032000000000000000000000001",
    Signature:       euiccSignature,
})

err = guard.GetBoundProfilePackage(netsec.ProfileRequest{
    TransactionID:    challenge.TransactionID,
    MatchingID:       "order-matching-id",
    EID:              "89049032000000000000000000000001",
    ConfirmationCode: "12345678",
})
```

`SignatureVerifier` 可接入 eUICC 的 ECDSA 公钥（`NewECDSAVerifier`），测试环境也可用 `NewHMACVerifier` / `SignHMAC`。`AuthenticationPayload` 给出签名必须覆盖的规范字节。

## 攻击验证

`go test -race ./...` 覆盖：

- 重放同一认证应答、重放旧挑战、并发重复使用应答、会话过期。
- 串用其他订单的 `transactionId` / `matchingId` / EID，以及用错误密钥应答挑战。
- 签名只覆盖 `transactionId` 或只覆盖挑战时拒绝。
- 确认码哈希公式，以及跨会话累计错误后锁定订单。
