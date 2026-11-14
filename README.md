# Topic 绑定授权

`netsec` 是供 Go 消息代理导入的 topic 绑定授权组件。授权按租户和交换机隔离，默认拒绝；ACL 变更使用代次快照生效，已存在的订阅会在变更时、投递时和消费时重新核验。

## 匹配规则

- 绑定键与路由键都按 `.` 分段，段边界严格匹配。
- `*` 只匹配一个非空段。
- `#` 匹配零个或多个段，可以出现在任意段位置。
- 通配符必须独占一个段；路由键中不允许出现通配符。
- 空段、连续点、首尾点等畸形键一律拒绝（fail closed）。

ACL 中的允许模式与订阅绑定键做语言包含判断，而不是简单的通配匹配。例如允许 `tenant-a.orders.*` 时可以订阅精确的 `tenant-a.orders.created`，但 `tenant-a.orders.#`、`tenant-a.#` 和 `#` 都会被拒绝。

## 使用方式

```go
authorizer, err := netsec.NewAuthorizerWithRules(netsec.Rule{
    Tenant:   "tenant-a",
    Exchange: "orders",
    Patterns: []string{"tenant-a.orders.created"},
})
if err != nil {
    log.Fatal(err)
}

sub, err := authorizer.Subscribe("tenant-a", "orders", "tenant-a.orders.created")
if err != nil {
    log.Fatal(err) // ErrDenied / ErrTenant / ErrExchange / ErrPattern
}
defer sub.Close()

delivered := authorizer.Publish(netsec.Event{
    Tenant:     "tenant-a",
    Exchange:   "orders",
    RoutingKey: "tenant-a.orders.created",
    Payload:    []byte(`{"id":1}`),
})

event, err := sub.Receive(context.Background())
```

`Publish` 只把事件放入同租户、同交换机且绑定匹配的订阅。`Subscription.Receive` 在取出事件时再次核对当前 ACL；若订阅已被收紧的 ACL 撤销，队列会清空并返回 `ErrSubscriptionClosed`。

## 代次与更新

- `SetRules`、`ReplaceRules`、`Update` 原子替换 ACL。
- `Grant`、`Revoke`、`RevokePattern` 做增量更新。
- 每次实际变更都会产生新代次，`Generation` 单调递增；无效更新不会改变 ACL 或代次。
- `AuthorizeBinding` 返回授权时的代次；`Subscription.Generation` 在重新核验成功后更新。

## 验证

`go test ./...` 覆盖 `#` 越权、前缀扩张、跨交换机、跨租户、ACL 更新撤销进行中订阅，以及合法精确绑定只收到授权事件。`coverage_exhaustive_test.go` 还用有限字母表穷举对照 NFA 语言包含实现。
