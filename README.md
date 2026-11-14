# 已签名的单点登出令牌

身份提供方不再接受未签名或无法校验的 `logout` 通知。本包提供可直接导入 Go
单点登出服务的令牌签发、令牌校验、一次性消费、会话存储以及前后通道 HTTP
处理器。

## 安全属性

- 登出令牌使用紧凑 JWS 格式，绑定 `iss`、`aud`、`sid`、`sub`、`iat`、`exp`
  和 `jti`。默认有效期 2 分钟，校验端默认拒绝超过 5 分钟生命周期的令牌。
- 仅接受非对称签名：`RS256/384/512`、`PS256/384/512`、`ES256/384/512` 和
  `EdDSA`。拒绝 `alg=none`、算法与密钥类型不匹配、未知 `kid`、已退役密钥以及
  `crit` 扩展头。
- `iss` 必须精确匹配调用方注册的可信签发者，`aud` 必须包含本登出服务配置的
  受众。令牌中的 `sub` 还会与目标会话的主体核对。
- `Verify` 在完成签名、声明和时效校验后，通过并发安全的 `ReplayCache`
  原子消费令牌。相同 `jti`（没有 `jti` 时使用完整令牌哈希）的并发请求只有
  一个能成功。
- 后通道只接受 `POST application/x-www-form-urlencoded`，要求恰好一个
  `logout_token`，并通过可配置的 `MaxBodyBytes`（默认 64 KiB）限制请求体。
- 前端通道只接受来源命中 `WithAllowedOrigins` 的 `Origin`（缺失时回退到
  `Referer`）请求；`null`、畸形和未登记来源一律返回 403。
- 两个通道都按令牌中的 `sid` 调用 `SessionStore.TerminateSession`，不会删除
  同一用户的其他会话，也不会执行“踢出该用户全部会话”的操作。

## 快速使用

```go
idpKeys := netsec.NewKeySet()
if err := idpKeys.AddSigningKey("2026-11", privateKey); err != nil {
    return err
}
issuer, err := netsec.NewTokenIssuer("https://idp.example.com", idpKeys, 90*time.Second)
if err != nil {
    return err
}
token, err := issuer.IssueLogoutToken(
    "https://sp.example.com", // aud
    "session-123",            // sid
    "user-42",                // sub
)
```

登出服务侧只保存公钥：

```go
verifierKeys := netsec.NewKeySet()
if err := verifierKeys.AddVerificationKey("2026-11", &privateKey.PublicKey); err != nil {
    return err
}
verifier, err := netsec.NewVerifier(netsec.VerifierConfig{
    Audience: "https://sp.example.com",
    Issuers: map[string]*netsec.KeySet{
        "https://idp.example.com": verifierKeys,
    },
})
if err != nil {
    return err
}

store := netsec.NewMemorySessionStore()
_ = store.AddSession("https://idp.example.com", "session-123", "user-42")

service := netsec.NewLogoutService(
    verifier,
    store,
    netsec.WithAllowedOrigins("https://app.example.com"),
)
mux.Handle("/backchannel-logout", service.BackChannelHandler())
mux.Handle("/frontchannel-logout", service.FrontChannelHandler())
```

生产环境中的 `SessionStore` 应实现 `TerminateSession`，并按
`(issuer, sessionID)` 精确删除一条会话。多实例部署时，`ReplayCache` 应替换为
Redis 等共享原子存储，否则各实例会各自消费一次令牌。

## 密钥轮换

过渡期内同时发布新旧公钥，新令牌由新密钥签发；旧令牌仍可验证：

```go
verifierKeys.AddVerificationKey("2026-12", &nextKey.PublicKey)
idpKeys.RotateSigningKey("2026-12", nextKey) // 新令牌使用新 kid
// 旧令牌全部过期后：
verifierKeys.RetireKey("2026-11") // 旧 kid 立刻被拒绝
```

`ReplaceSigningKey` / `RotateSigningKeyAndRetirePrevious` 可在需要立即切断旧密钥
时一次完成替换。

## 验证

运行 `go test ./...`。测试矩阵覆盖：

| 场景 | 预期 |
| --- | --- |
| 合法令牌 | 200，仅目标 `sid` 被终止，同用户其他会话保留 |
| 伪造 issuer | 拒绝，会话保留 |
| 重放同一令牌 | 拒绝，会话不再被终止 |
| 跨受众 | 拒绝，会话保留 |
| 旧/退役密钥、未知 `kid` | 拒绝 |
| 同令牌 32 路并发消费 | 恰好 1 次成功、1 次会话终止 |
| 错误 Content-Type | 415 |
| 超过请求体上限 | 413 |
| 不可信 iframe 来源 | 403 |
