# alertseg

将频繁的告警状态变化合并为**事件段（Segment）**，并在此基础上决定何时发送通知。
纯本地、纯标准库（Go），遥测事件由本地合成、可由 JSON 夹具回放；不接监控平台，不做展示看板。

## 语义

- **合并**：同一 source 连续相同状态合并为一个事件段，只有段首的 `FIRING` 触发通知。
- **重复通知**：故障段持续期间，`Tick` 每经过 `repeat_interval` 发一次 `REPEAT`。
- **恢复**：`FIRING -> OK` 且该故障曾对外通知过、且当前未抑制，发 `RESOLVED`。
- **抑制**：抑制只挂起通知，**真实状态照常跟踪**（段正常合并/关闭）。
  - 抑制期间开始、解除时仍在持续的故障 → 解除瞬间补发 `FIRING`（reason=`suppression-lifted`）。
  - 抑制期间已恢复的故障 → 永不补发（既无 `FIRING` 也无 `RESOLVED`）。
  - 抑制前已通知的故障，解除时不重复补发，按重复间隔继续 `REPEAT`。
- **时间可控**：引擎不读系统时钟，所有时间由事件时间戳、`Tick(t)`、`SetSuppressed(v, t)` 注入，回放完全确定。

## 布局

- `types.go` — `TelemetryEvent` / `Segment` / `Notification` 等类型
- `monitor.go` — `Monitor` 引擎（合并、抑制、恢复、重复间隔）
- `fixture.go` — JSON 夹具加载与 `Replay`
- `synthesize.go` — 本地确定性合成遥测夹具（种子可复现）
- `cmd/alertseg/main.go` — CLI：`replay` / `synthesize`
- `fixtures/*.json` — 三个演示夹具
- `monitor_test.go` / `fixture_test.go` — `go test` 测试

## 运行样例

```console
$ go run ./cmd/alertseg replay fixtures/suppressed-persistent.json
fixture: suppressed-persistent-fault (repeat interval: 10m)

notifications:
  2026-01-01T00:15:00Z  api-1    FIRING   suppression-lifted seg[FIRING +0s events=2]
  2026-01-01T00:26:00Z  api-1    REPEAT   repeat-interval    seg[FIRING +0s events=2]
  2026-01-01T00:30:00Z  api-1    RESOLVED state-change       seg[FIRING +29m0s events=2]

segments:
  api-1    FIRING 2026-01-01T00:01:00Z -> 2026-01-01T00:30:00Z  events=2
  api-1    OK     2026-01-01T00:30:00Z -> open  events=1
```

抑制 00:00–00:15 期间开始的故障被保留为真实 FIRING 段，解除抑制时补发，随后按 10m 间隔重复，恢复时发 `RESOLVED`。

```console
$ go run ./cmd/alertseg replay fixtures/recovery-in-suppression.json
fixture: recovery-during-suppression (repeat interval: 10m)

notifications:
  2026-01-01T00:06:00Z  cache-1  FIRING   state-change       seg[FIRING +0s events=1]
  2026-01-01T00:07:00Z  cache-1  RESOLVED state-change       seg[FIRING +1m0s events=1]

segments:
  cache-1  FIRING 2026-01-01T00:01:00Z -> 2026-01-01T00:03:00Z  events=1
  cache-1  OK     2026-01-01T00:03:00Z -> 2026-01-01T00:06:00Z  events=1
  cache-1  FIRING 2026-01-01T00:06:00Z -> 2026-01-01T00:07:00Z  events=1
  cache-1  OK     2026-01-01T00:07:00Z -> open  events=1
```

00:01–00:03 的故障完全发生在抑制窗口内，解除抑制（00:05）时**不补发**；解除后的新故障照常通知。

```console
$ go run ./cmd/alertseg replay fixtures/flapping.json
notifications:
  2026-01-01T00:00:00Z  db-1     FIRING   state-change
  2026-01-01T00:10:00Z  db-1     REPEAT   repeat-interval
  2026-01-01T00:20:00Z  db-1     REPEAT   repeat-interval
  2026-01-01T00:25:00Z  db-1     RESOLVED state-change
  2026-01-01T00:30:00Z  db-1     FIRING   state-change
  2026-01-01T00:31:00Z  db-1     RESOLVED state-change
```

合成新夹具并回放（同一种子输出恒定）：

```console
$ go run ./cmd/alertseg synthesize -seed 7 -events 4 -sources db-1,api-1 > /tmp/gen.json
$ go run ./cmd/alertseg replay /tmp/gen.json
```

## 测试结果

```console
$ go test -v ./...
=== RUN   TestFixtureFlapping
--- PASS: TestFixtureFlapping (0.00s)
=== RUN   TestFixtureSuppressedPersistent
--- PASS: TestFixtureSuppressedPersistent (0.00s)
=== RUN   TestFixtureRecoveryInSuppression
--- PASS: TestFixtureRecoveryInSuppression (0.00s)
=== RUN   TestMergesRepeatedFiringIntoOneSegment
--- PASS: TestMergesRepeatedFiringIntoOneSegment (0.00s)
=== RUN   TestRecoveryEmitsResolvedAndClosesSegment
--- PASS: TestRecoveryEmitsResolvedAndClosesSegment (0.00s)
=== RUN   TestRepeatNotificationsRespectInterval
--- PASS: TestRepeatNotificationsRespectInterval (0.00s)
=== RUN   TestSuppressedPersistentFaultNotifiedOnLift
--- PASS: TestSuppressedPersistentFaultNotifiedOnLift (0.00s)
=== RUN   TestRecoveryDuringSuppressionIsNotBackfilled
--- PASS: TestRecoveryDuringSuppressionIsNotBackfilled (0.00s)
=== RUN   TestKnownFaultRecoveringDuringSuppressionStaysSilent
--- PASS: TestKnownFaultRecoveringDuringSuppressionStaysSilent (0.00s)
=== RUN   TestKnownFaultStillFiringAfterLiftRepeatsByInterval
--- PASS: TestKnownFaultStillFiringAfterLiftRepeatsByInterval (0.00s)
=== RUN   TestMultipleSourcesAreIndependent
--- PASS: TestMultipleSourcesAreIndependent (0.00s)
=== RUN   TestSynthesizeIsDeterministic
--- PASS: TestSynthesizeIsDeterministic (0.00s)
PASS
ok  	alertseg
```
