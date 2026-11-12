# auth_request缓存键绑定

网关缓存子请求授权结果来降低延迟时，缓存键若只含路径，攻击者就能借他人缓存的放行结果访问受限资源。为Go网关写可导入的授权结果缓存包（模拟 Nginx auth_request 加缓存）：键须绑定规范化后的方法、主机、路径与查询、Authorization 值的 SHA-256、会话 Cookie 的 SHA-256 以及授权服务声明的 Vary 头值；Authorization 与 Cookie 都缺失的请求不缓存；403 缓存不超过 5 秒，200 缓存不超过授权服务 Cache-Control 的 max-age 且上限 60 秒，401 与 5xx 不缓存；路径规范化须消除 `.`、`..`、`%2e`，并拒绝 `%2f`。测换用户、换方法、路径编码绕过、过期边界。

## 包：`authcache`

导入路径：`example.org/netsec/q0268/authcache`

### 安全要点

- 缓存键绑定：HTTP 方法、主机、规范化路径与查询串、`Authorization` 头值的 SHA-256、会话 Cookie 值的 SHA-256（默认 Cookie 名 `session`，可配置），以及授权服务 `Vary` 声明的请求头取值；原始凭据绝不入键。
- `Authorization` 与会话 Cookie 同时缺失的请求只透传授权子请求、不写缓存，防止匿名结果被复用。
- 状态策略：`200` 按 `Cache-Control: max-age` 缓存且上限 60 秒（未声明时默认 60 秒，`max-age=0` 不缓存）；`403` 上限 5 秒（未声明时默认 5 秒）；`401` 与 `5xx` 不缓存；`no-store`/`no-cache` 与 `Vary: *` 不缓存。
- 路径规范化：按 RFC 3986 5.2.4 消除 `.`、`..` 及 `%2e`/`%2E` 编码变体；路径中出现 `%2f`/`%2F` 直接返回 `ErrEncodedSlash`，不发起授权子请求。

### 用法

```go
import "example.org/netsec/q0268/authcache"

authorizer, err := authcache.NewHTTPAuthorizer("https://authz.internal/check", nil)
if err != nil {
    return err
}
c := authcache.New(authcache.Config{
    Authorizer:    authorizer,
    SessionCookie: "session", // 可选，默认 "session"
})
defer close(c.StartCleanup(time.Minute)) // 可选：后台回收过期条目

// 网关处理请求时：
resp, err := c.Authorize(incomingRequest)
if err != nil {
    // 路径含 %2f 等规范化错误，或授权子请求失败
}
// resp.StatusCode == 200 放行；401/403 拒绝；其余按网关策略处理
```

### 测试

`go test ./... -race -cover`，覆盖换用户、换方法、主机/查询/会话隔离、路径编码绕过（`.`/`..`/`%2e` 归一与 `%2f` 拒绝）、Vary 变体、各状态码缓存策略与 403/200 的过期边界。
