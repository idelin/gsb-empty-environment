# WebSub 订阅意图验证

WebSub 中心收到订阅请求后须向回调地址确认意图，攻击者替受害者的回调地址批量订阅，或在确认时猜测挑战串。为Go的 WebSub 中心写可导入的订阅管理包：收到 hub.mode=subscribe 先返回 202，再以 GET 向 hub.callback 发送 hub.mode、hub.topic、hub.challenge、hub.lease_seconds；回调须返回 2xx 且正文与 hub.challenge 逐字节相等才生效，challenge 为至少 16 字节随机数的十六进制；lease_seconds 取请求值与上限 864000 中较小者；hub.secret 须小于 200 字节，内容分发时在 X-Hub-Signature 头里带 `sha256=` 加 HMAC-SHA256 十六进制；回调为非 HTTPS 或解析到私网地址拒绝；同一回调对同一 topic 重复订阅只更新租期；同一回调每小时未验证的订阅请求超过 20 个即拒绝。测错误挑战串、私网回调、租期上限、未验证洪泛。

## 用法

包路径为 `example.org/netsec/q0421`，包名为 `netsec`。`Manager` 同时实现
`http.Handler`：

```go
hub := netsec.NewHub()
mux.Handle("/websub", hub)
```

收到合法的 `hub.mode=subscribe` 后，处理器立即写回 `202 Accepted`，再异步
向 `hub.callback` 发起验证 GET。只有回调返回 2xx 且正文与 challenge 完全
一致时，订阅才会写入内存存储。测试或内网部署可通过
`netsec.WithHTTPClient` 和 `netsec.WithAllowPrivateCallbacks` 注入客户端和
放开私网限制；生产环境建议保留默认严格模式。

需要同步验证时可调用：

```go
sub, err := hub.Subscribe(ctx, netsec.SubscriptionRequest{
    Callback:     callback,
    Topic:        topic,
    Secret:       secret,
    LeaseSeconds: 3600,
})
```

内容分发与签名：

```go
results := hub.Distribute(ctx, topic, payload)
header := netsec.Signature(secret, payload) // sha256=<hex>
```

主要导出项：

- `New`、`NewManager`、`NewHub`、`NewSubscriptionManager`：构造入口。
- `Manager.ServeHTTP` / `Manager.Handler`：订阅请求入口。
- `Manager.Subscribe` / `Manager.Verify`：同步验证并保存订阅。
- `Manager.GetSubscription`、`Manager.Subscriptions`：查询已验证订阅。
- `Manager.Distribute`、`Signature`、`SetSignature`：分发和 HMAC-SHA256 签名。
- `Manager.Wait`：等待所有异步验证结束，便于关停和测试。

默认安全限制：challenge 为 16 个随机字节的十六进制；租期上限 864000 秒；
secret 必须小于 200 字节；同一回调地址滚动一小时内最多发起 20 次订阅验证。
