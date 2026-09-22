# aimd — 自适应并发限流器（加性增大 / 乘性减小）

仅使用 Go 标准库。限流器依据调用方显式上报的延迟样本与超时标记调整并发上限：

- 样本批次中**任一超时**：`limit = limit * backoff`（乘性减小，不低于 `minLimit`）
- 样本批次**全部成功**：`limit += 1`（加性增大，不超过 `maxLimit`）
- **空样本批次**：不调整，也不消耗防抖窗口
- **防抖**：距上次调整不足 `minInterval` 的更新被忽略，恢复阶段不会抖动
- **不强杀**：上限缩小时已运行任务正常结束，空出的槽位不补位，
  直到 `inflight < limit` 才放行新任务

时间通过 `Clock` 接口注入；测试使用 `ManualClock`，调整序列完全确定。

## 运行

```sh
go test -race -v ./...   # 竞态检测下的测试
go run ./cmd/demo        # 运行样例
```

## API

```go
limiter := aimd.NewLimiter(initial, min, max, backoff, minInterval, clock)
limiter.Acquire()                 // 阻塞直到有空闲槽位
defer limiter.Release()           // 任务结束归还槽位
limiter.Update([]aimd.Sample{     // 上报显式样本，触发 AIMD 调整
    {Latency: 30 * time.Millisecond},
    {Latency: 900 * time.Millisecond, Timeout: true},
})
```
