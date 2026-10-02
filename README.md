# clockguard — 双时钟源偏差检测与租约熔断守护器

持续比对本地单调时钟（Monotonic）与墙钟（Wall-Clock）的推进步调。
一旦检测到人为修改系统时间、NTP 大幅阶跃或虚拟机时钟挂起导致的时钟撕裂，
立即紧急熔断所有活跃租约，并将自身降级为安全模式（拒绝发放新租约）。

## 结构

- `clock.go` — 双时钟源抽象：`RealClock`（生产）/ `FakeClock`（测试可操控）
- `lease.go` — `LeaseManager`：租约发放、`RevokeAll` 紧急熔断、安全模式
- `guard.go` — `Guardian`：周期比对 `skew = wallDelta - monoDelta`，超阈值即熔断
- `guard_test.go` — go test 验收测试
- `cmd/demo` — 运行样例（模拟回拨系统时间 1 小时）

## 使用

```go
clock := clockguard.NewRealClock()
manager := clockguard.NewLeaseManager(clock)
guard := clockguard.NewGuardian(clock, manager, 5*time.Second, 100*time.Millisecond)
guard.OnTrip(func(ev clockguard.TripEvent) { log.Printf("熔断: %s", ev.Reason) })
guard.Start()
defer guard.Stop()

lease, err := manager.Acquire("my-svc", time.Minute) // 熔断后返回 ErrSafeMode
```

## 运行

```sh
go test ./... -v   # 验收测试
go run ./cmd/demo  # 运行样例
```
