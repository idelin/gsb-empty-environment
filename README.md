# objstore

Go 对象库：带宽限期的软删除、代际（generation）防护的清理任务、引用账，以及可确定性复现边界竞争的虚拟时钟。

## 生命周期

```
                Delete                宽限期到期(清理任务)
  Create ──► Active ──────► PendingDeletion ──────────────► Deleted
               ▲                  │
               │                  │ AddRef（合法新引用）
               │                  ▼
               └────── Active（generation + 1，撤销待删）
```

- **待删态**：`Delete` 将对象置为 `PendingDeletion`，记录 `DeleteAt = now + gracePeriod`，并调度一个绑定**当前代际**的清理任务。重复 `Delete` 是幂等 no-op，不延长宽限期。
- **撤销待删必须升代**：宽限期内 `AddRef` 撤销待删、恢复 `Active`，同时 `generation++`。因此旧清理任务触发时因代际不匹配而成为 no-op——**旧清理任务只对原代生效**。
- **真正删除后不可隐式重建**：清理任务在 `now >= DeleteAt` 时硬删除对象并清空引用账。此后 `AddRef` 返回 `ErrObjectDeleted`，绝不隐式重建；只有显式 `Create` 才能开启新一代（generation 继续递增）。
- **边界语义**：`now == DeleteAt` 时清理胜出（到期即删）；`now < DeleteAt` 时撤销胜出。两者在同一把互斥锁下串行化，并发下结果必居其一且可验证（见 `TestConcurrentRevokeVsCleanup`，需 `-race`）。

## 虚拟时钟与引用账核对

- `VirtualClock.Advance(d)` 同步按截止时间顺序触发定时器，回调执行期间 `Now()` 报告该定时器的截止时间，保证清理任务看到的时刻不早于自己的截止点。
- `VirtualClock.Set(t)` 直接跳时**不触发**定时器，用于模拟重启丢任务；之后调用 `Store.Reconcile()` 扫描引用账，硬删除所有已过宽限期的待删对象，返回被清除的 id 列表。
- 生产环境用 `NewRealClock()`。

## 使用

```go
clock := objstore.NewVirtualClock(time.Now())
store := objstore.NewStore(clock, 10*time.Second)

store.Create("obj-1")
store.Delete("obj-1")          // 进入待删态
store.AddRef("obj-1", "r-1")   // 撤销待删，generation 1 -> 2
clock.Advance(10 * time.Second) // 旧清理任务触发但为 no-op
```

## 测试

```sh
go test -race ./...
```
