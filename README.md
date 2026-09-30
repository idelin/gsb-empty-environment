# matchsim — 故障事件与告警症状的一对一全局最优对齐

本地合成遥测、由夹具（fixture）回放的故障-告警对齐器。它把模拟器注入的
已知故障事件与观测到的告警症状做**一对一**匹配，计算检测延迟、误报（FP）、
漏报（FN），并对未结束的观测窗做右删失单列。仅使用 Go 标准库。

## 匹配规则

- **时间窗**：告警时间须落在 `[fault.Start - maxLead, fault.Start + maxLag]`。
- **实体兼容**：实体映射到兼容组（`entityGroups`），同组实体可互相匹配；
  未配置的实体只能匹配自身。
- **重复症状**：症状映射到等价类（`symptomClasses`），同类症状可互相匹配。
- **一对一**：一个告警最多记功一次，一个故障最多吸收一个告警，杜绝重复计功。
- **提前告警**：默认在 `maxLead` 内允许匹配并报告提前量（负延迟，lead）；
  置 `earlyAlertAsFalsePositive: true` 则提前告警一律记为误报。

## 全局最小成本匹配（非贪心）

匹配目标是**先最大化匹配对数、再最小化总检测延迟**。实现为二分图上的
最小费用最大流（连续最短增广路，Bellman-Ford 处理可为负的提前量成本），
对任意实例给出全局最优解，而非逐条贪心。小实例（≤12×12）可用
`-verify` 枚举全部匹配验证最优性。

## 时钟偏移扫描

`-sweep-min/-sweep-max/-sweep-step` 在时钟偏移区间内重放场景，
输出每个偏移下的指标及区间范围（detected / fp / fn / 平均延迟的 min-max）。

## 迟到更正

夹具中的 `corrections` 提供告警时间戳的迟到更正。应用后重新计算匹配，
输出相对基线的历史 diff（增删的匹配、新增/消除的 FP/FN）以及更新后的
分类（症状等价类）统计。

## 右删失

`end` 缺失或晚于 `observationEnd` 的故障视为右删失，单独列出，
不计入漏报。

## 运行

```sh
go run ./cmd/matchsim -fixture fixtures/basic.json \
    -sweep-min -90s -sweep-max 90s -sweep-step 30s -verify
```

## 运行样例（实测输出）

```
scenario: 6 faults, 7 alerts, observation ends 2026-01-01T01:00:00Z
rules: maxLead=30s maxLag=5m0s earlyAlertAsFP=false

== BASELINE (offset 0) ==
matches (one-to-one, globally min-cost):
  f1     <-> a1     class=cpu_spike lag 35s
  f2     <-> a3     class=cpu_spike lead 5s
  f3     <-> a7     class=disk     lag 10s
  f4     <-> a5     class=mem_pressure lag 20s
false positives (unmatched alerts):
  a2     contention: compatible fault already claimed by a lower-cost alert
  a4     contention: compatible fault already claimed by a lower-cost alert
  a6     no compatible fault (entity/symptom mismatch)
false negatives (unmatched faults):
  f6     no compatible alert (entity/symptom mismatch)
right-censored faults (open at observation end, not counted as FN):
  f5
metrics: detected=4/6 fp=3 fn=1 censored=1 latency mean=15s min=-5s max=35s
  class cpu_spike n=2 mean=15s min=-5s max=35s
  class disk     n=1 mean=10s min=10s max=10s
  class mem_pressure n=1 mean=20s min=20s max=20s

== LATE CORRECTIONS ==
  applied: a2: 00:00:50 -> 00:09:50
history diff vs baseline:
  + match f2 <-> a2 (lead 10s)
  - match f2 <-> a3 (lead 5s)
  + fp a3
  - fp a2

== CLOCK OFFSET SWEEP ==
  offset   detected  fp  fn  censored  mean-latency
  -1m30s   0         7   5   1         -
  -1m0s    2         5   3   1         -27.5s
  -30s     3         4   2   1         -8.333333333s
  0s       4         3   1   1         15s
  30s      4         3   1   1         45s
  1m0s     4         3   1   1         1m15s
  1m30s    4         3   1   1         1m45s
metric ranges over the offset interval:
  detected: [0, 4]
  fp:       [3, 7]
  fn:       [1, 5]
  mean latency: [-27.5s, 1m45s]

== VERIFY ==
solver: 4 matches, cost 60000000000; exhaustive: 4 matches, cost 60000000000; optimal=true
```

## 测试

```sh
go test ./...
```

实测结果（全部通过）：

```
=== RUN   TestOneToOneMatching              --- PASS   # 一告警不可多次计功
=== RUN   TestGlobalBeatsGreedy             --- PASS   # 全局最优优于逐条贪心
=== RUN   TestRandomSmallMatchesEnumeration --- PASS   # 300 个随机小实例对拍枚举
=== RUN   TestEarlyAlertRules               --- PASS   # 提前量/提前告警记误报
=== RUN   TestCompatibilityRules            --- PASS   # 实体组与症状等价类
=== RUN   TestRightCensoring                --- PASS   # 右删失单列、不计漏报
=== RUN   TestCorrectionsUpdateHistory      --- PASS   # 迟到更正更新历史与分类统计
=== RUN   TestSweepRanges                   --- PASS   # 时钟偏移区间指标范围
=== RUN   TestUnmatchedReasons              --- PASS   # 未匹配原因分类
ok  	matchsim
```

## 布局

- `types.go` — 故障/告警/更正/规则/夹具的数据模型（JSON 可回放）
- `match.go` — 可行性规则、最小费用最大流匹配、全枚举验证
- `report.go` — 指标报告、未匹配原因、迟到更正 diff、偏移扫描
- `cmd/matchsim/main.go` — CLI
- `fixtures/basic.json` — 合成遥测夹具（含重复症状、实体组、提前告警、
  误报、漏报、右删失与一条迟到更正）
- `matchsim_test.go` — `go test` 测试
