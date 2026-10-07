# bq — 可关闭的有界队列 + 线性化历史检查器

## queue 包

`queue.Queue[T]` 是面向后台处理器的有界 FIFO 队列：

- `Enqueue(ctx, v)` 满时阻塞，`Dequeue(ctx)` 空时阻塞，均支持 context 取消。
- **提交点裁决**：`Close` 与 `Enqueue` 竞争时，在锁内提交点决定胜负——
  已提交的项在关闭后仍可排空；未提交的入队返回 `ErrClosed`。
- **取消无效果**：取消等待不会吞掉元素，也不会泄漏容量。
- **终止语义**：关闭后排空所有已提交项，此后所有 `Dequeue` 返回 `ErrClosed`。

```go
q := queue.New[Job](64)
go func() {
    for {
        job, err := q.Dequeue(ctx)
        if errors.Is(err, queue.ErrClosed) { return } // 排空后终止
        process(job)
    }
}()
```

## lin 包

`lin.Check(capacity, ops)` 对小规模历史（≤ 62 个操作）做带回溯与记忆化的
搜索，寻找同时满足以下条件的串行序：

- 实时序：操作 A 返回早于操作 B 调用（`A.Ret < B.Call`），则 A 排在 B 前；
- 顺序规约：有界容量、FIFO、关闭/提交点、取消无效果、排空后终止。

`lin.Recorder` 用单一原子时钟记录并发历史的调用/返回时间戳，
可直接配合 `queue` 的压力测试使用（见 `queue/queue_test.go`）。

## 测试

```sh
go test ./... -race
```
