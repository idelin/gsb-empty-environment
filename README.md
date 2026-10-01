# faultalign

把模拟器注入的已知故障事件与观测到的告警症状做**全局最优一对一匹配**，
计算检测延迟、误报、漏报；支持时钟偏移扫描、迟到更正回放与小实例枚举验证。
纯 Go 标准库实现，无外部依赖，不接入任何监控平台，遥测事件由本地夹具合成回放。

## 匹配规则

- 故障与告警可按 `entity + symptom` 兼容；时间窗为
  `[fault.start - maxLead, fault.end + maxLag]`（未结束故障以 `obsEnd` 为有效终点）。
- 一对一：一个告警最多记功一次，一个故障最多被一条告警检测；重复症状中
  只有全局最优的一条被计为检测，其余按误报处理。
- 目标函数：先最大化匹配对数，再最小化总代价（|告警时间 - 故障开始| 的秒数）。
  用 Hungarian（Kuhn–Munkres）算法求全局最小成本，而不是逐条贪心。
- 提前告警：`earlyAsFP=false` 时按提前量（lead）计入检测；`earlyAsFP=true` 时标为误报。
- 观测窗结束仍未结束的故障为**右删失**，单独列出，不计入漏报。

## 指标

- 检测延迟（latency）：告警时间 − 故障开始（≥0），统计均值/最大值。
- 提前量（lead）：告警早于故障开始（<0），单独统计。
- 误报（FP）：未匹配告警，附原因（无兼容故障 / 超出时间窗 / 重复症状）。
- 漏报（FN）：观测窗内已结束但无告警匹配的故障。
- 右删失：观测窗结束时仍打开的故障，单列。

## 用法

```sh
go build -o faultalign .

# 回放夹具：初始匹配 + 迟到更正后的历史更新
./faultalign run -fixture testdata/fixture.json

# 时钟偏移扫描：告警时钟在 [-5m, 5m] 内按 1m 步长平移，输出各指标范围
./faultalign sweep -fixture testdata/fixture.json -offsets -5m:5m:1m

# 小实例验证：枚举所有一一匹配，与 Hungarian 结果对比
./faultalign verify -fixture testdata/fixture.json

go test ./...
```

## 夹具格式（testdata/fixture.json）

```json
{
  "config": {"maxLead": "20m", "maxLag": "10m", "earlyAsFP": false, "obsEnd": "..."},
  "faults":  [{"id": "f1", "entity": "host-a", "symptom": "cpu_spike",
               "start": "...", "end": "..."}],
  "alerts":  [{"id": "a1", "entity": "host-a", "symptom": "cpu_spike", "time": "..."}],
  "corrections": [{"kind": "alert", "id": "a4", "entity": "host-b", "time": "..."}]
}
```

`corrections` 按顺序回放：每条更正（修改告警时间/实体或故障起止）应用后
重新做全局匹配，历史记录新增/移除的匹配对及更新后的分项统计。

## 运行样例

```
$ ./faultalign run
# replay fixture: initial matching
== matched pairs (fault <- alert, latency) ==
  f1     <- a1     latency 3m0s
  f2     <- a3     latency -15m0s (early, lead 15m0s)
== unmatched alerts (false positives) ==
  a2     false positive: duplicate symptom, compatible fault already matched
  a4     false positive: no fault with matching entity/symptom
== unmatched faults (false negatives) ==
  f4     missed: no alert matched within window
== right-censored faults (open at observation end, not counted as missed) ==
  f3     right-censored: fault still open at observation end
== stats ==
  matched=2  false_positives=2  false_negatives=1  right_censored=1
  detections=1 mean_latency=3m0s max_latency=3m0s  leads=1 mean_lead=15m0s

# late corrections: replay and update history
correction 1: alert a4 updated
  + added match   f4 <- a4 (latency 2m0s)
== stats ==
  matched=3  false_positives=1  false_negatives=0  right_censored=1
  detections=2 mean_latency=2m30s max_latency=3m0s  leads=1 mean_lead=15m0s
correction 2: fault f3 updated
  matches unchanged
== stats ==
  matched=3  false_positives=1  false_negatives=1  right_censored=0
  detections=2 mean_latency=2m30s max_latency=3m0s  leads=1 mean_lead=15m0s
```

```
$ ./faultalign sweep -offsets -5m:5m:1m
# clock offset sweep: alert clock shifted over [-5m0s, 5m0s] step 1m0s
offset   matched   fp   fn cens mean_latency  mean_lead
-5m0s          2    2    1    1           0s      20m0s
-4m0s          2    2    1    1           0s      10m0s
-3m0s          2    2    1    1           0s      18m0s
-2m0s          2    2    1    1         1m0s      17m0s
-1m0s          2    2    1    1         2m0s      16m0s
0s             2    2    1    1         3m0s      15m0s
1m0s           2    2    1    1         4m0s      14m0s
2m0s           2    2    1    1         5m0s      13m0s
3m0s           2    2    1    1         6m0s      12m0s
4m0s           2    2    1    1         7m0s      11m0s
5m0s           2    2    1    1         8m0s      10m0s
== metric ranges over the offset interval ==
  matched:         [2, 2]
  false_positives: [2, 2]
  false_negatives: [1, 1]
  mean_latency:    [0s, 8m0s]
```

```
$ ./faultalign verify
hungarian: matched=2 cost=1080.0
enumerate: matched=2 cost=1080.0
OK: Hungarian matching is globally optimal on this instance
```

## 测试结果

```
$ go test -v ./...
=== RUN   TestLateCorrectionRewritesHistory
--- PASS: TestLateCorrectionRewritesHistory (0.00s)
=== RUN   TestCorrectionCascadesAcrossSteps
--- PASS: TestCorrectionCascadesAcrossSteps (0.00s)
=== RUN   TestClockOffsetSweepShiftsMetrics
--- PASS: TestClockOffsetSweepShiftsMetrics (0.00s)
=== RUN   TestFixtureEndToEnd
--- PASS: TestFixtureEndToEnd (0.00s)
=== RUN   TestHungarianMatchesEnumeration
--- PASS: TestHungarianMatchesEnumeration (0.00s)
=== RUN   TestDuplicateSymptomCountsOnce
--- PASS: TestDuplicateSymptomCountsOnce (0.00s)
=== RUN   TestGlobalBeatsGreedy
--- PASS: TestGlobalBeatsGreedy (0.00s)
=== RUN   TestEarlyAlertRules
--- PASS: TestEarlyAlertRules (0.00s)
=== RUN   TestRightCensored
--- PASS: TestRightCensored (0.00s)
=== RUN   TestEntityAndSymptomCompatibility
--- PASS: TestEntityAndSymptomCompatibility (0.00s)
PASS
ok  	faultalign	1.066s
```

其中 `TestHungarianMatchesEnumeration` 在 300 个随机小实例上枚举所有
一一匹配，验证 Hungarian 结果与全局最优完全一致（匹配数与总成本）。

## 代码结构

- `types.go` — 故障/告警/配置/夹具类型
- `match.go` — 可行性窗口、代价函数、Hungarian 全局最优一对一匹配
- `enumerate.go` — 小实例暴力枚举（验证最优性）
- `metrics.go` — 延迟/提前量/误报/漏报/右删失统计
- `corrections.go` — 迟到更正应用、历史匹配 diff 与统计更新
- `main.go` — CLI：`run` / `sweep` / `verify`
- `testdata/fixture.json` — 可回放的本地合成遥测夹具
