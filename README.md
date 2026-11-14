# 一次性魔法链接被预览机器人消耗了

登录邮件里的魔法链接一被打开即失效，邮件网关的链接预览先发起请求，用户再点时已不可用，攻击者也可反复触发预览消耗他人链接。请实现供 Go 认证服务调用的可导入魔法链接组件。令牌绑定用户、会话意图、签发时间、短有效期与一次性消费；首次请求只做无副作用的探测，真正消费必须来自带明确用户动作的 POST 与同站来源。预览代理的 User-Agent 与来源不被信任，GET 不得改变认证状态。用预览消耗、重复点击、跨站触发、过期令牌和合法登录验证令牌只在用户动作时生效。

## 实现

本模块提供包 `example.org/netsec/q0314`（包名 `netsec`），认证服务可直接导入使用。

- `Service.Issue` / `Service.IssueFor`：签发 HMAC-SHA256 令牌。令牌包含用户、会话意图、可选会话 ID、签发时间、过期时间和随机令牌 ID。
- `Service.Probe`：只验证签名、有效期和消费状态，不写入任何认证或消费状态。邮件预览可以无限次调用。
- `Service.Consume`：要求 `POST` 动作、精确匹配的可信 `Origin`，并原子消费令牌；并发请求只有一个可成功。
- `Handler`：GET 只展示确认页，绝不下发会话 Cookie；只有通过同源检查的 POST 才会调用 `OnConsume`。

`User-Agent` 和 `Referer` 不参与信任判断，也不会被当作同源证据。`Origin` 必须精确匹配配置的可信源；`Sec-Fetch-Site` 存在时还必须是 `same-origin` 或 `same-site`，否则拒绝。

## 接入示例

```go
package auth

import (
	"context"
	"net/http"
	"time"

	netsec "example.org/netsec/q0314"
)

func newMagicLinkHandler(secret []byte, store netsec.Store) (http.Handler, *netsec.Service, error) {
	service, err := netsec.NewService(netsec.Config{
		Secret:         secret,
		TTL:            5 * time.Minute,
		Store:          store,
		AllowedOrigins: []string{"https://auth.example.com"},
	})
	if err != nil {
		return nil, nil, err
	}

	handler, err := netsec.NewHandler(netsec.HandlerConfig{
		Service:        service,
		RequiredIntent: "login",
		ExpectedBinding: func(r *http.Request) (netsec.Binding, error) {
			// 从待登录会话中读取用户和会话 ID，而不是信任请求头。
			return pendingLoginBinding(r)
		},
		OnConsume: func(ctx context.Context, w http.ResponseWriter, r *http.Request, claims netsec.Claims) error {
			// 到这里令牌已经原子消费成功，可以创建或轮换会话。
			return establishSession(ctx, w, r, claims)
		},
		SuccessURL: "/account",
	})
	if err != nil {
		return nil, nil, err
	}
	return handler, service, nil
}
```

签发后把令牌放进邮件链接，例如：

```go
token, err := service.Issue("user-123", "login")
link := "https://auth.example.com/login?token=" + url.QueryEscape(token)
```

GET 确认页会把令牌放入隐藏表单字段，提交时使用 POST。浏览器对 POST 自动发送的 `Origin` 是消费判断的依据；`GET`、链接预览、`Referer` 和 `User-Agent` 都不会让用户登录。

## 生产要求

- `Store` 在多进程或多实例部署中必须共享，并且 `MarkConsumed` 要使用唯一约束或 compare-and-set 保证原子性；`MemoryStore` 只适合单进程。
- 不要把 `Probe` 的结果当作登录成功。只有 `Consume` 返回成功后才能调用认证服务创建会话。
- `GET` 处理器不能设置认证 Cookie 或修改会话状态。本包的 `Handler` 已遵守这一点。
- 使用至少 32 字节的随机 `Secret`，通过安全配置系统注入；轮换 Secret 会使旧链接立即失效。
- 魔法链接应使用短 TTL（通常 5–10 分钟），并通过 HTTPS 发送。

## 测试覆盖

`go test ./...` 覆盖：

- 邮件预览多次 GET 不消费令牌，且不为 GET 设置 Cookie；
- 合法同源 POST 只消费一次，重复点击返回失效；
- 跨站 `Origin`、缺失 `Origin`、`Fetch-Site: cross-site` 均不消费；
- 过期令牌在 GET/POST 均不可用；
- 用户、意图、会话绑定不匹配时不消费，修正后仍可合法使用；
- 签名篡改、跨实例共享消费状态、并发消费单赢家。
