# RFC 9449 DPoP 校验中间件（`dpop` 包）

零第三方依赖（仅 Go 标准库 `crypto/ecdsa`、`crypto/ed25519`、`crypto/sha256`
等）的可导入服务端组件。

## 每个受保护请求的校验流程

1. 解析单个 `Authorization: DPoP <token>` 与 `DPoP: <jwt>`；拒绝多头、逗号
   列表等头部走私。带 `cnf.jkt` 的令牌以 `Bearer` 呈现时硬拒绝（防降级）。
2. JWT 头必须 `typ="dpop+jwt"`，`alg` 仅允许 `ES256`/`EdDSA`；拒绝 `none`、
   `HS256` 等无签名/对称算法与 `jku`/`x5u` 引用；嵌入 `jwk` 不得含 `d`/`k`/
   `p`/`q` 等私钥或对称字段，重复 JSON 成员同样被拒绝。
3. 计算 JWK 的 RFC 7638 规范 SHA-256 拇指印，以恒定时间比较与访问令牌
   `cnf.jkt` 做持有者绑定校验。
4. 载荷校验：`htm` 恒定时间匹配 HTTP 方法；`htu` 与去除 query/fragment 的
   规范目标 URL 比较（scheme/host 大小写不敏感、path 大小写敏感，并拒绝携带
   query/fragment 的混淆 `htu`）；`ath` 等于 `Base64URL(SHA-256(token))`；
   `iat` 落在 `ProofTTL + ClockSkew` 窗内；`jti` 在有界窗口内全局唯一，跨
   端点重放同样被拦截。
5. `NonceManager` 用 HMAC 按周期派生密钥，接受当前与上一周期 nonce 实现无缝
   轮换；缺失/过期/错签返回 `use_dpop_nonce` 并通过 `DPoP-Nonce` 响应头下发
   新 nonce。

## 集成方式

```go
nonceManager, _ := dpop.NewNonceManager(secret, time.Hour)
htuFunc, _ := dpop.BaseURLHTU("https://api.example.com")
verifier, _ := dpop.NewVerifier(dpop.Options{
    NonceManager: nonceManager,
    HTUFunc:      htuFunc,
    Inspector:    myTokenValidator, // 生产环境须自行校验 JWT 签名或内省
})
http.Handle("/resource", verifier.Middleware(protectedHandler))
```

默认 `Inspector`（`ParseJWTTokenClaims`）仅解码 JWT 载荷提取 `cnf.jkt`，不
校验访问令牌签名；生产网关应包装它或接入令牌内省。验证通过的证明材料通过
`dpop.FromContext(r.Context())` 暴露给下游处理器。

## 测试

`go test ./... -race` 覆盖私钥 JWK 注入、对称/`none` 算法、URL 混淆、Bearer
降级、多头走私、跨端点 `jti` 重放、时钟窗与 `DPoP-Nonce` 轮换等场景。
