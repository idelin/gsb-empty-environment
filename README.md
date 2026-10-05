# mealsolver — 五日餐单求解器

给定菜品库（每道菜可指定多个份量档位），求解满足以下全部约束的 5 日餐单，并**最小化总成本**：

- **每日营养整数区间**：每种营养素每天的总摄入量必须落在 `[min, max]` 内。
  约束按天逐日硬校验，**绝不**用多日平均达标来替代单日达标。
- **过敏禁用**：含禁用过敏原的菜品整道排除。
- **连续重复限制**：同一道菜最多连续出现 `maxConsecutiveDays` 天。
- **总量限制**：全程菜品总份数不超过 `maxTotalServings`（默认 20）。
- **份量档位**：每道菜可声明 `tiers`（如 `[0.5, 1.0, 1.5]`），营养与成本按档位线性缩放。

**无解时**输出最小冲突约束子集（IIS，不可约不可行子集）：子集本身不可行，
但再放宽其中任意一条即可恢复可行，帮助定位是哪些约束互相打架。

## 用法

```sh
go run ./cmd/mealsolver -config examples/feasible.json    # 输出最优餐单
go run ./cmd/mealsolver -config examples/infeasible.json  # 输出冲突约束子集
go test ./solver/                                         # 运行测试
```

## 配置格式（JSON）

```jsonc
{
  "days": 5,                        // 默认 5
  "nutrients": {                    // 每日整数区间
    "protein": { "min": 50, "max": 120 }
  },
  "bannedAllergens": ["peanut"],    // 禁用过敏原
  "maxConsecutiveDays": 2,          // 连续重复限制，0 = 不限
  "maxServingsPerDay": 5,           // 单日份数上限，0 = 不限
  "maxTotalServings": 20,           // 全程总份数上限，默认 20
  "maxPerDishPerDay": 2,            // 单菜单日份数上限，默认 2
  "dishes": [
    {
      "name": "鸡胸肉沙拉",
      "unitCost": 12,               // 1.0 档位的成本
      "tiers": [0.5, 1.0, 1.5],     // 份量档位，默认 [1.0]
      "nutrition": { "protein": 25 },  // 1.0 档位的营养
      "allergens": [],
      "maxTimes": 3                 // 全程出现次数上限，0 = 不限
    }
  ]
}
```

## 求解方法

1. **日方案枚举**：递归枚举单日内所有满足营养区间与单日份数上限的
   「菜品 × 份数 × 档位」组合（营养超上限即剪枝），按成本升序排序。
2. **跨天分支定界**：按天深度优先选择日方案，维护每道菜的最后出现天、
   连续天数与累计次数以校验连续重复限制和 `maxTimes`；用
   「当前成本 + 剩余天数 × 最低日成本」做下界剪枝，保证全局最优。
3. **冲突子集（删除过滤法）**：把每条约束（每个营养素的每日上/下限、
   每条过敏禁用、连续重复限制、总量/单日限量）视为可放松项；逐条试放松，
   若放松后仍不可行则永久移除该约束。最终剩下的集合即为最小冲突子集。

## 作为库使用

```go
plan, err := solver.Solve(cfg)          // err == solver.ErrInfeasible 表示无解
subset, err := solver.FindConflictSubset(cfg) // 无解时的最小冲突约束子集
ok, err := solver.Feasible(cfg)         // 仅判可行性
```
