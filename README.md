# spaneval

根据声明的预期 span 结构评估实际遥测 span，输出缺失 / 重复 / 未结束比例与公开分数。
纯 Go 标准库；遥测事件在本地合成或由夹具回放，不接监控平台，不做展示看板。

## 预期结构：有限规则图

`testdata/rules.json` 是一棵有限规则树：

- `kind: "seq"`（默认）：每个子节点都预期出现；
- `kind: "choice"`：互斥分支，恰好预期一个分支（如 `auth.token` 与 `auth.basic`）；
- `min` / `max`：重复次数（默认 1/1，`min:0` 表示可选）；
- `when: "key=value"` / `"key!=value"`：按 trace 属性决定节点是否预期。

## 匹配：最低缺失/重复代价映射，而非按名称计数

匹配（`match.go`）把展开后的 slot 树与实际 span 做全映射枚举（小树参考实现，
带分支限界与复杂度上限），约束：

- 名称兼容；
- 父子关系与稳定身份：span 只能匹配到其父 span 所匹配的 slot 实例之下；
  重放的同一 `span_id` 只计一次（`replay.go`）。
- 每个 slot 至多匹配一个 span；同名多余的 span 是重复，**不提高完整度**。

因此结构错误表现为「缺失 + 重复」双重代价：名字对但父节点错的 span
无法抵消缺失 slot（见 `TestStructuralErrorNotOffsetByNameCount`）。
贪心按名称计数或按到达顺序分配都会更差（见 `TestExhaustiveMappingBeatsGreedyOrder`）。

## 公开分数公式

```
score = clamp(1 - (missing + 0.5*duplicates + 0.5*unfinished) / total, 0, 1)
```

- `total`：实例化后的必需 slot 数（条件已求值、choice 计 1 分支、
  已匹配可选 slot 的必需后代计入）；
- 缺根（必需根 slot 缺失）=> `critical: true`，分数封顶 0.2——子 span 再多也没用；
- 未结束与确实缺失分开：`unfinished` 是已出现但未 `span_end` 的 span，权重减半，
  不计入 `missing`。

## 预算未决与迟到修订

- 评估时给定 `now` / `deadline`：仍有未结束 span 处于预算内时，
  `status: "undecided"`，只给 `score_range`，不给确定评分；
- `revise` 子命令把迟到事件并入后重新评估，并逐条解释：
  哪个迟到 span 填补了缺失、哪个只是重复、分数如何变动。

## 用法

```sh
go run ./cmd/spaneval eval   -rules testdata/rules.json -events testdata/events_ok.json -now 1000 -deadline 950
go run ./cmd/spaneval synth  -rules testdata/rules.json -attrs env=prod,cache=on -seed 7 [-drop db.query] [-dup render] [-open render]
go run ./cmd/spaneval revise -rules testdata/rules.json -base testdata/events_late_base.json -late testdata/events_late_arrival.json -now 1000 -deadline 950
```

## 运行样例（实际输出摘录）

完整匹配（`events_ok.json`）：`total=6 matched=6 score=1 status=final`。

缺根但子 span 很多（`events_missing_root.json`）：

```json
{ "missing": 6, "duplicates": 4, "score": 0, "critical": true,
  "notes": ["required root slot \"http.request\" missing: critical regardless of matched children"] }
```

父节点挂错（`events_wrong_parent.json`，`render.template` 挂到 `db.query` 下）：
`missing=1 duplicates=1 score=0.75` —— 名称数量完全一致，但结构错误不会被抵消。

超上限重复（`events_dup.json`，第 3 个 `db.query` 超过 `max:2`）：
`duplicates=1 matched=6(=total) score=0.9167`，重放的同一 `span_id` 被忽略。

预算未决（`events_unfinished.json`，`now=850 < deadline=950`）：

```json
{ "status": "undecided", "unfinished": 1, "pending": 1,
  "score": 0.9167, "score_range": [0.9167, 1] }
```

迟到修订（`revise`，base 缺 `db.query`，迟到事件补两个并多来一个）：

```
late span "db.query" (s3) arrived and filled an expected slot
late span "db.query" (s9) arrived and filled an expected slot
late span "db.query" (s10) arrived but matches no open slot: duplicate, completeness unchanged
missing 1->0, duplicates 0->1, unfinished 0->0, score 0.83->0.92
```

## 测试

```sh
go test ./...
```

覆盖：条件求值、重复/互斥/条件展开、全映射优于贪心、结构错误不可抵消、
缺根关键、重复不提高完整度、同 span 重放幂等、未结束≠缺失、预算未决不给确定分、
迟到修订解释、合成-回放往返。结果：`ok spaneval`（15 个测试全部通过）。
