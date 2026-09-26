# tailsampler

纯 Go 标准库实现的尾部采样（tail sampling）演示：span 在本地按 trace 聚合，
trace 完成（收到根 span）、超时或被缓存压力驱逐后，按错误 / 延迟 / 预算做
保留决策。不依赖任何监控平台，遥测事件在本地合成，也可用 JSON 夹具回放。

## 行为规则

- **错误**：trace 中任意一个错误 span 即触发保留（`error` / `timeout_error`）。
- **延迟**：完成的 trace 端到端耗时超过阈值则保留（`slow_trace`）。
- **预算**：令牌桶限制每秒保留数；预算耗尽立即丢弃（`budget_exhausted`），
  不会为等待令牌而无限缓存。
- **有界缓存**：`MaxBufferedTraces` / `MaxBufferedSpans` 超限后强制决策最老的
  未完成 trace（`cache_pressure_*`），内存始终有界。
- **超时**：未完成 trace 超过 `TraceTimeout` 后由 `Tick()` 清扫，走独立的
  超时策略（`timeout` / `timeout_error` / `timeout_slow`）。
- **迟到 span**：决策在 `DecisionTTL` 内按 `DecisionKey(traceID)` 缓存，
  决策后到达的 span 直接复用同一结果，不重新入缓存。
- **一致决策键**：`DecisionKey` 仅由 traceID 经 FNV-1a 哈希得出，同一 trace
  的 span 落在任何分片上都算出相同的键与分片路由（`ShardFor`）。

## 运行

```sh
go run ./cmd/demo   # 本地合成 60 条 trace 的遥测并打印决策
go test ./...       # 单元测试 + testdata/fixture.json 夹具回放
```

夹具格式见 `testdata/fixture.json`：`span` / `tick` 事件按 `at_ms` 毫秒偏移
驱动手动时钟，`Replay` 输出确定性决策序列，可完全复现。

## 运行样例（go run ./cmd/demo，截取）

```
decide trace=trace-000 -> KEEP (error)
decide trace=trace-001 -> KEEP (slow_trace)
decide trace=trace-002 -> DROP (no_signal)
...
decide trace=trace-016 -> DROP (timeout)
...
late span trace=trace-000 -> KEEP (error) [same decision as original]
decision key trace=trace-007 key=7657912598755181026 shard(4)=2 shard(16)=2 (key identical on every shard)

--- summary ---
kept=16 dropped=44 buffered_spans=0 buffered_traces=0
  error                    6
  no_signal                41
  slow_trace               10
  timeout                  3
```

## 测试结果（go test ./...）

```
=== RUN   TestFixtureReplay
--- PASS: TestFixtureReplay (0.00s)
=== RUN   TestDecisionKeyConsistentAcrossShards
--- PASS: TestDecisionKeyConsistentAcrossShards (0.00s)
=== RUN   TestErrorSpanTriggersKeep
--- PASS: TestErrorSpanTriggersKeep (0.00s)
=== RUN   TestSlowTraceKept
--- PASS: TestSlowTraceKept (0.00s)
=== RUN   TestFastCleanTraceDropped
--- PASS: TestFastCleanTraceDropped (0.00s)
=== RUN   TestBudgetExhaustedDropsWithoutCaching
--- PASS: TestBudgetExhaustedDropsWithoutCaching (0.00s)
=== RUN   TestBoundedCacheEvictsOldest
--- PASS: TestBoundedCacheEvictsOldest (0.00s)
=== RUN   TestTimeoutPolicyForIncompleteTraces
--- PASS: TestTimeoutPolicyForIncompleteTraces (0.00s)
=== RUN   TestLateSpanFollowsDecision
--- PASS: TestLateSpanFollowsDecision (0.00s)
PASS
ok  	tailsampler
```

## 代码结构

- `span.go` / `decision.go` / `clock.go`：Span、Decision、可替换时钟
- `key.go`：`DecisionKey` / `ShardFor`，跨分片一致的决策键
- `policy.go`：错误、延迟、超时策略（`CompositePolicy` / `TimeoutKeepPolicy`）
- `budget.go`：保留预算令牌桶
- `sampler.go`：有界缓存、完成/超时/驱逐决策、迟到 span 复用结果
- `fixture.go`：JSON 夹具回放（确定性手动时钟）
- `cmd/demo/main.go`：本地合成遥测的运行样例
