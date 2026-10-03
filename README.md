# SWIM 故障检测度量分析仪 (swimlab)

量化 SWIM 风格故障检测协议的运行指标：统计每个故障注入事件的**发生时刻、首次怀疑时刻、最终标记时刻**，计算**平均检测时间 (MTTD)** 与**误报率**，并输出详尽诊断报告。仅使用 Go 标准库。

## 组件结构

- `simulator.go` — 离散化 SWIM 检测模拟器：每协议周期探测一次成员；首次失败进入怀疑，连续失败达到 `MarkThreshold` 标记为故障；节点恢复响应即澄清怀疑。
- `analyzer.go` — 度量分析仪：登记注入事件、记录检测器观测，按节点与时间窗口匹配，计算 MTTD、漏报数、误报率，生成诊断报告。
- `scenario.go` — 验收场景：10 次真实节点宕机（6 同相 / 4 异相）+ 5 次短暂丢包扰动（持续 1~2 周期）。
- `cmd/swimlab/main.go` — 运行样例入口。

## 运行样例

```sh
go run ./cmd/swimlab
```

输出：

```
===== SWIM 故障检测诊断报告 =====
事件明细:
  ID   类型         节点     发生时刻       首次怀疑       最终标记       MTTD(周期)     结论
  0    节点宕机       N1     10         10         12         2.0          检测成功
  10   网络丢包       N11    16         16         -          -            怀疑后澄清(无误报)
  1    节点宕机       N2     22         23         25         3.0          检测成功
  2    节点宕机       N3     34         34         36         2.0          检测成功
  11   网络丢包       N12    40         40         -          -            怀疑后澄清(无误报)
  3    节点宕机       N4     46         46         48         2.0          检测成功
  4    节点宕机       N5     58         59         61         3.0          检测成功
  12   网络丢包       N13    64         64         -          -            怀疑后澄清(无误报)
  5    节点宕机       N6     70         70         72         2.0          检测成功
  6    节点宕机       N7     82         83         85         3.0          检测成功
  13   网络丢包       N14    88         88         -          -            怀疑后澄清(无误报)
  7    节点宕机       N8     94         94         96         2.0          检测成功
  8    节点宕机       N9     106        106        108        2.0          检测成功
  14   网络丢包       N15    112        112        -          -            怀疑后澄清(无误报)
  9    节点宕机       N10    118        119        121        3.0          检测成功
汇总:
  真实宕机事件: 10, 成功检测: 10, 漏报: 0
  扰动事件: 5, 被误标记: 0
  平均检测时间 MTTD: 2.4 周期
  标记总数: 10, 误报标记: 0
  误报率: 0.0%
```

## 测试

```sh
go test ./... -v
```

结果（13 个测试全部通过）：

```
--- PASS: TestAcceptanceMTTD                      # 平均 MTTD = 2.4 周期
--- PASS: TestAcceptanceFalsePositiveRate         # 误报率 = 0%
--- PASS: TestAcceptanceDetectionTimeDistribution # MTTD 分布 {2周期:6, 3周期:4}
--- PASS: TestAcceptanceDisturbancesNotMarked     # 5 次扰动均怀疑后澄清、未误标记
--- PASS: TestAnalyzerFalsePositive
--- PASS: TestAnalyzerUnmatchedMarkIsFalsePositive
--- PASS: TestAnalyzerMissedCrash
--- PASS: TestAnalyzerMTTDMath
--- PASS: TestReportContent
--- PASS: TestSuspectThenMark
--- PASS: TestPacketLossSuspectedThenCleared
--- PASS: TestRecoverResetsDetectorView
--- PASS: TestLongOutageMarksAfterThreshold
PASS
ok  	swimlab
```

## 检测时序说明

`MarkThreshold = 3`（怀疑超时约 2 个周期）：

- 同相宕机：周期 `t` 探测前宕机，`t` 首次怀疑，`t+2` 标记 → MTTD 2 周期
- 异相宕机：周期 `t` 探测刚结束后宕机，`t+1` 首次失败，`t+3` 标记 → MTTD 3 周期
- 10 次宕机平均 MTTD = (6×2 + 4×3) / 10 = **2.4 周期**
- 丢包扰动持续 1~2 周期 < 标记阈值，怀疑被澄清，**误报率 0%**
