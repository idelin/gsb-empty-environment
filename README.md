# batcher

批量写入库：请求在队列中累积，满足任一条件即刷批——

- **凑满数量**：队列长度达到 `maxSize`；
- **最老请求到期**：队首请求已等待 `maxDelay`。

## 语义保证

- **新请求不延后旧请求**：老化定时器只在空队列迎来首个请求时启动，
  之后的入队绝不重置它，因此任何请求的等待时间都不超过 `maxDelay`。
- **取消只移除未提交项**：`Request.Cancel` 仅在请求尚未被划入某一批时
  生效（返回 `true` 并投递 `ErrCanceled`）；已提交的请求不受取消影响，
  仍会收到本批的写入结果。若被取消的是队首，定时器会按新队首的截止
  时间重新武装。
- **批失败分别回传**：写函数返回的错误会逐一投递给该批每个成员的
  `Result()` 通道，成员之间互不影响。
- **同刻稳定划分**：所有队列变更都在同一把互斥锁下完成。刷新与入队
  发生在同一时刻时被串行化——请求要么进入正在刷新的批次，要么进入
  下一批，不会重复写入，也不会丢失。

## 虚拟时间模型

`Clock` 接口抽象时间来源。生产用 `RealClock`；测试用 `ManualClock`，
其 `Advance(d)` 以确定顺序同步触发到期定时器，使“刷新与入队同刻”
这类边界场景可以精确复现（见 `batcher_test.go` 中的
`TestSameInstant*` 系列）。

## 用法

```go
clock := batcher.RealClock{}
b := batcher.NewBatcher(100, 50*time.Millisecond, clock, func(batch []any) error {
    return db.Insert(batch) // 整批落库；出错则分别回传给每个成员
})

req := b.Enqueue(record)
err := <-req.Result() // nil / 批写入错误 / batcher.ErrCanceled
```

## 测试

```sh
go test -race ./...
```
