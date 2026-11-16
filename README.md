# auth_request 授权结果缓存

可导入的 Go 包，模拟 Nginx `auth_request` 结果的网关缓存。缓存键绑定规范化后的
方法、主机、路径、查询、`Authorization` 的 SHA-256、`Cookie` 的 SHA-256，以及授权
响应声明的 `Vary` 请求头值。凭证原文不会存入缓存。

## 使用

```go
import authcache "example.org/netsec/q0268"

cache := authcache.New()

if result, ok := cache.Get(r); ok {
    // 直接复用授权结果。
    writeAuthResult(w, result)
    return
}

// 这里执行实际的 auth_request 子请求。
authResponse := callAuthorizationService(r)

// 仅 200/403 且满足新鲜度策略时返回 true。
_ = cache.Set(r, authResponse.StatusCode, authResponse.Header)
writeAuthResult(w, authResponse)
```

`Get` 和 `Set` 对缺失凭证、畸形百分号编码、编码斜杠及 `Vary: *` 一律失败关闭。
非缓存状态（包括 401 与 5xx）会清除同一基础键的旧结果，避免继续复用旧的放行决定。

## 策略

- 路径逐层解码百分号转义，拒绝任意层级的 `%2f`，再消除 `.`、`..`、`%2e` 点段。
- 查询参数逐项规范化，保留参数顺序和重复参数。
- `Authorization` 与 `Cookie` 都缺失时不缓存。
- 200 必须有正的 `Cache-Control: max-age`，TTL 取 `max-age` 与 60 秒的较小值。
- 403 默认缓存 5 秒；若 `max-age` 更短则使用更短值，绝不会超过 5 秒。
- `Age` 会从剩余 TTL 中扣除；`no-store`、`no-cache`、`private` 不缓存。
- 401、5xx 及其他状态码不缓存。

## 测试

```sh
go test -race ./...
```
