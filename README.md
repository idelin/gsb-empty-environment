# scheduler — 固定频率任务调度器

仅使用 Go 标准库。计划时刻按基准时刻（anchor）计算：第 k 轮计划时间为
`Anchor + k*Period`，与上一轮结束时间无关，因此不会因任务耗时而漂移。

## 设计

- `Clock` 接口（`Now` / `WaitUntil`）：时间由外部注入，测试使用手动推进的
  假时钟，生产使用 `RealClock`（基于 `time.Timer`，取消时 `timer.Stop()` 释放）。
- `MissPolicy` 处理执行超时（一轮运行超过周期）：
  - `SkipMissed`：跳过错过的轮次，下一轮为锚定计划上第一个未来轮次。
  - `CoalesceMissed`：错过的轮次合并为一次立即补跑（携带最新错过轮次的 ID），
    随后回到锚定计划。
- 时钟回拨：调度器维护已执行轮次的高水位，下一轮 ID 取
  `max(计算值, lastExecuted+1)`，回拨后轮次 ID 单调递增、绝不重复。
- 取消：`Run(ctx)` 在 `ctx` 取消时返回 `ctx.Err()`，并释放等待中的计时器。
- 任务函数与失败由调用方注入：`Task func(ctx, round) error`，失败经
  `OnError` 回调上报，调度继续。

## 关键行为（周期 10、一次运行 25）

运行 0 在 t=0 开始、t=25 结束，错过 t=10、t=20 两轮：

- `SkipMissed`：下一轮为第 3 轮，t=30（锚定计划），而非结束时间 +10（t=35）。
- `CoalesceMissed`：先在 t=25 立即补跑一次合并轮（第 2 轮），再于 t=30 跑第 3 轮。

## 运行

```sh
go test -race -v ./...
go run ./cmd/demo
```

## 运行样例（go run ./cmd/demo）

周期 200ms，第 1 轮超时 450ms，第 2、3 轮被跳过，第 4 轮仍按锚点 +800ms 触发：

```
round 0 started at +0s (scheduled +0s)
round 1 started at +202ms (scheduled +200ms)
round 4 started at +801ms (scheduled +800ms)
round 5 started at +1.003s (scheduled +1s)
round 6 started at +1.202s (scheduled +1.2s)
scheduler stopped: context deadline exceeded
```

## 测试结果（go test -race -v ./...）

```
=== RUN   TestSkipPolicyAfterOverrun
--- PASS: TestSkipPolicyAfterOverrun (0.05s)
=== RUN   TestCoalescePolicyAfterOverrun
--- PASS: TestCoalescePolicyAfterOverrun (0.00s)
=== RUN   TestClockRewindDoesNotRepeatRounds
--- PASS: TestClockRewindDoesNotRepeatRounds (0.05s)
=== RUN   TestCancelReleasesWaiter
--- PASS: TestCancelReleasesWaiter (0.00s)
=== RUN   TestTaskFailureContinuesSchedule
--- PASS: TestTaskFailureContinuesSchedule (0.00s)
=== RUN   TestRealClockSmoke
--- PASS: TestRealClockSmoke (0.04s)
PASS
ok  	scheduler	1.538s
```
