# worksteal

Go 工作窃取（work-stealing）调度器，仅使用标准库同步原语。

## 设计

- **每工作者一个双端队列**（`deque.go`）：属主工作者从**底部**压入/弹出（LIFO），
  空闲者从**顶部**窃取（FIFO）。`popBottom` 与 `stealTop` 共用同一把互斥锁，
  因此窃取与本地弹出不可能把同一任务交出两次（**最多执行一次**）。
- **未完成计数**（`scheduler.go`）：`pending sync.WaitGroup` 记录未完成任务数。
  `enqueue` 在任务入队**之前**先 `pending.Add(1)`，计数不会在队列非空时归零。
  任务通过 `Scope.Submit` 派生子任务时父任务仍在计数内，`Add` 不会与归零竞态——
  这就是关闭时再提交安全的根源（规则：子任务必须在父任务返回前提交）。
- **失败注入**：任务返回非 nil error 即计为失败，不重试（at-most-once）。
- **关闭守恒**：`Shutdown` 先 `Wait` 待计数归零，再停 worker；
  此后恒有 `submitted == completed + failed`（`Stats.Conserved`）。

## 运行样例

```console
$ go run ./cmd/demo
workers=4 elapsed=232ms
submitted=1093 completed=983 failed=110 conserved=true
```

## 测试结果（`go test -race -count=1 -v ./...`）

```console
=== RUN   TestAtMostOnceUnderSteal          # 5000 任务，窃取/本地弹出竞争下每个恰好执行 1 次
--- PASS: TestAtMostOnceUnderSteal (0.06s)
=== RUN   TestSpawningWorkerGetsHelp        # 单根任务持续派生 2000 子任务，4/4 工作者分担
--- PASS: TestSpawningWorkerGetsHelp (0.03s)
=== RUN   TestRecursiveTreeSpawn            # 递归任务树全部恰好完成一次
--- PASS: TestRecursiveTreeSpawn (0.04s)
=== RUN   TestFailuresCountedNotRetried     # 注入失败计数准确、不重试
--- PASS: TestFailuresCountedNotRetried (0.01s)
=== RUN   TestConcurrentExternalSubmitters  # 8 个外部提交者并发提交
--- PASS: TestConcurrentExternalSubmitters (0.04s)
=== RUN   TestResubmissionDuringShutdownRace # 关闭竞态下持续再提交，无死锁且守恒
--- PASS: TestResubmissionDuringShutdownRace (0.01s)
=== RUN   TestTimingInjection               # 注入延时，Wait 不提前返回
--- PASS: TestTimingInjection (0.05s)
=== RUN   TestShutdownIdempotent
--- PASS: TestShutdownIdempotent (0.00s)
=== RUN   Example
--- PASS: Example (0.00s)
PASS
ok      worksteal
```

## API 速览

```go
s := worksteal.New(4)   // 4 个工作者
s.Start()
s.Submit(func(sc *worksteal.Scope) error {
    sc.Submit(child)    // 派生子任务到本地队列，可被窃取分担
    return nil          // 返回 error 记为失败
})
s.Shutdown()            // 等待全部完成后停止；幂等
st := s.Stats()         // st.Conserved() == true
```
