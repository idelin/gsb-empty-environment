# rwlease — 可撤销读写租约

纯 Go 标准库（`sync`/`context`/`time`/`errors`）实现的可撤销读写租约协调器。

## 特性

- **写者优先防饥饿**：内部 FIFO 队列 + 门控。一旦有写者排队，其后到达的新读者
  一律等待；队首写者在现有读者全部释放后立即获得租约。持续读流下写者必然最终获锁。
- **读 → 写升级，失败不丢读权**：`Upgrade` 只在成功时把读令牌原子转换成写令牌；
  返回 `ErrUpgradeConflict` / `ErrUpgradeImpossible` / `ErrTimeout` / context 错误时，
  原读令牌继续有效。
- **两个读者同时升级不死锁**：全库只有一个升级槽，槽位的"检查 + 占用"在同一把锁内
  原子完成。后来者立刻得到 `ErrUpgradeConflict`，绝不进入互相等待。
- **租约令牌安全释放**：
  - 重复 `Release` → `ErrAlreadyReleased`
  - 释放被撤销旧代（`Revoke` 之后）的令牌 → `ErrLeaseRevoked`（防越代释放）
  - 伪造 / 其他 Manager 的令牌 → `ErrNotHeld`
  - 升级成功后释放旧读令牌 → `ErrTokenReplaced`
- **超时 / 取消会移除队列节点**：`AcquireFor`/`UpgradeFor` 使用可注入的 `Clock`，
  context 取消或超时后节点出队并重新推动授权，不会堵死后继等待者。
- **可撤销**：`Revoke()` 代号 +1，令当前代所有存活令牌失效、所有排队等待立即失败；
  新获取的令牌属于新一代。
- **任务注入**：`WithLease(ctx, mode, task)` 获取租约后执行注入任务，结束必然释放；
  任务失败错误会被传播。
- **时间注入**：`WithClock(Clock)` 注入时钟，测试用假时钟确定性地驱动超时，无需 sleep。

## API 概览

```go
m := rwlease.NewManager()                       // 可选 rwlease.WithClock(clk)

r, err := m.Acquire(ctx, rwlease.Read)          // 读租约
w, err := m.AcquireFor(ctx, rwlease.Write, 5*time.Millisecond) // 有界等待
w, err := m.Upgrade(ctx, r)                     // 读 -> 写（失败保留读权）
w, err := m.UpgradeFor(ctx, r, 5*time.Millisecond)
err = m.Release(tok)                            // 幂等保护 + 代校验
m.Revoke()                                      // 撤销当前代
err = m.WithLease(ctx, rwlease.Write, func(tok rwlease.Token) error { ... })
```

## 错误

| 错误 | 含义 |
| --- | --- |
| `ErrAlreadyReleased` | 令牌已释放，禁止重复释放 |
| `ErrLeaseRevoked` | 令牌属于被撤销的旧代（越代释放） |
| `ErrNotHeld` | 令牌伪造、来自别的 Manager、或模式不符 |
| `ErrUpgradeConflict` | 已有升级进行中（读权保留） |
| `ErrUpgradeImpossible` | 有活动/排队写者，不能插队（读权保留） |
| `ErrTokenReplaced` | 升级成功后释放旧读令牌 |
| `ErrTimeout` | 有界等待超时，队列节点已移除 |

## 测试

```sh
go test -race -count=1 ./...
```

测试覆盖：持续读流下写者获锁、双读者升级不死锁、升级失败保留读权、超时/取消移除
队列节点、重复释放/越代释放/伪造令牌、`Revoke` 代失效、任务注入失败传播，以及一个
`-race` 压力测试和一个确定性输出的 `Example`。
