# 二维容量 + 互斥组背包求解器

纯 Python 3.11 标准库实现，不调用任何外部求解器。每种物品消耗两种资源、
可属于一个互斥组（每组至多选一个），在两种容量限制下选择总收益最大的子集，
并给出每件未选物品的原因。

## 模型与规则

- 收益与容量为整数；物品权重允许整数或有理数（`Fraction`、`"p/q"` 字符串、
  `(num, den)` 元组），内部按分母最小公倍数统一放大为整数后精确计算。
- 两种容量必须同时满足，只满足一种不算可行。
- 零成本正收益物品会被选中；负收益物品自动不选。
- 互斥组内至多选择一件。

## 算法

按互斥组分组的多选动态规划：状态为 `(已用容量1, 已用容量2) -> (最大收益, 选择)`，
每组取“不选”或“恰好选一个”；每轮用树状数组做二维帕累托剪枝
（两种容量都不更大且收益不更低的状态被丢弃）。

## 结果状态（三种分开）

| 状态 | 含义 |
| --- | --- |
| `Status.OPTIMAL` | 预算内完成全部 DP，解被证明最优 |
| `Status.FEASIBLE` | 状态数预算耗尽，返回当前最优可行解，最优性未决 |
| `Status.UNDECIDED` | 预算连初始状态都容不下，无法给出可信结论 |

## 用法

```python
from knapsack2d import Item, solve

items = [
    Item("A", profit=6, weight1=4, weight2=3),
    Item("D", profit=4, weight1="1/2", weight2=2),   # 有理数权重
    Item("G1", profit=8, weight1=6, weight2=4, group="g"),
    Item("G2", profit=9, weight1=7, weight2=5, group="g"),  # 与 G1 互斥
]
res = solve(items, 10, 10, state_budget=100_000)
print(res.status, res.total_profit, res.selected)
print(res.unselected_reasons)  # 每件未选物品的原因
```

## 运行样例（`python3.11 demo.py`）

```
== 样例 1：容量 (10, 10)，精确求解 ==
状态: optimal
总收益: 22  已用容量: (19/2, 10) / (10, 10)
已选: A, B, D, E, M2
未选原因:
  - C: 剩余容量不足（资源1、资源2）
  - F: 收益非正，不选更优
  - G1: 剩余容量不足（资源1、资源2）
  - G2: 剩余容量不足（资源1、资源2）
  - M1: 互斥组 'm' 中已选择 'M2'
  - H: 剩余容量不足（资源1、资源2）
DP 状态数: 65（预算 100000）

== 样例 2：24 件物品但状态预算仅 50（最优性未决） ==
状态: feasible
总收益: 60  已用容量: (27, 29) / (40, 30)
已选: X00, X01, X03, X05, X06, X07, X08, X09
未选原因:（略，含“剩余容量不足”与“最优性权衡”两类）
DP 状态数: 138（预算 50）

== 样例 3：状态预算为 0（无法决断） ==
状态: undecided
预算不足，无法给出可信结论
```

## 测试结果（`python3.11 -m unittest -v`）

```
test_both_capacities_must_hold ... ok        # 只满足一种容量不可行
test_budget_exhausted_is_feasible_not_optimal ... ok   # 预算耗尽 -> FEASIBLE
test_empty_item_list ... ok
test_invalid_input_rejected ... ok
test_matches_brute_force_on_random_instances ... ok    # 40 组随机实例对拍暴力枚举
test_mutex_group_at_most_one ... ok          # 互斥组至多一个
test_negative_profit_skipped ... ok          # 负收益不选
test_rational_weights ... ok                 # 有理数权重
test_zero_budget_is_undecided ... ok         # 零预算 -> UNDECIDED
test_zero_cost_positive_profit_selected ... ok         # 零成本正收益可选

Ran 10 tests in 0.02s
OK
```

## 文件

- `knapsack2d.py` — 求解器（`Item` / `solve` / `Status` / `SolveResult`）
- `demo.py` — 运行样例
- `test_knapsack2d.py` — unittest 测试（含暴力枚举对拍）
