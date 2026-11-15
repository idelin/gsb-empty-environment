# auth_request缓存键绑定

网关缓存子请求授权结果来降低延迟时，缓存键若只含路径，攻击者就能借他人缓存的放行结果访问受限资源。为Go网关写可导入的授权结果缓存包（模拟 Nginx auth_request 加缓存）：键须绑定规范化后的方法、主机、路径与查询、Authorization 值的 SHA-256、会话 Cookie 的 SHA-256 以及授权服务声明的 Vary 头值；Authorization 与 Cookie 都缺失的请求不缓存；403 缓存不超过 5 秒，200 缓存不超过授权服务 Cache-Control 的 max-age 且上限 60 秒，401 与 5xx 不缓存；路径规范化须消除 `.`、`..`、`%2e`，并拒绝 `%2f`。测换用户、换方法、路径编码绕过、过期边界。

## 使用

```go
import authcache "example.org/netsec/q0268"

cache := authcache.New()

if cached, ok := cache.Get(req); ok {
	// 直接使用 cached.StatusCode、cached.Header 和 cached.Body
	_ = cached
} else {
	// 调用授权子请求，response 为 *http.Response
	stored, err := cache.Store(req, response)
	if err != nil {
		// 路径非法或响应不可读取时失败关闭
	}
	_ = stored
}
```

缓存结果也可以先构造成 `authcache.NewResult(status, header, body)`，
再通过 `Store` 或 `Put` 写入。缓存支持零值使用，并可在并发网关 goroutine
中安全调用。

## 策略

- 键包含规范化方法、协议、主机、路径、原始查询、Authorization 哈希、
  Cookie 哈希，以及授权响应 `Vary` 指定请求头的全部值。
- Authorization 和 Cookie 都为空时不写入缓存。
- `200` 必须带有效 `Cache-Control: max-age=N`，TTL 取 `min(N, 60s)`。
- `403` 默认缓存 5 秒；若声明了更短的 `max-age` 则使用更短值，绝不
  超过 5 秒。
- `401`、`5xx`、`no-store`、`no-cache` 和 `Vary: *` 不缓存。
- 路径会拒绝任意解码层级的 `%2f`/`%2F`，并消除点段及重复编码的点号；
  需要拿到错误时使用 `LookupResult` 或 `BuildKey`。

`Get`/`Lookup` 在路径非法时按未命中处理；安全敏感的网关应使用
`LookupResult` 并拒绝请求，而不是继续回调授权服务。
