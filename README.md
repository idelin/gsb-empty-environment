# telemetry-agg

版本化固定桶遥测聚合存储。纯 Go 标准库实现，无外部依赖；遥测事件在本地合成，
可通过 JSON 夹具回放，不接监控平台，不含展示看板。

## 结构

- `agg/event.go` — 事件、摘要（min/max/sum/count/首末值）、排序与合并规则
- `agg/store.go` — 存储：去重摄入、带版本的区间更正/删除、审计日志
- `agg/query.go` — 跨层查询（边缘明细 + 内部摘要）、快照查询、全量排序对照、校验
- `agg/fixture.go` — 本地事件合成（`Synth`）与夹具回放（`Replay`/`Save`/`LoadFixture`）
- `cmd/aggdemo` — 端到端运行样例

## 语义要点

- **固定桶**：`TS` 按桶宽向下取整入桶（负时间戳同样是 floor 语义）；边界值归入右侧桶。
- **缺口 ≠ 0**：无事件的桶 `Present=false`（缺口）；值为 0 的事件使桶 `Present=true, Sum=0`。
- **重叠批次去重**：按事件 ID 去重，重复投递的批次幂等，更正后可复用已撤回的 ID。
- **均值**：`Mean() = Sum/Count`，绝不对桶均值再平均；空集返回 NaN。
- **峰值证据**：`Max` 就是峰值事件本身（ID/TS/值）。撤回峰值后从桶内有序明细重算，
  次大值浮出，而不是只扣 sum；单点峰（桶内仅一个事件）不消失。
- **首末值**：按事件时间排序，时间相同按 ID 决胜。
- **版本化更正/删除**：每次变更递增版本；事件带 `[addedAt, removedAt)` 可见区间，
  旧快照经 `QueryAt`/`Details` 继续可查。审计记录每次更正访问的桶与明细范围。
- **跨层查询**：完全落入范围的桶走摘要层，边缘桶走明细层，二者不重计；
  `Validate` 用全量排序参考实现复核结果与桶计数。

## 运行样例

```
$ go run ./cmd/aggdemo
== ingest (with overlapping redelivery) ==
events=181 sum=117444.0 mean=648.8619
peak evidence: id=e00142 ts=1700000285 value=5490.0
first=e00000@1700000000 last=e00179@1700000359 (event time, ID tiebreak)

== per-bucket breakdown (gap vs zero) ==
bucket 28333333 [1699999980,1700000040): count=21 sum=11079.0 min=0.0 max=5412.0
bucket 28333334 [1700000040,1700000100): count=30 sum=6988.0 min=15.5 max=435.0
bucket 28333335 [1700000100,1700000160): count=10 sum=8128.0 min=208.0 max=5056.0
bucket 28333343 [1700000580,1700000640): present=false count=0 (carved gap)
bucket 28333344 [1700000640,1700000700): present=false count=0 (carved gap)

== retract the peak (versioned delete) ==
retracted: id=e00142 ts=1700000285 value=5490.0
events=180 sum=111954.0 mean=621.9667
next-largest peak evidence: id=e00097 ts=1700000194 value=5479.5

== old snapshot still queryable ==
snapshot v2: events=181 peak=e00142 value=5490.0 (retracted peak still visible)

== correction audit log ==
v3 delete [1700000285,1700000286): buckets=[28333338] details=[1700000285,1700000286) scanned=1 removed=1 added=0

== cross-layer validation (edge details + internal summaries) ==
latest result matches full-sort reference; bucket counts add up

== fixture save / replay ==
replayed from /tmp/aggdemo-fixture.json: events=180 sum=111954.0 peak=e00097 value=5479.5 (matches: true)
```

## 测试结果

```
$ go test ./...
ok  	telemetry-agg/agg	0.551s
?   	telemetry-agg/cmd/aggdemo	[no test files]
```

18 个测试全部通过，覆盖：桶边界与负时间戳、缺口与零值区分、重叠批次去重、
首末 ID 决胜、sum/count 均值、峰值撤回找次大值、单点峰保持、版本化更正与快照、
审计的桶与明细范围、ID 复用、空/非法区间、跨层查询对全量排序的随机化对照
（60 组随机流 × 所有版本 × 随机区间）、夹具存取回放一致性。
