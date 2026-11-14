# 交易额度扣减组件

`example.org/netsec/q0467` 是可直接嵌入 Go 限额服务的额度预留组件。它不会暴露“先查余额、后扣减”的两步调用；准入判断和额度扣减由 `Reserve` 在同一个加锁临界区内完成。

## 行为

- 账户的日/月额度由所有渠道共享；`AccountPolicy.ChannelLimits` 可额外设置渠道上限。
- 日/月窗口按 `Config.Location` 的日历边界切换。
- 相同幂等键、相同账户/渠道/金额的并发重试只扣减一次。
- `Release` 按预留时所属的日/月窗口回滚，因此跨窗口后释放不会污染当前窗口。
- 已释放的幂等键保留墓碑；再次预留返回 `ErrReservationReleased`，不会重复扣减。

## 使用

```go
engine, err := netsec.New(netsec.Config{
    Location: time.FixedZone("UTC+8", 8*60*60),
    Accounts: map[string]netsec.AccountPolicy{
        "acct-1": {
            DailyLimit:   100_000,
            MonthlyLimit: 2_000_000,
            ChannelLimits: map[string]netsec.Limits{
                "card": {Daily: 60_000},
            },
        },
    },
})
if err != nil {
    return err
}

reservation, err := engine.Reserve(ctx, netsec.ReserveRequest{
    IdempotencyKey: "order-20261114-001",
    AccountID:      "acct-1",
    Channel:        "card",
    Amount:         50_000,
    At:             time.Now(),
})
if errors.Is(err, netsec.ErrQuotaExceeded) {
    // 拒绝交易，无需释放。
} else if err != nil {
    return err
}

if err := pay(); err != nil {
    // 下游失败时归还本次预留；重复调用 Release 是安全的。
    _ = engine.Release(ctx, reservation.IdempotencyKey)
    return err
}
```

多个预留只要业务最终成功，就不需要调用 `Release`。`Usage` 仅用于监控和对账，不应作为放行依据。

## 验证

```sh
go test ./...
go test -race ./...
```

测试覆盖并发争抢同一额度、跨渠道共享总额、幂等重试、失败释放、恰好用满剩余额度、日窗口切换、月窗口累计和跨窗口释放。

当前引擎在单进程内用互斥锁保证原子性。若限额服务部署多个实例，需要将相同状态机放入共享的事务存储（例如数据库行锁或 Redis Lua 脚本），不能在每个实例内各维护一份。
