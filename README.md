# genfence — 代际围栏任务执行器

仅用 Go 标准库同步原语（`sync.Mutex` / `sync.WaitGroup`）实现的代际（generation）围栏：

- **读取当前代际**：`Manager.Generation()`；任务通过 `Manager.Start` 加入当前代际。
- **切换代际**：`Manager.Switch()` 封存旧代际缓存并开启新代际，返回新代际号。
- **线性化**：`publish` 与 `Switch` 共用同一把互斥锁，二者构成全序——旧任务计算完成与切换同时发生时，结果要么先发布进它自己的代际缓存，要么被拒绝，不会静默丢失。
- **旧结果隔离**：发布前在锁内做围栏检查（`gen != m.gen` 即拒绝），旧结果永远不会写入新代际缓存。
- **清理不被阻断**：每个任务（无论发布、拒绝还是失败）都在自己的 goroutine 里恰好执行一次 `Cleanup`；`Switch` 与新任务都不等待旧任务清理。`WaitGeneration(gen)` 可等待某代际全部清理完成。
- **测试注入**：任务函数、时序（channel 阻塞）、失败（注入 error）全部由 `Task{Compute, Cleanup}` 注入。

## 运行样例

```console
$ go run ./cmd/example
current generation: 0
switching generation...
current generation: 1
  cleanup gen=1 key=report  published=true  rejected=false err=<nil>
  cleanup gen=0 key=report  published=false rejected=true  err=<nil>
generation 0 cache: map[]
generation 1 cache: map[report:q4-report]
```

新代际任务（gen=1）先于旧任务清理完成——新任务不等待旧清理；旧任务的 `q3-report` 被拒绝，未写入任何缓存。

## 测试结果（`go test -race -v ./...`）

```console
=== RUN   TestPublishBeforeSwitchIsKept
--- PASS: TestPublishBeforeSwitchIsKept (0.00s)
=== RUN   TestPublishRacingSwitchIsRejected
--- PASS: TestPublishRacingSwitchIsRejected (0.00s)
=== RUN   TestLinearizabilityInvariant
--- PASS: TestLinearizabilityInvariant (0.06s)
=== RUN   TestNewTasksDoNotWaitForOldCleanup
--- PASS: TestNewTasksDoNotWaitForOldCleanup (0.00s)
=== RUN   TestComputeFailureSkipsPublish
--- PASS: TestComputeFailureSkipsPublish (0.00s)
=== RUN   TestWaitGenerationBlocksUntilCleanup
--- PASS: TestWaitGenerationBlocksUntilCleanup (0.05s)
=== RUN   TestCleanupRunsExactlyOnce
--- PASS: TestCleanupRunsExactlyOnce (0.00s)
PASS
ok  	genfence	1.501s
```

竞态检测器（`-race`）下全部通过。
