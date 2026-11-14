# 短信验证码跨网关被复用

多网关发送短信验证码，码在网关A消费后网关B仍接受，攻击者用同一码完成两次验证。请实现供Go验证码服务调用的可导入跨网关消费组件。码绑定手机号、用途、发送批次与时间步，消费在共享原子存储完成；跨网关并发只有一个成功。发送频率、重试与号码变更受限。用跨网关复用、并发提交、号码换绑、合法验证和频率限制验证只成功一次。

## 用法

```go
store := netsec.NewMemoryStore() // 共享原子存储；生产可替换为 Redis(Lua) 实现 netsec.Store
svc := netsec.NewService(store, netsec.Config{})
gwA := svc.Gateway("A")
gwB := svc.Gateway("B")

res, _ := gwA.Send("13800000000", "login") // 码绑定手机号+用途+批次+时间步
err := gwB.Verify("13800000000", "login", res.Code) // 跨网关原子消费，仅一次成功
```

- 消费在 `Store.ConsumeCode` 内原子完成：任一网关消费后，其他网关/并发请求得到 `ErrCodeConsumed`。
- 发送受重发间隔与窗口上限限制（`ErrSendTooFrequent` / `ErrSendLimitExceeded`）。
- 错误尝试达到上限即作废（`ErrTooManyAttempts`）；超时间步过期（`ErrCodeExpired`）。
- `Rebind` 换绑受限（`ErrRebindLimitExceeded`）并使旧手机号未消费的码全部失效。

测试：`go test -race ./...`
