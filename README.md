# CloudEvents投递授权握手

事件系统向订阅者的 Webhook 投递之前，须确认对方愿意接收，攻击者把受害服务的地址登记成订阅者，让平台变成放大流量的跳板。为Go的事件分发服务写可导入的订阅验证包（CloudEvents HTTP Webhook 滥用防护）：登记目标时先发 OPTIONS，带 WebHook-Request-Origin 与 WebHook-Request-Rate；目标响应须带 WebHook-Allowed-Origin（值为发送方 origin 或 `*`）和 WebHook-Allowed-Rate（正整数或 `*`），缺任一项拒绝登记；实际速率取两方较小值；回调地址须为 HTTPS，主机解析出回环或私网地址拒绝，重定向不跟随；登记 24 小时未重新验证则暂停投递；验证响应来自与登记地址不同的主机时拒绝。测缺头、速率取小、内网地址、重定向、过期。

## webhook 子包

导入路径：`example.org/netsec/q0472/webhook`

### 安全保证

- 登记时向回调地址发送 `OPTIONS` 握手，携带 `WebHook-Request-Origin` 与 `WebHook-Request-Rate`。
- 响应必须同时包含 `WebHook-Allowed-Origin`（等于发送方 origin 或 `*`）与 `WebHook-Allowed-Rate`（正整数或 `*`），缺任一项或取值非法即拒绝登记。
- 实际投递速率取平台请求速率与订阅方允许速率的较小值（`*` 视为不限制）。
- 回调地址强制 `https://`；主机解析到回环、私网、链路本地、未指定、组播或 IPv4 保留网段时拒绝。
- 自定义 `http.Client` 永不跟随重定向，3xx 直接判定握手失败；同时禁用环境代理。
- DNS 结果在一次握手中固定（pin 拨号器），并校验最终响应主机与登记主机一致，防止 DNS rebinding 与响应主机掉包。
- 订阅有效期默认 24 小时；到期未重新验证时 `AllowedToDeliver` 返回 `ErrSubscriptionExpired` 并暂停投递，重新验证成功后续期。

### 用法

```go
verifier, err := webhook.New(webhook.Config{
    Origin:      "https://events.example.org",
    RequestRate: webhook.Rate{RequestsPerSecond: 100},
})
if err != nil { /* 配置非法 */ }

registry := webhook.NewRegistry(webhook.DefaultTTL)
manager := webhook.NewManager(verifier, registry)

sub, err := manager.Register(ctx, "https://subscriber.example/webhook")
if err != nil { /* 握手或安全校验失败，不会写入注册表 */ }

// 投递前准入检查；过期会返回 ErrSubscriptionExpired。
if _, err := manager.AllowedToDeliver(sub.URL); err != nil {
    // 暂停投递，可调用 manager.Reverify(ctx, sub.URL) 续期。
}
```

### 测试

`go test ./...`，覆盖缺头、速率取小、内网/回环地址、重定向不跟随、响应主机不一致与 24 小时过期暂停等场景。测试使用 `net.Pipe` 内存 TLS 服务器，不绑定真实端口、不访问外网。
