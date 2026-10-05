# scheduler — 下载器并发调度模块

运行时可调上限的并发调度器：降低额度不打断已运行任务，仅阻止超额启动；
额度为零时仍可取消与关闭；等待者按 FIFO 顺序获得额度。

## 用法

```go
s := scheduler.New(4) // 最多 4 个并发任务

// 方式一：手动获取/释放额度
slot, err := s.Acquire(ctx) // 阻塞直到获得额度、ctx 取消或调度器关闭
if err == nil {
    defer slot.Release()
    download(...)
}

// 方式二：托管执行
err := s.Run(ctx, func() { download(...) })

s.SetLimit(1) // 运行中调整上限；已有任务继续运行，新启动被抑制
s.Close()     // 关闭：唤醒所有排队者并返回 ErrClosed
```

## 语义

- `SetLimit` 调小：不强制停止已运行任务；`running >= limit` 期间不再发放新额度。
- `limit == 0`：`Acquire` 的 context 取消与 `Close` 均正常生效。
- 取消不占位：取消与发放竞争时，额度被归还给队列，不会泄漏。
- 公平性：排队者严格按 FIFO 顺序获得额度。

## 测试

```sh
go test -race ./...
```

覆盖：旧任务结束与额度调整的竞争（两种交错 + 并发搅动）、取消占位
（含取消与发放竞争）、额度恢复后的队列公平性、零额度下的取消与关闭、
以及多工作者压力测试。
