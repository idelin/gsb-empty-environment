# detrun — 确定性调度记录与回放

`detrun` 是一个纯标准库的 Go 运行时，用合作式单线程调度器执行任务，
把**调度选择、虚拟时钟推进、外部调用结果**全部记录为事件日志；回放模式
逐事件比对同一份日志，验证任务行为是否一致。任何分歧都会在**该事件位置**
立即报错，虚拟时钟不接触真实时间，不同运行轨迹无法被墙钟时间掩盖。

## 设计

- `Runtime`（`runtime.go`）：合作式调度器。任务通过 channel 让出执行权，
  同一时刻只有一个任务在运行，调度选择确定（可运行任务中取 id 最小者）。
- 虚拟时钟：所有任务睡眠时直接跳到最近的唤醒点，记录 `clock` 事件；
  `Task.Sleep` 不消耗真实时间。
- 事件日志（`event.go`）：`schedule` / `clock` / `external` 三类事件，
  外部调用的响应与失败（错误消息）一并记录。
- 记录模式 `NewRecord(ext)`：外部函数由调用方（测试）注入，结果落日志。
- 回放模式 `NewReplay(log)`：不接触外部函数，响应与失败从日志注入；
  每个产生的事件与日志逐一比对。
- 分歧检测（`DivergenceError`，带 `Pos`）：
  - 事件不匹配 → 报该位置与双方内容；
  - 任务多发请求（日志耗尽）→ 报 `expected end of recording`；
  - 运行提前结束（记录尾未消费）→ 报 `N unconsumed event(s)`，绝不宣称成功。

## 使用

```go
// 记录：任务函数、外部函数、失败都由调用方注入
rec := detrun.NewRecord(externalFunc)
rec.Go(taskA)
rec.Go(taskB)
_ = rec.Run()
log := rec.Log()

// 回放：同一份日志，无需外部函数，结果与失败从日志注入
rep := detrun.NewReplay(log)
rep.Go(taskA)
rep.Go(taskB)
if err := rep.Run(); err != nil {
    // *detrun.DivergenceError，含分歧位置
}
```

任务内使用 `t.Sleep(d)`（虚拟时钟）、`t.Call(req)`（外部请求）、`t.Now()`。

## 运行样例

```
$ go run ./cmd/demo
== record ==
  task B: B1 -> "resp:B1" err=<nil> (virtual now=0s)
  task A: A1 -> "resp:A1" err=<nil> (virtual now=5ms)
  task A: A2 -> "resp:A2" err=<nil> (virtual now=10ms)
  task B: B2 -> "" err=injected failure: B2 rejected (virtual now=20ms)
recorded 16 events, virtual clock ended at 20ms
  #00 schedule(task=0, now=0s)
  #01 schedule(task=1, now=0s)
  #02 external(task=1, req="B1", now=0s)
  ...（共 16 个事件）
== replay (same recording, no external func) ==
  task B: B1 -> "resp:B1" err=<nil> (virtual now=0s)
  task A: A1 -> "resp:A1" err=<nil> (virtual now=5ms)
  task A: A2 -> "resp:A2" err=<nil> (virtual now=10ms)
  task B: B2 -> "" err=injected failure: B2 rejected (virtual now=20ms)
replay OK: identical trajectory and results
== replay with a misbehaving task (one extra request) ==
  divergence reported: replay diverged at event 16: expected end of recording, got external(task=1, req="B3", now=20ms)
```

## 测试

```
$ go test -race -count=1 -v ./...
=== RUN   TestRecordThenReplaySameResults        --- PASS   # 同记录回放得到同结果
=== RUN   TestRecordIsDeterministic              --- PASS   # 两次记录逐字节一致
=== RUN   TestReplayDetectsExtraRequest          --- PASS   # 多发请求在该步报分歧
=== RUN   TestReplayDetectsRequestPastEndOfRecording --- PASS # 记录耗尽立即报错
=== RUN   TestReplayFailsOnMissingTail           --- PASS   # 记录尾缺失不算成功
=== RUN   TestReplayDetectsClockDivergence       --- PASS   # 时钟轨迹分歧报位置
=== RUN   TestReplayDetectsScheduleDivergence    --- PASS   # 调度选择分歧报位置
=== RUN   TestReplayInjectsRecordedFailure       --- PASS   # 失败从日志注入
=== RUN   TestReplayNeverCallsExternal           --- PASS   # 回放不触外部函数
=== RUN   TestVirtualClockDoesNotUseWallTime     --- PASS   # 10s 虚拟睡眠零墙钟
=== RUN   TestConcurrentRecordAndReplay          --- PASS   # 并发回放（race 检测）
PASS
ok  	detrun	1.408s
```
