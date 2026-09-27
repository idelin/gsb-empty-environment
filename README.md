# tailsampler

尾部采样（tail sampling）演示：仅用 Go 标准库实现，遥测事件在本地合成、
可由 JSON 夹具回放，不接监控平台，不做展示看板。

## 设计

- **决策规则（按优先级）**：任一 span 携带错误即保留（`error`）；trace 时长
  超过阈值保留（`latency`）；预算令牌窗口内保留（`budget`）；否则丢弃（`drop`）。
- **有界缓存**：未完成的 trace 缓存在 `MaxTraces` 上限内，超限按 FIFO 驱逐最老
  trace（`evicted`），预算耗尽或流量激增都不会无限缓存。
- **超时单独策略**：超过 `TraceTimeout` 未完成的 trace 由 `TimeoutPolicy`
  （`drop` / `keep_all` / `keep_error`）单独决策（`timeout`）。
- **迟到 span**：决策结果存入有界的 decided 缓存，决策后到达的 span 直接返回
  同一结果（`Late=true`），不重新缓存、不二次决策。
- **一致决策键**：`ShardKey(traceID)` 仅依赖 trace ID（FNV-1a），同一 trace
  的不同分片路由到同一决策键，决策与 span 到达顺序无关。

## 运行样例

```
$ go run ./cmd/sampler fixtures/mixed.json
t=22:13:20.010 trace=t1     shard=6 decision=keep reason=budget  spans=1
t=22:13:20.020 trace=t2     shard=3 decision=drop reason=drop    spans=1
t=22:13:20.040 trace=t3     shard=0 decision=keep reason=error   spans=2
t=22:13:20.050 trace=t4     shard=5 decision=keep reason=latency spans=1
t=22:13:20.080 trace=t5     shard=2 decision=drop reason=evicted spans=1
t=22:13:25.000 trace=t6     shard=7 decision=drop reason=timeout spans=1
t=22:13:25.000 trace=t7     shard=4 decision=drop reason=timeout spans=1
t=22:13:25.000 trace=t2     shard=3 decision=drop reason=drop    spans=1 (late span)
summary: kept=3 dropped=5 total=8
```

夹具 `fixtures/mixed.json` 自带采样配置，回放使用注入时钟，结果完全确定。

## 测试结果

```
$ go test ./... -v
=== RUN   TestErrorSpanTriggersKeep
--- PASS: TestErrorSpanTriggersKeep (0.00s)
=== RUN   TestLatencyThreshold
--- PASS: TestLatencyThreshold (0.00s)
=== RUN   TestBudgetExhaustionAndRefill
--- PASS: TestBudgetExhaustionAndRefill (0.00s)
=== RUN   TestBoundedCacheEvictsOldest
--- PASS: TestBoundedCacheEvictsOldest (0.00s)
=== RUN   TestTimeoutPolicies
--- PASS: TestTimeoutPolicies (0.00s)
    --- PASS: TestTimeoutPolicies/drop_with_error_span (0.00s)
    --- PASS: TestTimeoutPolicies/keep_all_without_error (0.00s)
    --- PASS: TestTimeoutPolicies/keep_error_with_error (0.00s)
    --- PASS: TestTimeoutPolicies/keep_error_without_error (0.00s)
=== RUN   TestLateSpanFollowsStoredDecision
--- PASS: TestLateSpanFollowsStoredDecision (0.00s)
=== RUN   TestDecidedCacheIsBounded
--- PASS: TestDecidedCacheIsBounded (0.00s)
=== RUN   TestShardKeyConsistency
--- PASS: TestShardKeyConsistency (0.00s)
=== RUN   TestReplayFixture
--- PASS: TestReplayFixture (0.00s)
PASS
ok  	tailsampler/sampler
```

## 布局

- `sampler/sampler.go` — 采样器核心：有界缓存、决策、预算、超时、迟到 span
- `sampler/fixture.go` — 夹具加载与确定性回放
- `sampler/sampler_test.go` — 全部单测（`go test ./...`）
- `cmd/sampler/main.go` — 回放 CLI
- `fixtures/mixed.json` — 覆盖六种决策路径的合成事件流
