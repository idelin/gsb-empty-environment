# 保障结算模拟器 (settlement-sim)

按输入规则（JSON）处理保障区间、等待期、免赔额与累计限额的理赔结算
模拟器。**不引用任何真实保险条款**，全部语义由输入配置定义。金额一律
使用整数“分”（`*_cents`），避免浮点误差。

## 运行

```sh
go run ./cmd/sim examples/input.json
go test ./...
```

存在限额违规时 CLI 以退出码 1 结束。

## 输入模型

- `policy.coverages[]`：保障责任
  - `window`：保障区间（起止均为闭区间）
  - `waiting_days`：等待期天数，区间起算日 + N 天之前就诊不赔付
  - `deductible_cents`：区间内累计免赔额，按结算顺序消耗
  - `per_event_limit_cents`：单次事件限额（0 = 不限）
  - `aggregate_limit_cents`：责任累计限额（0 = 不限）
  - `shared_limit_group`：可选，指向共享限额组
- `policy.shared_limits[]`：跨责任共享的累计限额组
- `events[]`：理赔事件，`seq` 为到达（报案）顺序，`service_date` 为
  就诊日期，二者无关——事件可以迟到。

## 结算语义

- **事件排序**：每次结算按（就诊日期, 到达序号, 事件 ID）排序，保证
  完全确定；同一就诊日期按到达顺序结算。
- **扣减顺序**（固定）：保障区间校验 → 等待期校验 → 单次限额 →
  免赔额 → 责任累计限额 → 共享限额。每一步扣减都会记录原因
  （`OUT_OF_WINDOW` / `IN_WAITING_PERIOD` / `PER_EVENT_LIMIT` /
  `DEDUCTIBLE` / `AGGREGATE_LIMIT` / `SHARED_LIMIT`）及金额。
- **迟到事件**：事件按 `seq` 逐个进入引擎；每次到达都对全部已到达
  事件做确定性重算，与上一轮结果 diff——应付上调生成**追补**记录，
  下调生成**追回**记录。新事件的首轮结算不算调整。
- **不变量校验**：每次输出前校验——单笔应付 ∈ [0, 索赔额] 且不超
  单次限额；每笔“索赔额 − 扣减合计 = 应付”；每个责任合计不超累计
  限额；每个共享组合计不超共享限额。任何违反都会出现在
  `violations` 中。

## 输出

`Report` 包含：

- `settlements`：每笔事件的应付金额与全部扣减原因
- `adjustments`：迟到事件引发的追补/追回记录
- `totals_paid_cents` / `shared_totals_cents`：按责任 / 共享组的已付合计
- `violations`：限额不变量校验结果（正常应为空）

## 代码结构

- `sim/types.go`：输入规则、事件、结算与调整记录等类型
- `sim/engine.go`：结算引擎（确定性重算 + diff 调整 + 不变量校验）
- `sim/engine_test.go`：规则与迟到事件场景测试
- `cmd/sim/main.go`：CLI 入口
- `examples/input.json`：示例输入
