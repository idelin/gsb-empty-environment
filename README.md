# labeleng — 标签重写引擎

纯 Go 标准库实现，无第三方依赖。按规则对系列标签做重命名、删除、合成，
检测多输入系列映射同一输出的冲突，仅在显式聚合模式下允许合并。

## 设计要点

- **规则**：`rename` / `delete` / `synth`（模板 `{label}` 合成新标签）。
  每条规则带显式 `Order`，全局唯一，重复即拒绝（`engine/rules.go`）。
- **保留标签**：`Reserved` 列表中的标签不能被普通规则改写，
  编译期整批拒绝（`Compile`）。
- **冲突判定**：比较完整输出标签集合的同一性（`LabelSet.Key`），
  而非重写文本片段。删除区分标签导致的碰撞是冲突，系列不会被静默覆盖。
- **冲突图**：`byKey` 维护 输出标识 → 输入系列 的映射，支持多条规则
  组合产生的冲突；`Upsert` / `Retract` 动态刷新，撤回即解冲突。
  每个冲突给出字典序最小冲突输入对（`Conflict.MinimalPair`）。
- **显式聚合**：`RuleSet.Merge=true` 时同输出系列合并为 `MergeGroup`，
  各系列贡献独立记录；撤回某系列后其余系列恢复各自贡献，
  规则撤回使系列重新分离时亦自动恢复。
- **原子发布**：`Publish` 先全量校验再切换，任一规则非法则整批不生效；
  版本严格递增。重写缓存按规则版本隔离（`cache[version]`），旧版本
  缓存不受新版本影响。
- **本地遥测**：事件仅在本地合成（`Telemetry`），可编码为 JSONL 夹具
  （`engine/testdata/replay.jsonl`）并通过 `Replay` 重建引擎状态。
  不接监控平台，无展示看板。

## 运行样例

```sh
go run ./cmd/demo              # 人类可读的完整演示
go run ./cmd/demo -telemetry   # 仅输出 JSONL 遥测事件（可保存为回放夹具）
```

输出节选：

```
== 1. 发布规则 v1（重命名 job->service、删除 instance、合成 site，无聚合）==
冲突: 输出 map[...service:api site:dc1-prod...] 由 [s-a s-b] 共享, 最小冲突输入对 = (s-a, s-b)
== 2. 发布规则 v2（显式聚合模式，冲突合法合并）==
合并组 map[...site:dc1-prod...] 成员贡献 = map[s-a:5 s-b:7]
s-a 有效值（组总值）= 12
== 3. 撤回 s-a（可撤回聚合，恢复 s-b 自身贡献）==
s-b 有效值 = 7
== 4. 恢复 s-a 并发布 v3（不再删除区分标签，系列重新分离）==
s-a 有效值 = 5 / s-b 有效值 = 7 / s-c 有效值 = 11, 冲突数 = 0
== 5. 原子性：含保留标签改写的批量发布被整体拒绝 ==
发布被拒绝（符合预期）: rule "bad": reserved label cannot be rewritten
```

## 测试

```sh
go test ./...
```

结果（12 个测试全部通过）：

```
--- PASS: TestRuleApplyRenameDeleteSynth          重命名/删除/合成及模板缺失跳过
--- PASS: TestRuleOrderExplicit                   顺序显式唯一且影响结果
--- PASS: TestReservedLabelsProtected             保留标签不可改写
--- PASS: TestConflictByOutputIdentityNotText     按输出标签同一性判冲突
--- PASS: TestDeleteDistinguishingLabelConflictsNotOverwrites  删除区分标签不覆盖
--- PASS: TestConflictGraphMultiRuleAndDynamicRetraction       多规则组合+动态撤回
--- PASS: TestMinimalConflictPair                 最小冲突输入对
--- PASS: TestMergeModeRetractableAggregation     可撤回聚合与贡献恢复
--- PASS: TestAtomicBatchPublish                  批量规则原子发布
--- PASS: TestCacheVersionIsolation               缓存按规则版本隔离
--- PASS: TestTelemetryReplayRoundTrip            遥测编解码回放一致
--- PASS: TestTelemetryFixtureReplay              夹具回放重建状态
ok  	labeleng/engine
```

## 目录

- `engine/labels.go` — 标签集合与规范化标识
- `engine/rules.go` — 规则类型、编译校验、按序应用
- `engine/engine.go` — 引擎：冲突图、聚合组、版本化缓存、快照
- `engine/telemetry.go` — 本地遥测事件、JSONL 编解码、回放
- `engine/engine_test.go` + `engine/testdata/replay.jsonl` — 测试与回放夹具
- `cmd/demo/main.go` — 运行样例
