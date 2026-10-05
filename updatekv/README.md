# updatekv

在只提供「读版本 + 条件写入（CAS）」的存储上实现 read-modify-write 更新：

- **有界冲突重试**：`Run` 循环执行 读 → 计算 → 条件写，冲突时按 `MaxRetries` 有界重试，指数退避，预算耗尽返回 `ErrRetryExhausted`。
- **副作用恰好投递一次**：更新函数 `Func` 必须保持纯净——它可能因冲突被执行多次。只有胜出那次尝试产生的 `Effect` 会在写入成功后进入 `Outbox` 并投递；失败尝试的副作用直接丢弃。
- **投递失败可重试**：写入成功后先入队再投递；投递失败的记录保留在 `Outbox` 中（含尝试次数与最近错误），可用 `Flush` 重试，`Run` 返回 `*DeliveryError` 告知调用方。
- **冲突注入器**：`ConflictInjector` 包装任意 `Store`，让前 N 次 CAS 返回 `ErrConflict`，用于验证重试逻辑以及「失败尝试的副作用不会被提前执行」。

## 接口

```go
type Store interface {
    Read(ctx, key) (Record, error)                                        // 值 + 版本
    CompareAndSwap(ctx, key, value, expectedVersion) (newVersion, error)  // 版本不符返回 ErrConflict
}

type Func func(current []byte) (next []byte, effects []Effect, err error)

func Run(ctx, store, key, fn, Options{MaxRetries, Backoff, Sleep, Outbox}) (Result, error)
```

## 测试

```sh
go test ./...        # 单元 + 并发集成测试
go test -race ./...
```

关键用例：

- `TestFailedAttemptsNeverDeliverEffects`：注入 2 次冲突，断言只有第 3 次（胜出）尝试的副作用被投递，且恰好一次。
- `TestRetryBudgetExhaustedDeliversNothing`：冲突数超过预算，断言返回 `ErrRetryExhausted`、无投递、值不变。
- `TestDeliveryFailureKeepsRetryableRecord`：投递失败后记录保留，`Flush` 重试后恰好投递一次。
- `TestConcurrentUpdatesLoseNeitherWriteNorEffects`：用 rendezvous store 确定性制造并发写冲突，断言两次更新都生效、各投递一次。
