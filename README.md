# 反向代理身份头中间件链

`netsec` 提供固定顺序的 Go 反向代理中间件链：

```text
清洗 -> 认证 -> 注入身份头 -> 转发
```

它用于避免两类身份伪造问题：

- 客户端在 `Connection` 中点名 `X-Auth-User` 或 `X-Auth-Groups`，诱导代理在注入后把身份头当作逐跳头删除。
- 客户端直接提交伪造的 `X-Auth-User` 或 `X-Auth-Groups`，在认证和注入前进入上游。

清洗、认证、注入和转发的位置在 `New`/`NewChain` 构造时固定，`Chain` 创建后不可重排。自定义中间件只能位于注入之后。

## 用法

```go
target, _ := url.Parse("http://upstream.internal")
proxy := httputil.NewSingleHostReverseProxy(target)

chain, err := netsec.New(
	func(r *http.Request) (netsec.Identity, error) {
		// 只信任这里产生的身份。r 已经过清洗，不会包含客户端伪造的内部身份头。
		user, err := authenticate(r)
		if err != nil {
			return netsec.Identity{}, err
		}
		return netsec.Identity{
			User:   user.Name,
			Groups: user.Groups,
		}, nil
	},
	proxy,
	// 下面这个自定义中间件一定在身份注入之后运行。
	func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	},
)
if err != nil {
	log.Fatal(err)
}

http.Handle("/", chain)
```

需要显式声明自定义中间件位置时使用 `NewChain`。只有 `netsec.PositionAfterInjection` 会被接受：

```go
chain, err := netsec.NewChain(authenticate, proxy, netsec.MiddlewareSpec{
	Position:   netsec.PositionAfterInjection,
	Middleware: auditMiddleware,
})
```

`PositionBeforeCleanup`、`PositionAfterCleanup` 和 `PositionAfterAuthentication` 都会返回 `ErrInvalidMiddlewarePosition`。

## 安全行为

- 清洗阶段在任何认证逻辑之前删除外部传入的 `X-Auth-User`、`X-Auth-Groups`。
- `Connection` 中列出内部身份头时，请求直接返回 `400 Bad Request`，不会进入认证或转发。
- 清洗阶段删除 `Connection` 点名的头，以及 `Keep-Alive`、`Proxy-Authenticate`、`Proxy-Authorization`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade` 等逐跳头。
- 认证失败默认返回 `401`；认证器可返回 `*netsec.AuthError` 指定其他 4xx/5xx 状态。
- 认证失败、清洗拒绝和内部身份缺失的响应都会清除 `X-Auth-User`、`X-Auth-Groups`，不会带出注入身份。
- 注入阶段重新生成身份头，不保留清洗前或认证前的同名值。无组时不会生成 `X-Auth-Groups`。

自定义中间件位于注入之后，因此属于可信调用方代码；它可以读取已注入的身份，也可以自行修改请求。链只保证它无法被插到认证或注入之前。

## 测试

```sh
go test ./...
go vet ./...
```
