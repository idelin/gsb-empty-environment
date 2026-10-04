# mealsolver — 五日餐单求解器

纯 Go（仅标准库）实现的餐单优化器：在满足**每日**营养整数区间、过敏禁用、
连续重复限制与菜品总数上限的前提下，最小化总成本。无解时给出一个极小
冲突约束子集（IIS）。

## 约束语义

- **每日营养区间**：每种营养素每天的总摄入必须落在 `[min, max]` 整数区间内，
  逐日独立判定，**不用多日平均值替代单日达标**。
- **份量档位**：每道菜可定义多个档位（`tiers`），各自携带营养与成本。
- **过敏禁用**：`config.allergens` 命中的菜品整道排除。
- **连续重复限制**：`maxConsecutiveRepeat` 为同一道菜允许连续出现的最长天数
  （`1` 表示相邻两天不能出现同一道菜）；`<=0` 表示不限制。
- **菜品总数上限**：`maxDishes` 限制全程使用的不同菜品数量，默认 20。
- **目标**：所有天、所有餐位的档位成本之和最小。

## 用法

```sh
go run ./cmd/mealsolver example.json     # 或: mealsolver < input.json
```

输入 JSON：`{"config": {...}, "dishes": [...]}`，字段见 `types.go` 与
`example.json`。输出为 `Result`：

- `status: "optimal"`：`choices` 给出每天每餐的菜品与档位，
  `dailyTotals` 给出每日各营养素总量，`totalCost` 为最小成本。
- `status: "infeasible"`：`conflicts` 列出极小冲突约束子集——整体不可解，
  但去掉其中任意一条即可解（删除过滤法，粒度为“第 d 天某营养素上/下限”、
  单条过敏禁用、连续重复限制、菜品总数上限）。
- `status: "error"`：输入非法或单日可行方案数超过枚举上限。

## 作为库使用

```go
res := mealsolver.Solve(config, dishes)
if res.Status == mealsolver.StatusInfeasible {
    // res.Conflicts 为冲突约束子集
}
```

## 算法

1. 过滤过敏与无档位菜品；对每一天枚举满足当日区间的所有餐位分配
   （餐位按多重集处理，带部分和剪枝）。
2. 跨天分支定界：以下界“已花成本 + 剩余各天最低成本”剪枝，
  同时维护连续出现天数与已用菜品集合。
3. 无解时对约束做删除过滤，输出极小冲突子集。

## 测试

```sh
go test ./...
```
