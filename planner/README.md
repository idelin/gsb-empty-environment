# 保质期配货规划器

接收未来 7 天需求、批次库存与到期时间、每日调拨额度，输出满足以下
字典序目标的每日调拨计划：

1. **满足需求最多**（不可满足需求最少）
2. **报废最少**（规划期内过期未售出的库存最少）
3. **调拨成本最低**

约束：批次数量 ≤ 10；到期当日禁售——到期日为 `e` 的批次只能用于
第 `1 .. e-1` 天；每日总调拨量不超过当日额度。

## 建模

最小费用最大流，用大 M 分层权重保证三目标字典序：

```
源 → 批次(容量=库存) → 可售日(费用=调拨成本-BIG) → 汇(容量=min(需求, 日额度))
批次 → 汇：剩余边，期内过期批次费用=MID(报废惩罚)，否则 0
```

`BIG >> MID >> 最大调拨成本`，因此最大流最小费用解即字典序最优。
到期当日禁售通过"批次只向早于到期日的日期连边"实现。

## 使用

```sh
go run . [-pretty] [input.json]   # 不带文件参数时从 stdin 读取
go test ./...
```

## 输入

```json
{
  "days": 7,
  "demand": [30, 25, 20, 20, 15, 15, 10],
  "transferCapacity": [40, 40, 35, 35, 30, 30, 30],
  "batches": [
    {"id": "B1", "quantity": 40, "expiryDay": 3, "transferCost": 2}
  ]
}
```

- `days`：规划天数，缺省 7
- `demand`：每日需求，长度 = days
- `transferCapacity`：每日调拨额度，缺省不限
- `batches[].expiryDay`：到期日（1 起计），当日禁售；> days 表示期内不过期
- `batches[].transferCost`：每单位调拨成本

## 输出

- `flows`：每日从各批次调拨的数量（每日流向）
- `unmetDemand`：每日不可满足需求
- `waste`：期内过期批次的报废量
- `remaining`：期内未过期批次的剩余量（不计报废）
- `totalSatisfied / totalUnmet / totalWaste / totalTransferCost`：汇总指标
