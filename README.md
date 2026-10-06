# aggregator

Go 写入聚合器：按数量阈值或显式屏障把写入刷成批次，每项提交返回独立回执。

## 语义

- `New(sink, maxBatch)`：积压满 `maxBatch` 项时自动刷批；`Sink.WriteBatch`
  返回成功写入的前缀长度 `n` 与错误。
- `Submit(data) <-chan error`：每项一个缓冲回执通道，恰好收到一次结果。
  短写（`n < len(batch)`）时前 `n` 项回执为 `nil`，余项收到批次错误；
  sink 未报错时余项收到 `ErrShortWrite`。
- `Barrier(ctx)`：刷掉当前积压并阻塞到完成；报告自上次 Barrier/Close
  以来累积的任何失败（含按数量自动刷出的失败批次），因此失败批次之后
  的屏障不会误报成功。返回后错误计数清零。
- `Close(ctx)`：停止接收新提交（之后 `Submit` 的回执立即为 `ErrClosed`），
  刷完剩余积压、等待在途刷批完成并结清所有回执；返回累积错误。
  重复关闭返回 `ErrClosed`。

## 测试

测试通过可编排的 `scriptSink`（每次 `WriteBatch` 经 channel 交接给测试
逐步放行）验证批界、短写部分成功与关闭争用，全程无真实等待：

```sh
go test ./... -race
```
