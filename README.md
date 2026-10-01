# labelengine

标签重写规则引擎（Go，仅标准库）。按显式顺序的规则对输入系列做标签
重命名、删除与合成，检测多输入映射到同一输出的冲突；只有显式聚合模式
才允许合并，且聚合可撤回。

## 设计要点

- **规则顺序显式**：规则按切片顺序依次应用（`RenameLabel` / `DeleteLabel` /
  `SynthLabel`），顺序不同结果不同（见 `TestRuleOrderIsExplicit`）。
- **输出相等看标签内容**：`LabelSet.Key()` 生成与顺序无关的规范键，冲突
  判断基于标签集合内容，不重写文本（`TestOutputEqualityByLabelsNotText`）。
- **冲突不覆盖**：删除区分性标签导致多输入同输出时，非聚合模式直接拒绝，
  已有输出不被覆盖（`TestDeleteDistinguishingLabelConflictsWithoutOverwrite`）。
- **显式聚合 + 可撤回**：聚合引擎按输入分别记录贡献（`Output.Contributions`），
  `RetractRule` 动态撤回规则后原系列重新分离、各自贡献恢复
  （`TestAggregationMergeAndRetractRestores`）。
- **冲突图**：`BuildConflictGraph` 支持多规则组合碰撞与 `Retract` 动态撤回；
  `EnumerateDomain` 对小标签域做笛卡尔展开，穷举核验冲突范围并给出最小
  冲突输入对（`MinimalPair`）。
- **原子批量发布**：`UpdateRules` 先对当前输入模拟整批规则，冲突则整批
  拒绝、版本号不变；成功才 bump 版本。重写缓存按规则版本隔离
  （`caches[version]`，见 `TestCacheIsolatedByRuleVersion`）。
- **保留标签**：构造引擎时声明的保留标签（如 `tenant`、`__name__`）不能
  被普通规则改写（`ValidateRules` 拒绝）。
- **本地遥测**：事件（series_added / series_merged / series_split /
  conflict_rejected / rules_published / rule_retracted / cache_store /
  cache_hit）在本地合成，可编码为 JSONL 夹具并经 `Replay` 回放重建统计；
  不接监控平台，不做展示看板。

## 运行

```sh
go run .        # 演示全部场景
go test ./...   # 全部测试
```

## 运行样例（go run .）

```
== rename job->service, synth site={dc}-{env}
  {dc=eu,env=prod,instance=a,service=api,site=eu-prod} value=10 contributions=[http_requests_total{dc=eu,env=prod,instance=a,job=api}=10]

== delete distinguishing label, second series rejected:
  conflict: inputs http_requests_total{dc=eu,env=prod,instance=a,job=api} and http_requests_total{dc=eu,env=prod,instance=b,job=api} both map to output {dc=eu,env=prod,service=api} (aggregation not enabled)

== original series untouched (no overwrite)
  {dc=eu,env=prod,service=api} value=10 contributions=[http_requests_total{dc=eu,env=prod,instance=a,job=api}=10]

== rule targeting reserved label rejected:
  rule "bad" targets reserved label "tenant"

== explicit aggregation merges contributions
  {job=api} value=30 contributions=[http_requests_total{instance=a,job=api}=10 http_requests_total{instance=b,job=api}=20]

== after retracting delete rule: series split, contributions restored
  {instance=a,job=api} value=10 contributions=[http_requests_total{instance=a,job=api}=10]
  {instance=b,job=api} value=20 contributions=[http_requests_total{instance=b,job=api}=20]

== atomic batch rejected, version stays 0:
  conflict: inputs m{app=x,pod=p1} and m{app=x,pod=p2} both map to output {app=x} (aggregation not enabled)

== enumerated 4 series, conflict nodes: 2
  minimal conflicting pair: m{app=x,pod=p1} vs m{app=x,pod=p2}
  after retracting r9: conflicts=false

== telemetry replayed from telemetry_fixture.jsonl:
  added=1 merged=1 split=1 published=1 retracted=1 cache(store=2 hit=0)
```

## 测试结果（go test -v ./...）

```
--- PASS: TestEnumerateDomainVerifiesConflictScope
--- PASS: TestConflictGraphDynamicRetraction
--- PASS: TestOutputEqualityByLabelsNotText
--- PASS: TestMultipleRuleCombinationCollision
--- PASS: TestRuleOrderIsExplicit
--- PASS: TestRenameDeleteSynth
--- PASS: TestReservedLabelCannotBeRewritten
--- PASS: TestDeleteDistinguishingLabelConflictsWithoutOverwrite
--- PASS: TestAggregationMergeAndRetractRestores
--- PASS: TestAtomicBatchUpdateAndMinimalPair
--- PASS: TestCacheIsolatedByRuleVersion
--- PASS: TestTelemetryRoundTripReplay
--- PASS: TestFixtureReplay
--- PASS: TestConflictRejectedEvent
PASS
ok  	labelengine
```

## 文件

- `labels.go` — LabelSet/Series、规范键、可读化
- `rules.go` — 重命名/删除/合成规则、保留标签校验
- `engine.go` — 引擎：冲突检测、聚合与撤回、原子批量发布、版本隔离缓存
- `conflict.go` — 冲突图、小标签域枚举、最小冲突对
- `telemetry.go` — 本地事件、JSONL 夹具编解码、回放投影
- `main.go` — 演示程序
- `*_test.go`、`testdata/telemetry_fixture.jsonl` — 测试与回放夹具
