# 保质期配货规划器 (shelflife-planner)

给定未来 N 天（通常 7 天）的需求、批次库存与到期日、每日有限调拨额度，
求满足需求的配货计划，目标按字典序优化：

1. **满足需求最多**（不可满足需求最少）
2. **报废最少**（规划期内到期而未售出的库存最少）
3. **调拨成本最小**

约束：**到期当日禁售**——到期日为 `expiry` 的批次只能用于第 `t < expiry` 天；
`expiry > days` 表示规划期内不过期。批次规模上限 10 个。

## 算法

最小费用最大流（SSP + SPFA）：

```
源 -> 批次   容量=库存, 费用=0
批次 -> 天   仅当 天 < expiry, 容量=库存, 费用=单位调拨成本 - 报废避免奖励*
天 -> 汇     容量=min(当日需求, 当日调拨额度), 费用=0
```

\* 仅对规划期内会到期的批次施加奖励（大权重），保证在最大流中优先消耗临期库存。
先保证流量最大（需求满足最多），再在最大流中最小化费用（报废最少 → 成本最小）。

## 用法

```sh
go run ./cmd/planner sample.json        # 文本输出
go run ./cmd/planner -json sample.json  # JSON 输出
cat sample.json | go run ./cmd/planner  # 标准输入
go test ./...                           # 运行测试
```

## 输入格式

```json
{
  "days": 7,
  "demand": [30, 25, 40, 35, 20, 45, 30],
  "quota":  [50, 50, 50, 50, 50, 50, 50],
  "batches": [
    {"id": "B1", "qty": 40, "expiry": 2, "cost": 3}
  ]
}
```

- `demand` / `quota`：长度 = `days`，每日需求 / 每日调拨额度上限
- `batches[].qty`：批次库存（≤ 2^20）
- `batches[].expiry`：到期日（天序号，1 起；到期当日禁售）
- `batches[].cost`：单位调拨成本（< 2^15）

## 输出

- `flows`：每日流向（天、批次、数量）
- `unmet` / `total_unmet`：每日及合计的不可满足需求
- `waste` / `total_waste`：报废明细与合计
- `total_cost`：总调拨成本
