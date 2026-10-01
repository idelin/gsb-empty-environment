# alertseg

将频繁的告警状态变化合并为**事件段**（segment），并按策略产生通知：支持**抑制窗口**、**恢复通知**与**重复通知间隔**。纯 Go 标准库实现，无外部依赖；不接监控平台，不做展示看板。

## 语义

- 原始遥测事件（`ok` / `firing`）被折叠为事件段：连续相同状态合并为一段，重复观测被吞掉。
- 进入 `firing` 立即通知（`FIRING`）；故障持续期间每 `repeat_interval` 重报（`REPEAT`）；恢复时通知（`RESOLVED`）。
- **抑制期间保留真实状态**，只是不发送。解除抑制时：
  - 故障仍在持续 → 立即补发（从未通知过发 `FIRING`，否则发 `REPEAT`），`fault_since` 指向真实故障起点，重复计时从解除点重新开始；
  - 故障在抑制中已恢复 → **不补发旧故障**，也不补发恢复，静默关闭。
- 重复通知恰好在恢复同一时刻到期时不发送（状态变化优先于定时器）。
- 抑制窗口为半开区间 `[start, end)`。
- **时间完全可控**：引擎不读墙钟，由 `Apply(event)` 与 `AdvanceTo(t)` 驱动虚拟时间，可确定性测试与回放。

## 结构

- `alertseg/engine.go` — 分段与通知策略引擎（虚拟时间驱动）
- `alertseg/fixture.go` — JSON 夹具加载与回放
- `cmd/alertseg/main.go` — 命令行回放器
- `fixtures/` — 本地合成遥测夹具（抖动合并 / 抑制中持续故障 / 抑制中恢复）

## 运行

```console
$ go run ./cmd/alertseg fixtures/flapping.json fixtures/suppressed_persistent.json fixtures/suppressed_recovery.json
=== flapping sensor merged into segments, repeats every 30m (fixtures/flapping.json)
segments:
  firing 2026-01-01T00:00:00Z -> 2026-01-01T00:01:00Z
  ok     2026-01-01T00:01:00Z -> 2026-01-01T00:02:00Z
  firing 2026-01-01T00:02:00Z -> 2026-01-01T02:00:00Z
  ok     2026-01-01T02:00:00Z -> ongoing
notifications:
  2026-01-01T00:00:00Z FIRING   fault_since=2026-01-01T00:00:00Z
  2026-01-01T00:01:00Z RESOLVED fault_since=2026-01-01T00:00:00Z
  2026-01-01T00:02:00Z FIRING   fault_since=2026-01-01T00:02:00Z
  2026-01-01T00:32:00Z REPEAT   fault_since=2026-01-01T00:02:00Z
  2026-01-01T01:02:00Z REPEAT   fault_since=2026-01-01T00:02:00Z
  2026-01-01T01:32:00Z REPEAT   fault_since=2026-01-01T00:02:00Z
  2026-01-01T02:00:00Z RESOLVED fault_since=2026-01-01T00:02:00Z

=== fault starts inside suppression and outlives it: notified at lift (fixtures/suppressed_persistent.json)
segments:
  firing 2026-01-01T00:15:00Z -> 2026-01-01T02:00:00Z
  ok     2026-01-01T02:00:00Z -> ongoing
notifications:
  2026-01-01T01:00:00Z FIRING   fault_since=2026-01-01T00:15:00Z
  2026-01-01T01:30:00Z REPEAT   fault_since=2026-01-01T00:15:00Z
  2026-01-01T02:00:00Z RESOLVED fault_since=2026-01-01T00:15:00Z

=== fault starts and recovers inside suppression: nothing is sent (fixtures/suppressed_recovery.json)
segments:
  firing 2026-01-01T00:15:00Z -> 2026-01-01T00:45:00Z
  ok     2026-01-01T00:45:00Z -> ongoing
notifications:
  (none)
```

## 测试

```console
$ go test -v ./...
=== RUN   TestFiringRepeatResolve
--- PASS: TestFiringRepeatResolve (0.00s)
=== RUN   TestSuppressedPersistentFaultNotifiedAfterLift
--- PASS: TestSuppressedPersistentFaultNotifiedAfterLift (0.00s)
=== RUN   TestRecoveryDuringSuppressionNoBackfill
--- PASS: TestRecoveryDuringSuppressionNoBackfill (0.00s)
=== RUN   TestRepeatsPausedDuringSuppression
--- PASS: TestRepeatsPausedDuringSuppression (0.00s)
=== RUN   TestSuppressionBoundaries
--- PASS: TestSuppressionBoundaries (0.00s)
=== RUN   TestFlappingMergedIntoSegments
--- PASS: TestFlappingMergedIntoSegments (0.00s)
=== RUN   TestFixtures
--- PASS: TestFixtures (0.01s)
PASS
ok  	alertseg/alertseg
```

覆盖：基本触发/重复/恢复、抑制中持续故障解除后补发、抑制中恢复不补发、抑制期间重复通知暂停并在解除后恢复、抑制窗口边界（半开区间）、抖动合并为事件段、三个夹具的回放金样。
