# RFC 9421 请求签名校验

为 Go 网关提供可导入的 RFC 9421 请求签名校验包。校验失败返回带哨兵错误的 `error`，可用 `errors.Is` 区分覆盖缺失、算法不一致、摘要不符、时间戳过期、换行注入等问题。

## 用法

```go
registry := netsec.NewKeyRegistry()
if err := registry.Register("gateway-key-1", netsec.AlgorithmEd25519, publicKey); err != nil {
	// handle key loading error
}

result, err := netsec.VerifyRequest(request, registry)
if err != nil {
	// Reject the request.
}
```

需要固定时钟时传 `netsec.WithNow(now)`。网关收到相对请求目标、或位于反向代理之后时，应从可信代理配置构造绝对目标 URI 并传入 `netsec.WithTargetURI(uri)`；不要使用客户端可控的 `Forwarded`/`X-Forwarded-*` 头。

已读取正文的调用方可使用 `netsec.WithBody(body)`，否则校验器会读取 `request.Body` 并把它恢复为可重读的流。`WithBody` 是单次校验选项；可复用的 `Verifier` 不应绑定某一个请求的正文。

## 校验策略

- 签名算法只来自密钥登记。消息中的 `alg` 不参与算法选择；若出现且与登记值不一致，请求会被拒绝。
- 覆盖列表必须包含 `@method`、`@target-uri` 和 `content-digest`。
- 按 RFC 9421 要求，签名基最后一行始终是 `"@signature-params"`；`created` 等签名参数由该行整体覆盖，缺少 `created` 参数会拒绝。
- `Content-Digest` 必须包含 SHA-256，校验器会对正文重新计算并做常量时间比较。
- `created` 与校验器时钟的偏差绝对值不得超过 60 秒；`expires` 已过则拒绝。
- 构造签名基时，任何参与签名的组件值含 CR 或 LF 都会被拒绝。
- 重复 HTTP 字段按出现顺序使用 `", "` 合并，字段名规范为小写。

支持 `ed25519`、`hmac-sha256`、`ecdsa-p256-sha256`、`ecdsa-p384-sha384`、`rsa-pss-sha512`、`rsa-pss-sha256` 和 `rsa-v1_5-sha256`。测试覆盖了漏覆盖组件、算法替换、正文篡改、CR/LF 注入、重复头顺序及时间窗。
