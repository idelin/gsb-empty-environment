# 加权租户公平队列调度器（Go，仅标准库）

按**租户权重**分配执行额度：每个租户一个 FIFO 队列，单条调度路径以
“游标式加权轮询（deficit round robin 的无显式轮次变体）”决定下一个执行谁；
同时用一个**全局容量信号量**限制正在运行的任务总数。调度器本身不持有任务
时钟，任务函数、耗时、失败/取消都由调用方注入。

## 需求与实现对应

| 需求 | 实现 |
| --- | --- |
| 按租户权重分配额度 | `popNextLocked`：游标到达有等待任务的租户时，把其 `deficit` 按**当前权重**补足；额度未用完连续服务，用完游标前进（`scheduler.go`） |
| 各租户 FIFO | 租户内队列 `queue []*entry`；另用每租户“启动闸门”链保证同租户**启动顺序**严格 FIFO，而一旦前序任务启动即放行后继，同租户仍可并发 |
| 全局并发限制 | 容量为 `Concurrency` 的信号量 `slots`；**先取槽、再出队**，保证运行中任务永不超过上限，单个重租户也不会堆积 goroutine |
| 动态权重只影响后续选择 | `UpdateWeight` 只改 `weight`；已发放的 `deficit` 不变，新权重仅作用于之后的补额 |
| 取消等待任务不消耗额度 | 等待中的任务在锁内直接从租户队列移除并标记完成，**从不经过 dispatcher、不取槽、不执行**；队列清空时丢弃该租户剩余 deficit（空闲不攒额度） |
| 空闲租户不浪费槽 | 调度器是 work-conserving：有排队就立即选可服务租户；空租户 deficit 清零，空闲时间不计入任何人 |
| 关闭后不接收新任务 | `Close` 后 `Submit`/`UpdateWeight` 返回 `ErrClosed`；已排队与运行中任务继续排空（drain），`Wait` 等全部结束 |
| 标准同步原语 | `sync.Mutex`、`sync.Cond`、`sync.WaitGroup`、channel；无第三方依赖 |
| 竞态检测 | 全部测试在 `go test -race` 下通过 |

任务以 `func(ctx context.Context) error` 注入；运行中被 `Cancel` 时通过
`context` 协作式取消。任务返回的 error 计入失败；panic 会被 recover 并包装为
`TaskPanicError`，不会拖垮调度器。

## API 速览

```go
s, _ := wfq.New(wfq.Config{
    Concurrency: 4,
    Tenants:     map[string]int{"a": 1, "b": 2}, // 权重必须 > 0
})

h, _ := s.Submit("a", func(ctx context.Context) error {
    <-ctx.Done() // 支持协作式取消
    return nil
})
<-h.Done()
_ = h.Err()              // nil / 任务 error / wfq.ErrTaskCancelled

s.UpdateWeight("b", 5)   // 只影响之后的选择
s.Cancel(h)              // 等待任务直接移除；运行中任务取消 ctx
s.Shutdown()             // Close + 等待排空
s.Stats()                // 提交/成功/失败/取消/在飞/峰值在飞/排队
```

## 目录

- `scheduler.go`：调度器实现。
- `scheduler_test.go`：全部测试（`go test`），覆盖加权份额、FIFO、全局并发、
  空闲租户不占槽、等待/运行中取消、失败与 panic、动态权重、关闭排空、参数
  校验，以及一个并发压测用于竞态检测。
- `cmd/demo/main.go`：可运行样例（三段独立场景）。
- `demo_output.txt`、`test_output.txt`：实际运行输出存档。

## 运行

```bash
go test -race -count=1 ./...
go run -race ./cmd/demo
```

## 一次实际运行样例（节选自 `demo_output.txt`）

```
scenario 1: saturated tenants, weights tier-a:b:c = 1:2:3
  served: tier-a=3333 tier-b=6666 tier-c=10000
  shares: tier-a=0.17 tier-b=0.33 tier-c=0.50 (want 0.17 / 0.33 / 0.50)
  peak concurrent running tasks=4 (limit=4)

scenario 2: same scheduler, weights tier-a:b change 1:1 -> 1:2
>>> tier-b weight changed 1 -> 2 after window 1 drained
  window 1 (weights 1:1): tier-a=3000 tier-b=3000
  window 2 (weights 1:2): tier-a=3000 tier-b=6000, shares 0.33 / 0.67 (want 0.33 / 0.67)

scenario 3: cancelling a waiting task consumes no slot
>>> all slots busy; cancelled a waiting tier-a task
>>> freed slot went to the queued tier-b task, not the cancelled one
  stats: submitted=6 succeeded=5 failed=0 cancelled=1 peak=4
scheduler shut down cleanly; no new tasks accepted after Close
```

## 设计说明（为什么是加权轮询而不是令牌桶）

- 持续饱和时长期服务次数天然趋近权重比，无需随时间累计小数额度，调度器也因此
  与时钟解耦（任务耗时完全由测试/调用方注入）。
- “先取槽再出队”使**实际运行**数严格不超过 `Concurrency`；额度的补入只发生在
  游标服务到非空租户时，取消发生在出队之前，因此等待任务被取消不可能消耗额度。
- 同租户启动闸门链保证 FIFO 启动顺序，同时仅在任务“开始”瞬间传递令牌，所以同
  租户任务仍可重叠执行，不被强制串行。
