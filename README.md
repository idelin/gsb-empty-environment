# 审计抽样覆盖优化

在检查预算约束下选择检查子集，使被**充分覆盖**的风险场景权重之和最大；
其次最小化总成本；最后使所选检查 ID 排序后的元组字典序最小。

- 每个风险场景要求至少 `k` 个来自**不同检查家族**的证据才算充分覆盖。
- 同一家族的多个检查对同一场景只计一个证据家族，不能虚增覆盖次数。
- 每个检查有成本 `cost` 与执行工时 `hours`；总成本受 `budget` 约束，
  可选地总工时受 `hours_budget` 约束。

## 运行环境

Python 3.11（仅标准库，无第三方依赖）。

## 命令行用法

```bash
python3 audit_coverage.py input.json     # 从文件读取
cat input.json | python3 audit_coverage.py   # 或从标准输入读取（也可用 '-'）
python3 -m unittest -v                   # 运行测试
```

## 输入格式（JSON）

```json
{
  "budget": 10,
  "hours_budget": 20,
  "scenarios": [
    {"id": "R1", "k": 2, "weight": 10}
  ],
  "checks": [
    {"id": "a", "family": "F1", "cost": 3, "hours": 2, "covers": ["R1"]}
  ]
}
```

| 字段 | 说明 | 缺省 |
| --- | --- | --- |
| `budget` | 总成本上限（必填） | — |
| `hours_budget` | 总工时上限 | 不限 |
| `scenarios[].id` | 场景 ID（唯一，必填） | — |
| `scenarios[].k` | 充分覆盖所需的不同家族数 | 1 |
| `scenarios[].weight` | 场景权重 | 1 |
| `checks[].id` | 检查 ID（唯一，必填） | — |
| `checks[].family` | 检查所属家族（必填） | — |
| `checks[].cost` | 检查成本（必填） | — |
| `checks[].hours` | 执行工时 | 0 |
| `checks[].covers` | 能提供证据的场景 ID 列表 | [] |

## 输出格式（JSON，写至标准输出）

```json
{
  "selected_checks": ["a", "c"],
  "total_cost": 2,
  "total_hours": 2,
  "covered_weight": 10,
  "total_weight": 10,
  "scenarios": {
    "R1": {
      "covered": true,
      "required_families": 2,
      "evidence_families": ["F1", "F2"]
    }
  }
}
```

- `selected_checks`：所选检查 ID（升序）。
- `scenarios.<id>.evidence_families`：所选检查为该场景实际提供的证据家族集合。
- `scenarios.<id>.covered`：`len(evidence_families) >= k` 时为 `true`。

## 求解方法与最优性

采用精确分支定界枚举检查子集：以"当前已覆盖权重 + 仍可能被充分覆盖的
场景权重"作为乐观上界进行安全剪枝，并对三个目标按字典序比较保留最优解。
因此**返回结果保证为精确最优解**（已由随机小实例与暴力枚举交叉验证）。

该问题是预算约束最大覆盖问题的推广，属 NP-hard；求解时间随检查数量
指数增长，适用于中小规模实例（数十个检查量级）。更大规模实例需要
启发式或整数规划求解器，届时最优性将不再有保障。

## 示例

`example.json` 为合成数据：场景 `R` 要求 2 个不同家族，检查 `a`、`b`
同属家族 `F1`，`c` 属于家族 `F2`。仅 `a`+`b` 不构成充分覆盖，
必须加入 `c`；最优解为 `a`+`c`（成本 2，字典序最小）。

```bash
python3 audit_coverage.py example.json
```
