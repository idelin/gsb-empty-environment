# worksteal

Go 工作窃取（work-stealing）调度器，仅使用标准库同步原语
（`sync.Mutex` / `sync.Cond` / `sync/atomic` / channel）。

## 设计

- 每个 `Worker` 持有一个本地双端队列（`deque.go`）：属主从**尾部** LIFO 弹出，
  空闲者从其他队列**头部** FIFO 窃取。`pop` 与 `steal` 由同一把互斥锁串行化，
  且都是“取出即移除”，因此同一任务不可能被双跑。
- 任务通过 `Scheduler.Submit`（外部，轮询分发）或 `Worker.Submit`
  （任务内派生子任务，进入本 worker 队尾）提交。持续派生子任务的 worker
  会把任务积压在队列头部，空闲者自然窃取分担。
- 任务至多执行一次；任务返回 error 或 panic 都计为失败，调度器继续运转。
- 停止与守恒：`pending` 计数与 `closed` 标志在同一把锁下维护，
  `Close` 先置 `closed`（此后 `Submit` 返回 `ErrClosed`），再用条件变量等待
  `pending == 0`（含关闭期间仍在运行的任务所派生的子任务），最后停掉 worker。
  不存在 WaitGroup “归零后再 Add” 的竞态。`Close` 返回后守恒成立：
  `Submitted == Completed + Failed`。

## 运行

```sh
go run ./cmd/demo     # 样例：根任务派生 40 个带注入延时/失败的子任务
go test -race ./...   # 竞态检测下运行全部测试
```

## 测试覆盖（scheduler_test.go）

- `TestEveryTaskRunsOnce`：1000 个任务各恰好执行一次。
- `TestContinuousSpawnerIsShared`：单 worker 持续派生 500 个子任务，
  断言被多个 worker 分担且 `Stolen > 0`。
- `TestStealVsLocalPopNoDoubleRun`：生产者持续投喂、8 个 worker 竞争
  本地弹出与窃取，执行总数严格等于提交数。
- `TestFailuresCounted`：注入 error 与 panic 计入 `Failed`，调度器不停摆。
- `TestTimingInjection`：注入随机延时的任务在 `Close` 返回前全部完成。
- `TestSubmitAfterClose` / `TestResubmitRaceOnClose` /
  `TestCloseConservation`：关闭后再提交被拒；关闭与“任务再提交自身”
  及多 goroutine 并发提交竞争时，被接纳的任务恰好执行一次，
  且 `Submitted == Completed + Failed` 守恒。

## 实测结果

```
$ go test -race -count=1 -v ./...
--- PASS: TestEveryTaskRunsOnce (0.01s)
--- PASS: TestContinuousSpawnerIsShared (0.10s)   # workers participated: 8, stolen: 436
--- PASS: TestStealVsLocalPopNoDoubleRun (0.05s)
--- PASS: TestFailuresCounted (0.00s)
--- PASS: TestTimingInjection (0.15s)
--- PASS: TestSubmitAfterClose (0.00s)
--- PASS: TestResubmitRaceOnClose (0.10s)
--- PASS: TestCloseConservation (0.01s)           # conserved with 840 submitted tasks
ok      worksteal

$ go run ./cmd/demo
submitted=41 completed=35 failed=6 stolen=30
conserved=true (submitted == completed+failed)
```
