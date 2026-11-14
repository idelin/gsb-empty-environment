# 监听前缀跨过了租户边界

键值服务允许按前缀监听，攻击者用空前缀或短前缀监听全库变更，读取其他租户写入。请实现供Go键值服务调用的可导入监听授权组件。监听前缀必须落在租户命名空间内，空前缀与跨前缀拒绝；起点、版本与历史回放按权限裁剪。监听数量与事件速率受限。用空前缀、跨租户前缀、历史回放、合法监听和权限变更验证只收到授权键。

## netsec 监听授权组件

包 `netsec`（模块 `example.org/netsec/q0398`）提供可直接导入的
`Authorizer`，由键值服务在服务启动时创建，所有监听注册与写入扇出都
经过它，从而保证租户只能观察到自己命名空间内的键。

### 安全边界

- 监听前缀必须非空，且完整落在该租户已注册的命名空间前缀之内；
  空前缀、跨租户前缀（如 `tenantB/`、跨命名空间的短前缀 `tenant`）、
  含 `.`/`..` 路径段的前缀一律拒绝，返回 `ErrEmptyPrefix` 或
  `ErrPrefixOutsideNamespace`，不创建任何监听。
- 写入（`Publish`）同样校验键归属：不在租户命名空间内的事件直接
  拒绝，且不会进入历史日志。
- 历史回放按当前权限裁剪：只回放本租户、匹配监听前缀、且仍在命名
  空间内的历史事件；起点早于保留窗口时自动夹到最旧可用版本，起点
  新于最新版本返回 `ErrStartInFuture`。
- 权限变更（`SetTenantPrefixes`）后，前缀不再被覆盖的监听立即以
  `*ErrPermissionRevoked` 终止并关闭通道；存活监听只收到新命名空间
  内的后续事件，回放也按新权限过滤。

### 资源限制

- 每个租户的并发监听数受 `Limits.MaxWatches` 限制，超限返回
  `ErrWatchLimitExceeded`；计数按租户独立。
- 每个租户的事件投递受令牌桶 `Limits.EventsPerSecond` /
  `EventBurst` 限制；桶耗尽或通道已满时丢弃该事件、累计到
  `DroppedEvents`，桶耗尽时 `Publish` 同时返回 `ErrRateLimited`
  （事件仍已持久化并可通过回放补取）。

### 用法

```go
auth := netsec.NewAuthorizer(
    netsec.WithLimits(netsec.Limits{MaxWatches: 8, EventsPerSecond: 50, EventBurst: 100}),
    netsec.WithHistoryCapacity(2048),
)
defer auth.Close()

if err := auth.RegisterTenant("tenantA", []string{"tenantA/"}); err != nil {
    return err
}

// startVersion > 0 时先做权限内历史回放，再无缝衔接实时事件。
w, err := auth.Watch(ctx, "tenantA", "tenantA/orders/", 1)
if err != nil {
    return err // ErrEmptyPrefix / ErrPrefixOutsideNamespace / ...
}
defer w.Close()

go func() {
    for ev := range w.Events() {
        handle(ev)
    }
    log.Print(w.Err()) // 权限吊销、ctx 取消或关闭原因
}()

version, err := auth.Publish("tenantA", netsec.OpPut, "tenantA/orders/1", payload)
```

### 测试

`go test -race ./...` 覆盖：空前缀与跨租户前缀拒绝、路径穿越拒绝、
合法监听只收授权键、历史回放按权限与版本裁剪、保留窗口夹取、权限
收窄后的回放过滤与监听吊销、并发监听数限制、令牌桶速率限制、通道
满丢弃计数、context 取消与关闭语义。
