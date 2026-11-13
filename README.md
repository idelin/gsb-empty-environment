# 反向通道登出令牌校验

攻击者把一个普通 ID 令牌当登出令牌投给服务，或重复投递，想强制他人下线。本包为 Go 依赖方实现 OpenID Connect Back-Channel Logout 令牌校验：

- 头部 `typ` 必须为 `logout+jwt`（媒体类型比较不区分大小写）。
- `events` 必须包含 `http://schemas.openid.net/event/backchannel-logout`，且事件值为 JSON 对象。
- `sub` 与 `sid` 至少有一个，且值必须为非空字符串。
- 任意 `nonce` 成员都会导致拒绝，包括 `null`。
- 先校验签名、`iss`、`aud`，再处理登出语义；签名支持 RS/PS/ES、EdDSA 和 HMAC。
- `jti` 在同一 `iss` 下 10 分钟内重复出现时拒绝；只有完整校验通过的令牌才会占用 `jti`。
- 结果含 `sid` 时只终止该会话；仅含 `sub` 时终止该主体的全部会话。
- 若提供 `exp`、`iat`、`nbf`，同时校验其时间和类型。

## 使用

```go
import (
	"context"
	"netsec "example.org/netsec/q0758"
)

validator, err := netsec.NewValidator(netsec.Config{
	Issuer:   "https://issuer.example",
	Audience: "client-0758",
	KeyFunc: func(ctx context.Context, token *netsec.Token) (any, error) {
		// token.Claims 此时尚未通过签名校验，只能用于选择密钥，
		// 例如按 token.Header.KeyID 查询公钥。
		return lookupPublicKey(token.Header.KeyID)
	},
})
if err != nil {
	return err
}

logout, err := validator.Validate(ctx, rawLogoutToken)
if err != nil {
	return err // 任意错误均应拒绝请求
}

switch logout.Scope() {
case netsec.TerminateSession:
	revokeSession(logout.Subject, logout.SessionID)
case netsec.TerminateAllSessions:
	revokeAllSessions(logout.Subject)
}
```

默认情况下每个 `Validator` 使用独立的 `ReplayCache`。同一进程内有多个校验器或需要跨请求共享防重放状态时，显式传入同一个缓存：

```go
cache := netsec.NewReplayCache()
validator, err := netsec.NewValidator(netsec.Config{
	Issuer:      "https://issuer.example",
	Audience:    "client-0758",
	KeyFunc:     keyFunc,
	ReplayCache: cache,
})
```

可设置 `Clock`、`ClockSkew`、`AllowedAlgorithms` 和 `MaxTokenSize`。测试或固定部署时间时使用 `Clock`；生产环境应限制为签发方实际使用的算法，例如只允许 `RS256`。

## 测试覆盖

`go test ./...` 覆盖：

- 普通 ID 令牌以 `typ: JWT` 冒充登出令牌时拒绝；
- 含 `nonce` 的登出令牌拒绝；
- 同一 `jti` 在 10 分钟内重复投递时拒绝，超过窗口后允许，并发投递只允许一次；
- 仅含 `sub` 时返回全会话终止范围，含 `sid` 时返回单会话终止范围；
- `iss`、`aud`、签名、事件、`sub`/`sid`、过期时间及 `alg: none` 的拒绝路径。

```sh
go test ./...
go test -race ./...
go vet ./...
```
