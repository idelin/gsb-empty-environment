# 多币种联合授信时态敞口监控器

集团授信额度与子公司敞口的**双时态**（bitemporal）监控器，纯 Go 标准库实现。

## 核心概念

- **双时态模型**：每条记录携带两个时间区间——
  - `valid time`（业务时间）：事实在业务上生效的区间；
  - `transaction time`（记录时间）：事实被系统知悉的区间。
  追溯调整不改写历史：旧版本的记录区间被关闭，新版本以原业务时间、新记录时间写入，
  因此可以"以任意知悉时点重现历史任意业务时点"的敞口状态。
- **集团授信协议**：额度 + 有效期双时态登记；有效期外的敞口视为裸露敞口并告警。
- **多币种折算**：汇率按观测点登记，敞口按**业务时点**最新生效汇率折算为基准币，
  汇率波动直接影响折算结果，历史时点仍按当时汇率重现。
- **预警线**：集团总敞口 / 授信额度 ≥ 预警比例即告警；`Replay` 以指定知悉时点
  重放一段业务时间，在告警上升沿记录日志，并识别"由追溯调整触发"。
- **质押率追溯**：担保物价值变动或借款追溯调整后调用 `RecalcPledgeRatio`，
  重算质押率（敞口 / 担保物价值）并追加不可变追溯记录。

## 文件结构

- `monitor.go` — 监控器实现（包 `exposure`）
- `monitor_test.go` — 验收与单元测试（`go test`）
- `cmd/demo/main.go` — 运行样例

## 运行

```sh
go test ./... -v     # 运行测试
go run ./cmd/demo    # 运行样例
```

## 验收边界（对应 `TestAcceptance_RetroactiveCorrectionTriggersAlert`）

1. 集团总授信 1000 万，预警线 65%（650 万）；
2. 子公司 A 借款 600 万（占比 60%，未触线）；
3. 时间 30 追溯上调 A 的历史借款为 700 万（业务生效时间不变）；
4. 监控器重现时间 30：同一业务时点在 tx=20 知悉为 600 万、在 tx=30 知悉为
   700 万；重放命中告警：敞口 700 万 / 额度 1000 万 = 70.0% ≥ 65%，
   告警注明"由时间 30 的追溯调整触发"。

## 运行样例输出

```
=== 场景一：验收边界 —— 追溯调整击穿预警线 ===
时间  0：集团授信协议生效，额度 1000 万 CNY，有效期 [0,100)，预警线 65%
时间 10：子公司 A 借款 600 万 CNY，集团总敞口 600 万（占比 60%），未触线
时间 20：以当前知悉状态重放 [0,20]，无告警： true
时间 30：追溯上调 A 的历史借款为 700 万（业务生效时间仍为 10）
  双时态对照：valid=10 在 tx=20 知悉为 600 万，在 tx=30 知悉为 700 万
  监控器重现时间 30：重放 [0,30] 命中 21 个告警时点
  [告警] valid=10 tx=30 敞口 700 万 / 额度 1000 万 = 70.0%
    原因：集团总敞口 7000000 CNY 达到授信额度 10000000 的 70.0%（预警线 65%）；由时间 30 的追溯调整触发

=== 场景二：汇率波动影响敞口折算 ===
时间 10：B 借款 100 万 USD，汇率 6.5，折算敞口 650 万 CNY
时间 15：USD 汇率升至 7.0，同一笔借款在 valid=20 折算 700 万 CNY
  历史不变：valid=10 仍按 6.5 折算 650 万 CNY

=== 场景三：担保物价值变动引发质押率重算 ===
  [质押率重算] valid=10 tx=10 A：敞口 600 万 / 担保物 1000 万 = 60.0%（初始登记）
  [质押率重算] valid=20 tx=20 A：敞口 600 万 / 担保物 800 万 = 75.0%（担保物价值变动：1000 万 -> 800 万）
  [质押率重算] valid=30 tx=30 A：敞口 700 万 / 担保物 800 万 = 87.5%（借款追溯调整：600 万 -> 700 万）
```

## 测试结果

```
=== RUN   TestAcceptance_RetroactiveCorrectionTriggersAlert
--- PASS: TestAcceptance_RetroactiveCorrectionTriggersAlert (0.00s)
    monitor_test.go:67: 告警样例：集团总敞口 7000000 CNY 达到授信额度 10000000 的 70.0%（预警线 65%）；由时间 30 的追溯调整触发
=== RUN   TestBitemporalVersionChain
--- PASS: TestBitemporalVersionChain (0.00s)
=== RUN   TestFXFluctuationChangesExposure
--- PASS: TestFXFluctuationChangesExposure (0.00s)
=== RUN   TestFacilityExpiry
--- PASS: TestFacilityExpiry (0.00s)
=== RUN   TestCollateralPledgeRecalcTrace
--- PASS: TestCollateralPledgeRecalcTrace (0.00s)
PASS
ok  	exposure	0.523s
?   	exposure/cmd/demo	[no test files]
```

## 说明

- 时间用逻辑节拍 `Time(int64)` 表示，便于确定性测试与重放；金额用 `float64`
  （演示量级，生产可替换为定点数）。
- 未知币种汇率按 0 处理（折算为 0），基准币汇率恒为 1。
