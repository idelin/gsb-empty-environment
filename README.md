# 设备码授权组件

`example.org/netsec/q0274` 提供可嵌入 Go 授权服务器的设备授权流程状态机。它修复了设备码兑换时用请求 `scope` 覆盖用户批准范围的权限提升问题。

## 安全不变量

- `device_code` 绑定发起授权的 `client_id`、客户端请求范围、用户码、轮询间隔和有效期。
- 用户批准时冻结主体和批准范围；再次以其他主体批准会被拒绝。
- 令牌请求的 `scope` 必须是被冻结范围的子集。省略范围只返回全部已批准范围，不能扩大范围。
- `device_code` 只能成功兑换一次。并发兑换由同一把锁串行化，只有一个请求能返回令牌。
- 早于公布间隔的轮询返回 `slow_down`，不会消费设备码，也不会改变批准状态。
- 拒绝、过期、client 不匹配、批准主体不匹配、范围提升、未知码和已消费码在令牌端点统一返回 `invalid_grant`，避免区分码是否有效。
- 拒绝后的设备码立即失效，不能再次批准或兑换。

## 接入示例

```go
package authserver

import (
	"context"
	"errors"

	device "example.org/netsec/q0274"
)

func deviceFlow(ctx context.Context, issuer *device.Service) error {
	authorization, err := issuer.Begin(ctx, "client-a", []string{"profile:read"})
	if err != nil {
		return err
	}

	// 用户登录后输入 authorization.UserCode。批准页面必须提交用户实际同意的范围。
	approvedScopes := []string{"profile:read"}
	if err := issuer.ApproveScopes(ctx, authorization.UserCode, "user-123", approvedScopes); err != nil {
		return err
	}

	token, err := issuer.Exchange(ctx, device.TokenRequest{
		DeviceCode: authorization.DeviceCode,
		ClientID:   "client-a",
		// Subject 省略时使用批准时记录的主体；非空时必须与批准主体一致。
		// Scopes 省略时返回全部批准范围；显式提交时必须是批准范围的子集。
		Scopes: []string{"profile:read"},
	})
	switch {
	case errors.Is(err, device.ErrAuthorizationPending):
		// 返回 OAuth error=authorization_pending，客户端按 interval 继续轮询。
	case errors.Is(err, device.ErrSlowDown):
		// 返回 OAuth error=slow_down，并增大下一次轮询间隔。
	case errors.Is(err, device.ErrInvalidGrant):
		// 所有终态失败都使用同一个错误，不能向客户端泄露具体原因。
	case err != nil:
		return err
	default:
		_ = token.Scopes // 已冻结批准范围的子集
	}
	return nil
}
```

HTTP 表单中的 `scope` 可以直接放入 `TokenRequest.Scope`（空格分隔）；它和 `TokenRequest.Scopes` 会合并后一起做子集校验。

审批接口有两个入口：

- `Approve` 批准客户端最初请求的全部范围。
- `ApproveScopes` 只批准传入的精确子集；传入 `nil` 或空集合表示不批准任何范围，不会回退为全部请求范围。

## 配置

`netsec.New` 的零值配置使用安全默认值：5 秒轮询间隔、10 分钟设备码有效期、1 小时令牌有效期、8 位用户码和 32 字节随机数。

```go
service := device.New(device.Config{
	PollInterval:  5 * time.Second,
	DeviceCodeTTL: 10 * time.Minute,
	TokenTTL:      time.Hour,
})
```

测试可通过 `WithClock`/`WithNow` 注入时钟，通过 `WithRandom` 注入熵源。服务状态保存在进程内；多实例部署时应使用粘性会话，或将状态机封装到共享存储适配层。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖范围提升、空格分隔 scope 提升、跨 client 轮询、批准主体变化、拒绝后重试、过期、轮询限速、并发兑换和合法低权限令牌。
