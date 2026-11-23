# webhook — CloudEvents HTTP Webhook 订阅验证

可导入的订阅验证包，实现 CloudEvents HTTP Webhook 的滥用防护握手，防止攻击者把受害服务登记为订阅者、把事件平台变成流量放大跳板。

导入路径：`prescreen.local/ps-523bb5e318/webhook`

## 防护规则

- 登记回调时先发送 `OPTIONS`，携带 `WebHook-Request-Origin` 与 `WebHook-Request-Rate`。
- 目标必须在 2xx 响应中返回：
  - `WebHook-Allowed-Origin`：值为发送方 origin 或 `*`，否则拒绝登记。
  - `WebHook-Allowed-Rate`：正整数或 `*`，缺失或非法则拒绝登记。
- 实际投递速率取「发送方请求速率」与「订阅方允许速率」的较小值；订阅方返回 `*` 时仅受发送方速率约束（`Subscription.AllowedRate == 0` 表示不限）。
- 回调地址必须是 `https`。
- 主机解析出回环、私网、链路本地、多播、未指定或 CGNAT 地址时拒绝登记（SSRF 防护）。
- HTTP 客户端永不跟随重定向，3xx 直接判定为验证失败。
- 验证响应的主机与登记主机不一致时拒绝（`ErrHostMismatch`）。
- 验证有效期默认 24 小时；到期未重新验证则 `AllowDelivery` 返回 `ErrExpired`，投递暂停，`Revalidate` 成功后恢复。

## 用法

```go
package main

import (
	"context"
	"log"

	"prescreen.local/ps-523bb5e318/webhook"
)

func main() {
	v, err := webhook.New(webhook.Options{
		Origin:      "event-platform.example.com", // 本平台的 reverse-DNS 标识
		RequestRate: 100,                         // 期望的每秒投递数
	})
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	sub, err := v.Register(ctx, "https://subscriber.example.com/events")
	if err != nil {
		// 常见错误：ErrMissingAllowedOrigin / ErrMissingAllowedRate /
		// ErrBlockedAddress / ErrUnexpectedStatus / ErrHostMismatch
		log.Fatalf("订阅登记被拒绝: %v", err)
	}
	log.Printf("登记成功，有效速率: %d (0=不限)", sub.AllowedRate)

	// 每次实际投递前检查；24 小时未 Revalidate 会返回 ErrExpired。
	if _, err := v.AllowDelivery("https://subscriber.example.com/events"); err != nil {
		// 暂停投递，必要时重新验证
		if _, err := v.Revalidate(ctx, "https://subscriber.example.com/events"); err != nil {
			log.Fatalf("重新验证失败: %v", err)
		}
	}
}
```

## 测试

```sh
go test ./webhook -v -cover
```

测试不依赖真实网络：通过自定义 `http.RoundTripper` 与 `LookupIP` 注入，覆盖缺头、速率取小、非法速率、内网/回环地址、重定向不跟随、响应主机不一致、24 小时过期与重新验证等场景。
