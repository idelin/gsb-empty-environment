# 保障结算模拟器 (settlement-sim)

按调用方输入的规则对理赔事件做结算的模拟器。**不包含任何真实保险条款**，
所有行为（保障区间、等待期、免赔额、限额）完全由输入规则驱动。

金额一律使用整数（分），避免浮点误差。

## 规则模型 (`settlement.Rule`)

| 字段 | 说明 |
| --- | --- |
| `coverage_start` / `coverage_end` | 保障区间（含边界），事故日期落在区间外 → 全额扣减 `OUT_OF_COVERAGE` |
| `waiting_days` | 等待期天数，事故日期早于 `coverage_start + waiting_days` → 全额扣减 `WAITING_PERIOD` |
| `deductible` | 免赔额，`per_event`（每笔都扣）或 `aggregate`（全程只扣一次，按处理顺序消耗） |
| `limits[]` | 限额列表：`per_event` 单次限额；`aggregate` 累计限额。`categories` 为空表示全部类别共用（共享限额），否则只作用于列出的类别 |

## 事件与结算顺序

- 事件按**到达顺序**提交（`Engine.Submit`），允许迟到（事故日期早于已结算事件）。
- 内部结算顺序确定：先按事故日期，再按到达序号。
- 每次到达新事件，引擎对全部已知事件**确定性重算**，与已入账金额对比：
  - 重算结果更高 → 生成 `CATCH_UP`（追补）记录；
  - 重算结果更低 → 生成 `CLAWBACK`（追回）记录，金额为负；
  - 新事件本身生成 `PAYMENT` 或 `DENIED` 记录。
- 每条记录携带该事件**当前完整的扣减原因列表**（`deductions`），
  原因码：`OUT_OF_COVERAGE`、`WAITING_PERIOD`、`DEDUCTIBLE`、
  `PER_EVENT_LIMIT`、`SHARED_LIMIT`（含 `limit_id`）。

## 不变量校验

`Engine.Validate()` 重放账本并断言：

- 账本净额与引擎状态一致；
- 每笔事件净支付 ∈ [0, 申报金额]；
- 每个累计/共享限额的已用量 ≤ 上限（总支付不突破限额）。

`Submit` 每次重算后也会检查限额不变量。

## 使用

```sh
go test ./...
go run ./cmd/simulator -rules examples/rules.json -events examples/events.json
```

事件文件中的数组顺序即到达顺序。输出为 JSON：`ledger`（全部流水，
含追补/追回）和 `summary`（总支付、每笔净额、各限额用量）。

`examples/` 中的示例演示了：等待期拒赔（c3）、免赔额、单次限额、
医疗共享限额，以及迟到事件 c4（事故日期 2025-02-15 但晚到）抢占
共享限额后对 c1、c2 的追回调整。
