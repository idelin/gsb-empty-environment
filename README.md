# clockguard — 双时钟源偏差检测与租约熔断守护器

持续比对本地**单调时钟（Monotonic）**与**墙钟（Wall-Clock）**的推进步调。
一旦检测到人为修改系统时间、NTP 大幅阶跃或虚拟机时钟挂起造成的严重撕裂，
立即紧急熔断：**强制注销所有活跃租约**，并将自身**降级为安全模式**（拒绝发放新租约），
防止时钟混乱引发逻辑灾难。

## 工作原理

每次采样读取两个时钟源，计算自上次采样以来各自的推进量：

```
skew = |wallDelta - monoDelta|
```

- 正常情况下两者同步推进，`skew ≈ 0`；
- 系统时间被回拨 / NTP 阶跃：墙钟跳变而单调时钟不变，`skew` 巨大；
- VM 挂起：单调时钟停滞而墙钟（恢复后）跳变，`skew` 巨大。

当 `skew > threshold`（默认 5s）时立即熔断。熔断后守护器保持安全模式，
不再发放任何租约（`Acquire` 返回 `ErrSafeMode`）。

## 结构

- `clock.go` — `ClockSource` 抽象；`SystemClock`（生产）与 `FakeClock`（测试/演示，可独立推进两个时钟域）
- `guardian.go` — `Guardian`：偏差检测（`Check`）、后台监视循环（`Run`）、租约管理（`Acquire`/`Release`）、安全模式
- `guardian_test.go` — go test 测试
- `cmd/demo/main.go` — 运行样例

## 使用

```go
guardian := clockguard.NewGuardian(clockguard.NewSystemClock(),
    clockguard.WithThreshold(5*time.Second),
    clockguard.WithInterval(time.Second),
    clockguard.OnTrip(func(ev clockguard.TripEvent) { /* 告警/上报 */ }),
)
go guardian.Run(ctx)

lease, err := guardian.Acquire("worker-a") // 熔断后返回 ErrSafeMode
```

## 运行样例

```
$ go run ./cmd/demo
[1] 系统正常运行，发放 3 份租约...
  [acquire] lease-1 -> worker-a
  [acquire] lease-2 -> worker-b
  [acquire] lease-3 -> worker-c
[2] 监视中... 活跃租约 3 份，守护器状态正常
[3] 故障注入：系统时间被回拨 1 小时！
  [revoke] 强制注销租约 lease-1 (holder=worker-a)
  [revoke] 强制注销租约 lease-2 (holder=worker-b)
  [revoke] 强制注销租约 lease-3 (holder=worker-c)

>>> 熔断！原因: wall clock moved backwards (manual time change or NTP step)
>>> 墙钟推进 -1h0m0s，单调时钟推进 0s，撕裂 1h0m0s，强制注销 3 份租约

[4] 熔断后状态：tripped=true，活跃租约=0
[5] 安全模式生效，新租约被拒绝: clockguard: guardian tripped, safe mode engaged, leasing disabled
```

## 测试结果

```
$ go test ./... -v
=== RUN   TestBackwardOneHourJumpTripsGuardian   # 验收边界：回拨 1 小时 → 立即熔断
--- PASS: TestBackwardOneHourJumpTripsGuardian (0.00s)
=== RUN   TestForwardNTPStepTripsGuardian        # NTP 向前阶跃 30 分钟 → 熔断
--- PASS: TestForwardNTPStepTripsGuardian (0.00s)
=== RUN   TestMonotonicStallTripsGuardian        # VM 挂起，单调时钟停滞 → 熔断
--- PASS: TestMonotonicStallTripsGuardian (0.00s)
=== RUN   TestSmallJitterDoesNotTrip             # 阈值内正常抖动 → 不误判
--- PASS: TestSmallJitterDoesNotTrip (0.00s)
=== RUN   TestAcquireAndRelease                  # 租约正常申请/释放
--- PASS: TestAcquireAndRelease (0.00s)
=== RUN   TestRunLoopTripsAsynchronously         # 后台监视循环异步熔断
--- PASS: TestRunLoopTripsAsynchronously (0.05s)
PASS
ok  	clockguard	0.364s
```
