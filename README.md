# deter — 确定性任务运行时：记录与回放

`deter` 在单个虚拟时钟上逐步执行任务，并把一次运行完整记录为事件日志；
回放模式用同一份日志驱动调度器，逐步校验任务行为，发现分歧立即报告位置。
运行时**从不读取真实时钟**，因此墙钟时间无法掩盖不同的运行轨迹。

## 记录什么

每个事件（`runtime.go` 中的 `Event`）对应一个调度步：

| 字段 | 含义 |
| --- | --- |
| `Task` | 调度选择：这一步运行哪个任务 |
| `At` | 时钟推进：事件发生时虚拟时钟的值 |
| `Op`/`Arg` | 任务发出的请求（`spawn` / `sleep` / `call` / `done`） |
| `Result`/`Err` | 外部结果：`call` 的返回值或注入的失败 |

## 注入点（均由测试/调用方提供）

- **任务函数**：`TaskFunc`，经 `Run` / `Env.Spawn` 注入。
- **时间**：只有 `Env.Sleep` 推进的虚拟时钟（`Runtime.Now` / `Env.Now`），无任何真实时间调用。
- **失败**：`CallHandler` 在记录时产生外部结果或失败；回放时不再调用 handler，任务收到的是记录中的结果/失败。

## 确定性保证

- 同一任务集记录两次，事件日志逐字节相同（FIFO 就绪队列 + 单任务轮转执行）。
- 同一记录回放得到同一结果：调度选择、时钟、外部结果全部来自日志。
- 任务**多发一次请求**：回放立即在该步报 `*Divergence`（含步号、期望与实际）。
- **记录尾缺失**（截断的日志）：任务还有请求而日志已尽，报分歧，绝不宣称成功。
- 记录比执行长（多出尾部事件）：同样报分歧。
- 篡改任一事件的参数或调度目标：在恰好那一步报分歧。

## 同步原语

调度器与任务 goroutine 之间只用无缓冲 channel 配对通信：任意时刻只有一个任务
goroutine 被唤醒，channel 的 happens-before 边保证共享状态无数据竞争。
全部测试在 `go test -race` 下通过。

## 运行样例

```
$ go run ./cmd/demo
recorded 15 events, virtual clock stopped at 3s
  step  0: task=0 op=spawn arg="worker-a" at=0s
  step  1: task=1 op=sleep arg="1s" at=0s
  step  2: task=0 op=spawn arg="worker-b" at=0s
  step  3: task=2 op=sleep arg="1s" at=0s
  step  4: task=0 op=sleep arg="500ms" at=0s
  step  5: task=0 op=call arg="fetch root" at=500ms result="data-root" err=false
  step  6: task=0 op=done arg="" at=500ms
  step  7: task=1 op=call arg="fetch a" at=1s result="data-a" err=false
  step  8: task=2 op=call arg="fetch b" at=1s result="injected failure for \"b\"" err=true
  step  9: task=1 op=sleep arg="2s" at=1s
  step 10: task=2 op=sleep arg="2s" at=1s
  step 11: task=1 op=call arg="fetch a" at=3s result="data-a" err=false
  step 12: task=2 op=call arg="fetch b" at=3s result="injected failure for \"b\"" err=true
  step 13: task=1 op=done arg="" at=3s
  step 14: task=2 op=done arg="" at=3s
task output:
  root: data-root
  a: data-a
  b: error: injected failure for "b"
  a: data-a
  b: error: injected failure for "b"

replay: OK, identical output: true
extra request: replay divergence at step 1: expected task 1 to issue sleep "1s", got task 1 (worker-a) issued call "ping a"
truncated recording: replay divergence at step 12: expected end of recording, got task 2 (worker-b) issued call "fetch b"
```

## 测试结果

```
$ go test -race -count=1 -v ./...
=== RUN   TestRecordIsDeterministic
--- PASS: TestRecordIsDeterministic (0.00s)
=== RUN   TestReplayMatchesRecord
--- PASS: TestReplayMatchesRecord (0.00s)
=== RUN   TestExtraRequestDiverges
--- PASS: TestExtraRequestDiverges (0.00s)
=== RUN   TestTruncatedRecordingFails
--- PASS: TestTruncatedRecordingFails (0.00s)
=== RUN   TestUnconsumedTailFails
--- PASS: TestUnconsumedTailFails (0.00s)
=== RUN   TestTamperedEventDiverges
--- PASS: TestTamperedEventDiverges (0.00s)
=== RUN   TestScheduleDivergence
--- PASS: TestScheduleDivergence (0.00s)
=== RUN   TestVirtualClockNotRealTime
--- PASS: TestVirtualClockNotRealTime (0.00s)
=== RUN   TestFailureInjectionRecorded
--- PASS: TestFailureInjectionRecorded (0.00s)
PASS
ok  	example.com/deter	1.345s
```

其中 `TestVirtualClockNotRealTime` 让任务睡眠 1000 小时并瞬间完成，
证明时钟推进完全来自虚拟时钟而非真实时间。

## 文件

- `runtime.go` — 调度器、事件日志、记录/回放、`Divergence`。
- `runtime_test.go` — 注入任务函数/时间/失败的全部测试。
- `cmd/demo/main.go` — 记录 → 回放 → 分歧演示。
