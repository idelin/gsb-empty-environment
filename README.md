# Traefik中间件顺序与逐跳头

`example.org/netsec/q0278` 提供一个可导入的 Go 反向代理中间件链。链在构造时固定为：

```text
清洗 -> 认证 -> 注入身份 -> 自定义中间件 -> 转发
```

`Chain` 不暴露重排阶段或追加中间件的方法。`WithMiddleware` 只在构造时生效，并且自定义中间件始终位于身份注入之后。

## 使用

```go
import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"example.org/netsec/q0278"
)

target, _ := url.Parse("http://backend.internal")
proxy := httputil.NewSingleHostReverseProxy(target)

chain, err := netsec.NewChain(
	func(r *http.Request) (netsec.Identity, error) {
		// 在这里校验 cookie、JWT 或外部会话。
		return netsec.Identity{
			User:   "alice",
			Groups: []string{"staff", "ops"},
		}, nil
	},
	proxy,
	netsec.WithMiddleware(requestLogger),
)
if err != nil {
	panic(err)
}

http.ListenAndServe(":8080", chain)
```

认证成功后，链设置：

```text
X-Auth-User: alice
X-Auth-Groups: staff,ops
```

自定义中间件可以从请求头读取这两个值，也可以使用 `netsec.IdentityFromContext`。

## 安全行为

- 清洗发生在认证之前，外部传入的 `X-Auth-User` 和 `X-Auth-Groups` 一律删除。
- `Connection` 中点名的请求头会删除；标准逐跳头 `Connection`、`Keep-Alive`、`Proxy-Authenticate`、`Proxy-Authorization`、`TE`、`Trailer`、`Transfer-Encoding`、`Upgrade` 也会删除。
- 如果 `Connection` 点名 `X-Auth-User` 或 `X-Auth-Groups`，请求直接返回 `400 Bad Request`，不会进入认证或转发。
- 认证失败时只运行配置的未授权处理器，不运行后续自定义中间件和转发处理器，也不会生成身份响应头。
- `SanitizeRequest` 和 `SanitizeMiddleware` 可单独用于其他代理管线。
