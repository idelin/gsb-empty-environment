# dpopcheck

Go 资源服务器的 DPoP（RFC 9449）校验中间件，纯标准库实现，无外部依赖。

## 校验流程

`Verifier.Middleware` 对每个请求依次执行：

1. 读取 `DPoP` 头中的紧凑 JWS，校验 `typ` 必须为 `dpop+jwt`、`alg` 必须为 `ES256`。
2. 解析受保护头里内嵌的 P-256 JWK，用该公钥验证 JWS 签名（R||S，64 字节）。
3. 校验 `iat` 存在且与当前时间的绝对差不超过允许窗口（默认 60s，`WithWindow` 可调）。
4. 校验 `htm` 与本次请求方法完全一致；`htu` 经过规范化后必须等于本次请求 URL：
   去掉 query 与 fragment、去掉 http/https 默认端口。
5. 服务端一旦通过 `NonceManager.Issue()` 下发过 nonce，后续证明必须携带仍在
   TTL 内的 `nonce`，否则返回 401 `use_dpop_nonce` 并在 `DPoP-Nonce` 响应头补发新值。
6. `jti` 在窗口内只能使用一次（内存缓存，惰性过期），重放返回 401
   `invalid_dpop_proof`。
7. 若请求带 `Authorization: DPoP <token>`，从令牌 `cnf.jkt` 取绑定指纹，与内嵌
   公钥的 RFC 7638 SHA-256 指纹比对，不一致返回 401 `invalid_token`；证明含
   `ath` 时还必须等于 `base64url(SHA-256(access token))`。

## 用法

```go
verifier := dpopcheck.NewVerifier(
    dpopcheck.WithWindow(60 * time.Second),
    // 反向代理后可自定义 htu 规范化，例如读取 X-Forwarded-Proto/Host
    // dpopcheck.WithURLNormalizer(myNormalizer),
)
mux := http.NewServeMux()
http.ListenAndServe(":8080", verifier.Middleware(mux))
```

需要强制 nonce 时（如检测到异常或按运维策略），调用
`verifier.NonceManager().Issue()`；下一次未带有效 nonce 的请求会收到带
`DPoP-Nonce` 头的 401，客户端用该 nonce 重新生成证明即可。

默认从 `Authorization: DPoP` 令牌（JWT）的 `cnf.jkt` 读取绑定指纹，可用
`WithJKTResolver` 接入自有的令牌内省逻辑。

## 测试

`go test ./...` 使用 ES256 私钥在内存中构造证明，覆盖：

- 同一证明二次提交的重放拒绝；
- 跨 URL、跨 HTTP 方法（htm/htu 不匹配）拒绝；
- 令牌 `jkt` 与证明公钥指纹不符拒绝；
- nonce 下发后缺失/伪造 nonce 拒绝、携带正确 nonce 放行；
- `iat` 超出窗口、`typ`/`alg` 错误、签名篡改或由他钥签名拒绝；
- query/fragment/默认端口的 htu 规范化、窗口结束后 jti 可重新使用。
